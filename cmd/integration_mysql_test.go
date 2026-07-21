//go:build integration

package cmd

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JiangHe12/dbgov-cli/internal/backend/mysql"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

func TestMySQLIntegrationQueryExplain(t *testing.T) {
	dsn := os.Getenv("DBGOV_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DBGOV_TEST_MYSQL_DSN is not set")
	}
	database := os.Getenv("DBGOV_TEST_MYSQL_DATABASE")
	if database == "" {
		database = "dbgov_it"
	}
	backend, err := mysql.New(dsn, database)
	if err != nil {
		t.Fatalf("mysql.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("backend.Close() error = %v", err)
		}
	})
	ctx := context.Background()
	if err := backend.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if _, err := backend.Query(ctx, "SELECT 1"); err != nil {
		t.Fatalf("Query() error = %v", err)
	}

	table := mysqlIntegrationName("explain")
	dropMySQLTables(t, backend, table)
	t.Cleanup(func() { dropMySQLTables(t, backend, table) })
	if _, err := backend.ExecDDL(ctx, []string{
		"CREATE TABLE `" + table + "` (`id` int AUTO_INCREMENT, `name` varchar(64), PRIMARY KEY (`id`)) ENGINE=InnoDB;",
	}); err != nil {
		t.Fatalf("create explain fixture error = %v", err)
	}
	if _, err := backend.ExecDML(ctx, "INSERT INTO `"+table+"` (`name`) VALUES ('a'), ('b'), ('c')"); err != nil {
		t.Fatalf("insert explain fixture error = %v", err)
	}
	result, err := backend.Explain(ctx, "SELECT * FROM `"+table+"`")
	if err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	if result.EstimatedRows == 0 {
		t.Fatalf("EstimatedRows = 0, want non-zero; explain = %+v", result)
	}
}

func TestMySQLIntegrationSchema(t *testing.T) {
	dsn := os.Getenv("DBGOV_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DBGOV_TEST_MYSQL_DSN is not set")
	}
	database := os.Getenv("DBGOV_TEST_MYSQL_DATABASE")
	if database == "" {
		database = "dbgov_it"
	}
	backend, err := mysql.New(dsn, database)
	if err != nil {
		t.Fatalf("mysql.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("backend.Close() error = %v", err)
		}
	})
	ctx := context.Background()
	suffix := mysqlIntegrationName("schema")
	parentTable := suffix + "_parent"
	childTable := suffix + "_child"
	unsignedTable := suffix + "_unsigned"
	autoAddTable := suffix + "_ai_add"
	autoModifyTable := suffix + "_ai_modify"
	dropMySQLTables(t, backend, childTable, parentTable, unsignedTable, autoAddTable, autoModifyTable)
	t.Cleanup(func() {
		dropMySQLTables(t, backend, childTable, parentTable, unsignedTable, autoAddTable, autoModifyTable)
	})
	if _, err := backend.ExecDDL(ctx, []string{
		"CREATE TABLE `" + parentTable + "` (`id` int AUTO_INCREMENT, `name` varchar(100) NOT NULL DEFAULT 'n', PRIMARY KEY (`id`)) ENGINE=InnoDB;",
		"CREATE TABLE `" + childTable + "` (`id` int AUTO_INCREMENT, `parent_id` int NOT NULL, `note` varchar(64) DEFAULT 'c', PRIMARY KEY (`id`), KEY `idx_" + childTable + "_parent` (`parent_id`), CONSTRAINT `fk_" + childTable + "_parent` FOREIGN KEY (`parent_id`) REFERENCES `" + parentTable + "` (`id`)) ENGINE=InnoDB;",
		"CREATE TABLE `" + unsignedTable + "` (`id` int AUTO_INCREMENT, `flags` int unsigned, PRIMARY KEY (`id`)) ENGINE=InnoDB;",
		"CREATE TABLE `" + autoAddTable + "` (`id` int NOT NULL, `seq` int, PRIMARY KEY (`id`)) ENGINE=InnoDB;",
		"CREATE TABLE `" + autoModifyTable + "` (`id` int NOT NULL, `seq` int, PRIMARY KEY (`id`)) ENGINE=InnoDB;",
	}); err != nil {
		t.Fatalf("create schema fixtures error = %v", err)
	}
	if _, err := backend.ExecDML(ctx, "INSERT INTO `"+parentTable+"` (`name`) VALUES ('seed')"); err != nil {
		t.Fatalf("insert parent fixture error = %v", err)
	}

	current, err := backend.IntrospectSchema(ctx)
	if err != nil {
		t.Fatalf("IntrospectSchema() error = %v", err)
	}
	parent := current.Tables[parentTable]
	if len(parent.Columns) < 2 || parent.Columns[0].Key != "PRI" || !parent.Columns[0].AutoIncrement || parent.Columns[1].Default == nil {
		t.Fatalf("parent table = %+v", parent)
	}
	child := current.Tables[childTable]
	if len(child.Indexes) == 0 || len(child.ForeignKeys) != 1 {
		t.Fatalf("child table = %+v", child)
	}

	ddl, err := backend.TableDDL(ctx, childTable)
	if err != nil {
		t.Fatalf("TableDDL() error = %v", err)
	}
	if !strings.Contains(ddl, "CREATE TABLE `"+childTable+"`") ||
		!strings.Contains(ddl, "CONSTRAINT `fk_"+childTable+"_parent` FOREIGN KEY (`parent_id`) REFERENCES `"+parentTable+"` (`id`)") ||
		!strings.Contains(ddl, "ENGINE=InnoDB") {
		t.Fatalf("unexpected TableDDL:\n%s", ddl)
	}
	parsed, err := schema.ParseDesiredSQL(ddl)
	if err != nil {
		t.Fatalf("ParseDesiredSQL(TableDDL) error = %v\n%s", err, ddl)
	}
	expected := schema.Schema{Tables: map[string]schema.Table{
		childTable: {
			Name: childTable,
			Columns: []schema.Column{
				{Name: "id", Type: "int", AutoIncrement: true},
				{Name: "parent_id", Type: "int"},
				{Name: "note", Type: "varchar(64)"},
			},
		},
	}}
	if diff := schema.Diff(expected, parsed); len(diff.Changes) != 0 {
		t.Fatalf("TableDDL round-trip diff = %+v, want none\nDDL:\n%s", diff.Changes, ddl)
	}
	unsignedDDL, err := backend.TableDDL(ctx, unsignedTable)
	if err != nil {
		t.Fatalf("unsigned TableDDL() error = %v", err)
	}
	unsignedParsed, err := schema.ParseDesiredSQL(unsignedDDL)
	if err != nil {
		t.Fatalf("ParseDesiredSQL(unsigned TableDDL) error = %v\n%s", err, unsignedDDL)
	}
	unsignedExpected := schema.Schema{Tables: map[string]schema.Table{
		unsignedTable: {
			Name: unsignedTable,
			Columns: []schema.Column{
				{Name: "id", Type: "int", AutoIncrement: true},
				{Name: "flags", Type: "int unsigned"},
			},
		},
	}}
	if diff := schema.Diff(unsignedExpected, unsignedParsed); len(diff.Changes) != 0 {
		t.Fatalf("unsigned TableDDL round-trip diff = %+v, want none\nDDL:\n%s", diff.Changes, unsignedDDL)
	}

	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionAddColumn, Table: parentTable, Column: "note", Type: "varchar(200)"},
		{Action: schema.ActionModifyColumn, Table: parentTable, Column: "name", Type: "varchar(200)"},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	if statements[0] != "ALTER TABLE `"+parentTable+"` ADD COLUMN `note` varchar(200);" ||
		statements[1] != "ALTER TABLE `"+parentTable+"` MODIFY COLUMN `name` varchar(200);" {
		t.Fatalf("RenderDDL statements = %+v", statements)
	}
	autoAddStatements, err := backend.RenderDDL(schema.Diff(
		schema.Schema{Tables: map[string]schema.Table{
			autoAddTable: {
				Name:    autoAddTable,
				Columns: []schema.Column{{Name: "id", Type: "int", Key: "PRI"}, {Name: "seq", Type: "int"}},
				Indexes: []schema.Index{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
			},
		}},
		schema.Schema{Tables: map[string]schema.Table{
			autoAddTable: {
				Name: autoAddTable,
				Columns: []schema.Column{
					{Name: "id", Type: "int"},
					{Name: "seq", Type: "int"},
					{Name: "new_seq", Type: "int", AutoIncrement: true},
				},
			},
		}},
	).Changes)
	if err != nil {
		t.Fatalf("RenderDDL(add autoincrement) error = %v", err)
	}
	if _, err := backend.ExecDDL(ctx, autoAddStatements); err != nil {
		t.Fatalf("apply add autoincrement with existing primary key error = %v; statements=%+v", err, autoAddStatements)
	}
	autoModifyStatements, err := backend.RenderDDL(schema.Diff(
		schema.Schema{Tables: map[string]schema.Table{
			autoModifyTable: {
				Name:    autoModifyTable,
				Columns: []schema.Column{{Name: "id", Type: "int", Key: "PRI"}, {Name: "seq", Type: "int"}},
				Indexes: []schema.Index{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
			},
		}},
		schema.Schema{Tables: map[string]schema.Table{
			autoModifyTable: {
				Name:    autoModifyTable,
				Columns: []schema.Column{{Name: "id", Type: "int"}, {Name: "seq", Type: "int", AutoIncrement: true}},
			},
		}},
	).Changes)
	if err != nil {
		t.Fatalf("RenderDDL(modify autoincrement) error = %v", err)
	}
	if _, err := backend.ExecDDL(ctx, autoModifyStatements); err != nil {
		t.Fatalf("apply modify autoincrement with existing primary key error = %v; statements=%+v", err, autoModifyStatements)
	}

	affected, err := backend.ExecDML(ctx, "UPDATE `"+parentTable+"` SET `name` = 'changed' WHERE `name` = 'seed'")
	if err != nil {
		t.Fatalf("ExecDML() error = %v", err)
	}
	if affected != 1 {
		t.Fatalf("ExecDML affected = %d, want 1", affected)
	}
}

func mysqlIntegrationName(prefix string) string {
	return "dbgov_mysql_" + prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func dropMySQLTables(t *testing.T, backend interface {
	ExecDDL(context.Context, []string) (int, error)
}, tables ...string) {
	t.Helper()
	statements := make([]string, 0, len(tables))
	for _, table := range tables {
		statements = append(statements, "DROP TABLE IF EXISTS `"+table+"`;")
	}
	if _, err := backend.ExecDDL(context.Background(), statements); err != nil {
		t.Fatalf("drop MySQL fixtures error = %v", err)
	}
}
