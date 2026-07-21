package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx database/sql driver.

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	dbbackend "github.com/JiangHe12/dbgov-cli/internal/backend"
	"github.com/JiangHe12/dbgov-cli/internal/schema"
)

type Backend struct {
	db       *sql.DB
	database string
	schema   string
}

const defaultSchema = "public"

func New(dsn, database string) (*Backend, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	return NewWithDB(db, database), nil
}

func NewWithDB(db *sql.DB, database string) *Backend {
	return &Backend{db: db, database: database, schema: defaultSchema}
}

func (b *Backend) Close() error {
	return b.db.Close()
}

func (b *Backend) Ping(ctx context.Context) error {
	return b.db.PingContext(ctx)
}

func (b *Backend) IntrospectSchema(ctx context.Context) (schema.Schema, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT c.relname AS table_name,
       a.attname AS column_name,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS column_type,
       a.attnotnull AS not_null,
       pg_get_expr(ad.adbin, ad.adrelid) AS column_default,
       a.attidentity AS identity_kind,
       EXISTS (
           SELECT 1
           FROM pg_depend dep
           JOIN pg_class seq ON seq.oid = dep.objid
           WHERE seq.relkind = 'S'
             AND dep.refobjid = c.oid
             AND dep.refobjsubid = a.attnum
             AND dep.deptype = 'a'
       ) AS has_owned_sequence,
       EXISTS (
           SELECT 1
           FROM pg_constraint pk
           WHERE pk.conrelid = c.oid
             AND pk.contype = 'p'
             AND a.attnum = ANY(pk.conkey)
       ) AS is_primary
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
LEFT JOIN pg_attrdef ad ON ad.adrelid = c.oid AND ad.adnum = a.attnum
WHERE n.nspname = $1
  AND c.relkind IN ('r', 'p')
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`, b.schema)
	if err != nil {
		return schema.Schema{}, err
	}
	defer func() { _ = rows.Close() }()

	result := schema.Schema{Tables: map[string]schema.Table{}}
	for rows.Next() {
		var tableName, columnName, columnType string
		var notNull, hasOwnedSequence, primary bool
		var defaultValue, identityKind sql.NullString
		if err := rows.Scan(&tableName, &columnName, &columnType, &notNull, &defaultValue, &identityKind, &hasOwnedSequence, &primary); err != nil {
			return schema.Schema{}, err
		}
		table := result.Tables[tableName]
		if table.Name == "" {
			table.Name = tableName
		}
		autoIncrement := identityKind.String == "a" || identityKind.String == "d" || (hasOwnedSequence && isPostgresNextvalDefault(defaultValue.String))
		column := schema.Column{Name: columnName, Type: columnType, Nullable: !notNull, AutoIncrement: autoIncrement}
		if primary {
			column.Key = "PRI"
		}
		if defaultValue.Valid && !autoIncrement {
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
SELECT tbl.relname AS table_name,
       idx.relname AS index_name,
       att.attname AS column_name,
       i.indisunique AS is_unique,
       ord.n AS ordinal
FROM pg_index i
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace nsp ON nsp.oid = tbl.relnamespace
JOIN pg_class idx ON idx.oid = i.indexrelid
JOIN unnest(i.indkey) WITH ORDINALITY AS ord(attnum, n) ON true
JOIN pg_attribute att ON att.attrelid = tbl.oid AND att.attnum = ord.attnum
WHERE nsp.nspname = $1
ORDER BY tbl.relname, idx.relname, ord.n`, b.schema)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type key struct{ table, name string }
	indexes := map[key]*schema.Index{}
	order := []key{}
	for rows.Next() {
		var tableName, indexName, columnName string
		var unique bool
		var ordinal int
		if err := rows.Scan(&tableName, &indexName, &columnName, &unique, &ordinal); err != nil {
			return err
		}
		k := key{table: tableName, name: indexName}
		idx, ok := indexes[k]
		if !ok {
			indexes[k] = &schema.Index{Name: indexName, Unique: unique}
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
SELECT tbl.relname AS table_name,
       con.conname AS constraint_name,
       src.attname AS column_name,
       ref.relname AS referenced_table_name,
       dst.attname AS referenced_column_name,
       ord.n AS ordinal
FROM pg_constraint con
JOIN pg_class tbl ON tbl.oid = con.conrelid
JOIN pg_namespace nsp ON nsp.oid = tbl.relnamespace
JOIN pg_class ref ON ref.oid = con.confrelid
JOIN unnest(con.conkey, con.confkey) WITH ORDINALITY AS ord(src_attnum, dst_attnum, n) ON true
JOIN pg_attribute src ON src.attrelid = tbl.oid AND src.attnum = ord.src_attnum
JOIN pg_attribute dst ON dst.attrelid = ref.oid AND dst.attnum = ord.dst_attnum
WHERE nsp.nspname = $1
  AND con.contype = 'f'
ORDER BY tbl.relname, con.conname, ord.n`, b.schema)
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
	current, err := b.IntrospectSchema(ctx)
	if err != nil {
		return "", err
	}
	tbl, ok := current.Tables[table]
	if !ok {
		return "", apperrors.New(apperrors.CodeResourceNotFound, fmt.Sprintf("table %q not found", table), nil)
	}
	return b.renderTableDDL(ctx, tbl)
}

func (b *Backend) RenderDDL(changes []schema.Change) ([]string, error) {
	statements := make([]string, 0, len(changes))
	for _, change := range changes {
		tableName := qualifiedIdent(b.schemaName(), change.Table)
		switch change.Action {
		case schema.ActionCreateTable:
			columns := make([]string, 0, len(change.Columns))
			for _, column := range change.Columns {
				columns = append(columns, renderColumnDefinition(column))
			}
			if len(columns) == 0 {
				return nil, apperrors.New(apperrors.CodeValidationFailed, fmt.Sprintf("CREATE_TABLE %s has no columns", change.Table), nil)
			}
			statements = append(statements, fmt.Sprintf("CREATE TABLE %s (%s);", tableName, strings.Join(columns, ", ")))
		case schema.ActionAddColumn:
			column := schema.Column{Name: change.Column, Type: change.Type, AutoIncrement: change.AutoIncrement}
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", tableName, renderColumnDefinition(column)))
		case schema.ActionModifyColumn:
			var clauses []string
			if change.TypeChanged {
				clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s TYPE %s", quoteIdent(change.Column), change.Type))
			}
			if change.AutoIncrementChanged {
				if change.AutoIncrement {
					clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s SET NOT NULL", quoteIdent(change.Column)))
					clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s ADD GENERATED BY DEFAULT AS IDENTITY", quoteIdent(change.Column)))
				} else {
					clauses = append(clauses, fmt.Sprintf("ALTER COLUMN %s DROP IDENTITY IF EXISTS", quoteIdent(change.Column)))
				}
			}
			if len(clauses) == 0 {
				continue
			}
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s %s;", tableName, strings.Join(clauses, ", ")))
		case schema.ActionDropColumn:
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", tableName, quoteIdent(change.Column)))
		case schema.ActionDropTable:
			statements = append(statements, fmt.Sprintf("DROP TABLE %s;", tableName))
		default:
			return nil, apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("unsupported schema change action %s", change.Action), nil)
		}
	}
	return statements, nil
}

func (b *Backend) ExecDDL(ctx context.Context, statements []string) (int, error) {
	if len(statements) == 0 {
		return 0, nil
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, backendErr("begin DDL transaction", err)
	}
	for i, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return 0, apperrors.New(
					apperrors.CodeBackendError,
					fmt.Sprintf("execute DDL statement %d and rollback transaction", i+1),
					errors.Join(err, rollbackErr),
				)
			}
			return 0, apperrors.New(apperrors.CodeBackendError, fmt.Sprintf("execute DDL statement %d", i+1), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, backendErr("commit DDL transaction", err)
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
	explainSQL := "EXPLAIN (FORMAT JSON) " + strings.TrimSpace(sqlText) //nolint:gosec // Adds EXPLAIN to an already classified statement.
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

func (b *Backend) renderTableDDL(ctx context.Context, table schema.Table) (string, error) {
	constraints, err := b.tableConstraints(ctx, table.Name)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(table.Columns)+len(constraints))
	for _, column := range table.Columns {
		parts = append(parts, "  "+renderColumnDefinition(column))
	}
	for _, constraint := range constraints {
		parts = append(parts, "  "+constraint)
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n);", qualifiedIdent(b.schemaName(), table.Name), strings.Join(parts, ",\n")), nil
}

func (b *Backend) tableConstraints(ctx context.Context, table string) ([]string, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT con.conname,
       con.contype,
       string_agg(src.attname, E'\x1f' ORDER BY ord.n) AS columns,
       ref_nsp.nspname AS referenced_schema,
       ref.relname AS referenced_table,
       string_agg(dst.attname, E'\x1f' ORDER BY ord.n) FILTER (WHERE dst.attname IS NOT NULL) AS referenced_columns
FROM pg_constraint con
JOIN pg_class tbl ON tbl.oid = con.conrelid
JOIN pg_namespace nsp ON nsp.oid = tbl.relnamespace
JOIN unnest(con.conkey) WITH ORDINALITY AS ord(src_attnum, n) ON true
JOIN pg_attribute src ON src.attrelid = tbl.oid AND src.attnum = ord.src_attnum
LEFT JOIN pg_class ref ON ref.oid = con.confrelid
LEFT JOIN pg_namespace ref_nsp ON ref_nsp.oid = ref.relnamespace
LEFT JOIN unnest(con.confkey) WITH ORDINALITY AS ref_ord(dst_attnum, n) ON ref_ord.n = ord.n
LEFT JOIN pg_attribute dst ON dst.attrelid = ref.oid AND dst.attnum = ref_ord.dst_attnum
WHERE nsp.nspname = $1
  AND tbl.relname = $2
  AND con.contype IN ('p', 'u', 'f')
GROUP BY con.conname, con.contype, ref_nsp.nspname, ref.relname
ORDER BY con.conname`, b.schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var constraints []string
	for rows.Next() {
		var name, constraintType string
		var columns string
		var refSchema sql.NullString
		var refTable sql.NullString
		var refColumns sql.NullString
		if err := rows.Scan(&name, &constraintType, &columns, &refSchema, &refTable, &refColumns); err != nil {
			return nil, err
		}
		quotedColumns := quoteIdentList(splitCatalogList(columns))
		switch constraintType {
		case "p":
			constraints = append(constraints, fmt.Sprintf("CONSTRAINT %s PRIMARY KEY (%s)", quoteIdent(name), strings.Join(quotedColumns, ", ")))
		case "u":
			constraints = append(constraints, fmt.Sprintf("CONSTRAINT %s UNIQUE (%s)", quoteIdent(name), strings.Join(quotedColumns, ", ")))
		case "f":
			constraints = append(constraints, fmt.Sprintf(
				"CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
				quoteIdent(name),
				strings.Join(quotedColumns, ", "),
				qualifiedIdent(refSchema.String, refTable.String),
				strings.Join(quoteIdentList(splitCatalogList(refColumns.String)), ", "),
			))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return constraints, nil
}

func splitCatalogList(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "\x1f")
}

func isPostgresNextvalDefault(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "nextval(")
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
	if len(result.Columns) != 1 ||
		!strings.EqualFold(strings.TrimSpace(result.Columns[0]), "QUERY PLAN") ||
		len(result.Rows) != 1 ||
		len(result.Rows[0]) != 1 ||
		strings.TrimSpace(result.Rows[0][0]) == "" {
		return 0, fmt.Errorf("EXPLAIN result has an invalid JSON plan shape")
	}
	return planRowsFromExplainJSON(result.Rows[0][0])
}

func planRowsFromExplainJSON(planJSON string) (int64, error) { //nolint:gocyclo // Strictly validates every PostgreSQL JSON plan branch before trusting its estimate.
	type planNode struct {
		NodeType  string      `json:"Node Type"`
		Operation string      `json:"Operation"`
		PlanRows  *float64    `json:"Plan Rows"`
		Plans     []*planNode `json:"Plans"`
	}
	var plans []struct {
		Plan *planNode `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(planJSON), &plans); err != nil {
		return 0, err
	}
	if len(plans) != 1 || plans[0].Plan == nil || plans[0].Plan.PlanRows == nil {
		return 0, fmt.Errorf("EXPLAIN JSON plan is missing Plan Rows")
	}
	rootRows, err := validPlanRows(plans[0].Plan.PlanRows)
	if err != nil {
		return 0, err
	}
	plan := plans[0].Plan
	if !strings.EqualFold(plan.NodeType, "ModifyTable") &&
		!strings.EqualFold(plan.Operation, "Insert") &&
		!strings.EqualFold(plan.Operation, "Update") &&
		!strings.EqualFold(plan.Operation, "Delete") &&
		!strings.EqualFold(plan.Operation, "Merge") {
		return rootRows, nil
	}
	if len(plan.Plans) == 0 {
		return 0, fmt.Errorf("EXPLAIN JSON ModifyTable plan has no input plans")
	}
	var inputRows int64
	for _, child := range plan.Plans {
		if child == nil || child.PlanRows == nil {
			return 0, fmt.Errorf("EXPLAIN JSON ModifyTable input is missing Plan Rows")
		}
		rows, err := validPlanRows(child.PlanRows)
		if err != nil {
			return 0, err
		}
		if rows > math.MaxInt64-inputRows {
			return 0, fmt.Errorf("EXPLAIN JSON Plan Rows estimate overflow")
		}
		inputRows += rows
	}
	if inputRows > rootRows {
		return inputRows, nil
	}
	return rootRows, nil
}

func validPlanRows(planRows *float64) (int64, error) {
	if planRows == nil {
		return 0, fmt.Errorf("EXPLAIN JSON plan is missing Plan Rows")
	}
	rows := *planRows
	if math.IsNaN(rows) ||
		math.IsInf(rows, 0) ||
		rows < 0 ||
		rows >= 9223372036854775808.0 ||
		math.Trunc(rows) != rows {
		return 0, fmt.Errorf("EXPLAIN JSON has an invalid Plan Rows estimate")
	}
	return int64(rows), nil
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func qualifiedIdent(schemaName, objectName string) string {
	return quoteIdent(schemaName) + "." + quoteIdent(objectName)
}

func (b *Backend) schemaName() string {
	if strings.TrimSpace(b.schema) == "" {
		return defaultSchema
	}
	return b.schema
}

func quoteIdentList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, quoteIdent(value))
	}
	return out
}

func renderColumn(column schema.Column) string {
	return fmt.Sprintf("%s %s", quoteIdent(column.Name), column.Type)
}

func renderColumnDefinition(column schema.Column) string {
	parts := []string{renderColumn(column)}
	if column.AutoIncrement {
		parts = append(parts, "NOT NULL")
	}
	if column.Default != nil && !column.AutoIncrement {
		parts = append(parts, "DEFAULT "+*column.Default)
	}
	if column.AutoIncrement {
		parts = append(parts, "GENERATED BY DEFAULT AS IDENTITY")
	}
	return strings.Join(parts, " ")
}
