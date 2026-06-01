package fake

import (
	"context"

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
