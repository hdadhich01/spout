# Config System (authoritative spec)

This is the single source of truth for spout's config data model. Any other
doc that describes config behavior defers to this file. `[planned]` tags
mark fields that aren't fully implemented in the CLI yet - the parser may
only understand a subset today.

## 1. Parsing rules

1. **One unified schema.** There is only one `Config` struct in the codebase.
   The global file and every project/folder file map to the exact same
   struct. There is no "system config" vs "project config" type distinction.

2. **Walk-up discovery.** On startup the CLI walks up the directory tree
   from `$CWD` toward the ceiling (`git rev-parse --show-toplevel` if in a
   git repo, otherwise `$HOME`). It collects every `spout.yaml` and `.env`
   it finds along the way. Finally, `~/.config/spout/spout.yaml` is loaded
   as the base, even when cwd is outside `$HOME`.

3. **Merge semantics.** When the same field appears at multiple levels:

   | Kind | How they combine | Examples |
   |---|---|---|
   | **Map** | MERGE, deepest wins per key | `servers:`, `env:` |
   | **List** | REPLACE, deepest wins entirely | `streams:`, `watch.rules:` |
   | **Scalar** | OVERRIDE, deepest wins | `job:`, `server:`, `default_server:` |

   A project that defines `streams:` completely overwrites any `streams:`
   from parents. A project that defines `servers:` adds to / overrides
   individual entries without dropping the global address book.

4. **Foreign-key validation**. `cfg.Validate()` checks that every string
   in `watch.rules[].sources` is the `label` of a stream in the merged
   `streams:` list, and that `watch.model.type: local` declares an
   `endpoint:`. `spout config`, `spout doctor`, and `runStreams()` all
   call this before doing work.

## 2. Priority order

Highest wins:

```
CLI flags (-s/-l/-n/...)
  > env vars (SPOUT_TOKEN, SPOUT_TOKEN_<PROFILE>, ...)
    > deepest spout.yaml (cwd)
      > ... parent dirs walking up ...
        > git-root spout.yaml
          > ~/.config/spout/spout.yaml
            > built-in defaults
```

`.env` files travel with `spout.yaml` at every level and are merged the
same way (deepest wins per variable). See [auth.md](auth.md) for token
resolution.

## 3. Global config (`~/.config/spout/spout.yaml`)

The user's personal environment setup and their private "address book" of
servers. Applies regardless of cwd.

```yaml
# =====================================================================
# ~/.config/spout/spout.yaml
# GLOBAL ENVIRONMENT & ADDRESS BOOK
# =====================================================================

# Fallback server profile if a project folder doesn't specify one.
default_server: spout.sh

# Where the CLI locally archives all .raw stream files regardless of tier.
storage: ~/.config/spout/storage

# Whether to persist .raw files locally (useful to disable for CI/CD).
history: true

# The "address book" of known Spout servers. MAP - merges with project files.
servers:
  spout.sh:
    url: spout.sh:443

  homelab:
    url: home.my-domain.net:3000

  work-cluster:
    url: spout.company.internal:8080
    token: SPOUT_TOKEN_WORK      # name of env var holding the token value
```

Auth is explicit-only: a profile uses a token iff it declares a `token:`
field, and the value of that field is the **name of the env var** that
holds the real token. The actual value lives in `~/.config/spout/.env` so
it never gets committed alongside the yaml. Profiles without `token:` are
treated as no-auth - spout sends no Authorization header. See
[auth.md](auth.md) for details.

## 4. Project config (`<repo>/spout.yaml`)

The execution blueprint. Defines what happens when the user runs `spout` in
a specific directory.

```yaml
# =====================================================================
# <repo>/ml-pipeline/spout.yaml
# PROJECT BLUEPRINT (execution + monitoring)
# =====================================================================

# ──────────────────────────────────────────────────────────────
# ROUTING & METADATA
# ──────────────────────────────────────────────────────────────

# Server target. Resolves as:
#   1. match in the merged `servers:` map    (e.g., "work-cluster")
#   2. otherwise treated as a raw host:port   (e.g., "localhost:3000")
server: work-cluster

# Dashboard grouping name for this directory.
job: ml-pipeline

# Template for sequential run names. Defaults to word-xxxx when omitted.
# Variables: {n}, {input}, {date}, {time}, {ts}, {t:FORMAT}
run_name: "exp-{n}"

# ──────────────────────────────────────────────────────────────
# WATCHDOG (opt-in LLM monitoring; parsed + validated today,
# polling loops not yet executed - see docs/roadmap.md)
# ──────────────────────────────────────────────────────────────

watch:
  enabled: true
  default_interval: 2m

  # LLM engine.
  model:
    type: local                           # 'local' | 'api' | 'cli'
    endpoint: http://localhost:11434      # required when type: local

  # Independent monitoring loops. LIST - replaces any parent watch.rules.
  rules:
    - name: high-memory
      prompt: "Alert if memory > 80% or signs of OOM risk."
      interval: 60s
      sources: [system-monitor, gpu]      # must match stream labels

    - name: training-stuck
      prompt: "Alert if loss hasn't meaningfully decreased for 3+ epochs."
      interval: 3m
      sources: [training, gpu]

# ──────────────────────────────────────────────────────────────
# STREAMS (the tmux panes spawned by bare `spout`)
# ──────────────────────────────────────────────────────────────
# Absolute source of truth for execution. LIST - a child file that
# defines `streams:` completely replaces any parent `streams:`.

streams:
  - label: training
    command: python train.py --epochs 200 --lr 0.001
    dir: ./src/models                     # relative to THIS yaml file
    env:
      CUDA_VISIBLE_DEVICES: "0"
      BATCH_SIZE: "64"

  - label: gpu
    command: watch -n 2 nvidia-smi
    # `dir` omitted -> defaults to the dir containing this yaml file

  - label: system-monitor
    command: htop -d 5

  - label: ui
    command: tensorboard --logdir ./logs
    dir: ./src/models
    env:
      PORT: "8080"
```

## 5. Field reference

| Field | Type | Level | Combines | What |
|---|---|---|---|---|
| `default_server` | scalar | global | override | Server used when no other source selects one |
| `server` | scalar | project | override | Profile name (→ `servers[name].url`) or raw `host:port` |
| `servers` | map | either | merge | Named server profiles `{name: {url: ...}}` |
| `job` | scalar | project | override | Dashboard grouping label |
| `run_name` | scalar | project | override | Template for sequential run names |
| `storage` | scalar | global | override | Local archive path for `.raw` files |
| `history` | bool | either | override | Persist `.raw` files locally |
| `watch` | object | project | merge | Watchdog config (sub-fields merge) |
| `watch.enabled` | bool | project | override | Master switch |
| `watch.default_interval` | duration | project | override | Default rule interval |
| `watch.model.type` | scalar | project | override | `local` \| `api` \| `cli` |
| `watch.model.endpoint` | scalar | project | override | Required when type: local |
| `watch.rules` | list | project | **replace** | Monitoring loops (see schema) |
| `streams` | list | project | **replace** | Tmux panes to launch for bare `spout` |

### `servers[]` schema

| Field | Required | Default |
|---|---|---|
| `url` | yes | - |
| `token` | no | - (omit = no auth for this profile) |

The `token:` value is the **name** of the env var that holds the auth
token, not the token itself. Values live in `~/.config/spout/.env`.
There is no implicit fallback to any default env var; omitting `token:`
means "this server takes no token."

### `streams[]` schema

| Field | Required | Default |
|---|---|---|
| `label` | yes | - |
| `command` | yes | - |
| `dir` | no | dir of the yaml file that owns the stream |
| `env` | no | empty map |

### `watch.rules[]` schema

| Field | Required | Default |
|---|---|---|
| `name` | yes | - |
| `prompt` | yes | - |
| `interval` | no | `watch.default_interval` |
| `sources` | yes | - (must match stream labels) |

## 6. Discovery in practice

Example walk-up from `~/work/ml-pipeline/experiments/exp1/` inside a git
repo rooted at `~/work/ml-pipeline/`:

```
~/work/ml-pipeline/experiments/exp1/spout.yaml    (deepest - highest priority)
~/work/ml-pipeline/spout.yaml                      (git root)
~/.config/spout/spout.yaml                         (global - fallback)
```

Each level can omit fields it doesn't care about. Walk stops at git root
(or `$HOME` if not in a repo), then appends the global file at the bottom.

`spout config` prints the exact files found + what every field resolves to.

## 7. Commands that touch config

| Command | What it does |
|---|---|
| `spout config` | Read-only. Prints loaded files + resolved values. |
| `spout login [profile]` | Interactive. Writes/updates `~/.config/spout/spout.yaml` and `~/.config/spout/.env`. |
| `spout init` | Writes the annotated project template into the current directory. |

On first run of any spout command, if `~/.config/spout/spout.yaml` is
missing or empty, the binary drops the annotated **global template**
there. Existing non-empty files are never overwritten.

None of these replace the files in-place without confirmation.

## 8. Starter templates

Two annotated templates ship inside the binary:

| Template source | Used by | Target |
|---|---|---|
| `internal/config/templates/global.yaml` | first-run auto-write + `spout login` | `~/.config/spout/spout.yaml` |
| `internal/config/templates/project.yaml` | `spout init` | `./spout.yaml` |

To change what those commands write, **edit the file in
`internal/config/templates/`** - both are pulled into the binary via
`go:embed` at build time. Don't look for Go string constants; there aren't any.

## 9. Open questions

- **YAML `env:` vs `.env` files**: do `.env` variables strictly override
  the per-stream `env:` block in yaml, or does yaml take precedence? Current
  intent (tentative): `.env` wins, since `.env` is the gitignored secret
  surface and should be able to override committed defaults.
