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
| `cmd/mock-network` | Fake network: accepts nodes, serves `/v1/chat/completions` and `/v1/videos` |
| `cmd/podtorrent-conformance` | Protocol conformance checker (plays the network) |
| `internal/conformance` | The checks behind it |
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
  context, registered in `inFlight` so `cancel` can abort it. Only the
  requests in `protocol.Allowed` (chat, and the video job paths) are
  forwarded. The engine's body is read in 16 KiB pieces and sent as
  `response_chunk`s (base64 for video content), then `response_end`.
- **Video jobs:** after an accepted `POST /v1/videos`, `watchJob` keeps the
  slot and checks `GET /v1/videos/{id}` on the engine every `JobPoll` until
  `completed` or `failed`, then frees the slot and sends the receipt on the
  current session. Status and content requests take no slot.
- WebSocket writes from many goroutines are safe: `coder/websocket` allows
  concurrent writers.

## Slots, reload and metrics

Slots are counters per model (`busy`) checked against the current manifest,
so `SetManifest` can change models and slot counts while requests are in
flight. Each session registers a channel; `SetManifest` signals it and the
session sends a `manifest` message and a fresh `status`. `metrics.go` keeps
counters and serves them in Prometheus text format.

## Inbound mode (`internal/agent/inbound.go`)

`InboundHandler` serves `GET /v0/node/connect`: it checks the bearer token
against the node token (constant-time), accepts the WebSocket, and runs the
same `Serve` loop as outbound mode. A newer connection closes the older one.
`TLSConfig` loads the server certificate and, with a client CA, requires and
verifies client certificates. Outbound and inbound share all protocol code
through `Serve(ctx, conn, peer)`.

## Design rules

- The agent forwards bodies unchanged and never inspects or logs prompt
  content.
- No inbound listener unless `-listen` is set, no filesystem access beyond the
  manifest and TLS files, no dependency other than `github.com/coder/websocket`.
- Protocol changes are additive: new message types and optional fields.
  Unknown types are ignored on both ends.
