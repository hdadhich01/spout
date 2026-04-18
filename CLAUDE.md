# CLAUDE.md - Spout

AI agent quick-ref. The old monolithic ARCHITECTURE.md has been split into
focused files under [`docs/`](docs/) - start at
[docs/README.md](docs/README.md). For the running dev log (what shipped
when), see [DEVELOPMENT.md](DEVELOPMENT.md).

When a question is about config fields / merge semantics, the authoritative
source is [docs/config.md](docs/config.md) - defer to it over anything you
see in code if the two disagree.

## Build / Test

```bash
go build ./...            # build everything
go install ./cmd/spout/   # install CLI as `spout`
go vet ./...              # lint
go test ./...             # test
make build                # both binaries to bin/
```

After editing `web/static/`, sync to embed dir:
```bash
cp web/static/*.html internal/server/static/
```

## File Map

```
cmd/spout/main.go             CLI entry point
cmd/spout/cmd/root.go         Pipe mode, streamToSession(), resolveServer()
cmd/spout/cmd/run.go          spout run (tmux + preCreate + exit code marker)
cmd/spout/cmd/server.go       spout server (embedded, port-in-use prompt)
cmd/spout/cmd/attach.go       spout attach (syscall.Exec into tmux)
cmd/spout/cmd/kill.go         spout kill (with confirm for active sessions)
cmd/spout/cmd/ls.go           spout ls / list / history (with pager)
cmd/spout/cmd/stats.go        spout stats (aggregate run statistics)
cmd/spout/cmd/rename.go       spout rename (auto-locates server)
cmd/spout/cmd/delete.go       spout delete / rm + spout clean (bulk)
cmd/spout/cmd/share.go        spout share (auto-detect server)
cmd/spout/cmd/open.go         spout open (xdg-open / open)
cmd/spout/cmd/logs.go         spout logs (replay output to stdout)
cmd/spout/cmd/doctor.go       spout doctor (env checks)
cmd/spout/cmd/init.go         spout init (writes spout.yaml)
cmd/spout/cmd/login.go        spout login (interactive profile setup)
cmd/spout/cmd/config_cmd.go   spout config (show loaded files + resolved values)
cmd/spout/cmd/status.go       Bare `spout` status display + banner
cmd/spout/cmd/stream.go       _stream (hidden, invoked by pipe-pane)
cmd/spout/cmd/meta.go         Collects hostname, user, git branch
cmd/spout/cmd/color.go        ANSI color helpers, banner, label, section
cmd/spout/cmd/clipboard.go    Cross-platform clipboard copy
cmd/spout/cmd/completion.go   Tab completion for tmux sessions
cmd/spout/cmd/help.go         Custom grouped help with aliases inline
cmd/spout/cmd/prompt.go       confirm / confirmYes / confirmTyped / askLine
cmd/spout/cmd/pager.go        withPager (less -FRX --mouse) for ls
cmd/spout/cmd/validate.go     checkServer, findRunServer, resolveSessionName, probeSpout
cmd/server/main.go            Standalone server entry point

internal/server/server.go     Fiber app, all routes and WebSocket handlers
internal/store/store.go       Session struct, Store, Subscribe/Write/Close
                              (folder-per-session: meta.json + data.raw)
internal/config/config.go     Config loading, walk-up discovery, .env, profiles
internal/config/defaults.go   ALL hardcoded constants (DefaultRemoteHost, etc.)
internal/names/names.go       Name generation (word-xxxx, ~1000 words)
internal/parser/parser.go     ANSI cleaning (preserves colors)
internal/types/types.go       Shared Line type

internal/server/static/       Embedded dashboard (go:embed)
web/static/                   Dashboard source (sync to above)
```

## Key Patterns

**Two WebSocket libraries** - `coder/websocket` (CLI, net/http) and
`fasthttp/websocket` (server, Fiber). Different HTTP stacks, same protocol.

**FastHTTP buffer reuse** - `c.Params()` and `c.Query()` return slices into
a buffer that fasthttp reuses after WebSocket upgrade. Always copy before
`Upgrade()`:
```go
name := string(append([]byte{}, c.Params("name")...))
```

**Static file sync** - `go:embed` doesn't follow symlinks. Dashboard source
is in `web/static/`, must be copied to `internal/server/static/` for embedding.

**Config discovery** - one file format (`spout.yaml`), one schema, multiple
locations. Walks up from cwd to git root (or home), collects all `spout.yaml`
and `.env` files. Merges deepest-wins. System file is at
`~/.config/spout/spout.yaml` (lowest priority fallback). See ARCHITECTURE.md §4.

**No hardcoded values** - every default lives in `internal/config/defaults.go`
(`DefaultRemoteHost`, `DefaultLocalHost`, `DefaultLocalPort`,
`DefaultLocalAddr()`). Never write `"localhost:3000"` or `"spout.sh"` as a
literal anywhere outside that file.

**Run server auto-detection** - commands that take a run name (`open`, `share`,
`logs`, `delete`, `rename`) use `findRunServer(name)` instead of `resolveServer()`.
This tries the resolved server first, then localhost as fallback, so the user
doesn't need to remember `-l` for every command.

**Spout-compatibility check** - `probeSpout(addr)` (in `validate.go`) hits
`GET /api/health` and confirms the response is `{"spout": true}`. Used by
`spout doctor`, bare `spout` status, and `checkServer` to distinguish
unreachable vs. reachable-but-not-spout (e.g. a domain that returns a
landing page). Wording is consistent everywhere: `unreachable`, `auth required`,
`reachable; not spout-compatible`, `spout-compatible`.

**Visual style** - `internal/cmd/color.go` defines `banner`, `label`, `section`,
plus `info`/`ok`/`warn`/`fail`. Sections are aqua+bold. Labels are plain bold.
Values are plain. Banner shows on `spout` (bare) and `spout server` only.

**Custom help** - `cmd/spout/cmd/help.go` overrides cobra's default. Groups
commands by `GroupID`, shows aliases inline as `name (alias1/alias2)` with the
shortest as the bold main name.

## Conventions

- Errors: `fmt.Errorf("doing X: %w", err)`
- Testing: table-driven, stdlib `testing`
- Exports: default unexported. Short receivers.
- No CGo.
- Commit: imperative mood, area prefix. `server: add session TTL`

## Dependencies

| Package | Why | Where |
|---------|-----|-------|
| `coder/websocket` | WS client (CLI) | cmd/spout/cmd/root.go |
| `fasthttp/websocket` | WS server (Fiber) | internal/server/server.go |
| `gofiber/fiber/v3` | HTTP server | internal/server/server.go |
| `spf13/cobra` | CLI framework | cmd/spout/cmd/*.go |
| `valyala/fasthttp` | HTTP engine | internal/server/server.go |
| `gopkg.in/yaml.v3` | Config parsing | internal/config/config.go |
