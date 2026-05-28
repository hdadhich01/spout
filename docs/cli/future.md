# CLI — Planned

Items spec'd but not yet shipped. They move out of this file as they ship.

For design context, see [ARCHITECTURE.md §14](../../ARCHITECTURE.md#14-deferred-items).

## Planned commands

| Command | What |
|---|---|
| `spout tail <run> [stream]` | Live follow of output (`tail -f` / `kubectl logs -f`). For headless boxes / SSH where you can't open a browser. |
| `spout config show` | Alias for bare `spout config` |
| `spout config get <key>` | Print one resolved field (e.g., `spout config get servers.acme.url`) |
| `spout config set <key>=<value>` | One-liner config edit (rewrites yaml in place) |
| `spout config edit` | Open `~/.config/spout/spout.yaml` in `$EDITOR` (fallback `vi`) |

## Planned persistent flags

| Flag | What |
|---|---|
| `--offline` | Don't talk to any remote server. Local viewer (`spout open`) still works via ephemeral localhost. |
| `--no-archive` | Don't write the local mirror (server-only) |
| `--no-encrypt` | Force plaintext (Tier 1) even on `spout.sh` |
| `--encrypt` | Force E2E (Tier 2) on any server |
| `--json` | Machine-parseable output (where applicable: `ls`, `stats`, `config`, `doctor`) |

## Planned per-command flags

| Command | Flag | What |
|---|---|---|
| `spout server` | `--public` | Hosted-mode (TCP listener, server-assigned naming, rate limits) |
| `spout share` | `--new` | Upload local copy as fresh server record (recovery) |
| `spout logs` | `--stream stdout\|stderr\|all` | Filter by stream type |
| `spout logs` | `--from <bytes>` | Start replay from byte offset |
| `spout logs` | `-f, --follow` | Live follow (or use `spout tail`) |
| `spout ls` | `--history` | Include local-archive runs not on the server |
| `spout doctor` | (new check row) | `websocket` — real WS handshake to catch proxy issues |

## Planned config fields

| Field | Where | What |
|---|---|---|
| `local_ttl` | global | TTL for local archive cleanup (e.g., `30d`) |
| `archive` | either | Master switch for dual-write local mirror (default `true`) |
| `notify` | either | Notification sink config — `slack`, `discord`, `email`, `push`, `webhook` |

### `notify` map schema

| Channel | Required | Optional |
|---|---|---|
| `slack` | `webhook: ENV_VAR_NAME` | `channel:` override |
| `discord` | `webhook: ENV_VAR_NAME` | — |
| `email` | `to: address@example.com` | `subject:` template |
| `push` | `enabled: true` | (uses spout.sh VAPID keys) |
| `webhook` | `url: ENV_VAR_NAME` | `headers:` map |

The string value of `webhook:` / `url:` names the env var that holds
the actual URL — same pattern as `servers[].token`. URLs live in
`.env`, never committed alongside yaml.

## Planned env vars

| Var | What |
|---|---|
| `EDITOR` | Editor used by `spout config edit` |

## Planned naming behaviors

- **Server-assigned run names on hosted** (`spout.sh` ignores `-n`)
- **Vanity URLs** (`/u/<user>/<run>`) — requires user accounts (deferred)

## Python SDK (`spoutsh`) — Phase 7

See [ARCHITECTURE.md §14.1](../../ARCHITECTURE.md#141-python-sdk--google-colab-locked-design).

```python
import spoutsh

# Cell magic (Colab/Jupyter)
%%spoutsh my-training
model.fit(X, y, epochs=100)

# Context manager
with spoutsh.run(name="my-training") as r:
    model.fit(X, y, epochs=100)

# Programmatic
run = spoutsh.start(name="...")
spoutsh.log("epoch 1: loss=0.5")
run.end()

# Subprocess wrapper (captures stdout + stderr separately)
spoutsh.exec(["python", "train.py"], name="my-training")
```

Pip name is `spoutsh` because `spout` is taken on PyPI by an abandoned
package (PEP 541 reclamation pursued in parallel).
