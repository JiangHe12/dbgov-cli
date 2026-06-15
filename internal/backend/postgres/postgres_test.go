package postgres

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/JiangHe12/opskit-core/apperrors"

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

func TestPostgresExecDMLReturnsNotImplemented(t *testing.T) {
	t.Parallel()

	backend := &Backend{}
	_, err := backend.ExecDML(context.Background(), "UPDATE t SET a=1")
	appErr := apperrors.AsAppError(err)
	if appErr.Code != apperrors.CodeNotImplemented {
		t.Fatalf("code = %s, want %s", appErr.Code, apperrors.CodeNotImplemented)
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
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "is_primary"}).
			AddRow("orders", "id", "integer", true, nil, true).
			AddRow("orders", "user_id", "integer", true, nil, false).
			AddRow("orders", "name", "character varying(100)", false, "'new'::character varying", false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}).
			AddRow("orders", "orders_pkey", "id", true, 1).
			AddRow("orders", "orders_user_id_idx", "user_id", false, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}).
			AddRow("orders", "orders_user_id_fkey", "user_id", "users", "id", 1))

	current, err := backend.IntrospectSchema(context.Background())
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	table := current.Tables["orders"]
	if table.Name != "orders" || len(table.Columns) != 3 {
		t.Fatalf("orders table = %+v", table)
	}
	if table.Columns[0].Name != "id" || table.Columns[0].Type != "integer" || table.Columns[0].Nullable || table.Columns[0].Key != "PRI" {
		t.Fatalf("id column = %+v", table.Columns[0])
	}
	if table.Columns[2].Default == nil || *table.Columns[2].Default != "'new'::character varying" {
		t.Fatalf("default column = %+v", table.Columns[2])
	}
	if len(table.Indexes) != 2 || table.Indexes[0].Name != "orders_pkey" || !table.Indexes[0].Unique || table.Indexes[1].Columns[0] != "user_id" {
		t.Fatalf("indexes = %+v", table.Indexes)
	}
	if len(table.ForeignKeys) != 1 || table.ForeignKeys[0].RefTable != "users" || table.ForeignKeys[0].RefColumns[0] != "id" {
		t.Fatalf("foreign keys = %+v", table.ForeignKeys)
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
		{Action: schema.ActionModifyColumn, Table: `evil";--`, Column: `name";--`, Type: "character varying(20)"},
		{Action: schema.ActionDropColumn, Table: `evil";--`, Column: `old";--`},
		{Action: schema.ActionDropTable, Table: `old";--`},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	joined := strings.Join(statements, "\n")
	for _, want := range []string{
		`CREATE TABLE "evil"";--" ("id"";DROP" integer);`,
		`ALTER TABLE "evil"";--" ADD COLUMN "name"";--" text;`,
		`ALTER TABLE "evil"";--" ALTER COLUMN "name"";--" TYPE character varying(20);`,
		`ALTER TABLE "evil"";--" DROP COLUMN "old"";--";`,
		`DROP TABLE "old"";--";`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("RenderDDL missing %q:\n%s", want, joined)
		}
	}
}

func TestTableDDLRebuildsPostgresCreateTable(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	expectIntrospectForTableDDL(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT con.conname,")).
		WithArgs(defaultSchema, `evil";--`).
		WillReturnRows(sqlmock.NewRows([]string{"conname", "contype", "columns", "referenced_table", "referenced_columns"}).
			AddRow(`evil";--_pkey`, "p", `id";--`, nil, nil).
			AddRow(`evil";--_user_fkey`, "f", "user_id", "users", "id"))

	ddl, err := backend.TableDDL(context.Background(), `evil";--`)
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	for _, want := range []string{
		`CREATE TABLE "evil"";--"`,
		`"id"";--" integer NOT NULL`,
		`"name" text DEFAULT 'new'::text`,
		`CONSTRAINT "evil"";--_pkey" PRIMARY KEY ("id"";--")`,
		`CONSTRAINT "evil"";--_user_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id")`,
	} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("TableDDL missing %q:\n%s", want, ddl)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExecDDLStopsOnPostgresFailure(t *testing.T) {
	t.Parallel()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "app")
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "ok" (id integer);`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`CREATE TABLE "bad" (id nope);`)).WillReturnError(assertErr("boom"))

	executed, err := backend.ExecDDL(context.Background(), []string{`CREATE TABLE "ok" (id integer);`, `CREATE TABLE "bad" (id nope);`})
	if executed != 1 {
		t.Fatalf("executed = %d, want 1", executed)
	}
	if appErr := apperrors.AsAppError(err); appErr.Code != apperrors.CodeBackendError {
		t.Fatalf("error = %v, want backend error", err)
	}
}

func expectIntrospectForTableDDL(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT c.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type", "not_null", "column_default", "is_primary"}).
			AddRow(`evil";--`, `id";--`, "integer", true, nil, true).
			AddRow(`evil";--`, "user_id", "integer", true, nil, false).
			AddRow(`evil";--`, "name", "text", false, "'new'::text", false))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "index_name", "column_name", "is_unique", "ordinal"}).
			AddRow(`evil";--`, `evil";--_pkey`, `id";--`, true, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT tbl.relname AS table_name")).
		WithArgs(defaultSchema).
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "constraint_name", "column_name", "referenced_table_name", "referenced_column_name", "ordinal"}).
			AddRow(`evil";--`, `evil";--_user_fkey`, "user_id", "users", "id", 1))
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}
