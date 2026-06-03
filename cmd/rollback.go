package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	dbgsnapshot "github.com/JiangHe12/dbgov-cli/internal/snapshot"
	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/printer"
)

const rollbackDataLossWarning = "structure-level restore only; data in dropped tables/columns is NOT recovered"

type rollbackListResult struct {
	Snapshots []dbgsnapshot.Meta `json:"snapshots"`
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
	metas, err := dbgsnapshot.List(baseDir)
	event := rollbackListAuditEvent(f)
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printRollbackList(f, rollbackListResult{Snapshots: metas})
}

func runRollbackTo(f *cliFlags, opts rollbackOptions) error {
	baseDir, err := snapshotBaseDir()
	if err != nil {
		event := rollbackAuditEvent(f, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	snap, err := dbgsnapshot.Load(baseDir, opts.to)
	if err != nil {
		event := rollbackAuditEvent(f, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	desired, err := schema.SchemaFromDDLMap(snap.Tables)
	if err != nil {
		event := rollbackAuditEvent(f, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := rollbackAuditEvent(f, opts.to, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	diff := schema.Diff(current, desired)
	diff.Changes = append(diff.Changes, schema.PruneChanges(current, desired)...)
	plan, err := buildSchemaPlanFromDiff(b, diff)
	applyRollbackPlanMetadata(&plan)
	if err != nil {
		event := rollbackAuditEvent(f, opts.to, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := rollbackAuditEvent(f, opts.to, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printSchemaPlan(f, plan)
	}

	requiredAllows, granted := planAllowFlags(plan, opts.allowDestructive, opts.allowProductionPrune)
	risk := safetyRisk(plan.OverallRisk)
	if err := authorizeWrite(f, risk, meta, requiredAllows, granted); err != nil {
		event := rollbackAuditEvent(f, opts.to, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	snapshotID, err := captureSchemaSnapshot(f, b, current, meta, "rollback")
	if err != nil {
		event := rollbackAuditEvent(f, opts.to, plan)
		emitAudit(f, event, err)
		return err
	}

	statements := schemaPlanStatements(plan)
	executed, err := b.ExecDDL(commandContext(f), statements)
	event := rollbackAuditEvent(f, opts.to, plan)
	event.SnapshotID = snapshotID
	event.Executed = executed
	if err != nil && executed < len(statements) {
		event.FailedStatement = statements[executed]
	}
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaPlan(f, plan)
}

func rollbackListAuditEvent(f *cliFlags) dbgaudit.Event {
	return dbgaudit.New(dbgaudit.EventTypeRollback, currentOperator(f), dbgaudit.Context{}, dbgaudit.Target{ObjectType: "rollback", Object: "list"})
}

func rollbackAuditEvent(f *cliFlags, snapshotID string, plan schemaPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeRollback, currentOperator(f), dbgaudit.Context{}, dbgaudit.Target{ObjectType: "rollback", Object: snapshotID})
	event.Risk = plan.OverallRisk
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	return event
}

func applyRollbackPlanMetadata(plan *schemaPlan) {
	plan.Warnings = append([]string{rollbackDataLossWarning}, plan.Warnings...)
	risk := maxRisk(safetyRisk(plan.OverallRisk), safety.R2)
	plan.OverallRisk = safetyRiskLabel(risk)
	plan.RequiredAuthorization = requiredAuthorization(schemaRiskLabel(risk))
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
	p.Table([]string{"ID", "TIMESTAMP", "OPERATOR", "COMMAND", "TICKET", "TABLES"}, rows)
	return nil
}
