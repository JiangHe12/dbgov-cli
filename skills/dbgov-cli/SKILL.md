---
name: dbgov-cli
description: Governed database operations via CLI — query, schema diff/plan/apply, governed DML, GitOps import/reconcile/rollback, audit. MySQL and PostgreSQL.
allowed-tools: Bash(dbgov:*), Bash(dbgov-cli:*), Bash(go:*)
---

# dbgov-cli — AI Agent Reference

## What This Tool Does

`dbgov` is a governed database operations CLI for AI agents. Use it for MySQL and PostgreSQL database reads, schema inspection, declarative schema changes, governed DML, GitOps schema import/reconcile/rollback, context management, and audit inspection.

**Always use `-o json` for agent-consumed output.** stdout is clean JSON/table/plain output; errors go to stderr.

**MySQL and PostgreSQL are supported.** Treat `dbgov-cli capabilities -o json` as the authoritative source for the current build's available engines and features.

**Never estimate blast radius yourself.** Impact must come from dbgov commands such as `dbgov-cli explain`, `dbgov-cli schema plan`, or command `--dry-run` output. If dbgov cannot measure impact, it fails closed instead of guessing.

---

## Governance Model

### Risk Levels

| Risk | Rule | Examples |
|---|---|---|
| R0 | Free to run; still audited | `query` read-only SQL, `explain`, `schema list`, `schema describe`, `schema dump` to stdout, `schema diff`, `schema plan`, `audit query`, `audit verify`, `version`, `capabilities`, `doctor` |
| R1 | Requires `--yes` or interactive confirmation | `schema dump --dir`, `export`, `install --skills`, `schema apply` adding columns, `data exec` with `WHERE` and small impact, incremental `import` |
| R2 | Requires `--ticket` plus `--yes` | `data exec` with `WHERE` but EXPLAIN estimated rows over the threshold, default 1000; R1 operations in a protected context |
| R3 | Requires `--ticket`, the relevant `--allow-*` flag, and `--yes` | destructive schema changes, no-WHERE UPDATE/DELETE, production prune, destructive rollback, context replacement/deletion, credential migration, role changes |

`--yes` only confirms R1. It does not satisfy R2 or R3 by itself. Never auto-supply `--ticket`, `--allow-*`, or high-risk `--yes`; surface the missing authorization to the user. `--ticket` must be non-empty for R2/R3. If the active context sets a `ticketPattern` via `ctx set --ticket-pattern`, the ticket must match it; by default no pattern is enforced.

### R3 Allow Flags

| Operation | Required allow flag |
|---|---|
| `dbgov-cli schema apply` or `dbgov-cli import` with DROP COLUMN or MODIFY COLUMN | `--allow-destructive` |
| `dbgov-cli data exec` with no-WHERE UPDATE/DELETE | `--allow-no-where` |
| `dbgov-cli reconcile --prune` dropping tables | `--allow-production-prune` |
| `dbgov-cli rollback --to` restoring structure with dropped columns | `--allow-destructive` |
| `dbgov-cli rollback --to` restoring structure with dropped tables | `--allow-production-prune` |
| `dbgov-cli ctx set`, `ctx use`, `ctx import`, or `ctx migrate-credentials` | `--allow-context-change` |
| `dbgov-cli ctx delete` | `--allow-context-delete` |
| `dbgov-cli ctx role set` or `ctx role unset` | `--allow-role-change` |

Rollback has an R2 floor even when the generated plan is incremental. If rollback includes both dropped columns and dropped tables, both allow flags are required.

### RBAC

RBAC applies to write paths only. If roles are configured for a context:

| Role | Maximum allowed risk |
|---|---|
| reader | R0 |
| writer | R2 |
| admin | R3 |

Use `dbgov-cli ctx role set`, `dbgov-cli ctx role unset`, and `dbgov-cli ctx role list` to manage local context roles.

Governance-control writes are authorized against pre-change policy. Context replacement, deletion, and role changes use the target policy. A new context uses the persisted current context's policy; with no current context, bootstrap still requires R3 authorization. `ctx use` uses the old persisted current policy, or the destination policy when no current context exists. Use `--dry-run` to preview `ctx set`, `ctx use`, `ctx delete`, `ctx import`, `ctx migrate-credentials`, and role changes without authorization or writes.

### Trusted Operator Identity

Authorization and audit identity always use the local OS `username@hostname`. The deprecated global `--operator` identity override and `DBGOV_OPERATOR` are ignored; never rely on either for role selection. This prevents CLI-local identity spoofing, but a human and an AI process under the same OS account still have the same identity. Until an external signed approval source is configured or the agent runs under a separately protected OS account, local RBAC is not a security boundary between that human and AI. Never treat access to the OS account as human approval.

### Snapshots and Rollback

Before schema mutations (`schema apply`, `import`, `reconcile`, `rollback`), dbgov captures a pre-change schema DDL snapshot bound to the context and physical database target. Legacy snapshots without a target binding can still be listed but cannot be executed. Rollback restores structure only for MySQL and PostgreSQL: dropped data is not recovered. Dry-run returns a fingerprinted `SchemaPlan`; successful execution returns an honest `RollbackResult` with planned/applied counts and `dataRestored: false`.

### Audit

Every operation, including denied and failed operations, appends a JSON audit event to `~/.dbgov/audit.log`. Use:

```bash
dbgov-cli audit query -o json
dbgov-cli audit verify -o json
dbgov-cli audit prune --before 30d -o json
dbgov-cli audit prune --keep-last 10 --confirm --yes --ticket <ticket> --allow-audit-prune -o json
```

`audit prune` only deletes same-directory rotations named exactly `<active>.YYYYMMDD-HHMMSS[.<positive-ordinal>].log` and never the active `audit.log`. It is dry-run by default. Confirmed pruning is a fixed R3 evidence-destruction operation requiring `--confirm`, `--yes`, a non-empty human ticket, and the exact `--allow-audit-prune`. It authorizes against the persisted current-context policy (empty only when no current context exists), never a `--context` override. Dry-run returns before authorization without deleting evidence. Confirmed pruning reloads policy under the context-config lock and holds that lock through intent, deletion, and outcome; control evidence is written to the sibling `.<audit-base>-control` log. Audit core v2 then binds the complete previewed rotation set under the audit-path lock, verifies the authenticated chain and stable identities, advances the checkpoint, and durably deletes the selected oldest prefix. Any policy, candidate, identity, or evidence change fails closed; successful JSON output reports `checkpointState`.

Real mutations emit a `dbgov-cli.io/mutation-audit/v1` intent after validation and authorization and before the first target side effect, followed by an outcome with the same `mutationId`. Intent persistence failure blocks execution. Audit core v2's durable commit state is authoritative: only known-not-committed outcomes are durably spooled beside the audit log; known-committed or indeterminate outcomes are never queued because the record may already exist. An indeterminate replay is atomically marked `.indeterminate`, and later replay fails closed until an operator reconciles the audit record. Never retry the target mutation blindly; retryable queued outcomes replay before the next intent. Correlate or deduplicate with `(mutationId, phase)`.

Audit and telemetry persistence uses domain-separated SHA-256 fingerprints plus lengths or bounded counts instead of raw tickets, reasons, SQL, target database/object values, bodies, output, or backend error text. `audit query` also sanitizes legacy records before returning them.

### Credentials

Runtime commands resolve stored credstore references first. If the selected context has no stored credential, they fall back to `DBGOV_PASSWORD`; prefer this for non-interactive automation. `ctx set --password` must use `--credential-backend keychain` or `--credential-backend encrypted-file`; plain-yaml `ctx set --password` is rejected. Legacy/imported inline passwords remain readable for migration compatibility.

```bash
dbgov-cli ctx migrate-credentials --to encrypted-file --dry-run -o json
dbgov-cli ctx migrate-credentials --to keychain --context prod --dry-run -o json
dbgov-cli ctx set prod --credential-backend keychain --password '<password>' --dry-run -o json
```

Actual credential migration or persistence is an R3 context change and requires a human-supplied ticket, `--allow-context-change`, and explicit confirmation.

---

## Before Running Commands

Check capabilities and active context first:

```bash
dbgov-cli capabilities -o json
dbgov-cli ctx current -o json
```

If no context exists, ask the user for MySQL or PostgreSQL host, database, username, and credential handling, then create and select one:

```bash
dbgov-cli ctx set prod --engine mysql --host 127.0.0.1 --port 3306 --database app --username appuser --env prod --protected --dry-run -o json
dbgov-cli ctx set prod-pg --engine postgres --host 127.0.0.1 --port 5432 --database app --username appuser --env prod --protected --dry-run -o json
dbgov-cli ctx use prod --dry-run -o json
```

After previewing, surface the required R3 `--ticket`, `--allow-context-change`, and `--yes` approval to the human. Do not create the context until those values are supplied by the human-controlled workflow.

For commands that connect, supply `DBGOV_PASSWORD='<password>'` in the environment unless the context stores a secure credstore reference.

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
dbgov-cli ctx set <name> --engine mysql --host <host> --port 3306 --database <db> --username <user> --dry-run -o json
dbgov-cli ctx use <name> --dry-run -o json
dbgov-cli ctx delete <name> --dry-run -o json
dbgov-cli ctx role set <context> --target-operator <os-user@hostname> --role writer --dry-run -o json
dbgov-cli ctx role unset <context> --target-operator <os-user@hostname> --dry-run -o json
dbgov-cli ctx role list <context> -o json
dbgov-cli ctx export <name> -o json
dbgov-cli ctx export <name> --include-credentials -o json
dbgov-cli ctx import -f ctx.yaml --rename <new-name> --force --dry-run -o json
dbgov-cli ctx migrate-credentials --to encrypted-file --context <name> --dry-run -o json
```

`ctx export` redacts `password` by default. `--include-credentials` only includes plaintext credentials when they are stored inline in legacy contexts (`plain-yaml` or an empty/unset credential backend); encrypted-file, keychain, and vault credentials must be shared out of band. `ctx import` accepts portable context YAML, supports `--rename` and `--force`, and leaves redacted credentials empty so the operator can supply `DBGOV_PASSWORD` at runtime or run `dbgov-cli ctx set <name> --credential-backend keychain --password=...`.

### Read Queries

Read-only SQL:

```bash
dbgov-cli query --sql "SELECT id, name FROM users" -o json
```

`query` rejects writable CTEs, row-locking clauses, file/session/administrative side-effect functions, MySQL user-variable assignment, and unknown or user-defined function calls. MySQL permits only recognized unquoted native functions; quoted function identifiers are rejected as ambiguous. Ordinary PostgreSQL functions require canonical `pg_catalog` qualification (for example, `pg_catalog.count(*)`); unqualified calls are limited to non-redefinable SQL grammar constructs such as `COALESCE`, and quoted identifiers are case-sensitive. It runs accepted SQL in a database read-only transaction and rolls that transaction back after reading the result. The lexical classifier cannot resolve view bodies, user-defined operators, or functions reached through casts, so production contexts must still use a database account whose privileges are read-only. JSON preserves SQL `NULL` as `null` and keeps it distinct from `""`; table output displays it as `NULL`. Use `data exec` for DML and `schema apply` for DDL.

Execution plan and estimated impact:

```bash
dbgov-cli explain --sql "SELECT * FROM users WHERE active = 1" -o json
```

### Schema Read and Planning

```bash
dbgov-cli schema list -o json
dbgov-cli schema describe users -o json
dbgov-cli schema dump --dir ./schema --yes -o json
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
dbgov-cli export --dir ./schema --yes -o json
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

Rollback restores schema structure only. Dropped table/column data is not recovered. Treat the returned plan/target fingerprints and `dataRestored: false` as authoritative.

### Audit

```bash
dbgov-cli audit query --since 24h --risk R2 -o json
dbgov-cli audit verify -o json
dbgov-cli audit prune --before 30d -o json
dbgov-cli audit prune --keep-last 20 --confirm --yes --ticket <ticket> --allow-audit-prune -o json
```

Use `--path` only when inspecting a non-default audit log:

```bash
dbgov-cli audit query --path ./audit.log --limit 50 --reverse -o json
dbgov-cli audit prune --path ./audit.log --before 2026-06-01 --confirm --yes --ticket <ticket> --allow-audit-prune -o json
```
