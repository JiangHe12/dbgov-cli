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
func runExport(f *cliFlags, opts exportOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := dbgaudit.New(dbgaudit.EventTypeExport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "export"))
		event.Risk = "R0"
		emitAudit(f, event, err)
		return err
	}
	result, err := dumpSchema(commandContext(f), b, current, opts.dir)
	event := dbgaudit.New(dbgaudit.EventTypeExport, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "export"))
	event.Risk = "R0"
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaDump(f, meta, result)
}

func runImport(f *cliFlags, opts importOptions) error {
	desired, err := schema.LoadDesiredDir(opts.dir)
	if err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := newImportAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildSchemaPlan(b, current, desired)
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
	snapshotID, err := captureSchemaSnapshot(f, b, current, meta, "import")
	if err != nil {
		event := newImportAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}

	statements := schemaPlanStatements(plan)
	executed, err := b.ExecDDL(commandContext(f), statements)
	event := newImportAuditEvent(f, meta, plan)
	event.SnapshotID = snapshotID
	event.Executed = executed
	if err != nil && executed < len(statements) {
		event.FailedStatement = statements[executed]
	}
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaPlan(f, meta, targetWrite, plan)
}

func runReconcile(f *cliFlags, opts reconcileOptions) error {
	desired, err := schema.LoadDesiredDir(opts.dir)
	if err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := newReconcileAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	diff := schema.Diff(current, desired)
	extra := schema.ExtraTables(current, desired)
	if opts.prune {
		diff.Changes = append(diff.Changes, schema.PruneChanges(current, desired)...)
	} else {
		for _, table := range extra {
			diff.Warnings = append(diff.Warnings, fmt.Sprintf("drift: table %s exists in database but not in desired schema; not pruned (use --prune)", table))
		}
	}
	plan, err := buildSchemaPlanFromDiff(b, diff)
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

	requiredAllows, granted := reconcileAllowFlags(plan, opts)
	if err := authorizeWrite(f, safetyRisk(plan.OverallRisk), meta, requiredAllows, granted); err != nil {
		event := newReconcileAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	snapshotID, err := captureSchemaSnapshot(f, b, current, meta, "reconcile")
	if err != nil {
		event := newReconcileAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}

	statements := schemaPlanStatements(plan)
	executed, err := b.ExecDDL(commandContext(f), statements)
	event := newReconcileAuditEvent(f, meta, plan)
	event.SnapshotID = snapshotID
	event.Executed = executed
	if err != nil && executed < len(statements) {
		event.FailedStatement = statements[executed]
	}
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaPlan(f, meta, targetWrite, plan)
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
		if stmt.Action == schema.ActionDropColumn || stmt.Action == schema.ActionModifyColumn {
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
