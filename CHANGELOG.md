# Changelog

## 0.1.0 (2026-10-24)

- Outbound WebSocket link with reconnect and status heartbeat.
- Forwarding of `/v1/chat/completions`, streaming and cancel.
- Manifest with per-model slots; engine check at start.
- Protocol spec v0.
- Mock engine and mock network.
- Inbound mode (`-listen`), with TLS and mutual TLS.
- Manifest reload without restart (file change or SIGHUP), sent as a
  `manifest` message.
- Prometheus metrics (`-metrics-listen`).
- `podtorrent-conformance` tool for checking any node implementation.
- Default network: `wss://api.podtorrent.com/v0/node/connect`.
- Manifest: tiers, batch and cached-input prices, per-model prices,
  `interactive_reserve`, `cached_prompts`.
