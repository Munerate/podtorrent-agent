# Roadmap

## Phase 1: one node, real traffic

| Item | Status |
| --- | --- |
| Outbound TLS WebSocket link, node token, reconnect, status heartbeat | Done |
| Forward `/v1/chat/completions` unchanged, streaming, cancel | Done |
| Manifest, per-model slots | Done |
| Engine check at start (`GET /v1/models`) | Done |
| Protocol spec v0 | Done |
| Mock engine and mock network for operators | Done |
| Release workflow: binaries for 4 platforms, multi-arch container image | Done (no release published yet) |
| Reload the manifest without a restart (`manifest` message) | Done |
| `/metrics` on localhost | Done |
| Conformance tests any third-party agent can run (`podtorrent-conformance`) | Done |

## Phase 2: batching and caching

| Item | Status |
| --- | --- |
| Manifest: tiers, per-tier and per-model prices, cached-input price, interactive reserve | Done |
| Cached prompts in the manifest; id defined in the spec | Done |
| Inbound mode: listener, node-token check, TLS and mutual TLS | Done |
| Private links: Cloudflare Tunnel (standard), operator's own network via a connector | Documented; tunnel provisioning per node is an operations step |

## Phase 3: money and trust

Signed receipts for every request, open and verified trust levels, operator
reports including tokens per watt, quality checks, a public demand board and
model-swap suggestions within the operator's approved list.
