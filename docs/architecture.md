# Architecture

## Where the agent sits

```
                 PodTorrent network (operated by PodTorrent)
client ──HTTPS──► gateway ──► scheduler ──► node link
                                                ▲
                                                ║ one WebSocket over TLS,
                                                ║ opened by the agent
operator's machine                              ║
  podtorrent-agent ═════════════════════════════╝
        │ HTTP (local)
        ▼
  operator's engine (OpenAI API)
```

The network's services are not in this repo. This repo has the agent, the
protocol spec, and stand-ins for both ends (`mock-engine`, `mock-network`).

## Code layout

| Path | What |
| --- | --- |
| `cmd/podtorrent-agent` | CLI: flags, manifest loading, engine check, signal handling |
| `cmd/mock-engine` | Fake OpenAI engine that echoes the prompt |
| `cmd/mock-network` | Fake network: accepts nodes, serves `/v1/chat/completions` |
| `internal/protocol` | Message and manifest types for protocol v0 |
| `internal/agent` | The link: connect, hello/welcome, status, forwarding, reconnect |
| `internal/mockengine`, `internal/mocknet` | Handlers behind the two mock commands |
| `internal/e2e_test.go` | Engine, network and agent wired together in one process |
| `spec/` | The protocol specification (normative) |

## The link (`internal/agent`)

- `Run` loops `session` with exponential backoff (1 s to 60 s, reset after a
  session that lasted over a minute).
- `session` dials, sends `hello` with the manifest, requires `welcome`, then
  reads messages until the connection fails.
- **Slots:** one buffered channel per manifest model, sized by `slots`. A
  `request` takes a slot without blocking or is refused with
  `response_error`. `status` reports free slots per model
  (`free_by_model`) and in total.
- **Forwarding:** each request runs in its own goroutine with a cancellable
  context, registered in `inFlight` so `cancel` can abort it. Only
  `POST /v1/chat/completions` is allowed. The engine's body is read in 16 KiB
  pieces and sent as `response_chunk`s, then `response_end`.
- WebSocket writes from many goroutines are safe: `coder/websocket` allows
  concurrent writers.

## Design rules

- The agent forwards bodies unchanged and never inspects or logs prompt
  content.
- No inbound listener, no filesystem access beyond the manifest, no
  dependency other than `github.com/coder/websocket`.
- Protocol changes are additive: new message types and optional fields.
  Unknown types are ignored on both ends.
