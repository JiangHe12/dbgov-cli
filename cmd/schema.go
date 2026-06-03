package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	"github.com/JiangHe12/opskit-core/apperrors"
	"github.com/JiangHe12/opskit-core/printer"
)

type schemaDiffOptions struct {
	file string
	fake bool
}

type schemaPlanOptions struct {
	file string
	fake bool
}

type schemaApplyOptions struct {
	file             string
	fake             bool
	dryRun           bool
	allowDestructive bool
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

type schemaPlan struct {
	Statements            []schemaPlanStatement `json:"statements"`
	OverallRisk           string                `json:"overallRisk"`
	Destructive           bool                  `json:"destructive"`
	Warnings              []string              `json:"warnings,omitempty"`
	RequiredAuthorization string                `json:"requiredAuthorization,omitempty"`
}

type schemaPlanStatement struct {
	SQL         string        `json:"sql"`
	Action      schema.Action `json:"action"`
	Table       string        `json:"table"`
	Column      string        `json:"column,omitempty"`
	Risk        string        `json:"risk"`
	Destructive bool          `json:"destructive"`
}

func newSchemaCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect database schema",
	}
	cmd.AddCommand(schemaListCmd(f), schemaDescribeCmd(f), schemaDumpCmd(f), schemaPlanCmd(f), schemaApplyCmd(f), schemaDiffCmd(f))
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

func schemaPlanCmd(f *cliFlags) *cobra.Command {
	var opts schemaPlanOptions
	cmd := &cobra.Command{
		Use:   "plan -f desired.sql",
		Short: "Plan schema changes without executing them",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaPlan(f, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.file, "file", "f", "", "Desired schema SQL file")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func schemaApplyCmd(f *cliFlags) *cobra.Command {
	var opts schemaApplyOptions
	cmd := &cobra.Command{
		Use:   "apply -f desired.sql",
		Short: "Apply schema changes from desired SQL",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaApply(f, opts)
		},
	}
	cmd.Flags().StringVarP(&opts.file, "file", "f", "", "Desired schema SQL file")
	cmd.Flags().BoolVar(&opts.fake, "fake", false, "Use in-memory fake backend")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print the plan without executing DDL")
	cmd.Flags().BoolVar(&opts.allowDestructive, "allow-destructive", false, "Allow destructive schema changes")
	_ = cmd.MarkFlagRequired("file")
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

func runSchemaPlan(f *cliFlags, opts schemaPlanOptions) error {
	if err := authorizeRead(f); err != nil {
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
		event := dbgaudit.New(dbgaudit.EventTypeSchemaPlan, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "plan"))
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildSchemaPlan(b, current, desired)
	event := dbgaudit.New(dbgaudit.EventTypeSchemaPlan, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "plan"))
	event.Risk = plan.OverallRisk
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaPlan(f, plan)
}

func runSchemaApply(f *cliFlags, opts schemaApplyOptions) error {
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
		event := newSchemaApplyAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildSchemaPlan(b, current, desired)
	if err != nil {
		event := newSchemaApplyAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := newSchemaApplyAuditEvent(f, meta, plan)
		event.DryRun = true
		emitAudit(f, event, nil)
		return printSchemaPlan(f, plan)
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
		event := newSchemaApplyAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}

	statements := schemaPlanStatements(plan)
	executed, err := b.ExecDDL(commandContext(f), statements)
	event := newSchemaApplyAuditEvent(f, meta, plan)
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

	result, opErr := dumpSchema(commandContext(f), b, current, opts.dir)

	event := dbgaudit.New(dbgaudit.EventTypeSchemaDump, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "dump"))
	event.Risk = "R0"
	emitAudit(f, event, opErr)
	if opErr != nil {
		return opErr
	}
	return printSchemaDump(f, result)
}

func dumpSchema(ctx context.Context, b interface {
	TableDDL(context.Context, string) (string, error)
}, current schema.Schema, dir string) (schemaDumpResult, error) {
	result := schemaDumpResult{}
	for _, name := range sortedTableNames(current) {
		ddl, err := b.TableDDL(ctx, name)
		if err != nil {
			return result, err
		}
		if dir == "" {
			result.Tables = append(result.Tables, schemaDumpTable{Name: name, DDL: ddl})
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return result, err
		}
		path := filepath.Join(dir, name+".sql")
		if err := os.WriteFile(path, []byte(formatDDLStatement(ddl)), 0o600); err != nil {
			return result, err
		}
		result.Files = append(result.Files, path)
	}
	return result, nil
}

func printSchemaDiff(f *cliFlags, diff schema.DiffResult) error {
	rows := make([][]string, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		risk, destructive := schema.ClassifyChange(change)
		marker := ""
		if destructive {
			marker = "DESTRUCTIVE"
		}
		rows = append(rows, []string{string(change.Action), change.Table, change.Column, change.Type, string(risk), marker})
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("SchemaDiff", diff)
	}
	p.Table([]string{"ACTION", "TABLE", "COLUMN", "TYPE", "RISK", "NOTE"}, rows)
	return nil
}

func buildSchemaPlan(b interface {
	RenderDDL([]schema.Change) ([]string, error)
}, current, desired schema.Schema) (schemaPlan, error) {
	diff := schema.Diff(current, desired)
	return buildSchemaPlanFromDiff(b, diff)
}

func buildSchemaPlanFromDiff(b interface {
	RenderDDL([]schema.Change) ([]string, error)
}, diff schema.DiffResult) (schemaPlan, error) {
	risk := schema.ClassifyDiff(diff)
	statements, err := b.RenderDDL(diff.Changes)
	if err != nil {
		return schemaPlan{OverallRisk: string(risk.OverallRisk), Destructive: risk.Destructive, Warnings: diff.Warnings, RequiredAuthorization: requiredAuthorization(risk.OverallRisk)}, err
	}
	plan := schemaPlan{
		Statements:            make([]schemaPlanStatement, 0, len(diff.Changes)),
		OverallRisk:           string(risk.OverallRisk),
		Destructive:           risk.Destructive,
		Warnings:              diff.Warnings,
		RequiredAuthorization: requiredAuthorization(risk.OverallRisk),
	}
	for i, change := range diff.Changes {
		changeRisk, destructive := schema.ClassifyChange(change)
		plan.Statements = append(plan.Statements, schemaPlanStatement{
			SQL:         statements[i],
			Action:      change.Action,
			Table:       change.Table,
			Column:      change.Column,
			Risk:        string(changeRisk),
			Destructive: destructive,
		})
	}
	return plan, nil
}

func printSchemaPlan(f *cliFlags, plan schemaPlan) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaPlan", Data: plan})
	}
	rows := make([][]string, 0, len(plan.Statements))
	for _, stmt := range plan.Statements {
		note := ""
		if stmt.Destructive {
			note = "DESTRUCTIVE"
		}
		rows = append(rows, []string{stmt.SQL, string(stmt.Action), stmt.Table, stmt.Column, stmt.Risk, note})
	}
	p.Table([]string{"SQL", "ACTION", "TABLE", "COLUMN", "RISK", "NOTE"}, rows)
	if len(plan.Warnings) > 0 {
		_, _ = fmt.Fprintf(p.Out, "\nWarnings:\n")
		for _, warning := range plan.Warnings {
			_, _ = fmt.Fprintf(p.Out, "- %s\n", warning)
		}
	}
	if plan.RequiredAuthorization != "" {
		_, _ = fmt.Fprintf(p.Out, "\nRequired authorization: %s\n", plan.RequiredAuthorization)
	}
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

func schemaPlanSQL(plan schemaPlan) string {
	return strings.Join(schemaPlanStatements(plan), "\n")
}

func schemaPlanStatements(plan schemaPlan) []string {
	statements := make([]string, 0, len(plan.Statements))
	for _, stmt := range plan.Statements {
		statements = append(statements, stmt.SQL)
	}
	return statements
}

func requiredAuthorization(risk schema.Risk) string {
	switch risk {
	case schema.RiskR3:
		return "R3 requires --yes or interactive confirmation, --ticket, and required allow flag(s) when applied"
	case schema.RiskR2:
		return "R2 requires --yes or interactive confirmation and --ticket when applied"
	case schema.RiskR1:
		return "R1 requires --yes or interactive confirmation when applied"
	default:
		return ""
	}
}

func newSchemaApplyAuditEvent(f *cliFlags, meta contextMeta, plan schemaPlan) dbgaudit.Event {
	event := dbgaudit.New(dbgaudit.EventTypeSchemaApply, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "apply"))
	event.Risk = plan.OverallRisk
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	return event
}

func setAuditError(event *dbgaudit.Event, err error) {
	appErr := apperrors.AsAppError(err)
	event.Error = &dbgaudit.ErrorInfo{Code: string(appErr.Code), Message: appErr.Message}
}

func safetyRisk(risk string) safety.Risk {
	switch risk {
	case "R1":
		return safety.R1
	case "R2":
		return safety.R2
	case "R3":
		return safety.R3
	default:
		return safety.R0
	}
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
	risk := schema.ClassifyDiff(diff)
	event.Risk = string(risk.OverallRisk)
	event.Destructive = risk.Destructive
	emitAudit(f, event, opErr)
}
