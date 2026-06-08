package cmd

import (
	"encoding/json"
	"testing"

	"github.com/JiangHe12/opskit-core/apperrors"
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
		name       string
		args       []string
		wantObject bool
	}{
		{name: "schema describe users", args: []string{"-o", "json", "schema", "describe", "users", "--fake"}, wantObject: true},
		{name: "schema list", args: []string{"-o", "json", "schema", "list", "--fake"}, wantObject: true},
		{name: "schema dump", args: []string{"-o", "json", "schema", "dump", "--fake"}, wantObject: true},
		{name: "data exec dry-run plan", args: []string{"-o", "json", "data", "exec", "--sql", "UPDATE users SET name='x' WHERE id=1", "--fake", "--dry-run"}, wantObject: true},
		{name: "schema apply dry-run plan reaches fake backend planning", args: nil, wantObject: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := setContractHome(t)
			args := tc.args
			if args == nil {
				desired := writeTestFile(t, home, "desired.sql", `CREATE TABLE users (id BIGINT, name VARCHAR(100), email VARCHAR(255));`)
				args = []string{"-o", "json", "schema", "apply", "-f", desired, "--fake", "--dry-run"}
			}
			out, err := executeCommandForTest(args...)
			if err != nil {
				t.Fatalf("Execute(%v) error = %v", args, err)
			}
			assertJSONOutput(t, out, tc.wantObject)
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

func assertJSONOutput(t *testing.T, out string, wantObject bool) {
	t.Helper()
	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON:\n%s", out)
	}
	if wantObject {
		var obj map[string]any
		if err := json.Unmarshal([]byte(out), &obj); err != nil {
			t.Fatalf("output is not a JSON object: %v\n%s", err, out)
		}
	}
}
