package sqlclass

import "testing"

func TestIsReadOnly(t *testing.T) {
	readOnly := []string{
		"SELECT * FROM users",
		" show tables",
		"DESCRIBE users",
		"desc users",
		"EXPLAIN SELECT * FROM users",
	}
	for _, sql := range readOnly {
		if !IsReadOnly(sql) {
			t.Fatalf("IsReadOnly(%q) = false", sql)
		}
	}
	writes := []string{
		"UPDATE users SET name='x'",
		"DELETE FROM users",
		"INSERT INTO users VALUES (1)",
		"ALTER TABLE users ADD COLUMN age INT",
		"",
	}
	for _, sql := range writes {
		if IsReadOnly(sql) {
			t.Fatalf("IsReadOnly(%q) = true", sql)
		}
	}
}

func TestIsReadOnlyRecognizesCTEOperativeKeyword(t *testing.T) {
	readOnly := []string{
		"WITH RECURSIVE t AS (SELECT 1 UNION ALL SELECT n+1 FROM t WHERE n<5) SELECT * FROM t",
		"WITH c AS (SELECT * FROM x WHERE id=1) SELECT * FROM c",
		"WITH a AS (SELECT (1 + (2))), b(v) AS (SELECT ')' /* delete */) SELECT * FROM a",
		" \n/* leading */ WiTh ReCuRsIvE `t` (`n`) AS (\nSELECT 1 -- keep reading\n) /* between */ SeLeCt * FROM `t`",
	}
	for _, sql := range readOnly {
		if !IsReadOnly(sql) {
			t.Errorf("IsReadOnly(%q) = false", sql)
		}
	}

	writes := []string{
		"WITH x AS (SELECT 1) DELETE FROM users",
		"WITH x AS (SELECT 1) UPDATE users SET a=1",
		"WITH x AS (SELECT 1) INSERT INTO t VALUES(1)",
		"WITH x AS (SELECT ')', 'delete')",
		"WITH x AS (SELECT 1",
	}
	for _, sql := range writes {
		if IsReadOnly(sql) {
			t.Errorf("IsReadOnly(%q) = true", sql)
		}
	}
}

func TestIsReadOnlyRejectsExecutingAndDataModifyingForms(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		dialect Dialect
	}{
		{
			name:    "postgres explain analyze delete",
			sql:     "EXPLAIN ANALYZE DELETE FROM users",
			dialect: DialectPostgres,
		},
		{
			name:    "postgres explain analyze option delete",
			sql:     "EXPLAIN (ANALYZE TRUE, FORMAT JSON) DELETE FROM users",
			dialect: DialectPostgres,
		},
		{
			name:    "mysql describe analyze",
			sql:     "DESCRIBE ANALYZE SELECT GET_LOCK('dbgov', 10)",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql desc analyze",
			sql:     "DESC /* execute */ ANALYZE SELECT SLEEP(1)",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql describe query plan with lock function",
			sql:     "DESCRIBE SELECT * FROM users, (SELECT GET_LOCK('dbgov', 10)) AS locked",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql desc query plan with sleep",
			sql:     "DESC SELECT * FROM users, (SELECT SLEEP(30)) AS delayed",
			dialect: DialectMySQL,
		},
		{
			name:    "postgres data modifying cte",
			sql:     "WITH gone AS (DELETE FROM users RETURNING *) SELECT count(*) FROM gone",
			dialect: DialectPostgres,
		},
		{
			name:    "mysql executable version comment",
			sql:     "SELECT 1 /*!80000 INTO OUTFILE '/tmp/dbgov-test' */",
			dialect: DialectMySQL,
		},
		{
			name:    "mariadb executable version comment",
			sql:     "SELECT 1 /*M!100100 INTO OUTFILE '/tmp/dbgov-test' */",
			dialect: DialectMySQL,
		},
		{
			name:    "postgres select into",
			sql:     "SELECT id INTO archived_users FROM users",
			dialect: DialectPostgres,
		},
		{
			name:    "mysql select into outfile",
			sql:     "SELECT * FROM users INTO OUTFILE '/tmp/users.csv'",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql select into dumpfile",
			sql:     "SELECT payload INTO DUMPFILE '/tmp/users.bin' FROM users",
			dialect: DialectMySQL,
		},
		{
			name:    "postgres row lock",
			sql:     "SELECT * FROM users FOR UPDATE",
			dialect: DialectPostgres,
		},
		{
			name:    "mysql share lock",
			sql:     "SELECT * FROM users LOCK IN SHARE MODE",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql dash without required whitespace is not a comment",
			sql:     "SELECT 1--x; DELETE FROM users",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql multiple statements",
			sql:     "SELECT 1; DELETE FROM users",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql malformed string",
			sql:     "SELECT 'unterminated",
			dialect: DialectMySQL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsReadOnly(tt.sql, tt.dialect) {
				t.Fatalf("IsReadOnly(%q, %s) = true, want fail-closed false", tt.sql, tt.dialect)
			}
		})
	}
}

func TestIsReadOnlyAllowsSideEffectWordsOnlyInsideLiteralsAndSubqueries(t *testing.T) {
	tests := []struct {
		sql     string
		dialect Dialect
	}{
		{sql: "SELECT 'INTO OUTFILE' AS note", dialect: DialectMySQL},
		{sql: "SELECT * FROM (SELECT 'FOR UPDATE' AS note) AS nested", dialect: DialectPostgres},
		{sql: "SELECT 1 -- valid comment ; DELETE FROM users\n", dialect: DialectMySQL},
	}
	for _, tt := range tests {
		if !IsReadOnly(tt.sql, tt.dialect) {
			t.Fatalf("IsReadOnly(%q, %s) = false, want true", tt.sql, tt.dialect)
		}
	}
}

func TestIsReadOnlyRejectsNestedLocksAndBuiltInLockFunctions(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		dialect Dialect
	}{
		{
			name:    "postgres nested for update",
			sql:     "SELECT * FROM (SELECT * FROM users FOR UPDATE) AS locked_users",
			dialect: DialectPostgres,
		},
		{
			name:    "postgres nested for share",
			sql:     "SELECT * FROM (SELECT * FROM users FOR SHARE) AS locked_users",
			dialect: DialectPostgres,
		},
		{
			name:    "postgres deeply nested for no key update",
			sql:     "SELECT * FROM (SELECT * FROM (SELECT * FROM users FOR NO KEY UPDATE) AS level_two) AS level_one",
			dialect: DialectPostgres,
		},
		{
			name:    "postgres nested for key share",
			sql:     "SELECT * FROM (SELECT * FROM users FOR KEY SHARE) AS locked_users",
			dialect: DialectPostgres,
		},
		{
			name:    "mysql nested lock in share mode",
			sql:     "SELECT * FROM (SELECT * FROM users LOCK IN SHARE MODE) AS locked_users",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql nested for update",
			sql:     "SELECT * FROM (SELECT * FROM users FOR UPDATE) AS locked_users",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql nested for share",
			sql:     "SELECT * FROM (SELECT * FROM users FOR SHARE) AS locked_users",
			dialect: DialectMySQL,
		},
		{
			name:    "postgres quoted catalog advisory lock",
			sql:     `SELECT pg_catalog."pg_advisory_lock"(42)`,
			dialect: DialectPostgres,
		},
		{
			name:    "postgres doubled quote schema advisory lock",
			sql:     `SELECT "pg""catalog"."pg_advisory_lock"(42)`,
			dialect: DialectPostgres,
		},
		{
			name:    "postgres unicode escaped advisory lock identifier",
			sql:     `SELECT U&"pg_advisory_l\006Fck"(42)`,
			dialect: DialectPostgres,
		},
		{
			name:    "mysql get lock",
			sql:     "SELECT GET_LOCK('dbgov', 10)",
			dialect: DialectMySQL,
		},
		{
			name:    "mysql ansi quoted named lock",
			sql:     `SELECT "GET_LOCK"('dbgov', 10)`,
			dialect: DialectMySQL,
		},
		{
			name:    "mysql ansi quoted qualified named lock",
			sql:     `SELECT "mysql"."RELEASE_LOCK"('dbgov')`,
			dialect: DialectMySQL,
		},
		{
			name:    "postgres nested advisory lock",
			sql:     "SELECT * FROM (SELECT pg_try_advisory_lock(42)) AS nested_lock",
			dialect: DialectPostgres,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsReadOnly(tt.sql, tt.dialect) {
				t.Fatalf("IsReadOnly(%q, %s) = true, want false", tt.sql, tt.dialect)
			}
		})
	}
}

func TestIsReadOnlyRejectsBuiltInLockFunctionFamilies(t *testing.T) {
	postgresFunctions := []string{
		"pg_advisory_lock",
		"pg_advisory_lock_shared",
		"pg_advisory_xact_lock",
		"pg_advisory_xact_lock_shared",
		"pg_try_advisory_lock",
		"pg_try_advisory_lock_shared",
		"pg_try_advisory_xact_lock",
		"pg_try_advisory_xact_lock_shared",
		"pg_advisory_unlock",
		"pg_advisory_unlock_shared",
		"pg_advisory_unlock_all",
	}
	for _, name := range postgresFunctions {
		arguments := "42"
		if name == "pg_advisory_unlock_all" {
			arguments = ""
		}
		sql := "SELECT pg_catalog." + name + "(" + arguments + ")"
		if IsReadOnly(sql, DialectPostgres) {
			t.Errorf("IsReadOnly(%q, postgres) = true, want false", sql)
		}
	}
	mysqlFunctions := []struct {
		name      string
		arguments string
	}{
		{name: "GET_LOCK", arguments: "'dbgov', 10"},
		{name: "RELEASE_LOCK", arguments: "'dbgov'"},
		{name: "RELEASE_ALL_LOCKS"},
	}
	for _, function := range mysqlFunctions {
		sql := "SELECT " + function.name + "(" + function.arguments + ")"
		if IsReadOnly(sql, DialectMySQL) {
			t.Errorf("IsReadOnly(%q, mysql) = true, want false", sql)
		}
	}
}

func TestIsReadOnlyRejectsSideEffectingBuiltInFunctions(t *testing.T) {
	postgresFunctions := []string{
		"autoprewarm_dump_now",
		"brin_desummarize_range",
		"brin_summarize_new_values",
		"dblink",
		"dblink_exec",
		"gin_clean_pending_list",
		"heap_force_freeze",
		"heap_force_kill",
		"lo_export",
		"lowrite",
		"nextval",
		"pg_backup_start",
		"pg_backup_stop",
		"pg_buffercache_evict_all",
		"pg_cancel_backend",
		"pg_checkpoint",
		"pg_clear_attribute_stats",
		"pg_clear_relation_stats",
		"pg_create_logical_replication_slot",
		"pg_create_restore_point",
		"pg_current_xact_id",
		"pg_drop_replication_slot",
		"pg_export_snapshot",
		"pg_file_write",
		"pg_import_system_collations",
		"pg_log_backend_memory_contexts",
		"pg_log_standby_snapshot",
		"pg_logical_emit_message",
		"pg_logical_slot_get_binary_changes",
		"pg_notify",
		"pg_prewarm",
		"pg_promote",
		"pg_reload_conf",
		"pg_replication_origin_advance",
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
		"pg_stat_reset_shared",
		"pg_stat_statements_reset",
		"pg_stop_backup",
		"pg_switch_wal",
		"pg_sync_replication_slots",
		"pg_terminate_backend",
		"pg_truncate_visibility_map",
		"pg_wal_replay_pause",
		"postgres_fdw_disconnect_all",
		"set_config",
		"setval",
		"txid_current",
	}
	for _, name := range postgresFunctions {
		sql := "SELECT " + name + "()"
		if IsReadOnly(sql, DialectPostgres) {
			t.Errorf("IsReadOnly(%q, postgres) = true, want false", sql)
		}
	}

	mysqlFunctions := []string{
		"asynchronous_connection_failover_add_source",
		"audit_log_rotate",
		"benchmark",
		"get_lock",
		"group_replication_set_as_primary",
		"keyring_key_store",
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
		"sys_exec",
		"version_tokens_delete",
		"version_tokens_edit",
		"version_tokens_lock_exclusive",
		"version_tokens_set",
		"version_tokens_unlock",
		"wait_for_executed_gtid_set",
		"wait_until_sql_thread_after_gtids",
	}
	for _, name := range mysqlFunctions {
		sql := "SELECT " + name + "()"
		if IsReadOnly(sql, DialectMySQL) {
			t.Errorf("IsReadOnly(%q, mysql) = true, want false", sql)
		}
	}

	tests := []struct {
		sql     string
		dialect Dialect
	}{
		{sql: `SELECT pg_catalog."pg_terminate_backend"(42)`, dialect: DialectPostgres},
		{sql: `SELECT * FROM public."dblink"('conn', 'DELETE FROM critical RETURNING id') AS t(id int)`, dialect: DialectPostgres},
		{sql: `SELECT * FROM (SELECT dblink('conn', 'UPDATE critical SET active=false')) AS remote_write`, dialect: DialectPostgres},
		{sql: `SELECT pg_catalog."heap_force_kill"('critical', '(0,1)')`, dialect: DialectPostgres},
		{sql: `SELECT "pg_catalog"."pg_truncate_visibility_map"('critical')`, dialect: DialectPostgres},
		{sql: `SELECT pg_catalog."pg_restore_relation_stats"('critical', 42, 100)`, dialect: DialectPostgres},
		{sql: `SELECT "pg_catalog"."pg_clear_attribute_stats"('critical', 'column_name')`, dialect: DialectPostgres},
		{sql: `SELECT "BENCHMARK"(1000, 1)`, dialect: DialectMySQL},
		{sql: "SELECT @request_id := 42", dialect: DialectMySQL},
		{sql: "SELECT * FROM (SELECT @request_id:=42) AS assigned", dialect: DialectMySQL},
	}
	for _, tt := range tests {
		if IsReadOnly(tt.sql, tt.dialect) {
			t.Errorf("IsReadOnly(%q, %s) = true, want false", tt.sql, tt.dialect)
		}
	}
}

func TestIsReadOnlyRejectsMariaDBSequenceMutationForms(t *testing.T) {
	tests := []string{
		"SELECT NEXTVAL(sequence_name)",
		"SELECT SETVAL(sequence_name, 100)",
		"SELECT NEXT VALUE FOR sequence_name",
		"SELECT * FROM (SELECT NEXT/* comment */VALUE FOR sequence_name) AS generated",
		"SELECT sequence_name.nextval",
		"SELECT schema_name.sequence_name.nextval",
		"SELECT `sequence_name`.`nextval`",
		`SELECT "sequence_name"."nextval"`,
	}
	for _, sql := range tests {
		if IsReadOnly(sql, DialectMySQL) {
			t.Errorf("IsReadOnly(%q, mysql) = true, want false", sql)
		}
	}
}

func TestIsReadOnlyAllowsSideEffectNamesOnlyAsData(t *testing.T) {
	tests := []struct {
		sql     string
		dialect Dialect
	}{
		{sql: "SELECT 'pg_terminate_backend(42)' AS text", dialect: DialectPostgres},
		{sql: "SELECT pg_terminate_backend_count FROM metrics", dialect: DialectPostgres},
		{sql: "SELECT dblink_latency FROM metrics", dialect: DialectPostgres},
		{sql: "SELECT 'dblink(''conn'', ''DELETE FROM critical'')' AS text", dialect: DialectPostgres},
		{sql: "SELECT 1 /* pg_notify('events', 'payload') */", dialect: DialectPostgres},
		{sql: "SELECT 'SLEEP(10)' AS text", dialect: DialectMySQL},
		{sql: "SELECT benchmark_score FROM metrics", dialect: DialectMySQL},
		{sql: "SELECT nextval_count FROM metrics", dialect: DialectMySQL},
		{sql: "SELECT sequence_name.nextvalue FROM metrics", dialect: DialectMySQL},
		{sql: "SELECT 'NEXT VALUE FOR sequence_name' AS text", dialect: DialectMySQL},
		{sql: "SELECT @request_id", dialect: DialectMySQL},
		{sql: "SELECT ':=' AS text", dialect: DialectMySQL},
		{sql: "SELECT 1 /* @request_id := 42 */", dialect: DialectMySQL},
	}
	for _, tt := range tests {
		if !IsReadOnly(tt.sql, tt.dialect) {
			t.Errorf("IsReadOnly(%q, %s) = false, want true", tt.sql, tt.dialect)
		}
	}
}

func TestIsReadOnlyDoesNotTreatLockNamesInDataAsCalls(t *testing.T) {
	tests := []struct {
		sql     string
		dialect Dialect
	}{
		{sql: "SELECT 'pg_advisory_lock(42)' AS text", dialect: DialectPostgres},
		{sql: "SELECT pg_advisory_lock FROM metrics", dialect: DialectPostgres},
		{sql: `SELECT "pg_advisory_l""ock"(42)`, dialect: DialectPostgres},
		{sql: "SELECT 1 /* pg_catalog.pg_advisory_lock(42) */", dialect: DialectPostgres},
		{sql: "SELECT 'GET_LOCK(1)' AS text", dialect: DialectMySQL},
		{sql: `SELECT "GET_LOCK('dbgov', 10)" AS text`, dialect: DialectMySQL},
		{sql: "SELECT GET_LOCK FROM metrics", dialect: DialectMySQL},
	}
	for _, tt := range tests {
		if !IsReadOnly(tt.sql, tt.dialect) {
			t.Errorf("IsReadOnly(%q, %s) = false, want safe adjacent true", tt.sql, tt.dialect)
		}
	}
}

func TestLineCommentsEndAtCarriageReturn(t *testing.T) {
	unsafe := []struct {
		sql     string
		dialect Dialect
	}{
		{sql: "SELECT 1 -- hidden\r, pg_catalog.pg_terminate_backend(42)", dialect: DialectPostgres},
		{sql: "SELECT 1 -- hidden\r, GET_LOCK('dbgov', 10)", dialect: DialectMySQL},
		{sql: "SELECT 1 # hidden\r, GET_LOCK('dbgov', 10)", dialect: DialectMySQL},
	}
	for _, tt := range unsafe {
		if IsReadOnly(tt.sql, tt.dialect) {
			t.Errorf("IsReadOnly(%q, %s) = true, want false", tt.sql, tt.dialect)
		}
	}

	for _, dialect := range []Dialect{DialectMySQL, DialectPostgres} {
		sql := "SELECT 1 -- hidden\r; DELETE FROM users"
		if !HasMultipleStatements(sql, dialect) {
			t.Errorf("HasMultipleStatements(%q, %s) = false, want true", sql, dialect)
		}
		if HasMultipleStatements("SELECT 1 -- ; remains a comment\r\n", dialect) {
			t.Errorf("HasMultipleStatements(CRLF line comment, %s) = true, want false", dialect)
		}
	}
}

func TestHasMultipleStatements(t *testing.T) {
	multiple := []string{
		"SELECT 1; DELETE FROM users",
		"SELECT 1; SELECT 2",
		"UPDATE t SET a=1; DROP TABLE t",
		"SELECT 1;;",
		"SELECT 1; /* separator */ DELETE FROM users",
	}
	for _, sql := range multiple {
		if !HasMultipleStatements(sql) {
			t.Errorf("HasMultipleStatements(%q) = false", sql)
		}
	}

	single := []string{
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1; ",
		"SELECT 1; -- tail",
		"SELECT ';' FROM t",
		"SELECT 1 -- ; not real\n",
		"SELECT 1 /* ; not real */",
		"SELECT 1; /* tail */",
		"WITH x AS (SELECT ';') SELECT * FROM x",
	}
	for _, sql := range single {
		if HasMultipleStatements(sql) {
			t.Errorf("HasMultipleStatements(%q) = true", sql)
		}
	}
}

func TestPostgresDialectReadOnlyAndStatementScanning(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		readOnly bool
		multiple bool
	}{
		{
			name:     "dollar quoted semicolon is opaque",
			sql:      "SELECT $$; DROP TABLE users;$$ AS body",
			readOnly: true,
			multiple: false,
		},
		{
			name:     "statement separator immediately after dollar quote",
			sql:      "SELECT 1 $$x$$;DROP TABLE users",
			readOnly: false,
			multiple: true,
		},
		{
			name:     "tagged dollar quoted body is opaque",
			sql:      "SELECT $tag$ ' ; UPDATE users SET name = 'x' $tag$ AS body",
			readOnly: true,
			multiple: false,
		},
		{
			name:     "double quoted identifier is opaque",
			sql:      `SELECT "semi;colon" FROM users`,
			readOnly: true,
			multiple: false,
		},
		{
			name:     "backslash does not escape quote",
			sql:      `SELECT 'a\'; DROP TABLE users`,
			readOnly: false,
			multiple: true,
		},
		{
			name:     "unclosed dollar quote fails closed",
			sql:      "SELECT $$not closed",
			readOnly: false,
			multiple: true,
		},
		{
			name:     "unclosed string fails closed",
			sql:      "SELECT 'not closed",
			readOnly: false,
			multiple: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReadOnly(tt.sql, DialectPostgres); got != tt.readOnly {
				t.Fatalf("IsReadOnly(%q, postgres) = %t, want %t", tt.sql, got, tt.readOnly)
			}
			if got := HasMultipleStatements(tt.sql, DialectPostgres); got != tt.multiple {
				t.Fatalf("HasMultipleStatements(%q, postgres) = %t, want %t", tt.sql, got, tt.multiple)
			}
		})
	}
}

func TestPostgresCTEDollarQuoteScanning(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		readOnly bool
		multiple bool
	}{
		{
			name:     "dollar quote inside cte is balanced",
			sql:      "WITH a AS (SELECT $$x$$ AS v) SELECT * FROM a",
			readOnly: true,
			multiple: false,
		},
		{
			name:     "semicolon immediately after cte dollar quote is separator",
			sql:      "WITH a AS (SELECT $$x$$;DROP TABLE t) SELECT * FROM a",
			readOnly: false,
			multiple: true,
		},
		{
			name:     "semicolon inside cte dollar quote is opaque",
			sql:      "WITH a AS (SELECT $$x;DROP TABLE t$$ AS v) SELECT * FROM a",
			readOnly: true,
			multiple: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReadOnly(tt.sql, DialectPostgres); got != tt.readOnly {
				t.Fatalf("IsReadOnly(%q, postgres) = %t, want %t", tt.sql, got, tt.readOnly)
			}
			if got := HasMultipleStatements(tt.sql, DialectPostgres); got != tt.multiple {
				t.Fatalf("HasMultipleStatements(%q, postgres) = %t, want %t", tt.sql, got, tt.multiple)
			}
		})
	}
}

func TestPostgresDollarQuoteRequiresLegalTokenBoundaryAndTag(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		readOnly bool
		multiple bool
	}{
		{
			name:     "valid tagged quote",
			sql:      "SELECT $tag$; DELETE FROM users;$tag$ AS body",
			readOnly: true,
		},
		{
			name:     "identifier adjacent tag is not a quote",
			sql:      "SELECT foo$tag$; DELETE FROM users; $tag$",
			multiple: true,
		},
		{
			name:     "numeric tag is not a quote",
			sql:      "SELECT $1$; DELETE FROM users; $1$",
			multiple: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReadOnly(tt.sql, DialectPostgres); got != tt.readOnly {
				t.Fatalf("IsReadOnly(%q) = %t, want %t", tt.sql, got, tt.readOnly)
			}
			if got := HasMultipleStatements(tt.sql, DialectPostgres); got != tt.multiple {
				t.Fatalf("HasMultipleStatements(%q) = %t, want %t", tt.sql, got, tt.multiple)
			}
		})
	}
}

func TestAmbiguousBackslashStringsFailClosed(t *testing.T) {
	sql := `SELECT 'a\'; DROP TABLE users'`
	if !HasMultipleStatements(sql, DialectMySQL) {
		t.Fatalf("HasMultipleStatements(%q, mysql) = false, want fail-closed true", sql)
	}
	if !HasMultipleStatements(`SELECT 'a\'; DROP TABLE users`, DialectPostgres) {
		t.Fatalf("HasMultipleStatements(postgres backslash case) = false, want true")
	}
	for _, dialect := range []Dialect{DialectMySQL, DialectPostgres} {
		if IsReadOnly(`SELECT 'C:\tmp'`, dialect) {
			t.Fatalf("IsReadOnly(ambiguous backslash, %s) = true", dialect)
		}
		if _, _, ok := ClassifyDML(`UPDATE users SET note='C:\tmp' WHERE id=1`, dialect); ok {
			t.Fatalf("ClassifyDML(ambiguous backslash, %s) ok = true", dialect)
		}
	}
	if !IsReadOnly(`SELECT E'C:\\tmp'`, DialectPostgres) {
		t.Fatal("IsReadOnly(explicit postgres E string) = false, want true")
	}
}

func TestPostgresClassifyDMLSkipsLeadingComments(t *testing.T) {
	kind, hasWhere, ok := ClassifyDML("/* lead */ UPDATE users SET name='x' WHERE id=1", DialectPostgres)
	if kind != KindUpdate || !hasWhere || !ok {
		t.Fatalf("ClassifyDML(postgres) = %v/%t/%t, want update/true/true", kind, hasWhere, ok)
	}
}

func TestUnknownDialectFailsClosed(t *testing.T) {
	if IsReadOnly("SELECT 1", DialectStrict) {
		t.Fatal("IsReadOnly(strict) = true, want false")
	}
	if !HasMultipleStatements("SELECT 1", DialectStrict) {
		t.Fatal("HasMultipleStatements(strict) = false, want true")
	}
	if _, _, ok := ClassifyDML("UPDATE t SET a=1", DialectStrict); ok {
		t.Fatal("ClassifyDML(strict) ok = true, want false")
	}
}

func TestClassifyDML(t *testing.T) {
	tests := []struct {
		sql      string
		kind     Kind
		hasWhere bool
		ok       bool
	}{
		{sql: "INSERT INTO users(id) VALUES (1)", kind: KindInsert, ok: true},
		{sql: "UPDATE users SET name='x' WHERE id = 1", kind: KindUpdate, hasWhere: true, ok: true},
		{sql: "DELETE FROM users", kind: KindDelete, ok: true},
		{sql: "UPDATE users SET somewhere_else = 1", kind: KindUpdate, hasWhere: false, ok: true},
		{sql: "SELECT * FROM users", ok: false},
		{sql: "WITH x AS (SELECT 1) UPDATE users SET name='x' WHERE id=1", ok: false},
	}
	for _, tt := range tests {
		kind, hasWhere, ok := ClassifyDML(tt.sql)
		if kind != tt.kind || hasWhere != tt.hasWhere || ok != tt.ok {
			t.Fatalf("ClassifyDML(%q) = %v/%t/%t, want %v/%t/%t", tt.sql, kind, hasWhere, ok, tt.kind, tt.hasWhere, tt.ok)
		}
	}
}

func TestClassifyDMLOnlyCountsTargetTopLevelWhere(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		dialect  Dialect
		hasWhere bool
		ok       bool
	}{
		{
			name:    "where in comment",
			sql:     "UPDATE users SET active=0 /* WHERE id=1 */",
			dialect: DialectMySQL,
			ok:      true,
		},
		{
			name:    "where in string",
			sql:     "UPDATE users SET note='WHERE'",
			dialect: DialectMySQL,
			ok:      true,
		},
		{
			name:    "where in mysql hash comment",
			sql:     "UPDATE users SET active=0 # WHERE id=1",
			dialect: DialectMySQL,
			ok:      true,
		},
		{
			name:    "where in postgres nested comment",
			sql:     "UPDATE users SET active=0 /* outer /* inner */ WHERE id=1 */",
			dialect: DialectPostgres,
			ok:      true,
		},
		{
			name:    "where in nested subquery",
			sql:     "UPDATE users SET score=(SELECT max(score) FROM archive WHERE active=1)",
			dialect: DialectMySQL,
			ok:      true,
		},
		{
			name:    "where in postgres dollar quoted string",
			sql:     "UPDATE users SET note=$$WHERE$$",
			dialect: DialectPostgres,
			ok:      true,
		},
		{
			name:    "where in postgres escape string",
			sql:     `UPDATE users SET note=E'a\' WHERE id=1 \'b'`,
			dialect: DialectPostgres,
			ok:      true,
		},
		{
			name:     "target top level where",
			sql:      "UPDATE users SET score=(SELECT max(score) FROM archive WHERE active=1) WHERE id=7",
			dialect:  DialectMySQL,
			hasWhere: true,
			ok:       true,
		},
		{
			name:    "malformed expression fails closed",
			sql:     "DELETE FROM users WHERE id IN (SELECT id FROM archive",
			dialect: DialectPostgres,
		},
		{
			name:    "update without set fails closed",
			sql:     "UPDATE users WHERE id=1",
			dialect: DialectMySQL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, hasWhere, ok := ClassifyDML(tt.sql, tt.dialect)
			if hasWhere != tt.hasWhere || ok != tt.ok {
				t.Fatalf("ClassifyDML(%q, %s) = hasWhere %t/ok %t, want %t/%t", tt.sql, tt.dialect, hasWhere, ok, tt.hasWhere, tt.ok)
			}
		})
	}
}
