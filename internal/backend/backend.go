package backend

import (
	"context"

	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend interface {
	Ping(ctx context.Context) error
	IntrospectSchema(ctx context.Context) (schema.Schema, error)
	Query(ctx context.Context, sql string) (QueryResult, error)
	Explain(ctx context.Context, sql string) (ExplainResult, error)
	TableDDL(ctx context.Context, table string) (string, error)
	RenderDDL(changes []schema.Change) ([]string, error)
	ExecDDL(ctx context.Context, statements []string) (executed int, err error)
	ExecDML(ctx context.Context, sql string) (affected int64, err error)
}

type QueryResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

type ExplainResult struct {
	Columns       []string   `json:"columns"`
	Rows          [][]string `json:"rows"`
	EstimatedRows int64      `json:"estimatedRows"`
}
