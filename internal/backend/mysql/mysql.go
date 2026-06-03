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
SELECT table_name, column_name, column_type, is_nullable, column_default, column_key
FROM information_schema.columns
WHERE table_schema = ?
ORDER BY table_name, ordinal_position`, b.database)
	if err != nil {
		return schema.Schema{}, err
	}
	defer func() { _ = rows.Close() }()

	result := schema.Schema{Tables: map[string]schema.Table{}}
	for rows.Next() {
		var tableName, columnName, columnType, nullable, columnKey string
		var defaultValue sql.NullString
		if err := rows.Scan(&tableName, &columnName, &columnType, &nullable, &defaultValue, &columnKey); err != nil {
			return schema.Schema{}, err
		}
		table := result.Tables[tableName]
		if table.Name == "" {
			table.Name = tableName
		}
		column := schema.Column{Name: columnName, Type: columnType, Nullable: strings.EqualFold(nullable, "YES"), Key: columnKey}
		if defaultValue.Valid {
			value := defaultValue.String
			column.Default = &value
		}
		table.Columns = append(table.Columns, column)
		result.Tables[tableName] = table
	}
	if err := rows.Err(); err != nil {
		return schema.Schema{}, err
	}
	if len(result.Tables) == 0 {
		return result, fmt.Errorf("no tables found in database %q", b.database)
	}
	if err := b.loadIndexes(ctx, &result); err != nil {
		return schema.Schema{}, err
	}
	if err := b.loadForeignKeys(ctx, &result); err != nil {
		return schema.Schema{}, err
	}
	return result, nil
}

func (b *Backend) loadIndexes(ctx context.Context, result *schema.Schema) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT table_name, index_name, column_name, non_unique, seq_in_index
FROM information_schema.statistics
WHERE table_schema = ?
ORDER BY table_name, index_name, seq_in_index`, b.database)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type key struct{ table, name string }
	indexes := map[key]*schema.Index{}
	order := []key{}
	for rows.Next() {
		var tableName, indexName, columnName string
		var nonUnique int
		var seq int
		if err := rows.Scan(&tableName, &indexName, &columnName, &nonUnique, &seq); err != nil {
			return err
		}
		k := key{table: tableName, name: indexName}
		idx, ok := indexes[k]
		if !ok {
			indexes[k] = &schema.Index{Name: indexName, Unique: nonUnique == 0}
			idx = indexes[k]
			order = append(order, k)
		}
		idx.Columns = append(idx.Columns, columnName)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range order {
		table := result.Tables[k.table]
		if table.Name == "" {
			continue
		}
		table.Indexes = append(table.Indexes, *indexes[k])
		result.Tables[k.table] = table
	}
	return nil
}

func (b *Backend) loadForeignKeys(ctx context.Context, result *schema.Schema) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT kcu.table_name, kcu.constraint_name, kcu.column_name, kcu.referenced_table_name, kcu.referenced_column_name, kcu.ordinal_position
FROM information_schema.key_column_usage kcu
JOIN information_schema.referential_constraints rc
  ON rc.constraint_schema = kcu.constraint_schema
 AND rc.constraint_name = kcu.constraint_name
WHERE kcu.table_schema = ?
  AND kcu.referenced_table_name IS NOT NULL
ORDER BY kcu.table_name, kcu.constraint_name, kcu.ordinal_position`, b.database)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type key struct{ table, name string }
	foreignKeys := map[key]*schema.ForeignKey{}
	order := []key{}
	for rows.Next() {
		var tableName, constraintName, columnName, refTable, refColumn string
		var ordinal int
		if err := rows.Scan(&tableName, &constraintName, &columnName, &refTable, &refColumn, &ordinal); err != nil {
			return err
		}
		k := key{table: tableName, name: constraintName}
		fk, ok := foreignKeys[k]
		if !ok {
			foreignKeys[k] = &schema.ForeignKey{Name: constraintName, RefTable: refTable}
			fk = foreignKeys[k]
			order = append(order, k)
		}
		fk.Columns = append(fk.Columns, columnName)
		fk.RefColumns = append(fk.RefColumns, refColumn)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range order {
		table := result.Tables[k.table]
		if table.Name == "" {
			continue
		}
		table.ForeignKeys = append(table.ForeignKeys, *foreignKeys[k])
		result.Tables[k.table] = table
	}
	return nil
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

func (b *Backend) TableDDL(ctx context.Context, table string) (string, error) {
	rows, err := b.db.QueryContext(ctx, "SHOW CREATE TABLE `"+escapeIdent(table)+"`")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", fmt.Errorf("table %q not found", table)
	}
	var tableName, ddl string
	if err := rows.Scan(&tableName, &ddl); err != nil {
		return "", err
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return ddl, nil
}

func (b *Backend) RenderDDL(changes []schema.Change) ([]string, error) {
	statements := make([]string, 0, len(changes))
	for _, change := range changes {
		switch change.Action {
		case schema.ActionCreateTable:
			columns := make([]string, 0, len(change.Columns))
			for _, column := range change.Columns {
				columns = append(columns, renderColumn(column))
			}
			if len(columns) == 0 {
				return nil, fmt.Errorf("CREATE_TABLE %s has no columns", change.Table)
			}
			statements = append(statements, fmt.Sprintf("CREATE TABLE `%s` (%s);", escapeIdent(change.Table), strings.Join(columns, ", ")))
		case schema.ActionAddColumn:
			statements = append(statements, fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` %s;", escapeIdent(change.Table), escapeIdent(change.Column), change.Type))
		case schema.ActionModifyColumn:
			statements = append(statements, fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN `%s` %s;", escapeIdent(change.Table), escapeIdent(change.Column), change.Type))
		case schema.ActionDropColumn:
			statements = append(statements, fmt.Sprintf("ALTER TABLE `%s` DROP COLUMN `%s`;", escapeIdent(change.Table), escapeIdent(change.Column)))
		case schema.ActionDropTable:
			statements = append(statements, fmt.Sprintf("DROP TABLE `%s`;", escapeIdent(change.Table)))
		default:
			return nil, fmt.Errorf("unsupported schema change action %s", change.Action)
		}
	}
	return statements, nil
}

func (b *Backend) ExecDDL(ctx context.Context, statements []string) (int, error) {
	for i, statement := range statements {
		if _, err := b.db.ExecContext(ctx, statement); err != nil {
			return i, fmt.Errorf("execute DDL statement %d: %w", i+1, err)
		}
	}
	return len(statements), nil
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

func escapeIdent(value string) string {
	return strings.ReplaceAll(value, "`", "``")
}

func renderColumn(column schema.Column) string {
	return fmt.Sprintf("`%s` %s", escapeIdent(column.Name), column.Type)
}
