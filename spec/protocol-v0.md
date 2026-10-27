# PodTorrent node protocol, v0

Status: draft, phase 1. Anything that implements this document is a valid
PodTorrent node; `podtorrent-agent` is the reference implementation, and
`podtorrent-conformance` checks any implementation against it.

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
{ "type": "hello", "protocol": 0, "agent": "podtorrent-agent/0.4.0",
  "manifest": { ...see Manifest... }, "receipt_key": "<base64 Ed25519 public key>" }
```

`receipt_key` is optional: a node that sends it signs receipts (see Receipts).

`status` — free capacity, in total and per model. Sent after `hello`, whenever
it changes, and every 30 s. The network routes on `free_by_model`.

```json
{ "type": "status", "free_slots": 31,
  "free_by_model": { "meta-llama/llama-3.3-70b": 31 } }
```

`manifest` — the node's manifest changed (for example the operator added a
model). Replaces the one from `hello`; followed by a `status`. Requests in
flight are unaffected.

```json
{ "type": "manifest", "manifest": { ...see Manifest... } }
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

For binary bodies (video content) the node sets `"encoding": "base64"` and
`data` is the bytes in standard base64. Each chunk is encoded on its own.
Without `encoding`, `data` is UTF-8 text as above.

`response_end` — the response finished.

```json
{ "type": "response_end", "id": "req_123" }
```

`response_error` — the request failed before or during the response.

```json
{ "type": "response_error", "id": "req_123", "message": "engine unreachable" }
```

`receipt` — the node's signed account of a finished request, sent after
`response_end`. Optional; see Receipts.

```json
{ "type": "receipt", "id": "req_123", "receipt": "{\"id\":\"req_123\",...}", "sig": "<base64>" }
```

### network → node

`welcome` — reply to `hello`.

```json
{ "type": "welcome", "protocol": 0, "node": "example-node-01",
  "receipt_key": "<base64 Ed25519 public key>" }
```

`receipt_key` is optional: the network's key for its receipts.

`request` — one inference request to forward to the local engine.

```json
{ "type": "request", "id": "req_123", "method": "POST",
  "path": "/v1/chat/completions", "body": { ...OpenAI request JSON... } }
```

`method` and `path` are always one of the allowed requests:

| Method | Path | Body | Slot |
| --- | --- | --- | --- |
| `POST` | `/v1/chat/completions` | OpenAI chat request | held until `response_end` |
| `POST` | `/v1/videos` | video job (see Video) | held until the job ends |
| `GET` | `/v1/videos/{id}` | none | none |
| `GET` | `/v1/videos/{id}/content` | none | none |

`{id}` is the engine's job id. The node forwards `body` unchanged. The network never sends more concurrent
requests than the node's free slots, and a node may refuse extras with
`response_error`.

`cancel` — the client went away; the node should abort the request.

```json
{ "type": "cancel", "id": "req_123" }
```

`receipt` — the network's signed account of the same request, sent in reply
to the node's `receipt`. Same shape as the node's.

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
      "context_max": 32768, "slots": 32, "interactive_reserve": 4, "power_watts": 1400,
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
| `power_watts` | Optional, declared by the operator: typical power draw in watts while serving at full load. Node-wide, or per model in `models[].power_watts`. Used for tokens per watt on the operator dashboard; not used for routing. |
| `models[].cached_prompts` | Openings of the conversation the engine keeps processed (see below). |
| `models[].modality` | `text` (default) or `video`. Video models get only the video requests; text models only chat. |
| `per_video_second` | In `price` (node-wide or per model, and in `batch`): USD per second of generated video. Used for `video` models instead of the token prices, which may then be omitted. |

- `models[].id` is matched exactly against the client's `model`.
- `slots` is the most concurrent requests the node accepts for that model.
  Each model has its own pool; a request for a model not in the manifest, or
  whose pool is full, gets `response_error`.

## Video

Video generation is an asynchronous job, shaped after OpenAI's videos API.
The network sends `POST /v1/videos` with a body such as

```json
{ "model": "example/video-1", "prompt": "…", "seconds": "6", "size": "1280x720" }
```

and the engine answers with a job object:

```json
{ "id": "video_abc", "object": "video", "model": "example/video-1", "status": "queued",
  "progress": 0, "seconds": "6", "size": "1280x720", "created_at": 1761400000 }
```

`status` is `queued`, `in_progress`, `completed` or `failed`; `seconds` may be
a string or a number. The network later asks for `GET /v1/videos/{id}` (same
object) and, once `completed`, `GET /v1/videos/{id}/content` (the clip,
usually `video/mp4`, sent as base64 chunks).

- The job holds its slot from the `POST` until the engine reports `completed`
  or `failed`; the node watches the job itself and shows the slot as busy in
  `status` until then. Status and content requests take no slot.
- The clip stays on the node; the network passes it through and never stores
  it. A node keeps finished clips for at least one hour and may delete them
  after that (content requests then get the engine's 404).
- When the job ends, the node sends its `receipt` with the `id` of the `POST`
  request, `status` 200 for `completed` or 500 for `failed`, the token counts
  `null`, and `video_seconds` (the engine's `seconds` as a number) and `size`.
  The network compares `video_seconds` too, and prices it at
  `per_video_second`.

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

## Receipts

Both sides sign what they saw of each finished request, so neither can claim
tokens the other did not see. Payment is made only for requests whose two
receipts agree. Receipts hold counts only, never prompt or response content.

- Keys are Ed25519. A node sends its public key as `receipt_key` in `hello`
  (raw 32 bytes, standard base64); the network sends its own in `welcome`
  and also publishes it at `GET /v0/network/keys`. A node may use a new key
  on each start.
- `receipt` is a JSON object serialised to a string; `sig` is the Ed25519
  signature of that string's UTF-8 bytes, in standard base64. Verify the
  string exactly as received, then parse it.
- After `response_end`, the node sends its receipt:

  | Field | Meaning |
  | --- | --- |
  | `id` | Request id. |
  | `node` | Node id from `welcome`. |
  | `model` | `model` from the request body. |
  | `status` | Engine HTTP status. |
  | `prompt_tokens`, `completion_tokens`, `cached_tokens` | From the engine's `usage` (`prompt_tokens_details.cached_tokens`), or `null` if it reported none. |
  | `video_seconds`, `size` | Video jobs only (see Video); absent otherwise. |
  | `ts` | Unix milliseconds when the response ended. Informational. |

- The network compares `id`, `node`, `model`, `status`, the three token
  counts and `video_seconds` with its own reading of the response. It replies with its own
  receipt: the same fields with its own values, plus `tier` and `cost_usd`
  (at the node's manifest price; `null` for error statuses or missing
  counts), and `node_sig`, the node's signature it accepted (or `null`).
- A request whose receipts differ, or whose node sent none, is recorded
  but not paid. Nodes that send no `receipt_key` keep working as before.
- Extra fields are allowed in either receipt and are ignored when comparing.
