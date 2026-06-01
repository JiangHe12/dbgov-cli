package cmd

import (
	"os"

	"github.com/spf13/cobra"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	"github.com/JiangHe12/opskit-core/audit"
	"github.com/JiangHe12/opskit-core/safety"
)

const eventSchemaDiff audit.EventType = "schema.diff"

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
	b, ctxName, meta, err := buildSchemaBackend(f, opts)
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		return err
	}
	diff := schema.Diff(current, desired)
	writeSchemaDiffAudit(f, ctxName, meta, diff, nil)
	return printSchemaDiff(f, diff)
}

func buildSchemaBackend(f *cliFlags, opts schemaDiffOptions) (dbbackend.Backend, string, *dbgovctxForAudit, error) {
	if opts.fake {
		return fake.New(), "fake", &dbgovctxForAudit{Name: "fake"}, nil
	}
	ctx, name := selectedContext(f)
	if ctx == nil {
		return nil, "", nil, errNoContext()
	}
	if ctx.Engine != "mysql" {
		return nil, "", nil, errUnsupportedEngine(ctx.Engine)
	}
	dsn := ctx.Username + ":" + ctx.Password + "@tcp(" + ctx.Host + ":" + itoa(ctx.Port) + ")/" + ctx.Database
	b, err := mysql.New(dsn, ctx.Database)
	if err != nil {
		return nil, "", nil, err
	}
	return b, name, &dbgovctxForAudit{Name: name, Env: ctx.Env, Protected: ctx.Protected, Database: ctx.Database}, nil
}

type dbgovctxForAudit struct {
	Name      string
	Env       string
	Protected bool
	Database  string
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

func writeSchemaDiffAudit(f *cliFlags, contextName string, meta *dbgovctxForAudit, diff schema.DiffResult, opErr error) {
	path, err := audit.DefaultPath()
	if err != nil {
		return
	}
	status := audit.StatusSuccess
	if opErr != nil {
		status = audit.StatusFailed
	}
	event := audit.Event{
		EventType: eventSchemaDiff,
		Operator:  currentOperator(f),
		Context: audit.EventContext{
			Name:      contextName,
			Env:       meta.Env,
			Protected: meta.Protected,
		},
		Target: audit.EventTarget{
			App:          meta.Database,
			ResourceType: "schema",
			Resource:     "diff",
		},
		Status: status,
	}
	_ = audit.Append(path, event)
}
