# PodTorrent node protocol, v0

Status: draft, phase 1. Anything that implements this document is a valid
PodTorrent node; `podtorrent-agent` is the reference implementation.

## Transport

There are two ways to open the link. Everything after the WebSocket is open
is identical: the node always sends `hello` first.

**Outbound mode (default).**

- The node opens **one outbound WebSocket** to the network:
  `wss://<network>/v0/node/connect`. In production this is always TLS
  (`wss://`). Local test servers may use `ws://`.
- The node authenticates with its node token:
  `Authorization: Bearer <node token>`.

**Inbound mode.** For operators who don't allow outbound connections, or want
the link on their own network:

- The node listens on `https://<node>/v0/node/connect` and the network dials
  it, sending `Authorization: Bearer <node token>`. The node must reject any
  other token.
- Over the internet (`public`), the node should require the network's client
  certificate (mutual TLS). Over a private link (`cloudflare_tunnel`,
  `wireguard`), the link itself provides the protection and plain HTTP behind
  it is acceptable.
- The network re-dials within a minute after a disconnect. If a second
  connection arrives, the node should close the first.

**Both modes:**

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

Sent in `hello`. Extra fields are allowed and ignored.

```json
{
  "node": "example-node-01",
  "tiers": ["interactive", "batch"],
  "price": {
    "per_mtok_in": 0.05, "per_mtok_out": 0.40, "per_mtok_in_cached": 0.01,
    "batch": { "per_mtok_in": 0.02, "per_mtok_out": 0.20 }
  },
  "models": [
    { "id": "meta-llama/llama-3.3-70b", "quant": "fp8",
      "context_max": 32768, "slots": 32, "interactive_reserve": 4,
      "speed": { "single_tps": 20, "batch_tps": 250 },
      "cached_prompts": [
        { "name": "agent-system-v3", "sha256": "9c1e…", "tokens": 20480 }
      ] }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `tiers` | Work the node accepts: `interactive` (one person waiting), `batch` (throughput, can queue). Default both. |
| `price` | USD per million tokens. `per_mtok_in_cached` applies to input tokens served from cache (default `per_mtok_in`). `batch` is the batch-tier price (default the main price). |
| `models[].price` | Same shape; overrides `price` for that model. |
| `models[].interactive_reserve` | Slots that batch work may not use, kept free for interactive requests. |
| `models[].cached_prompts` | Openings of the conversation the engine keeps processed (see below). |

- `models[].id` is matched exactly against the client's `model`.
- `slots` is the most concurrent requests the node accepts for that model.
  Each model has its own pool; a request for a model not in the manifest, or
  whose pool is full, gets `response_error`.

## Cached prompts

A cached prompt is a fixed opening of a conversation: the first *k* messages
(for example a long system prompt). Its id is

```
sha256_hex( JSON.stringify( messages[0:k].map(m => ({ role: m.role, content: m.content })) ) )
```

using JSON with no spaces and keys in the order `role`, `content`. The
network matches openings of up to 8 messages. Clients can get the id from
the network (`POST /v1/prompts`). A node that advertises an id gets requests
that open with those messages, and should keep them processed in its engine's
prefix cache.

## Not in v0

Signed receipts are phase 3; they will be added as new message types without
breaking v0 nodes.
