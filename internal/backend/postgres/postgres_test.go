package postgres

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

func TestPlanRowsFromExplainJSON(t *testing.T) {
	t.Parallel()

	got, err := planRowsFromExplainJSON(`[{"Plan":{"Node Type":"Seq Scan","Plan Rows":42}}]`)
	if err != nil {
		t.Fatalf("planRowsFromExplainJSON() error = %v", err)
	}
	if got != 42 {
		t.Fatalf("planRowsFromExplainJSON() = %d, want 42", got)
	}
}

func TestPlanRowsFromExplainJSONUsesModifyTableInputRows(t *testing.T) {
	t.Parallel()

	const plan = `[{"Plan":{"Node Type":"ModifyTable","Operation":"Update","Plan Rows":0,"Plans":[{"Node Type":"Seq Scan","Parent Relationship":"Outer","Plan Rows":5000}]}}]`
	got, err := planRowsFromExplainJSON(plan)
	if err != nil {
		t.Fatalf("planRowsFromExplainJSON() error = %v", err)
	}
	if got != 5000 {
		t.Fatalf("planRowsFromExplainJSON() = %d, want 5000", got)
	}
}

func TestPlanRowsFromExplainJSONRejectsModifyTableWithoutInput(t *testing.T) {
	t.Parallel()

	_, err := planRowsFromExplainJSON(`[{"Plan":{"Node Type":"ModifyTable","Operation":"Delete","Plan Rows":0}}]`)
	if err == nil {
		t.Fatal("planRowsFromExplainJSON() error = nil, want fail-closed error")
	}
}

func TestExecDMLCommitsPostgresTransaction(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	affected, err := backend.ExecDML(context.Background(), "UPDATE users SET name='x' WHERE id=1")
	if err != nil {
		t.Fatalf("ExecDML() error = %v", err)
	}
	if affected != 3 {
		t.Fatalf("affected = %d, want 3", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecDMLRollsBackPostgresTransactionOnFailure(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnError(assertErr("boom"))
	mock.ExpectRollback()

	affected, err := backend.ExecDML(context.Background(), "UPDATE users SET name='x' WHERE id=1")
	if err == nil {
		t.Fatal("ExecDML() error = nil, want failure")
	}
	if affected != 0 {
		t.Fatalf("affected = %d, want 0", affected)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresExecDMLErrorsAreBackendErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
	}{
		{
			name: "begin",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin().WillReturnError(assertErr("begin boom"))
			},
		},
		{
			name: "exec",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnError(assertErr("exec boom"))
				mock.ExpectRollback()
			},
		},
		{
			name: "rows affected",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnResult(sqlmock.NewErrorResult(assertErr("rows boom")))
				mock.ExpectRollback()
			},
		},
		{
			name: "commit",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET name='x' WHERE id=1")).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit().WillReturnError(assertErr("commit boom"))
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
			backend := NewWithDB(db, "app")
			tc.setup(mock)

			affected, err := backend.ExecDML(context.Background(), "UPDATE users SET name='x' WHERE id=1")
			if affected != 0 {
				t.Fatalf("affected = %d, want 0", affected)
			}
			assertBackendErrorExit(t, err)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresQueryErrorIsBackendError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM missing")).WillReturnError(errors.New("table missing"))
	mock.ExpectRollback()

	_, err = backend.Query(context.Background(), "SELECT * FROM missing")
	assertBackendErrorExit(t, err)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresQueryRollsBackSuccessfulRead(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
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
		t.Fatal(err)
	}
}

func TestPostgresQueryDistinguishesSQLNullFromEmptyString(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
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
		t.Fatal(err)
	}
}

func TestPostgresQueryReturnsRollbackError(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
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
		t.Fatal(err)
	}
}

func TestPostgresExplainRollsBackSuccessfulRead(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN (FORMAT JSON) SELECT * FROM users")).
		WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}).
			AddRow(`[{"Plan":{"Plan Rows":42}}]`))
	mock.ExpectRollback()

	result, err := backend.Explain(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if result.EstimatedRows != 42 || result.PlanFingerprint == "" || result.Columns[0] != "QUERY PLAN" {
		t.Fatalf("Explain() = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresExplainErrorsAreBackendErrors(t *testing.T) {
	t.Parallel()

	const explainSQL = "EXPLAIN (FORMAT JSON) SELECT * FROM missing"
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
					WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}).
						AddRow(`[{"Plan":{"Plan Rows":1}}]`).
						RowError(0, errors.New("scan failed")))
				mock.ExpectRollback()
			},
		},
		{
			name: "close",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}).
						CloseError(errors.New("close failed")))
				mock.ExpectRollback()
			},
		},
		{
			name: "invalid estimate",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}).
						AddRow(`[{"Plan":{"Node Type":"Seq Scan"}}]`))
				mock.ExpectRollback()
			},
		},
		{
			name: "rollback",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(explainSQL)).
					WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}).
						AddRow(`[{"Plan":{"Plan Rows":1}}]`))
				mock.ExpectRollback().WillReturnError(errors.New("rollback failed"))
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			backend := NewWithDB(db, "app")
			tc.setup(mock)

			_, err = backend.Explain(context.Background(), "SELECT * FROM missing")
			assertBackendErrorExit(t, err)
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresExecDMLBoundRevalidatesAndExecutesInOneTransaction(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	sqlText := "UPDATE users SET name='x' WHERE id=1"
	planJSON := `[{"Plan":{"Plan Rows":42}}]`
	explain := dbbackend.QueryResult{
		Columns: []string{"QUERY PLAN"},
		Rows:    [][]string{{planJSON}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN (FORMAT JSON) " + sqlText)).
		WillReturnRows(sqlmock.NewRows(explain.Columns).AddRow(planJSON))
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
		t.Fatal(err)
	}
}

func TestPostgresExecDMLBoundReportsIndeterminateCommit(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	sqlText := "UPDATE users SET name='x' WHERE id=1"
	planJSON := `[{"Plan":{"Plan Rows":42}}]`
	explain := dbbackend.QueryResult{
		Columns: []string{"QUERY PLAN"},
		Rows:    [][]string{{planJSON}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN (FORMAT JSON) " + sqlText)).
		WillReturnRows(sqlmock.NewRows(explain.Columns).AddRow(planJSON))
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
		t.Fatal(err)
	}
}

func TestPostgresExecDMLBoundRejectsChangedPlanBeforeExecution(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	sqlText := "UPDATE users SET name='x' WHERE id=1"
	previewJSON := `[{"Plan":{"Plan Rows":42}}]`
	preview := dbbackend.QueryResult{
		Columns: []string{"QUERY PLAN"},
		Rows:    [][]string{{previewJSON}},
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN (FORMAT JSON) " + sqlText)).
		WillReturnRows(sqlmock.NewRows(preview.Columns).
			AddRow(`[{"Plan":{"Plan Rows":5000}}]`))
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
		t.Fatal(err)
	}
}

func TestIntrospectSchemaMapsPostgresCatalog(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "identity_kind", "has_owned_sequence", "has_sequence_dependency", "is_primary"}).
			AddRow("orders", "id", "integer", true, nil, "d", false, false, true).
			AddRow("orders", "user_id", "integer", true, "nextval('orders_user_id_seq'::regclass)", "", true, true, false).
			AddRow("orders", "shared_seq_value", "integer", false, "nextval('shared_seq'::regclass)", "", false, true, false).
			AddRow("orders", "name", "character varying(100)", false, "'new'::character varying", "", false, false, false).
			AddRow("zero_columns", nil, nil, false, nil, nil, false, false, false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}).
			AddRow("orders", "orders_pkey", "id", true, 1).
			AddRow("orders", "orders_user_id_idx", "user_id", false, 1))
	mock.ExpectQuery(`(?s)SELECT tbl\.relname AS table_name,.*JOIN pg_catalog\.unnest\(con\.conkey\) WITH ORDINALITY AS ord.*JOIN pg_catalog\.unnest\(con\.confkey\) WITH ORDINALITY AS ref_ord`).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}).
			AddRow("orders", "orders_user_id_fkey", "user_id", "users", "id", 1))

	current, err := backend.IntrospectSchema(context.Background())
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	table := current.Tables["orders"]
	if table.Name != "orders" || len(table.Columns) != 4 {
		t.Fatalf("orders table = %+v", table)
	}
	if table.Columns[0].Name != "id" || table.Columns[0].Type != "integer" || table.Columns[0].Nullable || table.Columns[0].Key != "PRI" || !table.Columns[0].AutoIncrement {
		t.Fatalf("id column = %+v", table.Columns[0])
	}
	if !table.Columns[1].AutoIncrement || table.Columns[1].Default != nil {
		t.Fatalf("serial-like column = %+v, want autoIncrement without default", table.Columns[1])
	}
	if table.Columns[2].AutoIncrement || table.Columns[2].Default == nil || *table.Columns[2].Default != "nextval('shared_seq'::regclass)" {
		t.Fatalf("shared sequence column = %+v, want regular default", table.Columns[2])
	}
	if table.Columns[3].Default == nil || *table.Columns[3].Default != "'new'::character varying" {
		t.Fatalf("default column = %+v", table.Columns[3])
	}
	if len(table.Indexes) != 2 || table.Indexes[0].Name != "orders_pkey" || !table.Indexes[0].Unique || table.Indexes[1].Columns[0] != "user_id" {
		t.Fatalf("indexes = %+v", table.Indexes)
	}
	if len(table.ForeignKeys) != 1 || table.ForeignKeys[0].RefTable != "users" || table.ForeignKeys[0].RefColumns[0] != "id" {
		t.Fatalf("foreign keys = %+v", table.ForeignKeys)
	}
	if empty, ok := current.Tables["zero_columns"]; !ok || empty.Name != "zero_columns" || len(empty.Columns) != 0 {
		t.Fatalf("zero-column table = %+v, present=%t", empty, ok)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTableDDLRejectsZeroColumnTableInsteadOfOmittingIt(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "identity_kind", "has_owned_sequence", "has_sequence_dependency", "is_primary"}).
			AddRow("zero_columns", nil, nil, false, nil, nil, false, false, false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}))

	_, err = backend.TableDDL(context.Background(), "zero_columns")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeNotImplemented, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRenderDDLEscapesPostgresIdentifiers(t *testing.T) {
	t.Parallel()

	backend := &Backend{}
	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionCreateTable, Table: `evil";--`, Columns: []schema.Column{{Name: `id";DROP`, Type: "integer"}}},
		{Action: schema.ActionAddColumn, Table: `evil";--`, Column: `name";--`, Type: "text"},
		{Action: schema.ActionModifyColumn, Table: `evil";--`, Column: `name";--`, Type: "character varying(20)", TypeChanged: true},
		{Action: schema.ActionDropColumn, Table: `evil";--`, Column: `old";--`},
		{Action: schema.ActionDropTable, Table: `old";--`},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	joined := strings.Join(statements, "\n")
	for _, want := range []string{
		`CREATE TABLE "public"."evil"";--" ("id"";DROP" integer);`,
		`ALTER TABLE "public"."evil"";--" ADD COLUMN "name"";--" text;`,
		`ALTER TABLE "public"."evil"";--" ALTER COLUMN "name"";--" TYPE character varying(20);`,
		`ALTER TABLE "public"."evil"";--" DROP COLUMN "old"";--";`,
		`DROP TABLE "public"."old"";--";`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("RenderDDL missing %q:\n%s", want, joined)
		}
	}
}

func TestRenderDDLRejectsOpaqueCreateTableLike(t *testing.T) {
	backend := &Backend{}
	_, err := backend.RenderDDL([]schema.Change{{
		Action: schema.ActionCreateTable,
		Table:  "copy",
		Opaque: true,
		RawDDL: `CREATE TABLE "public"."copy" (LIKE "public"."source" INCLUDING ALL);`,
	}})
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeNotImplemented, err)
	}
}

func TestRenderDDLUsesPostgresIdentity(t *testing.T) {
	t.Parallel()

	backend := &Backend{}
	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionCreateTable, Table: "ids", Columns: []schema.Column{{Name: "id", Type: "integer", AutoIncrement: true}}},
		{Action: schema.ActionAddColumn, Table: "ids", Column: "next_id", Type: "bigint", AutoIncrement: true},
		{Action: schema.ActionModifyColumn, Table: "ids", Column: "legacy", Type: "integer", AutoIncrement: true, AutoIncrementChanged: true},
		{Action: schema.ActionModifyColumn, Table: "ids", Column: "plain", Type: "integer", AutoIncrement: false, AutoIncrementChanged: true},
		{Action: schema.ActionModifyColumn, Table: "ids", Column: "typed", Type: "bigint", TypeChanged: true},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	want := []string{
		`CREATE TABLE "public"."ids" ("id" integer NOT NULL GENERATED BY DEFAULT AS IDENTITY);`,
		`ALTER TABLE "public"."ids" ADD COLUMN "next_id" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY;`,
		`ALTER TABLE "public"."ids" ALTER COLUMN "legacy" SET NOT NULL, ALTER COLUMN "legacy" ADD GENERATED BY DEFAULT AS IDENTITY;`,
		`ALTER TABLE "public"."ids" ALTER COLUMN "plain" DROP IDENTITY IF EXISTS;`,
		`ALTER TABLE "public"."ids" ALTER COLUMN "typed" TYPE bigint;`,
	}
	for index := range want {
		if statements[index] != want[index] {
			t.Fatalf("statement[%d] = %q, want %q", index, statements[index], want[index])
		}
	}
	if strings.Contains(statements[2], " TYPE ") || strings.Contains(statements[3], " TYPE ") {
		t.Fatalf("identity-only changes must not emit TYPE clauses: %+v", statements)
	}
}

func TestTableDDLRebuildsSupportedPostgresCreateTable(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	expectIntrospectForTableDDL(mock)
	expectSupportedTableDDLFeatures(mock, `evil";--`)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT con.conname,")).
		WithArgs(defaultSchema, `evil";--`).
		WillReturnRows(sqlmock.NewRows([]string{"conname", "contype", "columns", "referenced_schema", "referenced_table", "referenced_columns"}).
			AddRow(`evil";--_pkey`, "p", `["id\";--"]`, nil, nil, nil).
			AddRow(`evil";--_user_fkey`, "f", `["user_id"]`, "public", "users", `["id"]`))

	ddl, err := backend.TableDDL(context.Background(), `evil";--`)
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	for _, want := range []string{
		`CREATE TABLE "public"."evil"";--"`,
		`"id"";--" integer NOT NULL`,
		`"name" text DEFAULT 'new'::text`,
		`CONSTRAINT "evil"";--_pkey" PRIMARY KEY ("id"";--")`,
		`CONSTRAINT "evil"";--_user_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id")`,
	} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("TableDDL missing %q:\n%s", want, ddl)
		}
	}
	if _, err := schema.ParseDesiredSQL(ddl); apperrors.AsAppError(err).Code != apperrors.CodeNotImplemented {
		t.Fatalf("ParseDesiredSQL(TableDDL) error = %v, want fail-closed rejection for constraints\n%s", err, ddl)
	}
	parsed, err := schema.ParseSchemaDDL(ddl)
	if err != nil || !parsed.Tables[`evil";--`].Opaque {
		t.Fatalf("ParseSchemaDDL(TableDDL) = %+v, %v, want opaque round-trip definition", parsed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestParseCatalogListPreservesControlCharacters(t *testing.T) {
	got, err := parseCatalogList(`["left\u001fright","normal"]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "left\x1fright" || got[1] != "normal" {
		t.Fatalf("parseCatalogList() = %#v", got)
	}
}

func TestTableDDLRejectsPostgresIdentityThatCannotRoundTripLosslessly(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	expectIntrospectForIdentityTableDDL(mock)

	_, err = backend.TableDDL(context.Background(), "identity_users")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeNotImplemented, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTableDDLRejectsNestedDynamicNextvalWithoutCatalogDependency(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "identity_kind", "has_owned_sequence", "has_sequence_dependency", "is_primary"}).
			AddRow("sequence_users", "id", "bigint", true, "COALESCE(pg_catalog.nextval(pg_catalog.to_regclass('external.user_ids')), 1)", "", false, false, false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}))

	_, err = backend.TableDDL(context.Background(), "sequence_users")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeNotImplemented, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestContainsPostgresSequenceStateCallIsQuoteAware(t *testing.T) {
	for _, expression := range []string{
		"COALESCE(pg_catalog.nextval(pg_catalog.to_regclass('public.seq')), 1)",
		"pg_catalog.currval(pg_catalog.to_regclass('public.seq'))",
		"pg_catalog.setval(pg_catalog.to_regclass('public.seq'), 42)",
		"pg_catalog.lastval()",
		`"pg_catalog"."nextval"('public.seq')`,
	} {
		if !containsPostgresSequenceStateCall(expression) {
			t.Fatalf("containsPostgresSequenceStateCall(%q) = false", expression)
		}
	}
	for _, expression := range []string{
		`'nextval('::text`,
		`$tag$nextval($tag$::text`,
		`$tag$foo nextval($tag$::text`,
		`$$prefix nextval($$::text`,
		`"nextval"`,
	} {
		if containsPostgresSequenceStateCall(expression) {
			t.Fatalf("containsPostgresSequenceStateCall(%q) = true", expression)
		}
	}
}

func TestTableDDLRejectsConstraintIndexAndRowSecurityDetailsThatCannotRoundTrip(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	expectIntrospectForSimpleTableDDL(mock, "secured_users")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relkind <> 'r'")).
		WithArgs(defaultSchema, "secured_users").
		WillReturnRows(sqlmock.NewRows([]string{
			"unsupported_relkind",
			"unsupported_persistence",
			"partitioned",
			"custom_tablespace",
			"relation_options",
			"inherited",
			"unsupported_constraint",
			"unsupported_index",
			"trigger",
			"policy",
			"generated_column",
			"foreign_key_options",
			"non_default_collation",
			"constraint_options",
			"unsupported_index_shape",
			"row_security",
			"replica_identity",
			"non_default_access_method",
			"rewrite_rule",
			"unsupported_index_semantics",
			"comment",
			"custom_column_storage",
			"typed_table",
			"non_catalog_column_type",
			"non_catalog_default_dependency",
		}).AddRow(false, false, false, false, false, false, false, false, false, false, false, false, false, true, true, true, true, true, true, true, true, true, true, true, true))

	_, err = backend.TableDDL(context.Background(), "secured_users")
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("error code = %s, want %s (err=%v)", got, apperrors.CodeNotImplemented, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecDDLRollsBackAllPostgresStatementsOnFailure(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "ok" (id integer);`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "bad" (id nope);`)).WillReturnError(assertErr("boom"))
	mock.ExpectRollback()

	executed, err := backend.ExecDDL(context.Background(), []string{`CREATE TABLE "ok" (id integer);`, `CREATE TABLE "bad" (id nope);`})
	if executed != 0 {
		t.Fatalf("executed = %d, want 0 committed statements", executed)
	}
	if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeBackendError {
		t.Fatalf("error = %v, want backend error", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecDDLCommitsAllPostgresStatementsTogether(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "one" (id integer);`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "two" (id integer);`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	executed, err := backend.ExecDDL(context.Background(), []string{
		`CREATE TABLE "one" (id integer);`,
		`CREATE TABLE "two" (id integer);`,
	})
	if err != nil {
		t.Fatalf("ExecDDL() error = %v", err)
	}
	if executed != 2 {
		t.Fatalf("executed = %d, want 2", executed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecDDLCommitErrorIsIndeterminateAndNonRetryable(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "one" (id integer);`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "two" (id integer);`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(assertErr("connection lost during commit"))

	executed, err := backend.ExecDDL(context.Background(), []string{
		`CREATE TABLE "one" (id integer);`,
		`CREATE TABLE "two" (id integer);`,
	})
	if executed != 2 {
		t.Fatalf("executed = %d, want 2 statements with uncertain commit outcome", executed)
	}
	if !dbbackend.IsCommitIndeterminate(err) {
		t.Fatalf("error = %v, want indeterminate commit", err)
	}
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodePartialFailure || appErr.Retryable || apperrors.ExitCode(err) != 11 {
		t.Fatalf("commit error = %+v, want non-retryable PARTIAL_FAILURE exit 11", appErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectIntrospectForTableDDL(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "identity_kind", "has_owned_sequence", "has_sequence_dependency", "is_primary"}).
			AddRow(`evil";--`, `id";--`, "integer", true, nil, "", false, false, true).
			AddRow(`evil";--`, "user_id", "integer", true, nil, "", false, false, false).
			AddRow(`evil";--`, "name", "text", false, "'new'::text", "", false, false, false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}).
			AddRow(`evil";--`, `evil";--_pkey`, `id";--`, true, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}).
			AddRow(`evil";--`, `evil";--_user_fkey`, "user_id", "users", "id", 1))
}

func expectSupportedTableDDLFeatures(mock sqlmock.Sqlmock, table string) {
	columns := []string{
		"unsupported_relkind",
		"unsupported_persistence",
		"partitioned",
		"custom_tablespace",
		"relation_options",
		"inherited",
		"unsupported_constraint",
		"unsupported_index",
		"trigger",
		"policy",
		"generated_column",
		"foreign_key_options",
		"non_default_collation",
		"constraint_options",
		"unsupported_index_shape",
		"row_security",
		"replica_identity",
		"non_default_access_method",
		"rewrite_rule",
		"unsupported_index_semantics",
		"comment",
		"custom_column_storage",
		"typed_table",
		"non_catalog_column_type",
		"non_catalog_default_dependency",
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relkind <> 'r'")).
		WithArgs(defaultSchema, table).
		WillReturnRows(sqlmock.NewRows(columns).
			AddRow(false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false))
}

func expectIntrospectForIdentityTableDDL(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "identity_kind", "has_owned_sequence", "has_sequence_dependency", "is_primary"}).
			AddRow("identity_users", "id", "integer", true, nil, "d", false, false, true))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}).
			AddRow("identity_users", "identity_users_pkey", "id", true, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}))
}

func expectIntrospectForSimpleTableDDL(mock sqlmock.Sqlmock, table string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "identity_kind", "has_owned_sequence", "has_sequence_dependency", "is_primary"}).
			AddRow(table, "id", "integer", false, nil, "", false, false, false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}))
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
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
