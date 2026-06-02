package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend struct {
	db       *sql.DB
	database string
}

func New(dsn, database string) (*Backend, error) {
	db, err := sql.Open("mysql", dsn)
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

func (b *Backend) IntrospectSchema(ctx context.Context) (schema.Schema, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT table_name, column_name, column_type
FROM information_schema.columns
WHERE table_schema = ?
ORDER BY table_name, ordinal_position`, b.database)
	if err != nil {
		return schema.Schema{}, err
	}
	defer func() { _ = rows.Close() }()

	result := schema.Schema{Tables: map[string]schema.Table{}}
	for rows.Next() {
		var tableName, columnName, columnType string
		if err := rows.Scan(&tableName, &columnName, &columnType); err != nil {
			return schema.Schema{}, err
		}
		table := result.Tables[tableName]
		if table.Name == "" {
			table.Name = tableName
		}
		table.Columns = append(table.Columns, schema.Column{Name: columnName, Type: columnType})
		result.Tables[tableName] = table
	}
	if err := rows.Err(); err != nil {
		return schema.Schema{}, err
	}
	if len(result.Tables) == 0 {
		return result, fmt.Errorf("no tables found in database %q", b.database)
	}
	return result, nil
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
	explainSQL := "EXPLAIN " + strings.TrimSpace(sqlText)
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
	rowsIndex := -1
	for i, column := range result.Columns {
		if strings.EqualFold(column, "rows") {
			rowsIndex = i
			break
		}
	}
	if rowsIndex < 0 {
		return 0
	}
	var total int64
	for _, row := range result.Rows {
		if rowsIndex >= len(row) {
			continue
		}
		value, err := strconv.ParseInt(row[rowsIndex], 10, 64)
		if err == nil {
			total += value
		}
	}
	return total
}
