# CLI

The CLI does three things:

1. Reads terminal output (pipe stdin or tmux pipe-pane)
2. Optionally encrypts it (Tier 2) `[planned]`
3. Sends it to a server via WebSocket

It doesn't care what server it talks to.

## Modes

| Invocation | What happens | Server needed |
|---|---|---|
| `command \| spout` | Foreground. Reads stdin, sends to server, passes through to stdout. | Yes |
| `spout run cmd` | Detached tmux session. Returns immediately. Streams via pipe-pane. | Yes |
| `spout run` (no args) | Interactive tmux shell. Attaches immediately; detach with Ctrl+b d. | Yes |
| `spout` (bare, with `streams:`) | Launches each stream in its own tmux pane under one session. | Yes |
| `spout` (bare, no `streams:`) | Status display (banner, server, active / recent). | No (offline-ok) |
| `spout server` | Starts the server locally. | - |
| `spout attach/kill/ls/...` | Session management (tmux + server). | Varies |

## Short flags (persistent, any command)

| Short | Long | What |
|---|---|---|
| `-s` | `--server` | Server address or profile name (from `servers:` map) |
| `-l` | `--local` | Shorthand for `--server localhost:3000` |
| `-n` | `--name` | Session name |
| `-p` | `--port` | Listen port (`spout server` only) |

Priority for resolving the server:
```
CLI flag (-s/-l) > config chain > defaults
```

See [config.md](config.md) for the full config chain.

## Encryption (CLI-side decision) `[planned]`

The CLI decides whether to encrypt. The server doesn't know or care.

| Flag | Behavior |
|---|---|
| (default) | E2E on against `spout.sh`; plaintext on `--local` / custom servers. |
| `--no-encrypt` | Force plaintext. Privacy warning shown. |
| `--encrypt` | Force E2E on any server. |

See [tiers.md](tiers.md) for the crypto details.

## Commands

| Command | Group | Summary |
|---|---|---|
| `spout run [cmd...]` | streaming | Detached tmux run; no args opens an interactive shell |
| `command \| spout` | streaming | Pipe mode (stdin → dashboard + stdout) |
| `spout attach NAME` | sessions | Reattach to a tmux run |
| `spout kill NAME` | sessions | Stop a tmux run (with confirmation if active) |
| `spout ls` | sessions | Browse all runs (pager for long lists) |
| `spout stats` | sessions | Aggregate stats across all runs |
| `spout delete NAME...` / `rm` | sessions | Delete runs |
| `spout clean` | sessions | Interactive bulk cleanup |
| `spout rename OLD NEW` | sessions | Rename a run |
| `spout open NAME` | sessions | Open a run in the browser |
| `spout logs NAME` | sessions | Print a run's output to stdout |
| `spout share NAME` | sessions | Print + copy a run's URL |
| `spout server` | server | Start the local server |
| `spout config` | setup | Show resolved config chain |
| `spout login [profile]` | setup | Interactive profile setup |
| `spout init` | setup | Create a `spout.yaml` in cwd |
| `spout doctor` | setup | Environment / connectivity checks |

## Session name format

Auto-generated names are `word-xxxx` (e.g., `wolf-a3f2`). The short form
`word` is used in most output lines; the full form disambiguates on collision.

`-n NAME` lets the user choose; if it collides, `spout` prompts to replace
or rename. `streams:` launches use a `run_name` template (supporting `{n}`,
`{input}`, `{t:FORMAT}`, etc.) — see [config.md](config.md) for the token
list.
