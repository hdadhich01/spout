# Spout

Pipe any terminal command into a live web dashboard, or launch a whole
blueprint of commands in a single tmux session and watch them all at
once. Works locally (Tier 3), over a hosted SaaS (Tier 2, planned), or
via plain `nc` (Tier 1, planned).

```bash
python train.py | spout                  # pipe mode
spout run python train.py --epochs 100   # detached tmux run
spout run                                # interactive tmux shell
spout                                    # bare: launch streams: blueprint
```

The dashboard is embedded in the binary. Run `spout server` and open
`localhost:3000`.

## Status

Tier 3 (local / self-hosted) is feature-complete for single-user use.
Tier 2 (hosted SaaS with E2E encryption) and Tier 1 (`nc`) are the
next major phases. See [DEVELOPMENT.md](DEVELOPMENT.md) for what's
shipped vs. pending and [docs/roadmap.md](docs/roadmap.md) for the
build order.

## Install

```bash
go install github.com/hdadhich01/spout/cmd/spout@latest
```

Requires Go 1.22+ and `tmux` for `spout run`. `xclip` / `pbcopy` / `wl-copy`
enables auto-clipboard.

On first invocation the CLI writes an annotated
`~/.config/spout/spout.yaml` starter into place (nothing is overwritten
if it already has content).

## Quickstart

```bash
spout server                    # start local dashboard on :3000
python train.py | spout -l      # stream a command, -l = localhost
open http://localhost:3000      # live output, xterm.js, status dots
```

## Modes

### Pipe mode — foreground, streams stdin

```bash
command | spout
```

Reads stdin, sends to the server, pipes through to your stdout.
Aborts cleanly (no ghost server session) if the upstream command dies
before producing output.

### Run mode — detached tmux

```bash
spout run python train.py --epochs 100
spout run -n training -- mycommand -n -l --foo   # -- escapes spout flags
```

Creates a detached tmux session. Your shell returns immediately.
Reattach any time:

```bash
spout ls                        # list all runs
spout attach training           # reattach to the tmux pane
spout kill training             # stop it (with confirmation)
spout logs training             # replay output to stdout
```

### Interactive shell mode — `spout run` with no args

```bash
spout run
```

Opens a live tmux shell, streamed to the dashboard. Ctrl+b d to detach.
Everything you type flows through to the web viewer.

### Streams blueprint — bare `spout` with `streams:` config

Drop a `spout.yaml` in a project directory:

```yaml
server: localhost:3000
job: ml-training
run_name: "exp-{n}"

streams:
  - label: training
    command: python train.py --epochs 200
    dir: ./src/models
    env:
      CUDA_VISIBLE_DEVICES: "0"

  - label: gpu
    command: watch -n 2 nvidia-smi

  - label: logs
    command: tail -f output.log
```

Then:

```bash
spout                           # launches all three as tmux panes,
                                # each streamed to the dashboard as a
                                # separate run (exp-1-training, etc.)
```

Every dashboard run carries the `job:` tag so groupings are possible
(server-side grouping coming in the next phase).

## Command reference

| Group | Command | What |
|---|---|---|
| streaming | `spout run [cmd...]` | Detached tmux; no args → interactive shell |
| streaming | `command \| spout` | Pipe stdin to server |
| streaming | `spout` | If `streams:` defined, launch the blueprint; otherwise show status |
| sessions | `spout ls` | All runs (active + ended), paged |
| sessions | `spout stats` | Aggregate metrics |
| sessions | `spout attach NAME` | Reattach to tmux |
| sessions | `spout kill NAME` | Kill a tmux session |
| sessions | `spout open NAME` | Open the dashboard URL in a browser |
| sessions | `spout share NAME` | Print + copy run URL |
| sessions | `spout logs NAME` | Replay recorded output |
| sessions | `spout rename OLD NEW` | |
| sessions | `spout delete NAME...` / `rm` | |
| sessions | `spout clean` | Interactive bulk cleanup |
| server | `spout server` | Start the local server |
| setup | `spout init` | Write project `spout.yaml` from template |
| setup | `spout login [profile]` | Add / edit a server profile |
| setup | `spout config` | Show loaded files + resolved values |
| setup | `spout doctor` | 11 environment / config checks |

## Config

One schema across global (`~/.config/spout/spout.yaml`) and project
(`<repo>/spout.yaml`) files. CLI walks up from cwd collecting
`spout.yaml` and `.env` files, merges deepest-wins.

Key fields:

| Field | Type | Purpose |
|---|---|---|
| `default_server` | scalar | Fallback server if no project config sets one |
| `server` | scalar | This project's server — profile name or `host:port` |
| `servers` | map | Address book of named profiles (with optional `token: ENV_VAR`) |
| `job` | scalar | Dashboard grouping label |
| `run_name` | scalar | Template for sequential run names |
| `storage` | scalar | Local archive path (default `~/.config/spout/storage`) |
| `history` | bool | Persist `.raw` locally |
| `streams` | list | Tmux panes for bare `spout` |
| `watch` | object | LLM watchdog (parsed + validated, execution planned) |

Full spec: [docs/config.md](docs/config.md).

### `run_name` templates

```yaml
run_name: "exp-{n}"                 # exp-1, exp-2, ...
run_name: "{t:YYYY-MM-DD}-{n}"      # 2026-04-16-1
run_name: "{input}-{n}"             # prompts for a label at launch
```

Variables: `{n}`, `{input}`, `{date}`, `{time}`, `{ts}`, `{t:FORMAT}`.
Custom datetime tokens (Moment/Java-style): `YYYY YY MM DD dd HH hh mm ss`.

### Auth

Explicit-only. A profile uses a token iff it declares `token:`, and the
value is the **name** of the env var that holds the actual token:

```yaml
# ~/.config/spout/spout.yaml
servers:
  work:
    url: spout.company.internal:3000
    token: SPOUT_TOKEN_WORK
```

```ini
# ~/.config/spout/.env
SPOUT_TOKEN_WORK=alice-secret-token
```

Profiles without `token:` are no-auth. See [docs/auth.md](docs/auth.md).

## Status taxonomy

| State | Color | Meaning |
|---|---|---|
| streaming | green `#22c55e` | Receiving data right now |
| awaiting | yellow `#eab308` | Run-mode shell idle at a prompt |
| success | dark green `#166534` | Run-mode command exited 0 |
| error | red `#b91c1c` | Run-mode command exited non-zero |
| killed | gray `#555` | Ended before any exit marker (user kill / close terminal) |

Colors are 24-bit truecolor on the CLI and the exact same hex on the web
dashboard — changing one edits both.

## Architecture

Go monorepo, two binaries, shared internal packages:

```
cmd/spout/      CLI
cmd/server/     Standalone server entry point
internal/
  server/       Fiber HTTP + WS + embedded dashboard
  store/        Folder-per-session file persistence
  config/       Schema, walk-up merge, templates
  names/        Word-xxxx generator
  parser/       ANSI cleanup
  types/        Shared types
web/static/     Dashboard source (embedded into server binary)
docs/           Architecture + spec (start at docs/README.md)
```

Two WebSocket libraries (one per HTTP stack) because Fiber and
`net/http` don't share one. See [CLAUDE.md](CLAUDE.md) for the full
key-patterns list.

## Tiers

| Tier | How | Privacy | CLI required? |
|---|---|---|---|
| 1 — `nc` (planned) | `command \| nc spout.sh 1337` | Plaintext | No |
| 2 — Hosted E2E (planned) | `command \| spout` | AES-256-GCM, key in URL fragment | Yes |
| 3 — Self-hosted (shipped) | `spout server` | Full local | Yes |

Same binary everywhere. See [docs/tiers.md](docs/tiers.md).

## Development

```bash
go build ./...              # build both binaries
go install ./cmd/spout/     # install CLI as `spout`
go vet ./...
go test ./...
```

After editing `web/static/*.html` sync to the embed dir:

```bash
cp web/static/*.html internal/server/static/
```

Yaml templates (`internal/config/templates/*.yaml`) are embedded via
`go:embed` — changes take effect on next build.

For the current-state feature list and dev log see
[DEVELOPMENT.md](DEVELOPMENT.md).

## License

TBD
