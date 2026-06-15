# Changelog

## [Unreleased]

### Added

- Add PostgreSQL P1 support for connection, read-only `query`, and `explain`
  using `EXPLAIN (FORMAT JSON)`.

### Security

- Harden SQL classification for PostgreSQL string, identifier, and
  dollar-quoted literal rules while keeping MySQL behavior unchanged.
- Redact PostgreSQL `CREATE/ALTER ROLE|USER ... PASSWORD ...` credential
  literals before caller output and audit persistence.

## v0.1.11

### Security

- Upgraded to opskit-core v1.0.5 to consume shared opaque token, URL credential,
  Bearer authorization, and session identifier value redaction across SQL,
  caller output, and audit boundaries.

## v0.1.10

### Security

- Redact SQL credential literals before statements and failed statements reach
  the audit log, while preserving the surrounding SQL structure.
- Redact governed DML plans/results and schema plan/dump SQL before returning
  JSON or table output to callers.
- Reuse the shared opskit-core v1.0.4 redactor for sensitive assignments, with
  a narrow MySQL layer for `IDENTIFIED BY/WITH` and `PASSWORD(...)` literals.

## v0.1.9

### Fixed

- Audit path resolution and append failures now emit a stderr warning without
  replacing the governed command's original result or exit code.

## v0.1.8

### Fixed

- `query` and `explain` now accept read-only `WITH [RECURSIVE]` CTE statements (previously rejected as non-read-only). CTEs that wrap a mutation (`WITH … DELETE/UPDATE/INSERT`) stay correctly blocked from the read path.
- Internal errors now map to correct exit codes instead of LOCAL_IO_ERROR (exit 6): missing table → RESOURCE_NOT_FOUND (4), invalid schema input → VALIDATION_FAILED (9), unsupported DDL → NOT_IMPLEMENTED (12), DDL execution failure → BACKEND_ERROR (7).

### Security

- `query`, `explain`, and `data exec` now reject multi-statement SQL (e.g. `SELECT 1; DELETE …`) — a defense-in-depth guard that holds even when the connection DSN enables multiStatements.

## v0.1.7

### Changed

- chore: bump opskit-core to v1.0.3 (hardens Cobra usage-error exit-code classification at the engine level).

### Tests

- add exit-code & `-o json` contract regression tests, incl. unknown-command → USAGE_ERROR/exit 1.

## v0.1.6

### Changed

- chore(lint): forbid bare fmt.Errorf/errors.New in cmd/ so the apperrors exit-code contract is mechanically enforced in CI.

## v0.1.5

### Fixed

- fix(schema): introspect empty databases instead of failing, enabling greenfield apply/import.
- fix(schema): return RESOURCE_NOT_FOUND (exit 4) when describing a missing table.

## v0.1.4

### Fixed

- Honor the opskit-core exit-code contract at the process boundary (previously every error exited 1; now AUTHORIZATION_REQUIRED -> 8, VALIDATION_FAILED -> 9, etc.).

## v0.1.3

### Fixed

- Exposed `dbgov-cli` npm command as the primary entry point.
- Kept `dbgov` alias for backward compatibility.
- Added root `--version` flag.
- Updated install/skill docs to use `dbgov-cli install ... --skills`.

## v0.1.2

### Changed

- Updated opskit-core dependency to v1.0.2.
- Bumped Go minor and patch dependencies.

## v0.1.1

### Fixed

- Enforced LF line endings via `.gitattributes` to fix Windows CI failures.
- Normalized CRLF line endings in skill frontmatter test for cross-platform compatibility.

## v0.1.0

_First public release. MySQL only; PostgreSQL is the fast-follow._

### Added

- Query and explain commands for governed read-only MySQL inspection.
- Schema read and planning workflow: `schema list`, `schema describe`, `schema dump`, `schema diff`, `schema plan`, and governed `schema apply`.
- Data mutation workflow via `data exec`, with DML classification, EXPLAIN-based impact estimation, transaction wrapping, and R0-R3 authorization gates.
- GitOps schema workflows: `export`, `import`, `reconcile`, schema snapshots, and structure-level rollback.
- Audit observability through `audit query` and `audit verify`.
- Context governance commands for RBAC role assignment and credential migration into secure backends.
- Static metadata commands: `version`, `capabilities`, and `doctor`.

### Governance

- Risk model follows the opskit family standard: R1/R2/R3 require confirmation, R2/R3 require tickets, and R3 requires operation-specific allow flags.
- Mutating schema and data operations emit dbgov audit events with effective risk, status, target, statement, impact rows, and snapshot metadata when applicable.
- MySQL rollback is structure-level only; deleted data is not reconstructed by dbgov.
