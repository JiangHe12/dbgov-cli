package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
	corectx "github.com/JiangHe12/opskit-core/ctx"
)

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
