package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	"github.com/JiangHe12/opskit-core/printer"
)

type schemaDiffOptions struct {
	file string
	fake bool
}

type schemaReadOptions struct {
	fake bool
	dir  string
}

type schemaTableList struct {
	Tables []schemaTableSummary `json:"tables"`
}

type schemaTableSummary struct {
	Name string `json:"name"`
}

type schemaDescribeResult struct {
	Table schema.Table `json:"table"`
}

type schemaDumpResult struct {
	Files  []string          `json:"files,omitempty"`
	Tables []schemaDumpTable `json:"tables,omitempty"`
}

type schemaDumpTable struct {
	Name string `json:"name"`
	DDL  string `json:"ddl"`
}

func newSchemaCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect database schema",
	}
	cmd.AddCommand(schemaListCmd(f), schemaDescribeCmd(f), schemaDumpCmd(f), schemaDiffCmd(f))
	return cmd
}

func schemaListCmd(f *cliFlags) *cobra.Command {
	var opts schemaReadOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List database tables",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaList(f, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	return cmd
}

func schemaDescribeCmd(f *cliFlags) *cobra.Command {
	var opts schemaReadOptions
	cmd := &cobra.Command{
		Use:   "describe <table>",
		Short: "Describe a database table",
		Args:  requireExactArgs("schema describe", 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaDescribe(f, opts, args[0])
		},
	}
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	return cmd
}

func schemaDumpCmd(f *cliFlags) *cobra.Command {
	var opts schemaReadOptions
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump authoritative CREATE TABLE DDL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaDump(f, opts)
		},
	}
	cmd.Flags().StringVar(&opts.dir, "dir", "", "Directory to write table SQL files")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
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

func runSchemaList(f *cliFlags, opts schemaReadOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	event := dbgaudit.New(dbgaudit.EventTypeSchemaList, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "list"))
	event.Risk = "R0"
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaList(f, current)
}

func runSchemaDescribe(f *cliFlags, opts schemaReadOptions, table string) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	event := dbgaudit.New(dbgaudit.EventTypeSchemaDescribe, currentOperator(f), auditContext(meta), auditTarget(meta, "table", table))
	event.Risk = "R0"
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	tbl, ok := current.Tables[table]
	if !ok {
		err := fmt.Errorf("table not found: %s", table)
		emitAudit(f, event, err)
		return err
	}
	emitAudit(f, event, nil)
	return printSchemaDescribe(f, tbl)
}

func runSchemaDump(f *cliFlags, opts schemaReadOptions) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := dbgaudit.New(dbgaudit.EventTypeSchemaDump, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "dump"))
		event.Risk = "R0"
		emitAudit(f, event, err)
		return err
	}

	result := schemaDumpResult{}
	var opErr error
	for _, name := range sortedTableNames(current) {
		ddl, err := b.TableDDL(commandContext(f), name)
		if err != nil {
			opErr = err
			break
		}
		if opts.dir == "" {
			result.Tables = append(result.Tables, schemaDumpTable{Name: name, DDL: ddl})
			continue
		}
		if err := os.MkdirAll(opts.dir, 0o700); err != nil {
			opErr = err
			break
		}
		path := filepath.Join(opts.dir, name+".sql")
		if err := os.WriteFile(path, []byte(formatDDLStatement(ddl)), 0o600); err != nil {
			opErr = err
			break
		}
		result.Files = append(result.Files, path)
	}

	event := dbgaudit.New(dbgaudit.EventTypeSchemaDump, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "dump"))
	event.Risk = "R0"
	emitAudit(f, event, opErr)
	if opErr != nil {
		return opErr
	}
	return printSchemaDump(f, result)
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

func printSchemaList(f *cliFlags, current schema.Schema) error {
	result := schemaTableList{}
	rows := make([][]string, 0, len(current.Tables))
	for _, name := range sortedTableNames(current) {
		result.Tables = append(result.Tables, schemaTableSummary{Name: name})
		rows = append(rows, []string{name})
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaTableList", Data: result})
	}
	p.Table([]string{"TABLE"}, rows)
	return nil
}

func printSchemaDescribe(f *cliFlags, table schema.Table) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaDescribe", Data: schemaDescribeResult{Table: table}})
	}
	rows := make([][]string, 0, len(table.Columns))
	for _, col := range table.Columns {
		def := ""
		if col.Default != nil {
			def = *col.Default
		}
		rows = append(rows, []string{col.Name, col.Type, fmt.Sprintf("%t", col.Nullable), def, col.Key})
	}
	p.Table([]string{"COLUMN", "TYPE", "NULLABLE", "DEFAULT", "KEY"}, rows)
	return nil
}

func printSchemaDump(f *cliFlags, result schemaDumpResult) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaDump", Data: result})
	}
	if len(result.Files) > 0 {
		rows := make([][]string, 0, len(result.Files))
		for _, file := range result.Files {
			rows = append(rows, []string{file})
		}
		p.Table([]string{"FILE"}, rows)
		return nil
	}
	var out strings.Builder
	for i, table := range result.Tables {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(formatDDLStatement(table.DDL))
	}
	p.Content("SchemaDump", out.String())
	return nil
}

func formatDDLStatement(ddl string) string {
	text := strings.TrimSpace(ddl)
	if !strings.HasSuffix(text, ";") {
		text += ";"
	}
	return text + "\n"
}

func sortedTableNames(current schema.Schema) []string {
	names := make([]string, 0, len(current.Tables))
	for name := range current.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
