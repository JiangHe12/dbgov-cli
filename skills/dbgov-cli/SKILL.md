---
name: dbgov-cli
description: Governed database operations via CLI — query, schema diff/plan/apply, governed DML, GitOps import/reconcile/rollback, audit. MySQL.
allowed-tools: Bash(dbgov-cli:*), Bash(go:*)
---

# dbgov-cli — AI Agent Reference

## What This Tool Does

`dbgov` is a governed database operations CLI for AI agents. Use it for MySQL database reads, schema inspection, declarative schema changes, governed DML, GitOps schema import/reconcile/rollback, context management, and audit inspection.

**Always use `-o json` for agent-consumed output.** stdout is clean JSON/table/plain output; errors go to stderr.

**MySQL only in the current release.** PostgreSQL is planned as a fast-follow, but do not assume PG support unless `dbgov-cli capabilities -o json` says it is available.

**Never estimate blast radius yourself.** Impact must come from dbgov commands such as `dbgov-cli explain`, `dbgov-cli schema plan`, or command `--dry-run` output. If dbgov cannot measure impact, it fails closed instead of guessing.

---

## Governance Model

### Risk Levels

| Risk | Rule | Examples |
|---|---|---|
| R0 | Free to run; still audited | `query` read-only SQL, `explain`, `schema list`, `schema describe`, `schema dump`, `schema diff`, `schema plan`, `audit query`, `audit verify`, `version`, `capabilities`, `doctor` |
| R1 | Requires `--yes` or interactive confirmation | `schema apply` adding columns, `data exec` with `WHERE` and small impact, incremental `import` |
| R2 | Requires `--ticket` plus `--yes` | `data exec` with `WHERE` but EXPLAIN estimated rows over the threshold, default 1000; R1 operations in a protected context |
| R3 | Requires `--ticket`, the relevant `--allow-*` flag, and `--yes` | destructive schema changes, no-WHERE UPDATE/DELETE, production prune, destructive rollback |

`--yes` only confirms R1. It does not satisfy R2 or R3 by itself. Never auto-supply `--ticket`, `--allow-*`, or high-risk `--yes`; surface the missing authorization to the user. `--ticket` must be non-empty for R2/R3. If the active context sets a `ticketPattern` via `ctx set --ticket-pattern`, the ticket must match it; by default no pattern is enforced.

### R3 Allow Flags

| Operation | Required allow flag |
|---|---|
| `dbgov-cli schema apply` or `dbgov-cli import` with DROP COLUMN or MODIFY COLUMN | `--allow-destructive` |
| `dbgov-cli data exec` with no-WHERE UPDATE/DELETE | `--allow-no-where` |
| `dbgov-cli reconcile --prune` dropping tables | `--allow-production-prune` |
| `dbgov-cli rollback --to` restoring structure with dropped columns | `--allow-destructive` |
| `dbgov-cli rollback --to` restoring structure with dropped tables | `--allow-production-prune` |

Rollback has an R2 floor even when the generated plan is incremental. If rollback includes both dropped columns and dropped tables, both allow flags are required.

### RBAC

RBAC applies to write paths only. If roles are configured for a context:

| Role | Maximum allowed risk |
|---|---|
| reader | R0 |
| writer | R2 |
| admin | R3 |

Use `dbgov-cli ctx role set`, `dbgov-cli ctx role unset`, and `dbgov-cli ctx role list` to manage local context roles.

### Snapshots and Rollback

Before schema mutations (`schema apply`, `import`, `reconcile`, `rollback`), dbgov captures a pre-change schema DDL snapshot. Rollback is structure-level only for MySQL: dropped data is not recovered. dbgov prints a data-loss warning during rollback planning and execution.

### Audit

Every operation, including denied and failed operations, appends a JSON audit event to `~/.dbgov/audit.log`. Use:

```bash
dbgov-cli audit query -o json
dbgov-cli audit verify -o json
dbgov-cli audit prune --before 30d -o json
dbgov-cli audit prune --keep-last 10 --confirm -o json
```

`audit prune` only deletes rotated audit logs. It never deletes the active `audit.log`; it is dry-run by default and requires `--confirm` to remove files.

### Credentials

Contexts resolve credentials through literal passwords, credstore references, or `DBGOV_PASSWORD`. Prefer secure backends:

```bash
dbgov-cli ctx migrate-credentials --to encrypted-file -o json
dbgov-cli ctx migrate-credentials --to keychain --context prod -o json
```

---

## Before Running Commands

Check capabilities and active context first:

```bash
dbgov-cli capabilities -o json
dbgov-cli ctx current -o json
```

If no context exists, ask the user for MySQL host, database, username, and credential handling, then create and select one:

```bash
DBGOV_PASSWORD='<password>' dbgov-cli ctx set prod --engine mysql --host 127.0.0.1 --port 3306 --database app --username appuser --env prod --protected -o json
dbgov-cli ctx use prod -o json
```

---

## Decision Tree

### Metadata and Diagnostics

```bash
dbgov-cli version -o json
dbgov-cli capabilities -o json
dbgov-cli doctor config -o json
dbgov-cli doctor network -o json
dbgov-cli doctor auth -o json
```

### Contexts and Roles

```bash
dbgov-cli ctx list -o json
dbgov-cli ctx current -o json
dbgov-cli ctx set <name> --engine mysql --host <host> --port 3306 --database <db> --username <user> -o json
dbgov-cli ctx use <name> -o json
dbgov-cli ctx delete <name> -o json
dbgov-cli ctx role set <context> --target-operator alice --role writer -o json
dbgov-cli ctx role unset <context> --target-operator alice -o json
dbgov-cli ctx role list <context> -o json
dbgov-cli ctx export <name> -o json
dbgov-cli ctx export <name> --include-credentials -o json
dbgov-cli ctx import -f ctx.yaml --rename <new-name> --force -o json
dbgov-cli ctx migrate-credentials --to encrypted-file --context <name> -o json
```

`ctx export` redacts `password` by default. `--include-credentials` is only valid for `plain-yaml` or empty credential backends; encrypted-file, keychain, and vault credentials must be shared out of band. `ctx import` accepts portable context YAML, supports `--rename` and `--force`, and leaves redacted credentials empty so the operator can run `dbgov-cli ctx set <name> --password=...`.

### Read Queries

Read-only SQL:

```bash
dbgov-cli query --sql "SELECT id, name FROM users" -o json
```

`query` rejects writes. Use `data exec` for DML and `schema apply` for DDL.

Execution plan and estimated impact:

```bash
dbgov-cli explain --sql "SELECT * FROM users WHERE active = 1" -o json
```

### Schema Read and Planning

```bash
dbgov-cli schema list -o json
dbgov-cli schema describe users -o json
dbgov-cli schema dump --dir ./schema -o json
dbgov-cli schema diff -f desired.sql -o json
dbgov-cli schema plan -f desired.sql -o json
```

Use `schema plan` before `schema apply`. Treat plan risk and warnings as authoritative.

### Schema Apply

Preview:

```bash
dbgov-cli schema apply -f desired.sql --dry-run -o json
```

Incremental R1 apply:

```bash
dbgov-cli schema apply -f desired.sql --yes -o json
```

Destructive R3 apply:

```bash
dbgov-cli schema apply -f desired.sql --ticket DB-123 --allow-destructive --yes -o json
```

### Governed DML

Preview impact and authorization:

```bash
dbgov-cli data exec --sql "UPDATE users SET active = 0 WHERE last_seen < '2025-01-01'" --dry-run -o json
```

Small-impact R1 DML:

```bash
dbgov-cli data exec --sql "UPDATE users SET active = 0 WHERE id = 42" --yes -o json
```

Large-impact R2 DML:

```bash
dbgov-cli data exec --sql "UPDATE users SET active = 0 WHERE last_seen < '2025-01-01'" --ticket DB-123 --yes -o json
```

No-WHERE R3 UPDATE/DELETE:

```bash
dbgov-cli data exec --sql "DELETE FROM sessions" --ticket DB-123 --allow-no-where --yes -o json
```

You may also read DML from a file:

```bash
dbgov-cli data exec -f change.sql --dry-run -o json
```

### GitOps Schema

Export current schema:

```bash
dbgov-cli export --dir ./schema -o json
```

Import desired schema directory:

```bash
dbgov-cli import ./schema --dry-run -o json
dbgov-cli import ./schema --yes -o json
dbgov-cli import ./schema --ticket DB-123 --allow-destructive --yes -o json
```

Reconcile with drift detection:

```bash
dbgov-cli reconcile ./schema --dry-run -o json
dbgov-cli reconcile ./schema --yes -o json
```

Prune extra database tables only when the user explicitly authorizes it:

```bash
dbgov-cli reconcile ./schema --prune --dry-run -o json
dbgov-cli reconcile ./schema --prune --ticket DB-123 --allow-production-prune --yes -o json
```

If the same reconcile plan also drops or modifies columns, add `--allow-destructive` too.

### Rollback

List snapshots:

```bash
dbgov-cli rollback list -o json
```

Preview structure-level restore:

```bash
dbgov-cli rollback --to <snapshot-id> --dry-run -o json
```

Execute rollback:

```bash
dbgov-cli rollback --to <snapshot-id> --ticket DB-123 --yes -o json
dbgov-cli rollback --to <snapshot-id> --ticket DB-123 --allow-destructive --allow-production-prune --yes -o json
```

Rollback restores schema structure only. Dropped table/column data is not recovered.

### Audit

```bash
dbgov-cli audit query --since 24h --risk R2 -o json
dbgov-cli audit verify -o json
dbgov-cli audit prune --before 30d -o json
dbgov-cli audit prune --keep-last 20 --confirm -o json
```

Use `--path` only when inspecting a non-default audit log:

```bash
dbgov-cli audit query --path ./audit.log --limit 50 --reverse -o json
dbgov-cli audit prune --path ./audit.log --before 2026-06-01 --confirm -o json
```
