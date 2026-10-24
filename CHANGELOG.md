# Changelog

## Unreleased (0.1.0)

- Outbound WebSocket link with reconnect and status heartbeat.
- Forwarding of `/v1/chat/completions`, streaming and cancel.
- Manifest with per-model slots; engine check at start.
- Protocol spec v0.
- Mock engine and mock network.
- Inbound mode (`-listen`), with TLS and mutual TLS.
- Manifest: tiers, batch and cached-input prices, per-model prices,
  `interactive_reserve`, `cached_prompts`.
