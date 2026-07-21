package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

func TestContractExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code apperrors.ErrorCode
		exit int
	}{
		{
			name: "unknown command is usage error",
			args: []string{"bogus-subcommand"},
			code: apperrors.CodeUsageError,
			exit: 1,
		},
		{
			name: "schema describe missing table is resource not found",
			args: []string{"schema", "describe", "missing", "--fake"},
			code: apperrors.CodeResourceNotFound,
			exit: 4,
		},
		{
			name: "data exec rejects read SQL as validation failed",
			args: []string{"data", "exec", "--sql", "SELECT * FROM users", "--fake"},
			code: apperrors.CodeValidationFailed,
			exit: 9,
		},
		{
			name: "query rejects multiple statements",
			args: []string{"query", "--sql", "SELECT 1; DELETE FROM users", "--fake"},
			code: apperrors.CodeValidationFailed,
			exit: 9,
		},
		{
			name: "explain rejects multiple statements",
			args: []string{"explain", "--sql", "SELECT 1; DELETE FROM users", "--fake"},
			code: apperrors.CodeValidationFailed,
			exit: 9,
		},
		{
			name: "data exec rejects multiple statements",
			args: []string{"data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1; DROP TABLE users", "--fake"},
			code: apperrors.CodeValidationFailed,
			exit: 9,
		},
		{
			name: "R3 data exec without allow flag requires authorization",
			args: []string{"--yes", "--ticket", "CHG-1", "data", "exec", "--sql", "DELETE FROM users", "--fake"},
			code: apperrors.CodeAuthorizationRequired,
			exit: 8,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setContractHome(t)
			_, err := executeCommandForTest(tc.args...)
			assertContractError(t, err, tc.code, tc.exit)
		})
	}
}

func TestContractJSONOutput(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		setup func(t *testing.T, home string) []string
	}{
		{name: "query", args: []string{"-o", "json", "query", "--sql", "SELECT 1", "--fake"}},
		{name: "explain", args: []string{"-o", "json", "explain", "--sql", "SELECT * FROM users", "--fake"}},
		{name: "schema describe users", args: []string{"-o", "json", "schema", "describe", "users", "--fake"}},
		{name: "schema list", args: []string{"-o", "json", "schema", "list", "--fake"}},
		{name: "schema dump", args: []string{"-o", "json", "schema", "dump", "--fake"}},
		{name: "schema diff", setup: func(t *testing.T, home string) []string {
			t.Helper()
			desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100), email VARCHAR(255));`)
			return []string{"-o", "json", "schema", "diff", "-f", desired, "--fake"}
		}},
		{name: "schema plan", setup: func(t *testing.T, home string) []string {
			t.Helper()
			desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100), email VARCHAR(255));`)
			return []string{"-o", "json", "schema", "plan", "-f", desired, "--fake"}
		}},
		{name: "data exec dry-run plan", args: []string{"-o", "json", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake", "--dry-run"}},
		{name: "schema apply dry-run plan reaches fake backend planning", setup: func(t *testing.T, home string) []string {
			t.Helper()
			desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100), email VARCHAR(255));`)
			return []string{"-o", "json", "schema", "apply", "-f", desired, "--fake", "--dry-run"}
		}},
		{name: "ctx list", args: []string{"-o", "json", "ctx", "list"}},
		{name: "ctx current", setup: func(t *testing.T, home string) []string {
			t.Helper()
			if _, err := executeCommandForTest("--config", home+"/config.yaml", "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "dev", "--engine", "mysql", "--host", "127.0.0.1", "--database", "app"); err != nil {
				t.Fatalf("ctx set error = %v", err)
			}
			if _, err := executeCommandForTest("--config", home+"/config.yaml", "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "use", "dev"); err != nil {
				t.Fatalf("ctx use error = %v", err)
			}
			return []string{"--config", home + "/config.yaml", "-o", "json", "ctx", "current"}
		}},
		{name: "capabilities", args: []string{"-o", "json", "capabilities"}},
		{name: "version", args: []string{"-o", "json", "version"}},
		{name: "doctor config", args: []string{"-o", "json", "doctor", "config"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := setContractHome(t)
			args := tc.args
			if tc.setup != nil {
				args = tc.setup(t, home)
			}
			out, err := executeCommandForTest(args...)
			if err != nil {
				t.Fatalf("Execute(%v) error = %v", args, err)
			}
			assertJSONSuccessEnvelope(t, out)
		})
	}
}

func TestContractPlainOutputIsNotJSON(t *testing.T) {
	setContractHome(t)
	out, err := executeCommandForTest("schema", "describe", "users", "--fake")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if json.Valid([]byte(out)) {
		t.Fatalf("plain schema describe output unexpectedly valid JSON: %s", out)
	}
}

func TestDBCommandTableOutputIncludesTargetHeader(t *testing.T) {
	home := setContractHome(t)
	configPath := filepath.Join(home, "config.yaml")
	createTargetContext(t, configPath)

	out, err := executeCommandForTest("--config", configPath, "query", "--sql", "SELECT 1", "--fake")
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	want := "TARGET\tcontext=dev | engine=mysql | host=127.0.0.1:3307 | database=app"
	if !strings.Contains(out, want) {
		t.Fatalf("query output missing target header %q:\n%s", want, out)
	}

	out, err = executeCommandForTest("--config", configPath, "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake", "--dry-run")
	if err != nil {
		t.Fatalf("data exec dry-run error = %v", err)
	}
	want = "WRITE TARGET\tcontext=dev | engine=mysql | host=127.0.0.1:3307 | database=app"
	if !strings.Contains(out, want) {
		t.Fatalf("data exec output missing write target header %q:\n%s", want, out)
	}
}

func TestDBCommandJSONOutputIncludesTargetWithoutBreakingEnvelope(t *testing.T) {
	home := setContractHome(t)
	configPath := filepath.Join(home, "config.yaml")
	createTargetContext(t, configPath)

	out, err := executeCommandForTest("--config", configPath, "-o", "json", "query", "--sql", "SELECT 1", "--fake")
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	assertJSONSuccessEnvelope(t, out)
	var envelope struct {
		Kind string `json:"kind"`
		Data struct {
			Columns []string      `json:"columns"`
			Target  commandTarget `json:"target"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("Unmarshal() error = %v\n%s", err, out)
	}
	if envelope.Kind != "QueryResult" {
		t.Fatalf("kind = %q, want QueryResult", envelope.Kind)
	}
	if len(envelope.Data.Columns) == 0 {
		t.Fatalf("query JSON lost existing columns field: %s", out)
	}
	target := envelope.Data.Target
	if target.Context != "dev" || target.Engine != "mysql" || target.HostPort != "127.0.0.1:3307" || target.Database != "app" || target.Operation != string(targetRead) {
		t.Fatalf("target = %#v, want dev/mysql/127.0.0.1:3307/app/read", target)
	}
}

func TestContractQueryAcceptsReadOnlyRecursiveCTE(t *testing.T) {
	setContractHome(t)
	sql := "WITH RECURSIVE t AS (SELECT 1 UNION ALL SELECT n+1 FROM t WHERE n<5) SELECT * FROM t"
	if _, err := executeCommandForTest("query", "--sql", sql, "--fake"); err != nil {
		t.Fatalf("query recursive CTE error = %v", err)
	}
}

func setContractHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func createTargetContext(t *testing.T, configPath string) {
	t.Helper()
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "set", "dev", "--engine", "mysql", "--host", "127.0.0.1", "--port", "3307", "--database", "app"); err != nil {
		t.Fatalf("ctx set error = %v", err)
	}
	if _, err := executeCommandForTest("--config", configPath, "--yes", "--ticket", "TEST-1", "--allow-context-change", "ctx", "use", "dev"); err != nil {
		t.Fatalf("ctx use error = %v", err)
	}
}

func assertContractError(t *testing.T, err error, wantCode apperrors.ErrorCode, wantExit int) {
	t.Helper()
	if err == nil {
		t.Fatalf("Execute() error = nil, want %s", wantCode)
	}
	if got := apperrors.AsAppError(err).Code; got != wantCode {
		t.Fatalf("error code = %s, want %s; err = %v", got, wantCode, err)
	}
	if got := apperrors.ExitCode(err); got != wantExit {
		t.Fatalf("exit code = %d, want %d; err = %v", got, wantExit, err)
	}
}

func assertJSONSuccessEnvelope(t *testing.T, out string) {
	t.Helper()
	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON:\n%s", out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, out)
	}
	for _, key := range []string{"apiVersion", "kind", "success", "data"} {
		if _, ok := obj[key]; !ok {
			t.Fatalf("JSON envelope missing %q: %s", key, out)
		}
	}
	if success, ok := obj["success"].(bool); !ok || !success {
		t.Fatalf("JSON envelope success = %#v, want true: %s", obj["success"], out)
	}
}
