<div align="center">

# dbgov-cli

**Governed MySQL & PostgreSQL operations for humans _and_ AI agents.**

Run queries, change schemas, and execute DML behind guardrails — every change is measured by `EXPLAIN`, previewed, snapshotted for rollback, and audited, so neither you nor an AI assistant can accidentally nuke a production database.

[![npm version](https://img.shields.io/npm/v/dbgov-cli.svg)](https://www.npmjs.com/package/dbgov-cli)
[![CI](https://github.com/JiangHe12/dbgov-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/JiangHe12/dbgov-cli/actions/workflows/ci.yml)
[![license](https://img.shields.io/npm/l/dbgov-cli.svg)](LICENSE)
[![signed](https://img.shields.io/badge/release-cosign%20%2B%20npm%20provenance-blue.svg)](#-trust--verification)

[English](README.md) · [简体中文](README_zh.md)

</div>

---

## 🧭 What is this? (read me first)

Touching a production database is one of the scariest things in ops: a missing `WHERE` clause, a careless `DROP COLUMN`, or a schema migration gone wrong can lose data in seconds — usually with no preview, no backup, and no record of what happened. Handing that power to a script or an AI agent is even scarier.

**dbgov-cli wraps every database operation in guardrails.** Think of it as a careful DBA sitting between you and the database:

- 📏 **Measures impact before acting** — `explain`, `schema plan`, and `--dry-run` tell you *exactly* how many rows or which columns a change will hit. dbgov never guesses; if it can't measure, it refuses.
- 🛡️ **Scales the friction to the danger** — a one-row update just needs a confirmation; a no-`WHERE` `DELETE` or a `DROP COLUMN` needs a change ticket *and* an explicit "yes, allow destruction" flag.
- 📸 **Snapshots schema before every mutation** — so you can roll structure back if something's wrong.
- 👥 **Honors roles** — readers can't write, writers can't do destructive ops, only admins can.
- 📜 **Audits everything** — every action (including denied ones) lands in a tamper-evident log.
- 🤖 **Is safe to hand to an AI agent** — it can read and preview freely, but **cannot** invent the human approvals that destructive changes require.

Works with **MySQL** and **PostgreSQL**.

---

## ✨ Features

| | |
|---|---|
| 🗄️ **Two engines** | **MySQL** and **PostgreSQL**, at full parity. `dbgov capabilities` reports what the current build supports. |
| 🔎 **Read & explain** | `query` (read-only SQL, rejects writes) and `explain` (real execution plan + estimated rows). |
| 🧱 **Declarative schema** | `schema list / describe / dump / diff / plan / apply` — diff your DB against a desired `.sql` and apply the delta. |
| ✏️ **Governed DML** | `data exec` runs `UPDATE`/`DELETE`/`INSERT` with `EXPLAIN`-measured blast radius and risk-scaled authorization. |
| 🔄 **GitOps for schema** | `export` → `import` → `reconcile` (with drift detection + optional `--prune`) → `rollback` from snapshots. |
| 🚦 **R0–R3 governance** | every operation is risk-classified; protected contexts escalate one tier; AI callers can never self-authorize. |
| 👥 **RBAC** | per-context `reader` / `writer` / `admin` roles cap the maximum risk a write path can reach. |
| 📸 **Snapshots & rollback** | automatic pre-change schema snapshot; structure-level restore with explicit data-loss warnings. |
| 📜 **Tamper-evident audit** | every operation appended to a hash-verifiable log; `audit verify` detects tampering. |
| 🔏 **Trusted supply chain** | **cosign-signed** binaries, npm **provenance**, and a **SHA-256**-verified installer. |

---

## 📦 Install

```bash
npm install -g dbgov-cli
```

This installs a tiny launcher; on first run it downloads the right pre-built binary for your OS/arch from the signed [GitHub Release](https://github.com/JiangHe12/dbgov-cli/releases) and **verifies its SHA-256** before use. Requires Node.js ≥ 14 for the installer (the CLI itself is a self-contained Go binary).

<details>
<summary>Other ways to install</summary>

- **Direct download** — grab the binary from the [Releases page](https://github.com/JiangHe12/dbgov-cli/releases), verify it against the cosign-signed `checksums.txt`, put it on your `PATH`, and rename it to `dbgov`.
- **From source** — `go install github.com/JiangHe12/dbgov-cli@latest` (Go 1.25+).

```bash
dbgov version
dbgov doctor config -o json     # static + read-only diagnostics
```

</details>

---

## 🚀 Quick start (60 seconds)

```bash
# 1. Point dbgov at your database (stored as a reusable "context"; password stays out of YAML)
dbgov ctx set prod --engine mysql \
  --host 127.0.0.1 --port 3306 --database app --username appuser --env prod --protected --dry-run
dbgov ctx set prod --engine mysql \
  --host 127.0.0.1 --port 3306 --database app --username appuser --env prod --protected \
  --ticket <human-ticket> --allow-context-change --yes
dbgov ctx use prod --ticket <human-ticket> --allow-context-change --yes
export DBGOV_PASSWORD='***'   # consumed when commands connect if the context has no stored credential

# 2. Read — read-only SQL is free (R0) and rejects writes
dbgov query --sql "SELECT id, name FROM users LIMIT 10" -o json

# 3. Measure before you change — see the plan & estimated rows
dbgov explain --sql "UPDATE users SET active = 0 WHERE last_seen < '2025-01-01'" -o json

# 4. Preview a DML change (nothing runs yet)
dbgov data exec --sql "UPDATE users SET active = 0 WHERE id = 42" --dry-run -o json

# 5. Apply it — a small, scoped write (R1) just needs your confirmation
dbgov data exec --sql "UPDATE users SET active = 0 WHERE id = 42" --yes -o json

# 6. See what happened
dbgov audit query --since 1h -o json
```

> 💡 **Tip:** create production contexts with `--protected`. dbgov then raises every operation one risk tier in that context automatically.

---

## 🔐 The governance model (the important part)

Every command is sorted into a **risk tier**. The more dangerous it is, the more explicit human sign-off it needs:

| Tier | What it covers | What you must provide |
|:---:|---|---|
| **R0** | Reads & inspection (`query`, `explain`, `schema list/describe/dump/diff/plan`, `audit`, `doctor`) | Nothing — but it's still audited |
| **R1** | Small, safe writes (add a column, `data exec` with `WHERE` and small estimated impact) | `--yes` (or interactive confirmation) |
| **R2** | Elevated writes (`data exec` whose `EXPLAIN` rows exceed the threshold; any R1 in a protected context) | `--yes` **and** a non-empty `--ticket` |
| **R3** | Destructive operations and governance-control changes (context replacement/deletion, credential migration, role changes) | The above **plus** the precise `--allow-*` flag |

**R3 allow flags** — destruction is never implicit:

| Operation | Required flag |
|---|---|
| `schema apply` / `import` dropping or modifying a column | `--allow-destructive` |
| `data exec` with a no-`WHERE` `UPDATE`/`DELETE` | `--allow-no-where` |
| `reconcile --prune` dropping tables | `--allow-production-prune` |
| `rollback --to` that drops columns / tables | `--allow-destructive` / `--allow-production-prune` |
| `ctx set` / `ctx use` / `ctx import` / `ctx migrate-credentials` | `--allow-context-change` |
| `ctx delete` | `--allow-context-delete` |
| `ctx role set` / `ctx role unset` | `--allow-role-change` |
| confirmed `audit prune` | `--allow-audit-prune` |

**RBAC** (when roles are configured on a context): `reader` → max R0, `writer` → max R2, `admin` → max R3. Governance-control writes are authorized against the target's **pre-change** policy. A new context uses the persisted current context's policy; without a current context, bootstrap still requires R3 authorization.

The authorization and audit identity is always the trusted local OS identity `username@hostname`. The deprecated global `--operator` override and `DBGOV_OPERATOR` are ignored. This prevents a CLI argument or environment variable from impersonating another role, but it cannot distinguish a human from an AI process running under the same OS account. Until an external signed approval source is configured or automation runs as a separately protected OS account, local RBAC alone is not a security boundary between that human and AI.

Three rules keep this safe — especially for automation:

1. **Impact comes from the database, not a guess.** Use `explain` / `schema plan` / `--dry-run`. dbgov fails closed rather than estimating. For governed `UPDATE` / `DELETE`, execution revalidates the exact EXPLAIN fingerprint and row estimate in the same transaction before changing data.
2. **Mutations are snapshotted first.** New snapshots are bound to their context and physical database target; an unbound legacy snapshot remains listable but cannot be executed. Rollback restores *structure* only — dropped row data is never recovered (dbgov warns you loudly and returns `dataRestored: false`).
3. **🤖 AI agents must never invent `--ticket`, `--allow-*`, or a high-risk `--yes`.** Those are *human* authorization inputs. An agent should surface "this needs approval X" and stop.

---

## 📚 Command reference

`dbgov <command> [flags]`. Add `-o json` for machine-readable output, `--help` on any command for its full flags, and `dbgov capabilities -o json` for the supported engines/features.

<details open>
<summary><b>Read & explain</b></summary>

```bash
dbgov query   --sql "SELECT ..." -o json          # read-only; rejects writes (R0)
dbgov explain --sql "SELECT ..." -o json          # execution plan + estimated rows (R0)
```

`query` rejects writable CTEs, row-locking clauses, file/session/administrative side-effect functions, and MySQL user-variable assignment. Accepted queries run inside a database read-only transaction that is explicitly rolled back after rows are consumed. JSON preserves SQL `NULL` as `null` (distinct from `""`), while table output renders it as `NULL`. Because custom functions and views cannot be proven side-effect-free lexically, production contexts must still use a database account whose privileges are read-only.
</details>

<details>
<summary><b>Schema</b> — inspect, diff & apply DDL</summary>

```bash
dbgov schema list                       -o json   # R0
dbgov schema describe <table>           -o json   # R0
dbgov schema dump                         -o json   # R0 (stdout)
dbgov schema dump  --dir ./schema --yes -o json   # R1 (local files)
dbgov schema diff  -f desired.sql       -o json   # R0
dbgov schema plan  -f desired.sql       -o json   # R0 — treat plan risk as authoritative
dbgov schema apply -f desired.sql --dry-run -o json
dbgov schema apply -f desired.sql --yes                                  -o json   # R1 (incremental)
dbgov schema apply -f desired.sql --ticket DB-123 --allow-destructive --yes -o json # R3 (destructive)
```

> Auto-increment columns are modeled as a normalized boolean across both engines; create / diff / apply / snapshot / rollback are preserved, but PostgreSQL `serial`-vs-identity, `ALWAYS`-vs-`BY DEFAULT`, and sequence start/increment options are intentionally **not** preserved. Generated PostgreSQL table DDL is explicitly qualified to the fixed `public` schema, and applicable DDL batches execute in one transaction.
</details>

<details>
<summary><b>Governed DML</b> — <code>data exec</code></summary>

```bash
dbgov data exec --sql "UPDATE ... WHERE ..." --dry-run -o json     # preview impact + required authz
dbgov data exec --sql "UPDATE ... WHERE id = 42" --yes  -o json     # R1 small impact
dbgov data exec --sql "UPDATE ... WHERE <wide>" --ticket DB-123 --yes -o json          # R2 large impact
dbgov data exec --sql "DELETE FROM sessions"    --ticket DB-123 --allow-no-where --yes -o json  # R3
dbgov data exec -f change.sql --dry-run -o json                     # read DML from a file
```
</details>

<details>
<summary><b>GitOps schema</b> — export · import · reconcile · rollback</summary>

```bash
dbgov export --dir ./schema --yes -o json                         # R1; dump current schema to files
dbgov import ./schema --dry-run -o json
dbgov import ./schema --yes -o json                               # R1 / R3 if destructive
dbgov reconcile ./schema --dry-run -o json                        # detect drift
dbgov reconcile ./schema --yes -o json
dbgov reconcile ./schema --prune --ticket DB-123 --allow-production-prune --yes -o json  # R3 prune
dbgov rollback list -o json                                       # list pre-change snapshots
dbgov rollback --to <snapshot-id> --dry-run -o json
dbgov rollback --to <snapshot-id> --ticket DB-123 --yes -o json   # structure only; data not recovered
```

Rollback dry-run returns a `SchemaPlan` with plan/target fingerprints. Successful execution returns `RollbackResult` with planned/applied statement counts, `scope: "schema-structure"`, and `dataRestored: false`.
</details>

<details>
<summary><b>Contexts, roles, audit & diagnostics</b></summary>

```bash
# Contexts (MySQL or PostgreSQL)
dbgov ctx set <name> --engine mysql|postgres --host <h> --port <p> --database <db> --username <u> [--protected] --dry-run
dbgov ctx set <name> ... --ticket <human-ticket> --allow-context-change --yes
dbgov ctx set <name> --credential-backend keychain|encrypted-file --password <secret> --ticket <human-ticket> --allow-context-change --yes
dbgov ctx list|current
dbgov ctx use <name> --dry-run
dbgov ctx use <name> --ticket <human-ticket> --allow-context-change --yes
dbgov ctx delete <name> --dry-run
dbgov ctx delete <name> --ticket <human-ticket> --allow-context-delete --yes
dbgov ctx export <name> [--include-credentials] -o json
dbgov ctx import -f ctx.yaml [--rename <new>] [--force] --dry-run -o json
dbgov ctx migrate-credentials --to encrypted-file|keychain [--context <name>] --dry-run -o json

# RBAC (write paths only): reader → R0, writer → R2, admin → R3
dbgov ctx role set <context> --target-operator <os-user@hostname> --role writer --dry-run -o json
dbgov ctx role set <context> --target-operator <os-user@hostname> --role writer --ticket <human-ticket> --allow-role-change --yes -o json
dbgov ctx role list <context> -o json

# Audit (tamper-evident; rotated logs only for prune)
dbgov audit query  [--since 24h] [--risk R2] [--limit 50] -o json
dbgov audit verify -o json
dbgov audit prune  (--before <30d|YYYY-MM-DD> | --keep-last <n>) -o json
dbgov audit prune  (--before <30d|YYYY-MM-DD> | --keep-last <n>) --confirm --yes --ticket <human-ticket> --allow-audit-prune -o json

# Diagnostics & ecosystem
dbgov doctor config|network|auth -o json
dbgov capabilities -o json
dbgov completion bash|zsh|fish|powershell
dbgov install <agent> --skills --yes # R1; install the dbgov AI skill (claude, codex, …)
dbgov version
```

> `audit prune` only deletes same-directory rotations named exactly `<active>.YYYYMMDD-HHMMSS[.<positive-ordinal>].log` (never the active `audit.log`) and defaults to a dry-run. Confirmed pruning is a fixed R3 evidence-destruction operation requiring `--confirm`, `--yes`, a non-empty `--ticket`, and the exact `--allow-audit-prune`. It authorizes against the persisted current-context policy (or an empty policy when no current context exists); `--context` does not replace that policy. Dry-run returns before authorization and performs no deletion. Confirmed pruning reloads the policy under the context-config lock and holds that lock through intent, deletion, and outcome; control intent/outcome is written to the sibling `.<audit-base>-control` log, never the target evidence namespace. Audit core v2 then holds the audit-path lock, binds the complete previewed rotation set, verifies the authenticated chain and stable file identities, advances the checkpoint, and durably deletes only the selected oldest prefix. Policy, candidate, identity, or evidence changes fail closed; successful JSON output reports the resulting `checkpointState`. CI identity is derived from its OS user and hostname; `DBGOV_OPERATOR` does not override it.

Every real mutation writes a `dbgov-cli.io/mutation-audit/v1` intent after validation and authorization but before the first target side effect, then writes a correlated outcome with the same `mutationId`. An intent write failure blocks the mutation. dbgov consumes audit core v2's durable commit state: only a known-not-committed outcome is stored in the owner-only adjacent `<audit.log>.outcome-spool`; known-committed and indeterminate outcomes are never queued because the record may already exist. If replay itself becomes indeterminate, dbgov atomically renames the entry with `.indeterminate` and all later replay fails closed until an operator reconciles the audit record. Retryable queued outcomes replay in order before the next intent; consumers deduplicate by `(mutationId, phase)`. Batch outcomes report bounded succeeded/failed/skipped counts.

Persisted audit and telemetry never contain raw tickets, reasons, SQL, target database/object values, backend error text, bodies, or command output. Audit records use domain-separated SHA-256 fingerprints plus byte lengths or bounded counters; `audit query` applies the same sanitization to legacy records before returning them.

For non-interactive runs, prefer `DBGOV_PASSWORD`; it is read when a command opens a connection and the selected context has no stored credential. To persist a password through `ctx set`, `--password` requires `--credential-backend keychain` or `--credential-backend encrypted-file`; plain-yaml `ctx set --password` is rejected. Legacy/imported inline credentials remain readable for migration and export compatibility.
</details>

---

## 🤖 For AI agents

- Run `dbgov capabilities -o json` first — it's the authoritative source for supported engines and features.
- Use `-o json` everywhere; every command returns a stable, versioned envelope.
- Get blast radius from `explain` / `schema plan` / `--dry-run`, **never** from your own reasoning.
- **Never self-fill `--ticket`, `--allow-*`, or a high-risk `--yes`.** Surface the required human approval and stop.

```bash
dbgov install claude --skills --yes # also: codex, opencode, copilot, cursor, windsurf, aider, cc-switch
```

---

## 🔏 Trust & verification

- **Signed binaries** — every release artifact is signed with [cosign](https://github.com/sigstore/cosign) (keyless / OIDC); a signed `checksums.txt` covers all platforms.
- **npm provenance** — published from CI via OpenID Connect with [provenance attestations](https://docs.npmjs.com/generating-provenance-statements) tying the package to this repo and workflow.
- **Verified installs** — the npm postinstall checks the binary's SHA-256 against the signed `checksums.txt` before installing.
- **Tamper-evident audit** — `dbgov audit verify` re-walks the log and reports any gap or modification.

---

## 🏗️ Build from source & contribute

```bash
git clone https://github.com/JiangHe12/dbgov-cli && cd dbgov-cli
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal      # must print nothing
golangci-lint run --timeout=5m
go vet -tags=integration ./...
npm pack --dry-run
```

MySQL / PostgreSQL integration tests are opt-in via `DBGOV_TEST_MYSQL_DSN` and `DBGOV_TEST_POSTGRES_DSN`. See [CONTRIBUTING.md](CONTRIBUTING.md) and the security policy in [SECURITY.md](SECURITY.md).

dbgov-cli is built on the shared [`opskit-core`](https://github.com/JiangHe12/opskit-core) governance engine and is part of the **opskit** family of governed CLIs for AI agents — alongside [`srvgov-cli`](https://www.npmjs.com/package/srvgov-cli) (remote servers), [`cfgov-cli`](https://www.npmjs.com/package/cfgov-cli) (config & Sentinel rules), and [`mqgov-cli`](https://www.npmjs.com/package/mqgov-cli) (message brokers).

---

## 📄 License

[MIT](LICENSE) © JiangHe12
