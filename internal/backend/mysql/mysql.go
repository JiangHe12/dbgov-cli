package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"

	_ "github.com/go-sql-driver/mysql" // Register MySQL database/sql driver.

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend struct {
	db       *sql.DB
	database string
}

const mysqlIdentifierMaxLen = 64

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

func (b *Backend) Close() error {
	return b.db.Close()
}

func (b *Backend) Ping(ctx context.Context) error {
	return b.db.PingContext(ctx)
}

func (b *Backend) IntrospectSchema(ctx context.Context) (schema.Schema, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT c.table_name, c.column_name, c.column_type, c.is_nullable, c.column_default, c.column_key, c.extra
FROM information_schema.columns c
JOIN information_schema.tables t
  ON t.table_schema = c.table_schema
 AND t.table_name = c.table_name
WHERE c.table_schema = ?
  AND t.table_type = 'BASE TABLE'
ORDER BY c.table_name, c.ordinal_position`, b.database)
	if err != nil {
		return schema.Schema{}, err
	}
	defer func() { _ = rows.Close() }()

	result := schema.Schema{Tables: map[string]schema.Table{}}
	for rows.Next() {
		var tableName, columnName, columnType, nullable, columnKey, extra string
		var defaultValue sql.NullString
		if err := rows.Scan(&tableName, &columnName, &columnType, &nullable, &defaultValue, &columnKey, &extra); err != nil {
			return schema.Schema{}, err
		}
		table := result.Tables[tableName]
		if table.Name == "" {
			table.Name = tableName
		}
		column := schema.Column{Name: columnName, Type: columnType, Nullable: strings.EqualFold(nullable, "YES"), Key: columnKey, AutoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment")}
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
	if err := rows.Close(); err != nil {
		return schema.Schema{}, err
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
	if err := rows.Close(); err != nil {
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
	if err := rows.Close(); err != nil {
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
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return dbbackend.QueryResult{}, backendErr("begin read-only query", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, sqlText)
	if err != nil {
		return dbbackend.QueryResult{}, backendErr("execute read query", err)
	}
	defer func() { _ = rows.Close() }()
	result, err := scanRows(rows)
	if err != nil {
		return dbbackend.QueryResult{}, backendErr("execute read query", err)
	}
	if err := rows.Close(); err != nil {
		return dbbackend.QueryResult{}, backendErr("close read query", err)
	}
	if err := tx.Rollback(); err != nil {
		return dbbackend.QueryResult{}, backendErr("rollback read-only query", err)
	}
	return result, nil
}

func (b *Backend) Explain(ctx context.Context, sqlText string) (dbbackend.ExplainResult, error) {
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return dbbackend.ExplainResult{}, backendErr("begin read-only explain", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := explainDML(ctx, tx, sqlText)
	if err != nil {
		return dbbackend.ExplainResult{}, backendErr("explain query", err)
	}
	if err := tx.Rollback(); err != nil {
		return dbbackend.ExplainResult{}, backendErr("rollback read-only explain", err)
	}
	return result, nil
}

func (b *Backend) TableDDL(ctx context.Context, table string) (string, error) {
	rows, err := b.db.QueryContext(ctx, "SHOW CREATE TABLE `"+escapeIdent(table)+"`")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", apperrors.New(apperrors.CodeResourceNotFound, fmt.Sprintf("table %q not found", table), nil)
	}
	var tableName, ddl string
	if err := rows.Scan(&tableName, &ddl); err != nil {
		return "", err
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if err := rows.Close(); err != nil {
		return "", err
	}
	statement, err := validateOpaqueMySQLDDL(table, ddl)
	if err != nil {
		return "", err
	}
	return statement, nil
}

func validateOpaqueMySQLDDL(table, ddl string) (string, error) {
	statement, err := schema.ValidatedOpaqueCreateDDL(table, ddl)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(statement, "CREATE TABLE `"+escapeIdent(table)+"`") {
		return "", apperrors.New(
			apperrors.CodeNotImplemented,
			fmt.Sprintf("opaque MySQL definition for table %q must use canonical SHOW CREATE TABLE syntax", table),
			nil,
		)
	}
	if engine, ok := schema.OpaqueTableEngine(statement); !ok || engine != "innodb" {
		return "", apperrors.New(
			apperrors.CodeNotImplemented,
			fmt.Sprintf("opaque MySQL definition for table %q must use the InnoDB storage engine", table),
			nil,
		)
	}
	return statement, nil
}

func (b *Backend) RenderDDL(changes []schema.Change) ([]string, error) {
	statements := make([]string, 0, len(changes))
	for _, change := range changes {
		switch change.Action {
		case schema.ActionCreateTable:
			if change.Opaque {
				statement, err := validateOpaqueMySQLDDL(change.Table, change.RawDDL)
				if err != nil {
					return nil, err
				}
				statements = append(statements, statement)
				continue
			}
			columns := make([]string, 0, len(change.Columns))
			autoIncrementKey := ""
			for _, column := range change.Columns {
				columns = append(columns, renderColumn(column))
				if column.AutoIncrement && autoIncrementKey == "" {
					autoIncrementKey = column.Name
				}
			}
			if len(columns) == 0 {
				return nil, apperrors.New(apperrors.CodeValidationFailed, fmt.Sprintf("CREATE_TABLE %s has no columns", change.Table), nil)
			}
			if autoIncrementKey != "" {
				columns = append(columns, fmt.Sprintf("PRIMARY KEY (`%s`)", escapeIdent(autoIncrementKey)))
			}
			statements = append(statements, fmt.Sprintf("CREATE TABLE `%s` (%s);", escapeIdent(change.Table), strings.Join(columns, ", ")))
		case schema.ActionAddColumn:
			column := schema.Column{Name: change.Column, Type: change.Type, AutoIncrement: change.AutoIncrement}
			statement := fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN %s", escapeIdent(change.Table), renderColumn(column))
			if change.AutoIncrementIndexRequired {
				statement += fmt.Sprintf(", ADD UNIQUE KEY `%s` (`%s`)", escapeIdent(autoIncrementIndexName(change.Table, change.Column)), escapeIdent(change.Column))
			}
			statements = append(statements, statement+";")
		case schema.ActionModifyColumn:
			return nil, apperrors.New(
				apperrors.CodeNotImplemented,
				fmt.Sprintf("in-place MySQL column modification for %s.%s cannot preserve the complete column definition; use a reviewed manual migration", change.Table, change.Column),
				nil,
			)
		case schema.ActionDropColumn:
			statements = append(statements, fmt.Sprintf("ALTER TABLE `%s` DROP COLUMN `%s`;", escapeIdent(change.Table), escapeIdent(change.Column)))
		case schema.ActionDropTable:
			statements = append(statements, fmt.Sprintf("DROP TABLE `%s`;", escapeIdent(change.Table)))
		default:
			return nil, apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("unsupported schema change action %s", change.Action), nil)
		}
	}
	return statements, nil
}

func (b *Backend) ExecDDL(ctx context.Context, statements []string) (int, error) {
	for i, statement := range statements {
		if _, err := b.db.ExecContext(ctx, statement); err != nil {
			return i, apperrors.New(apperrors.CodeBackendError, fmt.Sprintf("execute DDL statement %d", i+1), err)
		}
	}
	return len(statements), nil
}

func (b *Backend) ExecDML(ctx context.Context, sqlText string) (int64, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, backendErr("execute DML", err)
	}
	result, err := tx.ExecContext(ctx, sqlText)
	if err != nil {
		_ = tx.Rollback()
		return 0, backendErr("execute DML", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return 0, backendErr("execute DML", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, backendErr("execute DML", err)
	}
	return affected, nil
}

func (b *Backend) ExecDMLBound(ctx context.Context, sqlText string, binding dbbackend.DMLPlanBinding) (int64, error) {
	if binding.PlanFingerprint == "" || binding.EstimatedRows < 0 {
		return 0, apperrors.New(apperrors.CodeValidationFailed, "valid DML plan binding is required", nil)
	}
	tx, err := b.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, backendErr("execute bound DML", err)
	}
	current, err := explainDML(ctx, tx, sqlText)
	if err != nil {
		return rollbackDML(tx, backendErr("validate bound DML plan", err))
	}
	if current.PlanFingerprint != binding.PlanFingerprint ||
		current.EstimatedRows != binding.EstimatedRows {
		return rollbackDML(tx, apperrors.New(
			apperrors.CodeConflict,
			"DML plan changed after authorization; review and retry",
			nil,
		))
	}
	result, err := tx.ExecContext(ctx, sqlText)
	if err != nil {
		return rollbackDML(tx, backendErr("execute bound DML", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return rollbackDML(tx, backendErr("execute bound DML", err))
	}
	if err := tx.Commit(); err != nil {
		return affected, dbbackend.NewCommitIndeterminateError(backendErr("execute bound DML", err))
	}
	return affected, nil
}

type queryContext interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func explainDML(ctx context.Context, queryer queryContext, sqlText string) (dbbackend.ExplainResult, error) {
	explainSQL := "EXPLAIN " + strings.TrimSpace(sqlText) //nolint:gosec // Adds EXPLAIN to an already classified statement; no additional SQL surface is introduced.
	rows, err := queryer.QueryContext(ctx, explainSQL)
	if err != nil {
		return dbbackend.ExplainResult{}, err
	}
	defer func() { _ = rows.Close() }()
	result, err := scanRows(rows)
	if err != nil {
		return dbbackend.ExplainResult{}, err
	}
	if err := rows.Close(); err != nil {
		return dbbackend.ExplainResult{}, err
	}
	estimatedRows, err := estimateRows(result)
	if err != nil {
		return dbbackend.ExplainResult{}, err
	}
	return dbbackend.ExplainResult{
		Columns:         result.Columns,
		Rows:            result.Rows,
		Nulls:           result.Nulls,
		EstimatedRows:   estimatedRows,
		PlanFingerprint: dbbackend.PlanFingerprint(result),
	}, nil
}

func rollbackDML(tx *sql.Tx, operationErr error) (int64, error) {
	if err := tx.Rollback(); err != nil {
		return 0, backendErr("rollback DML", err)
	}
	return 0, operationErr
}

func backendErr(message string, err error) error {
	if err == nil {
		return nil
	}
	return apperrors.New(apperrors.CodeBackendError, message, err)
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
		nullRow := make([]bool, len(columns))
		for i, value := range values {
			nullRow[i] = value == nil
			row[i] = valueString(value)
		}
		result.Rows = append(result.Rows, row)
		result.Nulls = append(result.Nulls, nullRow)
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

func estimateRows(result dbbackend.QueryResult) (int64, error) {
	rowsIndex := -1
	for i, column := range result.Columns {
		if strings.EqualFold(column, "rows") {
			rowsIndex = i
			break
		}
	}
	if rowsIndex < 0 {
		return 0, fmt.Errorf("EXPLAIN result has no rows estimate column")
	}
	if len(result.Rows) == 0 {
		return 0, fmt.Errorf("EXPLAIN result has no plan rows")
	}
	if len(result.Rows) != 1 {
		return 0, fmt.Errorf("multi-node EXPLAIN result cannot be conservatively estimated")
	}
	var total int64
	for _, row := range result.Rows {
		if rowsIndex >= len(row) {
			return 0, fmt.Errorf("EXPLAIN result row is missing its rows estimate")
		}
		raw := strings.TrimSpace(row[rowsIndex])
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("EXPLAIN result has an invalid rows estimate")
		}
		if value > math.MaxInt64-total {
			return 0, fmt.Errorf("EXPLAIN rows estimate overflow")
		}
		total += value
	}
	return total, nil
}

func escapeIdent(value string) string {
	return strings.ReplaceAll(value, "`", "``")
}

func renderColumn(column schema.Column) string {
	parts := []string{fmt.Sprintf("`%s` %s", escapeIdent(column.Name), column.Type)}
	if column.AutoIncrement {
		parts = append(parts, "AUTO_INCREMENT")
	}
	return strings.Join(parts, " ")
}

func autoIncrementIndexName(table, column string) string {
	natural := "idx_" + table + "_" + column + "_autoinc"
	if len(natural) <= mysqlIdentifierMaxLen {
		return natural
	}
	sum := sha256.Sum256([]byte(table + "\x00" + column))
	suffix := "_" + hex.EncodeToString(sum[:])[:12]
	prefixLimit := mysqlIdentifierMaxLen - len(suffix)
	prefix := sanitizeIdentifierName(natural)
	if len(prefix) > prefixLimit {
		prefix = prefix[:prefixLimit]
	}
	prefix = strings.TrimRight(prefix, "_")
	if prefix == "" {
		prefix = "idx"
	}
	if len(prefix) > prefixLimit {
		prefix = prefix[:prefixLimit]
	}
	return prefix + suffix
}

func sanitizeIdentifierName(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}
