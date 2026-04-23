# CLAUDE.md - Spout

AI agent quick-ref. Architecture is split across focused files under
[`docs/`](docs/) — start at [docs/README.md](docs/README.md). The running
dev log lives at [DEVELOPMENT.md](DEVELOPMENT.md).

When a question is about config fields / merge semantics, the authoritative
source is [docs/config.md](docs/config.md) — defer to it over anything you
see in code if the two disagree.

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

The yaml templates are embedded via `go:embed` — edits to
`internal/config/templates/*.yaml` take effect on the next build.

## File map

### CLI (`cmd/spout/cmd/`)

```
root.go         Pipe mode, bare `spout` dispatch, streamToSession(), resolveServer()
run.go          spout run (tmux, preCreate, fast-fail polling, interactive-shell variant)
streams.go      Bare `spout` with streams: → multi-pane tmux orchestration
server.go       spout server (embedded, port-in-use prompt)
status.go       Bare `spout` status display (banner + active/recent tables)
stream.go       _stream (hidden, invoked by tmux pipe-pane; watchdog auto-kill)

attach.go       spout attach (syscall.Exec into tmux)
kill.go         spout kill (with active-session confirmation)
ls.go           spout ls / list / history (with pager + printStats)
stats.go        spout stats (aggregate metrics across runs)
rename.go       spout rename (auto-locates server)
delete.go       spout delete / rm + spout clean (bulk, interactive)
share.go        spout share (auto-detect server)
open.go         spout open (xdg-open / open)
logs.go         spout logs (replay output to stdout)

doctor.go       spout doctor (11 checks: version, tools, schema, server, token, storage, streams, watch)
init.go         spout init (writes project template)
login.go        spout login (interactive profile setup)
config_cmd.go   spout config (sources / resolved / streams / profiles sections)

meta.go         Collects hostname, user, git branch
color.go        ANSI truecolor helpers, banner, label, section, say()
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
config/defaults.go        All hardcoded constants + DefaultStorageDir with migration

server/server.go          Fiber app, all REST + WebSocket handlers, embedded dashboard
server/static/            Embedded dashboard (sync target of web/static/)

store/store.go            Session, Store, folder-per-session persistence,
                          scanExitMarker, Status() mode-aware

names/names.go            Word-xxxx generator (~1000 words × base36 suffix)
parser/parser.go          ANSI control-sequence cleaning (preserves SGR)
types/types.go            Shared Line type
```

### Entry points

```
cmd/spout/main.go          CLI entry (wires Execute + PrintError)
cmd/server/main.go         Standalone server binary
```

## Key patterns

**Two WebSocket libraries** — `coder/websocket` (CLI, `net/http`) and
`fasthttp/websocket` (server, Fiber). Different HTTP stacks, same protocol.

**FastHTTP buffer reuse** — `c.Params()` and `c.Query()` return slices into
a buffer that fasthttp reuses after WebSocket upgrade. Always copy before
`Upgrade()`:
```go
name := string(append([]byte{}, c.Params("name")...))
```

**Static file sync** — `go:embed` doesn't follow symlinks. Dashboard
source is in `web/static/`; must be `cp`'d to `internal/server/static/`
for embedding. Yaml templates are already colocated with their embed decl
at `internal/config/templates/`.

**Config schema authority** — `internal/config/config.go` defines the
`Config` struct; [docs/config.md](docs/config.md) is the spec. Merge rules
are: scalars override, maps merge, lists replace. `Watch` object is merged
field-by-field; its `Rules` list replaces.

**Config discovery** — one file format (`spout.yaml`), one schema, multiple
locations. Walks up from cwd to git root (or `$HOME`), collects all
`spout.yaml` and `.env` files. Merges deepest-wins. Global file is
`~/.config/spout/spout.yaml` (lowest priority). `config.EnsureSystemConfig()`
is called from `Execute()` so a missing/empty global file gets populated
from the embedded template.

**Tokens: explicit-only** — `servers[<name>].token` names the env var that
holds the auth value. No implicit fallback to `SPOUT_TOKEN_<PROFILE>` or
`SPOUT_TOKEN`. Profiles without `token:` send no Authorization header.
`Config.TokenVarFor(profile)` returns `""` when no profile auth is set —
`spout config` renders that as `token  none`.

**No hardcoded values** — every network default lives in
`internal/config/defaults.go` (`DefaultRemoteHost`, `DefaultLocalHost`,
`DefaultLocalPort`, `DefaultLocalAddr()`, `DefaultStorageDir()`). Never
write `"localhost:3000"` or `"spout.sh"` or `"sessions"` as a literal
anywhere outside that file.

**Run server auto-detection** — commands that take a run name (`open`,
`share`, `logs`, `delete`, `rename`) use `findRunServer(name)` instead of
`resolveServer()`. Tries the resolved server first, then localhost as
fallback, so the user doesn't need to remember `-l` for every command.

**Spout compatibility check** — `probeSpout(addr)` (in `validate.go`) hits
`GET /api/health` and confirms the response is `{"spout": true}`. Used by
`spout doctor`, bare `spout` status, `checkServer`, and `spout config`'s
resolved-server line. Terminology is uniform: `unreachable`, `auth
required`, `not compatible`, `compatible`.

**Streams launching** — `runStreams()` in `streams.go` creates a single
tmux session with one pane per stream via `split-window`, pipe-panes each
to the server as `<run>-<label>`, and tiles the layout. Pre-flight
collision check for *all* projected names before any sessions are created.
`config.ResolveStreamDir(s)` resolves `dir:` relative to the yaml file
that defined the stream (tracked via `Stream.SourceFile`).

**Run-mode exit-marker race** — tmux pane must have pipe-pane wired
*before* the user's command runs, otherwise a fast-failing command's OSC
9999 exit marker is lost and the session reads as `killed` instead of
`error`. Both `spout run` and streams do: (1) `new-session -d` with a
holding shell, (2) `pipe-pane`, (3) `send-keys` the real command.

**Status taxonomy** — single source of truth across CLI and web:
`streaming` / `awaiting` / `success` / `error` / `killed`. `Session.Status()`
in `store.go` is mode-aware (pipe-mode only emits `streaming`). CLI colors
are 24-bit truecolor ANSI matching exact web hex.

**Visual style** — `cmd/spout/cmd/color.go` defines `banner`, `label`,
`section`, plus `info`/`ok`/`warn`/`fail`. Prefix is always brand aqua
`#5ac8e2`. The caller wraps a single verb (`started`, `killed`, `not found`,
etc.) in the severity color. Sections are aqua+bold. Labels are plain
bold. Banner shows on bare `spout` and `spout server` only.

**Custom help** — `cmd/spout/cmd/help.go` overrides cobra's default.
Groups commands by `GroupID` (`streaming`, `sessions`, `server`, `setup`),
shows aliases inline as `name (alias1/alias2)` with the shortest token as
the bold main name.

## Palette

| Role | Hex | CLI ANSI |
|---|---|---|
| Brand aqua | `#5ac8e2` | `\033[38;2;90;200;226m` |
| streaming / ok | `#22c55e` | `\033[38;2;34;197;94m` |
| awaiting / warn | `#eab308` | `\033[38;2;234;179;8m` |
| success | `#166534` | `\033[38;2;22;101;52m` |
| error / fail | `#b91c1c` | `\033[38;2;185;28;28m` |
| killed / dim | `#555` | dim |

Every status color lives in `color.go` as a truecolor constant and mirrors
the equivalent CSS in `web/static/*.html`. Changing a status color means
editing both files.

## Conventions

- Errors: `fmt.Errorf("doing X: %w", err)`.
- Testing: table-driven, stdlib `testing`.
- Exports: default unexported. Short receivers.
- No CGo.
- Commit: imperative mood, area prefix. `server: add session TTL`.

## Dependencies

| Package | Why | Where |
|---|---|---|
| `coder/websocket` | WS client (CLI) | `cmd/spout/cmd/root.go` |
| `fasthttp/websocket` | WS server (Fiber) | `internal/server/server.go` |
| `gofiber/fiber/v3` | HTTP server | `internal/server/server.go` |
| `spf13/cobra` | CLI framework | `cmd/spout/cmd/*.go` |
| `valyala/fasthttp` | HTTP engine | `internal/server/server.go` |
| `gopkg.in/yaml.v3` | Config parsing | `internal/config/config.go` |
