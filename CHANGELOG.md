# Changelog

## 0.4.0 (2026-10-27)

- `podtorrent-comfy-video`: serves the video job API in front of a ComfyUI
  workflow (exported in API format, with placeholders for prompt, size,
  frames and seed), so ComfyUI video models can join without an adapter of
  their own. Quality modes as named workflows, jobs kept across restarts,
  clips deleted after `-keep` when given the output folder, and frame
  grids of n×multiple+offset (`-frame-multiple`, `-frame-offset`). See
  `docs/comfyui-video.md`.
- Video generation (spec: Video): models may set `modality: "video"` and
  `price.per_video_second`. The agent forwards `POST /v1/videos`, holds the
  slot until the engine finishes the job (checked every `JobPoll`, 2 s), then
  sends a receipt with `video_seconds` and `size`. Job status and content
  (`GET /v1/videos/{id}`, `/content`) take no slot; content goes as base64
  `response_chunk`s (`encoding: "base64"`).
- Mock engine runs fake video jobs; mock network routes `/v1/videos`.
- `docs/agent-prompts.md`: prompts for an operator's coding agent to survey
  the engine, write a video job adapter, and test the node locally.

## 0.3.0 (2026-10-25)

- Manifest: optional declared `power_watts`, node-wide or per model, passed
  to the network for tokens per watt on the operator dashboard.

## 0.2.0 (2026-10-25)

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
