# dbgov-cli

**Governed database operations for AI agents.**

`dbgov` lets an AI coding agent change a real database the way a careful human
would: every mutation is risk-classified, its blast radius is measured by the
database itself (never guessed), dangerous actions are walled behind a human
ticket and explicit allow-flags, and everything is written to an append-only
audit log. `mysql`/`psql` are great for typing a query — they give you no
dry-run, no impact estimate, no rollback, no multi-environment context, and no
audit. That gap is why dbgov exists.

> Status: **v0.x — MySQL only.** PostgreSQL is the fast-follow. The governance
> model and command surface are stable; the engine set will grow.

## Install

```sh
# npm (downloads the prebuilt binary for your platform)
npm install -g dbgov-cli      # then: dbgov --help

# or with Go
go install github.com/JiangHe12/dbgov-cli@latest
```

## Governance model

| Tier | Meaning | Gate |
|---|---|---|
| **R0** | reads / local (query, explain, schema inspect, plan, audit query) | free, still audited |
| **R1** | ordinary write (add column, small WHERE'd DML) | `--yes` (or interactive confirm) |
| **R2** | sensitive write / large impact / protected-context R1 | `+ --ticket` |
| **R3** | destructive / irreversible (drop column/table, no-WHERE DML, prune) | `+ --ticket + matching --allow-*` |

- **Impact is authoritative, not guessed.** Risk tiering for DML uses the
  database's own `EXPLAIN` row estimate; if it can't be measured, dbgov refuses
  to proceed rather than guess.
- **`--ticket` and `--allow-*` are walls an agent cannot fill in** — they force
  a single, traceable, intentional human approval. Protected contexts raise
  every operation one tier.
- **RBAC** (opt-in): per-operator roles `reader/writer/admin` cap the risk an
  operator may perform.
- **Snapshots**: schema-mutating commands capture the pre-change schema first;
  `rollback` restores structure (MySQL rollback is structure-level and lossy —
  dropped data is not recovered, and dbgov says so loudly).
- **Audit**: every command (including denied and failed) appends a JSONL record;
  query and verify it with `dbgov audit`.

## Commands

```
dbgov version | capabilities | doctor

dbgov ctx set|use|list|current|delete <name>     # connection contexts
dbgov ctx role set|unset|list <ctx>              # RBAC (opt-in)
dbgov ctx migrate-credentials --to encrypted-file|keychain

# Read (R0)
dbgov query   --sql "SELECT ..."                 # rejects write SQL
dbgov explain --sql "..."                        # plan / blast radius
dbgov schema  list | describe <t> | dump [--dir] | diff -f f.sql | plan -f f.sql

# Schema change (declarative, R1–R3)
dbgov schema apply -f desired.sql [--dry-run] [--ticket] [--allow-destructive] [--yes]

# Data change (imperative DML, R1–R3)
dbgov data exec --sql "UPDATE ... WHERE ..." [--dry-run] [--ticket] [--allow-no-where] [--yes]

# GitOps
dbgov export --dir ./schema                                # R0
dbgov import ./schema [--dry-run] [--ticket] [--allow-destructive]
dbgov reconcile ./schema [--prune] [--ticket] [--allow-destructive] [--allow-production-prune]
dbgov rollback list | --to <snapshot> [--dry-run] [--ticket] [--allow-*]

# Audit (R0)
dbgov audit query [--since --operator --type --status --risk --context ...]
dbgov audit verify [--strict]
```

Use `-o json` for machine-readable output (the default for agent consumption).

## For AI agents

dbgov is designed to be driven by an agent, with a human in the loop only where
it matters:

- The agent **must never** auto-fill `--ticket`, `--allow-*`, or a high-risk
  `--yes`. Those are exactly the human-approval walls. The agent should run
  `--dry-run` / `plan`, report the dbgov-computed risk and impact to the user,
  and let the user supply the ticket/allow-flags.
- Impact figures the agent reports must come from dbgov (`explain`, `plan`,
  `--dry-run`), never from the model's own estimate.

## Built on

dbgov is part of the JiangHe12 operations CLI family and is built on
[opskit-core](https://github.com/JiangHe12/opskit-core) (the shared governance
engine — risk model, audit, credential store, RBAC), alongside
[sentinel-cli](https://github.com/JiangHe12/sentinel-cli) and
[nacos-cli](https://github.com/JiangHe12/nacos-cli).

## License

[MIT](LICENSE) © 2026 JiangHe12
