# Spout

**Pipe any terminal output to a live web dashboard.** Free, hosted,
encrypted by default, or self-host the whole thing.

```bash
python train.py | spout                  # stream to spout.sh, live URL in clipboard
spout run python train.py                # detached tmux run, attach later
python train.py 2>&1 | spout             # capture stderr too (tqdm-friendly)
cmd | nc spout.sh 1337                   # zero-install Tier 0 path
```

The dashboard is embedded in the binary — runs locally with
`spout server`, or use the hosted `spout.sh`.

## What it is

Spout is the reliable, persistent, self-hostable evolution of
Seashells.io. Live terminal streaming that actually works, with:

- **Encrypted by default** on the hosted service (Tier 2: AES-GCM,
  key in URL fragment, server can't read)
- **Local mirror always on** — your CLI saves every byte locally; if
  hosted ever goes down, your data is fine
- **stderr capture** for `tqdm` and friends via `cmd 2>&1 | spout` or
  declaring a stderr stream in `streams:`
- **Named runs** with `run_name:` templates (`exp-1`, `exp-2`, ...)
- **Multi-pane streams** — define a `streams:` block, launch all panes
  in one tmux session
- **AI watchdog hooks** — define rules in yaml, get alerts when
  training plateaus / errors fire / etc.
- **Self-host** — same single binary; one env var to switch backends

## Spout vs Seashells

Every Seashells limitation, addressed:

| Seashells issue | How Spout fixes it |
|---|---|
| Repeated downtime / 502s ([client #18](https://github.com/anishathalye/seashells/issues/18), [#19](https://github.com/anishathalye/seashells/issues/19), [#21](https://github.com/anishathalye/seashells/issues/21)) | CLI dual-writes locally — hosted outage doesn't lose data. Self-host option for full control. |
| 24h expiry ([server #3](https://github.com/anishathalye/seashells-server/issues/3)) | 7-day hosted retention + unlimited local archive |
| Hard-coded `baseUrl` blocks self-host ([server #5](https://github.com/anishathalye/seashells-server/issues/5)) | Config-based servers from day one |
| Output appears only after exit ([client #11](https://github.com/anishathalye/seashells/issues/11)) | Live WebSocket streaming, every chunk flushes immediately |
| No stdout/stderr split — tqdm invisible ([client #3](https://github.com/anishathalye/seashells/issues/3), [#14](https://github.com/anishathalye/seashells/issues/14)) | `cmd 2>&1 \| spout` merges them at the shell; declare separate stdout/stderr streams in `streams:` for actual byte-level split |
| HTTP proxy / WebSocket Upgrade fails silently ([client #17](https://github.com/anishathalye/seashells/issues/17)) | HTTP-fallback ingest transport + clear "WS connection failed" UI banner |
| Windows socket errors ([client #9](https://github.com/anishathalye/seashells/issues/9)) | Pure Go networking; pipe mode works on Windows (tmux-using commands are Linux/macOS only) |
| Can't know URL ahead of time ([client #12](https://github.com/anishathalye/seashells/issues/12)) | `POST /api/run` returns the URL before streaming starts |
| No auth or privacy ([server #2](https://github.com/anishathalye/seashells-server/issues/2)) | Token auth + Tier 2 E2E encryption |
| `< /dev/urandom` abuse drives instability ([server #3](https://github.com/anishathalye/seashells-server/issues/3)) | Per-IP rate limits + per-run size caps + roll-over windows |
| No naming control ([server #1](https://github.com/anishathalye/seashells-server/issues/1)) | `run_name:` templates, server-assigned on hosted, client-picked self-hosted |
| Server source took 5+ years to release ([client #2](https://github.com/anishathalye/seashells/issues/2)) | Open-source from day one |

## The four tiers

Same single binary serves all four; how you invoke Spout determines
the tier.

| Tier | Use case | Command |
|---|---|---|
| **0** Zero-install demo | Hobbyist, locked-down boxes, viral demo | `cmd \| nc spout.sh 1337` |
| **1** CLI + hosted | Mass usability, plaintext | `cmd \| spout --no-encrypt` |
| **2** CLI + hosted E2E | Privacy-conscious, research labs (default) | `cmd \| spout` |
| **3** Self-hosted | Enterprise, internal infra | `cmd \| spout -s acme` |

See [ARCHITECTURE.md §3](ARCHITECTURE.md#3-the-four-tiers) for the full rundown.

## Install

```bash
# One-line installer (planned)
curl -sSL https://spout.sh/install | sh

# Or via Go
go install github.com/hdadhich01/spout/cmd/spout@latest
```

Requires `tmux` for `spout run`, `spout add-stream`, and `streams:`
modes. Pipe mode (`cmd | spout`) and read-only commands (`ls`, `open`,
`logs`, etc.) work without it.

On first invocation the CLI writes an annotated `~/.config/spout/spout.yaml`
starter (existing files are never overwritten).

## Quickstart

```bash
# Pipe a command to the hosted service (encrypted by default)
python train.py | spout
# → URL printed and copied to clipboard, with key in the fragment

# Run something detached and reattach later
spout run python train.py
spout attach   # picks from active runs
spout kill     # stop one

# Capture stderr too (tqdm-friendly)
python train.py 2>&1 | spout

# Self-host
spout server                          # localhost:3000, no auth
SPOUT_TOKEN=$(openssl rand -hex 32) spout server   # team mode
```

## Colab / Jupyter

For shell cells (`!python train.py`), use the CLI or `nc`:

```python
!apt-get install -y netcat-openbsd
!python train.py | nc spout.sh 1337                 # Tier 0, no install
# or:
!curl -sSL https://spout.sh/install | sh             # [planned]
!python train.py | spout                             # Tier 1/2
```

For inline Python (`model.fit(...)`, `tqdm`, etc.) — most common in
notebooks — use the **Python SDK** [planned]:

```python
!pip install spoutsh

import spoutsh

# Cell magic (easiest)
%%spoutsh my-training
model.fit(X, y, epochs=100)

# Or context manager
with spoutsh.run(name="my-training"):
    model.fit(X, y, epochs=100)
```

> **Why `spoutsh`?** The `spout` name on PyPI is taken by an
> abandoned package; `spoutsh` ties to the brand domain. PEP 541
> reclamation pursued in parallel.

**Tab-close survival**: closing your Colab tab doesn't kill the
WebSocket from the runtime to spout.sh. Bytes keep flowing for ~90 min
of Colab idle timeout. View live from your phone via the spout.sh URL.

**One-click**: `spout.sh/colab-template.ipynb` provides a pre-built
notebook with cell-magic + ML training boilerplate. ([Open in Colab
badge](https://colab.research.google.com/) on the landing page.)

See [docs/faq.md § Colab](docs/faq.md#can-i-use-spout-from-colab--jupyter)
for all the patterns and caveats.

## Modes at a glance

### Pipe — `cmd | spout`

Foreground, reads stdin, sends to server, passes through to your stdout.
Aborts cleanly if upstream produces nothing.

### Run — `spout run cmd...`

Detached tmux session. Returns immediately. `spout attach` to reattach,
`spout kill` to stop, `spout logs` to dump output.

### Streams — `spout run` with `streams:` config

Drop a `spout.yaml` in your project:

```yaml
job: ml-training
run_name: "exp-{n}"

streams:
  - label: training
    command: python train.py --epochs 200
  - label: gpu
    command: watch -n 2 nvidia-smi
  - label: logs
    command: tail -f output.log
```

Then `spout run` (no args) launches all three as tmux panes, each
streamed to the dashboard as `exp-1-training`, `exp-1-gpu`,
`exp-1-logs`. Add a fourth pane to the running run later with
`spout add-stream exp-1 --label tensorboard tensorboard --logdir runs/`.

## Observability (opt-in)

A CLI-side loop watches each run's output and emits structured events —
live status, extracted metrics, an end-of-run synthesis, and built-in
detectors for the ways agents fail (looping / drifting / amnesia). It
shows up in the dashboard's right-hand observe panel and as a CLI command:

```yaml
# spout.yaml
observe:
  enabled: true
  model:
    type: api                # api (cloud) | local (Ollama) | cli
    model: claude-haiku-4-5
  rules:
    - name: training-stuck
      prompt: "Alert if loss hasn't decreased for 3+ epochs."
      sources: [training, gpu]
```

```bash
# ~/.config/spout/.env
ANTHROPIC_API_KEY=sk-ant-...
```

Without a key the engine still runs — in "stub mode" — tracking byte
counts and heuristically classifying the run type, but no LLM calls.
Per-run override: `--observe` / `--no-observe`. Export to OTel: `--otel`.

Read the timeline from the CLI:

```bash
spout observe ember-7d2e
```

Full spec: [docs/cli/observe.md](docs/cli/observe.md).

## Self-hosting

```bash
# 1. Single binary, default config (FileStore, port 3000).
#    From the same box, http://localhost:3000/admin is open (loopback admin).
spout server

# 2. Team mode — mint per-user ingest tokens. A running server picks up
#    new tokens live; manage from the web at /admin or the CLI.
spout server token create alice
spout server token list
spout server token revoke alice

# 3. Remote admin access — set a password, then visit /admin/login from any
#    device. (Same-box use never needs this.)
SPOUT_ADMIN_PASSWORD=$(openssl rand -hex 16) spout server

# 4. Behind Caddy for HTTPS (auto-issues Let's Encrypt cert).
#    /etc/caddy/Caddyfile:
spout.acme.com {
    reverse_proxy localhost:3000
}

# 5. Mid-scale (10K+ runs) — SQLite metadata [planned]
SPOUT_STORE=sqlite SPOUT_DB_PATH=/opt/spout/spout.db spout server
```

The two credentials and the access model are documented at
[docs/server/config.md → Access model](docs/server/config.md#access-model).

Storage backends, runtime-selected via `SPOUT_STORE`:

| Backend | When to use |
|---|---|
| `file` (default) | Personal / hobbyist self-host |
| `sqlite` | Mid-scale (10K+ runs); fast metadata queries |
| `postgres` | Hosted (`spout.sh`); multi-box; bytes in R2 |

Bytes always live as files (or R2 objects). DB only holds metadata.

For the full env-var reference and operator recipes (Docker, Nginx,
Hetzner stack, hosted prod config), see [ARCHITECTURE.md §11.5](ARCHITECTURE.md#115-operations-env-vars--deployment).

## Documentation

| Doc | What's in it |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Master spec — tiers, streaming, storage, encryption, notifications, ops, costs, deferred items, rejected approaches |
| [docs/cli/](docs/cli/) | `commands.md` · `config.md` · `future.md` |
| [docs/server/](docs/server/) | `routes.md` · `config.md` · `future.md` |
| [docs/faq.md](docs/faq.md) | Troubleshooting + common gotchas |

For contributors: [DEVELOPMENT.md](DEVELOPMENT.md). For AI agents:
[CLAUDE.md](CLAUDE.md).

## Planned (not yet shipped)

Things spec'd but not in code yet. Each item links into ARCHITECTURE.md
where the design lives.

### Distribution

- **Curl one-line installer** — `curl -sSL spout.sh/install | sh`
- **Prebuilt binaries** for Linux/macOS x64+arm64 + Windows (GitHub Actions)
- **Docker image** for self-host (`docker run spout/spout server`)
- **Helm chart** for k8s self-host

### SDKs (sibling packages in the monorepo)

- **Python SDK** (`pip install spoutsh`) — pure-Python WS client, Colab-friendly. `spout` PyPI name is taken (PEP 541 reclaim pursued in parallel).
- **"Open in Colab" template** — `spout.sh/colab-template.ipynb` pre-built with cell-magic + ML training boilerplate
- **Node SDK** (`npm install @spout/sdk`)

### CLI (planned commands and flags)

See [docs/cli/future.md](docs/cli/future.md) for the full list. Highlights:

- `spout add-stream <run>` — attach a new pane to a running multi-stream run
- `spout tail <run>` — live follow without opening a browser
- `spout config edit / get / set` — yaml editing without manually opening files
- `--offline`, `--no-encrypt`, `--no-archive`, `--encrypt`, `--json` flags

### Tiers and features

- **Tier 0**: TCP listener `:1337` + HTTP chunked POST `/ingest`
- **Tier 2**: AES-256-GCM CLI-side encryption + browser WebCrypto decryption
- **Notifications**: CLI rule engine + server alert routing (Slack, Discord, email, push, webhook)
- **Replay UX**: load-on-scroll, jump-to-time, download full log
- **AI watchdog**: rule-based or local-LLM-driven alerts (Phase 4)

### Hosted (`spout.sh`)

- **Postgres + R2** backend
- **Multi-region HA** (Phase 2)
- **Public opt-in gallery** (Phase 4)
- **User accounts** + per-user dashboards (Phase 3)

### Observability / ops

- **Metrics endpoint** (Prometheus)
- **mDNS / Bonjour** LAN discovery for self-host

For the comprehensive deferred list with rationale, see
[ARCHITECTURE.md §14](ARCHITECTURE.md#14-deferred-items).

## License

TBD. See [ARCHITECTURE.md §17](ARCHITECTURE.md#17-open-strategic-questions-decide-before-public-launch).
