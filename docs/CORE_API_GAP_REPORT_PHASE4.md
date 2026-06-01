# Phase 4 Core API Gap Report

## Scope

This report records the API pressure test from the first dbgov vertical slice:

- `dbgov ctx set/use/list/current/delete`
- read-only `dbgov schema diff -f desired.sql`
- fake backend for tests
- MySQL backend that builds but is not used by tests

No opskit-core changes were required in this phase.

## Findings

### Context Shape

`ctx.Base` worked for shared governance fields: credentials, environment,
protected flag, ticket policy, roles, audit, backup, Vault, and OTLP settings.

DB connection topology did not belong in core. `engine`, `host`, `port`, and
`database` fit cleanly as dbgov-owned fields embedded beside `ctx.Base`.

Decision: no core change. Keep DB topology in dbgov context.

### Risk From Destructive Diff Entries

Core `safety.Authorize` accepts a final `Risk`, but it does not compute risk
from domain facts such as `DROP COLUMN`.

For this read-only phase, dbgov keeps the operation authorized as `R0` and
marks destructive diff entries as `R3` in the rendered plan. That demonstrates
the hook without adding write behavior or changing core.

Decision: no core change in Phase 4. A later apply/plan phase may need a small
domain-side risk classifier interface, but the classifier should likely remain
outside core unless a second consumer needs it.

### Audit Event Fit

Core `audit.Event` was sufficient for schema diff:

- `EventType`: `schema.diff`
- `Context`: dbgov context name/env/protected
- `Target`: database as app, `schema` as object type, `diff` as resource
- `Status`: success/failure

The configurable target type JSON name allowed dbgov to use `objectType`
without changing core.

Decision: no core change.

### Backend Capabilities

The dbgov design needs backend-declared capabilities later, such as whether an
engine can roll back DDL or needs online DDL tooling. This thin read-only diff
does not consume those capabilities yet.

Decision: no core change. Keep capabilities out of core for now; revisit when
`schema apply` or `capabilities` is implemented.

### Printer Envelope

Core `printer` currently emits naked JSON and tables. That matches current
opskit-core state and is enough for this thin slice. Introducing a JSON envelope
now would be speculative and is explicitly deferred with nacos migration.

Decision: no core change.

## Summary

The current opskit-core APIs are sufficient for the Phase 4 vertical slice.
All observed gaps are domain-level or future-phase concerns, so dbgov handled
them locally and no core contract was changed.
