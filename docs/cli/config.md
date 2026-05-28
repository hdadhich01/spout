# CLI Config (`spout.yaml`)

Authoritative spec for the CLI's config file.

## Where it lives

The CLI walks up from cwd looking for `spout.yaml` and `.env`, then
loads `~/.config/spout/spout.yaml` as the base.

```
<repo>/experiments/exp1/spout.yaml    deepest — highest priority
<repo>/spout.yaml                      walks up to git root
~/.config/spout/spout.yaml             global — fallback
```

Walk stops at git root (or `$HOME` if not in a repo). The global file
is always appended.

## Merge rules

| Kind | How they combine |
|---|---|
| Map (`servers:`, `env:`, `observe.detectors:`) | Merge, deepest wins per key |
| List (`streams:`, `observe.rules:`) | **Replace**, deepest wins entirely |
| Scalar (`job:`, `server:`, etc.) | Override, deepest wins |

Foreign-key validation: `observe.rules[].sources` must match a
`streams[].label`; `observe.model.type: local` requires `endpoint:`.

## Priority

```
CLI flags (-s/-l/-n)
  > env vars (SPOUT_TOKEN_<NAME>, ...)
    > deepest spout.yaml (cwd)
      > parent dirs walking up
        > git-root spout.yaml
          > ~/.config/spout/spout.yaml
            > built-in defaults (spout.sh)
```

## Global config

```yaml
# ~/.config/spout/spout.yaml

# Fallback server when projects don't set one.
default_server: spout.sh

# Local archive path.
storage: ~/.config/spout/storage

# Persist .raw files locally (false useful in CI).
history: true

# Address book of named server profiles.
servers:
  spout.sh:
    url: spout.sh:443

  homelab:
    url: home.lan:3000

  work:
    url: spout.company.internal:8080
    token: SPOUT_TOKEN_WORK     # name of env var holding the token
```

**Auth is explicit-only**: a profile uses a token iff it declares
`token:`, and the value names the env var (not the token itself). Token
values live in `~/.config/spout/.env` so they don't get committed.

## Project config

```yaml
# <repo>/spout.yaml
server: work                    # profile name OR raw host:port
job: ml-pipeline                # dashboard grouping label
run_name: "exp-{n}"             # template for sequential runs

observe:                        # opt-in LLM observability (see observe.md)
  enabled: true
  default_interval: 60s
  model:
    type: api                   # 'api' | 'local' | 'cli'
    model: claude-haiku-4-5
    # token: ANTHROPIC_API_KEY  # env var name (default)
  rules:
    - name: training-stuck
      prompt: "Alert if loss hasn't decreased for 3+ epochs."
      sources: [training, gpu]

streams:
  - label: training
    command: python train.py --epochs 200
    dir: ./src/models           # relative to THIS yaml file
    env:
      CUDA_VISIBLE_DEVICES: "0"

  - label: gpu
    command: watch -n 2 nvidia-smi
```

## Field reference

### Top-level

| Field | Type | Combines | What |
|---|---|---|---|
| `default_server` | scalar | override | Fallback server profile |
| `server` | scalar | override | This project's server (profile or `host:port`) |
| `servers` | map | merge | Address book of profiles |
| `job` | scalar | override | Dashboard grouping |
| `run_name` | scalar | override | Template for sequential names |
| `storage` | scalar | override | Local archive path |
| `history` | bool | override | Persist `.raw` locally |
| `observe` | object | merge | LLM observability subsystem (see [observe.md](observe.md)) |
| `streams` | list | **replace** | Tmux panes for `spout run` (multi-stream) |

### `servers[]`

| Field | Required | Default |
|---|---|---|
| `url` | yes | — |
| `token` | no | — (omit = no auth) |

### `streams[]`

| Field | Required | Default |
|---|---|---|
| `label` | yes | — |
| `command` | yes | — |
| `dir` | no | dir of the yaml file that owns the stream |
| `env` | no | empty map |

### `observe`

| Field | Type | Default | What |
|---|---|---|---|
| `enabled` | bool | `false` | Turn the observer on |
| `default_interval` | scalar | `60s` | Fallback poll cadence |
| `model` | object | — | Backing LLM (see below) |
| `detectors` | object | all on | Toggle built-in detectors |
| `rules` | list (replace) | — | Custom prompts, folded into the one LLM call |
| `otel` | scalar | — | OTLP/HTTP endpoint to also export events to |

### `observe.model`

| Field | Required | Default | What |
|---|---|---|---|
| `type` | no | `api` | `api` (cloud) \| `local` (Ollama/OpenAI-compatible) \| `cli` |
| `model` | no | `claude-haiku-4-5` | Model id (type=api) |
| `token` | no | `ANTHROPIC_API_KEY` | Env-var **name** holding the key (type=api) |
| `endpoint` | when `type: local` | — | OpenAI-compatible URL |

### `observe.detectors`

Built-ins run with zero config when `observe` is on. Each is a bool, default `true`:
`classify` (run type), `loop`, `drift`, `amnesia`. See [observe.md](observe.md).

### `observe.rules[]`

| Field | Required | Default |
|---|---|---|
| `name` | yes | — |
| `prompt` | yes | — (free-text instruction folded into the LLM call) |
| `sources` | no | whole run (must match `streams[].label` when set) |
| `interval` | no | `observe.default_interval` (advisory) |

## Run-name templates

Used by multi-stream runs (`streams:` blueprint). Variables:

| Variable | Expands to | Example |
|---|---|---|
| `{n}` | Per-job auto-incrementing integer | `1`, `2`, `3` |
| `{input}` | Value typed at an interactive prompt before launch | `lr-sweep` |
| `{date}` | Today (YYYY-MM-DD) | `2026-04-29` |
| `{time}` | Now (HH-mm-ss) | `14-30-05` |
| `{ts}` | Unix timestamp | `1714312345` |
| `{t:FORMAT}` | Custom datetime | `{t:dd/MM HH:mm}` → `29/04 14:30` |

`{t:FORMAT}` tokens (case-sensitive): `YYYY YY MM DD dd HH hh mm ss`.
Other characters emitted verbatim. `{n}` counter is file-backed at
`~/.config/spout/counters/<job>.txt`.

Standalone runs (`spout run cmd` / `cmd | spout`) always use `word-xxxx`
— templates only apply to multi-stream blueprint launches.

## CLI environment variables

| Var | What |
|---|---|
| `SPOUT_SERVER` | Override `default_server` |
| `SPOUT_TOKEN_<NAME>` | Token value for profile `<name>` |
| `NO_COLOR` | Disable ANSI color output |
| `EDITOR` | Used by `spout config edit` `[planned]` |

The CLI never reads `SPOUT_TOKEN` directly — auth is explicit-only via
profile `token:`.

For server-side env vars (`SPOUT_STORE`, `SPOUT_S3_*`, etc.), see
[server/config.md](../server/config.md).

## `.env` files

Travel with `spout.yaml` at every level (deepest wins per variable).
A bare `.env` without a sibling yaml still contributes.

```
<repo>/experiments/exp1/.env       highest priority
<repo>/.env
~/.config/spout/.env               lowest priority
```

Used for: token values, `streams[].env` overrides, webhook URLs (`[planned]`).

## Commands that write config

| Command | Writes |
|---|---|
| `spout login [profile]` | `~/.config/spout/spout.yaml` (+ `.env`) |
| `spout init` | `./spout.yaml` from project template |
| First run (auto) | Annotated global template if `~/.config/spout/spout.yaml` is missing |

Existing non-empty files are never overwritten without confirmation.

## Templates (embedded)

Two annotated yaml templates ship inside the binary:

| Template | Used by | Target |
|---|---|---|
| `internal/config/templates/global.yaml` | first-run + `spout login` | `~/.config/spout/spout.yaml` |
| `internal/config/templates/project.yaml` | `spout init` | `./spout.yaml` |

To change what these commands write, edit the template file — they're
embedded via `go:embed` at build time.

## See also

- [commands.md](commands.md) — every CLI command and flag
- [future.md](future.md) — planned config fields and behaviors
- [../server/config.md](../server/config.md) — server-side env vars
