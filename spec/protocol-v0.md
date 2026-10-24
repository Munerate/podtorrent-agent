# PodTorrent node protocol, v0

Status: draft, phase 1. Anything that implements this document is a valid
PodTorrent node; `podtorrent-agent` is the reference implementation.

## Transport

- The node opens **one outbound WebSocket** to the network:
  `wss://<network>/v0/node/connect`. In production this is always TLS
  (`wss://`). Local test servers may use `ws://`.
- The node authenticates with its node token:
  `Authorization: Bearer <node token>`.
- Every WebSocket message is one **text frame holding one JSON object** with a
  `type` field. Unknown `type`s must be ignored (forward compatibility).
- Keepalive: either side may send WebSocket pings; the node also sends a
  `status` message at least every 30 s. The network drops a node that has sent
  nothing for 90 s.
- On disconnect the node reconnects with exponential backoff (1 s up to 60 s).
  Requests in flight on a dropped link are failed by the network and may be
  retried elsewhere before any token reached the client.

## Messages

### node → network

`hello` — first message after connecting.

```json
{ "type": "hello", "protocol": 0, "agent": "podtorrent-agent/0.1.0",
  "manifest": { ...see Manifest... } }
```

`status` — free capacity, in total and per model. Sent after `hello`, whenever
it changes, and every 30 s. The network routes on `free_by_model`.

```json
{ "type": "status", "free_slots": 31,
  "free_by_model": { "meta-llama/llama-3.3-70b": 31 } }
```

`response_start` — the engine answered a request.

```json
{ "type": "response_start", "id": "req_123", "status": 200,
  "headers": { "content-type": "text/event-stream" } }
```

`response_chunk` — part of the engine's response body, in order. `data` is the
body bytes as a UTF-8 string (OpenAI responses are JSON or SSE text).

```json
{ "type": "response_chunk", "id": "req_123", "data": "data: {...}\n\n" }
```

`response_end` — the response finished.

```json
{ "type": "response_end", "id": "req_123" }
```

`response_error` — the request failed before or during the response.

```json
{ "type": "response_error", "id": "req_123", "message": "engine unreachable" }
```

### network → node

`welcome` — reply to `hello`.

```json
{ "type": "welcome", "protocol": 0, "node": "example-node-01" }
```

`request` — one inference request to forward to the local engine.

```json
{ "type": "request", "id": "req_123", "method": "POST",
  "path": "/v1/chat/completions", "body": { ...OpenAI request JSON... } }
```

`path` is always one of the allowed paths (v0: `/v1/chat/completions`). The
node forwards `body` unchanged. The network never sends more concurrent
requests than the node's free slots, and a node may refuse extras with
`response_error`.

`cancel` — the client went away; the node should abort the request.

```json
{ "type": "cancel", "id": "req_123" }
```

`error` — fatal; the network closes the link after sending it.

```json
{ "type": "error", "message": "bad token" }
```

## Manifest

Sent in `hello`. v0 uses this subset of the design's manifest; extra fields are
allowed and ignored.

```json
{
  "node": "example-node-01",
  "models": [
    { "id": "meta-llama/llama-3.3-70b", "quant": "fp8",
      "context_max": 32768, "slots": 32,
      "speed": { "single_tps": 20, "batch_tps": 250 } }
  ],
  "price": { "per_mtok_in": 0.05, "per_mtok_out": 0.40 }
}
```

- `models[].id` is matched exactly against the client's `model`.
- `slots` is the most concurrent requests the node accepts for that model.
  Each model has its own pool; a request for a model not in the manifest, or
  whose pool is full, gets `response_error`.

## Not in v0

Receipts and signatures, cached-prompt advertising, tiers, inbound mode and
private link are phase 2 and 3; they will be added as new message types and
manifest fields without breaking v0 nodes.
