package sqlclass

import (
	"regexp"
	"strings"
)

type Kind string

const (
	KindInsert Kind = "insert"
	KindUpdate Kind = "update"
	KindDelete Kind = "delete"
)

var whereRE = regexp.MustCompile(`(?i)\bwhere\b`)

func IsReadOnly(sql string) bool {
	keyword := operativeKeyword(sql)
	switch keyword {
	case "select", "show", "describe", "desc", "explain":
		return true
	default:
		return false
	}
}

func ClassifyDML(sql string) (kind Kind, hasWhere bool, ok bool) {
	switch firstKeyword(sql) {
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

func HasMultipleStatements(sql string) bool {
	scanner := sqlScanner{sql: sql}
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
			if depth > 0 {
				depth--
			}
			scanner.pos++
		case ';':
			scanner.pos++
			if depth == 0 {
				if !scanner.skipIgnorable() {
					return true
				}
				return scanner.pos < len(scanner.sql)
			}
		case '-', '/':
			start := scanner.pos
			if !scanner.skipComment() {
				return false
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

func operativeKeyword(sql string) string {
	scanner := sqlScanner{sql: sql}
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
	sql string
	pos int
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
	if s.sql[s.pos] == '`' {
		return s.skipQuoted('`')
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
		case '(':
			depth++
			s.pos++
		case ')':
			depth--
			s.pos++
			if depth == 0 {
				return true
			}
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
	s.pos++
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case '\\':
			s.pos += 2
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

func firstKeyword(sql string) string {
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
