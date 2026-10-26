# Prompts for your coding agent

Copy-paste prompts for setting up a node with an AI coding agent (Claude Code,
Codex, Cursor, or similar) running **on the machine that runs your model**.
The agent reads your setup, fills in the details the network needs, and gets
your model serving through `podtorrent-agent`. You stay in charge: the
prompts tell it to ask before installing anything, opening ports or starting
long-running services.

| Prompt | Use it to | Changes anything? |
| --- | --- | --- |
| [1. Engine survey](#1-engine-survey) | Collect your engine details and a draft manifest to send to PodTorrent | No (read-only) |
| [2. Launch a video model](#2-launch-a-video-model) | Expose a video model with the job API, test it locally, connect it | Yes, after asking |
| [3. Launch a text model](#3-launch-a-text-model) | The same for a chat model | Yes, after asking |

Before you start:

- Give the agent this repository (clone it, or point it at
  `https://github.com/Munerate/podtorrent-agent`). The prompts refer to
  `spec/protocol-v0.md` and `docs/operator-guide.md`.
- **Never paste your node token into the chat.** Set it yourself as the
  environment variable `PODTORRENT_NODE_TOKEN` when you start the agent for
  real. Tests use the mock network's token `dev-node-token`.
- Prompts and generated clips stay on your machine; neither the agent nor the
  network logs or stores them.

## 1. Engine survey

```text
You're helping me register my machine as a PodTorrent node. PodTorrent's node
agent is in this repository (podtorrent-agent); read docs/operator-guide.md
and spec/protocol-v0.md (especially "Manifest" and "Video") first.

This step is READ-ONLY. Don't install, start, stop or change anything, and
don't open ports. Run only commands that inspect: OS and version, CPU, RAM,
GPUs (nvidia-smi or the platform equivalent) with VRAM, disk free, running
processes and listening ports, installed inference software and its config,
model files and their sizes. If you need something you can't find, ask me.

For each model I serve (or want to serve), find out:
1. Modality: text (chat) or video. Model name and exact id, file/repo, and
   precision or quantization (e.g. fp16, fp8, q4_k_m).
2. Engine: the software that runs it (e.g. vLLM, llama.cpp, Ollama, SGLang,
   ComfyUI, a vendor SDK, a custom script), its version, how it is started,
   and where it listens.
3. Its local API: base URL, the endpoints, request fields and response shape.
   Does it already speak the OpenAI API? For chat: POST /v1/chat/completions
   with streaming and a `usage` object in the last stream chunk. For video:
   POST /v1/videos, GET /v1/videos/{id}, GET /v1/videos/{id}/content. If it
   speaks something else, describe that API precisely (paths, fields, sync or
   job-based, how to poll, where the output file ends up).
4. Video only: supported durations (seconds) and resolutions, typical time to
   make one clip at each common setting (measure one short clip only if I
   say yes), output format and typical file size, and how many jobs can run
   at once on this hardware.
5. Text only: context length, concurrent requests that fit, and tokens per
   second for one request and under load if known.
6. Network: can this machine open outbound HTTPS (port 443)? Is it behind a
   firewall, proxy or NAT? Is Docker available?

Then write two things:

A. A "node profile" in Markdown with a section per model and the answers
   above, marking anything you guessed or couldn't confirm. Add
   "Platform: <os>/<arch>", "Run as: binary or container", and "Connection:
   outbound (default) / needs inbound".
B. A draft manifest.json following docs/operator-guide.md, with
   "modality": "video" for video models, slots = concurrent jobs or
   requests, and price left as 0 with a TODO for me to fill in (USD per
   second of video for video models, per million tokens for text).

Save both in a new folder ./podtorrent-node/ and show me the profile. I'll send
the profile to PodTorrent. Don't include tokens, passwords, private hostnames
or personal details in either file.
```

## 2. Launch a video model

```text
Help me launch my video model as a PodTorrent node. Read spec/protocol-v0.md
(sections "Manifest" and "Video") and docs/operator-guide.md in this
repository (podtorrent-agent) first. If ./podtorrent-node/ has a node profile
and manifest from an earlier survey, use them; otherwise do the survey in
docs/agent-prompts.md (prompt 1) first.

Rules:
- Ask me before installing software, downloading models, opening ports,
  creating services or scheduled tasks, or changing system settings.
- Never ask for, print or store my node token. Tests use the mock network's
  token "dev-node-token"; I set PODTORRENT_NODE_TOKEN myself for the real run.
- Never log or save prompts or generated clips beyond what the engine itself
  keeps. Bind anything you write to 127.0.0.1 only.

Goal: podtorrent-agent can talk to my model through this local API on one
base URL (the "engine URL"):
  GET  /v1/models                  -> {"object":"list","data":[{"id":"<model id>","object":"model"}]}
  POST /v1/videos                  body {"model","prompt","seconds","size", ...}
                                   -> job object, at once (don't wait for the clip)
  GET  /v1/videos/{id}             -> job object
  GET  /v1/videos/{id}/content     -> the clip bytes (e.g. video/mp4) once completed;
                                      404 for unknown ids, an error status if not ready
Job object: {"id","object":"video","model","status","progress","seconds","size","created_at"}
with status one of queued, in_progress, completed, failed. "seconds" is the
clip length (string or number). Ids may use only letters, digits, "_", "-", ".".
A job must not hold its HTTP request open while generating. Keep finished
clips for at least one hour, then you may delete them.

Steps:
1. If my engine already serves exactly this API, skip to step 3.
2. Otherwise write a small adapter ("shim") in front of it that serves the API
   above and translates to my engine's own API: start a generation, track
   job state, map the engine's states to the four statuses, and serve the
   output file. Prefer the language and libraries already on this machine;
   keep it to one small file plus a README with how to start it. It runs
   jobs in the background, limits concurrent jobs to what the hardware can
   handle (queue the rest as "queued"), validates the job id in paths
   (no path traversal), and cleans up clips older than a set time (default
   24 h). Start it on 127.0.0.1.
3. Check the engine URL with curl: list models; submit a 2-4 second clip
   (ask me for a harmless test prompt or use "a lighthouse at dusk, slow
   pan"); poll until completed; download the content and check it plays or
   at least has a valid video header and a sensible size; check an unknown
   id returns 404.
4. Write ./podtorrent-node/manifest.json: my node id (ask me), the model
   with "modality": "video", "slots" = concurrent jobs, and
   "price": {"per_video_second": <ask me>}.
5. Test end to end with the mock network, built from this repo with Go 1.24+
   (`go build -o bin/ ./cmd/...`):
     ./bin/mock-network            (listens on 127.0.0.1:8787; client key dev-api-key)
     PODTORRENT_NODE_TOKEN=dev-node-token ./bin/podtorrent-agent \
       -network ws://127.0.0.1:8787/v0/node/connect \
       -engine <engine URL> -manifest ./podtorrent-node/manifest.json
   (on Windows set the variable with `$env:PODTORRENT_NODE_TOKEN="dev-node-token"`
   in PowerShell and run bin\podtorrent-agent.exe). Then through the mock
   network (Authorization: Bearer dev-api-key) submit a clip to
   http://127.0.0.1:8787/v1/videos, poll GET /v1/videos/{id} until
   completed, and download /v1/videos/{id}/content. The agent log must show
   it connected and no errors; the downloaded file must match what the
   engine made.
6. Stop the test processes. Give me: what you installed or wrote, how to
   start the engine/shim and the agent (and how to keep them running after a
   reboot, if I want that), the measured time per clip, and the exact command
   to connect for real:
     PODTORRENT_NODE_TOKEN=<my token> podtorrent-agent \
       -engine <engine URL> -manifest ./podtorrent-node/manifest.json
   Don't run that last command yourself.
```

## 3. Launch a text model

```text
Help me launch my text model as a PodTorrent node. Read docs/operator-guide.md
and spec/protocol-v0.md in this repository (podtorrent-agent) first; use
./podtorrent-node/ from an earlier survey if it exists. Same rules as always:
ask before installing, downloading, opening ports or creating services;
never ask for or print my node token; bind to 127.0.0.1.

1. Make sure an OpenAI-compatible engine serves the model on 127.0.0.1:
   GET /v1/models lists the exact model id, and POST /v1/chat/completions
   works with and without "stream": true. With
   "stream_options": {"include_usage": true} the last stream chunk before
   `data: [DONE]` must contain "usage" with prompt_tokens and
   completion_tokens (the network pays by these counts). If the engine
   doesn't do this, tell me which setting or version fixes it.
2. Write ./podtorrent-node/manifest.json (node id and price from me; slots =
   concurrent requests that fit; quant and context_max as configured).
3. Test with the mock network as in docs/operator-guide.md, "Test before
   going live", and check the usage chunk. Then run podtorrent-conformance
   as in "Check conformance".
4. Give me the commands to start everything and the real connect command;
   don't run the real one yourself.
```
