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
