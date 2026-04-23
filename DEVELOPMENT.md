# Spout - Development Record

For architecture / config / product details, start at
[docs/README.md](docs/README.md). For the authoritative config spec, see
[docs/config.md](docs/config.md).

## Current state

**Phase:** local / Tier 3 is feature-complete. Multi-stream launching
works. Config schema, templates, validation, and doctor are in place.
Encryption, hosted SaaS, and LLM watchdog execution are next.

### What works

#### Streaming + sessions
| Feature | Notes |
|---|---|
| `command \| spout` (pipe mode) | Peeks stdin first - aborts if upstream produces no output |
| `spout run cmd` (detached tmux) | Pipe-pane wired before command via new-session + send-keys ordering |
| `spout run` (no args, interactive) | Holding shell wrapper prints a colored welcome, then execs `$SHELL` |
| Bare `spout` with `streams:` | Launches each stream in its own pane under one tmux session |
| `spout server` (local dashboard) | Auto-populates empty `~/.config/spout/spout.yaml` on first run |
| `spout attach/kill/ls/rename` | |
| `spout delete` / `rm` / `clean` | Per-name, bulk, interactive menu with dynamic options |
| `spout open` / `share` / `logs` | Run server auto-detection via `findRunServer` |
| `spout stats` | Extracted standalone from `spout ls` |
| `spout init` / `login` / `config` / `doctor` | |

#### Status + colors
| Feature | Notes |
|---|---|
| Status taxonomy | `streaming` / `awaiting` / `success` / `error` / `killed` |
| Mode-aware `Status()` | Pipe only emits `streaming`; run mode has full palette |
| Colors synced CLI ↔ web | Exact hex via 24-bit truecolor ANSI |
| Brand aqua | `#5ac8e2` (Apple-system teal) |
| Status hex | green `#22c55e`, yellow `#eab308`, darkgreen `#166534`, red `#b91c1c`, gray `#555` |
| Hover tooltips on dashboard | Styled popups over status dots + session-page badge |
| Exit code capture | OSC 9999 marker scanned by server; fast-fail polling in `spout run` |
| Idle auto-kill (run mode) | `_stream` watchdog kills tmux after 10m idle post-marker |

#### Config system
| Feature | Notes |
|---|---|
| Unified schema | One `Config` struct for global + project, yaml tag `spout.yaml` everywhere |
| Walk-up discovery | cwd → git root → global file, deepest wins |
| Merge semantics | Scalars override, maps merge, lists replace |
| `streams[]` (was `runs`) | Labeled tmux panes with `dir:` and `env:` |
| `job:` grouping field | Dashboard grouping label (rendered CLI-side today) |
| `run_name:` templates | `{n}`, `{input}`, `{date}`, `{time}`, `{ts}`, `{t:FORMAT}` |
| `{t:FORMAT}` tokens | Moment/Java-style: `YYYY YY MM DD dd HH hh mm ss` |
| `{n}` counter | File-backed at `~/.config/spout/counters/<job>.txt` |
| `{input}` interactive prompt | User types value before any sessions are created |
| `watch:` subsystem | Parsed + validated (foreign keys, model endpoint). Execution deferred. |
| Foreign-key validation | `watch.rules[].sources` must match `streams[].label` |
| Tokens: explicit-only | `servers[<name>].token: ENV_VAR_NAME` - no implicit fallback |
| `.env` walk-up | Same discovery as `spout.yaml`, deepest file wins per variable |
| Embedded templates | `internal/config/templates/{global,project}.yaml` via `go:embed` |
| Auto-write global template | Drops annotated yaml into empty `~/.config/spout/spout.yaml` on first run |

#### Storage + server
| Feature | Notes |
|---|---|
| Folder-per-session | `~/.config/spout/storage/<name>/{meta.json, data.raw}` |
| Legacy `sessions/` migration | Auto-renamed to `storage/` on first server start |
| `GET /api/health` | Returns `{"spout": true}` for CLI compatibility probe |
| `GET /api/runs` + `/api/run/:name` | Run list and single-run metadata with `has_exit` + `exit_code` |
| `POST /api/run` | Pre-create session with metadata |
| `GET /api/run/:name/raw` | Replay bytes (used by `spout logs`) |
| `WS /ws/:name` (viewer) | Live + history replay |
| `WS /ingest/:name` (CLI) | 16ms batched binary with 64KB chunks |
| Exit marker (OSC 9999) | Server scans stream, stores `has_exit` + `exit_code` |

#### `spout doctor` (7 checks)
| Check | Surfaces |
|---|---|
| version | Binary version + VCS short SHA + install path |
| tmux / clipboard / browser / config | System tool + file presence |
| schema | Runs `cfg.Validate()` — watch foreign keys, model endpoint required |
| server | `/api/health` probe → `compatible` / `not compatible` / `unreachable` / `auth required` |
| token (conditional) | Env var named by profile's `token:` field + set/unset |
| storage | Path, writable probe, free disk (GB/TB formatted) |
| streams (conditional) | Per-stream `dir:` exists + first-token on PATH |
| watch (conditional) | HTTP GET against `watch.model.endpoint` |

### What's not built

| Feature | Effort | Bucket |
|---|---|---|
| E2E encryption (AES-GCM + WebCrypto) | M | Tier 2 |
| TCP listener `:1337` | S | Tier 1a |
| Token auth (server-side) | S | self-hosted team + hosted |
| `spout server token create/list/revoke` | S | multi-user auth |
| Session TTL + cleanup | S | all |
| Server-side `job` metadata | S | dashboard grouping |
| Dashboard grouping by `job` | S | self-hosted UX |
| `history: false` respect (server) | S | all |
| `storage:` field wired through CLI archive | S | all |
| Watch execution (LLM polling) | L | all |
| SQLite backend | M | self-hosted at scale |
| PostgreSQL + S3 backend | L | hosted SaaS |
| QR code in CLI | S | UX |
| `spout ls --history` (local-only runs) | S | all |
| PWA + push notifications | M | mobile alerts |

## Development log

### 2026-04-16 - Tokens explicit-only + run_name overhaul

- Tokens: stripped implicit fallbacks. A profile uses auth iff it sets
  `token: <ENV_VAR_NAME>`. No more `SPOUT_TOKEN_<PROFILE>` or default
  `SPOUT_TOKEN` magic. `spout config` token row now shows `none` when no
  profile auth is declared.
- `run_name` template: added `{input}` (interactive prompt before any
  sessions exist) and `{t:FORMAT}` (custom datetime, Moment/Java tokens
  `YYYY YY MM DD dd HH hh mm ss`). Dropped `{word}`. `{n}` counter only
  bumps when the template actually references it.

### 2026-04-16 - Doctor expansion + compatible rename

- `spout doctor` grew from 5 to up to 11 rows: version, tmux, clipboard,
  browser, config file, schema validation, server, token (conditional),
  storage (with disk free), stream preflight (per-stream dir + command
  check), watch endpoint probe.
- `humanBytes` extended to GB / TB.
- Renamed `spout-compatible` → `compatible`, `not spout-compatible` →
  `not compatible`. Dropped ` - ` separator between addr and reason in
  favor of two spaces; color carries the distinction.
- Path helper `shortPath` now collapses `/home/<user>` → `~/` regardless
  of cwd; `prettyPath` (used only in `spout config` tree) prefers
  `./`/`../` when the target is on cwd's direct ancestor chain.

### 2026-04-16 - spout config cleanup + templates

- Reworked `spout config` layout: dropped the "all configs merged"
  preamble + `context` section; renamed `loaded` → `sources`; streams and
  profiles each get their own section with counts; token row shows the
  env var name + `set`/`unset` state (or `none`); streams are column
  aligned; invalid config surfaces in a red `invalid` block.
- Added `token:` field to `Server` profile (env-var name, value in .env).
- Moved starter yaml out of Go string constants into
  `internal/config/templates/{global,project}.yaml` via `go:embed`.
- `Execute()` calls `config.EnsureSystemConfig()` on every CLI run so
  fresh installs get the annotated global yaml for free.

### 2026-04-15 - Phase 2: streams execution

- Bare `spout` with `streams:` defined now launches each entry as its
  own pane under a single tmux session. Same exit-marker wiring as
  `spout run` (new-session → pipe-pane → send-keys).
- Each pane pipe-panes to the server as `<run>-<label>`. Dashboard sees
  them as independent runs; user has one tmux session to attach to.
- `config.ResolveStreamDir` resolves each stream's `dir:` relative to
  the yaml file that owned it (tracked via a non-serialized `SourceFile`).
- `config.ExpandRunName` expands templates. `config.NextRunCounter` does
  file-backed per-job counter bumps.
- Pre-flight collision check across *all* projected stream names before
  any tmux or server sessions are created.
- `spout run` (args) unchanged. Pipe mode unchanged.

### 2026-04-15 - Phase 1: config schema rewrite

- `name` → `job`, `runs` → `streams`, `Run` type → `Stream` with
  added `Dir` and `Env` fields. Clean break; old yaml stops working.
- New scalars: `run_name`, `storage`, `history`.
- New `Watch` object with `WatchModel` and `WatchRule` types.
- `cfg.Validate()` enforces: watch rule sources → stream labels, and
  `watch.model.type=local` requires `watch.model.endpoint`.
- `spout config` shows all new fields + invalid section when validation
  fails.
- Renamed `storage_dir` → `storage`; renamed local folder
  `~/.config/spout/sessions/` → `~/.config/spout/storage/` with silent
  auto-rename on startup.

### 2026-04-14 - Status taxonomy cleanup

- Dropped `error-running`. Noisy signal (many tools write to stderr
  normally).
- Mode-aware `Session.Status()`: pipe mode only `streaming` / `ended`;
  run mode has full palette.
- `success` is dim green (not gray) - visually distinct from `ended`.
- `error` uses muted `#b91c1c`, not alarm red.
- Hover popup boxes on dashboard status dots + session badge.

### 2026-04-14 - Compatibility probe + cleanup

- Added `GET /api/health` for CLI compatibility probes.
- `probeSpout(addr)` in `validate.go`, shared by doctor / bare spout /
  `checkServer`.
- Extracted `spout stats` standalone.
- Replaced all em dashes with hyphens repo-wide.

### 2026-04-09 - Polish + new commands

- New commands: `delete`/`rm`, `clean`, `open`, `logs`, `doctor`.
- Custom grouped help with aliases inline.
- Banner (whale + Spout text), aqua brand color.
- Stats header, auto-pager, folder-per-session storage, exit-code marker.
- Run server auto-detection (`findRunServer`).
- Interactive prompts for destructive ops, unified `spout.yaml`,
  all hardcoded values moved to `internal/config/defaults.go`.

### 2026-04-03 - Core implementation

- Pipe mode, run mode, all session management commands.
- Fiber server with WebSocket ingest + viewer, REST API, file persistence.
- xterm.js dashboard with status dots and metadata.
- Status detection (streaming/awaiting/ended) with burst heuristics.
- Metadata: host, user, dir, git branch captured per run.

### 2026-03-24 - Scaffolding

- Documentation files, architecture tiers, conventions.

## Next up

Roughly ordered by value × unlock. See [docs/roadmap.md](docs/roadmap.md)
for the long list.

1. Server-side `job` field + dashboard grouping.
2. `history: false` respected by server + `storage:` wired through CLI.
3. Token auth — `spout server` middleware + Authorization header in CLI.
4. Watch execution — polling loop, prompt templating, notification sink.
5. E2E encryption — CLI AES-GCM + dashboard WebCrypto.
6. TCP listener on `:1337` for Tier 1a zero-install.
