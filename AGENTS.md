# AGENTS.md

Instructions for AI coding agents working in this repository. Humans: see
[README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## What this is

The public PodTorrent node agent (Go): an outbound WebSocket client that
forwards OpenAI-compatible requests from the PodTorrent network to an
operator's local engine. The normative protocol is `spec/protocol-v0.md`.

## Commands

```bash
make build   # must succeed
make test    # go test -race ./... ; must pass
make lint    # go vet + golangci-lint (config in .golangci.yml); must report 0 issues
```

Go 1.24+ is required (`go.mod`). Run `gofmt -w` on changed files.

## Map

- `internal/agent/agent.go` — link, slots, forwarding. Most changes land here.
- `internal/protocol/protocol.go` — wire types. Keep in sync with `spec/protocol-v0.md`.
- `internal/mocknet`, `internal/mockengine` — test doubles, also shipped to operators.
- `internal/e2e_test.go` — end-to-end tests; extend for observable behaviour.
- `cmd/*` — thin CLIs; no logic beyond flags and wiring.

## Rules

- Protocol changes are additive only (optional fields, new message types) and
  must update `spec/protocol-v0.md` in the same change.
- Never log prompt or response content. Never add an inbound listener, file
  access beyond the manifest, or a call to any engine path other than
  `POST /v1/chat/completions` and `GET /v1/models`.
- No new dependencies without the maintainers' agreement.
- This repo is public: no customer or operator names, private hostnames,
  account ids, tokens or internal discussion in code, docs, tests or commit
  messages. Use neutral examples such as `example-node-01`.
- The network's server code lives in a separate private repository; don't
  reference its internals here beyond what the spec says.
