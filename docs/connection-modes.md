# Connection modes and private links

The agent can reach the network in three ways. The protocol on top is the
same; only who opens the connection, and over what, changes.

| Mode | Who connects | Open ports on your side | Use when |
| --- | --- | --- | --- |
| **Outbound** (default) | Agent → network, `wss://` | none | Outbound HTTPS is allowed. Simplest. |
| **Private link: Cloudflare Tunnel** (standard private option) | Network → agent, through a tunnel your `cloudflared` opens | none | You want no public endpoint and no inbound ports, without running a VPN. |
| **Private link: your own network** (e.g. WireGuard) | Network → agent, over your private network | none to the internet | You already run WireGuard or another VPN and want the link to stay on it. |
| **Inbound, public** | Network → agent, `https://` with mutual TLS | one TLS port | You run your own firewall and TLS and prefer to expose an endpoint. |

All inbound kinds use the agent's inbound mode:

```bash
podtorrent-agent -listen :8443 [TLS flags] -engine http://127.0.0.1:8000 -manifest manifest.json
```

The network authenticates with your node token (`Authorization: Bearer`); the
agent rejects anything else. The network re-dials within a minute of a
disconnect.

## Private link: Cloudflare Tunnel (recommended private option)

1. The PodTorrent team creates a tunnel for your node and sends you its tunnel
   token and hostname (for example `node-01.tunnel.podtorrent.com`). The
   hostname is protected by Cloudflare Access: only the network's service
   token gets through.
2. Run the agent in inbound mode on a private address, plain HTTP (the tunnel
   is the encryption):

   ```bash
   podtorrent-agent -listen 127.0.0.1:8443 -insecure-plaintext -engine http://127.0.0.1:8000 -manifest manifest.json
   ```

3. Run `cloudflared` next to it, pointing at the agent:

   ```bash
   cloudflared tunnel run --token <tunnel token> --url http://127.0.0.1:8443
   ```

   `cloudflared` only makes outbound connections, so nothing is exposed.
4. The team registers the link (`transport: cloudflare_tunnel`).

## Private link: your own network (WireGuard or similar)

Use this when the link must stay on a network you control.

1. Run the agent in inbound mode on an address reachable over your network,
   with TLS, ideally requiring the network's client certificate:

   ```bash
   podtorrent-agent -listen 10.8.0.2:8443 -tls-cert node.crt -tls-key node.key \
     -client-ca podtorrent-network-ca.pem -engine http://127.0.0.1:8000 -manifest manifest.json
   ```

2. Tell the team the address and how your network is joined. The network's
   gateway runs on Cloudflare, which cannot join a WireGuard network
   directly, so the bridge is a `cloudflared` connector running on a machine
   inside your network (it can be the same machine): the connector carries
   the network's connection onto your private network, and only you control
   what it can reach. A dedicated WireGuard hub run by PodTorrent is possible
   later if a connector doesn't fit your policy.
3. The team registers the link (`transport: wireguard`) with the private URL.

## Inbound, public

```bash
podtorrent-agent -listen :8443 -tls-cert node.crt -tls-key node.key \
  -client-ca podtorrent-network-ca.pem -engine http://127.0.0.1:8000 -manifest manifest.json
```

- `-tls-cert`, `-tls-key`: your server certificate, for the hostname the
  network will dial.
- `-client-ca`: the PodTorrent network's client CA (from the team). With it,
  only the network can complete the TLS handshake. Without it the agent warns
  and relies on the node token alone.
- Allow inbound TCP on the port from the internet (Cloudflare's address
  ranges if you want to restrict it).

## Flags for inbound mode

| Flag | Environment | Meaning |
| --- | --- | --- |
| `-listen` | `PODTORRENT_LISTEN` | Listen address; enables inbound mode |
| `-tls-cert`, `-tls-key` | `PODTORRENT_TLS_CERT`, `PODTORRENT_TLS_KEY` | Server certificate and key (PEM) |
| `-client-ca` | `PODTORRENT_CLIENT_CA` | Require client certificates signed by this CA |
| `-insecure-plaintext` | — | No TLS; only behind a tunnel or for local tests |
