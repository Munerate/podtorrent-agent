# ComfyUI video models

`podtorrent-comfy-video` lets a video model that runs as a ComfyUI workflow
serve the network's video job API (spec: "Video") with no change to ComfyUI.
It sits between the agent and ComfyUI on the same machine:

```
network ⇄ podtorrent-agent ⇄ podtorrent-comfy-video (127.0.0.1:8100) ⇄ ComfyUI (127.0.0.1:8188)
```

Each job queues your workflow with ComfyUI's `POST /prompt`; status comes from
`/history` and `/queue`, and the clip from `/view`. ComfyUI keeps its output
files as usual. Nothing logs prompts or clips.

## 1. Export the workflow

In ComfyUI, open the workflow that makes your clips and use **Workflow →
Export (API)** (enable dev mode options in settings if you don't see it). The
UI's normal save format won't work.

## 2. Mark the inputs

Edit the exported JSON and put placeholders where the request's values go:

| Placeholder | Becomes | Typical input |
| --- | --- | --- |
| `"{{prompt}}"` (required) | the client's prompt (also replaced inside longer strings) | positive text encoder `text` |
| `"{{width}}"`, `"{{height}}"` | numbers from the requested size | empty video latent |
| `"{{frames}}"` | seconds × fps, or n×multiple+offset with `-frame-multiple` and `-frame-offset` | latent `length` / frame count |
| `"{{seconds}}"`, `"{{fps}}"` | numbers | nodes that take a duration or frame rate |
| `"{{seed}}"` | a random number per job | sampler `seed` / `noise_seed` |

Placeholders are whole JSON strings, quotes included: `"width": "{{width}}"`
becomes `"width": 832`. Leave everything else as it is. The workflow must end
in a node that saves a video file (mp4, webm, mov or mkv).

### Quality modes

To offer more than one way to render (say a full-step workflow and a faster
one with fewer steps or a turbo LoRA), export each as its own file and name
them: `-workflow best=h3.json,fast=h3-fast.json`. Clients pick one with
`"quality": "fast"` in the request; the first is the default. The price per
second is the same for every mode.

## 3. Run it

```bash
podtorrent-comfy-video -workflow workflow.json -model example/video-1 \
  -sizes 832x480,480x832,640x640 -min-seconds 5 -max-seconds 15 -fps 24
```

Then point the agent at it: `-engine http://127.0.0.1:8100`, with the same
model id, `"modality": "video"` and `"slots": 1` (or as many renders as your
GPU fits at once) in the manifest. `/v1/models` answers even while ComfyUI is
still starting, so the agent can connect first.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-listen` | `127.0.0.1:8100` | Where the job API is served. Keep it on loopback. |
| `-comfy` | `http://127.0.0.1:8188` | ComfyUI base URL. |
| `-workflow` | `workflow.json` | The exported, marked workflow. |
| `-model` | (required) | Model id, as in the manifest. |
| `-sizes` | `832x480,480x832,640x640` | Allowed sizes; the first is used when a request gives none. Others get 400. |
| `-min-seconds`, `-max-seconds`, `-default-seconds` | 5, 15, 5 | Allowed clip lengths. |
| `-fps` | 24 | Sets `{{frames}}`. |
| `-frame-multiple`, `-frame-offset` | 0, 1 | For models that need n×multiple+offset frames: 8 and 1 for 8n+1, 17 and 5 for MiniMax-H3's 17n+5. The count nearest seconds × fps is used. |
| `-default-quality` | first in `-workflow` | Quality mode when the request names none. |
| `-keep` | 24h | Ended jobs are forgotten after this, and their content then returns 404. |
| `-output-dir` | (none) | ComfyUI's `output` folder. If set, a job's clip is deleted when the job is forgotten. Otherwise ComfyUI keeps it. |
| `-state` | `comfy-video-jobs.json` | Jobs are kept here across restarts (ids, status, file names; no prompts). Empty keeps them in memory only. |
| `-max-queued` | 4 | Unfinished jobs allowed beyond the one rendering; more get 429. The network itself sends no more than your manifest's `slots`. |

If ComfyUI restarts, jobs it no longer knows turn `failed`, with a reason in
the job's `error`.

## 4. Test

Use the mock network as in [the operator guide](operator-guide.md#test-before-going-live),
with a video request instead of a chat one:

```bash
curl localhost:8787/v1/videos -H 'Authorization: Bearer dev-api-key' \
  -d '{"model":"example/video-1","prompt":"a lighthouse at dusk, slow pan","seconds":"5","size":"832x480"}'
curl localhost:8787/v1/videos/<id> -H 'Authorization: Bearer dev-api-key'          # until "completed"
curl -o clip.mp4 localhost:8787/v1/videos/<id>/content -H 'Authorization: Bearer dev-api-key'
```
