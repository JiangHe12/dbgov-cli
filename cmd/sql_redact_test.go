package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

func TestRedactSQLTruthTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "create user identified by",
			input: "CREATE USER 'u'@'%' IDENTIFIED BY 'S3cret'",
			want:  "CREATE USER 'u'@'%' IDENTIFIED BY '[REDACTED]'",
		},
		{
			name:  "alter user mixed case and spacing",
			input: "ALTER USER 'u'@'h' identified   by   'x'",
			want:  "ALTER USER 'u'@'h' identified   by   '[REDACTED]'",
		},
		{
			name:  "identified by password hash",
			input: "CREATE USER 'u'@'%' IDENTIFIED BY PASSWORD '*ABC'",
			want:  "CREATE USER 'u'@'%' IDENTIFIED BY PASSWORD '[REDACTED]'",
		},
		{
			name:  "identified with plugin by",
			input: "CREATE USER 'u'@'%' IDENTIFIED WITH caching_sha2_password BY 'x'",
			want:  "CREATE USER 'u'@'%' IDENTIFIED WITH caching_sha2_password BY '[REDACTED]'",
		},
		{
			name:  "identified with plugin as",
			input: "CREATE USER 'u'@'%' IDENTIFIED WITH plugin AS '*H'",
			want:  "CREATE USER 'u'@'%' IDENTIFIED WITH plugin AS '[REDACTED]'",
		},
		{
			name:  "password function",
			input: "SET PASSWORD FOR 'u'@'h' = PASSWORD('x')",
			want:  "SET PASSWORD FOR 'u'@'h' = PASSWORD('[REDACTED]')",
		},
		{
			name:  "set password for user literal",
			input: "SET PASSWORD FOR 'u'@'h' = 'p1'",
			want:  "SET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password current user literal",
			input: "SET PASSWORD = 'p2'",
			want:  "SET PASSWORD = '[REDACTED]'",
		},
		{
			name:  "identified by double quoted literal",
			input: `CREATE USER 'u'@'%' IDENTIFIED BY "p3"`,
			want:  `CREATE USER 'u'@'%' IDENTIFIED BY '[REDACTED]'`,
		},
		{
			name:  "identified with plugin double quoted literal",
			input: `CREATE USER 'u'@'%' IDENTIFIED WITH plugin BY "p4"`,
			want:  `CREATE USER 'u'@'%' IDENTIFIED WITH plugin BY '[REDACTED]'`,
		},
		{
			name:  "password function double quoted literal",
			input: `SET PASSWORD = PASSWORD("p5")`,
			want:  `SET PASSWORD = PASSWORD('[REDACTED]')`,
		},
		{
			name:  "server options password literal",
			input: "CREATE SERVER s FOREIGN DATA WRAPPER mysql OPTIONS (USER 'u', PASSWORD 'p6')",
			want:  "CREATE SERVER s FOREIGN DATA WRAPPER mysql OPTIONS (USER 'u', PASSWORD '[REDACTED]')",
		},
		{
			name:  "server options password double quoted literal",
			input: `CREATE SERVER s FOREIGN DATA WRAPPER mysql OPTIONS (USER "u", PASSWORD "p7")`,
			want:  `CREATE SERVER s FOREIGN DATA WRAPPER mysql OPTIONS (USER "u", PASSWORD '[REDACTED]')`,
		},
		{
			name:  "set password double quoted literal",
			input: `SET PASSWORD FOR 'u'@'h' = "p8"`,
			want:  `SET PASSWORD FOR 'u'@'h' = '[REDACTED]'`,
		},
		{
			name:  "set password after statement",
			input: "ALTER TABLE t ADD c INT; SET PASSWORD FOR 'u'@'h' = 'p'",
			want:  "ALTER TABLE t ADD c INT; SET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password after comment double quoted",
			input: `/* x */ SET PASSWORD FOR 'u'@'h' = "p"`,
			want:  `/* x */ SET PASSWORD FOR 'u'@'h' = '[REDACTED]'`,
		},
		{
			name:  "set password function after statement double quoted",
			input: `SELECT 1; SET PASSWORD FOR 'u'@'h' = PASSWORD("p")`,
			want:  `SELECT 1; SET PASSWORD FOR 'u'@'h' = PASSWORD('[REDACTED]')`,
		},
		{
			name:  "set password after dash comment",
			input: "-- c\nSET PASSWORD FOR 'u'@'h' = 'p'",
			want:  "-- c\nSET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password after dash comment double quoted",
			input: "-- c\nSET PASSWORD FOR 'u'@'h' = \"p\"",
			want:  "-- c\nSET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password after hash comment",
			input: "# c\nSET PASSWORD FOR 'u'@'h' = 'p'",
			want:  "# c\nSET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password after hash comment double quoted",
			input: "# c\nSET PASSWORD FOR 'u'@'h' = \"p\"",
			want:  "# c\nSET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password after newline without semicolon",
			input: "ALTER TABLE t ADD c INT\nSET PASSWORD FOR 'u'@'h' = 'p'",
			want:  "ALTER TABLE t ADD c INT\nSET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "set password after newline without semicolon double quoted",
			input: "ALTER TABLE t ADD c INT\nSET PASSWORD FOR 'u'@'h' = \"p\"",
			want:  "ALTER TABLE t ADD c INT\nSET PASSWORD FOR 'u'@'h' = '[REDACTED]'",
		},
		{
			name:  "grant identified by",
			input: "GRANT ALL ON db.* TO 'u'@'%' IDENTIFIED BY 'x'",
			want:  "GRANT ALL ON db.* TO 'u'@'%' IDENTIFIED BY '[REDACTED]'",
		},
		{
			name:  "postgres create role password",
			input: "CREATE ROLE app LOGIN PASSWORD 'pg-secret'",
			want:  "CREATE ROLE app LOGIN PASSWORD '[REDACTED]'",
		},
		{
			name:  "postgres create user encrypted password double quoted",
			input: `CREATE USER app WITH ENCRYPTED PASSWORD "pg-secret"`,
			want:  `CREATE USER app WITH ENCRYPTED PASSWORD '[REDACTED]'`,
		},
		{
			name:  "postgres alter role encrypted password",
			input: "ALTER ROLE app WITH ENCRYPTED PASSWORD 'pg-secret'",
			want:  "ALTER ROLE app WITH ENCRYPTED PASSWORD '[REDACTED]'",
		},
		{
			name:  "postgres alter user password",
			input: `ALTER USER app PASSWORD "pg-secret"`,
			want:  `ALTER USER app PASSWORD '[REDACTED]'`,
		},
		{
			name:  "core password assignment",
			input: "UPDATE users SET password='hunter2'",
			want:  "UPDATE users SET password='[REDACTED]'",
		},
		{
			name:  "core token assignment",
			input: "UPDATE x SET api_token='sk-live-value'",
			want:  "UPDATE x SET api_token=[REDACTED]",
		},
		{
			name:  "core master password assignment",
			input: "CHANGE MASTER TO MASTER_PASSWORD='x'",
			want:  "CHANGE MASTER TO MASTER_PASSWORD=[REDACTED]",
		},
		{
			name:  "core source password assignment",
			input: "CHANGE REPLICATION SOURCE TO SOURCE_PASSWORD = 'x'",
			want:  "CHANGE REPLICATION SOURCE TO SOURCE_PASSWORD = [REDACTED]",
		},
		{
			name:  "ordinary select unchanged",
			input: "SELECT * FROM users WHERE id=5",
			want:  "SELECT * FROM users WHERE id=5",
		},
		{
			name:  "benign key columns unchanged",
			input: "CREATE TABLE t(primary_key INT, routing_key VARCHAR(64))",
			want:  "CREATE TABLE t(primary_key INT, routing_key VARCHAR(64))",
		},
		{
			name:  "public key ddl unchanged",
			input: "ALTER TABLE t ADD COLUMN public_key TEXT",
			want:  "ALTER TABLE t ADD COLUMN public_key TEXT",
		},
		{
			name:  "plain ddl unchanged",
			input: "ALTER TABLE users ADD COLUMN enabled BOOLEAN",
			want:  "ALTER TABLE users ADD COLUMN enabled BOOLEAN",
		},
		{
			name:  "backtick identifier unchanged",
			input: "ALTER TABLE users ADD COLUMN `PASSWORD` TEXT",
			want:  "ALTER TABLE users ADD COLUMN `PASSWORD` TEXT",
		},
		{
			name:  "ordinary set unchanged",
			input: "SET col=val",
			want:  "SET col=val",
		},
		{
			name:  "password changed column unchanged",
			input: "SET password_changed=NOW()",
			want:  "SET password_changed=NOW()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := redactSQL(tt.input); got != tt.want {
				t.Fatalf("redactSQL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEmitAuditFingerprintsStatementAndFailedStatement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secureMutationAuditTestParent(t, home)

	event := dbgaudit.New(
		dbgaudit.EventTypeDataExec,
		"alice",
		dbgaudit.Context{Name: "prod"},
		dbgaudit.Target{Database: "app", ObjectType: "data", Object: "exec"},
	)
	event.Statement = "CREATE USER 'audit_user'@'%' IDENTIFIED WITH caching_sha2_password BY 'statement-secret'"
	event.FailedStatement = "SET PASSWORD FOR 'failed_user'@'host' = PASSWORD('failed-secret')"
	emitAudit(&cliFlags{Err: io.Discard}, event, nil)

	path, err := coreaudit.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	output := string(data)
	for _, secret := range []string{"statement-secret", "failed-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("audit leaked %q:\n%s", secret, output)
		}
	}
	for _, raw := range []string{"audit_user", "failed_user", "caching_sha2_password", "[REDACTED]"} {
		if strings.Contains(output, raw) {
			t.Fatalf("audit retained raw SQL fragment %q:\n%s", raw, output)
		}
	}
	if !strings.Contains(output, `"statementFingerprint":"sha256:`) ||
		!strings.Contains(output, `"failedStatementFingerprint":"sha256:`) {
		t.Fatalf("audit lacks SQL fingerprints:\n%s", output)
	}
}

func TestSQLOutputRenderersRedactWithoutChangingInputs(t *testing.T) {
	impact := int64(1)
	dataPlan := dataExecPlan{
		SQL:        "UPDATE users SET api_token='plan-secret' WHERE id=5",
		Kind:       "UPDATE",
		ImpactRows: &impact,
		Risk:       "R1",
	}
	dataResult := dataExecResult{
		SQL:          "UPDATE users SET password='result-secret' WHERE id=5",
		Risk:         "R1",
		ImpactRows:   &impact,
		AffectedRows: 1,
	}
	schemaValue := schemaPlan{
		Statements: []schemaPlanStatement{{
			SQL:    "CREATE USER 'schema_user'@'%' IDENTIFIED BY 'schema-secret'",
			Action: schema.ActionCreateTable,
			Table:  "schema_user",
			Risk:   "R1",
		}},
		OverallRisk: "R1",
	}
	dump := schemaDumpResult{
		Tables: []schemaDumpTable{{
			Name: "grant",
			DDL:  "GRANT ALL ON app.* TO 'dump_user'@'%' IDENTIFIED BY 'dump-secret'",
		}},
	}

	tests := []struct {
		name string
		run  func(*cliFlags) error
	}{
		{name: "data plan", run: func(f *cliFlags) error { return printDataExecPlan(f, contextMeta{}, dataPlan) }},
		{name: "data result", run: func(f *cliFlags) error { return printDataExecResult(f, contextMeta{}, dataResult) }},
		{name: "schema plan", run: func(f *cliFlags) error { return printSchemaPlan(f, contextMeta{}, targetWrite, schemaValue) }},
		{name: "schema dump", run: func(f *cliFlags) error { return printSchemaDump(f, contextMeta{}, dump) }},
	}
	for _, format := range []string{"json", "table"} {
		for _, tt := range tests {
			t.Run(format+"_"+tt.name, func(t *testing.T) {
				var output bytes.Buffer
				if err := tt.run(&cliFlags{Output: format, Out: &output, Err: io.Discard}); err != nil {
					t.Fatalf("render error = %v", err)
				}
				rendered := output.String()
				for _, secret := range []string{"plan-secret", "result-secret", "schema-secret", "dump-secret"} {
					if strings.Contains(rendered, secret) {
						t.Fatalf("%s leaked %q:\n%s", tt.name, secret, rendered)
					}
				}
				if !strings.Contains(rendered, "[REDACTED]") {
					t.Fatalf("%s output missing redaction:\n%s", tt.name, rendered)
				}
			})
		}
	}

	if !strings.Contains(dataPlan.SQL, "plan-secret") ||
		!strings.Contains(dataResult.SQL, "result-secret") ||
		!strings.Contains(schemaValue.Statements[0].SQL, "schema-secret") ||
		!strings.Contains(dump.Tables[0].DDL, "dump-secret") {
		t.Fatal("renderers mutated execution inputs")
	}
}
