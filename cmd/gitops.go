package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type exportOptions struct {
	dir  string
	fake bool
}

type importOptions struct {
	dir              string
	fake             bool
	dryRun           bool
	allowDestructive bool
}

type reconcileOptions struct {
	dir                  string
	fake                 bool
	prune                bool
	dryRun               bool
	allowDestructive     bool
	allowProductionPrune bool
}

//nolint:dupl // Export mirrors read-only schema commands but stays top-level by design.
func newExportCmd(f *cliFlags) *cobra.Command {
	var opts exportOptions
	cmd := &cobra.Command{
		Use:   "export --dir ./schema",
		Short: "Export current schema DDL to a directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.dir, "dir", "", "Directory to write schema SQL files")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	_ = cmd.MarkFlagRequired("dir")
	return cmd
}

func newImportCmd(f *cliFlags) *cobra.Command {
	var opts importOptions
	cmd := &cobra.Command{
		Use:   "import <schema-dir>",
		Short: "Import desired schema from a directory",
		Args:  requireExactArgs("import"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.dir = args[0]
			return runImport(f, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print the plan without executing DDL")
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "Allow destructive schema changes")
	return cmd
}

func newReconcileCmd(f *cliFlags) *cobra.Command {
	var opts reconcileOptions
	cmd := &cobra.Command{
		Use:   "reconcile <schema-dir>",
		Short: "Reconcile desired schema directory with the database",
		Args:  requireExactArgs("reconcile"),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.dir = args[0]
			return runReconcile(f, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	cmd.Flags().BoolVar(&opts.prune, "prune", false, "Drop database tables missing from desired schema")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print the plan without executing DDL")
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "Allow destructive column changes")
	cmd.Flags().BoolVar(&opts.allowProductionPrune, "allow-production-prune", false, "Allow pruning extra database tables")
	return cmd
}

//nolint:dupl // Export and schema dump share the same governed read/audit flow with different event types.
func runExport(f *cliFlags, opts exportOptions) (resultErr error) {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(b, &resultErr, backendCloseMutation)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := dbgaudit.New(dbgaudit.EventTypeExport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "export"))
		event.Risk = "R0"
		emitAudit(f, event, err)
		return err
	}
	event := dbgaudit.New(dbgaudit.EventTypeExport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "export"))
	event.Risk = "R0"
	result, err := collectSchemaDump(commandContext(f), b, current)
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	targetDir, err := canonicalLocalMutationDirectory(opts.dir)
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	relativePaths, err := schemaDumpRelativePaths(result)
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	if err := preflightPrivateMutationFiles(targetDir, relativePaths); err != nil {
		emitAudit(f, event, err)
		return err
	}
	event.Target = auditTarget(meta, "directory", targetDir)
	event.Risk = effectiveRiskLabel("R1", meta)
	if err := authorizeWrite(f, safety.R1, meta, nil, nil); err != nil {
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeExport), result.Tables)
	metadata.Items = len(result.Tables)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeExport),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	result, attempted, err := writeSchemaDump(targetDir, result)
	if auditErr := finishMutationAuditProgress(handle, metadata.Items, len(result.Files), attempted, err); auditErr != nil {
		return auditErr
	}
	return printSchemaDump(f, meta, result)
}

func runImport(f *cliFlags, opts importOptions) (resultErr error) {
	desired, err := schema.LoadDesiredDir(opts.dir)
	if err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(b, &resultErr, backendCloseMutation)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := newImportAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildBoundGitOpsSchemaPlan(f, b, meta, current, desired)
	if err != nil {
		event := newImportAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := newImportAuditEvent(f, meta, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printSchemaPlan(f, meta, targetWrite, plan)
	}
	if len(plan.Statements) == 0 {
		emitAudit(f, newImportAuditEvent(f, meta, plan), nil)
		return printSchemaPlan(f, meta, targetWrite, plan)
	}

	var requiredAllows []safety.AllowFlag
	granted := map[safety.AllowFlag]bool{}
	if plan.Destructive {
		requiredAllows = []safety.AllowFlag{safety.AllowDestructive}
		if opts.allowDestructive {
			granted[safety.AllowDestructive] = true
		}
	}
	if err := authorizeWrite(f, safetyRisk(plan.OverallRisk), meta, requiredAllows, granted); err != nil {
		event := newImportAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	statements := schemaPlanStatements(plan)
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeImport), schemaPlanExecutionBinding(plan))
	metadata.Items = len(statements)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeImport),
		Event:    newImportAuditEvent(f, meta, plan),
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	snapshotID, err := prepareGitOpsExecutionSnapshot(f, b, meta, "import", plan, func(roundTripFresh schema.Schema) (schemaPlan, error) {
		return buildBoundSchemaPlan(b, meta, roundTripFresh, desired)
	})
	if err != nil {
		return finishSkippedMutationAudit(handle, len(statements), err)
	}

	executed, err := b.ExecDDL(commandContext(f), statements)
	handle.spec.Event.SnapshotID = snapshotID
	if err != nil && executed < len(statements) {
		handle.spec.Event.FailedStatement = statements[executed]
	}
	if auditErr := finishDDLMutationAudit(handle, len(statements), executed, err); auditErr != nil {
		return auditErr
	}
	return printSchemaPlan(f, meta, targetWrite, plan)
}

func runReconcile(f *cliFlags, opts reconcileOptions) (resultErr error) {
	desired, err := schema.LoadDesiredDir(opts.dir)
	if err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(b, &resultErr, backendCloseMutation)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := newReconcileAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildBoundGitOpsReconcilePlan(f, b, meta, current, desired, opts.prune)
	if err != nil {
		event := newReconcileAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := newReconcileAuditEvent(f, meta, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printSchemaPlan(f, meta, targetWrite, plan)
	}
	if len(plan.Statements) == 0 {
		emitAudit(f, newReconcileAuditEvent(f, meta, plan), nil)
		return printSchemaPlan(f, meta, targetWrite, plan)
	}

	requiredAllows, granted := reconcileAllowFlags(plan, opts)
	if err := authorizeWrite(f, safetyRisk(plan.OverallRisk), meta, requiredAllows, granted); err != nil {
		event := newReconcileAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	statements := schemaPlanStatements(plan)
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeReconcile), schemaPlanExecutionBinding(plan))
	metadata.Items = len(statements)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeReconcile),
		Event:    newReconcileAuditEvent(f, meta, plan),
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	snapshotID, err := prepareGitOpsExecutionSnapshot(f, b, meta, "reconcile", plan, func(roundTripFresh schema.Schema) (schemaPlan, error) {
		return buildBoundReconcilePlan(b, meta, roundTripFresh, desired, opts.prune)
	})
	if err != nil {
		return finishSkippedMutationAudit(handle, len(statements), err)
	}

	executed, err := b.ExecDDL(commandContext(f), statements)
	handle.spec.Event.SnapshotID = snapshotID
	if err != nil && executed < len(statements) {
		handle.spec.Event.FailedStatement = statements[executed]
	}
	if auditErr := finishDDLMutationAudit(handle, len(statements), executed, err); auditErr != nil {
		return auditErr
	}
	return printSchemaPlan(f, meta, targetWrite, plan)
}

func buildBoundGitOpsSchemaPlan(
	f *cliFlags,
	b schemaExecutionBackend,
	meta contextMeta,
	current schema.Schema,
	desired schema.Schema,
) (schemaPlan, error) {
	roundTripCurrent, err := schemaFromTableDDL(commandContext(f), b, current)
	if err != nil {
		return schemaPlan{}, err
	}
	return buildBoundSchemaPlan(b, meta, roundTripCurrent, desired)
}

func buildBoundGitOpsReconcilePlan(
	f *cliFlags,
	b schemaExecutionBackend,
	meta contextMeta,
	current schema.Schema,
	desired schema.Schema,
	prune bool,
) (schemaPlan, error) {
	roundTripCurrent, err := schemaFromTableDDL(commandContext(f), b, current)
	if err != nil {
		return schemaPlan{}, err
	}
	return buildBoundReconcilePlan(b, meta, roundTripCurrent, desired, prune)
}

func buildBoundReconcilePlan(
	b schemaPlanRenderer,
	meta contextMeta,
	current schema.Schema,
	desired schema.Schema,
	prune bool,
) (schemaPlan, error) {
	diff := schema.Diff(current, desired)
	extra := schema.ExtraTables(current, desired)
	if prune {
		diff.Changes = append(diff.Changes, schema.PruneChanges(current, desired)...)
	} else {
		for _, table := range extra {
			diff.Warnings = append(diff.Warnings, fmt.Sprintf("drift: table %s exists in database but not in desired schema; not pruned (use --prune)", table))
		}
	}
	plan, err := buildSchemaPlanFromDiff(b, diff)
	bindSchemaPlan(&plan, meta, current, desired)
	return plan, err
}

func newImportAuditEvent(f *cliFlags, meta contextMeta, plan schemaPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeImport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "import"))
	event.Risk = effectiveRiskLabel(plan.OverallRisk, meta)
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	return event
}

func newReconcileAuditEvent(f *cliFlags, meta contextMeta, plan schemaPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeReconcile, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "reconcile"))
	event.Risk = effectiveRiskLabel(plan.OverallRisk, meta)
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	return event
}

func reconcileAllowFlags(plan schemaPlan, opts reconcileOptions) ([]safety.AllowFlag, map[safety.AllowFlag]bool) {
	return planAllowFlags(plan, opts.allowDestructive, opts.allowProductionPrune)
}

func planAllowFlags(plan schemaPlan, allowDestructive, allowProductionPrune bool) ([]safety.AllowFlag, map[safety.AllowFlag]bool) {
	var required []safety.AllowFlag
	granted := map[safety.AllowFlag]bool{}
	if planRequiresDestructiveAllow(plan) {
		required = append(required, safety.AllowDestructive)
		if allowDestructive {
			granted[safety.AllowDestructive] = true
		}
	}
	if planRequiresPruneAllow(plan) {
		required = append(required, safety.AllowProductionPrune)
		if allowProductionPrune {
			granted[safety.AllowProductionPrune] = true
		}
	}
	return required, granted
}

func planRequiresDestructiveAllow(plan schemaPlan) bool {
	for _, stmt := range plan.Statements {
		if stmt.Action == schema.ActionDropColumn ||
			stmt.Action == schema.ActionModifyColumn ||
			stmt.Opaque {
			return true
		}
	}
	return false
}

func planRequiresPruneAllow(plan schemaPlan) bool {
	for _, stmt := range plan.Statements {
		if stmt.Action == schema.ActionDropTable {
			return true
		}
	}
	return false
}
