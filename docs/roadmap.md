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
| Reload the manifest without a restart | Next |
| `/metrics` on localhost | Next |
| Conformance tests any third-party agent can run against `mock-network` | Next |

## Phase 2: batching and caching

Interactive and batch tiers, cached-prompt advertising and routing, inbound
mode (the network connects to the operator's endpoint over mutual TLS) and a
private-link option (the agent connects over the operator's own VPN).

## Phase 3: money and trust

Signed receipts for every request, open and verified trust levels, operator
reports including tokens per watt, quality checks, a public demand board and
model-swap suggestions within the operator's approved list.
