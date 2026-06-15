package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

func TestMySQLIntegrationQueryExplainIntrospect(t *testing.T) {
	dsn := os.Getenv("DBGOV_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DBGOV_TEST_MYSQL_DSN is not set")
	}
	database := os.Getenv("DBGOV_TEST_MYSQL_DATABASE")
	if database == "" {
		database = "test"
	}
	backend, err := mysql.New(dsn, database)
	if err != nil {
		t.Fatalf("mysql.New() error = %v", err)
	}
	if err := backend.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if _, err := backend.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if _, err := backend.Explain(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	_, _ = backend.IntrospectSchema(context.Background())
}

func TestMySQLIntegrationAutoIncrementSchema(t *testing.T) {
	dsn := os.Getenv("DBGOV_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DBGOV_TEST_MYSQL_DSN is not set")
	}
	database := os.Getenv("DBGOV_TEST_MYSQL_DATABASE")
	if database == "" {
		database = "test"
	}
	backend, err := mysql.New(dsn, database)
	if err != nil {
		t.Fatalf("mysql.New() error = %v", err)
	}
	ctx := context.Background()
	_, _ = backend.ExecDDL(ctx, []string{"DROP TABLE IF EXISTS `dbgov_ai_mysql`;"})
	t.Cleanup(func() {
		_, _ = backend.ExecDDL(context.Background(), []string{"DROP TABLE IF EXISTS `dbgov_ai_mysql`;"})
	})
	_, err = backend.ExecDDL(ctx, []string{"CREATE TABLE `dbgov_ai_mysql` (`id` int AUTO_INCREMENT, `flags` int unsigned, PRIMARY KEY (`id`));"})
	if err != nil {
		t.Fatalf("create auto increment fixture error = %v", err)
	}
	current, err := backend.IntrospectSchema(ctx)
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	table := current.Tables["dbgov_ai_mysql"]
	if len(table.Columns) < 2 || !table.Columns[0].AutoIncrement || table.Columns[0].Key != "PRI" {
		t.Fatalf("auto increment table = %+v", table)
	}
	ddl, err := backend.TableDDL(ctx, "dbgov_ai_mysql")
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	parsed, err := schema.ParseDesiredSQL(ddl)
	if err != nil {
		t.Fatalf("ParseDesiredSQL(TableDDL) error = %v\n%s", err, ddl)
	}
	expected := schema.Schema{Tables: map[string]schema.Table{
		"dbgov_ai_mysql": {Name: "dbgov_ai_mysql", Columns: []schema.Column{{Name: "id", Type: "int", AutoIncrement: true}, {Name: "flags", Type: "int unsigned"}}},
	}}
	if diff := schema.Diff(expected, parsed); len(diff.Changes) != 0 {
		t.Fatalf("round-trip diff = %+v, want none\nDDL:\n%s", diff.Changes, ddl)
	}
}
