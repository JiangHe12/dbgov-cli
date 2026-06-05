package mysql

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

func TestIntrospectSchemaQueriesInformationSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, column_name, column_type, is_nullable, column_default, column_key\nFROM information_schema.columns\nWHERE table_schema = ?\nORDER BY table_name, ordinal_position")).
		WithArgs("appdb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "is_nullable", "column_default", "column_key"}).
			AddRow("users", "id", "bigint", "NO", nil, "PRI").
			AddRow("users", "org_id", "bigint", "YES", nil, "MUL").
			AddRow("users", "name", "varchar(100)", "YES", "anonymous", ""))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, index_name, column_name, non_unique, seq_in_index\nFROM information_schema.statistics\nWHERE table_schema = ?\nORDER BY table_name, index_name, seq_in_index")).
		WithArgs("appdb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "non_unique", "seq_in_index"}).
			AddRow("users", "PRIMARY", "id", 0, 1).
			AddRow("users", "idx_users_org", "org_id", 1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT kcu.table_name, kcu.constraint_name, kcu.column_name, kcu.referenced_table_name, kcu.referenced_column_name, kcu.ordinal_position\nFROM information_schema.key_column_usage kcu\nJOIN information_schema.referential_constraints rc\n  ON rc.constraint_schema = kcu.constraint_schema\n AND rc.constraint_name = kcu.constraint_name\nWHERE kcu.table_schema = ?\n  AND kcu.referenced_table_name IS NOT NULL\nORDER BY kcu.table_name, kcu.constraint_name, kcu.ordinal_position")).
		WithArgs("appdb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal_position"}).
			AddRow("users", "fk_users_org", "org_id", "orgs", "id", 1))

	schema, err := backend.IntrospectSchema(context.Background())
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	users := schema.Tables["users"]
	if len(users.Columns) != 3 || users.Columns[0].Key != "PRI" || !users.Columns[1].Nullable {
		t.Fatalf("schema = %+v", schema)
	}
	if users.Columns[2].Default == nil || *users.Columns[2].Default != "anonymous" {
		t.Fatalf("default not parsed: %+v", users.Columns[2])
	}
	if len(users.Indexes) != 2 || users.Indexes[0].Name != "PRIMARY" || !users.Indexes[0].Unique {
		t.Fatalf("indexes = %+v", users.Indexes)
	}
	if len(users.ForeignKeys) != 1 || users.ForeignKeys[0].RefTable != "orgs" {
		t.Fatalf("foreign keys = %+v", users.ForeignKeys)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestIntrospectSchemaAllowsEmptyDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "emptydb")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, column_name, column_type, is_nullable, column_default, column_key\nFROM information_schema.columns\nWHERE table_schema = ?\nORDER BY table_name, ordinal_position")).
		WithArgs("emptydb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "is_nullable", "column_default", "column_key"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, index_name, column_name, non_unique, seq_in_index\nFROM information_schema.statistics\nWHERE table_schema = ?\nORDER BY table_name, index_name, seq_in_index")).
		WithArgs("emptydb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "non_unique", "seq_in_index"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT kcu.table_name, kcu.constraint_name, kcu.column_name, kcu.referenced_table_name, kcu.referenced_column_name, kcu.ordinal_position\nFROM information_schema.key_column_usage kcu\nJOIN information_schema.referential_constraints rc\n  ON rc.constraint_schema = kcu.constraint_schema\n AND rc.constraint_name = kcu.constraint_name\nWHERE kcu.table_schema = ?\n  AND kcu.referenced_table_name IS NOT NULL\nORDER BY kcu.table_name, kcu.constraint_name, kcu.ordinal_position")).
		WithArgs("emptydb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal_position"}))

	got, err := backend.IntrospectSchema(context.Background())
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	if len(got.Tables) != 0 {
		t.Fatalf("Tables = %+v, want empty", got.Tables)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestTableDDLUsesShowCreateTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("SHOW CREATE TABLE `users`")).
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow("users", "CREATE TABLE `users` (`id` bigint)"))

	ddl, err := backend.TableDDL(context.Background(), "users")
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	if ddl != "CREATE TABLE `users` (`id` bigint)" {
		t.Fatalf("TableDDL() = %q", ddl)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRenderDDLUsesMySQLSyntax(t *testing.T) {
	backend := NewWithDB(nil, "appdb")
	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionCreateTable, Table: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT"}, {Name: "user_id", Type: "BIGINT"}}},
		{Action: schema.ActionAddColumn, Table: "users", Column: "name", Type: "VARCHAR(100)"},
		{Action: schema.ActionModifyColumn, Table: "users", Column: "name", Type: "TEXT"},
		{Action: schema.ActionDropColumn, Table: "users", Column: "legacy"},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	want := []string{
		"CREATE TABLE `orders` (`id` BIGINT, `user_id` BIGINT);",
		"ALTER TABLE `users` ADD COLUMN `name` VARCHAR(100);",
		"ALTER TABLE `users` MODIFY COLUMN `name` TEXT;",
		"ALTER TABLE `users` DROP COLUMN `legacy`;",
	}
	if len(statements) != len(want) {
		t.Fatalf("statements = %+v", statements)
	}
	for i := range want {
		if statements[i] != want[i] {
			t.Fatalf("statement[%d] = %q, want %q", i, statements[i], want[i])
		}
	}
}

func TestExecDDLStopsAtFirstError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `users` ADD COLUMN `name` VARCHAR(100);")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `users` DROP COLUMN `legacy`;")).
		WillReturnError(errors.New("boom"))

	executed, err := backend.ExecDDL(context.Background(), []string{
		"ALTER TABLE `users` ADD COLUMN `name` VARCHAR(100);",
		"ALTER TABLE `users` DROP COLUMN `legacy`;",
		"ALTER TABLE `users` ADD COLUMN `after` BIGINT;",
	})
	if err == nil {
		t.Fatal("ExecDDL() error = nil, want failure")
	}
	if executed != 1 {
		t.Fatalf("executed = %d, want 1", executed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExecDMLCommitsTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	affected, err := backend.ExecDML(context.Background(), "UPDATE users SET name='x' WHERE id=1")
	if err != nil {
		t.Fatalf("ExecDML() error = %v", err)
	}
	if affected != 3 {
		t.Fatalf("affected = %d, want 3", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExecDMLRollsBackOnFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM users WHERE id=1")).
		WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	affected, err := backend.ExecDML(context.Background(), "DELETE FROM users WHERE id=1")
	if err == nil {
		t.Fatal("ExecDML() error = nil, want failure")
	}
	if affected != 0 {
		t.Fatalf("affected = %d, want 0", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestQueryReturnsColumnsAndStringRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice"))

	result, err := backend.Query(context.Background(), "SELECT id, name FROM users")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if result.Columns[0] != "id" || result.Rows[0][0] != "1" || result.Rows[0][1] != "alice" {
		t.Fatalf("Query() = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExplainEstimatesRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN SELECT * FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "select_type", "table", "rows"}).
			AddRow(1, "SIMPLE", "users", 42))

	result, err := backend.Explain(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if result.EstimatedRows != 42 || result.Columns[3] != "rows" || result.Rows[0][2] != "users" {
		t.Fatalf("Explain() = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}
