package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/printer"

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

func runQuery(f *cliFlags, opts sqlCommandOptions) error {
	if !sqlclass.IsReadOnly(opts.SQL) {
		return apperrors.New(apperrors.CodeValidationFailed, "query only accepts read-only SQL", nil)
	}
	if err := authorizeRead(f); err != nil {
		return err
	}
	backend, meta, err := buildBackend(f, backendOptions{Fake: opts.Fake})
	if err != nil {
		return err
	}
	result, err := backend.Query(commandContext(f), opts.SQL)
	event := dbgaudit.New(dbgaudit.EventTypeQuery, currentOperator(f), auditContext(meta), auditTarget(meta, "database", meta.Database))
	event.Statement = opts.SQL
	event.Risk = "R0"
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printQueryResult(f, result)
}

func printQueryResult(f *cliFlags, result dbbackend.QueryResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "QueryResult", Data: result})
	}
	p.Table(result.Columns, result.Rows)
	return nil
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

func runExplain(f *cliFlags, opts sqlCommandOptions) error {
	if !sqlclass.IsReadOnly(opts.SQL) {
		return apperrors.New(apperrors.CodeValidationFailed, "explain only accepts read-only SQL", nil)
	}
	if err := authorizeRead(f); err != nil {
		return err
	}
	backend, meta, err := buildBackend(f, backendOptions{Fake: opts.Fake})
	if err != nil {
		return err
	}
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
	return printExplainResult(f, result)
}

func printExplainResult(f *cliFlags, result dbbackend.ExplainResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "ExplainResult", Data: result})
	}
	p.Table(result.Columns, result.Rows)
	_, _ = fmt.Fprintf(p.Out, "\nEstimated rows: %d\n", result.EstimatedRows)
	return nil
}
