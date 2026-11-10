# exo clusters

[exo](https://github.com/exo-explore/exo) joins your Macs into one cluster
that can run models too big for any one of them. The agent connects a whole
exo cluster to the network as **one node**: one token, one manifest, one set
of receipts. Run exo as published; the agent needs no changes to it.

```
client ─► network ◄══ wss ══ podtorrent-agent ─► exo API (127.0.0.1:52415) ─► Mac A ⇄ Mac B ⇄ Mac C
```

The node's operator can see the prompts it serves, as for any node, and so
can every Mac in the cluster (see "Layer-split pipelines" in the
[spec](../spec/protocol-v0.md)).

## Set up

1. Run exo on each Mac (the exo app, or `uv run exo` from source), all on the
   same exo version. In exo, launch the models you want to serve and wait
   until they are ready.
2. **Close exo's port to the rest of your network.** exo serves its API,
   admin routes and cluster traffic on port 52415 on every interface, with no
   password, and its `/state` route lists the prompts of running requests.
   On every Mac, run the guard and name your other exo Macs (give them DHCP
   reservations first, so their addresses stay the same):

   ```bash
   sudo scripts/exo-port-guard.sh install 192.168.1.21 192.168.1.22
   ```

   It adds a macOS packet filter rule that allows the port only from this
   Mac and the addresses you name, and reloads it at every start. `status`
   shows the rule; `remove` takes it out. It changes nothing else.
3. Write the manifest with exo's model ids, such as
   `mlx-community/Llama-3.2-1B-Instruct-4bit`. Set `slots` no higher than
   exo's concurrent requests per instance (`EXO_MAX_CONCURRENT_REQUESTS`,
   default 8).
4. Run the agent on one Mac of the cluster, preferably one that stays awake:

   ```bash
   PODTORRENT_NODE_TOKEN=... podtorrent-agent -engine-kind exo \
     -engine http://127.0.0.1:52415 -manifest manifest.json
   ```

Keep the Macs awake while they serve (System Settings › Energy, or
`caffeinate -s`), and update exo on all of them together: Macs on different
exo versions don't join each other.

## What exo mode does

- **Offers only loaded models.** exo's `/v1/models` lists every model it
  knows, loaded or not. The agent reads which models have an instance whose
  runners are all ready (`GET /state/instances` and `/state/runners`, every
  5 s) and reports 0 free slots for the others. When exo moves or unloads a
  model, the network stops sending it requests within seconds. The agent
  never reads `/state` itself, which holds prompts.
- **Refuses remote URLs.** exo downloads image URLs in requests itself, from
  your network, so a client could make it fetch addresses on your LAN. The
  agent refuses any request with a `url` or `image_url` that isn't an inline
  `data:` URL. Clients send images inline.
- **Fails silent streams fast.** exo answers a stream with 200 and keep-alive
  comments while a model is still loading. The agent waits for the first
  event before it answers the network, and fails the request after
  `-first-event` (default 1 min) so the network can retry it elsewhere.

Not covered yet: the manifest doesn't report the pipeline route
(`models[].pipeline.hops`) or the exo version.
