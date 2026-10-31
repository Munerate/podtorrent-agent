# podtorrent-agent

[![CI](https://github.com/Munerate/podtorrent-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/Munerate/podtorrent-agent/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

The PodTorrent node agent connects **your own OpenAI-compatible inference
engine** (vLLM, TensorRT-LLM, SGLang, llama.cpp server, or anything else that
speaks the OpenAI API) to the PodTorrent network, so the network can send it
inference requests.

- **Outbound by default.** The agent opens one TLS WebSocket to the network.
  No inbound ports, works behind NAT and strict firewalls. Inbound mode with
  mutual TLS and private links (Cloudflare Tunnel, your own WireGuard) are
  also supported.
- **Least privilege.** One static binary (~6 MB) or a distroless, non-root
  container. No GPU, no host disk, no root. It never downloads models.
- **Your stack, untouched.** Requests go to your engine's
  `/v1/chat/completions` unchanged; responses stream back unchanged.
- **Open protocol.** [`spec/protocol-v0.md`](spec/protocol-v0.md). Any program
  that implements it is a valid node; this is the reference implementation.

```
client ──► PodTorrent gateway ◄══ wss (opened by the agent) ══ podtorrent-agent ──► your engine
```

## Quick start

1. Get a **node token** from the PodTorrent team.
2. Write a manifest describing what you serve
   ([example](examples/manifest.json), [reference](docs/operator-guide.md#manifest-reference)).
3. Run the agent next to your engine:

```bash
PODTORRENT_NODE_TOKEN=... podtorrent-agent \
  -engine http://127.0.0.1:8000 \
  -manifest manifest.json
```

At start the agent checks that your engine lists every model in the manifest
(`GET /v1/models`), then connects and waits for work.

Full instructions, the container setup and troubleshooting are in the
**[operator guide](docs/operator-guide.md)**.

## Try it without the real network

The repo ships a fake engine and a fake network, so you can test locally, and
test your own engine before going live:

```bash
make dev   # mock engine :8000, mock network :8787, agent in between
curl -N localhost:8787/v1/chat/completions -H 'Authorization: Bearer dev-api-key' \
  -d '{"model":"mock/echo-1","stream":true,"messages":[{"role":"user","content":"hi"}]}'
```

## Documentation

| Document | For |
| --- | --- |
| [Operator guide](docs/operator-guide.md) | Installing and running a node, manifest reference, troubleshooting |
| [Prompts for your coding agent](docs/agent-prompts.md) | Letting an AI coding agent on your machine survey your engine and launch a text or video model as a node |
| [ComfyUI video models](docs/comfyui-video.md) | Serving a video model that runs as a ComfyUI workflow, with `podtorrent-comfy-video` |
| [Connection modes](docs/connection-modes.md) | Outbound, inbound with mutual TLS, private links (Cloudflare Tunnel, your own WireGuard) |
| [Protocol spec v0](spec/protocol-v0.md) | Writing your own agent or client for the network; check it with `podtorrent-conformance` |
| [Go API](pkg/) | Building a program on the agent (e.g. a desktop app): `manifest`, `engine`, `node`, `selftest` — see `go doc ./pkg/...` |
| [Architecture](docs/architecture.md) | How the agent works inside |
| [Roadmap](docs/roadmap.md) | What's done and what's next |
| [Contributing](CONTRIBUTING.md) | Building, testing, sending changes |
| [Security](SECURITY.md) | Reporting vulnerabilities, threat model |
| [AGENTS.md](AGENTS.md) | Instructions for AI coding agents working in this repo |

## License

[Apache-2.0](LICENSE).
