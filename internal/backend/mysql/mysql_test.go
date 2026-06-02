package mysql

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestIntrospectSchemaQueriesInformationSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	backend := NewWithDB(db, "appdb")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT table_name, column_name, column_type\nFROM information_schema.columns\nWHERE table_schema = ?\nORDER BY table_name, ordinal_position")).
		WithArgs("appdb").
		WillReturnRows(sqlmock.NewRows([]string{"table_name", "column_name", "column_type"}).
			AddRow("users", "id", "bigint").
			AddRow("users", "name", "varchar(100)"))

	schema, err := backend.IntrospectSchema(context.Background())
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	if len(schema.Tables["users"].Columns) != 2 {
		t.Fatalf("schema = %+v", schema)
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
