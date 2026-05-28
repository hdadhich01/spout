# CLAUDE.md — Spout

AI agent quick-ref. **First read for any agent picking up work on this
repo.**

## Read these first, in order

1. **[ARCHITECTURE.md](ARCHITECTURE.md)** — the master architecture spec.
   Every design decision, trade-off, and caveat is there. **If
   ARCHITECTURE.md and code disagree, ARCHITECTURE.md wins** until the
   code catches up.
2. **[DEVELOPMENT.md](DEVELOPMENT.md)** — current state, what's
   shipping, dev log.
3. Topic docs in `docs/` as you need them — see file map below.

## What Spout is in 30 seconds

A single Go binary that pipes terminal output to a live web dashboard.
Same binary acts as CLI (`spout`) or server (`spout server`). Four
tiers serve four use cases (zero-install via `nc`, hosted plaintext,
hosted E2E, self-hosted) — one architecture, runtime config decides.

The two operating principles:
1. **Local archive is canonical.** CLI writes every byte locally; the
   server is a TTL-bounded convenience layer.
2. **Server is a thin fan-out hub.** Bytes flow through; expensive work
   (encryption, rendering) lives at the producer + browser.

See [ARCHITECTURE.md §1-3](ARCHITECTURE.md#1-north-star).

## Naming model (locked)

```
job        (optional grouping label across runs — pure metadata)
└─ run     (one user invocation — owns the URL, top-level addressable thing)
   └─ stream(s)  (labeled byte stream(s) within a run)
```

User-facing strings say **"run"** and **"stream"**. The word
**"session"** is internal-only (tmux session, server session record);
where it leaks into user-visible CLI output today, fix it on next touch.

**Single-stream runs** (pipe / `spout run cmd`) have one stream with an
auto-derived label (not user-visible). **Multi-stream runs** (blueprint
from `streams:` in yaml, or grown via `spout add-stream`) have N
labeled streams.

Industry parallel: GitHub Actions (workflow run → job → step). See
[docs/cli/commands.md](docs/cli/commands.md) and [ARCHITECTURE.md §3](ARCHITECTURE.md#3-the-four-tiers).

## Build / test

```bash
go build ./...            # build everything
go install ./cmd/spout/   # install CLI as `spout`
go vet ./...              # lint
go test ./...             # test
make build                # both binaries to bin/
```

After editing `web/static/`, sync to the embed dir:
```bash
cp web/static/*.html internal/server/static/
```

Yaml templates are embedded via `go:embed` — edits to
`internal/config/templates/*.yaml` take effect on the next build.

## File map

### CLI (`cmd/spout/cmd/`)

```
root.go         Pipe mode, bare `spout` dispatch, streamToSession(),
                resolveServer(), [planned: TLV framing, dual-write,
                Tier 2 encryption hooks]
run.go          spout run — detached single command, interactive shell,
                multi-stream blueprint launcher, AND --into <run> mode
                (add a new stream to an existing multi-stream run)
streams.go      runStreams: tmux orchestration for multi-stream runs
                (called from run.go when no args + streams: yaml exists)
local.go        Local-store target resolver (resolveLocalTargets,
                resolveLocalSingle, pickLocalRun, openLocal) shared by
                share / rename / logs / delete
server.go       spout server (embedded, port-in-use prompt)
token.go        spout server token create/list/revoke (ingest-token admin)
status.go       Bare `spout` status display
stream.go       _stream (hidden, invoked by tmux pipe-pane; watchdog auto-kill)

attach.go       spout attach (syscall.Exec into tmux)
kill.go         spout kill (with active-session confirmation)
ls.go           spout ls / list / history (with pager)
stats.go        spout stats (aggregate metrics)
rename.go       spout rename
delete.go       spout delete / rm / clean (bulk, interactive)
share.go        spout share (auto-detect server) [planned: upload from local]
open.go         spout open (xdg-open / open) [planned: local-archive fallback]
logs.go         spout logs (replay output to stdout)
observe.go      spout observe (print observer assessment + timeline from local copy)

doctor.go       spout doctor (env + connectivity checks)
init.go         spout init (writes project template)
login.go        spout login (interactive profile setup)
config_cmd.go   spout config (sources / resolved / streams / profiles)

meta.go         Collects hostname, user, git branch
color.go        ANSI truecolor helpers, banner, label, section
clipboard.go    Cross-platform clipboard copy
completion.go   Tab completion for tmux sessions
help.go         Custom grouped help with aliases inline
prompt.go       confirm / confirmYes / confirmTyped / askLine
pager.go        withPager (less -FRX --mouse) for ls
validate.go     checkServer, findRunServer, resolveSessionName, probeSpout
```

### Shared packages (`internal/`)

```
config/config.go          Config struct, Load, Validate, Resolve, walk-up, merge
config/streams.go         Stream dir resolution, ExpandRunName, NextRunCounter
config/templates.go       go:embed hooks for starter yaml
config/templates/         global.yaml + project.yaml (source of truth)
config/defaults.go        Hardcoded constants + DefaultStorageDir

server/server.go          Fiber app, REST + WebSocket handlers, embedded dashboard
server/static/            Embedded dashboard (sync target of web/static/)

store/iface.go            Store + Session interfaces (+ AppendEvent/Events)
store/file.go             FileStore (folder per run; meta.json + data.raw + events.jsonl)
store/session.go          Session struct, status taxonomy, Write/Subscribe/Close
store/sqlite.go           SQLiteStore (metadata in DB)                [planned]
store/postgres.go         PostgresStore (hosted)                      [planned]
store/r2.go               R2 multipart upload helper                  [planned]

server/auth.go            Auth: ingest tokens (Bearer) vs admin (loopback / login cookie)
server/tokens.go          File-backed ingest-token store (reload-on-change)
server/caps.go            Per-session size cap + rolling window
server/ratelimit.go       Per-IP / per-stream rate limit              [planned]
server/ttl.go             Session TTL sweeper                         [planned]
server/tcp.go             Tier 0 raw TCP listener on :1337            [planned]

crypto/cipher.go          Tier 2 AES-GCM helpers                      [planned]
crypto/keygen.go           Random key + nonce generation               [planned]

notify/engine.go          Alert routing (rule eval now lives in observe) [planned]
notify/sinks.go           Slack/Discord/Email/Webhook/VAPID push      [planned]

observe/engine.go         CLI-side observer: byte tap + adaptive loop + sinks
observe/prefilter.go      Cheap signal gate (velocity/repeat/error/idle)
observe/client.go         Client iface + stub + heuristic classifier
observe/client_api.go     Anthropic Messages API client (net/http, prompt cache)
observe/prompt.go         System/user prompt assembly + detector specs
observe/sink.go           FileSink (events.jsonl) + ServerSink (POST events)
observe/otel.go           OTLP/HTTP span exporter sink (--otel)
observe/events.go         Event / Observation / Metric / Decision types

names/names.go            Word-xxxx generator (~1000 words × base36 suffix)
parser/parser.go          ANSI control-sequence cleaning
types/types.go            Shared Line type
```

### Entry points

```
cmd/spout/main.go          CLI entry (wires Execute + PrintError)
cmd/server/main.go         Standalone server binary (wires SPOUT_STORE backend)
```

## Key patterns

**Two WebSocket libraries** — `coder/websocket` (CLI, `net/http`) and
`fasthttp/websocket` (server, Fiber). Different HTTP stacks, same protocol.

**FastHTTP buffer reuse** — `c.Params()` and `c.Query()` return slices
into a buffer that fasthttp reuses after WebSocket upgrade. Always copy
before `Upgrade()`:
```go
name := string(append([]byte{}, c.Params("name")...))
```

**Static file sync** — `go:embed` doesn't follow symlinks. Dashboard
source is in `web/static/`; must be `cp`'d to `internal/server/static/`
for embedding. Yaml templates are colocated with their embed decl at
`internal/config/templates/`.

**Config schema authority** — `internal/config/config.go` defines the
`Config` struct; [docs/cli/config.md](docs/cli/config.md) is the spec. Merge
rules: scalars override, maps merge, lists replace. `Watch` object is
merged field-by-field; its `Rules` list replaces.

**Config discovery** — one file format (`spout.yaml`), one schema,
multiple locations. Walks up from cwd to git root (or `$HOME`),
collects `spout.yaml` and `.env` files. Merges deepest-wins. Global
file is `~/.config/spout/spout.yaml`. `config.EnsureSystemConfig()` is
called from `Execute()`.

**Tokens: explicit-only** — `servers[<name>].token` names the env var
that holds the auth value. No implicit fallbacks.
`Config.TokenVarFor(profile)` returns `""` when no auth declared.

**No hardcoded values** — every default lives in
`internal/config/defaults.go`. Never write `"localhost:3000"` or
`"spout.sh"` as a literal anywhere else.

**Run server auto-detection** — name-taking commands (`open`, `share`,
`logs`, `delete`, `rename`) use `findRunServer(name)` instead of
`resolveServer()`. Tries resolved server first, then localhost as
fallback.

**Spout compatibility check** — `probeSpout(addr)` hits `/api/health`
and confirms response is `{"spout": true}`. Used by doctor / bare spout
/ `checkServer`. Terminology: `unreachable` / `auth required` /
`not compatible` / `compatible`.

**Streams launching** — `runStreams()` in `streams.go` creates a single
tmux session with one pane per stream via `split-window`, pipe-panes
each to the server as `<run>-<label>`. Pre-flight collision check for
*all* projected names before any sessions are created.

**Run-mode exit-marker race** — tmux pane must have pipe-pane wired
*before* the user's command runs. Both `spout run` and streams do:
(1) `new-session -d` with a holding shell, (2) `pipe-pane`,
(3) `send-keys` the real command. (Note: planned migration to TLV EXIT
opcode replaces inline OSC 9999 marker — see ARCHITECTURE.md §4.1.)

**Status taxonomy** — single source of truth across CLI and web:
`streaming` / `awaiting` / `success` / `error` / `killed`.
`Session.Status()` is mode-aware (pipe-mode only emits `streaming`).
CLI colors are 24-bit truecolor ANSI matching exact web hex.

**Visual style** — `cmd/spout/cmd/color.go` defines `banner`, `label`,
`section`, plus `info`/`ok`/`warn`/`fail`. Brand aqua `#5ac8e2`.
Banner shows on bare `spout` and `spout server` only.

**Custom help** — `cmd/spout/cmd/help.go` overrides cobra's default.
Groups by `GroupID` (`streaming`, `sessions`, `server`, `setup`),
shows aliases inline as `name (alias1/alias2)`.

## Palette

Changing one of these requires editing both `cmd/spout/cmd/color.go`
AND `web/static/*.html` (and re-`cp`ing to `internal/server/static/`).

| Role | Hex | CLI ANSI |
|---|---|---|
| Brand aqua | `#5ac8e2` | `\033[38;2;90;200;226m` |
| streaming / ok | `#22c55e` | `\033[38;2;34;197;94m` |
| awaiting / warn | `#eab308` | `\033[38;2;234;179;8m` |
| success | `#166534` | `\033[38;2;22;101;52m` |
| error / fail | `#b91c1c` | `\033[38;2;185;28;28m` |
| killed / dim | `#555` | dim |

## Conventions

- Errors: `fmt.Errorf("doing X: %w", err)`
- Testing: table-driven, stdlib `testing`
- Exports: default unexported. Short receivers
- No CGo
- Commit: imperative mood, area prefix. `server: add session TTL`

## Dependencies

| Package | Why | Where |
|---|---|---|
| `coder/websocket` | WS client (CLI) | `cmd/spout/cmd/root.go` |
| `fasthttp/websocket` | WS server (Fiber) | `internal/server/server.go` |
| `gofiber/fiber/v3` | HTTP server | `internal/server/server.go` |
| `spf13/cobra` | CLI framework | `cmd/spout/cmd/*.go` |
| `valyala/fasthttp` | HTTP engine | `internal/server/server.go` |
| `gopkg.in/yaml.v3` | Config parsing | `internal/config/config.go` |
| `aws-sdk-go-v2` | R2 multipart upload (configured for R2 endpoint) | `internal/store/r2.go` [planned] |
| `modernc.org/sqlite` | SQLite metadata (pure Go, no CGo) | `internal/store/sqlite.go` [planned] |
| `lib/pq` or `jackc/pgx` | Postgres metadata (hosted) | `internal/store/postgres.go` [planned] |

## When the architecture and code disagree

Code may not yet reflect the spec. The plan in
[ARCHITECTURE.md](ARCHITECTURE.md) is authoritative for design intent;
[DEVELOPMENT.md](DEVELOPMENT.md) tracks what's actually shipped.

If you're implementing, follow ARCHITECTURE.md. If you're reading code,
expect placeholder behavior in places where the spec is `[planned]` —
don't assume the code is wrong.

## Planned (high-signal items for new agents)

Items the spec calls for but code doesn't yet have. Land them in the
listed locations.

### Planned files

| Path | What |
|---|---|
| `cmd/spout/cmd/tail.go` | `spout tail` (live follow) |
| `internal/store/sqlite.go` | SQLiteStore (modernc.org/sqlite, no CGo) |
| `internal/store/postgres.go` | PostgresStore (hosted) |
| `internal/store/r2.go` | R2 multipart upload helper |
| `internal/server/ratelimit.go` | Per-IP / per-stream rate limit |
| `internal/server/ttl.go` | Session TTL sweeper |
| `internal/server/tcp.go` | Tier 0 raw TCP listener on `:1337` |
| `internal/crypto/cipher.go` | Tier 2 AES-GCM helpers (CLI side) |
| `internal/crypto/keygen.go` | Random key + nonce generation |
| `internal/notify/engine.go` | Alert routing → sinks (rule eval lives in `internal/observe`) |
| `internal/notify/sinks.go` | Slack / Discord / Email / Webhook implementations |
| `internal/notify/push.go` | VAPID push setup |
| `clients/python/` | Pure Python WebSocket SDK (`spoutsh` on PyPI). ~400-600 LOC. Deps: `websockets`, `cryptography`, `pyyaml`. Cell magic + iframe display when IPython/Colab detected. See ARCHITECTURE.md §14.1 |
| `clients/python/colab-template.ipynb` | One-click "Open in Colab" notebook with cell-magic + ML training boilerplate |
| `clients/node/` | Thin Node SDK |
| `deploy/Dockerfile` | Self-host Docker image |

### Planned commands

| Command | Source file |
|---|---|
| `spout tail <run> [stream]` | `cmd/spout/cmd/tail.go` |
| `spout config edit / get / set` | `cmd/spout/cmd/config_cmd.go` (extend) |

### Planned flags

| Flag | Where | Purpose |
|---|---|---|
| `--offline` | persistent | Local-only mode (no remote server) |
| `--no-archive` | persistent | Skip local mirror |
| `--no-encrypt` | persistent | Force plaintext |
| `--encrypt` | persistent | Force E2E |
| `--json` | `ls`, `stats`, `config`, `doctor` | Machine-parseable output |
| `--public` | `spout server` | Hosted mode |
| `--new` | `spout share` | Upload as fresh server record |
| `--from <bytes>` | `spout logs` | Start replay from offset |
| `-f, --follow` | `spout logs` | Live follow |
| `--history` | `spout ls` | Include local-archive runs not on server |

### Recently locked decisions

- **CLI flag scheme** (locked 2026-05-01):
  - `attach -a/--all` → `-t/--tiled` (resolves the dual `-a` meaning across attach vs kill/delete)
  - `delete -r/--errors` short flag dropped (use `--errors`); `-a` now consistently means "operate on all" for destructive ops
  - `-n/--name` consistently names the new run/stream being created
  - `-y/--yes` skips binary yes/no confirms; on multi-choice prompts (chooser), it errors cleanly rather than auto-picking
- **`spout add-stream` folded into `spout run --into`** (2026-05-01): one less top-level command. `spout run --into <run> cmd...` adds a stream to an existing multi-stream run. Label from `-n` or derived from cmd basename. Single-stream runs can't be promoted — start over with a `streams:` yaml.
- **Address-form parity** (2026-05-01): every target-taking command (`attach`, `kill`, `open`, `share`, `rename`, `logs`, `delete`) accepts the same four forms via shared resolvers (`resolveTargets` server-side, `resolveLocalTargets` for local store): `<run>` / `<stream>` / `<run> <stream>` / `<run>/<stream>`.
- **Job-aware disambiguation** (2026-05-01): when the chooser fires or display is rendered, runs in a `job:` show as `[job-name] run/stream`. Plain runs (no job) show `run/stream`.
- **Watch dependency notice** (2026-05-01): `kill` scans cwd's `spout.yaml` for watch rules referencing the targeted stream label and prints `watch rule X depends on this`. Informational; doesn't block. Becomes load-bearing when watch executes.
- **`spout exec` dropped** (2026-04-30): original justification (Windows portability + stdout/stderr split) didn't hold up; `cmd 2>&1 \| spout` and multi-stream yaml cover the cases. Surface stays smaller.
- **Storage backends** (2026-04-30): three behind one `Store` interface. Default `SPOUT_STORE=file` (FileStore, zero setup). Opt up: `SPOUT_STORE=sqlite` or `SPOUT_STORE=postgres` (+ S3/R2 blob env vars). All pass the same `runStoreContract` suite. Streaming latency identical.
- **Build phases** (2026-04-30): Phase 1 (server core) ✅ done. Phase 1.5 (SQLite + Postgres+R2) is next. Phase 2 (CLI catch-up) is after 1.5. Phase 3 (Tiers 0/1/2) is later.
- **Server storage path** (2026-04-30): server defaults to `~/.config/spout/server-storage/` (Phase 1.5 will add `DefaultServerStorageDir()` in `internal/config/defaults.go`). CLI keeps `~/.config/spout/storage/`.
- **Naming model**: `job > run > stream(s)` (replaces ambiguous use of "session")
- **`--no-server` → `--offline`** (clearer; doesn't conflict with local viewer)
- **Alias removed**: `spout history` (kept `list`); `--history` flag now unambiguous
- **Python SDK pip name**: `spoutsh` (the `spout` PyPI name is taken by an abandoned package). PEP 541 reclamation pursued in parallel.
- **Python SDK implementation**: pure Python WebSocket client (~400-600 LOC; `websockets` + `cryptography` + `pyyaml`). No binary download.
- **Colab template**: `spout.sh/colab-template.ipynb` + "Open in Colab" button on the landing page.

For the comprehensive deferred list, see
[ARCHITECTURE.md §14](ARCHITECTURE.md#14-deferred-items).
