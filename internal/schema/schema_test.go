package schema

import "testing"

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
