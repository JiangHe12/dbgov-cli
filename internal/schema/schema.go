package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JiangHe12/opskit-core/apperrors"
)

type Schema struct {
	Tables map[string]Table `json:"tables"`
}

type Table struct {
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	Indexes     []Index      `json:"indexes,omitempty"`
	ForeignKeys []ForeignKey `json:"foreignKeys,omitempty"`
}

type Column struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	Nullable      bool    `json:"nullable"`
	AutoIncrement bool    `json:"autoIncrement,omitempty"`
	Default       *string `json:"default,omitempty"`
	Key           string  `json:"key,omitempty"`
}

type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
}

type Action string

const (
	ActionCreateTable  Action = "CREATE_TABLE"
	ActionDropTable    Action = "DROP_TABLE"
	ActionAddColumn    Action = "ADD_COLUMN"
	ActionDropColumn   Action = "DROP_COLUMN"
	ActionModifyColumn Action = "MODIFY_COLUMN"
)

type Change struct {
	Action                     Action   `json:"action"`
	Table                      string   `json:"table"`
	Column                     string   `json:"column,omitempty"`
	Type                       string   `json:"type,omitempty"`
	AutoIncrement              bool     `json:"autoIncrement,omitempty"`
	TypeChanged                bool     `json:"-"`
	AutoIncrementChanged       bool     `json:"-"`
	AutoIncrementIndexRequired bool     `json:"-"`
	Columns                    []Column `json:"columns,omitempty"`
	Destructive                bool     `json:"destructive"`
}

type DiffResult struct {
	Changes     []Change `json:"changes"`
	Destructive bool     `json:"destructive"`
	Warnings    []string `json:"warnings,omitempty"`
}

type Risk string

const (
	RiskR0 Risk = "R0"
	RiskR1 Risk = "R1"
	RiskR2 Risk = "R2"
	RiskR3 Risk = "R3"
)

type RiskSummary struct {
	OverallRisk Risk `json:"overallRisk"`
	Destructive bool `json:"destructive"`
}

var createTableRE = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:` + "`" + `([^` + "`" + `]+)` + "`" + `|"((?:""|[^"])*)"|([a-zA-Z_][a-zA-Z0-9_]*))\s*\((.*)\)\s*(?:[a-z].*)?;?\s*$`)

func ParseDesiredSQL(sqlText string) (Schema, error) {
	trimmed := strings.TrimSpace(sqlText)
	matches := createTableRE.FindStringSubmatch(trimmed)
	if matches == nil {
		return Schema{}, apperrors.New(apperrors.CodeNotImplemented, "unsupported DDL: only simple CREATE TABLE statements are supported", nil)
	}
	tableName := firstNonEmpty(matches[1], strings.ReplaceAll(matches[2], `""`, `"`), matches[3])
	columns, err := parseColumns(matches[4])
	if err != nil {
		return Schema{}, err
	}
	return Schema{Tables: map[string]Table{
		tableName: {Name: tableName, Columns: columns},
	}}, nil
}

func LoadDesiredDir(dir string) (Schema, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Schema{}, err
	}
	result := Schema{Tables: map[string]Table{}}
	seenSQL := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".sql") {
			continue
		}
		seenSQL = true
		data, err := os.ReadFile(filepath.Join(dir, entry.Name())) //nolint:gosec // entry.Name comes from os.ReadDir over the requested schema directory.
		if err != nil {
			return Schema{}, err
		}
		parsed, err := ParseDesiredSQL(string(data))
		if err != nil {
			return Schema{}, err
		}
		for name, table := range parsed.Tables {
			if _, exists := result.Tables[name]; exists {
				return Schema{}, apperrors.New(apperrors.CodeValidationFailed, fmt.Sprintf("duplicate table %q in desired schema directory", name), nil)
			}
			result.Tables[name] = table
		}
	}
	if !seenSQL {
		return Schema{}, apperrors.New(apperrors.CodeValidationFailed, "desired schema directory contains no .sql files", nil)
	}
	return result, nil
}

func SchemaFromDDLMap(tables map[string]string) (Schema, error) {
	result := Schema{Tables: map[string]Table{}}
	for _, tableName := range sortedTableNamesFromDDLMap(tables) {
		parsed, err := ParseDesiredSQL(tables[tableName])
		if err != nil {
			return Schema{}, err
		}
		for name, table := range parsed.Tables {
			if _, exists := result.Tables[name]; exists {
				return Schema{}, apperrors.New(apperrors.CodeValidationFailed, fmt.Sprintf("duplicate table %q in DDL map", name), nil)
			}
			result.Tables[name] = table
		}
	}
	return result, nil
}

func parseColumns(body string) ([]Column, error) {
	parts := splitColumnParts(body)
	columns := make([]Column, 0, len(parts))
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		first := strings.ToUpper(trimSQLIdent(fields[0]))
		switch first {
		case "PRIMARY", "KEY", "UNIQUE", "INDEX", "CONSTRAINT", "FOREIGN":
			continue
		}
		if len(fields) < 2 {
			return nil, apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("unsupported column definition: %s", strings.TrimSpace(part)), nil)
		}
		name := trimSQLIdent(fields[0])
		columnType := columnTypeFromFields(fields[1:])
		if columnType == "" {
			return nil, apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("unsupported column definition: %s", strings.TrimSpace(part)), nil)
		}
		columnType, autoIncrement := normalizeAutoIncrementType(columnType, fields[1:])
		columns = append(columns, Column{Name: name, Type: columnType, AutoIncrement: autoIncrement})
	}
	if len(columns) == 0 {
		return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: CREATE TABLE must contain at least one column", nil)
	}
	return columns, nil
}

func normalizeAutoIncrementType(columnType string, fields []string) (string, bool) {
	switch strings.ToLower(columnType) {
	case "serial":
		return "integer", true
	case "bigserial":
		return "bigint", true
	case "smallserial":
		return "smallint", true
	}
	for index, field := range fields {
		normalized := strings.ToUpper(strings.Trim(field, "`\""))
		if normalized == "AUTO_INCREMENT" {
			return columnType, true
		}
		if normalized == "GENERATED" {
			if hasIdentityClause(fields[index:]) {
				return columnType, true
			}
		}
	}
	return columnType, false
}

func hasIdentityClause(fields []string) bool {
	for _, field := range fields {
		if strings.EqualFold(strings.Trim(field, "`\""), "IDENTITY") {
			return true
		}
	}
	return false
}

func columnTypeFromFields(fields []string) string {
	typeFields := make([]string, 0, len(fields))
	for _, field := range fields {
		if isColumnConstraintKeyword(field) {
			break
		}
		typeFields = append(typeFields, field)
	}
	return strings.Join(typeFields, " ")
}

func isColumnConstraintKeyword(field string) bool {
	switch strings.ToUpper(strings.Trim(field, "`\"")) {
	case "NOT", "NULL", "DEFAULT", "PRIMARY", "UNIQUE", "REFERENCES", "CHECK", "GENERATED", "COLLATE", "CONSTRAINT", "AUTO_INCREMENT", "COMMENT", "KEY", "IDENTITY":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func trimSQLIdent(value string) string {
	if strings.HasPrefix(value, "`") && strings.HasSuffix(value, "`") {
		return strings.TrimSuffix(strings.TrimPrefix(value, "`"), "`")
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(value, `"`), `"`), `""`, `"`)
	}
	return value
}

func splitColumnParts(body string) []string {
	var parts []string
	start := 0
	depth := 0
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, body[start:])
	return parts
}

func Diff(current, desired Schema) DiffResult {
	var result DiffResult
	for _, tableName := range sortedTableNames(desired.Tables) {
		desiredTable := desired.Tables[tableName]
		currentTable, ok := current.Tables[tableName]
		if !ok {
			result.Changes = append(result.Changes, Change{Action: ActionCreateTable, Table: tableName, Columns: desiredTable.Columns})
			continue
		}
		currentCols := columnMap(currentTable.Columns)
		desiredCols := columnMap(desiredTable.Columns)
		added := false
		dropped := false
		for _, col := range desiredTable.Columns {
			if _, ok := currentCols[col.Name]; !ok {
				result.Changes = append(result.Changes, Change{
					Action:                     ActionAddColumn,
					Table:                      tableName,
					Column:                     col.Name,
					Type:                       col.Type,
					AutoIncrement:              col.AutoIncrement,
					AutoIncrementIndexRequired: col.AutoIncrement,
				})
				added = true
				continue
			}
			typeChanged := !sameColumnType(currentCols[col.Name].Type, col.Type)
			autoIncrementChanged := currentCols[col.Name].AutoIncrement != col.AutoIncrement
			if typeChanged || autoIncrementChanged {
				result.Changes = append(result.Changes, Change{
					Action:                     ActionModifyColumn,
					Table:                      tableName,
					Column:                     col.Name,
					Type:                       col.Type,
					AutoIncrement:              col.AutoIncrement,
					TypeChanged:                typeChanged,
					AutoIncrementChanged:       autoIncrementChanged,
					AutoIncrementIndexRequired: col.AutoIncrement && !columnHasLeadingIndex(currentTable, col.Name),
					Destructive:                true,
				})
				result.Destructive = true
			}
		}
		for _, col := range currentTable.Columns {
			if _, ok := desiredCols[col.Name]; !ok {
				result.Changes = append(result.Changes, Change{Action: ActionDropColumn, Table: tableName, Column: col.Name, Type: col.Type, Destructive: true})
				result.Destructive = true
				dropped = true
			}
		}
		if added && dropped {
			result.Warnings = append(result.Warnings, fmt.Sprintf("possible column rename in table %s: add+drop detected; drop will lose data, please confirm manually", tableName))
		}
	}
	return result
}

func ExtraTables(current, desired Schema) []string {
	var extra []string
	for _, tableName := range sortedTableNames(current.Tables) {
		if _, ok := desired.Tables[tableName]; !ok {
			extra = append(extra, tableName)
		}
	}
	return extra
}

func PruneChanges(current, desired Schema) []Change {
	extra := ExtraTables(current, desired)
	changes := make([]Change, 0, len(extra))
	for _, tableName := range extra {
		changes = append(changes, Change{Action: ActionDropTable, Table: tableName, Destructive: true})
	}
	return changes
}

func ClassifyChange(change Change) (Risk, bool) {
	switch change.Action {
	case ActionCreateTable, ActionAddColumn:
		return RiskR1, false
	case ActionDropTable, ActionDropColumn, ActionModifyColumn:
		return RiskR3, true
	default:
		if change.Destructive {
			return RiskR3, true
		}
		return RiskR0, false
	}
}

func ClassifyDiff(diff DiffResult) RiskSummary {
	summary := RiskSummary{OverallRisk: RiskR0, Destructive: diff.Destructive}
	for _, change := range diff.Changes {
		risk, destructive := ClassifyChange(change)
		if riskRank(risk) > riskRank(summary.OverallRisk) {
			summary.OverallRisk = risk
		}
		if destructive {
			summary.Destructive = true
		}
	}
	return summary
}

func columnMap(columns []Column) map[string]Column {
	out := make(map[string]Column, len(columns))
	for _, col := range columns {
		out[col.Name] = col
	}
	return out
}

func columnHasLeadingIndex(table Table, columnName string) bool {
	for _, column := range table.Columns {
		if column.Name == columnName && column.Key != "" {
			return true
		}
	}
	for _, index := range table.Indexes {
		if len(index.Columns) > 0 && index.Columns[0] == columnName {
			return true
		}
	}
	return false
}

func sameColumnType(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func riskRank(risk Risk) int {
	switch risk {
	case RiskR0:
		return 0
	case RiskR1:
		return 1
	case RiskR2:
		return 2
	case RiskR3:
		return 3
	default:
		return 0
	}
}

func sortedTableNames(tables map[string]Table) []string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedTableNamesFromDDLMap(tables map[string]string) []string {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
