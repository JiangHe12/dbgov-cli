//go:build integration

package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JiangHe12/dbgov-cli/internal/backend/postgres"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
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

func TestPostgresIntegrationSchema(t *testing.T) {
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
	ctx := context.Background()
	_, _ = backend.ExecDDL(ctx, []string{`DROP TABLE IF EXISTS "dbgov_pg_child";`, `DROP TABLE IF EXISTS "dbgov_pg_serial";`, `DROP TABLE IF EXISTS "dbgov_pg_shared_seq";`, `DROP TABLE IF EXISTS "dbgov_pg_parent";`, `DROP SEQUENCE IF EXISTS "dbgov_pg_shared_sequence";`})
	t.Cleanup(func() {
		_, _ = backend.ExecDDL(context.Background(), []string{`DROP TABLE IF EXISTS "dbgov_pg_child";`, `DROP TABLE IF EXISTS "dbgov_pg_serial";`, `DROP TABLE IF EXISTS "dbgov_pg_shared_seq";`, `DROP TABLE IF EXISTS "dbgov_pg_parent";`, `DROP SEQUENCE IF EXISTS "dbgov_pg_shared_sequence";`})
	})
	_, err = backend.ExecDDL(ctx, []string{
		`CREATE SEQUENCE "dbgov_pg_shared_sequence";`,
		`CREATE TABLE "dbgov_pg_parent" ("id" integer GENERATED ALWAYS AS IDENTITY, "name" text DEFAULT 'n', CONSTRAINT "dbgov_pg_parent_pkey" PRIMARY KEY ("id"));`,
		`CREATE TABLE "dbgov_pg_child" ("id" integer NOT NULL, "parent_id" integer NOT NULL, CONSTRAINT "dbgov_pg_child_pkey" PRIMARY KEY ("id"), CONSTRAINT "dbgov_pg_child_parent_fkey" FOREIGN KEY ("parent_id") REFERENCES "dbgov_pg_parent" ("id"));`,
		`CREATE TABLE "dbgov_pg_serial" ("id" serial PRIMARY KEY, "name" character varying(255));`,
		`CREATE TABLE "dbgov_pg_shared_seq" ("id" integer DEFAULT nextval('"dbgov_pg_shared_sequence"'::regclass), "name" text);`,
		`CREATE INDEX "dbgov_pg_child_parent_idx" ON "dbgov_pg_child" ("parent_id");`,
	})
	if err != nil {
		t.Fatalf("create schema fixtures error = %v", err)
	}

	current, err := backend.IntrospectSchema(ctx)
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	parent := current.Tables["dbgov_pg_parent"]
	if parent.Columns[0].Key != "PRI" || !parent.Columns[0].AutoIncrement || parent.Columns[1].Default == nil {
		t.Fatalf("parent table = %+v", parent)
	}
	serialTable := current.Tables["dbgov_pg_serial"]
	if len(serialTable.Columns) < 2 || !serialTable.Columns[0].AutoIncrement || serialTable.Columns[0].Default != nil {
		t.Fatalf("serial table = %+v", serialTable)
	}
	sharedSeqTable := current.Tables["dbgov_pg_shared_seq"]
	if len(sharedSeqTable.Columns) < 2 || sharedSeqTable.Columns[0].AutoIncrement || sharedSeqTable.Columns[0].Default == nil {
		t.Fatalf("shared sequence table = %+v, want regular nextval default", sharedSeqTable)
	}
	child := current.Tables["dbgov_pg_child"]
	if len(child.Indexes) == 0 || len(child.ForeignKeys) != 1 {
		t.Fatalf("child table = %+v", child)
	}
	ddl, err := backend.TableDDL(ctx, "dbgov_pg_child")
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	if !strings.Contains(ddl, `CREATE TABLE "dbgov_pg_child"`) || !strings.Contains(ddl, `FOREIGN KEY ("parent_id") REFERENCES "dbgov_pg_parent" ("id")`) {
		t.Fatalf("unexpected TableDDL:\n%s", ddl)
	}
	serialDDL, err := backend.TableDDL(ctx, "dbgov_pg_serial")
	if err != nil {
		t.Fatalf("serial TableDDL() error = %v", err)
	}
	parsed, err := schema.ParseDesiredSQL(serialDDL)
	if err != nil {
		t.Fatalf("ParseDesiredSQL(serial TableDDL) error = %v\n%s", err, serialDDL)
	}
	expected := schema.Schema{Tables: map[string]schema.Table{
		"dbgov_pg_serial": {Name: "dbgov_pg_serial", Columns: []schema.Column{{Name: "id", Type: "integer", AutoIncrement: true}, {Name: "name", Type: "character varying(255)"}}},
	}}
	if diff := schema.Diff(expected, parsed); len(diff.Changes) != 0 {
		t.Fatalf("serial round-trip diff = %+v, want none\nDDL:\n%s", diff.Changes, serialDDL)
	}
	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionAddColumn, Table: "dbgov_pg_parent", Column: "note", Type: "text"},
		{Action: schema.ActionModifyColumn, Table: "dbgov_pg_parent", Column: "name", Type: "character varying(200)", TypeChanged: true},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	if statements[0] != `ALTER TABLE "dbgov_pg_parent" ADD COLUMN "note" text;` ||
		statements[1] != `ALTER TABLE "dbgov_pg_parent" ALTER COLUMN "name" TYPE character varying(200);` {
		t.Fatalf("RenderDDL statements = %+v", statements)
	}
	affected, err := backend.ExecDML(ctx, `UPDATE "dbgov_pg_parent" SET "name" = 'changed' WHERE "id" = 1`)
	if err != nil {
		t.Fatalf("ExecDML() error = %v", err)
	}
	if affected != 0 {
		t.Fatalf("ExecDML affected = %d, want 0 for empty fixture", affected)
	}
}
