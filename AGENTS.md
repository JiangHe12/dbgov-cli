# dbgov-cli Agent Guide

## Project Summary

Governed MySQL operations CLI with query/explain, schema plan/apply, DML, GitOps, rollback, audit, RBAC, and credential migration.

## Build and Test

```bash
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal
npm pack --dry-run
```

`gofmt -l` must print nothing. Do not change generated release artifacts or local credentials.

## Governance Rules

R0 reads are free but audited. R1 requires `--yes`; R2 requires non-empty `--ticket` plus `--yes`; R3 requires `--ticket`, precise `--allow-*`, and `--yes`. Rollback is structure-only for MySQL and may be destructive. DML impact must come from EXPLAIN.

AI agents must never auto-fill `--ticket`, `--allow-*`, or high-risk `--yes`. Impact or blast radius must come from the CLI's own plan/diff/explain/dry-run output, not from model guesses.

## Code Conventions

- Keep changes narrow and aligned with existing Cobra command style.
- Use opskit-core helpers for shared app errors, credential storage, telemetry, printing, context, audit, safety, and lockfile behavior.
- Prefer focused command tests for CLI behavior and golden tests where output contracts are stable.
- Do not weaken production authorization for tests.

## Repository Layout

- `cmd/`: Cobra commands
- `internal/`: backend, schema, safety, audit, snapshot, sqlclass packages
- `skills/dbgov-cli/`: embedded AI Skill
- `.github/`: CI and release workflows

## Release Boundary

Do not tag or publish unless the user explicitly asks. Release workflows are tag-triggered.
