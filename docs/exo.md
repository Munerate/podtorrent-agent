# Serve from an exo cluster

[exo](https://github.com/exo-explore/exo) links several Macs on your local
network into one cluster, so together they can run models that are too big
for any one of them. With the agent, the whole cluster joins PodTorrent as
**one node**: one node token, one manifest, one set of earnings.

```
client ─► PodTorrent ◄══ outbound TLS ══ podtorrent-agent ─► exo (127.0.0.1:52415) ─► Mac A ⇄ Mac B ⇄ Mac C
```

You run exo exactly as exo publishes it. The agent runs next to it on one of
the Macs and needs no open ports.

## Before you start

- Two or more Apple-silicon Macs on the same network (Wi-Fi, Ethernet or
  Thunderbolt), or just one Mac to try it out.
- exo installed on each Mac, **all on the same exo version** (Macs on
  different versions don't join each other).
- A node token from the PodTorrent team.
- An administrator password on each Mac, for step 2.

## Set it up

### 1. Load your models in exo

Open exo's dashboard (`http://localhost:52415`), pick the models you want to
serve and launch them. Wait until each one shows as ready. The agent offers
a model to the network only while exo has it loaded, so you can load and
unload models at any time.

### 2. Close exo's port to the rest of your network

**Do this before you connect to PodTorrent.** exo listens on port 52415 on
every network interface, without a password. Anyone on your network could
then use your cluster, change which models it runs, or read the prompts of
requests it is serving (exo's `/state` page lists them).

The guard script fixes that. It adds a firewall rule that lets only this Mac
and your other exo Macs reach the port. Run it on **every** Mac in the
cluster, each time naming the **other** Macs' addresses:

```bash
sudo scripts/exo-port-guard.sh install 192.168.1.21 192.168.1.22
```

- Give each Mac a fixed address first (a DHCP reservation in your router),
  so the rule keeps matching after a restart.
- The rule stays in place after a restart.
- `sudo scripts/exo-port-guard.sh status` shows the rule.
  `sudo scripts/exo-port-guard.sh remove` takes it out.
- It changes nothing else: not exo, not your other firewall settings.

### 3. Write the manifest

Use exo's model ids, which include the quantisation:

```json
{
  "node": "example-node-01",
  "price": { "per_mtok_in": 0.05, "per_mtok_out": 0.40 },
  "models": [
    { "id": "mlx-community/Llama-3.2-1B-Instruct-4bit", "quant": "4bit", "slots": 4, "context_max": 8192 }
  ]
}
```

Keep `slots` at 8 or less: by default exo runs up to 8 requests at a time
per model (`EXO_MAX_CONCURRENT_REQUESTS`). See the
[manifest reference](operator-guide.md#manifest-reference) for the other
fields.

### 4. Start the agent

On one Mac of the cluster, ideally one that stays on:

```bash
PODTORRENT_NODE_TOKEN=... podtorrent-agent -engine-kind exo \
  -engine http://127.0.0.1:52415 -manifest manifest.json
```

You should see `connected`, then `engine models ready` with the models exo
has loaded. Run it as a service so it restarts with the Mac (see the
[operator guide](operator-guide.md)).

## Keep it running

- **Keep the Macs awake** while they serve: System Settings › Energy, or
  run `caffeinate -s`. A Mac that sleeps drops out of the cluster, and exo
  moves or stops its models.
- **Update all Macs together.** The exo app updates itself. A Mac on a newer
  version leaves the cluster until the others update too.
- **The exo app may change your network settings.** It offers to set up
  Thunderbolt networking with a small system service (`io.exo.networksetup`)
  that switches your Mac to an `exo` network location. Only accept it if you
  connect Macs with Thunderbolt. exo's README explains how to undo it.

## What the agent does differently for exo

`-engine-kind exo` turns on three protections. You don't need to configure
them.

| exo does this | So the agent |
| --- | --- |
| Lists every model it knows at `/v1/models`, loaded or not | Checks which models are actually loaded and ready, every 5 seconds, and offers only those. It reads only exo's instance and runner lists, never the page that holds prompts. |
| Downloads image URLs in requests by itself, from your network | Refuses requests with a `url` or `image_url` that isn't inline `data:`. Otherwise a client could make your Mac fetch addresses on your network, such as your router's admin page. Clients must send images inline. |
| Answers "OK" and then waits silently when a model is still loading | Waits for the first real output. If none comes within `-first-event` (default 1 minute), it fails the request so the network can send it to another node. |

## Who sees what

As with any node, you, the operator, can see the prompts your node serves.
So can every Mac in your cluster, because each one runs part of the model.
Billing and receipts are for the node as a whole.

## Troubleshooting

| What you see | What to do |
| --- | --- |
| `engine models ready models=[]` | No model is loaded in exo, or it is still loading. Load it in exo's dashboard and wait until it's ready. |
| A model never shows as ready | The id in your manifest must match exo's id exactly, for example `mlx-community/Llama-3.2-1B-Instruct-4bit`. |
| `exo state` warnings | The agent can't reach exo. Check that exo is running and that `-engine` points at it. |
| Another Mac doesn't join the cluster | Check that it runs the same exo version as the others. |
| Models spread over several Macs fail, but single-Mac ones work | The Macs also talk to each other on port 52415. Check that the guard on each Mac lists the other Macs' current addresses (`status`). |
| Requests fail with "engine sent no output in time" | exo took longer than `-first-event` to start answering. This is usually a model that is still loading. For very long prompts on a slow cluster, raise `-first-event`. |

## Not done yet

The manifest doesn't yet show the cluster's route (which Mac runs which
layers) or the exo version.
