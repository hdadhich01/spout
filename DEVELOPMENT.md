# Spout - Development Record

For architecture, tiers, config, and product design, see ARCHITECTURE.md.

## Current State

**Phase: Core working (local / Tier 3)**

### What works

| Feature | Status |
|---------|--------|
| `command \| spout` (pipe mode) | Done |
| `spout run command` (tmux detach) | Done |
| `spout server` (local dashboard) | Done |
| `spout attach/kill/ls/rename/init` | Done |
| WebSocket streaming with batching | Done |
| xterm.js dashboard with status detection | Done |
| Session persistence + replay | Done |
| Metadata (host, user, dir, git branch) | Done |
| Name generation (word-xxxx, ~1.7B unique) | Done |
| Config system (system + project + folder + .env) | Done |
| Config walk-up to git root | Done |
| Server profiles (`-s work`) | Done |
| CLI short flags (`-s`, `-l`, `-n`, `-p`) | Done |
| Status detection (streaming/awaiting/ended) | Done |
| Copy buttons (command, directory) | Done |
| Bare `spout` shows status (active runs, server, tmux) | Done |
| `spout login` (interactive profile setup) | Done |
| `spout config` (show resolved config chain) | Done |
| `spout share` (print shareable URL) | Done |
| Auto-clipboard copy URL on run start | Done |
| Tab completion for attach/kill/rename | Done |
| Name collision handling (prompt replace/rename) | Done |
| Server reachability check before streaming | Done |
| Self-pipe guard (`spout run spout server` blocked) | Done |
| Duration + line count metrics (CLI + dashboard) | Done |
| Compound duration format (1h30m, 1d12h) | Done |
| Stats header on `spout ls` (totals, avg, errors) | Done |
| Auto-pager for long history (less -FRX --mouse) | Done |
| `spout delete` / `rm` (single & multi) | Done |
| `spout clean` (--all, --ended, --errors, --older, --keep) | Done |
| `spout open` (cross-platform browser) | Done |
| `spout logs` (replay run output to stdout) | Done |
| `spout doctor` (env checks) | Done |
| Folder-per-session storage (meta.json + data.raw) | Done |
| Auto-migrate flat session files to folder layout | Done |
| Exit code capture via OSC 9999 marker | Done |
| Status states: streaming / awaiting / success / error / ended | Done |
| Status colors consistent CLI ↔ dashboard | Done |
| Run server auto-detection (`findRunServer`) | Done |
| Custom grouped help with aliases inline | Done |
| Banner (whale + Spout text) on bare `spout` and `spout server` | Done |
| Interactive prompts (init overwrite, kill active, port reuse, etc.) | Done |
| `confirmTyped` for destructive ops (`clean --all`) | Done |
| Unified `spout.yaml` (no more `config.yaml` distinction) | Done |
| `internal/config/defaults.go` - no hardcoded values | Done |
| `LoadedFiles()` for showing the actual config chain | Done |
| `spout stats` standalone command | Done |
| `/api/health` + `probeSpout` compatibility check | Done |

### What's not built

| Feature | Effort | Needed for |
|---------|--------|-----------|
| Bare `spout` with sub-runs from config | Medium | Config-driven sessions |
| E2E encryption (AES-GCM + WebCrypto) | Medium | Tier 2 |
| TCP listener (:1337) | Small | Tier 1 (nc) |
| Token auth (server-side) | Small | Self-hosted + hosted |
| `spout server token create/list/revoke` | Small | Multi-user auth |
| Session TTL + cleanup | Small | All deployments |
| QR code in CLI | Small | UX |
| PostgreSQL backend | Medium | Hosted at scale |
| Privacy warnings (Tier 1 dashboard) | Small | Hosted |
| PWA + push notifications | Medium | Mobile alerts |
| Watchdog (regex + LLM) | Large | Smart alerts |
| Notifications (ntfy/Slack/Discord) | Medium | Alerts |
| Server-side naming (spout.sh assigns names) | Small | Hosted |
| Local run storage (CLI saves .raw locally) | Small | All tiers |
| `storage_dir` config option | Small | All |
| Session TTL with 2w default on hosted | Small | Hosted |
| `spout ls --history` (browse local run history) | Small | All tiers |
| Self-hosted merged view (server + local history) | Medium | Tier 3 |

## Development Log

### 2026-04-14 - Status taxonomy cleanup

- Dropped `error-running` status. "Wrote to stderr while running" was a noisy
  signal - lots of tools log to stderr without erroring.
- Mode-aware status computation in `Session.Status()`:
  - Pipe mode: only `streaming` / `ended` (no exit code available).
  - Run mode: `streaming` / `awaiting` / `success` / `error` / `ended`.
- Unified color palette across CLI (`color.go` + `status.go`) and both web
  dashboards. `success` is now dim green (not gray) so it's distinguishable
  from plain `ended`. `error` is muted red (#b91c1c) not alarm red.
- Hover popup boxes on status dots in the dashboard list and on the status
  badge in the session view, both driven by the same `statusInfo()` helper.

### 2026-04-14 - Compatibility probe + cleanup

- Added `GET /api/health` endpoint returning `{"spout": true}` so clients can
  confirm a server is actually spout (not just any HTTP 200 like spout.sh's
  landing page).
- New `probeSpout(addr)` helper in `validate.go` shared by `spout doctor`,
  bare `spout` status, and `checkServer`. Consistent terminology everywhere:
  `unreachable`, `auth required`, `reachable; not spout-compatible`,
  `spout-compatible`.
- `spout doctor` shows red ✗ for "reachable but not spout-compatible"
  (treated as a real failure, not a warning).
- Extracted `spout stats` into its own command (was previously bundled into
  `spout ls`).
- Replaced all em dashes with hyphens across the repo.

### 2026-04-09 - Polish + new commands

- New commands: `delete`/`rm`, `clean`, `open`, `logs`, `doctor`
- Custom grouped help with aliases inline
- Banner (whale + Spout text), aqua brand color, consistent label/section style
- Stats header on `spout ls` (total, avg, uptime, lines, data, errors)
- Auto-pager for long history with mouse scrolling
- Folder-per-session storage with auto-migration from flat layout
- Exit code capture via OSC 9999 marker for `spout run`
- New status states (success/error) with consistent colors
- Run server auto-detection: `open`, `share`, `logs`, `delete`, `rename` find
  the right server automatically without needing `-l`
- Interactive prompts: init overwrite, kill active, port reuse, login overwrite,
  config no-profiles, clean --all type-to-confirm
- Unified config file: everything is `spout.yaml` (system + project)
- Auto-migration of legacy `config.yaml` → `spout.yaml`
- All hardcoded values moved to `internal/config/defaults.go`
- `spout config` shows actual loaded files + resolved values (no more setup prompts)

### 2026-04-03 - Core implementation

- CLI: pipe mode, run mode, all session management commands
- Server: Fiber with WebSocket ingest + viewer, REST API, file persistence
- Dashboard: xterm.js terminal, run list with status dots, metadata
- Streaming: 16ms batched binary WebSocket, 64KB chunks
- Status detection: streaming/awaiting/ended with burst heuristics
- Names: ~1000 words × base36 suffix (~1.7B unique)
- Metadata: host, user, dir, git branch captured per run
- tmux: pipe-pane for output capture, session stays alive after command
- Config: system + project + folder levels, .env for tokens, walk-up to git root
- Profiles: named server profiles, `-s work` shorthand
- Documentation: ARCHITECTURE.md (comprehensive), CLAUDE.md (AI ref), DEVELOPMENT.md (log)

### 2026-03-24 - Project scaffolding

- Created documentation files
- Defined architecture, tiers, conventions
- Repository greenfield

## Next Up

1. Bare `spout` - read config, start sub-runs in tmux, stream each
2. Token auth - `spout server token create`, server middleware
3. E2E encryption - CLI AES-GCM + browser WebCrypto
4. TCP listener - nc support on :1337
5. Session TTL + cleanup
