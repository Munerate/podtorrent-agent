# Operator guide

How to connect your inference engine to the PodTorrent network with
`podtorrent-agent`.

Using an AI coding agent on your machine? [Prompts for your coding
agent](agent-prompts.md) has ready-made prompts that survey your engine, write
an adapter if your video engine needs one, and test the node locally.

## What you need

- An inference engine that serves the OpenAI API on an address the agent can
  reach, for example `http://127.0.0.1:8000`. It must support:
  - `GET /v1/models`, listing the model ids you put in your manifest;
  - `POST /v1/chat/completions`, both with and without `"stream": true`;
  - token counts in `usage`, and in the final stream chunk when the request
    has `"stream_options": {"include_usage": true}` (the network sets this, to
    meter every request).
  - For video models (`"modality": "video"` in the manifest, priced with
    `price.per_video_second`): the OpenAI-style job API `POST /v1/videos`,
    `GET /v1/videos/{id}` and `GET /v1/videos/{id}/content`. Keep finished
    clips for at least an hour. See the protocol spec, section Video. If your
    model runs in ComfyUI, use [`podtorrent-comfy-video`](comfyui-video.md).
    If your engine has another API, put a small adapter in front of it;
    prompt 2 in [agent-prompts.md](agent-prompts.md) has your coding agent
    write one.
- Outbound HTTPS (port 443) from the agent to the network, or one of the
  inbound and private-link setups in [connection modes](connection-modes.md).
- A **node token** from the PodTorrent team. Keep it secret; it identifies
  your node.

## Install

**Binary.** Download the file for your platform from
[Releases](https://github.com/Munerate/podtorrent-agent/releases)
(linux/amd64, linux/arm64, darwin/arm64, windows/amd64) and check it against
`SHA256SUMS`. Or build it: `make build` (Go 1.24+), output in `bin/`.

**Container.** `ghcr.io/munerate/podtorrent-agent:<version>`. It is distroless,
runs as a non-root user and has no shell.

## Configure

### Manifest reference

The manifest is a JSON file that tells the network what your node offers.
Every value is a claim the network will check against measured behaviour.

```json
{
  "node": "example-node-01",
  "models": [
    {
      "id": "meta-llama/llama-3.3-70b",
      "quant": "fp8",
      "context_max": 32768,
      "slots": 32,
      "speed": { "single_tps": 20, "batch_tps": 250 }
    }
  ],
  "price": { "per_mtok_in": 0.05, "per_mtok_out": 0.40 }
}
```

| Field | Required | Meaning |
| --- | --- | --- |
| `node` | yes | A name for this node, shown in your reports. |
| `models[].id` | yes | Model id exactly as your engine's `/v1/models` lists it; clients request it by this id. |
| `models[].slots` | yes | Most requests for this model the node serves at once. The network never sends more. |
| `models[].quant` | no | Precision actually running (`bf16`, `fp8`, `q4_k_m`, ...). |
| `models[].context_max` | no | Longest prompt plus output accepted, in tokens. |
| `models[].speed.single_tps` | no | Output tokens/s for one request alone. |
| `models[].speed.batch_tps` | no | Total output tokens/s with all slots busy. |
| `models[].interactive_reserve` | no | Slots batch work may not take, kept for interactive requests. |
| `models[].cached_prompts` | no | Conversation openings your engine keeps processed: `[{ "name", "sha256", "tokens" }]`. See [Cached prompts](#cached-prompts). |
| `models[].price` | no | Overrides `price` for this model. |
| `tiers` | no | Work you accept: `["interactive", "batch"]` (default both). |
| `price.per_mtok_in`, `price.per_mtok_out` | no | Your price per million input and output tokens, in USD. |
| `price.per_mtok_in_cached` | no | Price for input tokens served from your cache (default `per_mtok_in`). |
| `price.batch` | no | Your batch-tier price, same fields (default the main price). |

### Tiers and pricing

- **Interactive** requests have a person waiting: they go to the node with
  the most free slots, for the best speed per request, and wait at most 10 s
  for a slot.
- **Batch** requests care about cost and throughput: they go to the node with
  the *fewest* free slots that still has one, so batches fill up (cheaper per
  token on batched hardware), and wait up to 2 minutes for a slot. They never
  take `interactive_reserve` slots.
- You set your own prices per tier and per model. Clients can set a maximum
  price; nodes above it don't get the request. Clients see the price range
  per model and tier on the network's offers list.

### Cached prompts

If clients send the same long opening (a system prompt, an agent framework's
instructions), keep it processed in your engine's prefix cache and advertise
it. The network sends requests that start with it to you first; you skip the
work, and cached input tokens are billed at `per_mtok_in_cached`.

The id is a SHA-256 of the opening messages (definition in the
[spec](../spec/protocol-v0.md#cached-prompts)). Get it from the PodTorrent
team or compute it with the spec's formula.

### Flags and environment

| Flag | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `-network` | `PODTORRENT_NETWORK` | `wss://api.podtorrent.com/v0/node/connect` | Network link URL |
| `-engine` | `PODTORRENT_ENGINE` | `http://127.0.0.1:8000` | Your engine's base URL (without `/v1`) |
| `-manifest` | `PODTORRENT_MANIFEST` | `manifest.json` | Manifest file |
| — | `PODTORRENT_NODE_TOKEN` | — | Node token (required) |
| `-skip-engine-check` | — | off | Don't check `/v1/models` at start |
| `-version` | — | — | Print the version and exit |
| `-metrics-listen` | `PODTORRENT_METRICS_LISTEN` | off | Serve Prometheus metrics at `/metrics`, e.g. `127.0.0.1:9100` |
| `-listen` and TLS flags | — | — | Inbound mode; see [connection modes](connection-modes.md) |

## Run

### As a binary

```bash
export PODTORRENT_NODE_TOKEN=...
podtorrent-agent -engine http://127.0.0.1:8000 -manifest /etc/podtorrent/manifest.json
```

To keep it running, use your service manager (for example a systemd unit with
`Restart=always`, `DynamicUser=yes` and `NoNewPrivileges=yes`).

### As a container

```bash
docker run -d --name podtorrent-agent --restart unless-stopped \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  -e PODTORRENT_NODE_TOKEN=... \
  -v /etc/podtorrent/manifest.json:/manifest.json:ro \
  ghcr.io/munerate/podtorrent-agent:<version> \
  -manifest /manifest.json -engine http://engine:8000
```

Put the agent on a network where it can reach only your engine and the
internet. It needs no GPU, no volumes other than the manifest, and no host
networking.

## What happens when it runs

1. Checks the engine lists every manifest model (waits up to a minute for the
   engine to start).
2. Connects to the network, sends the manifest, receives `welcome`.
3. Reports free slots per model now, whenever they change, and every 30 s.
4. For each request: forwards it to your engine and streams the answer back.
   It refuses requests for models it doesn't serve or when that model's slots
   are full.
5. If the link drops, reconnects with backoff (1 s up to 60 s).

## Change the manifest without a restart

Edit the manifest file. The agent notices within 5 seconds (or at once on
`SIGHUP`), checks the engine serves every model in it, and sends it to the
network. Requests in flight carry on. An invalid file is logged and ignored.

## Metrics

With `-metrics-listen 127.0.0.1:9100`, `GET /metrics` returns Prometheus text:

| Metric | Meaning |
| --- | --- |
| `podtorrent_agent_connected` | 1 while the link is up |
| `podtorrent_agent_connects_total` | Links established since start |
| `podtorrent_agent_slots{model}` | Slots from the manifest |
| `podtorrent_agent_busy_slots{model}` | Requests in flight |
| `podtorrent_agent_requests_total{model,outcome}` | Requests: `ok`, `engine_error`, `failed`, `refused` |

Keep it on localhost or your monitoring network; it has no authentication.

## Check conformance

`podtorrent-conformance` plays the network and checks a node follows the
protocol, including with your real engine:

```bash
podtorrent-conformance -listen 127.0.0.1:8787 -token test &
PODTORRENT_NODE_TOKEN=test podtorrent-agent -network ws://127.0.0.1:8787/v0/node/connect -engine http://127.0.0.1:8000 -manifest manifest.json
```

For an inbound node: `podtorrent-conformance -dial http://127.0.0.1:8443/v0/node/connect -token <node token>`.
It exits 0 and prints `conformant` when every check passes. Writing your own
agent? Run this against it.

## Test before going live

Run the mock network locally and point the agent at your real engine:

```bash
make build
./bin/mock-network &                       # :8787, client key dev-api-key, node token dev-node-token
PODTORRENT_NODE_TOKEN=dev-node-token ./bin/podtorrent-agent \
  -network ws://127.0.0.1:8787/v0/node/connect \
  -engine http://127.0.0.1:8000 -manifest manifest.json
curl -N localhost:8787/v1/chat/completions -H 'Authorization: Bearer dev-api-key' \
  -d '{"model":"<your model id>","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}'
```

Check that the stream ends with a chunk containing `usage`, then `data: [DONE]`.

## Troubleshooting

| Log message | Cause and fix |
| --- | --- |
| `PODTORRENT_NODE_TOKEN is not set` | Set the environment variable. |
| `engine at ... not reachable` | The engine isn't listening at `-engine`, or the container can't reach it. |
| `engine ... does not list these manifest models` | Make `models[].id` match `/v1/models` exactly. |
| `link down, reconnecting ... 401` | Wrong or revoked node token. |
| `link down, reconnecting ... dial` | No outbound access to the network URL; check firewall and proxy. |
| `request failed ... engine unreachable` | The engine stopped or restarted mid-request. |

## Privacy

Your node sees the prompts and answers it serves. The agent itself logs no
prompt or answer content, only request ids and errors.
