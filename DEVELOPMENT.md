# Development

For architecture / design intent, start at [ARCHITECTURE.md](ARCHITECTURE.md).
For the AI-agent quick-ref, see [CLAUDE.md](CLAUDE.md).
For the config spec, see [docs/cli/config.md](docs/cli/config.md).

## Build plan (three phases)

```
Phase 1.  Self-hosted server (Tier 3) — local file storage, optional auth   ✅ done
Phase 2.  CLI ↔ server — refine until streaming feels clean                  ⏳ next
Phase 3.  Tiers 0 / 1 / 2 — nc/curl, hosted plaintext, hosted E2E            ⏸  later
```

**Where we are**: Phase 1 done. Server is dogfoodable on its own box
(`spout server` with optional `SPOUT_TOKEN`). Existing CLI works
against it unchanged.

**Phase 2 to-do** (pull from §12.1 in `enumerated-mixing-stream.md`):
TLV wire framing (atomic CLI + server + browser cut), CLI dual-write
to local mirror, `spout share` recovery, `spout doctor` end-to-end
checks.

## Current state

### What ships today

#### Streaming + sessions
| Feature | Notes |
|---|---|
| `command \| spout` (pipe mode) | Peeks stdin first; aborts cleanly if upstream produces nothing. On Linux, `captureUpstreamCommand` (process-group + parent match) records the upstream argv so the dashboard title shows it like a run-mode command |
| `spout run cmd` (detached tmux) | Pipe-pane wired, then `tmux respawn-pane` runs the wrapper directly (replaces older `send-keys` which leaked the `; printf …; exec $SHELL` echo into the captured stream) |
| `spout run` (no args, interactive shell) | Holding shell wrapper prints colored welcome, then execs `$SHELL` |
| Ctrl-C on pipe | Clean stop: flushes tail, closes WS normally, prints `stopped — N sent`, **exits 0** (was `stream failed: interrupted` + non-zero) |
| Terminal sizing | tmux pane + xterm both fixed at **120 × 30** (`config.DefaultTerminalCols/Rows`) so programs like claude/k9s render correctly without xterm/tmux width mismatch |
| Bare `spout` with `streams:` | Each stream → its own tmux pane under one session |
| `spout server` (local dashboard) | Auto-populates empty `~/.config/spout/spout.yaml` on first run |
| `spout attach/kill/ls/rename` | |
| `spout delete` / `rm` / `clean` | Per-name, bulk, interactive menu |
| `spout open` / `share` / `logs` | Run server auto-detection via `findRunServer` |
| `spout observe <run>` | Print observer assessment + timeline from the local copy |
| `spout stats` | Aggregate metrics |
| `spout init` / `login` / `config` / `doctor` | |

#### Observability (`observe`) — opt-in
| Feature | Notes |
|---|---|
| CLI-side observer engine | `internal/observe`: byte tap in `streamToSession`, cheap pre-filter, clamped adaptive interval, single-flight LLM call, final synthesis on close |
| Backends | `observe.model.type`: `api` (Anthropic via net/http, prompt-cache breakpoint) shipped; `local`/`cli` fall back to stub for now. No key → network-free stub |
| Built-in detectors | `classify` / `loop` / `drift` / `amnesia`, all in one LLM call; custom `observe.rules` fold in |
| Events | local `events.jsonl` (canonical) + `POST/GET /api/run/:name/events` (server stores opaquely); `spout observe` + dashboard observe panel render them |
| Run lineage | git commit captured; dashboard groups sibling runs by command + dir (zero-LLM) |
| Flags / export | `--observe` / `--no-observe` per-run override; `--otel <endpoint>` exports events as OTLP/HTTP spans |
| Config | unified `observe:` block (replaces the old parsed-but-dead `watch:`, which migrates forward with a deprecation warning) |

See [docs/cli/observe.md](docs/cli/observe.md).

#### Status + colors
| Feature | Notes |
|---|---|
| Status taxonomy | `streaming` / `awaiting` / `success` / `error` / `killed` |
| Mode-aware `Status()` | Pipe-only emits `streaming`; run mode has full palette |
| CLI ↔ web color sync | Exact hex via 24-bit truecolor ANSI |
| Brand aqua | `#5ac8e2` |
| Hover tooltips on dashboard | Status dots + session-page badge |
| Exit code capture | OSC 9999 marker (will migrate to EXIT opcode — see below) |
| Idle auto-kill (run mode) | `_stream` watchdog kills tmux after 10m idle post-marker |

#### Config system
| Feature | Notes |
|---|---|
| Unified schema | One `Config` struct for global + project, `spout.yaml` everywhere |
| Walk-up discovery | cwd → git root → global file, deepest wins |
| Merge semantics | Scalars override, maps merge, lists replace |
| `streams[]` | Labeled tmux panes with `dir:` and `env:` |
| `job:` grouping field | Dashboard grouping (rendered CLI-side today) |
| `run_name:` templates | `{n}`, `{input}`, `{date}`, `{time}`, `{ts}`, `{t:FORMAT}` |
| `{n}` counter | File-backed at `~/.config/spout/counters/<job>.txt` |
| `{input}` interactive prompt | User types value before any sessions are created |
| `observe:` subsystem | Engine shipped (see Observability above + ARCHITECTURE.md §7) |
| Foreign-key validation | `observe.rules[].sources` must match `streams[].label` |
| Tokens: explicit-only | `servers[<name>].token: ENV_VAR_NAME`; no implicit fallback |
| `.env` walk-up | Same discovery as `spout.yaml`, deepest wins per variable |
| Embedded templates | `internal/config/templates/{global,project}.yaml` via `go:embed` |

#### Storage + server
| Feature | Notes |
|---|---|
| Folder-per-session | `~/.config/spout/storage/<name>/{meta.json, data.raw}` |
| Legacy `sessions/` migration | Auto-renamed to `storage/` on first server start |
| `GET /api/health` | Returns `{"spout": true}` for CLI compatibility probe |
| `GET /api/runs` + `/api/run/:name` | Run list and metadata with `has_exit` + `exit_code` |
| `POST /api/run` | Pre-create session with metadata |
| `GET /api/run/:name/raw` | Replay bytes (used by `spout logs`) |
| `WS /ws/:name` (viewer) | Live + history replay |
| `WS /ingest/:name` (CLI) | 16ms batched binary with 64KB chunks |
| Exit marker (OSC 9999) | Server scans stream for marker (will migrate to TLV EXIT opcode) |

#### Auth + admin (Tier 1 / Tier 3)
| Feature | Notes |
|---|---|
| Two credentials | Ingest token (CLI `Bearer`, send-only) vs admin (loopback same-box, or `/admin/login` cookie via `SPOUT_ADMIN_PASSWORD`) |
| Multi-token store | `spout server token create/list/revoke` + `/admin` UI; file-backed `tokens.json`, reload-on-change so a live server sees CLI changes |
| Auth activates when configured | Nothing set → same-box open, remote denied; legacy `SPOUT_TOKEN` still works; `SPOUT_PUBLIC=true` bypasses all |
| Routes | `/` → `/admin` (dashboard + tokens); `/admin/login`+`logout`; `GET/POST /api/tokens`, `POST /api/tokens/revoke` (body, bulletproof), `DELETE /api/tokens/:label` (URL-decoded), `GET /api/admin/whoami` (powers the dashboard's "log out" hide on loopback + remote-access hint) |
| CLI sends token | `/ingest` WS + `POST /api/run` now carry `Authorization` so remote Tier-3 push works |

#### Web UI design language
The dashboard + run viewer follow a deliberate **TUI-panel** aesthetic
(k9s / lazygit, in a browser). Worth knowing before editing the HTML.

| Element | Convention |
|---|---|
| Font | All monospace (SF Mono / JetBrains / Cascadia / Menlo). Hierarchy is **weight + size + color**, never family. |
| Borders | Single thin frame around each page, with inner "panel-on-border" labels (a `span` overlapping the rule, bg-matched to the panel). Section labels use the same pattern. |
| Tree line | Multi-stream runs nest under a `2px solid var(--aqua-dim)` left border — distinct from the `--border` (#383838) frame so it reads as a branch, not duplicate chrome. |
| Path vocabulary | The hierarchy `job / run / stream` uses slash-separated `.path` spans on the dashboard and a matching `.crumbs` breadcrumb on the run page. Same words on both sides. |
| Tokens | `--bg #0a0a0a`, `--panel #0e0e0e`, `--border #383838`, `--aqua #5ac8e2`. `--ease-out: cubic-bezier(0.23, 1, 0.32, 1)` is the only easing. |
| Motion | Only `transform` / `opacity` / `background-color` animate. Buttons get `:active { transform: scale(0.97) }`. Hover styles are gated by `@media (hover: hover) and (pointer: fine)`. `prefers-reduced-motion` kills all transitions. |
| Status colors | Per CLAUDE.md palette: streaming green, awaiting yellow, success darkgreen, error red, killed gray (`--c-killed: #6a6a6a` — brighter than `--dim` so ended runs are visible). |
| Run viewer chrome | Two rows: breadcrumb `← runs   job / run / stream   …   copy cmd  copy dir`, then (for multi-stream) `streams: [tabs]`. |
| Terminal frame | xterm wrapped in a Mac-window-style container — rounded 10px, three traffic-light dots, a `user@host` plain-text label + `$ command` shown as a bordered code chip, status pill + time + bytes on the right. |
| Observe panel | Right rail (340px) that only appears once events arrive. In stub mode (no LLM key) a yellow banner explains how to enable real summaries. |

When changing the palette: update both `cmd/spout/cmd/color.go` AND
`web/static/{index,session,login}.html`, then `cp web/static/*.html
internal/server/static/`.

#### `spout doctor`
| Check | What it surfaces |
|---|---|
| version | Binary version + VCS short SHA + install path |
| tmux / clipboard / browser / config | System tool + file presence |
| schema | Runs `cfg.Validate()` — watch FK + model endpoint |
| server | `/api/health` probe → compatible / not / unreachable / auth required |
| token (conditional) | Env var named by profile's `token:` |
| storage | Path, writable probe, free disk |
| streams (conditional) | Per-stream `dir:` exists + first-token on PATH |
| observe (conditional) | HTTP GET against `observe.model.endpoint` when `type: local` |

### What's next

Tracked in [ARCHITECTURE.md §14](ARCHITECTURE.md#14-deferred-items)
under deferred and §15 under rejected approaches. The next implementation
arc, in dependency order:

**Foundations:**
1. `Store` + `Session` interface refactor (split `internal/store/store.go`)
2. TLV wire framing (CLI + server + browser; replaces OSC 9999)

**Storage:**
3. Strategy E in server (write to local NVMe during run + per-session caps)
4. R2 multipart upload at end of run (with `aws-sdk-go-v2`)
5. SQLite backend (via `modernc.org/sqlite`)
6. Postgres backend (for hosted)

**CLI:**
7. CLI dual-write (local FileStore + WS in parallel)
8. `spout share` (recovery / re-publish from local)
9. `spout add-stream <run>` (attach a new pane to a running multi-stream run)
10. `spout ls --history` (read local archive)
11. First-run UX one-line note

**Tier 0:**
12. TCP listener on `:1337`
13. HTTP chunked POST `/ingest/:name`

**Tier 2:**
14. CLI AES-GCM encryption layer
15. URL fragment + key generation
16. Browser WebCrypto decryption
17. `encrypted_meta` blob handling
18. Keyless-viewer browser UX
19. `--no-encrypt` flag

**Auth + scale:**
20. Token auth middleware
21. Async broadcast + drop slow viewers
22. Per-IP / per-stream rate limits
23. Session TTL sweeper
24. Doctor enhancements (auth probe, WS check)

**Notifications:**
26. CLI rule engine
27. Server alert routing
28. Notification sinks (Slack/Discord/email/push/webhook)
29. VAPID push setup
30. Tier 0 server-side state alerts

**Replay UX + dashboard:**
31. Replay window cap (4 MB default)
32. Auto-load on scroll-up
33. Jump-to-time markers
34. Download full log
35. WS-failure dashboard banner
36. stdout/stderr filter toggle

**Distribution:**
37. Curl one-line installer
38. GitHub Actions release pipeline (multi-arch prebuilts)
39. Docker image
40. Helm chart (Phase 3 if pressed)

Total estimate: ~6-8 weeks of focused work for the full release.

## Build / install

```bash
go build ./...              # both binaries
go install ./cmd/spout/     # install CLI as `spout`
go vet ./...
go test ./...
make build                  # both binaries to bin/
```

After editing `web/static/*.html`:
```bash
cp web/static/*.html internal/server/static/
```

Yaml templates in `internal/config/templates/` are embedded via
`go:embed` — changes take effect on next build.

## Development log

### 2026-04-29 — Doc reorg (folder partitioning, decisive fluff cuts)

- **Layout shipped**: 4 root files (`README`, `ARCHITECTURE`, `CLAUDE`, `DEVELOPMENT`) + `docs/` partitioned into `cli/` (commands, config, future) and `server/` (routes, config, future), plus `docs/faq.md` cross-cutting.
- **Cuts**: removed `docs/cli.md` (854→292 lines, -66%), `docs/config.md` (402→234, -42%), `docs/api.md` (327→244, -25%), `docs/faq.md` (461→301, -35%). Net total docs −578 lines (4134→3581) despite adding 3 new `future.md` files.
- **Earlier consolidation** (April 28): folded `docs/tiers.md`, `docs/storage.md`, `docs/encryption.md`, `docs/notifications.md` into ARCHITECTURE.md sections (§3, §6, §5, §7) — they were design narrative, not reference.
- **`future.md` per folder**: planned items now grouped by surface (CLI vs server) so future planning can scan one file per concern.
- **Cross-references** updated across README, ARCHITECTURE, CLAUDE, DEVELOPMENT to point at new paths.
- **Plan file** §13 + appendix updated to match shipped layout.

### 2026-04-28 — Python SDK + Colab strategy locked

- **Pip name**: `spoutsh` (verified `spout` is taken on PyPI by an abandoned 12-month-stale package by `daviesjamie`). Pip name and Python module name match: `pip install spoutsh; import spoutsh`.
- **PEP 541 reclamation**: pursue in parallel for the existing `spout` package. If granted, migrate `spoutsh` → `spout` (alias `spoutsh` for back-compat). If refused, `spoutsh` stays.
- **Implementation**: pure Python WebSocket client. ~400-600 LOC. Deps: `websockets`, `cryptography` (Tier 2 only), `pyyaml`. Wheel ~2-3 MB. No binary download.
- **Colab strategy**: tab-close survival is the killer feature — WS from Colab runtime to spout.sh is independent of user's browser tab; bytes keep flowing for ~90 min Colab idle window. Spout doesn't prevent Colab timeout but captures everything up to it.
- **Colab template**: ship `spout.sh/colab-template.ipynb` + "Open in Colab" button on landing page. Pre-built notebook with cell-magic + ML training boilerplate.
- **Caveats documented**: `tqdm.notebook` writes widgets not stdout (recommend plain `tqdm`); ephemeral filesystem (spout.sh is canonical for Colab); Pro+ "background execution" is unreliable.
- Full design in [ARCHITECTURE.md §14.1](ARCHITECTURE.md#141-python-sdk--google-colab-locked-design).

### 2026-04-28 — Naming + flag lockdown

- **Naming model locked**: `job > run > stream(s)` (replaces ambiguous use
  of "session" in user-facing strings). Industry parallel: GitHub Actions
  workflow run → job → step. See [docs/cli/commands.md](docs/cli/commands.md) and [ARCHITECTURE.md §3](ARCHITECTURE.md#3-the-four-tiers).
- **Flag renames** locked (code catches up incrementally):
  - `attach --all` → `attach --tiled` (resolves `--all` meaning conflict; `--all` means "operate on all runs" elsewhere; `--tiled` describes the layout)
  - `--no-server` → `--offline` (clearer; the local viewer in `spout open` is also a "server" in a sense — `--offline` describes intent)
- **Alias dropped**: `spout history` (was alias for `ls`; conflicted with `ls --history` flag — kept `list` alias for discoverability)
- **`--json` flag** added to spec for `ls`, `stats`, `config`, `doctor` (machine-parseable output for scripts / CI)
- **New planned commands**: `spout tail <run>` (live follow), `spout config edit / get / set` (yaml editing without manually opening files), `spout exec` (subprocess wrapper for stdout/stderr capture)
- Doc restructuring: planned items are now grouped at the bottom of every doc that has them (CLI ref, config schema, API ref, README) for fast scan.

### 2026-04-28 — Architecture rewrite + doc restructure

- Replaced phased plan with comprehensive [ARCHITECTURE.md](ARCHITECTURE.md) spec.
- New tier ladder: 0/1/2/3 use-case framing (replaces 1a/1b/2 encryption framing).
- Storage strategy locked: Strategy E (local NVMe during run + R2 multipart at end).
- TLV wire framing decided (replaces OSC 9999 marker for exit codes).
- CLI dual-write commitment (local + server in parallel) is the durability anchor.
- Tier 2 E2E spec'd: AES-GCM, key in URL fragment, encrypted_meta blob.
- Notification architecture: CLI evaluates rules, server routes to sinks.
- Doc structure rethought: README.md (users) + ARCHITECTURE.md (master spec) +
  CLAUDE.md (AI ref) + DEVELOPMENT.md (dev log) + topic docs in `docs/`.
- Removed: `docs/README.md`, `docs/dashboard.md`, `docs/product.md`,
  `docs/auth.md`, `docs/roadmap.md` (consolidated into ARCHITECTURE.md
  + topic docs).

### 2026-04-16 — Tokens explicit-only + run_name overhaul

- Tokens: stripped implicit fallbacks. A profile uses auth iff it sets
  `token: <ENV_VAR_NAME>`. No more `SPOUT_TOKEN_<PROFILE>` or default
  `SPOUT_TOKEN` magic. `spout config` token row shows `none` when no
  profile auth is declared.
- `run_name` template: added `{input}` (interactive prompt before any
  sessions exist) and `{t:FORMAT}` (custom datetime, Moment/Java tokens
  `YYYY YY MM DD dd HH hh mm ss`). Dropped `{word}`. `{n}` counter only
  bumps when the template references it.

### 2026-04-16 — Doctor expansion + compatible rename

- `spout doctor` grew from 5 to up to 11 rows: version, tmux, clipboard,
  browser, config file, schema validation, server, token (conditional),
  storage (with disk free), stream preflight, watch endpoint probe.
- `humanBytes` extended to GB / TB.
- Renamed `spout-compatible` → `compatible`, `not spout-compatible` →
  `not compatible`. Color carries the distinction.
- Path helper `shortPath` collapses `/home/<user>` → `~/`.

### 2026-04-16 — `spout config` cleanup + templates

- Reworked `spout config` layout: dropped "all configs merged" preamble;
  renamed `loaded` → `sources`; streams and profiles each get their own
  section; token row shows env var name + `set`/`unset` state.
- Added `token:` field to `Server` profile (env-var name; value in `.env`).
- Moved starter yaml out of Go string constants into
  `internal/config/templates/{global,project}.yaml` via `go:embed`.
- `Execute()` calls `config.EnsureSystemConfig()` so fresh installs get
  the annotated global yaml for free.

### 2026-04-15 — Phase 2: streams execution

- Bare `spout` with `streams:` defined now launches each entry as its
  own pane under a single tmux session. Same exit-marker wiring as
  `spout run` (new-session → pipe-pane → send-keys).
- Each pane pipe-panes to the server as `<run>-<label>`. Dashboard sees
  them as independent runs; user has one tmux session to attach to.
- `config.ResolveStreamDir` resolves each stream's `dir:` relative to
  the yaml file that owned it (tracked via `Stream.SourceFile`).
- `config.ExpandRunName` expands templates. `config.NextRunCounter`
  does file-backed per-job counter bumps.
- Pre-flight collision check across *all* projected stream names.

### 2026-04-15 — Phase 1: config schema rewrite

- `name` → `job`, `runs` → `streams`, `Run` type → `Stream` with added
  `Dir` and `Env` fields. Clean break.
- New scalars: `run_name`, `storage`, `history`.
- New observability object (shipped as `Observe`/`ObserveModel`/`ObserveRule`;
  originally landed under the `watch:` key, since renamed to `observe:`).
- `cfg.Validate()` enforces observe rule sources → stream labels and
  `observe.model.type=local` requires `observe.model.endpoint`.
- `spout config` shows new fields + invalid section when validation fails.
- Renamed `storage_dir` → `storage`; renamed local folder
  `~/.config/spout/sessions/` → `~/.config/spout/storage/` with auto-rename.

### 2026-04-14 — Status taxonomy cleanup

- Dropped `error-running` (noisy; many tools write to stderr normally).
- Mode-aware `Session.Status()`: pipe mode only `streaming` / `ended`;
  run mode has full palette.
- `success` is dim green (not gray) — visually distinct from `ended`.
- `error` uses muted `#b91c1c`, not alarm red.
- Hover popup boxes on dashboard status dots + session badge.

### 2026-04-14 — Compatibility probe + cleanup

- Added `GET /api/health` for CLI compatibility probes.
- `probeSpout(addr)` in `validate.go` shared by doctor / bare spout.
- Extracted `spout stats` standalone.
- Replaced em dashes with hyphens repo-wide.

### 2026-04-09 — Polish + new commands

- New commands: `delete`/`rm`, `clean`, `open`, `logs`, `doctor`.
- Custom grouped help with aliases inline.
- Banner (whale + Spout text), aqua brand color.
- Stats header, auto-pager, folder-per-session storage, exit-code marker.
- Run server auto-detection (`findRunServer`).
- Interactive prompts for destructive ops, unified `spout.yaml`.
- Hardcoded values moved to `internal/config/defaults.go`.

### 2026-04-03 — Core implementation

- Pipe mode, run mode, all session management commands.
- Fiber server with WebSocket ingest + viewer, REST API, file persistence.
- xterm.js dashboard with status dots and metadata.
- Status detection (streaming/awaiting/ended) with burst heuristics.
- Metadata: host, user, dir, git branch captured per run.

### 2026-03-24 — Scaffolding

- Documentation files, architecture tiers, conventions.

## Conventions for new commits

- Commit messages: imperative mood, area prefix. `server: add session TTL`.
- New tasks: track in `.claude/plans/` plan files or in code comments
  as needed; the master architecture lives in ARCHITECTURE.md.
- Doc updates: keep ARCHITECTURE.md and topic docs in sync. If a topic
  doc and ARCHITECTURE.md disagree, ARCHITECTURE.md wins.
