package sqlclass

import (
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

const maxReadOnlyNesting = 64

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
	return isReadOnlyStatement(sql, selectedDialect(dialects), 0)
}

func ClassifyDML(sql string, dialects ...Dialect) (kind Kind, hasWhere bool, ok bool) {
	dialect := selectedDialect(dialects)
	if dialect == DialectStrict || !isSingleWellFormedStatement(sql, dialect) {
		return "", false, false
	}
	words, ok := topLevelWords(sql, dialect)
	if !ok || len(words) == 0 {
		return "", false, false
	}
	switch words[0] {
	case "insert":
		return KindInsert, false, true
	case "update":
		setAt := wordIndex(words, "set", 1)
		if setAt < 0 {
			return "", false, false
		}
		return KindUpdate, wordIndex(words, "where", setAt+1) >= 0, true
	case "delete":
		fromAt := wordIndex(words, "from", 1)
		if fromAt < 0 {
			return "", false, false
		}
		return KindDelete, wordIndex(words, "where", fromAt+1) >= 0, true
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
		case '-', '/', '#':
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

func isReadOnlyStatement(sql string, dialect Dialect, nesting int) bool {
	if dialect == DialectStrict || nesting > maxReadOnlyNesting || !isSingleWellFormedStatement(sql, dialect) {
		return false
	}
	scanner := sqlScanner{sql: sql, dialect: dialect}
	keyword, ok := scanner.readWord()
	if !ok {
		return false
	}
	switch keyword {
	case "select":
		return !hasReadSideEffect(sql, dialect)
	case "show":
		return !hasReadSideEffect(sql, dialect)
	case "describe", "desc":
		if !scanner.skipIgnorable() || scanner.pos >= len(scanner.sql) {
			return false
		}
		if scanner.consumeWord("analyze") {
			return false
		}
		return !hasReadSideEffect(sql, dialect)
	case "explain":
		return isReadOnlyExplain(&scanner, nesting)
	case "with":
		return isReadOnlyWith(&scanner, nesting)
	default:
		return false
	}
}

func isReadOnlyWith(scanner *sqlScanner, nesting int) bool {
	scanner.consumeWord("recursive")
	for {
		if !scanner.skipIdentifier() {
			return false
		}
		if !scanner.skipIgnorable() {
			return false
		}
		if scanner.peek() == '(' && !scanner.skipBalanced() {
			return false
		}
		if !scanner.consumeWord("as") {
			return false
		}
		if !scanner.skipIgnorable() || scanner.peek() != '(' {
			return false
		}
		bodyStart := scanner.pos + 1
		if !scanner.skipBalanced() {
			return false
		}
		if !isReadOnlyStatement(scanner.sql[bodyStart:scanner.pos-1], scanner.dialect, nesting+1) {
			return false
		}
		if !scanner.skipIgnorable() {
			return false
		}
		if scanner.peek() != ',' {
			return isReadOnlyStatement(scanner.sql[scanner.pos:], scanner.dialect, nesting+1)
		}
		scanner.pos++
	}
}

func isReadOnlyExplain(scanner *sqlScanner, nesting int) bool {
	if !scanner.skipIgnorable() {
		return false
	}
	if scanner.peek() == '(' {
		optionStart := scanner.pos + 1
		if !scanner.skipBalanced() {
			return false
		}
		if containsUnquotedWord(scanner.sql[optionStart:scanner.pos-1], scanner.dialect, "analyze") {
			return false
		}
	}
	for {
		start := scanner.pos
		word, ok := scanner.readWord()
		if !ok {
			return false
		}
		switch word {
		case "analyze":
			return false
		case "verbose", "extended", "partitions":
			continue
		case "format":
			if !scanner.skipIgnorable() {
				return false
			}
			if scanner.peek() == '=' {
				scanner.pos++
			}
			if _, ok := scanner.readWord(); !ok {
				return false
			}
		default:
			return isReadOnlyStatement(scanner.sql[start:], scanner.dialect, nesting+1)
		}
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

func (s *sqlScanner) readPotentialIdentifier() (string, bool) {
	if !s.skipIgnorable() || s.pos >= len(s.sql) || !isPotentialIdentifierStart(s.sql[s.pos]) {
		return "", false
	}
	start := s.pos
	s.pos++
	for s.pos < len(s.sql) && isPotentialIdentifierPart(s.sql[s.pos]) {
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
	if !isPotentialIdentifierStart(s.sql[s.pos]) {
		return false
	}
	_, ok := s.readPotentialIdentifier()
	return ok
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
		case '-', '/', '#':
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
	backslashEscapes := s.dialect != DialectPostgres || quote == '\'' && s.isPostgresEscapeString()
	s.pos++
	for s.pos < len(s.sql) {
		switch s.sql[s.pos] {
		case '\\':
			if s.dialect == DialectMySQL || s.dialect == DialectPostgres && quote == '\'' && !backslashEscapes {
				return false
			}
			if backslashEscapes {
				s.pos += 2
			} else {
				s.pos++
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

func (s *sqlScanner) isPostgresEscapeString() bool {
	if s.pos == 0 || s.sql[s.pos-1] != 'e' && s.sql[s.pos-1] != 'E' {
		return false
	}
	return s.pos == 1 || !isIdentifierPart(s.sql[s.pos-2])
}

func (s *sqlScanner) readQuotedIdentifier(quote byte) (string, bool) {
	if s.peek() != quote {
		return "", false
	}
	s.pos++
	var value strings.Builder
	for s.pos < len(s.sql) {
		if s.sql[s.pos] != quote {
			if s.dialect == DialectMySQL && s.sql[s.pos] == '\\' {
				return "", false
			}
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
}

func (s *sqlScanner) readIdentifierToken() (string, bool) {
	if !s.skipIgnorable() {
		return "", false
	}
	switch s.peek() {
	case '"':
		if s.dialect == DialectPostgres || s.dialect == DialectMySQL {
			return s.readQuotedIdentifier('"')
		}
	case '`':
		if s.dialect == DialectMySQL {
			return s.readQuotedIdentifier('`')
		}
	}
	return s.readPotentialIdentifier()
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
	if s.pos > 0 && !isDollarQuoteBoundary(s.sql[s.pos-1]) {
		return ""
	}
	end := s.pos + 1
	if end < len(s.sql) && s.sql[end] == '$' {
		return s.sql[s.pos : end+1]
	}
	if end >= len(s.sql) || !isDollarTagStart(s.sql[end]) {
		return ""
	}
	end++
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
		if s.pos >= len(s.sql) || (s.sql[s.pos] != '-' && s.sql[s.pos] != '/' && s.sql[s.pos] != '#') {
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

//nolint:gocyclo // MySQL line comments and PostgreSQL nested block comments require dialect-specific state.
func (s *sqlScanner) skipComment() bool {
	if s.pos < len(s.sql) && s.sql[s.pos] == '#' {
		if s.dialect != DialectMySQL {
			return true
		}
		s.pos++
		for s.pos < len(s.sql) && !isLineTerminator(s.sql[s.pos]) {
			s.pos++
		}
		return true
	}
	if s.pos+1 >= len(s.sql) {
		return true
	}
	switch s.sql[s.pos : s.pos+2] {
	case "--":
		if s.dialect == DialectMySQL && s.pos+2 < len(s.sql) && !isMySQLCommentSpace(s.sql[s.pos+2]) {
			return true
		}
		s.pos += 2
		for s.pos < len(s.sql) && !isLineTerminator(s.sql[s.pos]) {
			s.pos++
		}
		return true
	case "/*":
		s.pos += 2
		depth := 1
		for s.pos < len(s.sql) {
			if s.dialect == DialectPostgres && s.pos+1 < len(s.sql) && s.sql[s.pos:s.pos+2] == "/*" {
				depth++
				s.pos += 2
				continue
			}
			if s.pos+1 < len(s.sql) && s.sql[s.pos:s.pos+2] == "*/" {
				depth--
				s.pos += 2
				if depth == 0 {
					return true
				}
				continue
			}
			s.pos++
		}
		return false
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
	return true
}

//nolint:gocyclo // A single lexical pass is easier to audit than several partially overlapping SQL validators.
func isSingleWellFormedStatement(sql string, dialect Dialect) bool {
	if dialect == DialectStrict {
		return false
	}
	scanner := sqlScanner{sql: sql, dialect: dialect}
	depth := 0
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return false
			}
		case '`':
			if dialect == DialectPostgres || !scanner.skipQuoted('`') {
				return false
			}
		case '$':
			if dialect == DialectPostgres && scanner.hasDollarQuote() {
				if !scanner.skipDollarQuote() {
					return false
				}
				continue
			}
			scanner.pos++
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
			if depth != 0 {
				return false
			}
			scanner.pos++
			if !scanner.skipIgnorable() {
				return false
			}
			return scanner.pos == len(scanner.sql)
		case '-', '/', '#':
			if dialect == DialectMySQL && isMySQLExecutableComment(scanner.sql[scanner.pos:]) {
				return false
			}
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
	return depth == 0
}

//nolint:gocyclo // Quote, comment, and nesting state must be considered together for trustworthy top-level tokens.
func topLevelWords(sql string, dialect Dialect) ([]string, bool) {
	scanner := sqlScanner{sql: sql, dialect: dialect}
	var words []string
	depth := 0
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"', '`':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return nil, false
			}
		case '$':
			if dialect == DialectPostgres && scanner.hasDollarQuote() {
				if !scanner.skipDollarQuote() {
					return nil, false
				}
				continue
			}
			scanner.pos++
		case '(':
			depth++
			scanner.pos++
		case ')':
			depth--
			scanner.pos++
		case '-', '/', '#':
			start := scanner.pos
			if !scanner.skipComment() {
				return nil, false
			}
			if scanner.pos == start {
				scanner.pos++
			}
		default:
			if depth == 0 && isIdentifierStart(scanner.sql[scanner.pos]) {
				word, ok := scanner.readWord()
				if !ok {
					return nil, false
				}
				words = append(words, word)
				continue
			}
			scanner.pos++
		}
	}
	return words, true
}

func hasReadSideEffect(sql string, dialect Dialect) bool {
	if hasUnsafeReadConstruct(sql, dialect) {
		return true
	}
	words, ok := topLevelWords(sql, dialect)
	if !ok {
		return true
	}
	for _, word := range words {
		if word == "into" {
			return true
		}
	}
	return false
}

type precedingSQLToken struct {
	word   string
	symbol byte
}

func (token precedingSQLToken) isWord(words ...string) bool {
	for _, word := range words {
		if token.word == word {
			return true
		}
	}
	return false
}

func (token precedingSQLToken) isComparisonOperator() bool {
	switch token.symbol {
	case '=', '<', '>', '!':
		return true
	default:
		return false
	}
}

//nolint:gocyclo // Lock clauses and function calls must share one quote/comment-aware pass at every nesting depth.
func hasUnsafeReadConstruct(sql string, dialect Dialect) bool {
	scanner := sqlScanner{sql: sql, dialect: dialect}
	var previous precedingSQLToken
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'':
			if !scanner.skipQuoted('\'') {
				return true
			}
			previous = precedingSQLToken{symbol: '\''}
		case '"':
			if dialect == DialectPostgres {
				name, ok := scanner.readQuotedIdentifier('"')
				if !ok ||
					isUnsafeFunctionCall(name, &scanner, dialect, true, previous) ||
					isUnsafeSequenceConstruct(name, &scanner, dialect) {
					return true
				}
			} else {
				quoted := scanner
				if name, ok := quoted.readQuotedIdentifier('"'); ok &&
					(isUnsafeFunctionCall(name, &quoted, dialect, true, previous) ||
						isUnsafeSequenceConstruct(name, &quoted, dialect)) {
					return true
				}
				if !scanner.skipQuoted('"') {
					return true
				}
			}
			previous = precedingSQLToken{symbol: '"'}
		case '`':
			if dialect != DialectMySQL {
				return true
			}
			name, ok := scanner.readQuotedIdentifier('`')
			if !ok ||
				isUnsafeFunctionCall(name, &scanner, dialect, true, previous) ||
				isUnsafeSequenceConstruct(name, &scanner, dialect) {
				return true
			}
			previous = precedingSQLToken{symbol: '`'}
		case '$':
			if dialect == DialectPostgres && scanner.hasDollarQuote() {
				if !scanner.skipDollarQuote() {
					return true
				}
				previous = precedingSQLToken{symbol: '\''}
				continue
			}
			word, ok := scanner.readPotentialIdentifier()
			if !ok ||
				isUnsafeLockClause(word, &scanner, dialect) ||
				isUnsafeSequenceConstruct(word, &scanner, dialect) ||
				isUnsafeFunctionCall(word, &scanner, dialect, false, previous) {
				return true
			}
			previous = precedingSQLToken{word: word}
		case ':':
			if dialect == DialectMySQL &&
				scanner.pos+1 < len(scanner.sql) &&
				scanner.sql[scanner.pos+1] == '=' {
				return true
			}
			scanner.pos++
			previous = precedingSQLToken{symbol: ':'}
		case '-', '/', '#':
			start := scanner.pos
			if !scanner.skipComment() {
				return true
			}
			if scanner.pos == start {
				scanner.pos++
			}
		default:
			if isSpace(scanner.sql[scanner.pos]) {
				scanner.pos++
				continue
			}
			if !isPotentialIdentifierStart(scanner.sql[scanner.pos]) {
				previous = precedingSQLToken{symbol: scanner.sql[scanner.pos]}
				scanner.pos++
				continue
			}
			word, ok := scanner.readPotentialIdentifier()
			if !ok ||
				isPostgresUnicodeQuotedIdentifier(word, &scanner, dialect) ||
				isUnsafeLockClause(word, &scanner, dialect) ||
				isUnsafeSequenceConstruct(word, &scanner, dialect) ||
				isUnsafeFunctionCall(word, &scanner, dialect, false, previous) {
				return true
			}
			previous = precedingSQLToken{word: word}
		}
	}
	return false
}

func isPostgresUnicodeQuotedIdentifier(word string, scanner *sqlScanner, dialect Dialect) bool {
	return dialect == DialectPostgres &&
		word == "u" &&
		scanner.pos+1 < len(scanner.sql) &&
		scanner.sql[scanner.pos] == '&' &&
		scanner.sql[scanner.pos+1] == '"'
}

//nolint:gocyclo // Each supported locking clause is explicit so an unknown token never broadens the R0 path.
func isUnsafeLockClause(word string, scanner *sqlScanner, dialect Dialect) bool {
	lookahead := *scanner
	next, ok := lookahead.readWord()
	if !ok {
		return false
	}
	switch word {
	case "for":
		switch next {
		case "share", "update":
			return true
		case "key":
			afterKey, ok := lookahead.readWord()
			return ok && afterKey == "share"
		case "no":
			key, keyOK := lookahead.readWord()
			update, updateOK := lookahead.readWord()
			return keyOK && updateOK && key == "key" && update == "update"
		}
	case "lock":
		if dialect != DialectMySQL || next != "in" {
			return false
		}
		share, shareOK := lookahead.readWord()
		mode, modeOK := lookahead.readWord()
		return shareOK && modeOK && share == "share" && mode == "mode"
	}
	return false
}

func isUnsafeSequenceConstruct(word string, scanner *sqlScanner, dialect Dialect) bool {
	if dialect != DialectMySQL {
		return false
	}
	lookahead := *scanner
	if word == "next" {
		value, valueOK := lookahead.readWord()
		forWord, forOK := lookahead.readWord()
		return valueOK && forOK && value == "value" && forWord == "for"
	}
	for {
		if !lookahead.skipIgnorable() || lookahead.peek() != '.' {
			return false
		}
		lookahead.pos++
		next, ok := lookahead.readIdentifierToken()
		if !ok {
			return false
		}
		if strings.EqualFold(next, "nextval") {
			return true
		}
	}
}

//nolint:gocyclo // Function qualification, quoted-identifier semantics, and dialect syntax must fail closed together.
func isUnsafeFunctionCall(
	firstName string,
	scanner *sqlScanner,
	dialect Dialect,
	firstQuoted bool,
	previous precedingSQLToken,
) bool {
	lookahead := *scanner
	names := []string{firstName}
	quoted := []bool{firstQuoted}
	for {
		if !lookahead.skipIgnorable() {
			return true
		}
		if lookahead.peek() != '.' {
			break
		}
		lookahead.pos++
		if !lookahead.skipIgnorable() {
			return true
		}
		nextQuoted := lookahead.peek() == '"' || lookahead.peek() == '`'
		next, ok := lookahead.readIdentifierToken()
		if !ok {
			return true
		}
		names = append(names, next)
		quoted = append(quoted, nextQuoted)
	}
	if !lookahead.skipIgnorable() || lookahead.peek() != '(' {
		return false
	}
	name := names[len(names)-1]
	if isUnsafeReadFunction(name, dialect) {
		return true
	}
	finalQuoted := quoted[len(quoted)-1]
	if dialect == DialectPostgres && finalQuoted && name != strings.ToLower(name) {
		return true
	}
	if len(names) == 1 {
		if !finalQuoted && isReadOnlySQLSyntax(name, dialect, previous, lookahead) {
			return false
		}
		if finalQuoted || dialect != DialectMySQL || !isKnownMySQLReadOnlyFunction(name) {
			return true
		}
		return false
	}
	if dialect != DialectPostgres ||
		len(names) != 2 ||
		names[0] != "pg_catalog" ||
		!isKnownPostgresReadOnlyFunction(name) {
		return true
	}
	scanner.pos = lookahead.pos
	return false
}

func isUnsafeReadFunction(name string, dialect Dialect) bool {
	name = strings.ToLower(name)
	switch dialect {
	case DialectPostgres:
		if hasUnsafeFunctionPrefix(name,
			"autoprewarm_",
			"dblink_",
			"heap_force_",
			"lo_",
			"pg_buffercache_evict",
			"pg_file_",
			"pg_logical_slot_",
			"pg_replication_origin_",
			"pg_stat_reset",
			"pg_wal_replay_",
			"pg_xlog_replay_",
			"postgres_fdw_disconnect",
		) {
			return true
		}
		switch name {
		case "brin_desummarize_range",
			"brin_summarize_new_values",
			"brin_summarize_range",
			"dblink",
			"gin_clean_pending_list",
			"lowrite",
			"nextval",
			"pg_backup_start",
			"pg_backup_stop",
			"pg_advisory_lock",
			"pg_advisory_lock_shared",
			"pg_advisory_unlock",
			"pg_advisory_unlock_all",
			"pg_advisory_unlock_shared",
			"pg_advisory_xact_lock",
			"pg_advisory_xact_lock_shared",
			"pg_cancel_backend",
			"pg_checkpoint",
			"pg_clear_attribute_stats",
			"pg_clear_relation_stats",
			"pg_copy_logical_replication_slot",
			"pg_copy_physical_replication_slot",
			"pg_create_logical_replication_slot",
			"pg_create_physical_replication_slot",
			"pg_create_restore_point",
			"pg_current_xact_id",
			"pg_drop_replication_slot",
			"pg_export_snapshot",
			"pg_import_system_collations",
			"pg_log_backend_memory_contexts",
			"pg_log_standby_snapshot",
			"pg_logical_emit_message",
			"pg_notify",
			"pg_prewarm",
			"pg_promote",
			"pg_reload_conf",
			"pg_replication_slot_advance",
			"pg_restore_attribute_stats",
			"pg_restore_relation_stats",
			"pg_rotate_logfile",
			"pg_sleep",
			"pg_sleep_for",
			"pg_sleep_until",
			"pg_start_backup",
			"pg_stat_clear_snapshot",
			"pg_stat_force_next_flush",
			"pg_stat_statements_reset",
			"pg_stop_backup",
			"pg_switch_wal",
			"pg_switch_xlog",
			"pg_sync_replication_slots",
			"pg_terminate_backend",
			"pg_truncate_visibility_map",
			"pg_try_advisory_lock",
			"pg_try_advisory_lock_shared",
			"pg_try_advisory_xact_lock",
			"pg_try_advisory_xact_lock_shared",
			"set_config",
			"setseed",
			"setval",
			"txid_current":
			return true
		}
	case DialectMySQL:
		if hasUnsafeFunctionPrefix(name,
			"asynchronous_connection_failover_",
			"audit_log_",
			"group_replication_",
			"keyring_",
			"version_tokens_",
		) {
			return true
		}
		switch name {
		case "benchmark",
			"get_lock",
			"last_insert_id",
			"load_file",
			"master_pos_wait",
			"master_gtid_wait",
			"nextval",
			"ps_kill",
			"release_all_locks",
			"release_lock",
			"service_get_read_locks",
			"service_get_write_locks",
			"service_release_locks",
			"setval",
			"sleep",
			"source_pos_wait",
			"sys_eval",
			"sys_exec",
			"wait_for_executed_gtid_set",
			"wait_until_sql_thread_after_gtids":
			return true
		}
	case DialectStrict:
		return true
	}
	return false
}

func isReadOnlySQLSyntax(
	name string,
	dialect Dialect,
	previous precedingSQLToken,
	scanner sqlScanner,
) bool {
	switch dialect {
	case DialectPostgres:
		switch name {
		case "all", "and", "any", "array", "as", "bigint", "bit", "boolean",
			"case", "cast", "char", "character", "coalesce", "date", "decimal",
			"distinct", "else", "except", "exists", "extract", "float", "from",
			"greatest", "group", "grouping", "having", "in", "int", "integer",
			"intersect", "interval", "json", "lateral", "least", "limit",
			"normalize", "not", "nullif", "numeric", "offset", "on", "or",
			"order", "overlay", "position", "real", "row", "select", "smallint",
			"some", "substring", "table", "then", "time", "timestamp", "trim",
			"union", "using", "varchar", "when", "where", "xmlattributes",
			"xmlconcat", "xmlelement", "xmlforest", "xmlparse", "xmlpi",
			"xmlroot", "xmlserialize":
			return true
		case "by":
			return previous.isWord("group", "order", "partition")
		case "cube", "rollup":
			return previous.isWord("by")
		case "filter":
			return previous.symbol == ')' && parenthesizedStartsWith(scanner, "where")
		case "join":
			return parenthesizedStartsWith(scanner, "select", "values", "with")
		case "over":
			return previous.symbol == ')'
		case "sets":
			return previous.isWord("grouping")
		}
	case DialectMySQL:
		switch name {
		case "all", "and", "as", "bigint", "binary", "by", "case", "cast",
			"char", "character", "convert", "cube", "decimal", "distinct",
			"double", "else", "except", "exists", "explain", "extract", "float",
			"from", "group", "grouping", "having", "in", "int", "integer",
			"intersect", "interval", "join", "lateral", "limit", "not",
			"numeric", "on", "or", "order", "over", "real", "row", "select",
			"smallint", "table", "then", "union", "unsigned", "using",
			"varchar", "when", "where":
			return true
		case "any", "some":
			return previous.isComparisonOperator()
		}
	case DialectStrict:
		return false
	}
	return false
}

func parenthesizedStartsWith(scanner sqlScanner, words ...string) bool {
	if scanner.peek() != '(' {
		return false
	}
	scanner.pos++
	word, ok := scanner.readWord()
	if !ok {
		return false
	}
	for _, want := range words {
		if word == want {
			return true
		}
	}
	return false
}

func isKnownPostgresReadOnlyFunction(name string) bool {
	switch name {
	case "abs", "acos", "age", "array_agg", "array_append", "array_cat",
		"array_dims", "array_fill", "array_length", "array_lower", "array_ndims",
		"array_position", "array_positions", "array_prepend", "array_remove",
		"array_replace", "array_to_json", "array_to_string", "array_upper",
		"ascii", "asin", "atan", "atan2", "avg", "bit_and", "bit_length",
		"bit_or", "bool_and", "bool_or", "btrim", "cardinality", "cbrt",
		"ceil", "ceiling", "char_length", "character_length", "chr",
		"clock_timestamp", "coalesce", "col_description", "concat", "concat_ws",
		"convert_from", "convert_to", "corr", "cos", "cot", "count",
		"covar_pop", "covar_samp", "current_database", "current_schema",
		"current_schemas", "currval", "date_bin", "date_part", "date_trunc",
		"decode", "degrees", "dense_rank", "div", "encode", "every", "exp",
		"factorial", "first_value", "floor", "format", "format_type", "gcd",
		"generate_series", "get_bit", "get_byte", "greatest", "has_any_column_privilege",
		"has_column_privilege", "has_database_privilege", "has_foreign_data_wrapper_privilege",
		"has_function_privilege", "has_language_privilege", "has_parameter_privilege",
		"has_schema_privilege", "has_sequence_privilege", "has_server_privilege",
		"has_table_privilege", "has_tablespace_privilege", "has_type_privilege",
		"initcap", "isfinite", "json_agg", "json_array_length",
		"json_build_array", "json_build_object", "json_each", "json_each_text",
		"json_extract_path", "json_extract_path_text", "json_object",
		"json_object_agg", "json_object_keys", "json_populate_record",
		"json_populate_recordset", "json_strip_nulls", "json_to_record",
		"json_to_recordset", "json_typeof", "jsonb_agg", "jsonb_array_elements",
		"jsonb_array_elements_text", "jsonb_array_length", "jsonb_build_array",
		"jsonb_build_object", "jsonb_each", "jsonb_each_text",
		"jsonb_extract_path", "jsonb_extract_path_text", "jsonb_insert",
		"jsonb_object", "jsonb_object_agg", "jsonb_object_keys",
		"jsonb_path_exists", "jsonb_path_match", "jsonb_path_query",
		"jsonb_path_query_array", "jsonb_path_query_first", "jsonb_populate_record",
		"jsonb_populate_recordset", "jsonb_pretty", "jsonb_set",
		"jsonb_set_lax", "jsonb_strip_nulls", "jsonb_to_record",
		"jsonb_to_recordset", "jsonb_typeof", "justify_days", "justify_hours",
		"justify_interval", "lag", "last_value", "lastval", "lcm", "lead",
		"least", "left", "length", "ln", "log", "log10", "lower", "lpad",
		"ltrim", "make_date", "make_interval", "make_time", "make_timestamp",
		"make_timestamptz", "max", "md5", "min", "min_scale", "mod",
		"now", "nth_value", "ntile", "nullif", "obj_description",
		"octet_length", "parse_ident", "percent_rank", "pg_backend_pid",
		"pg_client_encoding", "pg_column_size", "pg_conf_load_time",
		"pg_current_logfile", "pg_current_wal_flush_lsn", "pg_current_wal_insert_lsn",
		"pg_current_wal_lsn", "pg_database_size", "pg_get_constraintdef",
		"pg_get_expr", "pg_get_function_arguments", "pg_get_function_result",
		"pg_get_functiondef", "pg_get_indexdef", "pg_get_keywords",
		"pg_get_ruledef", "pg_get_serial_sequence", "pg_get_statisticsobjdef",
		"pg_get_triggerdef", "pg_get_userbyid", "pg_get_viewdef",
		"pg_indexes_size", "pg_is_in_recovery", "pg_last_wal_receive_lsn",
		"pg_last_wal_replay_lsn", "pg_last_xact_replay_timestamp",
		"pg_postmaster_start_time", "pg_relation_size", "pg_size_bytes",
		"pg_size_pretty", "pg_table_is_visible", "pg_table_size",
		"pg_tablespace_size", "pg_total_relation_size", "pg_type_is_visible",
		"pg_typeof", "pi", "power", "quote_ident", "quote_literal",
		"quote_nullable", "radians", "random", "rank", "regexp_count",
		"regexp_instr", "regexp_like", "regexp_match", "regexp_matches",
		"regexp_replace", "regexp_split_to_array", "regexp_split_to_table",
		"regr_avgx", "regr_avgy", "regr_count", "regr_intercept", "regr_r2",
		"regr_slope", "regr_sxx", "regr_sxy", "regr_syy", "repeat",
		"replace", "reverse", "right", "round", "row_number", "row_to_json",
		"rpad", "rtrim", "scale", "session_user", "set_bit", "set_byte",
		"sign", "split_part", "sqrt", "statement_timestamp", "stddev",
		"stddev_pop", "stddev_samp", "string_agg", "string_to_array",
		"strpos", "substr", "sum", "timeofday", "to_ascii", "to_char",
		"to_date", "to_hex", "to_json", "to_jsonb", "to_number",
		"to_regclass", "to_regcollation", "to_regnamespace", "to_regoper",
		"to_regoperator", "to_regproc", "to_regprocedure", "to_regrole",
		"to_regtype", "to_timestamp", "transaction_timestamp", "translate",
		"trim_array", "trim_scale", "trunc", "txid_current_if_assigned",
		"txid_current_snapshot", "unnest", "upper", "var_pop", "var_samp",
		"variance", "version", "width_bucket", "xmlagg":
		return true
	}
	return false
}

func isKnownMySQLReadOnlyFunction(name string) bool {
	switch name {
	case "abs", "acos", "adddate", "addtime", "aes_decrypt", "aes_encrypt",
		"any_value", "ascii", "asin", "atan", "atan2", "avg", "bin",
		"bin_to_uuid", "bit_and", "bit_count", "bit_length", "bit_or",
		"bit_xor", "ceil", "ceiling", "char", "char_length",
		"character_length", "coalesce", "compress", "concat", "concat_ws",
		"connection_id", "conv", "convert_tz", "cos", "cot", "count",
		"crc32", "curdate", "current_date", "current_role", "current_time",
		"current_timestamp", "current_user", "curtime", "database", "date",
		"date_add", "date_format", "date_sub", "datediff", "day", "dayname",
		"dayofmonth", "dayofweek", "dayofyear", "degrees", "dense_rank",
		"elt", "exp", "export_set", "extract", "field", "find_in_set",
		"first_value", "floor", "format", "from_base64", "from_days",
		"from_unixtime", "get_format", "greatest", "group_concat", "hex",
		"hour", "if", "ifnull", "inet6_aton", "inet6_ntoa", "inet_aton",
		"inet_ntoa", "insert", "instr", "is_ipv4", "is_ipv4_compat",
		"is_ipv4_mapped", "is_ipv6", "is_uuid", "isnull", "json_array",
		"json_array_append", "json_array_insert", "json_arrayagg",
		"json_contains", "json_contains_path", "json_depth", "json_extract",
		"json_insert", "json_keys", "json_length", "json_merge_patch",
		"json_merge_preserve", "json_object", "json_objectagg", "json_overlaps",
		"json_pretty", "json_quote", "json_remove", "json_replace",
		"json_schema_valid", "json_schema_validation_report", "json_search",
		"json_set", "json_storage_free", "json_storage_size", "json_table",
		"json_type", "json_unquote", "json_valid", "lag", "last_day",
		"last_value", "lcase", "lead", "least", "left", "length", "ln",
		"localtime", "localtimestamp", "locate", "log", "log10", "log2",
		"lower", "lpad", "ltrim", "makedate", "maketime", "make_set",
		"max", "md5", "microsecond", "mid", "min", "minute", "mod",
		"month", "monthname", "name_const", "now", "nth_value", "ntile",
		"nullif", "oct", "octet_length", "ord", "percent_rank", "period_add",
		"period_diff", "pi", "position", "pow", "power", "quarter", "quote",
		"radians", "rand", "random_bytes", "rank", "regexp_instr",
		"regexp_like", "regexp_replace", "regexp_substr", "repeat", "replace",
		"reverse", "right", "round", "row_count", "row_number", "rpad",
		"rtrim", "schema", "sec_to_time", "second", "session_user", "sha",
		"sha1", "sha2", "sign", "sin", "soundex", "space", "sqrt",
		"std", "stddev", "stddev_pop", "stddev_samp", "strcmp", "str_to_date",
		"subdate", "substr", "substring", "substring_index", "subtime", "sum",
		"sysdate", "system_user", "tan", "time", "timediff", "timestamp",
		"timestampadd", "timestampdiff", "time_format", "time_to_sec",
		"to_base64", "to_days", "to_seconds", "trim", "truncate",
		"ucase", "uncompress", "uncompressed_length", "unhex", "unix_timestamp",
		"upper", "user", "utc_date", "utc_time", "utc_timestamp", "uuid",
		"uuid_to_bin", "var_pop", "var_samp", "variance", "version", "week",
		"weekday", "weekofyear", "year", "yearweek":
		return true
	}
	return false
}

func hasUnsafeFunctionPrefix(name string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func wordIndex(words []string, want string, start int) int {
	for index := start; index < len(words); index++ {
		if words[index] == want {
			return index
		}
	}
	return -1
}

//nolint:gocyclo // EXPLAIN options need the same quote and comment awareness as statement classification.
func containsUnquotedWord(sql string, dialect Dialect, want string) bool {
	scanner := sqlScanner{sql: sql, dialect: dialect}
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'', '"', '`':
			if !scanner.skipQuoted(scanner.sql[scanner.pos]) {
				return true
			}
		case '$':
			if dialect == DialectPostgres && scanner.hasDollarQuote() {
				if !scanner.skipDollarQuote() {
					return true
				}
				continue
			}
			scanner.pos++
		case '-', '/', '#':
			start := scanner.pos
			if !scanner.skipComment() {
				return true
			}
			if scanner.pos == start {
				scanner.pos++
			}
		default:
			if isIdentifierStart(scanner.sql[scanner.pos]) {
				word, ok := scanner.readWord()
				if ok && word == want {
					return true
				}
				continue
			}
			scanner.pos++
		}
	}
	return false
}

func isSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func isLineTerminator(ch byte) bool {
	return ch == '\n' || ch == '\r'
}

func isIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || ch >= '0' && ch <= '9' || ch == '$'
}

func isPotentialIdentifierStart(ch byte) bool {
	return isIdentifierStart(ch) || ch >= '0' && ch <= '9' || ch == '$' || ch >= 0x80
}

func isPotentialIdentifierPart(ch byte) bool {
	return isPotentialIdentifierStart(ch)
}

func isDollarTagPart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func isDollarTagStart(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isDollarQuoteBoundary(ch byte) bool {
	if isSpace(ch) {
		return true
	}
	switch ch {
	case '(', ',', ';', '=', '+', '-', '*', '/', '%', '<', '>', '!', '~', '^', '|', '&', ':', '[':
		return true
	default:
		return false
	}
}

func isMySQLCommentSpace(ch byte) bool {
	return ch <= ' ' || ch == 0x7f
}

func isMySQLExecutableComment(sql string) bool {
	return strings.HasPrefix(sql, "/*!") || strings.HasPrefix(sql, "/*M!") || strings.HasPrefix(sql, "/*m!")
}
