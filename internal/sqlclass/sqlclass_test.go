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
