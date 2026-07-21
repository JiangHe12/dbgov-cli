package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	"github.com/JiangHe12/dbgov-cli/internal/sqlclass"
)

const defaultImpactThreshold int64 = 1000 // Future phase: make per-context configurable.

type dataExecOptions struct {
	sql          string
	file         string
	fake         bool
	dryRun       bool
	allowNoWhere bool
}

type dataExecPlan struct {
	SQL                   string `json:"sql"`
	Kind                  string `json:"kind"`
	HasWhere              bool   `json:"hasWhere"`
	ImpactRows            *int64 `json:"impactRows,omitempty"`
	PlanFingerprint       string `json:"planFingerprint,omitempty"`
	Risk                  string `json:"risk"`
	Destructive           bool   `json:"destructive"`
	RequiredAuthorization string `json:"requiredAuthorization,omitempty"`
}

type dataExecResult struct {
	SQL             string `json:"sql"`
	Risk            string `json:"risk"`
	ImpactRows      *int64 `json:"impactRows,omitempty"`
	PlanFingerprint string `json:"planFingerprint,omitempty"`
	AffectedRows    int64  `json:"affectedRows"`
}

func newDataCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Execute governed data changes",
	}
	cmd.AddCommand(dataExecCmd(f))
	return cmd
}

func dataExecCmd(f *cliFlags) *cobra.Command {
	var opts dataExecOptions
	cmd := &cobra.Command{
		Use:   "exec --sql UPDATE ...",
		Short: "Execute governed INSERT/UPDATE/DELETE statements",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDataExec(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.sql, "sql", "", "DML statement to execute")
	cmd.Flags().StringVarP(&opts.file, "file", "f", "", "Read DML statement from file")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print risk and impact without executing DML")
	cmd.Flags().BoolVar(&opts.allowNoWhere, "allow-no-where", false, "Allow UPDATE/DELETE without WHERE")
	return cmd
}

func runDataExec(f *cliFlags, opts dataExecOptions) (resultErr error) {
	sqlText, err := readDMLStatement(opts)
	if err != nil {
		return err
	}
	backend, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(backend, &resultErr, backendCloseMutation)
	dialect := sqlclass.DialectForEngine(meta.Engine)
	if sqlclass.HasMultipleStatements(sqlText, dialect) {
		err := apperrors.New(apperrors.CodeValidationFailed, "multiple SQL statements are not allowed; submit one statement at a time", nil)
		event := newDataExecAuditEvent(f, meta, dataExecPlan{SQL: sqlText})
		emitAudit(f, event, err)
		return err
	}
	kind, hasWhere, ok := sqlclass.ClassifyDML(sqlText, dialect)
	if !ok {
		err := apperrors.New(apperrors.CodeValidationFailed, "data exec only accepts INSERT, UPDATE, or DELETE; use dbgov query for reads and schema apply for DDL", nil)
		event := newDataExecAuditEvent(f, meta, dataExecPlan{SQL: sqlText})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildDataExecPlan(commandContext(f), backend, sqlText, kind, hasWhere)
	if err != nil {
		event := newDataExecAuditEvent(f, meta, dataExecPlan{SQL: sqlText, Kind: string(kind), HasWhere: hasWhere})
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := newDataExecAuditEvent(f, meta, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printDataExecPlan(f, meta, plan)
	}

	var requiredAllows []safety.AllowFlag
	granted := map[safety.AllowFlag]bool{}
	if plan.Destructive {
		requiredAllows = []safety.AllowFlag{safety.AllowNoWhere}
		if opts.allowNoWhere {
			granted[safety.AllowNoWhere] = true
		}
	}
	if err := authorizeWrite(f, safetyRisk(plan.Risk), meta, requiredAllows, granted); err != nil {
		event := newDataExecAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}

	event := newDataExecAuditEvent(f, meta, plan)
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeDataExec), plan)
	metadata.Items = 1
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeDataExec),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	affected, err := backend.ExecDMLBound(commandContext(f), sqlText, dbbackend.DMLPlanBinding{
		PlanFingerprint: plan.PlanFingerprint,
		EstimatedRows:   *plan.ImpactRows,
	})
	outcome := dbgaudit.MutationOutcome{AffectedRows: &affected}
	switch {
	case dbbackend.IsCommitIndeterminate(err):
		outcome.Uncertain = 1
	case err == nil:
		outcome.Succeeded = 1
	default:
		outcome.Failed = 1
	}
	if auditErr := finishMutationAudit(handle, outcome, err); auditErr != nil {
		return auditErr
	}
	return printDataExecResult(f, meta, dataExecResult{
		SQL:             sqlText,
		Risk:            plan.Risk,
		ImpactRows:      plan.ImpactRows,
		PlanFingerprint: plan.PlanFingerprint,
		AffectedRows:    affected,
	})
}

func readDMLStatement(opts dataExecOptions) (string, error) {
	if (opts.sql == "") == (opts.file == "") {
		return "", apperrors.New(apperrors.CodeUsageError, "provide exactly one of --sql or --file", nil)
	}
	sqlText := opts.sql
	if opts.file != "" {
		data, err := os.ReadFile(opts.file)
		if err != nil {
			return "", err
		}
		sqlText = string(data)
	}
	return normalizeSingleStatement(sqlText)
}

func normalizeSingleStatement(sqlText string) (string, error) {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return "", apperrors.New(apperrors.CodeValidationFailed, "SQL statement is empty", nil)
	}
	trimmed = strings.TrimSuffix(trimmed, ";")
	if strings.Contains(trimmed, ";") {
		return "", apperrors.New(apperrors.CodeValidationFailed, "data exec accepts exactly one SQL statement", nil)
	}
	return strings.TrimSpace(trimmed), nil
}

func buildDataExecPlan(ctx context.Context, backend interface {
	Explain(ctx context.Context, sql string) (dbbackend.ExplainResult, error)
}, sqlText string, kind sqlclass.Kind, hasWhere bool,
) (dataExecPlan, error) {
	plan := dataExecPlan{
		SQL:      sqlText,
		Kind:     string(kind),
		HasWhere: hasWhere,
	}
	switch kind {
	case sqlclass.KindInsert:
	case sqlclass.KindUpdate, sqlclass.KindDelete:
		if !hasWhere {
			plan.Risk = "R3"
			plan.Destructive = true
			plan.RequiredAuthorization = "R3 requires --yes or interactive confirmation, --ticket, and --allow-no-where"
		}
	default:
		return plan, apperrors.New(apperrors.CodeValidationFailed, "unsupported DML kind", nil)
	}

	explain, err := backend.Explain(ctx, sqlText)
	if err != nil {
		return plan, apperrors.New(apperrors.CodeValidationFailed, "cannot estimate DML impact with EXPLAIN; refusing to continue", err)
	}
	if explain.EstimatedRows < 0 || explain.PlanFingerprint == "" {
		return plan, apperrors.New(apperrors.CodeValidationFailed, "database returned an invalid DML impact plan; refusing to continue", nil)
	}
	impact := explain.EstimatedRows
	plan.ImpactRows = &impact
	plan.PlanFingerprint = explain.PlanFingerprint
	if plan.Destructive {
		return plan, nil
	}
	if impact > defaultImpactThreshold {
		plan.Risk = "R2"
	} else {
		plan.Risk = "R1"
	}
	plan.RequiredAuthorization = requiredAuthorization(sqlclassRisk(plan.Risk))
	return plan, nil
}

func printDataExecPlan(f *cliFlags, meta contextMeta, plan dataExecPlan) error {
	plan.SQL = redactSQL(plan.SQL)
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "DataExecPlan", Data: dataWithTarget(plan, meta, targetWrite)})
	}
	if err := printTargetHeader(p, meta, targetWrite); err != nil {
		return err
	}
	rows := [][]string{{plan.SQL, plan.Kind, fmtImpactRows(plan.ImpactRows), plan.Risk, plan.RequiredAuthorization}}
	return p.Table([]string{"SQL", "KIND", "IMPACT_ROWS", "RISK", "REQUIRED_AUTHORIZATION"}, rows)
}

func printDataExecResult(f *cliFlags, meta contextMeta, result dataExecResult) error {
	result.SQL = redactSQL(result.SQL)
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "DataExecResult", Data: dataWithTarget(result, meta, targetWrite)})
	}
	if err := printTargetHeader(p, meta, targetWrite); err != nil {
		return err
	}
	return p.Table([]string{"SQL", "RISK", "IMPACT_ROWS", "AFFECTED_ROWS"}, [][]string{{result.SQL, result.Risk, fmtImpactRows(result.ImpactRows), fmt.Sprint(result.AffectedRows)}})
}

func newDataExecAuditEvent(f *cliFlags, meta contextMeta, plan dataExecPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeDataExec, currentOperator(f), auditContext(meta), auditTarget(meta, "data", "exec"))
	event.Statement = plan.SQL
	event.Risk = effectiveRiskLabel(plan.Risk, meta)
	event.Destructive = plan.Destructive
	if plan.ImpactRows != nil {
		impact := int(*plan.ImpactRows)
		event.ImpactRows = &impact
	}
	return event
}

func fmtImpactRows(rows *int64) string {
	if rows == nil {
		return ""
	}
	return fmt.Sprint(*rows)
}

func sqlclassRisk(risk string) schema.Risk {
	switch risk {
	case "R1":
		return schema.RiskR1
	case "R2":
		return schema.RiskR2
	case "R3":
		return schema.RiskR3
	default:
		return schema.RiskR0
	}
}
