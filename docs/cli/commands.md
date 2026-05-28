# CLI Commands

Every `spout` subcommand with flags and examples.

For design rationale, see [ARCHITECTURE.md §8](../../ARCHITECTURE.md#8-cli-surface).
For the `spout.yaml` schema, see [config.md](config.md).
For planned commands and flags, see [future.md](future.md).

## Quick reference

| Group | Commands |
|---|---|
| Streaming | `spout` (pipe), `spout run`, `spout server` |
| Runs | `ls`, `attach`, `kill`, `rename`, `delete`/`rm`/`clean`, `share`, `open`, `logs` |
| Setup | `init`, `login`, `config`, `doctor` |
| Meta | `stats`, `completion` |

## Addressing runs and streams

Every command that takes a target accepts the same forms:

```
<run>                     # by run name (prefix match ok)
<stream>                  # by stream label (prompts if ambiguous)
<run> <stream>            # exact stream, two-arg form
<run>/<stream>            # exact stream, slash form
```

Commands that operate on a single target: `attach`, `kill`, `open`,
`share`, `rename`, `logs`. Commands that take multiple targets:
`delete`/`rm` (each arg resolves independently).

## Persistent flags (any command)

| Short | Long | Default | What |
|---|---|---|---|
| `-s` | `--server` | `default_server` from config | Profile name or raw `host:port` |
| `-l` | `--local` | false | Shorthand for `--server localhost:3000` |
| `-n` | `--name` | random `word-xxxx` | Name for the new run / stream |
| | `--observe` | from config | Force observability on for this run |
| | `--no-observe` | from config | Force observability off (wins conflicts) |
| | `--otel` | `observe.otel` | Also export observe events as OTLP/HTTP spans |

Server resolution: CLI flag → config chain → built-in default (`spout.sh`).
Observe resolution: `--no-observe` → `--observe` → `observe.enabled` in config.

## Tier mapping

| Invocation | Tier |
|---|---|
| `cmd \| nc spout.sh 1337` | 0 (zero-install, plaintext) |
| `cmd \| spout` against `spout.sh` | 2 (E2E by default) `[planned]` |
| `cmd \| spout` against self-hosted | 1 (plaintext, trusted server) |

## Streaming

### `spout` (pipe mode)

Reads stdin, sends to server, passes through to stdout.

```bash
command | spout
echo "hi" | spout -n my-test
```

Aborts cleanly if upstream produces no output. Auto-copies URL to clipboard.

### `spout` (bare, no `streams:` config)

Status display. Banner, configured server, active/recent runs.

### `spout run [flags] [command [args...]]`

Detached tmux run. Returns immediately.

```bash
spout run                                  # interactive shell (or blueprint if streams: defined)
spout run python train.py
spout run -n training python train.py
spout run --dir /tmp python train.py
spout run -- mycommand --my-flag           # -- escapes spout flags

# Add a stream to an existing multi-stream run
spout run --into exp-1 tensorboard --logdir runs/
spout run --into exp-1 -n gpu watch -n 2 nvidia-smi
```

**Modes**:
- no args + `streams:` blueprint in cwd's yaml → multi-stream run
- no args + no blueprint → interactive shell
- args → detached single command
- `--into <run> args` → add a new stream to an existing multi-stream run

| Flag | What |
|---|---|
| `--into <run>` | Add this command as a new stream of an existing run |
| `--dir <path>` | Working directory for the run / stream |
| `-y, --yes` | Skip prompts (replace silently on name collision) |

**Requires** `tmux`.

### `spout server [-p port]`

Start the local server. The dashboard lives at `/admin` (reachable from the
same box; set `SPOUT_ADMIN_PASSWORD` for remote login).

```bash
spout server                                       # localhost:3000, open on this box
spout server -p 8080
SPOUT_ADMIN_PASSWORD=$(openssl rand -hex 16) spout server   # + remote admin login
```

### `spout server token create|list|revoke`

Manage ingest tokens — the credential a remote sender presents to push runs.
Same-box senders need none. A running server picks up changes live.

```bash
spout server token create alice     # mint a token (shown once)
spout server token list
spout server token revoke alice
```

See [../server/config.md → Access model](../server/config.md#access-model).

Full env-var reference in [server/config.md](../server/config.md).

## Run management

All commands take a run name (or are interactive when called bare). Multi-stream runs use `<run>/<label>` slash form or two args.

### `spout ls` / `list`

```bash
spout ls
spout ls -s acme
```

Output: tabular, status dot, name, mode, command, started_at.

### `spout stats`

Aggregate metrics across all runs.

### `spout attach <run> [stream]`

Reattach to a detached tmux run.

```bash
spout attach training
spout attach exp-2 training       # specific stream in multi-stream run
spout attach exp-2 --tiled        # multi-pane tiled view, skip per-stream picker
```

| Flag | What |
|---|---|
| `--tiled` | Skip the per-stream sub-picker; attach with all panes visible |

### `spout kill <run> [stream]`

Stop a running run.

```bash
spout kill                        # interactive picker
spout kill training
spout kill exp-2 training         # one stream within a multi-stream run
spout kill --all                  # all active runs (typed-confirm required)
spout kill ember -y               # skip confirmation
```

| Flag | What |
|---|---|
| `-y, --yes` | Skip confirmation |
| `-a, --all` | Kill every active run |

Already-ended runs print "is already ended" and exit 0.

### `spout rename <old> [new]`

```bash
spout rename old-name new-name
spout rename old-name             # prompts for new
spout rename                      # picker, then prompt
```

Multi-stream runs can't be renamed as a unit (rename individual streams).

### `spout delete [name...]` / `rm` / `clean`

Same command, three names.

```bash
# Direct
spout rm wolf-a3f2
spout rm exp-2 exp-3              # multiple
spout rm exp-2/training           # one stream

# Bulk filters
spout rm --all
spout rm --ended
spout rm --errors
spout rm --older 7d               # m/h/d/w
spout rm --keep 10

# Interactive
spout rm                          # menu with bucket counts
spout clean                       # alias
```

| Flag | What |
|---|---|
| `-a, --all` | Delete every run |
| `-e, --ended` | Every ended run (keeps active) |
| `--errors` | Only errored runs |
| `--older <duration>` | Older than N (`30m`/`24h`/`7d`/`2w`) |
| `--keep <N>` | Keep newest N, delete rest |
| `-y, --yes` | Skip confirmation |

If a run is still active, prompts to kill+delete.

### `spout share <run> [stream]`

Print + copy run URL to clipboard. Shows local path and streaming
status. Falls back to a server lookup when there's no local copy.

```bash
spout share wolf-a3f2
spout share exp-2/training        # one stream
spout share wolf-a3f2 -s acme     # against a specific profile
```

### `spout open <run> [stream]`

Open the run in your default browser. Uses `xdg-open` (Linux), `open` (macOS).

### `spout logs <run> [stream]`

Replay run output to stdout. Reads only the local archive — never hits
the server.

```bash
spout logs wolf-a3f2
spout logs exp-2/training         # one stream
spout logs wolf-a3f2 | grep ERROR
```

### `spout observe <run> [stream]`

Print the observer's assessment of a run from the local archive: latest
status, metrics, triggered detectors, and the timeline. Opt-in — enable
`observe` in `spout.yaml` or run with `--observe`. See [observe.md](observe.md).

```bash
spout observe wolf-a3f2
spout observe exp-2/training      # one stream
```

## Setup

### `spout init`

Write annotated `spout.yaml` template into cwd. Doesn't overwrite existing.

### `spout login [profile]`

Interactive profile config. Prompts for URL and (optional) token env var name.

```bash
spout login                       # configures default_server
spout login work                  # creates/updates "work" profile
```

Writes to `~/.config/spout/spout.yaml` (and `.env` if token given).

### `spout config`

Read-only inspection of the resolved config chain.

Output: sources (every `spout.yaml`/`.env` that contributed), resolved fields, streams section, profiles section, invalid block in red if validation fails.

### `spout doctor`

Health checks: version, tools (tmux/clipboard/browser), config + schema validation, server probe (`/api/health`), token (if profile uses auth), storage path/disk, per-stream `dir:` + command on `$PATH`, observe endpoint (when `observe.model.type: local`).

## Hidden / internal

### `_stream`

Invoked by tmux pipe-pane. Not for direct human use.

| Flag | What |
|---|---|
| `--session <name>` | Run name to register on the server |
| `--cmd <string>` | Command being run (for metadata) |

## Output

```
spout: started run wolf-a3f2     # ok (green)
spout: killed wolf-a3f2          # ok
spout: not found wolf-a3f2       # warn / fail
spout: server unreachable        # fail (red)
```

Banner shown only on bare `spout` and `spout server`. Status palette in [ARCHITECTURE.md §13](../../ARCHITECTURE.md#13-status-taxonomy). `NO_COLOR=1` disables colors.

## Tab completion

```bash
spout completion bash > /etc/bash_completion.d/spout
spout completion zsh > "${fpath[1]}/_spout"
spout completion fish > ~/.config/fish/completions/spout.fish
```

Completes: subcommands, run/stream names (queries `/api/runs`).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Generic error |
| 2 | Misuse (bad flags / args) |
| 130 | User cancelled (Ctrl-c) |

For `spout run`, the **child command's** exit code shows in the dashboard + `spout ls`, but the spout CLI returns 0 once the run is recorded.

## Common patterns

```bash
# CI: stream to your team's server, no local archive
SPOUT_TOKEN_CI=$CI_SECRET pytest -v | spout -s ci

# Multi-stream training (with spout.yaml in cwd)
spout run                                 # launches blueprint
spout attach exp-1 training              # focus one stream
spout kill exp-1                         # stop the whole run

# Self-hosted with auth (~/.config/spout/spout.yaml)
servers:
  acme:
    url: spout.acme.com:443
    token: SPOUT_TOKEN_ACME

# Then anywhere with the env var set:
python train.py | spout -s acme
```

## See also

- [config.md](config.md) — `spout.yaml` schema and templating
- [future.md](future.md) — planned commands and flags
- [../faq.md](../faq.md) — troubleshooting
