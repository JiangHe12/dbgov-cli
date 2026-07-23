package mysql

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/JiangHe12/opskit-core/v2/apperrors"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

func TestIntrospectSchemaQueriesInformationSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, column_name, column_type, is_nullable, column_default, column_key, extra\nFROM information_schema.columns\nWHERE table_schema = ?\nORDER BY table_name, ordinal_position")).
		WithArgs("appdb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "is_nullable", "column_default", "column_key", "extra"}).
			AddRow("users", "id", "bigint", "NO", nil, "PRI", "auto_increment").
			AddRow("users", "org_id", "bigint", "YES", nil, "MUL", "").
			AddRow("users", "name", "varchar(100)", "YES", "anonymous", "", ""))
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
	if !users.Columns[0].AutoIncrement {
		t.Fatalf("id autoIncrement = false, want true: %+v", users.Columns[0])
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, column_name, column_type, is_nullable, column_default, column_key, extra\nFROM information_schema.columns\nWHERE table_schema = ?\nORDER BY table_name, ordinal_position")).
		WithArgs("emptydb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "is_nullable", "column_default", "column_key", "extra"}))
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
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow("users", "CREATE TABLE `users` (`id` int AUTO_INCREMENT, `flags` int unsigned, PRIMARY KEY (`id`))"))

	ddl, err := backend.TableDDL(context.Background(), "users")
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	if ddl != "CREATE TABLE `users` (`id` int AUTO_INCREMENT, `flags` int unsigned, PRIMARY KEY (`id`))" {
		t.Fatalf("TableDDL() = %q", ddl)
	}
	if _, err := schema.ParseDesiredSQL(ddl); apperrors.AsAppError(err).Code != apperrors.CodeNotImplemented {
		t.Fatalf("ParseDesiredSQL(TableDDL) error = %v, want fail-closed rejection for primary key", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestTableDDLMissingTableIsResourceNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("SHOW CREATE TABLE `missing`")).
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}))

	_, err = backend.TableDDL(context.Background(), "missing")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeResourceNotFound {
		t.Fatalf("error code = %s, want %s; err = %v", got, apperrors.CodeResourceNotFound, err)
	}
}

func TestRenderDDLUsesMySQLSyntax(t *testing.T) {
	backend := NewWithDB(nil, "appdb")
	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionCreateTable, Table: "orders", Columns: []schema.Column{{Name: "id", Type: "BIGINT", AutoIncrement: true}, {Name: "user_id", Type: "BIGINT"}}},
		{Action: schema.ActionAddColumn, Table: "users", Column: "name", Type: "VARCHAR(100)"},
		{Action: schema.ActionModifyColumn, Table: "users", Column: "name", Type: "TEXT"},
		{Action: schema.ActionAddColumn, Table: "users", Column: "seq", Type: "int unsigned", AutoIncrement: true, AutoIncrementIndexRequired: true},
		{Action: schema.ActionModifyColumn, Table: "users", Column: "seq", Type: "int unsigned", AutoIncrement: true, AutoIncrementChanged: true, AutoIncrementIndexRequired: true},
		{Action: schema.ActionDropColumn, Table: "users", Column: "legacy"},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	want := []string{
		"CREATE TABLE `orders` (`id` BIGINT AUTO_INCREMENT, `user_id` BIGINT, PRIMARY KEY (`id`));",
		"ALTER TABLE `users` ADD COLUMN `name` VARCHAR(100);",
		"ALTER TABLE `users` MODIFY COLUMN `name` TEXT;",
		"ALTER TABLE `users` ADD COLUMN `seq` int unsigned AUTO_INCREMENT, ADD UNIQUE KEY `idx_users_seq_autoinc` (`seq`);",
		"ALTER TABLE `users` MODIFY COLUMN `seq` int unsigned AUTO_INCREMENT, ADD UNIQUE KEY `idx_users_seq_autoinc` (`seq`);",
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

func TestRenderDDLDoesNotAddSecondPrimaryKeyForAutoIncrementOnExistingTable(t *testing.T) {
	backend := NewWithDB(nil, "appdb")
	current := schema.Schema{Tables: map[string]schema.Table{
		"users": {
			Name:    "users",
			Columns: []schema.Column{{Name: "id", Type: "BIGINT", Key: "PRI"}, {Name: "seq", Type: "int"}},
			Indexes: []schema.Index{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
		},
	}}
	desiredAdd := schema.Schema{Tables: map[string]schema.Table{
		"users": {
			Name:    "users",
			Columns: []schema.Column{{Name: "id", Type: "BIGINT"}, {Name: "seq", Type: "int"}, {Name: "new_seq", Type: "int", AutoIncrement: true}},
		},
	}}
	desiredModify := schema.Schema{Tables: map[string]schema.Table{
		"users": {
			Name:    "users",
			Columns: []schema.Column{{Name: "id", Type: "BIGINT"}, {Name: "seq", Type: "int", AutoIncrement: true}},
		},
	}}

	for _, tc := range []struct {
		name string
		diff schema.DiffResult
		want string
	}{
		{name: "add", diff: schema.Diff(current, desiredAdd), want: "ALTER TABLE `users` ADD COLUMN `new_seq` int AUTO_INCREMENT, ADD UNIQUE KEY `idx_users_new_seq_autoinc` (`new_seq`);"},
		{name: "modify", diff: schema.Diff(current, desiredModify), want: "ALTER TABLE `users` MODIFY COLUMN `seq` int AUTO_INCREMENT, ADD UNIQUE KEY `idx_users_seq_autoinc` (`seq`);"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := backend.RenderDDL(tc.diff.Changes)
			if err != nil {
				t.Fatalf("RenderDDL() error = %v", err)
			}
			if len(statements) != 1 || statements[0] != tc.want {
				t.Fatalf("statements = %+v, want %q", statements, tc.want)
			}
			if strings.Contains(statements[0], "ADD PRIMARY KEY") {
				t.Fatalf("statement adds duplicate primary key: %s", statements[0])
			}
		})
	}
}

func TestAutoIncrementIndexNameKeepsShortNamesUnchanged(t *testing.T) {
	got := autoIncrementIndexName("users", "seq")
	if got != "idx_users_seq_autoinc" {
		t.Fatalf("autoIncrementIndexName() = %q, want old short-name format", got)
	}
}

func TestAutoIncrementIndexNameCapsLongNamesDeterministically(t *testing.T) {
	table := strings.Repeat("very_long_table_name_", 4)
	column := strings.Repeat("very_long_column_name_", 4)

	first := autoIncrementIndexName(table, column)
	second := autoIncrementIndexName(table, column)
	if first != second {
		t.Fatalf("autoIncrementIndexName() not deterministic: %q vs %q", first, second)
	}
	if len(first) > mysqlIdentifierMaxLen {
		t.Fatalf("index name length = %d, want <= %d: %q", len(first), mysqlIdentifierMaxLen, first)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(first) {
		t.Fatalf("index name contains unsafe identifier characters: %q", first)
	}
	natural := "idx_" + table + "_" + column + "_autoinc"
	if len(natural) <= mysqlIdentifierMaxLen {
		t.Fatalf("test fixture natural name length = %d, want > %d", len(natural), mysqlIdentifierMaxLen)
	}
	if first == natural {
		t.Fatalf("long name was not shortened: %q", first)
	}
}

func TestRenderDDLErrorCodes(t *testing.T) {
	backend := NewWithDB(nil, "appdb")
	tests := []struct {
		name   string
		change schema.Change
		code   apperrors.ErrorCode
	}{
		{name: "create without columns", change: schema.Change{Action: schema.ActionCreateTable, Table: "empty"}, code: apperrors.CodeValidationFailed},
		{name: "unsupported action", change: schema.Change{Action: schema.Action("UNKNOWN"), Table: "users"}, code: apperrors.CodeNotImplemented},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := backend.RenderDDL([]schema.Change{tc.change})
			if got := apperrors.AsAppError(err).Code; got != tc.code {
				t.Fatalf("error code = %s, want %s; err = %v", got, tc.code, err)
			}
		})
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
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeBackendError {
		t.Fatalf("error code = %s, want %s; err = %v", got, apperrors.CodeBackendError, err)
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

func TestExecDMLErrorsAreBackendErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{
			name: "begin",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin().WillReturnError(errors.New("begin boom"))
			},
		},
		{
			name: "exec",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnError(errors.New("exec boom"))
				mock.ExpectRollback()
			},
		},
		{
			name: "rows affected",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnResult(sqlmock.NewErrorResult(errors.New("rows boom")))
				mock.ExpectRollback()
			},
		},
		{
			name: "commit",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit().WillReturnError(errors.New("commit boom"))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			backend := NewWithDB(db, "appdb")
			tc.setup(mock)

			affected, err := backend.ExecDML(context.Background(), "UPDATE users SET name='x' WHERE id=1")
			if affected != 0 {
				t.Fatalf("affected = %d, want 0", affected)
			}
			assertBackendErrorExit(t, err)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
		})
	}
}

func TestQueryReturnsColumnsAndStringRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice"))
	mock.ExpectRollback()

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

func TestQueryDistinguishesSQLNullFromEmptyString(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT nullable_value, empty_value FROM sample")).
		WillReturnRows(sqlmock.NewRows([]string{"nullable_value", "empty_value"}).AddRow(nil, ""))
	mock.ExpectRollback()

	result, err := backend.Query(context.Background(), "SELECT nullable_value, empty_value FROM sample")
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(result.Nulls) != 1 || len(result.Nulls[0]) != 2 || !result.Nulls[0][0] || result.Nulls[0][1] {
		t.Fatalf("Query() null map = %#v", result.Nulls)
	}
	if result.Rows[0][0] != "" || result.Rows[0][1] != "" {
		t.Fatalf("Query() rows = %#v", result.Rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestQueryReturnsRollbackError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))

	if _, err := backend.Query(context.Background(), "SELECT id FROM users"); err == nil {
		t.Fatal("Query() error = nil, want rollback failure")
	} else {
		assertBackendErrorExit(t, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestQueryErrorIsBackendError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM missing")).WillReturnError(errors.New("table missing"))
	mock.ExpectRollback()

	_, err = backend.Query(context.Background(), "SELECT * FROM missing")
	assertBackendErrorExit(t, err)
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
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN SELECT * FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "select_type", "table", "rows"}).
			AddRow(1, "SIMPLE", "users", 42))
	mock.ExpectRollback()

	result, err := backend.Explain(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if result.EstimatedRows != 42 ||
		result.PlanFingerprint == "" ||
		result.Columns[3] != "rows" ||
		result.Rows[0][2] != "users" {
		t.Fatalf("Explain() = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExplainRejectsMultiNodeRowsEstimate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN SELECT * FROM users JOIN orgs ON orgs.id=users.org_id")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "select_type", "table", "rows"}).
			AddRow(1, "SIMPLE", "users", 400).
			AddRow(1, "SIMPLE", "orgs", 400))
	mock.ExpectRollback()

	_, err = backend.Explain(context.Background(), "SELECT * FROM users JOIN orgs ON orgs.id=users.org_id")
	assertBackendErrorExit(t, err)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExplainErrorsAreBackendErrors(t *testing.T) {
	const explainSQL = "EXPLAIN SELECT * FROM missing"
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{
			name: "begin",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin().WillReturnError(errors.New("begin failed"))
			},
		},
		{
			name: "query",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).WillReturnError(errors.New("table missing"))
				mock.ExpectRollback()
			},
		},
		{
			name: "scan",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"rows"}).
						AddRow(1).
						RowError(0, errors.New("scan failed")))
				mock.ExpectRollback()
			},
		},
		{
			name: "close",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"rows"}).
						CloseError(errors.New("close failed")))
				mock.ExpectRollback()
			},
		},
		{
			name: "invalid estimate",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"rows"}).AddRow("unknown"))
				mock.ExpectRollback()
			},
		},
		{
			name: "rollback",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"rows"}).AddRow(1))
				mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			backend := NewWithDB(db, "appdb")
			tc.setup(mock)

			_, err = backend.Explain(context.Background(), "SELECT * FROM missing")
			assertBackendErrorExit(t, err)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
		})
	}
}

func TestExecDMLBoundRevalidatesAndExecutesInOneTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	sqlText := "UPDATE users SET name='x' WHERE id=1"
	explain := dbbackend.QueryResult{
		Columns: []string{"id", "select_type", "table", "rows"},
		Rows:    [][]string{{"1", "SIMPLE", "users", "42"}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN " + sqlText)).
		WillReturnRows(sqlmock.NewRows(explain.Columns).AddRow(1, "SIMPLE", "users", 42))
	mock.ExpectExec(regexp.QuoteMeta(sqlText)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	affected, err := backend.ExecDMLBound(context.Background(), sqlText, dbbackend.DMLPlanBinding{
		PlanFingerprint: dbbackend.PlanFingerprint(explain),
		EstimatedRows:   42,
	})
	if err != nil {
		t.Fatalf("ExecDMLBound() error = %v", err)
	}
	if affected != 3 {
		t.Fatalf("affected = %d, want 3", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExecDMLBoundReportsIndeterminateCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	sqlText := "UPDATE users SET name='x' WHERE id=1"
	explain := dbbackend.QueryResult{
		Columns: []string{"id", "select_type", "table", "rows"},
		Rows:    [][]string{{"1", "SIMPLE", "users", "42"}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN " + sqlText)).
		WillReturnRows(sqlmock.NewRows(explain.Columns).AddRow(1, "SIMPLE", "users", 42))
	mock.ExpectExec(regexp.QuoteMeta(sqlText)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit().WillReturnError(errors.New("commit result lost"))

	affected, err := backend.ExecDMLBound(context.Background(), sqlText, dbbackend.DMLPlanBinding{
		PlanFingerprint: dbbackend.PlanFingerprint(explain),
		EstimatedRows:   42,
	})
	if affected != 3 {
		t.Fatalf("affected = %d, want observed value 3", affected)
	}
	if !dbbackend.IsCommitIndeterminate(err) {
		t.Fatalf("error = %v, want indeterminate commit", err)
	}
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodePartialFailure || appErr.Retryable || apperrors.ExitCode(err) != 11 {
		t.Fatalf("commit error = %+v, want non-retryable PARTIAL_FAILURE exit 11", appErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestExecDMLBoundRejectsChangedPlanBeforeExecution(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	sqlText := "UPDATE users SET name='x' WHERE id=1"
	preview := dbbackend.QueryResult{
		Columns: []string{"id", "select_type", "table", "rows"},
		Rows:    [][]string{{"1", "SIMPLE", "users", "42"}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN " + sqlText)).
		WillReturnRows(sqlmock.NewRows(preview.Columns).AddRow(1, "SIMPLE", "users", 5000))
	mock.ExpectRollback()

	affected, err := backend.ExecDMLBound(context.Background(), sqlText, dbbackend.DMLPlanBinding{
		PlanFingerprint: dbbackend.PlanFingerprint(preview),
		EstimatedRows:   42,
	})
	if affected != 0 {
		t.Fatalf("affected = %d, want 0", affected)
	}
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeConflict {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeConflict, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func assertBackendErrorExit(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want backend error")
	}
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeBackendError {
		t.Fatalf("code = %s, want %s (err=%v)", got, apperrors.CodeBackendError, err)
	}
	if got := apperrors.ExitCode(err); got != 7 {
		t.Fatalf("exit code = %d, want 7 (err=%v)", got, err)
	}
}
