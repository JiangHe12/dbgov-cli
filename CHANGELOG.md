# Changelog

## [Unreleased]

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
