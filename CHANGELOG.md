# Changelog

## Unreleased

- Signed receipts: the agent makes an Ed25519 key at start, sends it in
  `hello`, signs a `receipt` (token counts only) after each response, and
  checks the network's countersigned receipt. New metric
  `podtorrent_agent_network_receipts_total`. The mock network countersigns.

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
