package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
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

func ParseDesiredSQL(sqlText string) (Schema, error) {
	trimmed := strings.TrimSpace(sqlText)
	tableName, body, ok := parseCreateTableEnvelope(trimmed)
	if !ok {
		return Schema{}, apperrors.New(apperrors.CodeNotImplemented, "unsupported DDL: only simple CREATE TABLE statements are supported", nil)
	}
	columns, err := parseColumns(body)
	if err != nil {
		return Schema{}, err
	}
	return Schema{Tables: map[string]Table{
		tableName: {Name: tableName, Columns: columns},
	}}, nil
}

type ddlScanner struct {
	sql string
	pos int
}

func parseCreateTableEnvelope(sqlText string) (tableName, body string, ok bool) {
	scanner := ddlScanner{sql: sqlText}
	if !scanner.consumeWord("create") || !scanner.consumeWord("table") {
		return "", "", false
	}
	scanner.skipSpace()
	qualifierQuote := byte(0)
	if scanner.peek() == '"' || scanner.peek() == '`' {
		qualifierQuote = scanner.peek()
	}
	tableName, ok = scanner.readIdentifier()
	if !ok {
		return "", "", false
	}
	scanner.skipSpace()
	if scanner.peek() == '.' {
		if !isPublicSchemaQualifier(tableName, qualifierQuote) {
			return "", "", false
		}
		scanner.pos++
		tableName, ok = scanner.readIdentifier()
		if !ok {
			return "", "", false
		}
		scanner.skipSpace()
	}
	if scanner.peek() != '(' {
		return "", "", false
	}
	bodyStart := scanner.pos + 1
	bodyEnd, ok := scanner.skipBalancedBody()
	if !ok || !validCreateTableTail(scanner.sql[scanner.pos:]) {
		return "", "", false
	}
	return tableName, scanner.sql[bodyStart:bodyEnd], true
}

func isPublicSchemaQualifier(value string, quote byte) bool {
	switch quote {
	case 0:
		return strings.EqualFold(value, "public")
	case '"':
		return value == "public"
	default:
		return false
	}
}

func (s *ddlScanner) consumeWord(want string) bool {
	s.skipSpace()
	start := s.pos
	for s.pos < len(s.sql) && isDDLIdentifierPart(s.sql[s.pos]) {
		s.pos++
	}
	if start == s.pos || !strings.EqualFold(s.sql[start:s.pos], want) {
		s.pos = start
		return false
	}
	return true
}

func (s *ddlScanner) readIdentifier() (string, bool) {
	s.skipSpace()
	if s.pos >= len(s.sql) {
		return "", false
	}
	switch s.sql[s.pos] {
	case '`', '"':
		quote := s.sql[s.pos]
		s.pos++
		var value strings.Builder
		for s.pos < len(s.sql) {
			if s.sql[s.pos] != quote {
				value.WriteByte(s.sql[s.pos])
				s.pos++
				continue
			}
			s.pos++
			if s.pos < len(s.sql) && s.sql[s.pos] == quote {
				value.WriteByte(quote)
				s.pos++
				continue
			}
			return value.String(), value.Len() > 0
		}
		return "", false
	default:
		if !isDDLIdentifierStart(s.sql[s.pos]) {
			return "", false
		}
		start := s.pos
		s.pos++
		for s.pos < len(s.sql) && isDDLIdentifierPart(s.sql[s.pos]) {
			s.pos++
		}
		return s.sql[start:s.pos], true
	}
}

//nolint:gocyclo // CREATE TABLE bodies require one quote/comment-aware pass to identify the actual closing parenthesis.
func (s *ddlScanner) skipBalancedBody() (int, bool) {
	if s.peek() != '(' {
		return 0, false
	}
	depth := 0
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case '\'', '"', '`':
			if !s.skipQuoted(s.sql[s.pos]) {
				return 0, false
			}
		case '(':
			depth++
			s.pos++
		case ')':
			depth--
			if depth < 0 {
				return 0, false
			}
			bodyEnd := s.pos
			s.pos++
			if depth == 0 {
				return bodyEnd, true
			}
		case ';':
			return 0, false
		case '-', '/':
			start := s.pos
			if !s.skipComment() {
				return 0, false
			}
			if s.pos == start {
				s.pos++
			}
		default:
			s.pos++
		}
	}
	return 0, false
}

func (s *ddlScanner) skipQuoted(quote byte) bool {
	s.pos++
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case '\\':
			start := s.pos
			for s.pos < len(s.sql) && s.sql[s.pos] == '\\' {
				s.pos++
			}
			if s.pos < len(s.sql) && s.sql[s.pos] == quote && (s.pos-start)%2 != 0 {
				return false
			}
		case quote:
			s.pos++
			if s.pos < len(s.sql) && s.sql[s.pos] == quote {
				s.pos++
				continue
			}
			return true
		default:
			s.pos++
		}
	}
	return false
}

func (s *ddlScanner) skipComment() bool {
	if s.pos+1 >= len(s.sql) {
		return true
	}
	switch s.sql[s.pos : s.pos+2] {
	case "--":
		s.pos += 2
		for s.pos < len(s.sql) && s.sql[s.pos] != '\n' && s.sql[s.pos] != '\r' {
			s.pos++
		}
		return true
	case "/*":
		end := strings.Index(s.sql[s.pos+2:], "*/")
		if end < 0 {
			return false
		}
		s.pos += end + 4
		return true
	default:
		return true
	}
}

func (s *ddlScanner) skipSpace() {
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			s.pos++
		default:
			return
		}
	}
}

func (s *ddlScanner) peek() byte {
	if s.pos >= len(s.sql) {
		return 0
	}
	return s.sql[s.pos]
}

func validCreateTableTail(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" || tail == ";" {
		return true
	}
	if strings.HasSuffix(tail, ";") {
		tail = strings.TrimSpace(strings.TrimSuffix(tail, ";"))
	}
	if tail == "" || !validDDLFragment(tail, true) {
		return tail == ""
	}
	first := firstDDLWord(tail)
	switch first {
	case "auto_increment", "character", "collate", "comment", "compression", "default", "encryption",
		"engine", "inherits", "key_block_size", "on", "partition", "row_format", "stats_persistent",
		"tablespace", "using", "with", "without":
		return !containsDangerousDDLWord(tail)
	default:
		return false
	}
}

func firstDDLWord(fragment string) string {
	scanner := ddlScanner{sql: fragment}
	scanner.skipSpace()
	start := scanner.pos
	for scanner.pos < len(scanner.sql) && isDDLIdentifierPart(scanner.sql[scanner.pos]) {
		scanner.pos++
	}
	return strings.ToLower(scanner.sql[start:scanner.pos])
}

func isDDLIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isDDLIdentifierPart(ch byte) bool {
	return isDDLIdentifierStart(ch) || ch >= '0' && ch <= '9' || ch == '$'
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
	parts, err := splitColumnParts(body)
	if err != nil {
		return nil, err
	}
	columns := make([]Column, 0, len(parts))
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		first := strings.ToUpper(trimSQLIdent(fields[0]))
		switch first {
		case "PRIMARY", "KEY", "UNIQUE", "INDEX", "CONSTRAINT", "FOREIGN", "CHECK", "FULLTEXT", "SPATIAL", "EXCLUDE", "LIKE":
			return nil, unsupportedSchemaDefinition()
		}
		if len(fields) < 2 {
			return nil, apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("unsupported column definition: %s", strings.TrimSpace(part)), nil)
		}
		name := trimSQLIdent(fields[0])
		columnType, modifiers := columnTypeAndModifiers(fields[1:])
		if columnType == "" {
			return nil, apperrors.New(apperrors.CodeNotImplemented, fmt.Sprintf("unsupported column definition: %s", strings.TrimSpace(part)), nil)
		}
		if !supportedColumnModifiers(modifiers) {
			return nil, unsupportedSchemaDefinition()
		}
		if !validDDLFragment(columnType, false) || containsDangerousDDLWord(columnType, "after", "first", "using") {
			return nil, apperrors.New(apperrors.CodeValidationFailed, fmt.Sprintf("unsafe column type in definition: %s", strings.TrimSpace(part)), nil)
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

func columnTypeAndModifiers(fields []string) (string, []string) {
	typeFields := make([]string, 0, len(fields))
	for index, field := range fields {
		if isColumnConstraintKeyword(field) {
			return strings.Join(typeFields, " "), fields[index:]
		}
		typeFields = append(typeFields, field)
	}
	return strings.Join(typeFields, " "), nil
}

func supportedColumnModifiers(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	normalized := make([]string, len(fields))
	for index, field := range fields {
		normalized[index] = strings.ToUpper(strings.Trim(field, "`\""))
	}
	if len(normalized) == 1 && normalized[0] == "AUTO_INCREMENT" {
		return true
	}
	return slices.Equal(normalized, []string{"GENERATED", "ALWAYS", "AS", "IDENTITY"}) ||
		slices.Equal(normalized, []string{"GENERATED", "BY", "DEFAULT", "AS", "IDENTITY"})
}

func unsupportedSchemaDefinition() error {
	return apperrors.New(
		apperrors.CodeNotImplemented,
		"schema definition contains constraints or modifiers that cannot be represented losslessly",
		nil,
	)
}

func isColumnConstraintKeyword(field string) bool {
	switch strings.ToUpper(strings.Trim(field, "`\"")) {
	case "NOT", "NULL", "DEFAULT", "PRIMARY", "UNIQUE", "REFERENCES", "CHECK", "GENERATED", "COLLATE", "CONSTRAINT", "AUTO_INCREMENT", "COMMENT", "KEY", "IDENTITY", "ON":
		return true
	default:
		return false
	}
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

//nolint:gocyclo // Column splitting must keep quote, comment, and parenthesis state in one auditable pass.
func splitColumnParts(body string) ([]string, error) {
	var parts []string
	start := 0
	depth := 0
	scanner := ddlScanner{sql: body}
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"', '`':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: unterminated quoted value in CREATE TABLE", nil)
			}
		case '(':
			depth++
			scanner.pos++
		case ')':
			if depth == 0 {
				return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: unbalanced column type parentheses", nil)
			}
			depth--
			scanner.pos++
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:scanner.pos])
				start = scanner.pos + 1
			}
			scanner.pos++
		case ';':
			return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: statement separator inside CREATE TABLE", nil)
		case '#':
			return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: MySQL hash comments are not allowed in CREATE TABLE", nil)
		case '-', '/':
			commentStart := scanner.pos
			if !scanner.skipComment() {
				return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: unterminated comment in CREATE TABLE", nil)
			}
			if scanner.pos == commentStart {
				scanner.pos++
			}
		default:
			scanner.pos++
		}
	}
	if depth != 0 {
		return nil, apperrors.New(apperrors.CodeValidationFailed, "unsupported DDL: unbalanced column type parentheses", nil)
	}
	parts = append(parts, body[start:])
	return parts, nil
}

//nolint:gocyclo // Raw column types are rendered into DDL, so validation must account for every quoted and nested form.
func validDDLFragment(fragment string, allowComments bool) bool {
	scanner := ddlScanner{sql: fragment}
	depth := 0
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"', '`':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return false
			}
		case '(':
			depth++
			scanner.pos++
		case ')':
			if depth == 0 {
				return false
			}
			depth--
			scanner.pos++
		case ';':
			return false
		case '#':
			if !allowComments {
				return false
			}
			scanner.pos++
		case '-', '/':
			start := scanner.pos
			if !scanner.skipComment() || (!allowComments && scanner.pos != start) {
				return false
			}
			if scanner.pos == start {
				scanner.pos++
			}
		default:
			scanner.pos++
		}
	}
	return depth == 0
}

func containsDangerousDDLWord(fragment string, additional ...string) bool {
	scanner := ddlScanner{sql: fragment}
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"', '`':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return true
			}
		case '-', '/':
			start := scanner.pos
			if !scanner.skipComment() {
				return true
			}
			if scanner.pos == start {
				scanner.pos++
			}
		default:
			if !isDDLIdentifierStart(scanner.sql[scanner.pos]) {
				scanner.pos++
				continue
			}
			start := scanner.pos
			scanner.pos++
			for scanner.pos < len(scanner.sql) && isDDLIdentifierPart(scanner.sql[scanner.pos]) {
				scanner.pos++
			}
			switch strings.ToLower(scanner.sql[start:scanner.pos]) {
			case "alter", "create", "delete", "drop", "grant", "insert", "revoke", "truncate", "update":
				return true
			}
			for _, word := range additional {
				if strings.EqualFold(scanner.sql[start:scanner.pos], word) {
					return true
				}
			}
		}
	}
	return false
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
