<!--
Thanks for contributing to dbgov-cli.

Before opening this PR, keep README.md, README_zh.md, and CHANGELOG.md in sync
when the change affects command behavior, governance, audit, safety, schema
planning, or rollback semantics.
-->

## Summary

<!-- One or two sentences. What does this PR change and why? Link the issue if applicable. -->

## Type of change

<!-- Tick all that apply. -->

- [ ] Bug fix
- [ ] Refactor with no behavior change
- [ ] New feature
- [ ] Governance, safety, audit, or output contract change
- [ ] Documentation only
- [ ] Build, CI, or release tooling

## Documentation sync checklist

- [ ] Command, flag, schema, output, error, safety, audit, or compatibility changes are reflected in `README.md` and `README_zh.md`.
- [ ] `CHANGELOG.md` is updated for user-facing changes.
- [ ] This PR does not require documentation changes, or the reason is explained below.

## Implementation checklist

- [ ] `gofmt -l main.go cmd internal` is clean.
- [ ] `go vet ./...` and `golangci-lint run --timeout=5m` are clean.
- [ ] `go build ./...` succeeds.
- [ ] `go test -count=1 ./...` passes locally on at least one OS.
- [ ] If this PR changes MySQL behavior, the env-gated integration path was considered with `DBGOV_TEST_MYSQL_DSN`.
- [ ] No credentials, SQL secrets, config payloads, or unsafe connection strings appear in stdout, stderr, audit, or logs.
- [ ] Errors flow through the existing app error conventions where applicable.

## Safety, audit, and authorization

- [ ] If this PR touches a mutating command, R0-R3 classification is unchanged or documented.
- [ ] Required `--yes`, `--ticket`, and `--allow-*` gates are not weakened.
- [ ] New audit event types or fields are covered by tests.
- [ ] Dry-run paths do not execute writes.
- [ ] Rollback or snapshot changes state their structure-level and data-loss limits.

## Test plan

<!-- How was this verified? Include the commands you ran and relevant output snippets. -->

```text
go build ./...
go test -count=1 ./...
```

## Risks and rollback

<!-- What could break? How is this rolled back if needed? -->

## Reviewer notes

<!-- Anything specific you'd like the reviewer to focus on. -->
