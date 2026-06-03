package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCreateTablesMinimal(t *testing.T) {
	got, err := ParseDesiredSQL(`
CREATE TABLE users (
  id BIGINT,
  name VARCHAR(100),
  created_at DATETIME
);
`)
	if err != nil {
		t.Fatalf("ParseDesiredSQL() error = %v", err)
	}
	users, ok := got.Tables["users"]
	if !ok {
		t.Fatalf("users table missing: %+v", got)
	}
	if len(users.Columns) != 3 || users.Columns[1].Name != "name" || users.Columns[1].Type != "VARCHAR(100)" {
		t.Fatalf("columns = %+v", users.Columns)
	}
}

func TestParseCreateTablesRejectsUnsupportedDDL(t *testing.T) {
	_, err := ParseDesiredSQL(`ALTER TABLE users ADD COLUMN age INT;`)
	if err == nil {
		t.Fatal("ParseDesiredSQL() error = nil, want unsupported DDL error")
	}
}

func TestDiffSchemaFindsAddAndDropColumns(t *testing.T) {
	current := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "legacy", Type: "TEXT"}}},
	}}
	desired := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "name", Type: "VARCHAR(100)"}}},
	}}

	diff := Diff(current, desired)
	if len(diff.Changes) != 2 {
		t.Fatalf("changes = %+v, want 2", diff.Changes)
	}
	if diff.Changes[0].Action != ActionAddColumn || diff.Changes[0].Column != "name" || diff.Changes[0].Destructive {
		t.Fatalf("add change = %+v", diff.Changes[0])
	}
	if diff.Changes[1].Action != ActionDropColumn || diff.Changes[1].Column != "legacy" || !diff.Changes[1].Destructive {
		t.Fatalf("drop change = %+v", diff.Changes[1])
	}
	if !diff.Destructive {
		t.Fatal("DiffResult.Destructive = false, want true")
	}
}

func TestDiffSchemaFindsModifyColumnAndCreateTable(t *testing.T) {
	current := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "name", Type: "VARCHAR(100)"}}},
	}}
	desired := Schema{Tables: map[string]Table{
		"users":  {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "name", Type: "TEXT"}}},
		"orders": {Name: "orders", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "user_id", Type: "BIGINT"}}},
	}}

	diff := Diff(current, desired)
	if len(diff.Changes) != 2 {
		t.Fatalf("changes = %+v, want 2", diff.Changes)
	}
	if diff.Changes[0].Action != ActionCreateTable || diff.Changes[0].Table != "orders" || len(diff.Changes[0].Columns) != 2 {
		t.Fatalf("create table change = %+v", diff.Changes[0])
	}
	if diff.Changes[1].Action != ActionModifyColumn || diff.Changes[1].Column != "name" || diff.Changes[1].Type != "TEXT" || !diff.Changes[1].Destructive {
		t.Fatalf("modify column change = %+v", diff.Changes[1])
	}
	if !diff.Destructive {
		t.Fatal("DiffResult.Destructive = false, want true")
	}
}

func TestDiffSchemaWarnsAboutPossibleRename(t *testing.T) {
	current := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "legacy", Type: "TEXT"}}},
	}}
	desired := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}, {Name: "display_name", Type: "TEXT"}}},
	}}

	diff := Diff(current, desired)
	if len(diff.Warnings) != 1 || diff.Warnings[0] == "" {
		t.Fatalf("warnings = %+v, want possible rename warning", diff.Warnings)
	}
	if diff.Changes[1].Action != ActionDropColumn || !diff.Changes[1].Destructive {
		t.Fatalf("drop side of possible rename = %+v", diff.Changes[1])
	}
}

func TestDiffDoesNotDropTablesMissingFromDesired(t *testing.T) {
	current := Schema{Tables: map[string]Table{
		"users":  {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
		"orders": {Name: "orders", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
	}}
	desired := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
	}}

	diff := Diff(current, desired)
	for _, change := range diff.Changes {
		if change.Action == ActionDropTable {
			t.Fatalf("Diff produced DROP_TABLE for missing desired table: %+v", diff.Changes)
		}
	}
}

func TestExtraTablesAndPruneChanges(t *testing.T) {
	current := Schema{Tables: map[string]Table{
		"users":  {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
		"orders": {Name: "orders", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
		"logs":   {Name: "logs", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
	}}
	desired := Schema{Tables: map[string]Table{
		"users": {Name: "users", Columns: []Column{{Name: "id", Type: "BIGINT"}}},
	}}

	extra := ExtraTables(current, desired)
	if len(extra) != 2 || extra[0] != "logs" || extra[1] != "orders" {
		t.Fatalf("ExtraTables() = %+v, want sorted logs/orders", extra)
	}
	prune := PruneChanges(current, desired)
	if len(prune) != 2 {
		t.Fatalf("PruneChanges() = %+v, want 2", prune)
	}
	for i, name := range extra {
		if prune[i].Action != ActionDropTable || prune[i].Table != name || !prune[i].Destructive {
			t.Fatalf("prune[%d] = %+v, want DROP_TABLE %s destructive", i, prune[i], name)
		}
	}
}

func TestLoadDesiredDirMergesSQLFiles(t *testing.T) {
	dir := t.TempDir()
	writeSchemaTestFile(t, dir, "users.sql", "CREATE TABLE users (id BIGINT);")
	writeSchemaTestFile(t, dir, "orders.sql", "CREATE TABLE orders (id BIGINT, user_id BIGINT);")
	writeSchemaTestFile(t, dir, "notes.txt", "ignored")

	got, err := LoadDesiredDir(dir)
	if err != nil {
		t.Fatalf("LoadDesiredDir() error = %v", err)
	}
	if len(got.Tables) != 2 || got.Tables["users"].Name != "users" || got.Tables["orders"].Name != "orders" {
		t.Fatalf("schema = %+v", got)
	}
}

func TestLoadDesiredDirRejectsDuplicateTablesAndEmptyDirs(t *testing.T) {
	dup := t.TempDir()
	writeSchemaTestFile(t, dup, "users.sql", "CREATE TABLE users (id BIGINT);")
	writeSchemaTestFile(t, dup, "also_users.sql", "CREATE TABLE users (name TEXT);")
	if _, err := LoadDesiredDir(dup); err == nil {
		t.Fatal("expected duplicate table error")
	}

	empty := t.TempDir()
	if _, err := LoadDesiredDir(empty); err == nil {
		t.Fatal("expected empty directory error")
	}
}

func TestSchemaFromDDLMapMergesTablesAndRejectsInvalidInput(t *testing.T) {
	got, err := SchemaFromDDLMap(map[string]string{
		"users":  "CREATE TABLE users (id BIGINT);",
		"orders": "CREATE TABLE orders (id BIGINT, user_id BIGINT);",
	})
	if err != nil {
		t.Fatalf("SchemaFromDDLMap() error = %v", err)
	}
	if len(got.Tables) != 2 || got.Tables["orders"].Columns[1].Name != "user_id" {
		t.Fatalf("schema = %+v", got)
	}
	if _, err := SchemaFromDDLMap(map[string]string{"bad": "ALTER TABLE users ADD COLUMN name TEXT;"}); err == nil {
		t.Fatal("expected invalid DDL to be rejected")
	}
	if _, err := SchemaFromDDLMap(map[string]string{
		"a": "CREATE TABLE users (id BIGINT);",
		"b": "CREATE TABLE users (name TEXT);",
	}); err == nil {
		t.Fatal("expected duplicate table to be rejected")
	}
}

func TestClassifyDiffRisk(t *testing.T) {
	add := Change{Action: ActionAddColumn, Table: "users", Column: "name", Type: "VARCHAR(100)"}
	if risk, destructive := ClassifyChange(add); risk != RiskR1 || destructive {
		t.Fatalf("ClassifyChange(add) = %s/%t", risk, destructive)
	}
	modify := Change{Action: ActionModifyColumn, Table: "users", Column: "name", Type: "TEXT", Destructive: true}
	if risk, destructive := ClassifyChange(modify); risk != RiskR3 || !destructive {
		t.Fatalf("ClassifyChange(modify) = %s/%t", risk, destructive)
	}
	diff := DiffResult{Changes: []Change{add, modify}, Destructive: true}
	if classified := ClassifyDiff(diff); classified.OverallRisk != RiskR3 || !classified.Destructive {
		t.Fatalf("ClassifyDiff() = %+v", classified)
	}
}

func writeSchemaTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaModelCarriesIntrospectionDetails(t *testing.T) {
	def := "CURRENT_TIMESTAMP"
	table := Table{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: "BIGINT", Nullable: false, Key: "PRI"},
			{Name: "created_at", Type: "DATETIME", Nullable: true, Default: &def},
		},
		Indexes:     []Index{{Name: "idx_users_created_at", Columns: []string{"created_at"}, Unique: false}},
		ForeignKeys: []ForeignKey{{Name: "fk_users_org", Columns: []string{"org_id"}, RefTable: "orgs", RefColumns: []string{"id"}}},
	}
	if table.Columns[1].Default == nil || *table.Columns[1].Default != def {
		t.Fatalf("default not carried: %+v", table.Columns[1])
	}
	if table.Indexes[0].Columns[0] != "created_at" || table.ForeignKeys[0].RefTable != "orgs" {
		t.Fatalf("introspection details not carried: %+v", table)
	}
}
