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
	}
	for _, tt := range tests {
		kind, hasWhere, ok := ClassifyDML(tt.sql)
		if kind != tt.kind || hasWhere != tt.hasWhere || ok != tt.ok {
			t.Fatalf("ClassifyDML(%q) = %v/%t/%t, want %v/%t/%t", tt.sql, kind, hasWhere, ok, tt.kind, tt.hasWhere, tt.ok)
		}
	}
}
