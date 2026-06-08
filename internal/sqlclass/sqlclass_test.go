package sqlclass

import "testing"

func TestIsReadOnly(t *testing.T) {
	readOnly := []string{
		"SELECT * FROM users",
		" show tables",
		"DESCRIBE users",
		"desc users",
		"EXPLAIN SELECT * FROM users",
	}
	for _, sql := range readOnly {
		if !IsReadOnly(sql) {
			t.Fatalf("IsReadOnly(%q) = false", sql)
		}
	}
	writes := []string{
		"UPDATE users SET name='x'",
		"DELETE FROM users",
		"INSERT INTO users VALUES (1)",
		"ALTER TABLE users ADD COLUMN age INT",
		"",
	}
	for _, sql := range writes {
		if IsReadOnly(sql) {
			t.Fatalf("IsReadOnly(%q) = true", sql)
		}
	}
}

func TestIsReadOnlyRecognizesCTEOperativeKeyword(t *testing.T) {
	readOnly := []string{
		"WITH RECURSIVE t AS (SELECT 1 UNION ALL SELECT n+1 FROM t WHERE n<5) SELECT * FROM t",
		"WITH c AS (SELECT * FROM x WHERE id=1) SELECT * FROM c",
		"WITH a AS (SELECT (1 + (2))), b(v) AS (SELECT ')' /* delete */) SELECT * FROM a",
		" \n/* leading */ WiTh ReCuRsIvE `t` (`n`) AS (\nSELECT 1 -- keep reading\n) /* between */ SeLeCt * FROM `t`",
	}
	for _, sql := range readOnly {
		if !IsReadOnly(sql) {
			t.Errorf("IsReadOnly(%q) = false", sql)
		}
	}

	writes := []string{
		"WITH x AS (SELECT 1) DELETE FROM users",
		"WITH x AS (SELECT 1) UPDATE users SET a=1",
		"WITH x AS (SELECT 1) INSERT INTO t VALUES(1)",
		"WITH x AS (SELECT ')', 'delete')",
		"WITH x AS (SELECT 1",
	}
	for _, sql := range writes {
		if IsReadOnly(sql) {
			t.Errorf("IsReadOnly(%q) = true", sql)
		}
	}
}

func TestHasMultipleStatements(t *testing.T) {
	multiple := []string{
		"SELECT 1; DELETE FROM users",
		"SELECT 1; SELECT 2",
		"UPDATE t SET a=1; DROP TABLE t",
		"SELECT 1;;",
		"SELECT 1; /* separator */ DELETE FROM users",
	}
	for _, sql := range multiple {
		if !HasMultipleStatements(sql) {
			t.Errorf("HasMultipleStatements(%q) = false", sql)
		}
	}

	single := []string{
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1; ",
		"SELECT 1; -- tail",
		"SELECT ';' FROM t",
		"SELECT 1 -- ; not real\n",
		"SELECT 1 /* ; not real */",
		"SELECT 1; /* tail */",
		"WITH x AS (SELECT ';') SELECT * FROM x",
	}
	for _, sql := range single {
		if HasMultipleStatements(sql) {
			t.Errorf("HasMultipleStatements(%q) = true", sql)
		}
	}
}

func TestClassifyDML(t *testing.T) {
	tests := []struct {
		sql      string
		kind     Kind
		hasWhere bool
		ok       bool
	}{
		{sql: "INSERT INTO users(id) VALUES (1)", kind: KindInsert, ok: true},
		{sql: "UPDATE users SET name='x' WHERE id = 1", kind: KindUpdate, hasWhere: true, ok: true},
		{sql: "DELETE FROM users", kind: KindDelete, ok: true},
		{sql: "UPDATE users SET somewhere_else = 1", kind: KindUpdate, hasWhere: false, ok: true},
		{sql: "SELECT * FROM users", ok: false},
		{sql: "WITH x AS (SELECT 1) UPDATE users SET name='x' WHERE id=1", ok: false},
	}
	for _, tt := range tests {
		kind, hasWhere, ok := ClassifyDML(tt.sql)
		if kind != tt.kind || hasWhere != tt.hasWhere || ok != tt.ok {
			t.Fatalf("ClassifyDML(%q) = %v/%t/%t, want %v/%t/%t", tt.sql, kind, hasWhere, ok, tt.kind, tt.hasWhere, tt.ok)
		}
	}
}
