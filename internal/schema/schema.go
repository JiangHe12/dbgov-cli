package schema

import (
	"fmt"
	"regexp"
	"strings"
)

type Schema struct {
	Tables map[string]Table `json:"tables"`
}

type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Action string

const (
	ActionAddColumn  Action = "ADD_COLUMN"
	ActionDropColumn Action = "DROP_COLUMN"
)

type Change struct {
	Action      Action `json:"action"`
	Table       string `json:"table"`
	Column      string `json:"column"`
	Type        string `json:"type,omitempty"`
	Destructive bool   `json:"destructive"`
}

type DiffResult struct {
	Changes     []Change `json:"changes"`
	Destructive bool     `json:"destructive"`
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
	for tableName, desiredTable := range desired.Tables {
		currentTable, ok := current.Tables[tableName]
		if !ok {
			for _, col := range desiredTable.Columns {
				result.Changes = append(result.Changes, Change{
					Action: ActionAddColumn,
					Table:  tableName,
					Column: col.Name,
					Type:   col.Type,
				})
			}
			continue
		}
		currentCols := columnMap(currentTable.Columns)
		desiredCols := columnMap(desiredTable.Columns)
		for _, col := range desiredTable.Columns {
			if _, ok := currentCols[col.Name]; !ok {
				result.Changes = append(result.Changes, Change{Action: ActionAddColumn, Table: tableName, Column: col.Name, Type: col.Type})
			}
		}
		for _, col := range currentTable.Columns {
			if _, ok := desiredCols[col.Name]; !ok {
				result.Changes = append(result.Changes, Change{Action: ActionDropColumn, Table: tableName, Column: col.Name, Type: col.Type, Destructive: true})
				result.Destructive = true
			}
		}
	}
	return result
}

func columnMap(columns []Column) map[string]Column {
	out := make(map[string]Column, len(columns))
	for _, col := range columns {
		out[col.Name] = col
	}
	return out
}
