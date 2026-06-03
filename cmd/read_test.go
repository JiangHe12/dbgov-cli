package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/backend/fake"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
	corectx "github.com/JiangHe12/opskit-core/ctx"
)

var errFakeExplain = errors.New("fake explain failure")

func TestQueryFakeBackendJSONAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, _, err := executeCommandForTest("-o", "json", "query", "--sql", "SELECT id, name FROM users", "--fake")
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if !strings.Contains(out, `"kind": "QueryResult"`) || !strings.Contains(out, `"columns"`) {
		t.Fatalf("unexpected query output: %s", out)
	}
	evt := lastAuditEvent(t, home)
	if evt.EventType != dbgaudit.EventTypeQuery || evt.Statement != "SELECT id, name FROM users" || evt.Target.ObjectType != "database" {
		t.Fatalf("audit event = %+v", evt)
	}
}

func TestQueryRejectsWriteSQL(t *testing.T) {
	_, _, err := executeCommandForTest("query", "--sql", "UPDATE users SET name='x'", "--fake")
	if err == nil {
		t.Fatal("expected query to reject write SQL")
	}
	if !strings.Contains(err.Error(), "query only accepts read-only SQL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExplainFakeBackendJSONAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	out, _, err := executeCommandForTest("-o", "json", "explain", "--sql", "SELECT * FROM users", "--fake")
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

func TestDoctorConfigFakeBackend(t *testing.T) {
	out, _, err := executeCommandForTest("-o", "json", "doctor", "config", "--fake")
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
	_, _, err := executeCommandForTest("--config", filepath.Join(home, "config.yaml"), "schema", "diff", "-f", desired, "--fake")
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

	out, _, err := executeCommandForTest("-o", "json", "schema", "list", "--fake")
	if err != nil {
		t.Fatalf("schema list error = %v", err)
	}
	if !strings.Contains(out, `"kind": "SchemaTableList"`) || !strings.Contains(out, `"users"`) {
		t.Fatalf("unexpected list output: %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeSchemaList || evt.Target.ObjectType != "schema" {
		t.Fatalf("list audit event = %+v", evt)
	}

	out, _, err = executeCommandForTest("-o", "json", "schema", "describe", "users", "--fake")
	if err != nil {
		t.Fatalf("schema describe error = %v", err)
	}
	if !strings.Contains(out, `"kind": "SchemaDescribe"`) || !strings.Contains(out, `"indexes"`) || !strings.Contains(out, `"foreignKeys"`) {
		t.Fatalf("unexpected describe output: %s", out)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeSchemaDescribe || evt.Target.Object != "users" {
		t.Fatalf("describe audit event = %+v", evt)
	}

	out, _, err = executeCommandForTest("-o", "json", "schema", "dump", "--fake")
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

func TestSchemaDumpWritesDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, "schema")
	_, _, err := executeCommandForTest("schema", "dump", "--fake", "--dir", dir)
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
}

func TestSchemaPlanFakeBackendShowsRiskDDLAndAudit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100));`)

	out, _, err := executeCommandForTest("-o", "json", "schema", "plan", "-f", desired, "--fake")
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

func TestSchemaApplyR1RequiresYesAndExecutesWhenAuthorized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, legacy TEXT, name VARCHAR(100));`)

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, _, err := executeCommandForTest("--non-interactive", "schema", "apply", "-f", desired, "--fake")
	if err == nil {
		t.Fatal("expected R1 apply without --yes to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed without authorization: %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.EventType != dbgaudit.EventTypeSchemaApply {
		t.Fatalf("denied audit event = %+v", evt)
	}

	_, _, err = executeCommandForTest("--yes", "schema", "apply", "-f", desired, "--fake")
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
		_, _, err := executeCommandForTest(fullArgs...)
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
	_, _, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "schema", "apply", "-f", desired, "--fake", "--allow-destructive")
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

func TestAuthorizeWriteRaisesProtectedR1ToR2(t *testing.T) {
	flags := &cliFlags{Yes: true, NonInteractive: true, Operator: "alice"}
	err := authorizeWrite(flags, safety.R1, contextMeta{Protected: true}, nil, nil)
	if err == nil {
		t.Fatal("expected protected R1 to require ticket after risk upgrade")
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

	out, _, err := executeCommandForTest("-o", "json", "schema", "apply", "-f", desired, "--fake", "--dry-run")
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

	_, _, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "schema", "apply", "-f", desired, "--fake", "--allow-destructive")
	if err == nil {
		t.Fatal("expected partial failure")
	}
	if len(backend.Executed) != 1 {
		t.Fatalf("executed = %+v, want one successful statement", backend.Executed)
	}
	evt := lastAuditEvent(t, home)
	if evt.Status != dbgaudit.StatusFailed || evt.Executed != 1 || !strings.Contains(evt.FailedStatement, "DROP COLUMN") {
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

	_, _, err := executeCommandForTest("--non-interactive", "data", "exec", "--sql", "INSERT INTO users(id) VALUES (1)", "--fake")
	if err == nil {
		t.Fatal("expected INSERT without --yes to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed denied insert: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.EventType != dbgaudit.EventTypeDataExec || evt.Risk != "R1" {
		t.Fatalf("denied insert audit event = %+v", evt)
	}

	_, _, err = executeCommandForTest("--yes", "data", "exec", "--sql", "INSERT INTO users(id) VALUES (1)", "--fake")
	if err != nil {
		t.Fatalf("insert exec error = %v", err)
	}
	if len(backend.ExecutedDML) != 1 {
		t.Fatalf("ExecutedDML = %+v", backend.ExecutedDML)
	}
	evt := lastAuditEvent(t, home)
	if evt.Status != dbgaudit.StatusSucceeded || evt.Risk != "R1" || evt.AffectedRows == nil || *evt.AffectedRows != 4 {
		t.Fatalf("insert audit event = %+v", evt)
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

	_, _, err := executeCommandForTest("--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id < 10", "--fake")
	if err != nil {
		t.Fatalf("update exec error = %v", err)
	}
	evt := lastAuditEvent(t, home)
	if evt.Risk != "R1" || evt.ImpactRows == nil || *evt.ImpactRows != 10 || evt.AffectedRows == nil || *evt.AffectedRows != 2 {
		t.Fatalf("small update audit event = %+v", evt)
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

	_, _, err := executeCommandForTest("--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE active=1", "--fake")
	if err == nil {
		t.Fatal("expected large update without ticket to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed denied large update: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.Risk != "R2" || evt.ImpactRows == nil || *evt.ImpactRows != 5000 {
		t.Fatalf("large update denied audit event = %+v", evt)
	}

	_, _, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE active=1", "--fake")
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
		_, _, err := executeCommandForTest(fullArgs...)
		restore()
		if err == nil {
			t.Fatalf("expected no-WHERE delete with args %v to be denied", args)
		}
		if len(backend.ExecutedDML) != 0 {
			t.Fatalf("executed denied no-WHERE delete: %+v", backend.ExecutedDML)
		}
	}

	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()
	_, _, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "data", "exec", "--sql", "DELETE FROM users", "--fake", "--allow-no-where")
	if err != nil {
		t.Fatalf("no-WHERE delete authorized error = %v", err)
	}
	if len(backend.ExecutedDML) != 1 {
		t.Fatalf("ExecutedDML = %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Risk != "R3" || !evt.Destructive {
		t.Fatalf("no-WHERE delete audit event = %+v", evt)
	}
}

func TestDataExecProtectedSmallUpdateUpgradesToR2(t *testing.T) {
	flags := &cliFlags{Yes: true, NonInteractive: true, Operator: "alice"}
	err := authorizeWrite(flags, safety.R1, contextMeta{Protected: true}, nil, nil)
	if err == nil {
		t.Fatal("expected protected R1 data exec to require ticket")
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

	_, _, err := executeCommandForTest("--config", configPath, "--context", "prod", "--yes", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake")
	if err == nil {
		t.Fatal("expected protected small update without ticket to be denied")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed protected denied update: %+v", backend.ExecutedDML)
	}
	if evt := lastAuditEvent(t, home); evt.Status != dbgaudit.StatusDenied || evt.Risk != "R1" {
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

	out, _, err := executeCommandForTest("-o", "json", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run data exec error = %v", err)
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("dry-run executed DML: %+v", backend.ExecutedDML)
	}
	if !strings.Contains(out, `"kind": "DataExecPlan"`) || !strings.Contains(out, `"impactRows": 12`) {
		t.Fatalf("dry-run output = %s", out)
	}
	if evt := lastAuditEvent(t, home); !evt.DryRun || evt.ImpactRows == nil || *evt.ImpactRows != 12 {
		t.Fatalf("dry-run audit event = %+v", evt)
	}
}

func TestDataExecRejectsNonDMLAndExplainFailure(t *testing.T) {
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()
	if _, _, err := executeCommandForTest("data", "exec", "--sql", "SELECT * FROM users", "--fake"); err == nil {
		t.Fatal("expected SELECT to be rejected")
	}
	if _, _, err := executeCommandForTest("data", "exec", "--sql", "CREATE TABLE t (id BIGINT)", "--fake"); err == nil {
		t.Fatal("expected DDL to be rejected")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed rejected SQL: %+v", backend.ExecutedDML)
	}

	backend.ExplainErr = errFakeExplain
	if _, _, err := executeCommandForTest("data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake"); err == nil {
		t.Fatal("expected EXPLAIN failure to stop data exec")
	}
	if len(backend.ExecutedDML) != 0 {
		t.Fatalf("executed after explain failure: %+v", backend.ExecutedDML)
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

	_, _, err := executeCommandForTest("export", "--dir", dir, "--fake")
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
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeExport || evt.Status != dbgaudit.StatusSucceeded {
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
	_, _, err := executeCommandForTest("--non-interactive", "import", dir, "--fake")
	if err == nil {
		t.Fatal("expected R1 import without --yes to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied import: %+v", backend.Executed)
	}
	if evt := lastAuditEvent(t, home); evt.EventType != dbgaudit.EventTypeImport || evt.Status != dbgaudit.StatusDenied || evt.Risk != "R1" {
		t.Fatalf("denied import audit event = %+v", evt)
	}

	_, _, err = executeCommandForTest("--yes", "import", dir, "--fake")
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

func TestImportR3RequiresTicketAllowDestructive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	writeTestFile(t, dir, "users.sql", "CREATE TABLE users (id TEXT);")
	backend := fake.New()
	restore := stubFakeBackend(t, backend)
	defer restore()

	_, _, err := executeCommandForTest("--yes", "--ticket", "CHG-1", "import", dir, "--fake")
	if err == nil {
		t.Fatal("expected destructive import without allow flag to be denied")
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("executed denied destructive import: %+v", backend.Executed)
	}

	_, _, err = executeCommandForTest("--yes", "--ticket", "CHG-1", "import", dir, "--fake", "--allow-destructive")
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

	out, _, err := executeCommandForTest("-o", "json", "import", dir, "--fake", "--dry-run")
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

func stubFakeBackend(t *testing.T, backend *fake.Backend) func() {
	t.Helper()
	old := newFakeBackend
	newFakeBackend = func() dbbackend.Backend { return backend }
	return func() { newFakeBackend = old }
}

func lastAuditEvent(t *testing.T, home string) dbgaudit.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".dbgov", "audit.log"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var evt dbgaudit.Event
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &evt); err != nil {
		t.Fatalf("unmarshal audit event: %v\n%s", err, lines[len(lines)-1])
	}
	return evt
}
