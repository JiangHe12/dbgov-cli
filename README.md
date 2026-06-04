# dbgov-cli

[English](README.md) | [中文](README_zh.md)

Governed MySQL operations CLI for AI agents and operators. It provides read queries, schema planning and apply, governed DML, GitOps import/reconcile/rollback, audit, RBAC, and local credential management.

## Overview

`dbgov` is built around a governance spine: connect to MySQL, classify risk, require explicit authorization for writes, execute through backend interfaces, and write structured audit events. It is MySQL-only today; PostgreSQL is planned but not enabled unless capabilities report it.

## Install

```bash
npm install -g dbgov-cli
# or
go install github.com/JiangHe12/dbgov-cli@latest
```

Release binaries are available from GitHub Releases. npm installs download the matching platform binary.

## Quickstart

```bash
DBGOV_PASSWORD='<password>' dbgov-cli ctx set local --engine mysql --host 127.0.0.1 --port 3306 --database app --username appuser -o json
dbgov-cli ctx use local -o json
dbgov-cli query --sql "SELECT 1" -o json
dbgov-cli explain --sql "SELECT * FROM users WHERE id = 1" -o json
dbgov-cli schema list -o json
```

Use `-o json` for automation and AI agents.

## Governance Model

| Risk | Meaning | Authorization |
|---|---|---|
| R0 | read-only operations and local inspection | no approval required, still audited |
| R1 | incremental writes such as add column, small WHERE DML, incremental import | `--yes` or interactive confirmation |
| R2 | large-impact WHERE DML or protected-context R1 | non-empty `--ticket` plus `--yes` |
| R3 | destructive schema, no-WHERE UPDATE/DELETE, prune, destructive rollback | `--ticket`, required `--allow-*`, and `--yes` |

Allow flags are precise: schema drop/modify uses `--allow-destructive`, no-WHERE DML uses `--allow-no-where`, table prune uses `--allow-production-prune`. Rollback has an R2 floor and may require one or both destructive/prune allow flags. If a context defines `ticketPattern`, tickets must match it; by default no pattern is enforced.

RBAC applies to writes: `reader` is R0, `writer` is up to R2, and `admin` is up to R3. AI agents and automation must not auto-fill `--ticket`, `--allow-*`, or high-risk `--yes`. Impact must come from `dbgov-cli explain`, `schema plan`, or `--dry-run`, never model guesses.

All operations, including denied and failed attempts, append to `~/.dbgov/audit.log`. Use `audit query`, `audit verify`, and `audit prune` to inspect, validate, and clean rotated logs.

## Usage

```bash
dbgov-cli version -o json
dbgov-cli capabilities -o json
dbgov-cli doctor config -o json
dbgov-cli ctx list -o json
dbgov-cli ctx export local > local.ctx.yaml
dbgov-cli ctx import -f local.ctx.yaml --rename local-copy -o json
dbgov-cli query --sql "SELECT * FROM users" -o json
dbgov-cli explain --sql "SELECT * FROM users WHERE active = 1" -o json
dbgov-cli schema dump --dir ./schema -o json
dbgov-cli schema plan -f desired.sql -o json
dbgov-cli schema apply -f desired.sql --dry-run -o json
dbgov-cli data exec --sql "UPDATE users SET active=0 WHERE id=1" --dry-run -o json
dbgov-cli export --dir ./schema -o json
dbgov-cli import ./schema --dry-run -o json
dbgov-cli reconcile ./schema --dry-run -o json
dbgov-cli rollback list -o json
dbgov-cli audit query --since 24h -o json
```

## Configuration and Contexts

Contexts live under `~/.dbgov`. Use `ctx set`, `ctx use`, `ctx current`, and `ctx list` to manage them. Credentials may be literal during setup, read from `DBGOV_PASSWORD`, or migrated to secure backends:

```bash
dbgov-cli ctx export prod > prod.ctx.yaml
dbgov-cli ctx import -f prod.ctx.yaml --rename prod-copy -o json
dbgov-cli ctx migrate-credentials --to encrypted-file -o json
dbgov-cli ctx role set prod --target-operator alice --role writer -o json
```

Portable context export redacts passwords by default. `--include-credentials` is only allowed for `plain-yaml` or empty credential backends; secure backend credentials must be shared out of band.

Set `DBGOV_OPERATOR` in CI to make audit and RBAC identity stable.

## Rollback and Snapshots

Schema mutations capture a pre-change DDL snapshot before execution. `rollback --to <snapshot>` restores structure only; MySQL data dropped by table or column deletion is not recovered. dbgov prints this warning during rollback planning and execution.

## Build from Source

```bash
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal
golangci-lint run --timeout=5m
```

MySQL integration tests are opt-in with `DBGOV_TEST_MYSQL_DSN`.

## AI Skill

```bash
dbgov-cli install claude --skills
dbgov-cli install codex --skills
```

## Contributing, Security, License

See [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), and [LICENSE](LICENSE).
