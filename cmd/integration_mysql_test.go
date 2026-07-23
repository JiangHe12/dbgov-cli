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
	"github.com/JiangHe12/opskit-core/v2/apperrors"
)

func TestMySQLIntegrationQueryExplain(t *testing.T) {
	dsn := requiredDBIntegrationEnv(t, "DBGOV_TEST_MYSQL_DSN")
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
	dsn := requiredDBIntegrationEnv(t, "DBGOV_TEST_MYSQL_DSN")
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
	_, err = schema.ParseDesiredSQL(ddl)
	if err == nil || apperrors.AsAppError(err).Code != apperrors.CodeNotImplemented {
		t.Fatalf("ParseDesiredSQL(TableDDL) error = %v, want fail-closed rejection for PK/FK/default/index definitions\n%s", err, ddl)
	}
	roundTrip, err := schema.ParseSchemaDDL(ddl)
	if err != nil || !roundTrip.Tables[childTable].Opaque {
		t.Fatalf("ParseSchemaDDL(TableDDL) = %+v, %v, want opaque lossless definition", roundTrip, err)
	}
	createDiff := schema.Diff(schema.Schema{Tables: map[string]schema.Table{}}, roundTrip)
	if len(createDiff.Changes) != 1 || !createDiff.Changes[0].Opaque || createDiff.Changes[0].RawDDL != ddl {
		t.Fatalf("TableDDL create round-trip diff = %+v", createDiff)
	}
	opaqueStatements, err := backend.RenderDDL(createDiff.Changes)
	if err != nil || len(opaqueStatements) != 1 || opaqueStatements[0] != ddl {
		t.Fatalf("RenderDDL(opaque round-trip) = %+v, %v; want exact captured DDL", opaqueStatements, err)
	}
	if _, err := backend.ExecDDL(ctx, []string{"DROP TABLE `" + childTable + "`;"}); err != nil {
		t.Fatalf("drop opaque round-trip fixture error = %v", err)
	}
	if _, err := backend.ExecDDL(ctx, opaqueStatements); err != nil {
		t.Fatalf("execute opaque round-trip DDL error = %v", err)
	}
	recreatedDDL, err := backend.TableDDL(ctx, childTable)
	if err != nil || !schema.SameOpaqueDDL(ddl, recreatedDDL) {
		t.Fatalf("recreated TableDDL = %q, %v; want captured definition %q", recreatedDDL, err, ddl)
	}
	unsignedDDL, err := backend.TableDDL(ctx, unsignedTable)
	if err != nil {
		t.Fatalf("unsigned TableDDL() error = %v", err)
	}
	if _, err := schema.ParseDesiredSQL(unsignedDDL); err == nil ||
		apperrors.AsAppError(err).Code != apperrors.CodeNotImplemented {
		t.Fatalf("ParseDesiredSQL(unsigned TableDDL) error = %v, want fail-closed rejection for primary key\n%s", err, unsignedDDL)
	}
	supportedSQL := "CREATE TABLE `" + unsignedTable + "` (`id` int, `flags` int unsigned);"
	supportedParsed, err := schema.ParseDesiredSQL(supportedSQL)
	if err != nil {
		t.Fatalf("ParseDesiredSQL(supported desired schema) error = %v\n%s", err, supportedSQL)
	}
	supportedExpected := schema.Schema{Tables: map[string]schema.Table{
		unsignedTable: {
			Name: unsignedTable,
			Columns: []schema.Column{
				{Name: "id", Type: "int"},
				{Name: "flags", Type: "int unsigned"},
			},
		},
	}}
	if diff := schema.Diff(supportedExpected, supportedParsed); len(diff.Changes) != 0 {
		t.Fatalf("supported desired schema diff = %+v, want none\nDDL:\n%s", diff.Changes, supportedSQL)
	}

	statements, err := backend.RenderDDL([]schema.Change{
		{Action: schema.ActionAddColumn, Table: parentTable, Column: "note", Type: "varchar(200)"},
	})
	if err != nil {
		t.Fatalf("RenderDDL() error = %v", err)
	}
	if len(statements) != 1 || statements[0] != "ALTER TABLE `"+parentTable+"` ADD COLUMN `note` varchar(200);" {
		t.Fatalf("RenderDDL statements = %+v", statements)
	}
	_, err = backend.RenderDDL([]schema.Change{
		{Action: schema.ActionModifyColumn, Table: parentTable, Column: "name", Type: "varchar(200)"},
	})
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("RenderDDL(modify column) error = %v, want fail-closed NOT_IMPLEMENTED", err)
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
	_, err = backend.RenderDDL(schema.Diff(
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
	if got := apperrors.AsAppError(err).Code; got != apperrors.CodeNotImplemented {
		t.Fatalf("RenderDDL(modify autoincrement) error = %v, want fail-closed NOT_IMPLEMENTED", err)
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
