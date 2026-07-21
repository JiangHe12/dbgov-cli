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
		return true
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
	return s.readWord()
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

//nolint:gocyclo // Lock clauses and function calls must share one quote/comment-aware pass at every nesting depth.
func hasUnsafeReadConstruct(sql string, dialect Dialect) bool {
	scanner := sqlScanner{sql: sql, dialect: dialect}
	for scanner.pos < len(scanner.sql) {
		switch scanner.sql[scanner.pos] {
		case '\'':
			if !scanner.skipQuoted('\'') {
				return true
			}
		case '"':
			if dialect == DialectPostgres {
				name, ok := scanner.readQuotedIdentifier('"')
				if !ok ||
					isUnsafeFunctionCall(name, &scanner, dialect) ||
					isUnsafeSequenceConstruct(name, &scanner, dialect) {
					return true
				}
			} else {
				quoted := scanner
				if name, ok := quoted.readQuotedIdentifier('"'); ok &&
					(isUnsafeFunctionCall(name, &quoted, dialect) ||
						isUnsafeSequenceConstruct(name, &quoted, dialect)) {
					return true
				}
				if !scanner.skipQuoted('"') {
					return true
				}
			}
		case '`':
			if dialect != DialectMySQL {
				return true
			}
			name, ok := scanner.readQuotedIdentifier('`')
			if !ok ||
				isUnsafeFunctionCall(name, &scanner, dialect) ||
				isUnsafeSequenceConstruct(name, &scanner, dialect) {
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
		case ':':
			if dialect == DialectMySQL &&
				scanner.pos+1 < len(scanner.sql) &&
				scanner.sql[scanner.pos+1] == '=' {
				return true
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
			if !isIdentifierStart(scanner.sql[scanner.pos]) {
				scanner.pos++
				continue
			}
			word, ok := scanner.readWord()
			if !ok ||
				isPostgresUnicodeQuotedIdentifier(word, &scanner, dialect) ||
				isUnsafeLockClause(word, &scanner, dialect) ||
				isUnsafeSequenceConstruct(word, &scanner, dialect) ||
				isUnsafeFunctionCall(word, &scanner, dialect) {
				return true
			}
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

func isUnsafeFunctionCall(firstName string, scanner *sqlScanner, dialect Dialect) bool {
	lookahead := *scanner
	name := firstName
	for {
		if !lookahead.skipIgnorable() {
			return true
		}
		if lookahead.peek() != '.' {
			break
		}
		lookahead.pos++
		next, ok := lookahead.readIdentifierToken()
		if !ok {
			return false
		}
		name = next
	}
	if !lookahead.skipIgnorable() || lookahead.peek() != '(' {
		return false
	}
	return isUnsafeReadFunction(name, dialect)
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
