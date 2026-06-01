package mysql

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"

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
	return &Backend{db: db, database: database}, nil
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
