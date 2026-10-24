# Contributing

## Setup

Go 1.24 or newer, and optionally [golangci-lint](https://golangci-lint.run) v2.

```bash
make build   # bin/podtorrent-agent, bin/mock-engine, bin/mock-network
make test    # go test -race ./...
make lint    # go vet + golangci-lint
make dev     # run all three together locally
```

## Changes

- Keep the agent small: standard library plus `github.com/coder/websocket`.
  Discuss any new dependency in an issue first.
- Protocol changes go in `spec/protocol-v0.md` in the same pull request as the
  code, and must stay backward compatible (new optional fields or message
  types only).
- Add or extend a test in `internal/e2e_test.go` for behaviour a node or
  client can observe.
- CI (build, tests, lint, cross-compile, container build) must pass.
- Commit messages: imperative summary line, then why.

## Releases

Maintainers push a `vX.Y.Z` tag; `.github/workflows/release.yml` builds the
binaries, `SHA256SUMS` and the container image. Update `Name` in
`internal/agent/agent.go` and `CHANGELOG.md` first.
