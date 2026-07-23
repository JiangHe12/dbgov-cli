package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/sqlclass"
)

type sqlCommandOptions struct {
	SQL  string
	Fake bool
}

//nolint:dupl // Query and explain commands intentionally keep separate Cobra wiring for clearer help text and audit paths.
func newQueryCmd(f *cliFlags) *cobra.Command {
	var opts sqlCommandOptions
	cmd := &cobra.Command{
		Use:   "query --sql SELECT ...",
		Short: "Run read-only SQL and print rows",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runQuery(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.SQL, "sql", "", "Read-only SQL statement")
	cmd.Flags().BoolVar(&opts.Fake, "fake", false, "Use in-memory fake backend")
	_ = cmd.MarkFlagRequired("sql")
	return cmd
}

func runQuery(f *cliFlags, opts sqlCommandOptions) (resultErr error) {
	dialect := dialectForSQLCommand(f)
	if sqlclass.HasMultipleStatements(opts.SQL, dialect) {
		return apperrors.New(apperrors.CodeValidationFailed, "multiple SQL statements are not allowed; submit one statement at a time", nil)
	}
	if !sqlclass.IsReadOnly(opts.SQL, dialect) {
		return apperrors.New(
			apperrors.CodeValidationFailed,
			"query only accepts read-only SQL using known safe built-in functions; PostgreSQL built-ins require pg_catalog qualification, and unknown or user-defined functions are rejected",
			nil,
		)
	}
	if err := authorizeRead(f); err != nil {
		return err
	}
	backend, meta, err := buildBackend(f, backendOptions{Fake: opts.Fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(backend, &resultErr, backendCloseRead)
	result, err := backend.Query(commandContext(f), opts.SQL)
	event := dbgaudit.New(dbgaudit.EventTypeQuery, currentOperator(f), auditContext(meta), auditTarget(meta, "database", meta.Database))
	event.Statement = opts.SQL
	event.Risk = "R0"
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printQueryResult(f, meta, result)
}

func printQueryResult(f *cliFlags, meta contextMeta, result dbbackend.QueryResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "QueryResult", Data: dataWithTarget(result, meta, targetRead)})
	}
	if err := printTargetHeader(p, meta, targetRead); err != nil {
		return err
	}
	return p.Table(result.Columns, result.DisplayRows())
}

//nolint:dupl // Query and explain commands intentionally keep separate Cobra wiring for clearer help text and audit paths.
func newExplainCmd(f *cliFlags) *cobra.Command {
	var opts sqlCommandOptions
	cmd := &cobra.Command{
		Use:   "explain --sql SELECT ...",
		Short: "Show execution plan for read SQL",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runExplain(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.SQL, "sql", "", "SQL statement to explain")
	cmd.Flags().BoolVar(&opts.Fake, "fake", false, "Use in-memory fake backend")
	_ = cmd.MarkFlagRequired("sql")
	return cmd
}

func runExplain(f *cliFlags, opts sqlCommandOptions) (resultErr error) {
	dialect := dialectForSQLCommand(f)
	if sqlclass.HasMultipleStatements(opts.SQL, dialect) {
		return apperrors.New(apperrors.CodeValidationFailed, "multiple SQL statements are not allowed; submit one statement at a time", nil)
	}
	if !sqlclass.IsReadOnly(opts.SQL, dialect) {
		return apperrors.New(
			apperrors.CodeValidationFailed,
			"explain only accepts read-only SQL using known safe built-in functions; PostgreSQL built-ins require pg_catalog qualification, and unknown or user-defined functions are rejected",
			nil,
		)
	}
	if err := authorizeRead(f); err != nil {
		return err
	}
	backend, meta, err := buildBackend(f, backendOptions{Fake: opts.Fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(backend, &resultErr, backendCloseRead)
	result, err := backend.Explain(commandContext(f), opts.SQL)
	event := dbgaudit.New(dbgaudit.EventTypeExplain, currentOperator(f), auditContext(meta), auditTarget(meta, "database", meta.Database))
	event.Statement = opts.SQL
	event.Risk = "R0"
	impactRows := int(result.EstimatedRows)
	event.ImpactRows = &impactRows
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printExplainResult(f, meta, result)
}

func dialectForSQLCommand(f *cliFlags) sqlclass.Dialect {
	ctx, _ := selectedContext(f)
	if ctx == nil {
		return sqlclass.DialectMySQL
	}
	return sqlclass.DialectForEngine(ctx.Engine)
}

func printExplainResult(f *cliFlags, meta contextMeta, result dbbackend.ExplainResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "ExplainResult", Data: dataWithTarget(result, meta, targetRead)})
	}
	if err := printTargetHeader(p, meta, targetRead); err != nil {
		return err
	}
	if err := p.Table(result.Columns, result.DisplayRows()); err != nil {
		return err
	}
	return p.Info(fmt.Sprintf("\nEstimated rows: %d", result.EstimatedRows))
}
