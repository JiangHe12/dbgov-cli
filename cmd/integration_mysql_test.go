package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
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
