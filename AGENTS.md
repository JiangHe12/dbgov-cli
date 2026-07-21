# dbgov-cli Agent Guide

This file is the contributor and AI-agent guide for this repository. `CLAUDE.md` and `AGENTS.md` are kept identical — edit both together.

## Project Summary

dbgov is a governed MySQL and PostgreSQL operations CLI for AI agents: read queries and `explain`, schema `diff` / `plan` / `apply`, governed DML (`data exec`), GitOps `import` / `reconcile` / `rollback`, audit query/verify/prune, RBAC, and credential migration. It is built on the shared `opskit-core` governance engine.

## Working Discipline

- Make the smallest change that solves the task, and match the surrounding style.
- A change is not finished until it builds, every test passes, and formatting (and lint, where enforced) is clean — see Build & Verify.
- Never weaken governance, security, or production authorization to make code or tests pass.

## Build & Verify

```bash
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal   # must print nothing
golangci-lint run --timeout=5m
go vet -tags=integration ./...  # compiles the //go:build integration tests
npm pack --dry-run              # must list exactly the 5 files in Release & Versioning
```

Real-backend integration tests (`//go:build integration`) for MySQL and PostgreSQL are env-gated (skipped locally unless `DBGOV_TEST_MYSQL_DSN` / `DBGOV_TEST_POSTGRES_DSN` is set). Nightly and release workflows set `DBGOV_TEST_REQUIRED=1`, so missing endpoints fail instead of producing a green skip, and run against digest-pinned live databases via `docker-compose.integration.yml` — not on push/PR.

## Governance Rules

- R0 reads are free but still audited. R1 needs `--yes`. R2 also needs a non-empty `--ticket`. R3 also needs the precise `--allow-*` flag(s). Protected contexts raise every operation one tier.
- DML impact must come from `EXPLAIN`, and governed execution must revalidate the exact plan binding; rollback snapshots must match the selected physical target and restore structure only, so dropped data is never recovered.
- AI agents must never auto-fill `--ticket`, `--allow-*`, or a high-risk `--yes`. Impact and blast radius must come from the CLI's own `plan` / `diff` / `explain` / `--dry-run` output, never from model guesses.
- Confirmed `audit prune` is a fixed R3 evidence-destruction operation requiring `--confirm`, `--yes`, a ticket, and the exact `--allow-audit-prune`. It uses the trusted OS operator and persisted current-context policy; dry-run returns before authorization and performs no deletion. Its control intent/outcome is written to sibling `.<audit-base>-control`, never the target evidence namespace. Confirmed pruning delegates the locked transaction to audit core v2, which binds the complete previewed rotation set, verifies authenticated history and stable file identities, advances the checkpoint, and durably removes only the selected oldest prefix. Invalid, changed, non-regular, or unreadable evidence fails closed.
- Every real mutation persists a sanitized `dbgov-cli.io/mutation-audit/v1` intent before its first target side effect and a correlated outcome afterward. Intent failure blocks execution. Core v2 commit state is authoritative: only a known-not-committed outcome enters the owner-only adjacent replay spool; known-committed and indeterminate outcomes are never queued because the record may already exist. If replay itself becomes indeterminate, the entry is atomically marked `.indeterminate` and later replay fails closed until manual reconciliation. Never persist raw tickets, reasons, SQL, target database/object values, bodies, output, or backend error text in audit or telemetry.

## Code Conventions

- Keep changes narrow and aligned with the existing Cobra command style.
- Use `opskit-core` for shared app errors, credential storage, telemetry, printing, context, audit, safety, and lockfile behavior; do not reimplement those concerns locally.
- Prefer focused command tests, plus golden tests where output contracts are stable.
- Do not weaken production authorization for tests.

## Repository Layout

- `cmd/` — Cobra commands
- `internal/` — backend, schema, safety, audit, snapshot, and sqlclass packages
- `skills/dbgov-cli/` — embedded AI Skill, installed via `dbgov install <agent> --skills`
- `.github/` — CI and release workflows

## Release & Versioning

Release only when the user explicitly asks; the pipeline is tag-triggered. To cut version `X.Y.Z`:

1. `package.json` → `"version": "X.Y.Z"`.
2. `CHANGELOG.md` → add a section headed exactly `## vX.Y.Z`. It must equal the git tag with no trailing text — `release.yml` extracts the release notes by matching that exact line. Promote entries from `## [Unreleased]`.
3. Run Build & Verify; `npm pack --dry-run` must still list exactly: `LICENSE`, `README.md`, `package.json`, `bin/dbgov-cli.js`, `scripts/install.js`.
4. Commit, then create a signed annotated tag `vX.Y.Z` with the maintainer's GitHub-registered SSH signing key and push it. The release preflight requires the tag, `package.json`, and the exact changelog heading to agree, and requires GitHub's REST API to report the tag signature as verified. The tag then triggers `.github/workflows/release.yml`: integration tests → multi-platform build → cosign signing → checksums → GitHub Release → npm publish via OIDC Trusted Publishing.
5. Never hand-edit release artifacts or publish manually.
