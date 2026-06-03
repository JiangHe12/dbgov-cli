package fake

import (
	"context"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend struct {
	Schema schema.Schema
}

func New() *Backend {
	return &Backend{Schema: schema.Schema{Tables: map[string]schema.Table{
		"users": {
			Name: "users",
			Columns: []schema.Column{
				{Name: "id", Type: "BIGINT"},
				{Name: "legacy", Type: "TEXT"},
			},
			Indexes:     []schema.Index{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
			ForeignKeys: []schema.ForeignKey{{Name: "fk_users_org", Columns: []string{"org_id"}, RefTable: "orgs", RefColumns: []string{"id"}}},
		},
	}}}
}

func (b *Backend) Ping(context.Context) error { return nil }

func (b *Backend) IntrospectSchema(context.Context) (schema.Schema, error) {
	return b.Schema, nil
}

func (b *Backend) Query(context.Context, string) (dbbackend.QueryResult, error) {
	return dbbackend.QueryResult{
		Columns: []string{"id", "name"},
		Rows:    [][]string{{"1", "alice"}, {"2", "bob"}},
	}, nil
}

func (b *Backend) Explain(context.Context, string) (dbbackend.ExplainResult, error) {
	return dbbackend.ExplainResult{
		Columns:       []string{"id", "select_type", "table", "rows"},
		Rows:          [][]string{{"1", "SIMPLE", "users", "2"}},
		EstimatedRows: 2,
	}, nil
}

func (b *Backend) TableDDL(context.Context, string) (string, error) {
	return "CREATE TABLE `users` (`id` BIGINT, `legacy` TEXT);", nil
}

func (b *Backend) RenderDDL(changes []schema.Change) ([]string, error) {
	statements := make([]string, 0, len(changes))
	for _, change := range changes {
		switch change.Action {
		case schema.ActionCreateTable:
			statements = append(statements, "CREATE TABLE `"+change.Table+"` (`id` BIGINT);")
		case schema.ActionAddColumn:
			statements = append(statements, "ALTER TABLE `"+change.Table+"` ADD COLUMN `"+change.Column+"` "+change.Type+";")
		case schema.ActionModifyColumn:
			statements = append(statements, "ALTER TABLE `"+change.Table+"` MODIFY COLUMN `"+change.Column+"` "+change.Type+";")
		case schema.ActionDropColumn:
			statements = append(statements, "ALTER TABLE `"+change.Table+"` DROP COLUMN `"+change.Column+"`;")
		case schema.ActionDropTable:
			statements = append(statements, "DROP TABLE `"+change.Table+"`;")
		}
	}
	return statements, nil
}
