package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	"github.com/JiangHe12/opskit-core/v2/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
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
	PlannedStatements     int                   `json:"plannedStatements"`
	PlanFingerprint       string                `json:"planFingerprint,omitempty"`
	TargetFingerprint     string                `json:"targetFingerprint,omitempty"`
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

type schemaPlanRenderer interface {
	RenderDDL([]schema.Change) ([]string, error)
}

type schemaPlanBackend interface {
	schemaPlanRenderer
	IntrospectSchema(context.Context) (schema.Schema, error)
}

type schemaExecutionBackend interface {
	schemaPlanBackend
	TableDDL(context.Context, string) (string, error)
}

type schemaExecutionBinding struct {
	PlanFingerprint   string   `json:"planFingerprint"`
	TargetFingerprint string   `json:"targetFingerprint"`
	Statements        []string `json:"statements"`
}

type schemaPlanFingerprintPayload struct {
	TargetFingerprint     string                `json:"targetFingerprint"`
	Current               schema.Schema         `json:"current"`
	Desired               schema.Schema         `json:"desired"`
	Statements            []schemaPlanStatement `json:"statements"`
	OverallRisk           string                `json:"overallRisk"`
	Destructive           bool                  `json:"destructive"`
	Warnings              []string              `json:"warnings,omitempty"`
	RequiredAuthorization string                `json:"requiredAuthorization,omitempty"`
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
		Args:  requireExactArgs("schema describe"),
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

func runSchemaDiff(f *cliFlags, opts schemaDiffOptions) (resultErr error) {
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
	defer finishBackendClose(b, &resultErr, backendCloseRead)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		return err
	}
	diff := schema.Diff(current, desired)
	writeSchemaDiffAudit(f, meta, diff, nil)
	return printSchemaDiff(f, meta, diff)
}

func runSchemaPlan(f *cliFlags, opts schemaPlanOptions) (resultErr error) {
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
	defer finishBackendClose(b, &resultErr, backendCloseRead)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := dbgaudit.New(dbgaudit.EventTypeSchemaPlan, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "plan"))
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildBoundSchemaPlan(b, meta, current, desired)
	event := dbgaudit.New(dbgaudit.EventTypeSchemaPlan, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "plan"))
	event.Risk = plan.OverallRisk
	event.Destructive = plan.Destructive
	event.Statement = schemaPlanSQL(plan)
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaPlan(f, meta, targetRead, plan)
}

func runSchemaApply(f *cliFlags, opts schemaApplyOptions) (resultErr error) {
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
	defer finishBackendClose(b, &resultErr, backendCloseMutation)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := newSchemaApplyAuditEvent(f, meta, schemaPlan{})
		emitAudit(f, event, err)
		return err
	}
	plan, err := buildBoundSchemaPlan(b, meta, current, desired)
	if err != nil {
		event := newSchemaApplyAuditEvent(f, meta, plan)
		emitAudit(f, event, err)
		return err
	}
	if opts.dryRun {
		event := newSchemaApplyAuditEvent(f, meta, plan)
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
		event := newSchemaApplyAuditEvent(f, meta, plan)
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	statements := schemaPlanStatements(plan)
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeSchemaApply), schemaPlanExecutionBinding(plan))
	metadata.Items = len(statements)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeSchemaApply),
		Event:    newSchemaApplyAuditEvent(f, meta, plan),
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	snapshotID, err := prepareSchemaExecutionSnapshot(f, b, meta, "apply", plan, func(fresh schema.Schema) (schemaPlan, error) {
		return buildBoundSchemaPlan(b, meta, fresh, desired)
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
	return printSchemaPlan(f, meta, targetWrite, plan)
}

func runSchemaList(f *cliFlags, opts schemaReadOptions) (resultErr error) {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(b, &resultErr, backendCloseRead)
	current, err := b.IntrospectSchema(commandContext(f))
	event := dbgaudit.New(dbgaudit.EventTypeSchemaList, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "list"))
	event.Risk = "R0"
	emitAudit(f, event, err)
	if err != nil {
		return err
	}
	return printSchemaList(f, meta, current)
}

func runSchemaDescribe(f *cliFlags, opts schemaReadOptions, table string) (resultErr error) {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	defer finishBackendClose(b, &resultErr, backendCloseRead)
	current, err := b.IntrospectSchema(commandContext(f))
	event := dbgaudit.New(dbgaudit.EventTypeSchemaDescribe, currentOperator(f), auditContext(meta), auditTarget(meta, "table", table))
	event.Risk = "R0"
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	tbl, ok := current.Tables[table]
	if !ok {
		err := apperrors.New(apperrors.CodeResourceNotFound, fmt.Sprintf("table not found: %s", table), nil)
		emitAudit(f, event, err)
		return err
	}
	emitAudit(f, event, nil)
	return printSchemaDescribe(f, meta, tbl)
}

//nolint:dupl // Schema dump and export share the same governed read/audit flow with different event types.
func runSchemaDump(f *cliFlags, opts schemaReadOptions) (resultErr error) {
	if err := authorizeRead(f); err != nil {
		return err
	}
	b, meta, err := buildBackend(f, backendOptions{Fake: opts.fake})
	if err != nil {
		return err
	}
	closeSemantics := backendCloseRead
	if opts.dir != "" {
		closeSemantics = backendCloseMutation
	}
	defer finishBackendClose(b, &resultErr, closeSemantics)
	current, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		event := dbgaudit.New(dbgaudit.EventTypeSchemaDump, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "dump"))
		event.Risk = "R0"
		emitAudit(f, event, err)
		return err
	}

	event := dbgaudit.New(dbgaudit.EventTypeSchemaDump, currentOperator(f), auditContext(meta), auditTarget(meta, "schema", "dump"))
	event.Risk = "R0"
	result, opErr := collectSchemaDump(commandContext(f), b, current)
	if opErr != nil {
		emitAudit(f, event, opErr)
		return opErr
	}
	if opts.dir == "" {
		emitAudit(f, event, nil)
		return printSchemaDump(f, meta, result)
	}
	targetDir, err := canonicalLocalMutationDirectory(opts.dir)
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	relativePaths, err := schemaDumpRelativePaths(result)
	if err != nil {
		emitAudit(f, event, err)
		return err
	}
	if err := preflightPrivateMutationFiles(targetDir, relativePaths); err != nil {
		emitAudit(f, event, err)
		return err
	}
	event.Target = auditTarget(meta, "directory", targetDir)
	event.Risk = effectiveRiskLabel("R1", meta)
	if err := authorizeWrite(f, safety.R1, meta, nil, nil); err != nil {
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeSchemaDump), result.Tables)
	metadata.Items = len(result.Tables)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeSchemaDump),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	result, attempted, opErr := writeSchemaDump(targetDir, result)
	if auditErr := finishMutationAuditProgress(handle, metadata.Items, len(result.Files), attempted, opErr); auditErr != nil {
		return auditErr
	}
	return printSchemaDump(f, meta, result)
}

func collectSchemaDump(ctx context.Context, b interface {
	TableDDL(context.Context, string) (string, error)
}, current schema.Schema,
) (schemaDumpResult, error) {
	result := schemaDumpResult{}
	for _, name := range sortedTableNames(current) {
		ddl, err := b.TableDDL(ctx, name)
		if err != nil {
			return result, err
		}
		result.Tables = append(result.Tables, schemaDumpTable{Name: name, DDL: ddl})
	}
	return result, nil
}

func writeSchemaDump(dir string, source schemaDumpResult) (schemaDumpResult, int, error) {
	result := schemaDumpResult{Files: []string{}}
	relativePaths, err := schemaDumpRelativePaths(source)
	if err != nil {
		return result, 0, err
	}
	if err := ensurePrivateMutationDirectory(dir); err != nil {
		return result, 0, err
	}
	attempted := 0
	for index, table := range source.Tables {
		attempted++
		path, err := writePrivateMutationFile(dir, relativePaths[index], []byte(formatDDLStatement(table.DDL)))
		if err != nil {
			return result, attempted, err
		}
		result.Files = append(result.Files, path)
	}
	return result, attempted, nil
}

func schemaDumpRelativePaths(source schemaDumpResult) ([]string, error) {
	relativePaths := make([]string, 0, len(source.Tables))
	seen := make(map[string]struct{}, len(source.Tables))
	for _, table := range source.Tables {
		if err := validateMutationBasename(table.Name); err != nil {
			return nil, err
		}
		relativePath, err := cleanMutationRelativePath(table.Name + ".sql")
		if err != nil {
			return nil, err
		}
		key := mutationPathKey(relativePath)
		if _, exists := seen[key]; exists {
			return nil, mutationPathCollisionError(relativePath)
		}
		seen[key] = struct{}{}
		relativePaths = append(relativePaths, relativePath)
	}
	return relativePaths, nil
}

func printSchemaDiff(f *cliFlags, meta contextMeta, diff schema.DiffResult) error {
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
		return p.JSONData("SchemaDiff", dataWithTarget(diff, meta, targetRead))
	}
	if err := printTargetHeader(p, meta, targetRead); err != nil {
		return err
	}
	return p.Table([]string{"ACTION", "TABLE", "COLUMN", "TYPE", "RISK", "NOTE"}, rows)
}

func buildSchemaPlan(b schemaPlanRenderer, current, desired schema.Schema) (schemaPlan, error) {
	diff := schema.Diff(current, desired)
	return buildSchemaPlanFromDiff(b, diff)
}

func buildSchemaPlanFromDiff(b schemaPlanRenderer, diff schema.DiffResult) (schemaPlan, error) {
	risk := schema.ClassifyDiff(diff)
	statements, err := b.RenderDDL(diff.Changes)
	if err != nil {
		return schemaPlan{OverallRisk: string(risk.OverallRisk), Destructive: risk.Destructive, Warnings: diff.Warnings, RequiredAuthorization: requiredAuthorization(risk.OverallRisk)}, err
	}
	plan := schemaPlan{
		Statements:            make([]schemaPlanStatement, 0, len(diff.Changes)),
		PlannedStatements:     len(statements),
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

func buildBoundSchemaPlan(
	b schemaPlanRenderer,
	meta contextMeta,
	current schema.Schema,
	desired schema.Schema,
) (schemaPlan, error) {
	plan, err := buildSchemaPlan(b, current, desired)
	bindSchemaPlan(&plan, meta, current, desired)
	return plan, err
}

func bindSchemaPlan(plan *schemaPlan, meta contextMeta, current, desired schema.Schema) {
	targetData, _ := json.Marshal(normalizedSnapshotTarget(*snapshotTarget(meta)))
	plan.TargetFingerprint, _ = dbgaudit.Fingerprint("schema-target", string(targetData))
	plan.PlannedStatements = len(plan.Statements)
	payload := schemaPlanFingerprintPayload{
		TargetFingerprint:     plan.TargetFingerprint,
		Current:               current,
		Desired:               desired,
		Statements:            plan.Statements,
		OverallRisk:           plan.OverallRisk,
		Destructive:           plan.Destructive,
		Warnings:              plan.Warnings,
		RequiredAuthorization: plan.RequiredAuthorization,
	}
	payloadData, _ := json.Marshal(payload)
	plan.PlanFingerprint, _ = dbgaudit.Fingerprint("schema-plan", string(payloadData))
}

func schemaPlanExecutionBinding(plan schemaPlan) schemaExecutionBinding {
	return schemaExecutionBinding{
		PlanFingerprint:   plan.PlanFingerprint,
		TargetFingerprint: plan.TargetFingerprint,
		Statements:        schemaPlanStatements(plan),
	}
}

func revalidateSchemaExecutionPlan(
	f *cliFlags,
	b schemaPlanBackend,
	expected schemaPlan,
	rebuild func(schema.Schema) (schemaPlan, error),
) (schema.Schema, error) {
	if expected.PlanFingerprint == "" ||
		expected.TargetFingerprint == "" ||
		expected.PlannedStatements != len(expected.Statements) {
		return schema.Schema{}, apperrors.New(
			apperrors.CodeConflict,
			"schema plan binding is invalid; review and retry",
			nil,
		)
	}
	fresh, err := b.IntrospectSchema(commandContext(f))
	if err != nil {
		return schema.Schema{}, err
	}
	actual, err := rebuild(fresh)
	if err != nil {
		return fresh, apperrors.New(
			apperrors.CodeConflict,
			"database schema changed after authorization; review and retry",
			err,
		)
	}
	if actual.PlanFingerprint != expected.PlanFingerprint ||
		actual.TargetFingerprint != expected.TargetFingerprint ||
		actual.PlannedStatements != expected.PlannedStatements {
		return fresh, apperrors.New(
			apperrors.CodeConflict,
			"database schema changed after authorization; review and retry",
			nil,
		)
	}
	return fresh, nil
}

func prepareSchemaExecutionSnapshot(
	f *cliFlags,
	b schemaExecutionBackend,
	meta contextMeta,
	command string,
	expected schemaPlan,
	rebuild func(schema.Schema) (schemaPlan, error),
) (string, error) {
	fresh, err := revalidateSchemaExecutionPlan(f, b, expected, rebuild)
	if err != nil {
		return "", err
	}
	return captureSchemaSnapshot(f, b, fresh, meta, command)
}

func printSchemaPlan(f *cliFlags, meta contextMeta, mode targetMode, plan schemaPlan) error {
	plan.Statements = append([]schemaPlanStatement(nil), plan.Statements...)
	for index := range plan.Statements {
		plan.Statements[index].SQL = redactSQL(plan.Statements[index].SQL)
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaPlan", Data: dataWithTarget(plan, meta, mode)})
	}
	if err := printTargetHeader(p, meta, mode); err != nil {
		return err
	}
	rows := make([][]string, 0, len(plan.Statements))
	for _, stmt := range plan.Statements {
		note := ""
		if stmt.Destructive {
			note = "DESTRUCTIVE"
		}
		rows = append(rows, []string{stmt.SQL, string(stmt.Action), stmt.Table, stmt.Column, stmt.Risk, note})
	}
	if err := p.Table([]string{"SQL", "ACTION", "TABLE", "COLUMN", "RISK", "NOTE"}, rows); err != nil {
		return err
	}
	if len(plan.Warnings) > 0 {
		if err := p.Info("\nWarnings:"); err != nil {
			return err
		}
		for _, warning := range plan.Warnings {
			if err := p.Info("- " + warning); err != nil {
				return err
			}
		}
	}
	if plan.RequiredAuthorization != "" {
		return p.Info("\nRequired authorization: " + plan.RequiredAuthorization)
	}
	return nil
}

func printSchemaList(f *cliFlags, meta contextMeta, current schema.Schema) error {
	result := schemaTableList{}
	rows := make([][]string, 0, len(current.Tables))
	for _, name := range sortedTableNames(current) {
		result.Tables = append(result.Tables, schemaTableSummary{Name: name})
		rows = append(rows, []string{name})
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaTableList", Data: dataWithTarget(result, meta, targetRead)})
	}
	if err := printTargetHeader(p, meta, targetRead); err != nil {
		return err
	}
	return p.Table([]string{"TABLE"}, rows)
}

func printSchemaDescribe(f *cliFlags, meta contextMeta, table schema.Table) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaDescribe", Data: dataWithTarget(schemaDescribeResult{Table: table}, meta, targetRead)})
	}
	if err := printTargetHeader(p, meta, targetRead); err != nil {
		return err
	}
	rows := make([][]string, 0, len(table.Columns))
	for _, col := range table.Columns {
		def := ""
		if col.Default != nil {
			def = *col.Default
		}
		rows = append(rows, []string{col.Name, col.Type, fmt.Sprintf("%t", col.Nullable), def, col.Key})
	}
	return p.Table([]string{"COLUMN", "TYPE", "NULLABLE", "DEFAULT", "KEY"}, rows)
}

func printSchemaDump(f *cliFlags, meta contextMeta, result schemaDumpResult) error {
	result.Tables = append([]schemaDumpTable(nil), result.Tables...)
	for index := range result.Tables {
		result.Tables[index].DDL = redactSQL(result.Tables[index].DDL)
	}
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "SchemaDump", Data: dataWithTarget(result, meta, targetRead)})
	}
	if err := printTargetHeader(p, meta, targetRead); err != nil {
		return err
	}
	if len(result.Files) > 0 {
		rows := make([][]string, 0, len(result.Files))
		for _, file := range result.Files {
			rows = append(rows, []string{file})
		}
		return p.Table([]string{"FILE"}, rows)
	}
	var out strings.Builder
	for i, table := range result.Tables {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(formatDDLStatement(table.DDL))
	}
	return p.Content("SchemaDump", out.String())
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
	case schema.RiskR0:
		return ""
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
	event.Risk = effectiveRiskLabel(plan.OverallRisk, meta)
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
