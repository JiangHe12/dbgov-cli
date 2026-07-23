package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"
	corecredstore "github.com/JiangHe12/opskit-core/v2/credstore"
	corectx "github.com/JiangHe12/opskit-core/v2/ctx"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
	"github.com/JiangHe12/dbgov-cli/internal/backend/postgres"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	dbgsnapshot "github.com/JiangHe12/dbgov-cli/internal/snapshot"
	"github.com/JiangHe12/dbgov-cli/internal/sqlclass"
)

var errFakeExplain = errors.New("fake explain failure")

type fixedExplainBackend struct {
	result dbbackend.ExplainResult
}

func (b fixedExplainBackend) Explain(context.Context, string) (dbbackend.ExplainResult, error) {
	return b.result, nil
}

func TestQueryFakeBackendJSONAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, err := executeCommandForTest("-o", "json", "query", "--sql", "SELECT id, name FROM users", "--fake")
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if !strings.Contains(out, `"kind": "QueryResult"`) || !strings.Contains(out, `"columns"`) {
		t.Fatalf("unexpected query output: %s", out)
	}
	evt := lastAuditEvent(t, home)
	statementFingerprint, _ := dbgaudit.Fingerprint("statement", "SELECT id, name FROM users")
	if evt.EventType != dbgaudit.EventTypeQuery ||
		evt.Statement != "" ||
		evt.StatementFingerprint != statementFingerprint ||
		evt.Target.ObjectType != "database" {
		t.Fatalf("audit event = %+v", evt)
	}
}

func TestQueryRejectsWriteSQL(t *testing.T) {
	_, err := executeCommandForTest("query", "--sql", "UPDATE users SET name='x'", "--fake")
	if err == nil {
		t.Fatal("expected query to reject write SQL")
	}
	if !strings.Contains(err.Error(), "query only accepts read-only SQL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueryRejectsUnknownFunctionBeforeBackendAccess(t *testing.T) {
	_, err := executeCommandForTest(
		"query",
		"--sql", "SELECT dangerous_udf(id) FROM users",
		"--fake",
	)
	if err == nil {
		t.Fatal("expected query to reject unknown function")
	}
	if !strings.Contains(err.Error(), "unknown or user-defined functions are rejected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueryRejectsExecutingExplainAndDataModifyingCTE(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "pg", "--engine", "postgres", "--host", "127.0.0.1", "--database", "demo"); err != nil {
		t.Fatalf("ctx set postgres error = %v", err)
	}
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "use", "pg"); err != nil {
		t.Fatalf("ctx use postgres error = %v", err)
	}

	tests := []string{
		"EXPLAIN ANALYZE DELETE FROM users",
		"WITH gone AS (DELETE FROM users RETURNING *) SELECT count(*) FROM gone",
	}
	for _, sqlText := range tests {
		if _, err := executeCommandForTest("--config", configPath, "query", "--sql", sqlText, "--fake"); err == nil {
			t.Fatalf("query accepted executing read-path SQL %q", sqlText)
		} else if !strings.Contains(err.Error(), "query only accepts read-only SQL") {
			t.Fatalf("query error for %q = %v", sqlText, err)
		}
	}
}

func TestExplainFakeBackendJSONAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, err := executeCommandForTest("-o", "json", "explain", "--sql", "SELECT * FROM users", "--fake")
	if err != nil {
		t.Fatalf("explain error = %v", err)
	}
	if !strings.Contains(out, `"kind": "ExplainResult"`) || !strings.Contains(out, `"estimatedRows": 2`) {
		t.Fatalf("unexpected explain output: %s", out)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeExplain || evt.ImpactRows == nil || *evt.ImpactRows != 2 {
		t.Fatalf("audit event = %+v", evt)
	}
}

func TestQueryUsesPostgresDialectForSelectedContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "pg", "--engine", "postgres", "--host", "127.0.0.1", "--database", "demo"); err != nil {
		t.Fatalf("ctx set postgres error = %v", err)
	}
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "use", "pg"); err != nil {
		t.Fatalf("ctx use postgres error = %v", err)
	}
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("--config", configPath, "-o", "json", "query", "--sql", "SELECT $$; DROP TABLE users;$$ AS body", "--fake")
	if err != nil {
		t.Fatalf("postgres dialect query error = %v", err)
	}
	if !strings.Contains(out, `"kind": "QueryResult"`) {
		t.Fatalf("query output = %s", out)
	}

	if _, err := executeCommandForTest("--config", configPath, "query", "--sql", `SELECT 'a\'; DROP TABLE users`, "--fake"); err == nil {
		t.Fatal("expected postgres backslash string to expose multiple statements and be rejected")
	}
}

func TestCtxSetAllowsPostgresDefaultPort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "pg", "--engine", "postgres", "--host", "127.0.0.1", "--database", "demo"); err != nil {
		t.Fatalf("ctx set postgres error = %v", err)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := cfg.Contexts["pg"]
	if ctx.Engine != "postgres" || ctx.Port != 5432 || ctx.Server != "postgres://127.0.0.1:5432" {
		t.Fatalf("postgres context = %+v", ctx)
	}
}

func TestBuildBackendAllowsPostgres(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("pg", dbgovctx.Context{
		Base:     corectx.Base{Password: "secret"},
		Engine:   "postgres",
		Host:     "127.0.0.1",
		Port:     5432,
		Database: "demo",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("pg"); err != nil {
		t.Fatal(err)
	}

	backend, meta, err := buildBackend(&cliFlags{Config: configPath}, backendOptions{})
	if err != nil {
		t.Fatalf("buildBackend(postgres) error = %v", err)
	}
	defer func() { _ = backend.Close() }()
	if _, ok := backend.(*postgres.Backend); !ok {
		t.Fatalf("backend type = %T, want *postgres.Backend", backend)
	}
	if meta.Engine != "postgres" || meta.Database != "demo" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDoctorConfigFakeBackend(t *testing.T) {
	out, err := executeCommandForTest("-o", "json", "doctor", "config", "--fake")
	if err != nil {
		t.Fatalf("doctor config error = %v", err)
	}
	if !strings.Contains(out, `"kind": "DoctorReport"`) || !strings.Contains(out, `"status": "ok"`) {
		t.Fatalf("unexpected doctor output: %s", out)
	}
}

func TestSchemaDiffWritesDbgovAuditEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)
	_, err := executeCommandForTest("--config", filepath.Join(home, "config.yaml"), "schema", "diff", "-f", desired, "--fake")
	if err != nil {
		t.Fatalf("schema diff error = %v", err)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeSchemaDiff || evt.Target.ObjectType != "schema" || evt.Risk != "R3" || !evt.Destructive {
		t.Fatalf("schema diff audit event = %+v", evt)
	}
}

func TestSchemaListDescribeDumpFakeBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	out, err := executeCommandForTest("-o", "json", "schema", "list", "--fake")
	if err != nil {
		t.Fatalf("schema list error = %v", err)
	}
	if !strings.Contains(out, `"kind": "SchemaTableList"`) || !strings.Contains(out, `"users"`) {
		t.Fatalf("unexpected list output: %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeSchemaList || evt.Target.ObjectType != "schema" {
		t.Fatalf("list audit event = %+v", evt)
	}

	out, err = executeCommandForTest("-o", "json", "schema", "describe", "users", "--fake")
	if err != nil {
		t.Fatalf("schema describe error = %v", err)
	}
	if !strings.Contains(out, `"kind": "SchemaDescribe"`) || !strings.Contains(out, `"indexes"`) || !strings.Contains(out, `"foreignKeys"`) {
		t.Fatalf("unexpected describe output: %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeSchemaDescribe ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" {
		t.Fatalf("describe audit event = %+v", evt)
	}

	out, err = executeCommandForTest("-o", "json", "schema", "dump", "--fake")
	if err != nil {
		t.Fatalf("schema dump stdout error = %v", err)
	}
	if !strings.Contains(out, "CREATE TABLE `users`") {
		t.Fatalf("unexpected dump output: %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeSchemaDump {
		t.Fatalf("dump audit event = %+v", evt)
	}
}

func TestSchemaDescribeMissingTableReturnsNotFoundExitCode(t *testing.T) {
	_, err := executeCommandForTest("schema", "describe", "missing", "--fake")
	if err == nil {
		t.Fatal("schema describe missing table error = nil, want resource not found")
	}
	if code := apperrors.ExitCode(err); code != 4 {
		t.Fatalf("ExitCode() = %d, want 4; err = %v", code, err)
	}
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeResourceNotFound {
		t.Fatalf("error code = %s, want %s", got, apperrors.CodeResourceNotFound)
	}
}

func TestSchemaDumpWritesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "schema")
	_, err := executeCommandForTest("--yes", "schema", "dump", "--fake", "--dir", dir)
	if err != nil {
		t.Fatalf("schema dump --dir error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "users.sql"))
	if err != nil {
		t.Fatalf("read dumped ddl: %v", err)
	}
	if !strings.Contains(string(data), "CREATE TABLE `users`") {
		t.Fatalf("dumped ddl = %s", string(data))
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeSchemaDump ||
		evt.Target.Object != "" || evt.Target.Fingerprint == "" {
		t.Fatalf("schema dump directory audit target = %+v", evt)
	}
}

func TestSchemaDumpDirectoryRequiresR1Authorization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "schema")
	_, err := executeCommandForTest("schema", "dump", "--fake", "--dir", dir)
	if apperrors.AsAppError(err).Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("schema dump authorization error = %v, want AUTHORIZATION_REQUIRED", err)
	}
	if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("denied schema dump created target directory: %v", statErr)
	}
}

func TestSchemaPlanFakeBackendShowsRiskDDLAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)

	out, err := executeCommandForTest("-o", "json", "schema", "plan", "-f", desired, "--fake")
	if err != nil {
		t.Fatalf("schema plan error = %v", err)
	}
	for _, want := range []string{
		`"kind": "SchemaPlan"`,
		`"overallRisk": "R3"`,
		`"destructive": true`,
		"ALTER TABLE `users` ADD COLUMN `name` VARCHAR(100);",
		"ALTER TABLE `users` DROP COLUMN `legacy`;",
		"possible column rename",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("schema plan output missing %q:\n%s", want, out)
		}
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeSchemaPlan || evt.Risk != "R3" || !evt.Destructive {
		t.Fatalf("schema plan audit event = %+v", evt)
	}
}

func TestPostgresSchemaPlanUsesPostgresDDL(t *testing.T) {
	t.Parallel()

	backend := postgres.NewWithDB(nil, "app")
	current := schema.Schema{Tables: map[string]schema.Table{
		`evil";--`: {
			Name:    `evil";--`,
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: `name";--`, Type: "text"}},
		},
	}}
	desired := schema.Schema{Tables: map[string]schema.Table{
		`evil";--`: {
			Name:    `evil";--`,
			Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: `name";--`, Type: "character varying(20)"}, {Name: `new";--`, Type: "text"}},
		},
	}}

	plan, err := buildSchemaPlan(backend, current, desired)
	if err != nil {
		t.Fatalf("buildSchemaPlan(postgres) error = %v", err)
	}
	sqlText := schemaPlanSQL(plan)
	for _, want := range []string{
		`ALTER TABLE "public"."evil"";--" ADD COLUMN "new"";--" text;`,
		`ALTER TABLE "public"."evil"";--" ALTER COLUMN "name"";--" TYPE character varying(20);`,
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("postgres plan missing %q:\n%s", want, sqlText)
		}
	}
}

func TestSchemaApplyR1RequiresYesAndExecutesWhenAuthorized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--non-interactive", "schema", "apply", "-f", desired, "--fake")
	if err == nil {
		t.Fatal("expected R1 apply without --yes to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed without authorization: %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.EventType != dbgaudit.EventTypeSchemaApply {
		t.Fatalf("denied audit event = %+v", evt)
	}

	_, err = executeCommandForTest("--yes", "schema", "apply", "-f", desired, "--fake")
	if err != nil {
		t.Fatalf("schema apply R1 error = %v", err)
	}
	if len(backend.Executed) != 1 || !strings.Contains(backend.Executed[0], "ADD COLUMN `name`") {
		t.Fatalf("executed = %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusSucceeded || evt.Risk != "R1" || evt.Executed != 1 {
		t.Fatalf("success audit event = %+v", evt)
	}
}

func TestSchemaApplyR3RequiresTicketAllowFlagAndYes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)

	cases := [][]string{
		{"--yes", "--allow-destructive"},
		{"--yes", "--ticket", "CHG-1"},
		{"--non-interactive", "--ticket", "CHG-1", "--allow-destructive"},
	}
	for _, args := range cases {
		backend := fake.New()
		restore := stubFakeBackend(t, backend)
		fullArgs := append([]string{}, args...)
		fullArgs = append(fullArgs, "schema", "apply", "-f", desired, "--fake")
		_, err := executeCommandForTest(fullArgs...)
		restore()
		if err == nil {
			t.Fatalf("expected R3 apply with args %v to be denied", args)
		}
		if len(backend.Executed) != 0 {
			t.Fatalf("executed for denied args %v: %+v", args, backend.Executed)
		}
	}

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "schema", "apply", "-f", desired, "--fake", "--allow-destructive")
	if err != nil {
		t.Fatalf("schema apply R3 error = %v", err)
	}
	if len(backend.Executed) != 2 {
		t.Fatalf("executed = %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.Risk != "R3" || !evt.Destructive || evt.Executed != 2 {
		t.Fatalf("R3 audit event = %+v", evt)
	}
}

func TestSchemaPlanBindingIsStableAndBindsStateAndTarget(t *testing.T) {
	current := schema.Schema{Tables: map[string]schema.Table{
		"users": {Name: "users", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}},
	}}
	desired := schema.Schema{Tables: map[string]schema.Table{
		"users": {Name: "users", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}, {Name: "name", Type: "TEXT"}}},
	}}
	meta := contextMeta{Name: "dev", Engine: "mysql", Host: "db.example", Port: 3306, Database: "app"}
	backend := fake.New()

	first, err := buildBoundSchemaPlan(backend, meta, current, desired)
	if err != nil {
		t.Fatalf("first plan error = %v", err)
	}
	second, err := buildBoundSchemaPlan(backend, meta, current, desired)
	if err != nil {
		t.Fatalf("second plan error = %v", err)
	}
	if first.PlanFingerprint == "" || first.TargetFingerprint == "" {
		t.Fatalf("plan binding is empty: %+v", first)
	}
	if first.PlanFingerprint != second.PlanFingerprint || first.TargetFingerprint != second.TargetFingerprint {
		t.Fatalf("stable inputs produced different bindings:\nfirst=%+v\nsecond=%+v", first, second)
	}

	drifted := schema.Schema{Tables: map[string]schema.Table{
		"users":  current.Tables["users"],
		"orders": {Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}},
	}}
	afterDrift, err := buildBoundSchemaPlan(backend, meta, drifted, desired)
	if err != nil {
		t.Fatalf("drifted plan error = %v", err)
	}
	if afterDrift.PlanFingerprint == first.PlanFingerprint {
		t.Fatal("current schema drift did not change plan fingerprint")
	}
	if afterDrift.TargetFingerprint != first.TargetFingerprint {
		t.Fatal("current schema drift unexpectedly changed target fingerprint")
	}

	otherTarget := meta
	otherTarget.Host = "other.example"
	retargeted, err := buildBoundSchemaPlan(backend, otherTarget, current, desired)
	if err != nil {
		t.Fatalf("retargeted plan error = %v", err)
	}
	if retargeted.TargetFingerprint == first.TargetFingerprint {
		t.Fatal("database target change did not change target fingerprint")
	}
}

func TestSchemaApplyCommitIndeterminateAuditsEveryStatementAsUncertain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)
	backend := fake.New()
	backend.DDLCommitErr = errors.New("connection lost during commit")
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest(
		"--yes", "--ticket", "CHG-1",
		"schema", "apply", "-f", desired, "--fake", "--allow-destructive",
	)
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodePartialFailure || appErr.Retryable || apperrors.ExitCode(err) != 11 {
		t.Fatalf("schema apply error = %+v, want non-retryable PARTIAL_FAILURE exit 11", appErr)
	}
	if len(backend.Executed) != 2 {
		t.Fatalf("executed = %+v, want both statements attempted before commit", backend.Executed)
	}
	event := lastAuditEvent(t, home)
	if event.Status != dbgaudit.StatusFailed ||
		event.Error == nil ||
		event.Error.Code != string(apperrors.CodePartialFailure) ||
		event.Executed != 2 ||
		event.FailedStatementFingerprint != "" ||
		event.Outcome == nil ||
		event.Outcome.Succeeded != 0 ||
		event.Outcome.Failed != 0 ||
		event.Outcome.Uncertain != 2 ||
		event.Outcome.Skipped != 0 {
		t.Fatalf("commit-indeterminate audit event = %+v", event)
	}
}

func TestSchemaMutationsRejectPostAuthorizationDriftBeforeDDL(t *testing.T) {
	tests := []struct {
		name string
		args func(t *testing.T, home string) []string
	}{
		{
			name: "apply",
			args: func(t *testing.T, home string) []string {
				desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)
				return []string{"--yes", "schema", "apply", "-f", desired, "--fake"}
			},
		},
		{
			name: "import",
			args: func(t *testing.T, _ string) []string {
				dir := t.TempDir()
				writeTestFile(t, dir, "users.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)
				return []string{"--yes", "import", dir, "--fake"}
			},
		},
		{
			name: "reconcile",
			args: func(t *testing.T, _ string) []string {
				dir := t.TempDir()
				writeTestFile(t, dir, "users.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)
				return []string{"--yes", "reconcile", dir, "--fake"}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			baseline := fake.New().Schema
			drifted := schema.Schema{Tables: map[string]schema.Table{
				"users":  baseline.Tables["users"],
				"orders": {Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}},
			}}
			backend := fake.New()
			backend.IntrospectSchemas = []schema.Schema{baseline, drifted}
			restore := stubFakeBackend(t, backend)
			defer restore()

			_, err := executeCommandForTest(test.args(t, home)...)
			if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeConflict {
				t.Fatalf("error = %+v, want CONFLICT", appErr)
			}
			if backend.IntrospectCalls != 2 {
				t.Fatalf("introspection calls = %d, want initial plan plus post-authorization revalidation", backend.IntrospectCalls)
			}
			if len(backend.Executed) != 0 {
				t.Fatalf("DDL executed after drift: %+v", backend.Executed)
			}
			event := lastAuditEvent(t, home)
			if event.Status != dbgaudit.StatusFailed ||
				event.Error == nil ||
				event.Error.Code != string(apperrors.CodeConflict) ||
				event.Outcome == nil ||
				event.Outcome.Succeeded != 0 ||
				event.Outcome.Failed != 0 ||
				event.Outcome.Uncertain != 0 ||
				event.Outcome.Skipped != 1 ||
				event.Executed != 0 {
				t.Fatalf("drift audit event = %+v", event)
			}
			snapshots, listErr := dbgsnapshot.List(snapshotDirForTest(home))
			if listErr != nil {
				t.Fatalf("list snapshots: %v", listErr)
			}
			if len(snapshots) != 0 {
				t.Fatalf("snapshots created after drift: %+v", snapshots)
			}
		})
	}
}

func TestAuthorizeWriteRaisesProtectedR1ToR2(t *testing.T) {
	flags := &cliFlags{Yes: true, NonInteractive: true, trustedOperator: "alice"}
	err := authorizeWrite(flags, safety.R1, contextMeta{Protected: true}, nil, nil)
	if err == nil {
		t.Fatal("expected protected R1 to require ticket after risk upgrade")
	}
}

func TestAuthorizeWriteEnforcesRoles(t *testing.T) {
	roles := map[string]string{
		"alice": safety.RoleReader,
		"bob":   safety.RoleWriter,
		"carol": safety.RoleAdmin,
	}
	meta := contextMeta{Roles: roles}

	if err := authorizeWrite(&cliFlags{trustedOperator: "alice", Yes: true, NonInteractive: true}, safety.R1, meta, nil, nil); err == nil {
		t.Fatal("reader should be denied for R1 writes")
	}
	if err := authorizeWrite(&cliFlags{trustedOperator: "bob", Yes: true, NonInteractive: true, Ticket: "CHG-1"}, safety.R2, meta, nil, nil); err != nil {
		t.Fatalf("writer R2 should pass: %v", err)
	}
	if err := authorizeWrite(&cliFlags{trustedOperator: "bob", Yes: true, NonInteractive: true, Ticket: "CHG-1"}, safety.R3, meta, []safety.AllowFlag{safety.AllowDestructive}, map[safety.AllowFlag]bool{safety.AllowDestructive: true}); err == nil {
		t.Fatal("writer should be denied for R3 writes")
	}
	if err := authorizeWrite(&cliFlags{trustedOperator: "carol", Yes: true, NonInteractive: true, Ticket: "CHG-1"}, safety.R3, meta, []safety.AllowFlag{safety.AllowDestructive}, map[safety.AllowFlag]bool{safety.AllowDestructive: true}); err != nil {
		t.Fatalf("admin R3 should pass: %v", err)
	}
	if err := authorizeWrite(&cliFlags{trustedOperator: "dave", Yes: true, NonInteractive: true}, safety.R1, meta, nil, nil); err == nil {
		t.Fatal("operator without assigned role should be denied")
	}
	if err := authorizeWrite(&cliFlags{trustedOperator: "dave", Yes: true, NonInteractive: true}, safety.R1, contextMeta{}, nil, nil); err != nil {
		t.Fatalf("empty Roles should preserve original write authorization: %v", err)
	}
}

func TestSchemaApplyDryRunDoesNotAuthorizeOrExecute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("-o", "json", "schema", "apply", "-f", desired, "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run apply error = %v", err)
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("dry-run executed statements: %+v", backend.Executed)
	}
	if !strings.Contains(out, `"kind": "SchemaPlan"`) || !strings.Contains(out, `"overallRisk": "R3"`) {
		t.Fatalf("dry-run output = %s", out)
	}
	if evt := lastAuditEvent(t, home); !evt.DryRun || evt.Status != dbgaudit.StatusSucceeded {
		t.Fatalf("dry-run audit event = %+v", evt)
	}
}

func TestSchemaApplyAuditsPartialFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)
	backend := fake.New()
	backend.FailAt = 2
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "schema", "apply", "-f", desired, "--fake", "--allow-destructive")
	if err == nil {
		t.Fatal("expected partial failure")
	}
	if len(backend.Executed) != 1 {
		t.Fatalf("executed = %+v, want one successful statement", backend.Executed)
	}
	evt := lastAuditEvent(t, home)
	if evt.Status != dbgaudit.StatusPartial ||
		evt.Executed != 1 ||
		evt.FailedStatement != "" ||
		evt.FailedStatementFingerprint == "" {
		t.Fatalf("partial failure audit event = %+v", evt)
	}
}

func TestDataExecInsertR1RequiresYesAndAuditsAffectedRows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.DMLAffected = 4
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--non-interactive", "data", "exec", "--sql", "INSERT INTO users(id) VALUES (1)", "--fake")
	if err == nil {
		t.Fatal("expected INSERT without --yes to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed denied insert: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.EventType != dbgaudit.EventTypeDataExec || evt.Risk != "R1" {
		t.Fatalf("denied insert audit event = %+v", evt)
	}

	out, err := executeCommandForTest("--yes", "-o", "json", "data", "exec", "--sql", "INSERT INTO users(id) VALUES (1)", "--fake")
	if err != nil {
		t.Fatalf("insert exec error = %v", err)
	}
	if !strings.Contains(out, `"planFingerprint": "sha256:`) {
		t.Fatalf("insert output missing plan binding: %s", out)
	}
	if len(backend.ExecutedDML) != 1 {
		t.Fatalf("ExecutedDML = %+v", backend.ExecutedDML)
	}
	evt := lastAuditEvent(t, home)
	if evt.Status != dbgaudit.StatusSucceeded ||
		evt.Risk != "R1" ||
		evt.ImpactRows == nil ||
		*evt.ImpactRows != 2 ||
		evt.AffectedRows == nil ||
		*evt.AffectedRows != 4 {
		t.Fatalf("insert audit event = %+v", evt)
	}
}

func TestDataExecInsertSelectLargeImpactRequiresTicketAndBindsPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.ExplainRows = 5000
	backend.DMLAffected = 5000
	restore := stubFakeBackend(t, backend)
	defer restore()
	const statement = "INSERT INTO archive_users SELECT * FROM users"

	_, err := executeCommandForTest("--yes", "data", "exec", "--sql", statement, "--fake")
	if err == nil {
		t.Fatal("expected large INSERT SELECT without ticket to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed denied INSERT SELECT: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied ||
		evt.Risk != "R2" ||
		evt.ImpactRows == nil ||
		*evt.ImpactRows != 5000 {
		t.Fatalf("large INSERT SELECT denied audit event = %+v", evt)
	}

	changedRows := int64(5001)
	backend.BoundExplainRows = &changedRows
	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "data", "exec", "--sql", statement, "--fake")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeConflict {
		t.Fatalf("changed INSERT SELECT plan error code = %s, want %s (err=%v)", got, apperrors.CodeConflict, err)
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed INSERT SELECT after plan change: %+v", backend.ExecutedDML)
	}

	backend.BoundExplainRows = nil
	out, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "-o", "json", "data", "exec", "--sql", statement, "--fake")
	if err != nil {
		t.Fatalf("large INSERT SELECT authorized error = %v", err)
	}
	if !strings.Contains(out, `"planFingerprint": "sha256:`) {
		t.Fatalf("large INSERT SELECT output missing plan binding: %s", out)
	}
	if len(backend.ExecutedDML) != 1 {
		t.Fatalf("ExecutedDML = %+v", backend.ExecutedDML)
	}
}

func TestDataExecUpdateWhereSmallImpactR1(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.ExplainRows = 10
	backend.DMLAffected = 2
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id < 10", "--fake")
	if err != nil {
		t.Fatalf("update exec error = %v", err)
	}
	evt := lastAuditEvent(t, home)
	if evt.Risk != "R1" || evt.ImpactRows == nil || *evt.ImpactRows != 10 || evt.AffectedRows == nil || *evt.AffectedRows != 2 {
		t.Fatalf("small update audit event = %+v", evt)
	}
}

func TestDataExecCommitErrorAuditsUncertainOutcome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.ExplainRows = 10
	backend.DMLAffected = 3
	backend.BoundCommitErr = apperrors.New(apperrors.CodeBackendError, "commit result lost", nil)
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id < 10", "--fake")
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodePartialFailure || appErr.Retryable || !strings.Contains(appErr.Suggestion, "inspect") {
		t.Fatalf("commit error = %+v, want non-retryable partial failure with inspection guidance", appErr)
	}
	if len(backend.ExecutedDML) != 1 {
		t.Fatalf("ExecutedDML = %+v, want one attempted mutation", backend.ExecutedDML)
	}
	event := lastAuditEvent(t, home)
	if event.Status != dbgaudit.StatusFailed ||
		event.Outcome == nil ||
		event.Outcome.Uncertain != 1 ||
		event.Outcome.Succeeded != 0 ||
		event.Outcome.Failed != 0 ||
		event.AffectedRows == nil ||
		*event.AffectedRows != 3 {
		t.Fatalf("commit-indeterminate audit event = %+v", event)
	}
}

func TestDataExecUpdateWhereLargeImpactR2RequiresTicket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.ExplainRows = 5000
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE active=1", "--fake")
	if err == nil {
		t.Fatal("expected large update without ticket to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed denied large update: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.Risk != "R2" || evt.ImpactRows == nil || *evt.ImpactRows != 5000 {
		t.Fatalf("large update denied audit event = %+v", evt)
	}

	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE active=1", "--fake")
	if err != nil {
		t.Fatalf("large update authorized error = %v", err)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusSucceeded || evt.Risk != "R2" {
		t.Fatalf("large update success audit event = %+v", evt)
	}
}

func TestDataExecDeleteNoWhereR3RequiresTicketAllowAndYes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cases := [][]string{
		{"--yes", "--allow-no-where"},
		{"--yes", "--ticket", "CHG-1"},
		{"--non-interactive", "--ticket", "CHG-1", "--allow-no-where"},
	}
	for _, args := range cases {
		backend := fake.New()
		restore := stubFakeBackend(t, backend)
		fullArgs := append([]string{}, args...)
		fullArgs = append(fullArgs, "data", "exec", "--sql", "DELETE FROM users", "--fake")
		_, err := executeCommandForTest(fullArgs...)
		restore()
		if err == nil {
			t.Fatalf("expected no-WHERE delete with args %v to be denied", args)
		}
		if len(backend.ExecutedDML) != 0 {
			t.Fatalf("executed denied no-WHERE delete: %+v", backend.ExecutedDML)
		}
	}

	backend := fake.New()
	backend.ExplainRows = 5000
	restore := stubFakeBackend(t, backend)
	defer restore()
	changedRows := int64(5001)
	backend.BoundExplainRows = &changedRows
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "data", "exec", "--sql", "DELETE FROM users", "--fake", "--allow-no-where")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeConflict {
		t.Fatalf("changed no-WHERE plan error code = %s, want %s (err=%v)", got, apperrors.CodeConflict, err)
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed no-WHERE delete after plan change: %+v", backend.ExecutedDML)
	}

	backend.BoundExplainRows = nil
	out, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "-o", "json", "data", "exec", "--sql", "DELETE FROM users", "--fake", "--allow-no-where")
	if err != nil {
		t.Fatalf("no-WHERE delete authorized error = %v", err)
	}
	if !strings.Contains(out, `"planFingerprint": "sha256:`) {
		t.Fatalf("no-WHERE delete output missing plan binding: %s", out)
	}
	if len(backend.ExecutedDML) != 1 {
		t.Fatalf("ExecutedDML = %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Risk != "R3" ||
		!evt.Destructive ||
		evt.ImpactRows == nil ||
		*evt.ImpactRows != 5000 {
		t.Fatalf("no-WHERE delete audit event = %+v", evt)
	}
}

func TestDataExecProtectedSmallUpdateUpgradesToR2(t *testing.T) {
	flags := &cliFlags{Yes: true, NonInteractive: true, trustedOperator: "alice"}
	err := authorizeWrite(flags, safety.R1, contextMeta{Protected: true}, nil, nil)
	if err == nil {
		t.Fatal("expected protected R1 data exec to require ticket")
	}
}

func TestPostgresDataExecGovernedFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := setupPostgresContext(t, home)
	backend := fake.New()
	backend.ExplainRows = 2
	backend.DMLAffected = 1
	restore := stubPostgresBackend(t, backend)
	defer restore()

	sqlText := "UPDATE users SET name=$$pg$$ WHERE id=1"
	out, err := executeCommandForTest("--config", configPath, "--yes", "-o", "json", "data", "exec", "--sql", sqlText)
	if err != nil {
		t.Fatalf("postgres data exec error = %v", err)
	}
	if len(backend.ExecutedDML) != 1 || backend.ExecutedDML[0] != sqlText {
		t.Fatalf("ExecutedDML = %+v", backend.ExecutedDML)
	}
	if !strings.Contains(out, `"kind": "DataExecResult"`) || !strings.Contains(out, `"affectedRows": 1`) {
		t.Fatalf("postgres data exec output = %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeDataExec || evt.Context.Name != "pg" || evt.Risk != "R1" {
		t.Fatalf("postgres data exec audit = %+v", evt)
	}
}

func TestCtxRoleSetListUnsetAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	operator, err := resolveTrustedOperator()
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "local", "--engine", "mysql", "--host", "127.0.0.1", "--database", "demo")
	if err != nil {
		t.Fatalf("ctx set error = %v", err)
	}
	_, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-role-change", "ctx", "role", "set", "local", "--target-operator", operator, "--role", safety.RoleAdmin)
	if err != nil {
		t.Fatalf("ctx role set error = %v", err)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeRoleAssign ||
		evt.Role != safety.RoleAdmin ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" ||
		evt.Context.Name != "local" ||
		evt.Risk != "R3" {
		t.Fatalf("role assign audit event = %+v", evt)
	}

	out, err := executeCommandForTest("--config", configPath, "-o", "json", "ctx", "role", "list", "local")
	if err != nil {
		t.Fatalf("ctx role list error = %v", err)
	}
	if !strings.Contains(out, `"operator": `) || !strings.Contains(out, `"role": "admin"`) {
		t.Fatalf("ctx role list output = %s", out)
	}

	_, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-role-change", "ctx", "role", "unset", "local", "--target-operator", operator)
	if err != nil {
		t.Fatalf("ctx role unset error = %v", err)
	}
	evt = lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeRoleRevoke ||
		evt.Role != "" ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" ||
		evt.Context.Name != "local" ||
		evt.Risk != "R3" {
		t.Fatalf("role revoke audit event = %+v", evt)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Contexts["local"].Roles != nil {
		t.Fatalf("roles after unset = %+v, want nil", cfg.Contexts["local"].Roles)
	}
}

func TestCtxRoleValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	if _, err := executeCommandForTest("--config", configPath, "ctx", "role", "set", "missing", "--target-operator", "alice", "--role", safety.RoleWriter); err == nil {
		t.Fatal("expected missing context to fail")
	}
	_, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "local", "--engine", "mysql", "--host", "127.0.0.1")
	if err != nil {
		t.Fatalf("ctx set error = %v", err)
	}
	if _, err := executeCommandForTest("--config", configPath, "ctx", "role", "set", "local", "--role", safety.RoleWriter); err == nil {
		t.Fatal("expected missing --target-operator to fail")
	}
	if _, err := executeCommandForTest("--config", configPath, "ctx", "role", "set", "local", "--target-operator", "alice", "--role", "owner"); err == nil {
		t.Fatal("expected invalid role to fail")
	}
}

func TestCtxMigrateCredentialsMigratesOnlyLiteralPasswords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_MASTER_PASSWORD", "test-passphrase")
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("literal", dbgovctx.Context{Base: corectx.Base{Password: "secret"}, Engine: "mysql", Host: "127.0.0.1", Port: 3306}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("ref", dbgovctx.Context{Base: corectx.Base{Password: corecredstore.EncodeRef("encrypted-file"), CredentialBackend: "encrypted-file"}, Engine: "mysql", Host: "127.0.0.1", Port: 3306}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("empty", dbgovctx.Context{Engine: "mysql", Host: "127.0.0.1", Port: 3306}); err != nil {
		t.Fatal(err)
	}

	out, err := executeCommandForTest("--config", configPath, "-o", "json", "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "migrate-credentials", "--to", "encrypted-file")
	if err != nil {
		t.Fatalf("ctx migrate-credentials error = %v", err)
	}
	if !strings.Contains(out, `"migrated": 1`) || !strings.Contains(out, `"backend": "encrypted-file"`) {
		t.Fatalf("migrate output = %s", out)
	}
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Contexts["literal"].Password != corecredstore.EncodeRef("encrypted-file") || cfg.Contexts["literal"].CredentialBackend != "encrypted-file" {
		t.Fatalf("literal context after migration = %+v", cfg.Contexts["literal"])
	}
	if cfg.Contexts["ref"].Password != corecredstore.EncodeRef("encrypted-file") || cfg.Contexts["empty"].Password != "" {
		t.Fatalf("non-candidates changed: ref=%+v empty=%+v", cfg.Contexts["ref"], cfg.Contexts["empty"])
	}
	resolved, err := cfg.Contexts["literal"].ResolvePasswordContext(commandContext(&cliFlags{}), "literal")
	if err != nil {
		t.Fatalf("resolve migrated password: %v", err)
	}
	if resolved != "secret" {
		t.Fatalf("resolved migrated password = %q", resolved)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeCredentialMigrate ||
		evt.Context.Name != "literal" ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" {
		t.Fatalf("credential migrate audit event = %+v", evt)
	}
}

func TestCtxMigrateCredentialsContextFilterAndNoCandidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_MASTER_PASSWORD", "test-passphrase")
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("prod", dbgovctx.Context{Base: corectx.Base{Password: "prod-secret"}, Engine: "mysql", Host: "127.0.0.1", Port: 3306}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("dev", dbgovctx.Context{Base: corectx.Base{Password: "dev-secret"}, Engine: "mysql", Host: "127.0.0.1", Port: 3306}); err != nil {
		t.Fatal(err)
	}

	_, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "migrate-credentials", "--to", "encrypted-file", "--context", "prod")
	if err != nil {
		t.Fatalf("filtered migrate error = %v", err)
	}
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Contexts["prod"].Password != corecredstore.EncodeRef("encrypted-file") || cfg.Contexts["dev"].Password != "dev-secret" {
		t.Fatalf("context filter changed wrong contexts: prod=%+v dev=%+v", cfg.Contexts["prod"], cfg.Contexts["dev"])
	}

	out, err := executeCommandForTest("--config", configPath, "-o", "json", "ctx", "migrate-credentials", "--to", "encrypted-file", "--context", "prod")
	if err != nil {
		t.Fatalf("no-candidate migrate error = %v", err)
	}
	if !strings.Contains(out, `"migrated": 0`) {
		t.Fatalf("no-candidate output = %s", out)
	}
}

func TestCtxMigrateCredentialsValidationAndBackendUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("local", dbgovctx.Context{Base: corectx.Base{Password: "secret"}, Engine: "mysql", Host: "127.0.0.1", Port: 3306}); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"--config", configPath, "ctx", "migrate-credentials"},
		{"--config", configPath, "ctx", "migrate-credentials", "--to", "plain-yaml"},
		{"--config", configPath, "ctx", "migrate-credentials", "--to", "vault"},
		{"--config", configPath, "ctx", "migrate-credentials", "--to", "unknown"},
	} {
		if _, err := executeCommandForTest(args...); err == nil {
			t.Fatalf("expected args %v to fail", args)
		}
	}
	if _, err := executeCommandForTest("--config", configPath, "ctx", "migrate-credentials", "--to", "encrypted-file"); err == nil {
		t.Fatal("expected encrypted-file without master password to fail")
	}
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Contexts["local"].Password != "secret" || cfg.Contexts["local"].CredentialBackend != "" {
		t.Fatalf("context changed despite unavailable backend: %+v", cfg.Contexts["local"])
	}
}

func TestCtxExportRedactsCredentialByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("local", dbgovctx.Context{
		Base:   corectx.Base{Password: "secret", CredentialBackend: "plain-yaml", Env: "dev"},
		Engine: "mysql",
		Host:   "127.0.0.1",
		Port:   3306,
	}); err != nil {
		t.Fatal(err)
	}

	out, err := executeCommandForTest("--config", configPath, "ctx", "export", "local")
	if err != nil {
		t.Fatalf("ctx export error = %v", err)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("ctx export leaked password:\n%s", out)
	}
	for _, want := range []string{
		"apiVersion: dbgov-cli.io/ctx-export/v1",
		"name: local",
		"password: <REDACTED>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ctx export output missing %q:\n%s", want, out)
		}
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeContextExport || evt.Context.Name != "local" {
		t.Fatalf("ctx export audit event = %+v", evt)
	}
}

func TestCtxExportIncludeCredentialsRejectsNonPlainBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("prod", dbgovctx.Context{
		Base:   corectx.Base{Password: corecredstore.EncodeRef("keychain"), CredentialBackend: "keychain"},
		Engine: "mysql",
		Host:   "127.0.0.1",
		Port:   3306,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := executeCommandForTest("--config", configPath, "ctx", "export", "prod", "--include-credentials")
	if err == nil {
		t.Fatal("expected ctx export --include-credentials to reject secure backend")
	}
	if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeCredentialStoreError {
		t.Fatalf("error code = %s, want %s", appErr.Code, apperrors.CodeCredentialStoreError)
	}
	if !strings.Contains(err.Error(), "migrate to plain-yaml first or share out-of-band") {
		t.Fatalf("error missing operator hint: %v", err)
	}
}

func TestCtxImportRedactedCredentialClearsPasswordAndAudits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	importPath := writeTestFile(t, home, "ctx.yaml", `apiVersion: dbgov-cli.io/ctx-export/v1
name: dev
context:
    engine: mysql
    host: 127.0.0.1
    port: 3306
    database: appdb
    username: root
    password: <REDACTED>
    credentialBackend: plain-yaml
    env: dev
`)

	out, err := executeCommandForTest("--config", configPath, "-o", "json", "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "import", "-f", importPath)
	if err != nil {
		t.Fatalf("ctx import error = %v", err)
	}
	if !strings.Contains(out, `"name": "dev"`) || !strings.Contains(out, `"credentialRedacted": true`) {
		t.Fatalf("ctx import output = %s", out)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Contexts["dev"].Password; got != "" {
		t.Fatalf("imported password = %q, want empty", got)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeContextImport || evt.Context.Name != "dev" {
		t.Fatalf("ctx import audit event = %+v", evt)
	}
}

func TestCtxImportVersionRenameForceAndNonInteractive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	badPath := writeTestFile(t, home, "bad.yaml", "apiVersion: wrong/v1\nname: dev\ncontext: {}\n")
	_, err := executeCommandForTest("--config", configPath, "ctx", "import", "-f", badPath)
	if err == nil {
		t.Fatal("expected unsupported apiVersion")
	}
	if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeUnsupportedProtocol {
		t.Fatalf("error code = %s, want %s", appErr.Code, apperrors.CodeUnsupportedProtocol)
	}

	importPath := writeTestFile(t, home, "good.yaml", `apiVersion: dbgov-cli.io/ctx-export/v1
name: dev
context:
    engine: mysql
    host: 127.0.0.1
    port: 3306
    password: secret
    credentialBackend: plain-yaml
`)
	_, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "import", "-f", importPath, "--rename", "copy")
	if err != nil {
		t.Fatalf("ctx import --rename error = %v", err)
	}
	_, err = executeCommandForTest("--config", configPath, "ctx", "import", "-f", importPath, "--rename", "copy")
	if err == nil {
		t.Fatal("expected existing context import to require --force")
	}
	_, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "import", "-f", importPath, "--rename", "copy", "--force")
	if err != nil {
		t.Fatalf("ctx import --force error = %v", err)
	}
	legacyPath := writeTestFile(t, home, "legacy.yaml", `apiVersion: dbgov.io/ctx-export/v1
name: legacy
context:
    engine: mysql
    host: 127.0.0.1
    port: 3306
`)
	_, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "import", "-f", legacyPath)
	if err != nil {
		t.Fatalf("legacy ctx import error = %v", err)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Contexts["copy"].Password; got != "secret" {
		t.Fatalf("renamed context password = %q, want secret", got)
	}
	if _, ok := cfg.Contexts["legacy"]; !ok {
		t.Fatalf("legacy context was not imported: %#v", cfg.Contexts)
	}

	_, err = executeCommandForTest("--non-interactive", "--config", filepath.Join(home, "other.yaml"), "ctx", "import", "-f", importPath)
	if err == nil {
		t.Fatal("expected non-interactive import without --yes to fail")
	}
	if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeAuthorizationRequired {
		t.Fatalf("error code = %s, want %s", appErr.Code, apperrors.CodeAuthorizationRequired)
	}
}

func TestDataExecProtectedContextCommandRequiresTicket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("prod", dbgovctx.Context{
		Base:     corectx.Base{Protected: true},
		Engine:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "appdb",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("prod"); err != nil {
		t.Fatal(err)
	}
	backend := fake.New()
	backend.ExplainRows = 10
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--config", configPath, "--context", "prod", "--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake")
	if err == nil {
		t.Fatal("expected protected small update without ticket to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed protected denied update: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.Risk != "R2" {
		t.Fatalf("protected denied audit event = %+v", evt)
	}
}

func TestDataExecDryRunDoesNotAuthorizeOrExecute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.ExplainRows = 12
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("-o", "json", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run data exec error = %v", err)
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("dry-run executed DML: %+v", backend.ExecutedDML)
	}
	if !strings.Contains(out, `"kind": "DataExecPlan"`) ||
		!strings.Contains(out, `"impactRows": 12`) ||
		!strings.Contains(out, `"planFingerprint": "sha256:`) {
		t.Fatalf("dry-run output = %s", out)
	}
	if evt := lastAuditEvent(t, home); !evt.DryRun || evt.ImpactRows == nil || *evt.ImpactRows != 12 {
		t.Fatalf("dry-run audit event = %+v", evt)
	}
}

func TestDataExecBlocksWhenPlanChangesAfterAuthorization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.ExplainRows = 10
	changedRows := int64(5000)
	backend.BoundExplainRows = &changedRows
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest(
		"--yes",
		"data", "exec",
		"--sql", "UPDATE users SET name='x' WHERE id=1",
		"--fake",
	)
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeConflict {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeConflict, err)
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed DML after plan change: %+v", backend.ExecutedDML)
	}
	event := lastAuditEvent(t, home)
	if event.Status != dbgaudit.StatusFailed ||
		event.Error == nil ||
		event.Error.Code != string(apperrors.CodeConflict) {
		t.Fatalf("plan-change audit event = %+v", event)
	}
}

func TestDataExecRejectsNonDMLAndExplainFailure(t *testing.T) {
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()
	if _, err := executeCommandForTest("data", "exec", "--sql", "SELECT * FROM users", "--fake"); err == nil {
		t.Fatal("expected SELECT to be rejected")
	}
	if _, err := executeCommandForTest("data", "exec", "--sql", "CREATE TABLE t (id BIGINT)", "--fake"); err == nil {
		t.Fatal("expected DDL to be rejected")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed rejected SQL: %+v", backend.ExecutedDML)
	}

	backend.ExplainErr = errFakeExplain
	if _, err := executeCommandForTest("data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake"); err == nil {
		t.Fatal("expected EXPLAIN failure to stop data exec")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed after explain failure: %+v", backend.ExecutedDML)
	}
}

func TestBuildDataExecPlanRequiresExplainBindingForEveryDMLKind(t *testing.T) {
	backend := fixedExplainBackend{result: dbbackend.ExplainResult{EstimatedRows: 1}}
	tests := []struct {
		name      string
		statement string
		kind      sqlclass.Kind
		hasWhere  bool
	}{
		{name: "insert", statement: "INSERT INTO users(id) VALUES (1)", kind: sqlclass.KindInsert},
		{name: "update with where", statement: "UPDATE users SET active=0 WHERE id=1", kind: sqlclass.KindUpdate, hasWhere: true},
		{name: "update without where", statement: "UPDATE users SET active=0", kind: sqlclass.KindUpdate},
		{name: "delete with where", statement: "DELETE FROM users WHERE id=1", kind: sqlclass.KindDelete, hasWhere: true},
		{name: "delete without where", statement: "DELETE FROM users", kind: sqlclass.KindDelete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := buildDataExecPlan(
				context.Background(),
				backend,
				test.statement,
				test.kind,
				test.hasWhere,
			); err == nil {
				t.Fatal("buildDataExecPlan() error = nil, want missing plan fingerprint rejection")
			}
		})
	}
}

func TestExportWritesSchemaDirectoryAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	backend.DDLs = map[string]string{
		"users":  "CREATE TABLE `users` (`id` BIGINT);",
		"orders": "CREATE TABLE `orders` (`id` BIGINT);",
	}
	restore := stubFakeBackend(t, backend)
	defer restore()
	dir := filepath.Join(home, "schema")

	_, err := executeCommandForTest("--yes", "export", "--dir", dir, "--fake")
	if err != nil {
		t.Fatalf("export error = %v", err)
	}
	for _, table := range []string{"users", "orders"} {
		data, err := os.ReadFile(filepath.Join(dir, table+".sql"))
		if err != nil {
			t.Fatalf("read exported %s.sql: %v", table, err)
		}
		if !strings.Contains(string(data), "CREATE TABLE `"+table+"`") {
			t.Fatalf("%s.sql = %s", table, data)
		}
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeExport ||
		evt.Status != dbgaudit.StatusSucceeded ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" {
		t.Fatalf("export audit event = %+v", evt)
	}
}

func TestImportR1RequiresYesAndExecutesMultiTablePlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));")
	writeTestFile(t, dir, "orders.sql", "CREATE TABLE orders (id BIGINT);")

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()
	_, err := executeCommandForTest("--non-interactive", "import", dir, "--fake")
	if err == nil {
		t.Fatal("expected R1 import without --yes to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied import: %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeImport || evt.Status != dbgaudit.StatusDenied || evt.Risk != "R1" {
		t.Fatalf("denied import audit event = %+v", evt)
	}

	_, err = executeCommandForTest("--yes", "import", dir, "--fake")
	if err != nil {
		t.Fatalf("import R1 error = %v", err)
	}
	if len(backend.Executed) != 2 {
		t.Fatalf("executed = %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeImport || evt.Status != dbgaudit.StatusSucceeded || evt.Executed != 2 {
		t.Fatalf("success import audit event = %+v", evt)
	}
}

func TestPostgresGitOpsAndRollbackUsePostgresDDL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := setupPostgresContext(t, home)

	importDir := t.TempDir()
	writeTestFile(t, importDir, "users.sql", `CREATE TABLE users (id integer, name text);`)
	importBackend := newPostgresFakeBackend(schema.Schema{Tables: map[string]schema.Table{}})
	restore := stubPostgresBackend(t, importBackend)
	out, err := executeCommandForTest("--config", configPath, "--yes", "-o", "json", "import", importDir)
	restore()
	if err != nil {
		t.Fatalf("postgres import error = %v\n%s", err, out)
	}
	if len(importBackend.Executed) != 1 || importBackend.Executed[0] != `CREATE TABLE "public"."users" ("id" integer, "name" text);` {
		t.Fatalf("postgres import executed = %+v", importBackend.Executed)
	}

	reconcileDir := t.TempDir()
	writeTestFile(t, reconcileDir, "users.sql", `CREATE TABLE users (id integer);`)
	reconcileBackend := newPostgresFakeBackend(schema.Schema{Tables: map[string]schema.Table{
		"users": {Name: "users", Columns: []schema.Column{{Name: "id", Type: "integer"}, {Name: "legacy", Type: "text"}}},
	}})
	reconcileBackend.DDLs["users"] = `CREATE TABLE "users" ("id" integer, "legacy" text);`
	restore = stubPostgresBackend(t, reconcileBackend)
	out, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "CHG-1", "-o", "json", "reconcile", reconcileDir, "--allow-destructive")
	restore()
	if err != nil {
		t.Fatalf("postgres reconcile error = %v\n%s", err, out)
	}
	if len(reconcileBackend.Executed) != 1 || reconcileBackend.Executed[0] != `ALTER TABLE "public"."users" DROP COLUMN "legacy";` {
		t.Fatalf("postgres reconcile executed = %+v", reconcileBackend.Executed)
	}

	sourceID := captureSnapshotWithMetaForTest(
		t,
		home,
		dbgsnapshot.Meta{
			Operator: "tester",
			Command:  "apply",
			Context:  "pg",
			Target: &dbgsnapshot.Target{
				Context:  "pg",
				Engine:   "postgres",
				Host:     "127.0.0.1",
				Port:     5432,
				Database: "app",
				Schema:   "public",
			},
		},
		map[string]string{
			"users": `CREATE TABLE "public"."users" ("id" integer, "name" text);`,
		},
	)
	rollbackBackend := newPostgresFakeBackend(schema.Schema{Tables: map[string]schema.Table{}})
	restore = stubPostgresBackend(t, rollbackBackend)
	out, err = executeCommandForTest("--config", configPath, "--yes", "--ticket", "CHG-2", "-o", "json", "rollback", "--to", sourceID)
	restore()
	if err != nil {
		t.Fatalf("postgres rollback error = %v\n%s", err, out)
	}
	if len(rollbackBackend.Executed) != 1 || rollbackBackend.Executed[0] != `CREATE TABLE "public"."users" ("id" integer, "name" text);` {
		t.Fatalf("postgres rollback executed = %+v", rollbackBackend.Executed)
	}
	if !strings.Contains(out, rollbackDataLossWarning) {
		t.Fatalf("postgres rollback output missing data-loss warning:\n%s", out)
	}
}

func TestImportR3RequiresTicketAllowDestructive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id TEXT);")
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "import", dir, "--fake")
	if err == nil {
		t.Fatal("expected destructive import without allow flag to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied destructive import: %+v", backend.Executed)
	}

	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "import", dir, "--fake", "--allow-destructive")
	if err != nil {
		t.Fatalf("destructive import error = %v", err)
	}
	if len(backend.Executed) != 2 {
		t.Fatalf("executed = %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeImport || evt.Risk != "R3" || !evt.Destructive {
		t.Fatalf("destructive import audit event = %+v", evt)
	}
}

func TestImportDryRunAndNoDropTableForMissingDesiredTable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));")
	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("-o", "json", "import", dir, "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run import error = %v", err)
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("dry-run executed import: %+v", backend.Executed)
	}
	if strings.Contains(out, "DROP TABLE") || strings.Contains(out, "DROP_TABLE") {
		t.Fatalf("import dry-run planned table deletion:\n%s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeImport || !evt.DryRun {
		t.Fatalf("dry-run import audit event = %+v", evt)
	}
}

func TestReconcileWithoutPruneReportsDriftAndExecutesInTableChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));")
	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("-o", "json", "--yes", "reconcile", dir, "--fake")
	if err != nil {
		t.Fatalf("reconcile without prune error = %v", err)
	}
	if len(backend.Executed) != 1 || !strings.Contains(backend.Executed[0], "ADD COLUMN `name`") {
		t.Fatalf("executed = %+v, want only in-table add column", backend.Executed)
	}
	if strings.Contains(out, "DROP TABLE") || strings.Contains(out, "DROP_TABLE") {
		t.Fatalf("reconcile without prune planned table deletion:\n%s", out)
	}
	if !strings.Contains(out, "not pruned") || !strings.Contains(out, "orders") {
		t.Fatalf("reconcile without prune output missing drift warning:\n%s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeReconcile || evt.Status != dbgaudit.StatusSucceeded || evt.Risk != "R1" {
		t.Fatalf("reconcile audit event = %+v", evt)
	}
}

func TestReconcilePruneRequiresProductionPruneAllowFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT, legacy TEXT);")

	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "reconcile", dir, "--fake", "--prune")
	restore()
	if err == nil {
		t.Fatal("expected prune reconcile without --allow-production-prune to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied prune: %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeReconcile || evt.Status != dbgaudit.StatusDenied || evt.Risk != "R3" {
		t.Fatalf("denied prune audit event = %+v", evt)
	}

	backend = fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore = stubFakeBackend(t, backend)
	defer restore()
	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "reconcile", dir, "--fake", "--prune", "--allow-production-prune")
	if err != nil {
		t.Fatalf("authorized prune reconcile error = %v", err)
	}
	if len(backend.Executed) != 1 || !strings.Contains(backend.Executed[0], "DROP TABLE `orders`") {
		t.Fatalf("executed = %+v, want DROP TABLE orders", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeReconcile || evt.Risk != "R3" || !evt.Destructive || evt.Executed != 1 {
		t.Fatalf("authorized prune audit event = %+v", evt)
	}
}

func TestReconcilePruneAndDestructiveColumnRequireBothAllowFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT);")

	cases := [][]string{
		{"--yes", "--ticket", "CHG-1", "reconcile", dir, "--fake", "--prune", "--allow-production-prune"},
		{"--yes", "--ticket", "CHG-1", "reconcile", dir, "--fake", "--prune", "--allow-destructive"},
	}
	for _, args := range cases {
		backend := fake.New()
		backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
		restore := stubFakeBackend(t, backend)
		_, err := executeCommandForTest(args...)
		restore()
		if err == nil {
			t.Fatalf("expected reconcile with args %v to be denied", args)
		}
		if len(backend.Executed) != 0 {
			t.Fatalf("executed denied reconcile with args %v: %+v", args, backend.Executed)
		}
	}

	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	defer restore()
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "reconcile", dir, "--fake", "--prune", "--allow-destructive", "--allow-production-prune")
	if err != nil {
		t.Fatalf("reconcile with both allow flags error = %v", err)
	}
	if len(backend.Executed) != 2 {
		t.Fatalf("executed = %+v, want drop column and drop table", backend.Executed)
	}
	if !strings.Contains(strings.Join(backend.Executed, "\n"), "DROP COLUMN `legacy`") || !strings.Contains(strings.Join(backend.Executed, "\n"), "DROP TABLE `orders`") {
		t.Fatalf("executed = %+v, want both destructive statements", backend.Executed)
	}
}

func TestReconcileDryRunDoesNotAuthorizeOrExecute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT);")
	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("-o", "json", "reconcile", dir, "--fake", "--prune", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run reconcile error = %v", err)
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("dry-run executed reconcile: %+v", backend.Executed)
	}
	if !strings.Contains(out, "DROP TABLE `orders`") || !strings.Contains(out, "DROP COLUMN `legacy`") {
		t.Fatalf("dry-run reconcile output missing prune/destructive statements:\n%s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeReconcile || !evt.DryRun || evt.Status != dbgaudit.StatusSucceeded {
		t.Fatalf("dry-run reconcile audit event = %+v", evt)
	}
}

func TestSchemaApplyCapturesSnapshotBeforeDDLAndAuditsID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "schema", "apply", "-f", desired, "--fake")
	if err != nil {
		t.Fatalf("schema apply error = %v", err)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeSchemaApply ||
		evt.SnapshotID != "" ||
		evt.SnapshotFingerprint == "" {
		t.Fatalf("schema apply audit event = %+v, want snapshot fingerprint only", evt)
	}
	metas, err := dbgsnapshot.List(snapshotDirForTest(home))
	if err != nil || len(metas) != 1 {
		t.Fatalf("list snapshots: metas=%+v err=%v", metas, err)
	}
	snap, err := dbgsnapshot.Load(snapshotDirForTest(home), metas[0].ID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snap.Meta.Command != "apply" ||
		snap.Meta.Context != "fake" ||
		snap.Meta.Target == nil ||
		*snap.Meta.Target != *fakeSnapshotTarget() ||
		snap.Meta.TableCount != 1 ||
		!strings.Contains(snap.Tables["users"], "CREATE TABLE") {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestSchemaApplyDryRunDoesNotCaptureSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("schema", "apply", "-f", desired, "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run schema apply error = %v", err)
	}
	metas, err := dbgsnapshot.List(snapshotDirForTest(home))
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("dry-run snapshots = %+v, want none", metas)
	}
}

func TestSchemaApplySnapshotFailureStopsBeforeDDL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)
	backend := fake.New()
	backend.TableDDLErr = errors.New("snapshot DDL failure")
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("--yes", "schema", "apply", "-f", desired, "--fake")
	if err == nil {
		t.Fatal("expected snapshot capture failure")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed despite snapshot failure: %+v", backend.Executed)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeSchemaApply || evt.Status != dbgaudit.StatusFailed || evt.SnapshotID != "" {
		t.Fatalf("snapshot failure audit event = %+v", evt)
	}
}

func TestImportAndReconcileCaptureSnapshots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	importDir := t.TempDir()
	writeTestFile(t, importDir, "users.sql", "CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));")
	reconcileDir := t.TempDir()
	writeTestFile(t, reconcileDir, "users.sql", "CREATE TABLE users (id BIGINT);")

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	_, err := executeCommandForTest("--yes", "import", importDir, "--fake")
	restore()
	if err != nil {
		t.Fatalf("import error = %v", err)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeImport ||
		evt.SnapshotID != "" ||
		evt.SnapshotFingerprint == "" {
		t.Fatalf("import audit event = %+v, want snapshot fingerprint", evt)
	}

	backend = fake.New()
	restore = stubFakeBackend(t, backend)
	defer restore()
	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "reconcile", reconcileDir, "--fake", "--allow-destructive")
	if err != nil {
		t.Fatalf("reconcile error = %v", err)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeReconcile ||
		evt.SnapshotID != "" ||
		evt.SnapshotFingerprint == "" {
		t.Fatalf("reconcile audit event = %+v, want snapshot fingerprint", evt)
	}
	metas, err := dbgsnapshot.List(snapshotDirForTest(home))
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("snapshot count = %d, want 2: %+v", len(metas), metas)
	}
}

func TestRollbackListOutputsSnapshotsAndAudits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	captureSnapshotWithMetaForTest(
		t,
		home,
		dbgsnapshot.Meta{Operator: "alice", Command: "apply", Ticket: "CHG-1", Context: "dev", TableCount: 1},
		map[string]string{"users": "CREATE TABLE `users` (`id` BIGINT);"},
	)
	captureSnapshotWithMetaForTest(
		t,
		home,
		dbgsnapshot.Meta{Operator: "bob", Command: "reconcile", Ticket: "CHG-2", Context: "prod", TableCount: 2},
		map[string]string{"users": "CREATE TABLE `users` (`id` BIGINT);"},
	)

	out, err := executeCommandForTest("-o", "json", "rollback", "list")
	if err != nil {
		t.Fatalf("rollback list error = %v", err)
	}
	if !strings.Contains(out, `"kind": "RollbackSnapshotList"`) || !strings.Contains(out, `"command": "reconcile"`) || !strings.Contains(out, `"command": "apply"`) {
		t.Fatalf("rollback list output = %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeRollback ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" {
		t.Fatalf("rollback list audit event = %+v", evt)
	}
}

func TestRollbackListEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, err := executeCommandForTest("-o", "json", "rollback", "list")
	if err != nil {
		t.Fatalf("empty rollback list error = %v", err)
	}
	if !strings.Contains(out, `"snapshots": []`) {
		t.Fatalf("empty rollback list output = %s", out)
	}
}

func TestMaxRisk(t *testing.T) {
	if got := maxRisk(safety.R1, safety.R2); got != safety.R2 {
		t.Fatalf("maxRisk(R1,R2) = %v, want R2", got)
	}
	if got := maxRisk(safety.R3, safety.R2); got != safety.R3 {
		t.Fatalf("maxRisk(R3,R2) = %v, want R3", got)
	}
}

func TestRollbackToIncrementalRestoreHasR2FloorAndAuditsSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sourceID := captureSnapshotForTest(t, home, map[string]string{
		"users":  "CREATE TABLE users (id BIGINT, legacy TEXT);",
		"orders": "CREATE TABLE orders (id BIGINT);",
	})

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	_, err := executeCommandForTest("--yes", "rollback", "--to", sourceID, "--fake")
	restore()
	if err == nil {
		t.Fatal("expected rollback R2 floor to require --ticket")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied rollback: %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeRollback ||
		evt.Status != dbgaudit.StatusDenied ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" ||
		evt.Risk != "R2" {
		t.Fatalf("denied rollback audit event = %+v", evt)
	}

	backend = fake.New()
	restore = stubFakeBackend(t, backend)
	defer restore()
	out, err := executeCommandForTest("-o", "json", "--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake")
	if err != nil {
		t.Fatalf("authorized rollback error = %v", err)
	}
	if len(backend.Executed) != 1 || !strings.Contains(backend.Executed[0], "CREATE TABLE `orders`") {
		t.Fatalf("executed = %+v, want CREATE TABLE orders", backend.Executed)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeRollback ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" ||
		evt.SnapshotID != "" ||
		evt.SnapshotFingerprint == "" ||
		evt.Risk != "R2" {
		t.Fatalf("rollback audit event = %+v", evt)
	}
	for _, want := range []string{
		`"kind": "RollbackResult"`,
		`"scope": "schema-structure"`,
		`"plannedStatements": 1`,
		`"appliedStatements": 1`,
		`"dataRestored": false`,
		`"planFingerprint": "sha256:`,
		`"targetFingerprint": "sha256:`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rollback result missing %q:\n%s", want, out)
		}
	}
}

func TestRollbackToDestructiveRestoreRequiresAllowDestructive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sourceID := captureSnapshotForTest(t, home, map[string]string{
		"users": "CREATE TABLE users (id BIGINT);",
	})

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake")
	restore()
	if err == nil {
		t.Fatal("expected destructive rollback without --allow-destructive to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied destructive rollback: %+v", backend.Executed)
	}

	backend = fake.New()
	restore = stubFakeBackend(t, backend)
	defer restore()
	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake", "--allow-destructive")
	if err != nil {
		t.Fatalf("destructive rollback error = %v", err)
	}
	if len(backend.Executed) != 1 || !strings.Contains(backend.Executed[0], "DROP COLUMN `legacy`") {
		t.Fatalf("executed = %+v, want DROP COLUMN legacy", backend.Executed)
	}
}

func TestRollbackToPruneRestoreRequiresAllowProductionPrune(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sourceID := captureSnapshotForTest(t, home, map[string]string{
		"users": "CREATE TABLE users (id BIGINT, legacy TEXT);",
	})
	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake")
	restore()
	if err == nil {
		t.Fatal("expected prune rollback without --allow-production-prune to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied prune rollback: %+v", backend.Executed)
	}

	backend = fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore = stubFakeBackend(t, backend)
	defer restore()
	_, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake", "--allow-production-prune")
	if err != nil {
		t.Fatalf("prune rollback error = %v", err)
	}
	if len(backend.Executed) != 1 || !strings.Contains(backend.Executed[0], "DROP TABLE `orders`") {
		t.Fatalf("executed = %+v, want DROP TABLE orders", backend.Executed)
	}
}

func TestRollbackToRequiresBothAllowFlagsForDropColumnAndDropTable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sourceID := captureSnapshotForTest(t, home, map[string]string{
		"users": "CREATE TABLE users (id BIGINT);",
	})
	cases := [][]string{
		{"--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake", "--allow-production-prune"},
		{"--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake", "--allow-destructive"},
	}
	for _, args := range cases {
		backend := fake.New()
		backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
		restore := stubFakeBackend(t, backend)
		_, err := executeCommandForTest(args...)
		restore()
		if err == nil {
			t.Fatalf("expected rollback args %v to be denied", args)
		}
		if len(backend.Executed) != 0 {
			t.Fatalf("executed denied rollback args %v: %+v", args, backend.Executed)
		}
	}

	backend := fake.New()
	backend.Schema.Tables["orders"] = schema.Table{Name: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}}}
	restore := stubFakeBackend(t, backend)
	defer restore()
	_, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "rollback", "--to", sourceID, "--fake", "--allow-destructive", "--allow-production-prune")
	if err != nil {
		t.Fatalf("rollback with both allow flags error = %v", err)
	}
	if joined := strings.Join(backend.Executed, "\n"); !strings.Contains(joined, "DROP COLUMN `legacy`") || !strings.Contains(joined, "DROP TABLE `orders`") {
		t.Fatalf("executed = %+v, want drop column and drop table", backend.Executed)
	}
}

func TestRollbackToDryRunWarnsAndDoesNotCaptureOrExecute(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sourceID := captureSnapshotForTest(t, home, map[string]string{
		"users": "CREATE TABLE users (id BIGINT);",
	})
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	out, err := executeCommandForTest("-o", "json", "rollback", "--to", sourceID, "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run rollback error = %v", err)
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("dry-run executed rollback: %+v", backend.Executed)
	}
	if !strings.Contains(out, `"kind": "SchemaPlan"`) ||
		!strings.Contains(out, `"plannedStatements": 1`) ||
		!strings.Contains(out, `"planFingerprint": "sha256:`) ||
		!strings.Contains(out, `"targetFingerprint": "sha256:`) ||
		!strings.Contains(out, "data in dropped tables/columns is NOT recovered") {
		t.Fatalf("dry-run rollback output missing data-loss warning:\n%s", out)
	}
	metas, err := dbgsnapshot.List(snapshotDirForTest(home))
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("dry-run snapshot count = %d, want only source snapshot: %+v", len(metas), metas)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeRollback || !evt.DryRun {
		t.Fatalf("dry-run rollback audit event = %+v", evt)
	}
}

func TestRollbackRejectsLegacyUnboundSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sourceID := captureSnapshotWithMetaForTest(
		t,
		home,
		dbgsnapshot.Meta{Operator: "legacy", Command: "apply", Context: "fake"},
		map[string]string{"users": "CREATE TABLE users (id BIGINT);"},
	)
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, err := executeCommandForTest("rollback", "--to", sourceID, "--fake", "--dry-run")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeValidationFailed {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeValidationFailed, err)
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed legacy unbound rollback: %+v", backend.Executed)
	}
}

func TestRollbackRejectsCrossTargetSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*dbgsnapshot.Target)
	}{
		{
			name: "context",
			mutate: func(target *dbgsnapshot.Target) {
				target.Context = "other"
			},
		},
		{
			name: "host",
			mutate: func(target *dbgsnapshot.Target) {
				target.Host = "other-host"
			},
		},
		{
			name: "database",
			mutate: func(target *dbgsnapshot.Target) {
				target.Database = "other_database"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			target := fakeSnapshotTarget()
			tc.mutate(target)
			sourceID := captureSnapshotWithMetaForTest(
				t,
				home,
				dbgsnapshot.Meta{
					Operator: "tester",
					Command:  "apply",
					Context:  target.Context,
					Target:   target,
				},
				map[string]string{"users": "CREATE TABLE users (id BIGINT);"},
			)
			backend := fake.New()
			restore := stubFakeBackend(t, backend)
			defer restore()

			_, err := executeCommandForTest("rollback", "--to", sourceID, "--fake", "--dry-run")
			if got := apperrors.AsAppError(err).Code; got != apperrors.CodeConflict {
				t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeConflict, err)
			}
			if len(backend.Executed) != 0 {
				t.Fatalf("executed cross-target rollback: %+v", backend.Executed)
			}
		})
	}
}

func TestRollbackToRejectsInvalidSnapshotID(t *testing.T) {
	if _, err := executeCommandForTest("rollback", "--to", "..\\evil", "--fake", "--dry-run"); err == nil {
		t.Fatal("expected invalid snapshot id to be rejected")
	}
	if _, err := executeCommandForTest("rollback", "--to", "missing-snapshot", "--fake", "--dry-run"); err == nil {
		t.Fatal("expected missing snapshot id to be rejected")
	}
}

func TestAuditQueryFiltersDbgovEventsAndPreservesFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	path := filepath.Join(home, ".audit", "audit.jsonl")
	now := time.Now().UTC()
	appendAuditRecordForTest(t, path, dbgaudit.Event{
		Timestamp:  now.Add(-2 * time.Hour),
		EventType:  dbgaudit.EventTypeDataExec,
		Operator:   "alice",
		Context:    dbgaudit.Context{Name: "prod"},
		Target:     dbgaudit.Target{ObjectType: "data", Object: "exec"},
		Risk:       "R2",
		Status:     dbgaudit.StatusDenied,
		SnapshotID: "snap-before",
		ImpactRows: intPtr(5000),
	})
	appendAuditRecordForTest(t, path, dbgaudit.Event{
		Timestamp: now.Add(-time.Hour),
		EventType: dbgaudit.EventTypeQuery,
		Operator:  "bob",
		Context:   dbgaudit.Context{Name: "dev"},
		Target:    dbgaudit.Target{ObjectType: "database"},
		Risk:      "R0",
		Status:    dbgaudit.StatusSucceeded,
	})

	out, err := executeCommandForTest("-o", "json", "audit", "query",
		"--path", path,
		"--operator", "alice",
		"--type", "data.exec",
		"--status", "denied",
		"--risk", "R2",
		"--context", "prod",
		"--since", "3h",
		"--limit", "1",
	)
	if err != nil {
		t.Fatalf("audit query error = %v", err)
	}
	for _, want := range []string{`"kind": "AuditQueryResult"`, `"eventType": "data.exec"`, `"snapshotFingerprint": "sha256:`, `"impactRows": 5000`, `"malformed": 0`} {
		if !strings.Contains(out, want) {
			t.Fatalf("audit query output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"operator": "bob"`) {
		t.Fatalf("audit query output included filtered event:\n%s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeAuditQuery {
		t.Fatalf("audit query audit event = %+v", evt)
	}
}

func TestAuditQueryReverseAndLimitAfterFiltering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	path := filepath.Join(home, ".audit", "audit.jsonl")
	base := time.Now().UTC().Add(-3 * time.Hour)
	for i, name := range []string{"first", "second", "third"} {
		appendAuditRecordForTest(t, path, dbgaudit.Event{
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			EventType: dbgaudit.EventTypeDataExec,
			Operator:  "alice",
			Context:   dbgaudit.Context{Name: name},
			Target:    dbgaudit.Target{ObjectType: "data", Object: "exec"},
			Risk:      "R2",
			Status:    dbgaudit.StatusSucceeded,
		})
	}
	appendAuditRecordForTest(t, path, dbgaudit.Event{
		Timestamp: base.Add(10 * time.Minute),
		EventType: dbgaudit.EventTypeQuery,
		Operator:  "alice",
		Context:   dbgaudit.Context{Name: "filtered-out"},
		Risk:      "R0",
		Status:    dbgaudit.StatusSucceeded,
	})

	out, err := executeCommandForTest("-o", "json", "audit", "query", "--path", path, "--risk", "R2", "--reverse", "--limit", "2")
	if err != nil {
		t.Fatalf("audit query reverse error = %v", err)
	}
	if strings.Contains(out, `"name": "first"`) || strings.Contains(out, "filtered-out") {
		t.Fatalf("audit query reverse/limit output = %s", out)
	}
	if !strings.Contains(out, `"name": "third"`) || !strings.Contains(out, `"name": "second"`) {
		t.Fatalf("audit query reverse/limit missing newest filtered events:\n%s", out)
	}
	if strings.Index(out, `"name": "third"`) > strings.Index(out, `"name": "second"`) {
		t.Fatalf("audit query reverse order wrong:\n%s", out)
	}
}

func TestAuditQueryEmptyLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, err := executeCommandForTest("-o", "json", "audit", "query", "--path", filepath.Join(home, "missing.log"))
	if err != nil {
		t.Fatalf("audit query empty error = %v", err)
	}
	if !strings.Contains(out, `"events": []`) {
		t.Fatalf("audit query empty output = %s", out)
	}
}

func TestAuditVerifyReportsMalformedAndStrictFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)
	path := filepath.Join(home, ".audit", "audit.jsonl")
	appendAuditRecordForTest(t, path, dbgaudit.Event{
		Timestamp: time.Now().UTC().Add(-time.Minute),
		EventType: dbgaudit.EventTypeQuery,
		Operator:  "alice",
		Risk:      "R0",
		Status:    dbgaudit.StatusSucceeded,
	})
	out, err := executeCommandForTest("-o", "json", "audit", "verify", "--path", path)
	if err != nil {
		t.Fatalf("audit verify clean error = %v", err)
	}
	if !strings.Contains(out, `"kind": "AuditVerifyResult"`) || !strings.Contains(out, `"malformed": 0`) {
		t.Fatalf("audit verify clean output = %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeAuditVerify {
		t.Fatalf("audit verify audit event = %+v", evt)
	}

	appendBadAuditLineForTest(t, path)
	out, err = executeCommandForTest("-o", "json", "audit", "verify", "--path", path)
	if err != nil {
		t.Fatalf("audit verify non-strict malformed error = %v", err)
	}
	if !strings.Contains(out, `"malformed": 1`) {
		t.Fatalf("audit verify malformed output = %s", out)
	}
	if _, err = executeCommandForTest("audit", "verify", "--path", path, "--strict"); err == nil {
		t.Fatal("expected strict audit verify to fail on malformed log")
	}
}

func TestAuditPruneBeforeDryRunAndConfirm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "audit.log")
	rotated := writeAuthenticatedAuditRotationsForTest(t, path, []string{"20260101-000000", "20260201-000000"})
	active := path
	old := rotated[0]
	newer := rotated[1]

	out, err := executeCommandForTest("-o", "json", "audit", "prune", "--path", path, "--before", "2026-01-15T00:00:00Z")
	if err != nil {
		t.Fatalf("audit prune dry-run error = %v", err)
	}
	if !strings.Contains(out, `"kind": "AuditPruneResult"`) || !strings.Contains(out, `"dryRun": true`) || !strings.Contains(out, filepath.Base(old)) {
		t.Fatalf("audit prune dry-run output = %s", out)
	}
	for _, filePath := range []string{active, old, newer} {
		if _, err := os.Stat(filePath); err != nil {
			t.Fatalf("dry-run should keep %s: %v", filePath, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".dbgov", "audit.log")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write prune audit event, stat err = %v", err)
	}

	out, err = executeCommandForTest("-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune", "audit", "prune", "--path", path, "--before", "2026-01-15", "--confirm")
	if err != nil {
		t.Fatalf("audit prune confirm error = %v", err)
	}
	if !strings.Contains(out, `"dryRun": false`) ||
		!strings.Contains(out, `"count": 1`) ||
		!strings.Contains(out, `"checkpointState": "advanced"`) {
		t.Fatalf("audit prune confirm output = %s", out)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old rotated log should be deleted, stat err = %v", err)
	}
	for _, filePath := range []string{active, newer} {
		if _, err := os.Stat(filePath); err != nil {
			t.Fatalf("confirm should keep %s: %v", filePath, err)
		}
	}
	if evt := lastAuditEventAtPath(t, auditControlPath(path)); evt.EventType != dbgaudit.EventTypeAuditPrune ||
		evt.Target.ObjectType != "audit" ||
		evt.Target.Object != "" ||
		evt.Target.Fingerprint == "" ||
		evt.Status != dbgaudit.StatusSucceeded ||
		evt.Statement != "" ||
		evt.StatementFingerprint == "" {
		t.Fatalf("audit prune event = %+v", evt)
	}
}

func TestAuditPruneKeepLastNeverDeletesActiveLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "audit.log")
	rotated := writeAuthenticatedAuditRotationsForTest(t, path, []string{
		"20260101-000000",
		"20260201-000000",
		"20260301-000000",
	})
	active := path
	first := rotated[0]
	second := rotated[1]
	third := rotated[2]

	out, err := executeCommandForTest("-o", "json", "--yes", "--ticket", "TEST-1", "--allow-audit-prune", "audit", "prune", "--path", path, "--keep-last", "1", "--confirm")
	if err != nil {
		t.Fatalf("audit prune keep-last error = %v", err)
	}
	if !strings.Contains(out, `"count": 2`) || !strings.Contains(out, filepath.Base(first)) || !strings.Contains(out, filepath.Base(second)) || strings.Contains(out, filepath.Base(third)) {
		t.Fatalf("audit prune keep-last output = %s", out)
	}
	for _, filePath := range []string{first, second} {
		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Fatalf("%s should be deleted, stat err = %v", filePath, err)
		}
	}
	for _, filePath := range []string{active, third} {
		if _, err := os.Stat(filePath); err != nil {
			t.Fatalf("%s should remain: %v", filePath, err)
		}
	}
}

func TestAuditPruneRejectsInvalidSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, "audit.log")

	cases := [][]string{
		{"audit", "prune", "--path", path},
		{"audit", "prune", "--path", path, "--before", "30d", "--keep-last", "1"},
		{"audit", "prune", "--path", path, "--keep-last", "-2"},
	}
	for _, args := range cases {
		if _, err := executeCommandForTest(args...); err == nil {
			t.Fatalf("expected audit prune args to fail: %v", args)
		}
	}
}

func TestInstallSkillsRequiresFlagAndCopiesSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	SetSkillFS(os.DirFS(repoRootForCmdTest(t)))
	t.Cleanup(func() { SetSkillFS(nil) })

	if _, err := executeCommandForTest("install", "claude"); err == nil {
		t.Fatal("expected install without --skills to fail")
	}

	out, err := executeCommandForTest("-o", "json", "--yes", "install", "claude", "--skills")
	if err != nil {
		t.Fatalf("install skills error = %v", err)
	}
	dst := filepath.Join(home, ".claude", "skills", "dbgov-cli")
	if !strings.Contains(out, `"path"`) || !strings.Contains(out, filepath.Base(dst)) {
		t.Fatalf("install output = %s", out)
	}
	data, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil {
		t.Fatalf("installed SKILL.md missing: %v", err)
	}
	if !strings.Contains(string(data), "name: dbgov-cli") {
		t.Fatalf("installed SKILL.md content = %s", string(data))
	}
	if _, err := os.Stat(filepath.Join(dst, "skill_test.go")); !os.IsNotExist(err) {
		t.Fatalf("skill_test.go should not be installed, stat err = %v", err)
	}
}

func TestBuildBackendResolvesCredentialReference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("prod", dbgovctx.Context{
		Base:     corectx.Base{Password: "credstore:missing-backend"},
		Engine:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "appdb",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("prod"); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	flags := &cliFlags{Config: configPath, Output: "json"}
	flags.commandContext = cmd.Context()
	_, _, err := buildBackend(flags, backendOptions{})
	if err == nil {
		t.Fatal("expected missing credential backend error")
	}
	if !strings.Contains(err.Error(), "missing-backend") {
		t.Fatalf("expected credstore backend error, got %v", err)
	}
}

func TestBuildBackendUsesDBGOVPasswordForCurrentContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_PASSWORD", "env-secret")
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("prod", dbgovctx.Context{
		Base:     corectx.Base{Username: "appuser"},
		Engine:   "postgres",
		Host:     "127.0.0.1",
		Port:     5432,
		Database: "appdb",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("prod"); err != nil {
		t.Fatal(err)
	}
	backend := fake.New()
	var gotDSN string
	old := newPostgresBackend
	newPostgresBackend = func(dsn, database string) (dbbackend.Backend, error) {
		gotDSN = dsn
		return backend, nil
	}
	defer func() { newPostgresBackend = old }()

	built, meta, err := buildBackend(&cliFlags{Config: configPath}, backendOptions{})
	if err != nil {
		t.Fatalf("buildBackend() error = %v", err)
	}
	defer func() { _ = built.Close() }()
	if meta.Name != "prod" {
		t.Fatalf("context name = %q, want prod", meta.Name)
	}
	if !strings.Contains(gotDSN, "appuser:env-secret@") {
		t.Fatalf("postgres DSN did not use DBGOV_PASSWORD: %s", gotDSN)
	}
}

func TestBuildBackendUsesDBGOVPasswordForContextOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DBGOV_PASSWORD", "override-secret")
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	if err := dbgovctx.SetContext("dev", dbgovctx.Context{
		Base:     corectx.Base{Username: "devuser", Password: "dev-secret"},
		Engine:   "postgres",
		Host:     "127.0.0.1",
		Port:     5432,
		Database: "devdb",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.SetContext("prod", dbgovctx.Context{
		Base:     corectx.Base{Username: "produser"},
		Engine:   "postgres",
		Host:     "127.0.0.1",
		Port:     5432,
		Database: "proddb",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("dev"); err != nil {
		t.Fatal(err)
	}
	backend := fake.New()
	var gotDSN string
	old := newPostgresBackend
	newPostgresBackend = func(dsn, database string) (dbbackend.Backend, error) {
		gotDSN = dsn
		return backend, nil
	}
	defer func() { newPostgresBackend = old }()

	built, meta, err := buildBackend(&cliFlags{Config: configPath, Context: "prod"}, backendOptions{})
	if err != nil {
		t.Fatalf("buildBackend(--context prod) error = %v", err)
	}
	defer func() { _ = built.Close() }()
	if meta.Name != "prod" {
		t.Fatalf("context name = %q, want prod", meta.Name)
	}
	if !strings.Contains(gotDSN, "produser:override-secret@") {
		t.Fatalf("postgres DSN did not use DBGOV_PASSWORD for override context: %s", gotDSN)
	}
}

func TestCtxSetRejectsPlainYamlPassword(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	_, err := executeCommandForTest("--config", configPath, "ctx", "set", "prod", "--engine", "mysql", "--host", "127.0.0.1", "--username", "app", "--password", "secret")
	if err == nil {
		t.Fatal("expected ctx set --password with plain-yaml backend to fail")
	}
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodeUsageError || appErr.Message != "credentials must use a non-plain credential backend" {
		t.Fatalf("error = %#v, want usage guard", appErr)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := cfg.Contexts["prod"]; ok {
		t.Fatalf("context was saved despite rejected plain-yaml password: %+v", cfg.Contexts["prod"])
	}
}

func TestCtxSetStoresPasswordInKeychainBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	store := map[string]string{}
	corecredstore.Register("keychain", func() corecredstore.Backend {
		return memoryCredentialBackend{values: store}
	})

	_, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "prod", "--engine", "mysql", "--host", "127.0.0.1", "--username", "app", "--password", "secret", "--credential-backend", "keychain")
	if err != nil {
		t.Fatalf("ctx set keychain error = %v", err)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, err := dbgovctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := cfg.Contexts["prod"]
	if ctx.Password != corecredstore.EncodeRef("keychain") || ctx.CredentialBackend != "keychain" {
		t.Fatalf("stored context credential = %+v", ctx.Base)
	}
	resolved, err := ctx.ResolvePasswordContext(context.Background(), "prod")
	if err != nil {
		t.Fatalf("ResolvePasswordContext() error = %v", err)
	}
	if resolved != "secret" {
		t.Fatalf("resolved password = %q, want secret", resolved)
	}
}

func TestCtxSetFailsWhenCredentialBackendCannotStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath := filepath.Join(home, "config.yaml")
	_, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "prod", "--engine", "mysql", "--host", "127.0.0.1", "--username", "app", "--password", "secret", "--credential-backend", "missing-backend")
	if err == nil {
		t.Fatal("expected missing credential backend to fail during ctx set")
	}
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodeCredentialStoreError || appErr.Message != "failed to store credential" {
		t.Fatalf("error = %#v, want credential-store failure", appErr)
	}
	dbgovctx.SetConfigPath(configPath)
	defer dbgovctx.SetConfigPath("")
	cfg, loadErr := dbgovctx.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := cfg.Contexts["prod"]; ok {
		t.Fatalf("context was saved despite credential storage failure: %+v", cfg.Contexts["prod"])
	}
}

type memoryCredentialBackend struct {
	values map[string]string
}

func (m memoryCredentialBackend) Name() string { return "keychain" }

func (m memoryCredentialBackend) Available() error { return nil }

func (m memoryCredentialBackend) Get(_ context.Context, contextName string) (string, error) {
	value, ok := m.values[contextName]
	if !ok {
		return "", corecredstore.ErrNotFound
	}
	return value, nil
}

func (m memoryCredentialBackend) Put(_ context.Context, contextName, password string) error {
	m.values[contextName] = password
	return nil
}

func (m memoryCredentialBackend) Delete(_ context.Context, contextName string) error {
	delete(m.values, contextName)
	return nil
}

func stubFakeBackend(t *testing.T, backend *fake.Backend) func() {
	t.Helper()
	old := newFakeBackend
	newFakeBackend = func() dbbackend.Backend { return backend }
	return func() { newFakeBackend = old }
}

func setupPostgresContext(t *testing.T, home string) string {
	t.Helper()
	configPath := filepath.Join(home, "config.yaml")
	dbgovctx.SetConfigPath(configPath)
	t.Cleanup(func() { dbgovctx.SetConfigPath("") })
	if err := dbgovctx.SetContext("pg", dbgovctx.Context{
		Base:     corectx.Base{Password: "secret"},
		Engine:   "postgres",
		Host:     "127.0.0.1",
		Port:     5432,
		Database: "app",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbgovctx.UseContext("pg"); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func stubPostgresBackend(t *testing.T, backend dbbackend.Backend) func() {
	t.Helper()
	old := newPostgresBackend
	newPostgresBackend = func(string, string) (dbbackend.Backend, error) { return backend, nil }
	return func() { newPostgresBackend = old }
}

type postgresFakeBackend struct {
	*fake.Backend
	renderer *postgres.Backend
}

func newPostgresFakeBackend(current schema.Schema) *postgresFakeBackend {
	return &postgresFakeBackend{
		Backend:  &fake.Backend{Schema: current, DDLs: map[string]string{}},
		renderer: postgres.NewWithDB(nil, "app"),
	}
}

func (b *postgresFakeBackend) RenderDDL(changes []schema.Change) ([]string, error) {
	return b.renderer.RenderDDL(changes)
}

func lastAuditEvent(t *testing.T, home string) dbgaudit.Event {
	t.Helper()
	return lastAuditEventAtPath(t, filepath.Join(home, ".dbgov", "audit.log"))
}

func lastAuditEventAtPath(t *testing.T, path string) dbgaudit.Event {
	t.Helper()
	result, err := coreaudit.QueryRaw(path, coreaudit.Filter{})
	if err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if len(result.Records) == 0 {
		t.Fatal("query audit log: no records")
	}
	var evt dbgaudit.Event
	line := result.Records[len(result.Records)-1].Line
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatalf("unmarshal audit event: %v\n%s", err, line)
	}
	return evt
}

func snapshotDirForTest(home string) string {
	return filepath.Join(home, ".dbgov", "snapshots")
}

func captureSnapshotForTest(t *testing.T, home string, tables map[string]string) string {
	t.Helper()
	return captureSnapshotWithMetaForTest(
		t,
		home,
		dbgsnapshot.Meta{
			Operator: "tester",
			Command:  "apply",
			Context:  "fake",
			Target:   fakeSnapshotTarget(),
		},
		tables,
	)
}

func fakeSnapshotTarget() *dbgsnapshot.Target {
	return &dbgsnapshot.Target{
		Context:  "fake",
		Engine:   "mysql",
		Host:     "fake",
		Database: "fake",
	}
}

func captureSnapshotWithMetaForTest(t *testing.T, home string, meta dbgsnapshot.Meta, tables map[string]string) string {
	t.Helper()
	secureMutationAuditTestParent(t, home)
	baseDir := snapshotDirForTest(home)
	id, data, err := dbgsnapshot.Prepare(meta, tables)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateMutationDirectory(baseDir); err != nil {
		t.Fatal(err)
	}
	if _, err := writePrivateEvidenceFile(baseDir, id+".json", data); err != nil {
		t.Fatal(err)
	}
	return id
}

func appendAuditRecordForTest(t *testing.T, path string, event dbgaudit.Event) {
	t.Helper()
	if event.Operator == "" {
		event.Operator = "tester"
	}
	if event.Risk == "" {
		event.Risk = "R0"
	}
	if event.Status == "" {
		event.Status = dbgaudit.StatusSucceeded
	}
	if err := coreaudit.AppendRecord(path, event, coreaudit.Options{}); err != nil {
		t.Fatal(err)
	}
}

func writeAuthenticatedAuditRotationsForTest(t *testing.T, path string, stamps []string) []string {
	t.Helper()
	secureMutationAuditTestParent(t, filepath.Dir(path))
	for index := 0; index <= len(stamps); index++ {
		event := dbgaudit.New(
			dbgaudit.EventTypeQuery,
			"tester@host",
			dbgaudit.Context{},
			dbgaudit.Target{ObjectType: "audit"},
		)
		event.Timestamp = time.Unix(int64(index+1), 0).UTC()
		if err := dbgaudit.Append(path, event, coreaudit.Options{MaxSizeBytes: 1}); err != nil {
			t.Fatalf("Append(rotation %d) error = %v", index, err)
		}
	}
	rotations, err := coreaudit.RotatedFiles(path)
	if err != nil || len(rotations) != len(stamps) {
		t.Fatalf("RotatedFiles() = %v, %v; want %d", rotations, err, len(stamps))
	}
	result := make([]string, len(stamps))
	for index, stamp := range stamps {
		result[index] = path + "." + stamp + ".log"
		if err := os.Rename(rotations[index], result[index]); err != nil {
			t.Fatalf("Rename(rotation %d) error = %v", index, err)
		}
	}
	return result
}

func appendBadAuditLineForTest(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString("{bad json\n"); err != nil {
		t.Fatal(err)
	}
}

func repoRootForCmdTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find repo root")
	return ""
}

func intPtr(v int) *int {
	return &v
}
