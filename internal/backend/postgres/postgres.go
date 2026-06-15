package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx database/sql driver.

	"github.com/JiangHe12/opskit-core/apperrors"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend struct {
	db       *sql.DB
	database string
}

func New(dsn, database string) (*Backend, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return NewWithDB(db, database), nil
}

func NewWithDB(db *sql.DB, database string) *Backend {
	return &Backend{db: db, database: database}
}

func (b *Backend) Ping(ctx context.Context) error {
	return b.db.PingContext(ctx)
}

func (b *Backend) IntrospectSchema(context.Context) (schema.Schema, error) {
	return schema.Schema{}, notImplemented("schema introspection")
}

func (b *Backend) Query(ctx context.Context, sqlText string) (dbbackend.QueryResult, error) {
	rows, err := b.db.QueryContext(ctx, sqlText)
	if err != nil {
		return dbbackend.QueryResult{}, err
	}
	defer func() { _ = rows.Close() }()
	return scanRows(rows)
}

func (b *Backend) Explain(ctx context.Context, sqlText string) (dbbackend.ExplainResult, error) {
	explainSQL := "EXPLAIN (FORMAT JSON) " + strings.TrimSpace(sqlText) //nolint:gosec // Adds EXPLAIN to an already classified statement.
	rows, err := b.db.QueryContext(ctx, explainSQL)
	if err != nil {
		return dbbackend.ExplainResult{}, err
	}
	defer func() { _ = rows.Close() }()
	result, err := scanRows(rows)
	if err != nil {
		return dbbackend.ExplainResult{}, err
	}
	return dbbackend.ExplainResult{
		Columns:       result.Columns,
		Rows:          result.Rows,
		EstimatedRows: estimateRows(result),
	}, nil
}

func (b *Backend) TableDDL(context.Context, string) (string, error) {
	return "", notImplemented("table ddl")
}

func (b *Backend) RenderDDL([]schema.Change) ([]string, error) {
	return nil, notImplemented("render ddl")
}

func (b *Backend) ExecDDL(context.Context, []string) (int, error) {
	return 0, notImplemented("exec ddl")
}

func (b *Backend) ExecDML(context.Context, string) (int64, error) {
	return 0, notImplemented("exec dml")
}

func notImplemented(op string) error {
	return apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("postgres %s not supported until a later phase", op), nil)
}

func scanRows(rows *sql.Rows) (dbbackend.QueryResult, error) {
	columns, err := rows.Columns()
	if err != nil {
		return dbbackend.QueryResult{}, err
	}
	result := dbbackend.QueryResult{Columns: columns}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return dbbackend.QueryResult{}, err
		}
		row := make([]string, len(columns))
		for i, value := range values {
			row[i] = valueString(value)
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return dbbackend.QueryResult{}, err
	}
	return result, nil
}

func valueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func estimateRows(result dbbackend.QueryResult) int64 {
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return 0
	}
	rows, _ := planRowsFromExplainJSON(result.Rows[0][0])
	return rows
}

func planRowsFromExplainJSON(planJSON string) (int64, error) {
	var plans []struct {
		Plan struct {
			PlanRows float64 `json:"Plan Rows"`
		} `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(planJSON), &plans); err != nil {
		return 0, err
	}
	if len(plans) == 0 {
		return 0, nil
	}
	return int64(plans[0].Plan.PlanRows), nil
}
