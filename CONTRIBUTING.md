# Contributing

Thank you for contributing. This is a production CLI, so changes should be small, well-tested, and easy to review.

## Development

Before sending changes, run these and keep them clean:

```bash
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal   # must print nothing
golangci-lint run --timeout=5m
go vet -tags=integration ./...
npm pack --dry-run
```

Use the Go version configured by CI. Never commit generated credentials, local context files, audit logs, backups, snapshots, or downloaded release binaries.

## Pull Requests

- Keep one behavioral topic per PR; don't fold a broad refactor into a focused fix.
- Add tests for command behavior, authorization changes, output schemas, or parsers.
- Update `README.md`, `README_zh.md`, and `CHANGELOG.md` when user-facing behavior changes.
- Never weaken production code to get a test past a governance check.

## Releases

Maintainers cut releases from `main` with GitHub-verified signed annotated `v*` tags; the steps live in `AGENTS.md`. Do not push tags unless explicitly authorized.
