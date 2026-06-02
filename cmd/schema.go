package cmd

import (
	"os"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type schemaDiffOptions struct {
	file string
	fake bool
}

func newSchemaCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect database schema",
	}
	cmd.AddCommand(schemaDiffCmd(f))
	return cmd
}

func schemaDiffCmd(f *cliFlags) *cobra.Command {
	var opts schemaDiffOptions
	cmd := &cobra.Command{
		Use:   "diff -f desired.sql",
		Short: "Compare desired CREATE TABLE SQL against current schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaDiff(f, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.file, "file", "f", "", "Desired schema SQL file")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runSchemaDiff(f *cliFlags, opts schemaDiffOptions) error {
	if err := safety.Authorize(safety.R0, safety.Options{Operator: currentOperator(f)}); err != nil {
		return err
	}
	desiredBytes, err := os.ReadFile(opts.file)
	if err != nil {
		return err
	}
	desired, err := schema.ParseDesiredSQL(string(desiredBytes))
	if err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		return err
	}
	diff := schema.Diff(current, desired)
	writeSchemaDiffAudit(f, meta, diff, nil)
	return printSchemaDiff(f, diff)
}

func printSchemaDiff(f *cliFlags, diff schema.DiffResult) error {
	rows := make([][]string, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		risk := "R0"
		marker := ""
		if change.Destructive {
			risk = "R3"
			marker = "DESTRUCTIVE"
		}
		rows = append(rows, []string{string(change.Action), change.Table, change.Column, change.Type, risk, marker})
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("SchemaDiff", diff)
	}
	p.Table([]string{"ACTION", "TABLE", "COLUMN", "TYPE", "RISK", "NOTE"}, rows)
	return nil
}

func writeSchemaDiffAudit(f *cliFlags, meta contextMeta, diff schema.DiffResult, opErr error) {
	event := dbgaudit.New(dbgaudit.EventTypeSchemaDiff, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "diff"))
	event.Risk = "R0"
	if diff.Destructive {
		event.Risk = "R3"
		event.Destructive = true
	}
	emitAudit(f, event, opErr)
}
