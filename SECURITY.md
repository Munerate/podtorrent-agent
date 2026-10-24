# Security

## Reporting a vulnerability

Please report privately through
[GitHub security advisories](https://github.com/Munerate/podtorrent-agent/security/advisories/new).
Do not open a public issue. We aim to reply within three working days.

## Threat model (summary)

- The agent holds one secret, the node token. Anyone with it can register as
  that node. Keep it out of logs and images; rotate it if exposed.
- The agent accepts work only over the connection it opened itself, and only
  calls `POST /v1/chat/completions` on the configured engine. It cannot be
  told to call another path or host.
- The container runs as non-root, with a read-only filesystem possible, no
  shell and no capabilities required.
- Prompts and answers pass through the agent unmodified and are not logged.
  The node operator can see them on their own engine.
