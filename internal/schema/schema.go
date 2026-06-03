package schema

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
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
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default,omitempty"`
	Key      string  `json:"key,omitempty"`
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
	Action      Action   `json:"action"`
	Table       string   `json:"table"`
	Column      string   `json:"column,omitempty"`
	Type        string   `json:"type,omitempty"`
	Columns     []Column `json:"columns,omitempty"`
	Destructive bool     `json:"destructive"`
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

var createTableRE = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+` + "`?" + `([a-zA-Z_][a-zA-Z0-9_]*)` + "`?" + `\s*\((.*)\)\s*;?\s*$`)

func ParseDesiredSQL(sqlText string) (Schema, error) {
	trimmed := strings.TrimSpace(sqlText)
	matches := createTableRE.FindStringSubmatch(trimmed)
	if matches == nil {
		return Schema{}, fmt.Errorf("unsupported DDL: only simple CREATE TABLE statements are supported")
	}
	tableName := matches[1]
	columns, err := parseColumns(matches[2])
	if err != nil {
		return Schema{}, err
	}
	return Schema{Tables: map[string]Table{
		tableName: {Name: tableName, Columns: columns},
	}}, nil
}

func parseColumns(body string) ([]Column, error) {
	parts := splitColumnParts(body)
	columns := make([]Column, 0, len(parts))
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		first := strings.ToUpper(strings.Trim(fields[0], "`"))
		switch first {
		case "PRIMARY", "KEY", "UNIQUE", "INDEX", "CONSTRAINT", "FOREIGN":
			continue
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("unsupported column definition: %s", strings.TrimSpace(part))
		}
		name := strings.Trim(fields[0], "`")
		columns = append(columns, Column{Name: name, Type: fields[1]})
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("unsupported DDL: CREATE TABLE must contain at least one column")
	}
	return columns, nil
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
				result.Changes = append(result.Changes, Change{Action: ActionAddColumn, Table: tableName, Column: col.Name, Type: col.Type})
				added = true
				continue
			}
			if !sameColumnType(currentCols[col.Name].Type, col.Type) {
				result.Changes = append(result.Changes, Change{Action: ActionModifyColumn, Table: tableName, Column: col.Name, Type: col.Type, Destructive: true})
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
	for _, tableName := range sortedTableNames(current.Tables) {
		if _, ok := desired.Tables[tableName]; !ok {
			result.Changes = append(result.Changes, Change{Action: ActionDropTable, Table: tableName, Destructive: true})
			result.Destructive = true
		}
	}
	return result
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

func sameColumnType(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func riskRank(risk Risk) int {
	switch risk {
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
