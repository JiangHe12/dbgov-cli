package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	dbgsnapshot "github.com/JiangHe12/dbgov-cli/internal/snapshot"
)

const (
	rollbackDataLossWarning = "structure-level restore only; data in dropped tables/columns is NOT recovered"
	rollbackScope           = "schema-structure"
)

type rollbackListResult struct {
	Snapshots []dbgsnapshot.Meta `json:"snapshots"`
}

type rollbackResult struct {
	SnapshotID        string   `json:"snapshotId"`
	Scope             string   `json:"scope"`
	PlannedStatements int      `json:"plannedStatements"`
	AppliedStatements int      `json:"appliedStatements"`
	DataRestored      bool     `json:"dataRestored"`
	PlanFingerprint   string   `json:"planFingerprint"`
	TargetFingerprint string   `json:"targetFingerprint"`
	Warnings          []string `json:"warnings,omitempty"`
}

type rollbackExecutionBinding struct {
	PlanFingerprint   string   `json:"planFingerprint"`
	TargetFingerprint string   `json:"targetFingerprint"`
	Statements        []string `json:"statements"`
}

type rollbackOptions struct {
	to                   string
	fake                 bool
	dryRun               bool
	allowDestructive     bool
	allowProductionPrune bool
}

func newRollbackCmd(f *cliFlags) *cobra.Command {
	var opts rollbackOptions
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Inspect schema rollback snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.to == "" {
				return apperrors.New(apperrors.CodeUsageError, "rollback requires --to or a subcommand", nil)
			}
			return runRollbackTo(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.to, "to", "", "Snapshot ID to restore")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print the plan without executing DDL")
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "Allow destructive column changes")
	cmd.Flags().BoolVar(&opts.allowProductionPrune, "allow-production-prune", false, "Allow pruning extra database tables")
	cmd.AddCommand(rollbackListCmd(f))
	return cmd
}

func rollbackListCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List schema snapshots",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollbackList(f)
		},
	}
}

func runRollbackList(f *cliFlags) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	baseDir, err := snapshotBaseDir()
	if err != nil {
		event := rollbackListAuditEvent(f)
		emitAudit(f, event, err)
		return err
	}
	if err := validateSnapshotEvidenceDirectory(baseDir, true); err != nil {
		event := rollbackListAuditEvent(f)
		emitAudit(f, event, err)
		return err
	}
	metas, err := dbgsnapshot.List(baseDir)
	event := rollbackListAuditEvent(f)
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printRollbackList(f, rollbackListResult{Snapshots: metas})
}

func runRollbackTo(f *cliFlags, opts rollbackOptions) (resultErr error) { //nolint:gocyclo // Target binding, authorization, intent, snapshot, execution, and outcome form one governed flow.
	baseDir, err := snapshotBaseDir()
	if err != nil {
		event := rollbackAuditEvent(f, contextMeta{}, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	if err := validateSnapshotEvidenceDirectory(baseDir, false); err != nil {
		event := rollbackAuditEvent(f, contextMeta{}, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	snap, err := dbgsnapshot.Load(baseDir, opts.to)
	if err != nil {
		event := rollbackAuditEvent(f, contextMeta{}, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(b, &resultErr, backendCloseMutation)
	if err := validateRollbackSnapshotTarget(snap.Meta.Target, meta); err != nil {
		event := rollbackAuditEvent(f, meta, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	desired, err := schema.SchemaFromDDLMap(snap.Tables)
	if err != nil {
		event := rollbackAuditEvent(f, meta, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := rollbackAuditEvent(f, meta, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, roundTripCurrent, err := buildBoundRollbackPlan(f, b, current, desired, *snap.Meta.Target)
	if err != nil {
		event := rollbackAuditEvent(f, meta, opts.to, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := rollbackAuditEvent(f, meta, opts.to, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printSchemaPlan(f, meta, targetWrite, plan)
	}
	if len(plan.Statements) == 0 {
		emitAudit(f, rollbackAuditEvent(f, meta, opts.to, plan), nil)
		return printRollbackResult(f, meta, rollbackResult{
			SnapshotID:        opts.to,
			Scope:             rollbackScope,
			PlannedStatements: 0,
			AppliedStatements: 0,
			DataRestored:      false,
			PlanFingerprint:   plan.PlanFingerprint,
			TargetFingerprint: plan.TargetFingerprint,
			Warnings:          append([]string(nil), plan.Warnings...),
		})
	}

	requiredAllows, granted := planAllowFlags(plan, opts.allowDestructive, opts.allowProductionPrune)
	risk := safetyRisk(plan.OverallRisk)
	if err := authorizeWrite(f, risk, meta, requiredAllows, granted); err != nil {
		event := rollbackAuditEvent(f, meta, opts.to, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	statements := schemaPlanStatements(plan)
	if err := validateRollbackExecutionBinding(plan, *snap.Meta.Target, roundTripCurrent, desired, statements); err != nil {
		event := rollbackAuditEvent(f, meta, opts.to, plan)
		emitAudit(f, event, err)
		return err
	}
	binding := rollbackExecutionBinding{
		PlanFingerprint:   plan.PlanFingerprint,
		TargetFingerprint: plan.TargetFingerprint,
		Statements:        statements,
	}
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeRollback), binding)
	metadata.Items = len(statements)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeRollback),
		Event:    rollbackAuditEvent(f, meta, opts.to, plan),
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	snapshotID, err := prepareGitOpsExecutionSnapshot(f, b, meta, "rollback", plan, func(roundTripFresh schema.Schema) (schemaPlan, error) {
		return buildBoundRollbackPlanFromRoundTrip(b, roundTripFresh, desired, *snap.Meta.Target)
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
	return printRollbackResult(f, meta, rollbackResult{
		SnapshotID:        opts.to,
		Scope:             rollbackScope,
		PlannedStatements: len(statements),
		AppliedStatements: executed,
		DataRestored:      false,
		PlanFingerprint:   plan.PlanFingerprint,
		TargetFingerprint: plan.TargetFingerprint,
		Warnings:          append([]string(nil), plan.Warnings...),
	})
}

func buildBoundRollbackPlan(
	f *cliFlags,
	b schemaExecutionBackend,
	current schema.Schema,
	desired schema.Schema,
	target dbgsnapshot.Target,
) (schemaPlan, schema.Schema, error) {
	roundTripCurrent, err := schemaFromTableDDL(commandContext(f), b, current)
	if err != nil {
		return schemaPlan{}, schema.Schema{}, err
	}
	plan, err := buildBoundRollbackPlanFromRoundTrip(b, roundTripCurrent, desired, target)
	return plan, roundTripCurrent, err
}

func buildBoundRollbackPlanFromRoundTrip(
	b schemaPlanRenderer,
	roundTripCurrent schema.Schema,
	desired schema.Schema,
	target dbgsnapshot.Target,
) (schemaPlan, error) {
	diff := schema.Diff(roundTripCurrent, desired)
	diff.Changes = append(diff.Changes, schema.PruneChanges(roundTripCurrent, desired)...)
	plan, err := buildSchemaPlanFromDiff(b, diff)
	applyRollbackPlanMetadata(&plan)
	bindRollbackPlan(&plan, target, roundTripCurrent, desired)
	return plan, err
}

func rollbackListAuditEvent(f *cliFlags) dbgaudit.Event {
	return dbgaudit.New(dbgaudit.EventTypeRollback, currentOperator(f), dbgaudit.Context{}, dbgaudit.Target{ObjectType: "rollback", Object: "list"})
}

func rollbackAuditEvent(f *cliFlags, meta contextMeta, snapshotID string, plan schemaPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeRollback, currentOperator(f), dbgaudit.Context{}, dbgaudit.Target{ObjectType: "rollback", Object: snapshotID})
	event.Risk = effectiveRiskLabel(plan.OverallRisk, meta)
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	return event
}

func applyRollbackPlanMetadata(plan *schemaPlan) {
	if len(plan.Statements) == 0 {
		plan.OverallRisk = safetyRiskLabel(safety.R0)
		plan.RequiredAuthorization = requiredAuthorization(schemaRiskLabel(safety.R0))
		return
	}
	plan.Warnings = append([]string{rollbackDataLossWarning}, plan.Warnings...)
	risk := maxRisk(safetyRisk(plan.OverallRisk), safety.R2)
	plan.OverallRisk = safetyRiskLabel(risk)
	plan.RequiredAuthorization = requiredAuthorization(schemaRiskLabel(risk))
}

func validateRollbackSnapshotTarget(bound *dbgsnapshot.Target, meta contextMeta) error {
	if bound == nil {
		return apperrors.New(
			apperrors.CodeValidationFailed,
			"snapshot has no target binding and cannot be used for rollback",
			nil,
		)
	}
	actual := normalizedSnapshotTarget(*snapshotTarget(meta))
	if normalizedSnapshotTarget(*bound) != actual {
		return apperrors.New(
			apperrors.CodeConflict,
			"snapshot target does not match the selected database target",
			nil,
		)
	}
	return nil
}

func normalizedSnapshotTarget(target dbgsnapshot.Target) dbgsnapshot.Target {
	target.Context = strings.TrimSpace(target.Context)
	target.Engine = strings.ToLower(strings.TrimSpace(target.Engine))
	target.Host = strings.ToLower(strings.TrimSpace(target.Host))
	target.Database = strings.TrimSpace(target.Database)
	target.Schema = strings.TrimSpace(target.Schema)
	return target
}

func bindRollbackPlan(plan *schemaPlan, target dbgsnapshot.Target, current, desired schema.Schema) {
	targetData, _ := json.Marshal(normalizedSnapshotTarget(target))
	plan.TargetFingerprint, _ = dbgaudit.Fingerprint("snapshot-target", string(targetData))
	plan.PlannedStatements = len(plan.Statements)
	binding := schemaPlanFingerprintPayload{
		TargetFingerprint:     plan.TargetFingerprint,
		Current:               schemaForPlanBinding(current),
		Desired:               schemaForPlanBinding(desired),
		Statements:            plan.Statements,
		OverallRisk:           plan.OverallRisk,
		Destructive:           plan.Destructive,
		Warnings:              plan.Warnings,
		RequiredAuthorization: plan.RequiredAuthorization,
	}
	bindingData, _ := json.Marshal(binding)
	plan.PlanFingerprint, _ = dbgaudit.Fingerprint("rollback-plan", string(bindingData))
}

func validateRollbackExecutionBinding(
	plan schemaPlan,
	target dbgsnapshot.Target,
	current schema.Schema,
	desired schema.Schema,
	statements []string,
) error {
	recomputed := plan
	recomputed.Statements = append([]schemaPlanStatement(nil), plan.Statements...)
	bindRollbackPlan(&recomputed, target, current, desired)
	if plan.PlanFingerprint == "" ||
		plan.TargetFingerprint == "" ||
		plan.PlannedStatements != len(statements) ||
		!slices.Equal(schemaPlanStatements(plan), statements) ||
		recomputed.PlanFingerprint != plan.PlanFingerprint ||
		recomputed.TargetFingerprint != plan.TargetFingerprint {
		return apperrors.New(
			apperrors.CodeConflict,
			"rollback plan binding changed after preview; review and retry",
			nil,
		)
	}
	return nil
}

func maxRisk(left, right safety.Risk) safety.Risk {
	if left > right {
		return left
	}
	return right
}

func safetyRiskLabel(risk safety.Risk) string {
	switch risk {
	case safety.R1:
		return "R1"
	case safety.R2:
		return "R2"
	case safety.R3:
		return "R3"
	default:
		return "R0"
	}
}

func schemaRiskLabel(risk safety.Risk) schema.Risk {
	return schema.Risk(safetyRiskLabel(risk))
}

func printRollbackList(f *cliFlags, result rollbackListResult) error {
	if result.Snapshots == nil {
		result.Snapshots = []dbgsnapshot.Meta{}
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "RollbackSnapshotList", Data: result})
	}
	rows := make([][]string, 0, len(result.Snapshots))
	for _, meta := range result.Snapshots {
		rows = append(rows, []string{
			meta.ID,
			meta.Timestamp.Format("2006-01-02T15:04:05Z"),
			meta.Operator,
			meta.Command,
			meta.Ticket,
			fmt.Sprint(meta.TableCount),
		})
	}
	return p.Table([]string{"ID", "TIMESTAMP", "OPERATOR", "COMMAND", "TICKET", "TABLES"}, rows)
}

func printRollbackResult(f *cliFlags, meta contextMeta, result rollbackResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{
			Kind: "RollbackResult",
			Data: dataWithTarget(result, meta, targetWrite),
		})
	}
	if err := printTargetHeader(p, meta, targetWrite); err != nil {
		return err
	}
	if err := p.Table(
		[]string{"SNAPSHOT", "SCOPE", "PLANNED", "APPLIED", "DATA RESTORED"},
		[][]string{{
			result.SnapshotID,
			result.Scope,
			fmt.Sprint(result.PlannedStatements),
			fmt.Sprint(result.AppliedStatements),
			fmt.Sprint(result.DataRestored),
		}},
	); err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		if err := p.Warn(warning); err != nil {
			return err
		}
	}
	return nil
}
