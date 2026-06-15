//go:build integration

package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/JiangHe12/dbgov-cli/internal/backend/postgres"
)

func TestPostgresIntegrationQueryExplain(t *testing.T) {
	dsn := os.Getenv("DBGOV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DBGOV_TEST_POSTGRES_DSN is not set")
	}
	database := os.Getenv("DBGOV_TEST_POSTGRES_DATABASE")
	if database == "" {
		database = "postgres"
	}
	backend, err := postgres.New(dsn, database)
	if err != nil {
		t.Fatalf("postgres.New() error = %v", err)
	}
	if err := backend.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if _, err := backend.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	result, err := backend.Explain(context.Background(), "SELECT * FROM generate_series(1, 5)")
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if result.EstimatedRows == 0 {
		t.Fatalf("EstimatedRows = 0, want non-zero")
	}
}
