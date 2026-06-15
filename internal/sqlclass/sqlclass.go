package sqlclass

import (
	"regexp"
	"strings"
)

type Kind string

type Dialect string

const (
	KindInsert Kind = "insert"
	KindUpdate Kind = "update"
	KindDelete Kind = "delete"

	DialectMySQL    Dialect = "mysql"
	DialectPostgres Dialect = "postgres"
	DialectStrict   Dialect = "strict"
)

var whereRE = regexp.MustCompile(`(?i)\bwhere\b`)

func DialectForEngine(engine string) Dialect {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "mysql":
		return DialectMySQL
	case "postgres", "postgresql":
		return DialectPostgres
	default:
		return DialectStrict
	}
}

func IsReadOnly(sql string, dialects ...Dialect) bool {
	dialect := selectedDialect(dialects)
	if dialect == DialectStrict {
		return false
	}
	if dialect == DialectPostgres && HasMultipleStatements(sql, dialect) {
		return false
	}
	keyword := operativeKeyword(sql, dialect)
	switch keyword {
	case "select", "show", "describe", "desc", "explain":
		return true
	default:
		return false
	}
}

func ClassifyDML(sql string, dialects ...Dialect) (kind Kind, hasWhere bool, ok bool) {
	dialect := selectedDialect(dialects)
	if dialect == DialectStrict {
		return "", false, false
	}
	switch firstKeyword(sql, dialect) {
	case "insert":
		return KindInsert, false, true
	case "update":
		return KindUpdate, hasWhereToken(sql), true
	case "delete":
		return KindDelete, hasWhereToken(sql), true
	default:
		return "", false, false
	}
}

//nolint:gocyclo // SQL token scanning is intentionally centralized so dialect fail-closed rules stay in one place.
func HasMultipleStatements(sql string, dialects ...Dialect) bool {
	dialect := selectedDialect(dialects)
	if dialect == DialectStrict {
		return strings.TrimSpace(sql) != ""
	}
	scanner := sqlScanner{sql: sql, dialect: dialect}
	depth := 0
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"', '`':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return scanner.failClosed()
			}
		case '$':
			if scanner.dialect == DialectPostgres && scanner.hasDollarQuote() {
				if !scanner.skipDollarQuote() {
					return true
				}
				continue
			}
			scanner.pos++
		case '(':
			depth++
			scanner.pos++
		case ')':
			if depth > 0 {
				depth--
			}
			scanner.pos++
		case ';':
			scanner.pos++
			if scanner.dialect == DialectPostgres || depth == 0 {
				if !scanner.skipIgnorable() {
					return true
				}
				return scanner.pos < len(scanner.sql)
			}
		case '-', '/':
			start := scanner.pos
			if !scanner.skipComment() {
				return scanner.failClosed()
			}
			if scanner.pos == start {
				scanner.pos++
			}
		default:
			scanner.pos++
		}
	}
	return false
}

func selectedDialect(dialects []Dialect) Dialect {
	if len(dialects) == 0 {
		return DialectMySQL
	}
	return dialects[0]
}

func operativeKeyword(sql string, dialect Dialect) string {
	scanner := sqlScanner{sql: sql, dialect: dialect}
	keyword, ok := scanner.readWord()
	if !ok || keyword != "with" {
		return keyword
	}
	scanner.consumeWord("recursive")
	for {
		if !scanner.skipIdentifier() {
			return ""
		}
		if !scanner.skipIgnorable() {
			return ""
		}
		if scanner.peek() == '(' && !scanner.skipBalanced() {
			return ""
		}
		if !scanner.consumeWord("as") {
			return ""
		}
		if !scanner.skipIgnorable() || scanner.peek() != '(' || !scanner.skipBalanced() {
			return ""
		}
		if !scanner.skipIgnorable() {
			return ""
		}
		if scanner.peek() != ',' {
			keyword, _ = scanner.readWord()
			return keyword
		}
		scanner.pos++
	}
}

type sqlScanner struct {
	sql     string
	pos     int
	dialect Dialect
}

func (s *sqlScanner) readWord() (string, bool) {
	if !s.skipIgnorable() || s.pos >= len(s.sql) || !isIdentifierStart(s.sql[s.pos]) {
		return "", false
	}
	start := s.pos
	s.pos++
	for s.pos < len(s.sql) && isIdentifierPart(s.sql[s.pos]) {
		s.pos++
	}
	return strings.ToLower(s.sql[start:s.pos]), true
}

func (s *sqlScanner) consumeWord(want string) bool {
	start := s.pos
	word, ok := s.readWord()
	if !ok || word != want {
		s.pos = start
		return false
	}
	return true
}

func (s *sqlScanner) skipIdentifier() bool {
	if !s.skipIgnorable() || s.pos >= len(s.sql) {
		return false
	}
	if s.dialect == DialectMySQL && s.sql[s.pos] == '`' {
		return s.skipQuoted('`')
	}
	if s.dialect == DialectPostgres && s.sql[s.pos] == '"' {
		return s.skipQuoted('"')
	}
	if !isIdentifierStart(s.sql[s.pos]) {
		return false
	}
	s.pos++
	for s.pos < len(s.sql) && isIdentifierPart(s.sql[s.pos]) {
		s.pos++
	}
	return true
}

//nolint:gocyclo // Balanced SQL scanning keeps quote/comment/dollar-quote state in one fail-closed loop.
func (s *sqlScanner) skipBalanced() bool {
	if s.peek() != '(' {
		return false
	}
	depth := 0
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case '\'', '"', '`':
			if !s.skipQuoted(s.sql[s.pos]) {
				return false
			}
		case '$':
			if s.dialect == DialectPostgres && s.hasDollarQuote() {
				if !s.skipDollarQuote() {
					return false
				}
				continue
			}
			s.pos++
		case '(':
			depth++
			s.pos++
		case ')':
			depth--
			s.pos++
			if depth == 0 {
				return true
			}
		case ';':
			if s.dialect == DialectPostgres {
				return false
			}
			s.pos++
		case '-', '/':
			start := s.pos
			if !s.skipComment() {
				return false
			}
			if s.pos == start {
				s.pos++
			}
		default:
			s.pos++
		}
	}
	return false
}

func (s *sqlScanner) skipQuoted(quote byte) bool {
	if s.dialect == DialectPostgres && quote == '`' {
		s.pos++
		return true
	}
	s.pos++
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case '\\':
			if s.dialect == DialectPostgres {
				s.pos++
			} else {
				s.pos += 2
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

func (s *sqlScanner) hasDollarQuote() bool {
	return s.dollarQuoteDelimiter() != ""
}

func (s *sqlScanner) skipDollarQuote() bool {
	delimiter := s.dollarQuoteDelimiter()
	if delimiter == "" {
		return false
	}
	s.pos += len(delimiter)
	end := strings.Index(s.sql[s.pos:], delimiter)
	if end < 0 {
		return false
	}
	s.pos += end + len(delimiter)
	return true
}

func (s *sqlScanner) dollarQuoteDelimiter() string {
	if s.pos >= len(s.sql) || s.sql[s.pos] != '$' {
		return ""
	}
	end := s.pos + 1
	for end < len(s.sql) && isDollarTagPart(s.sql[end]) {
		end++
	}
	if end < len(s.sql) && s.sql[end] == '$' {
		return s.sql[s.pos : end+1]
	}
	return ""
}

func (s *sqlScanner) skipIgnorable() bool {
	for {
		for s.pos < len(s.sql) && isSpace(s.sql[s.pos]) {
			s.pos++
		}
		if s.pos >= len(s.sql) || (s.sql[s.pos] != '-' && s.sql[s.pos] != '/') {
			return true
		}
		start := s.pos
		if !s.skipComment() {
			return false
		}
		if s.pos == start {
			return true
		}
	}
}

func (s *sqlScanner) skipComment() bool {
	if s.pos+1 >= len(s.sql) {
		return true
	}
	switch s.sql[s.pos : s.pos+2] {
	case "--":
		s.pos += 2
		for s.pos < len(s.sql) && s.sql[s.pos] != '\n' {
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

func (s *sqlScanner) peek() byte {
	if s.pos >= len(s.sql) {
		return 0
	}
	return s.sql[s.pos]
}

func (s *sqlScanner) failClosed() bool {
	return s.dialect == DialectPostgres
}

func isSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func isIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || ch >= '0' && ch <= '9' || ch == '$'
}

func isDollarTagPart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func firstKeyword(sql string, dialect Dialect) string {
	if dialect != DialectPostgres {
		return firstKeywordMySQL(sql)
	}
	scanner := sqlScanner{sql: sql, dialect: dialect}
	keyword, ok := scanner.readWord()
	if !ok {
		return ""
	}
	return keyword
}

func firstKeywordMySQL(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func hasWhereToken(sql string) bool {
	return whereRE.MatchString(sql)
}
