# Contributing

Thank you for contributing. This repository is a production CLI, so changes should be small, tested, and easy to review.

## Development

- Use the Go version configured by CI.
- Run `go build ./...` before sending changes.
- Run `go test -count=1 ./...` before sending changes.
- Run `gofmt -l main.go cmd internal`; it must print nothing.
- Do not commit generated credentials, local context files, audit logs, backups, snapshots, or downloaded release binaries.

## Pull Requests

- Keep one behavioral topic per PR.
- Include tests for command behavior, authorization changes, output schemas, or parsers.
- Update README / README_zh / CHANGELOG when user-facing behavior changes.
- Do not bypass governance checks in tests by weakening production code.
- Do not add broad refactors to a focused fix.

## Release Notes

Maintainers cut releases from `main` using `v*` tags. Do not push tags unless explicitly authorized.
