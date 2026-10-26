# Prompts for your coding agent

Copy-paste prompts for setting up a node with an AI coding agent (Claude Code,
Codex, Cursor, or similar) running **on the machine that runs your model**.
Each prompt is self-contained: it tells the agent what PodTorrent is and where
to look. The agent asks you what you run, checks the rest itself, and asks
before installing anything, opening ports or starting services.

| Prompt | Use it to | Changes anything? |
| --- | --- | --- |
| [1. Node profile](#1-node-profile) | Collect your engine details and a draft manifest to send to PodTorrent | No (read-only) |
| [2. Launch a video model](#2-launch-a-video-model) | Expose a video model with the job API, test it locally, connect it | Yes, after asking |
| [3. Launch a text model](#3-launch-a-text-model) | The same for a chat model | Yes, after asking |

**Never paste your node token into the chat.** You set it yourself as
`PODTORRENT_NODE_TOKEN` when you connect for real.

## 1. Node profile

```text
Context: PodTorrent is a network that sends AI inference requests to
operators' machines. A machine joins by running podtorrent-agent, which
connects out to the network and forwards requests to a local engine that
speaks an OpenAI-style HTTP API. The agent's repo is
https://github.com/Munerate/podtorrent-agent. Clone it (or fetch the files)
and read only what you need:
- spec/protocol-v0.md, sections "Manifest" and "Video": what a node declares,
  and the job API a video engine must serve.
- docs/operator-guide.md, "What you need" and "Manifest reference".
- examples/manifest.json: a manifest to copy.

Task (READ-ONLY: don't install, start, stop or change anything):
1. First ask me, in one message: which model(s) I want to serve, what runs
   them (engine/software and how I start it), and whether it's text or video.
2. Then check what you can yourself instead of asking: OS/arch, GPUs and
   VRAM, the engine's version, where it listens, its endpoints (does it
   already serve GET /v1/models and, for text, POST /v1/chat/completions
   with a final `usage` chunk; for video, POST /v1/videos,
   GET /v1/videos/{id} and GET /v1/videos/{id}/content?), output format and
   size, and outbound HTTPS access. Ask me only what you can't find:
   typical time per clip or tokens/s, durations/resolutions, how many jobs or
   requests can run at once, binary or Docker.
3. Write ./podtorrent-node/profile.md (one section per model; mark guesses)
   and ./podtorrent-node/manifest.json (per examples/manifest.json; add
   "modality": "video" for video models; price as a TODO for me: USD per
   second of video, or per million tokens for text).
No tokens, passwords, private hostnames or personal details in either file.
Show me the profile; I'll send it to PodTorrent.
```

## 2. Launch a video model

```text
Context: PodTorrent is a network that sends AI inference requests to
operators' machines. A machine joins by running podtorrent-agent, which
connects out to the network and forwards requests to a local engine. The
agent's repo is https://github.com/Munerate/podtorrent-agent; clone it.
What you need from it:
- spec/protocol-v0.md, sections "Manifest" and "Video": the contract below.
- docs/operator-guide.md, "Test before going live".
- cmd/: podtorrent-agent, mock-network (a local stand-in for the network)
  and mock-engine (a reference video engine). Build all with Go 1.24+:
  `go build -o bin/ ./cmd/...`.
If ./podtorrent-node/ has a profile and manifest, use them. Otherwise ask me
which video model I run and what runs it, then check the details yourself.

Rules: ask before installing, downloading, opening ports, or creating
services. Never ask for or print my node token. Bind anything you start to
127.0.0.1. Don't log or keep prompts or clips beyond what the engine keeps.

Contract: podtorrent-agent talks to one base URL (the "engine URL") that must serve
  GET  /v1/models               -> {"object":"list","data":[{"id":"<model id>","object":"model"}]}
  POST /v1/videos               {"model","prompt","seconds","size",...} -> job object, at once
  GET  /v1/videos/{id}          -> job object
  GET  /v1/videos/{id}/content  -> clip bytes (e.g. video/mp4) when completed; 404 if unknown
Job object: {"id","object":"video","model","status","progress","seconds","size","created_at"};
status is queued | in_progress | completed | failed; ids use only [A-Za-z0-9_.-].
Keep finished clips at least 1 hour. ./bin/mock-engine is a working example.

Steps:
1. If my engine already serves the contract, skip to 3.
2. Otherwise write a small adapter in front of it: one file plus a README,
   in a language already on this machine. It starts jobs in the background,
   maps the engine's states to the four statuses, caps concurrent jobs to
   what the hardware handles (queue the rest), rejects bad ids, serves the
   output file, and deletes clips after 24 h.
3. Check with curl: list models; make a 2-4 s clip (prompt "a lighthouse at
   dusk, slow pan"); poll to completed; download it and confirm it's a valid
   video; unknown id gives 404. Note the time per clip.
4. Write ./podtorrent-node/manifest.json: node id and price per second from
   me, "modality": "video", "slots" = concurrent jobs.
5. Test through the mock network:
     ./bin/mock-network      (127.0.0.1:8787; client key dev-api-key; node token dev-node-token)
     PODTORRENT_NODE_TOKEN=dev-node-token ./bin/podtorrent-agent \
       -network ws://127.0.0.1:8787/v0/node/connect \
       -engine <engine URL> -manifest ./podtorrent-node/manifest.json
   (Windows PowerShell: $env:PODTORRENT_NODE_TOKEN="dev-node-token"; bin\podtorrent-agent.exe ...)
   Submit to http://127.0.0.1:8787/v1/videos with "Authorization: Bearer
   dev-api-key", poll, download the content. The agent log must be clean.
6. Stop the test processes. Tell me what you installed or wrote, how to
   start everything (and keep it running after reboot, if I want), the time
   per clip, and the real connect command, which I'll run myself:
     PODTORRENT_NODE_TOKEN=<my token> podtorrent-agent -engine <engine URL> -manifest ./podtorrent-node/manifest.json
```

## 3. Launch a text model

```text
Context: PodTorrent is a network that sends AI inference requests to
operators' machines. A machine joins by running podtorrent-agent, which
connects out to the network and forwards requests to a local
OpenAI-compatible engine. The agent's repo is
https://github.com/Munerate/podtorrent-agent; clone it. Read
docs/operator-guide.md ("What you need", "Manifest reference", "Test before
going live", "Check conformance") and examples/manifest.json. Build the
tools with Go 1.24+: `go build -o bin/ ./cmd/...`.
If ./podtorrent-node/ has a profile and manifest, use them. Otherwise ask me
which model I run and what runs it, then check the details yourself.

Rules: ask before installing, downloading, opening ports, or creating
services. Never ask for or print my node token. Bind to 127.0.0.1.

1. Confirm the engine on 127.0.0.1 lists the exact model id at
   GET /v1/models and serves POST /v1/chat/completions with and without
   "stream": true; with "stream_options": {"include_usage": true} the last
   chunk before `data: [DONE]` must have usage.prompt_tokens and
   usage.completion_tokens (payment is by these). If not, tell me the
   setting or version that fixes it.
2. Write ./podtorrent-node/manifest.json: node id and price from me,
   "slots" = concurrent requests that fit, quant and context_max as set.
3. Test with ./bin/mock-network as in "Test before going live", then run
   ./bin/podtorrent-conformance as in "Check conformance".
4. Give me the start commands and the real connect command; I'll run it.
```
