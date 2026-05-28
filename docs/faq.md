# FAQ + Troubleshooting

Common gotchas and "why doesn't this work" answers.

## Streaming

### tqdm progress bars don't show in the dashboard

`tqdm` writes to stderr by default; pipe mode only sees stdin (which
is the upstream's stdout). Two fixes:

```bash
# Most cases: redirect stderr to stdout
python train.py 2>&1 | spout

# When you need actual byte-level separation: declare two streams
# in spout.yaml, one for stdout, one for stderr.
```

### Output appears all at once at the end, not live

Upstream is buffering. Force unbuffered:

```bash
PYTHONUNBUFFERED=1 python train.py | spout
# or
stdbuf -oL python train.py | spout
# or
python -u train.py | spout
```

### Can't connect to spout.sh — WebSocket fails

Some corporate proxies block the `Upgrade: websocket` header. Workarounds:
- Try a different network (mobile hotspot)
- Tier 0 fallback: `cmd | curl -T - https://spout.sh/ingest` (HTTP chunked POST goes through proxies)
- Self-host on a VPS

`spout doctor` will gain a `websocket` check row that does a real WS handshake.

### I want everything local, no server

```bash
spout server                    # start your own
python train.py | spout -l      # -l = localhost:3000
```

Or set `default_server: localhost:3000` in `~/.config/spout/spout.yaml`.

`--offline` flag is planned for "don't talk to any server" mode.

## Encryption (Tier 2)

### Shared URL but viewer says "encrypted"

The URL needs the full `#k=...` fragment. Some chat apps strip URL
fragments; some browsers' "copy URL" strips them. Make sure the URL
you shared ends with `#k=` followed by a long random string.

Spout copies the **full** URL (with fragment) to clipboard at run
start — that's the one to share.

### "Decryption failed — wrong key"

Three possible causes:
1. URL was modified in transit (someone re-typed it, an editor "helped")
2. Bytes were tampered with on the server (AES-GCM auth tag catches this)
3. Run is from a different key (URLs got mixed up)

Try the original URL fresh-copied from the source.

### Lost the URL — can I recover?

If you streamed it from your own machine, your local archive at
`~/.config/spout/storage/<run-name>/` has the plaintext. Use
`spout open <name>` (planned: opens local-archive view) or
`spout logs <name>` to dump.

If you streamed from a different machine without the URL, the data is
gone. E2E means no recovery — that's by design.

## Self-hosting

### HTTPS for `spout server`

Use Caddy — auto-issues from Let's Encrypt:

```caddy
spout.acme.com {
    reverse_proxy localhost:3000
}
```

### Tokens not working — `spout doctor` shows "auth required"

Check the env var name matches:

```yaml
# spout.yaml
servers:
  acme:
    url: spout.acme.com:443
    token: SPOUT_TOKEN_ACME    # this is the env var NAME
```
```ini
# ~/.config/spout/.env
SPOUT_TOKEN_ACME=actual-secret-value
```

Common mistake: putting the actual token in `spout.yaml` instead of
the env var name.

### Multiple users on one self-hosted box

Mint a named ingest token per teammate; the running server picks up new
tokens live (the store reloads on file change):

```bash
spout server token create alice
spout server token list
spout server token revoke alice
```

Tokens can also be managed from the dashboard at `/admin`. Each teammate
puts their token in `~/.config/spout/.env` and references it by env-var
name in `servers[<profile>].token` (see the previous FAQ entry).

### Accessing `/admin` from another machine

By default the dashboard is open from the same box (loopback) and
**denied** from anywhere else. Enable remote login by setting a password:

```bash
SPOUT_ADMIN_PASSWORD=$(openssl rand -hex 16) spout server
# visit http://your-host:3000/admin → redirected to /admin/login
# enter the password → cookie set → dashboard
```

Without `SPOUT_ADMIN_PASSWORD`, remote `/admin` requests redirect to a
disabled login page. (Ingest tokens still let the CLI push from anywhere —
they're a separate credential; tokens send, admin views.)

### Programs like `claude` / `k9s` render with garbled lines

The tmux pane (where the program actually runs) and the browser xterm
(where you view it) **must agree on width**. Spout pins both to 120 cols
× 30 rows so they match. If you're upgrading from a build older than
2026-05-27, reinstall — the old binaries left tmux at 200 cols while
xterm auto-fit narrower:

```bash
go install ./cmd/spout
```

### "Revoke token" button doesn't fire

If you're running a `spout` from before 2026-05-27, the `DELETE
/api/tokens/:label` route didn't URL-decode the path (Fiber v3 doesn't
auto-decode), so labels with spaces 404'd. Reinstall and the dashboard
will use the new `POST /api/tokens/revoke` endpoint (label in body —
bulletproof for any characters).

### Switching to SQLite (`[planned]`)

```bash
SPOUT_STORE=sqlite SPOUT_DB_PATH=/opt/spout/spout.db spout server
```

Useful when `spout ls` slows down at 10K+ runs. Bytes still files; only
metadata moves to SQLite.

### Windows support

Windows isn't a v1 priority. Pipe mode and read-only commands work;
tmux-using commands don't.

| Mode | Windows? |
|---|---|
| `cmd \| spout` (pipe) | ✅ Yes |
| `spout ls`, `open`, `logs`, etc. | ✅ Yes |
| `spout run cmd` | ❌ Needs tmux |
| `spout add-stream` | ❌ Needs tmux |
| `streams:` config | ❌ Needs tmux |

WSL2 gets you the full feature set.

## Run management

### `spout ls` is slow

If you have 10K+ runs:
- Self-hosted: switch to `SPOUT_STORE=sqlite` (planned)
- CLI archive: `spout delete` runs you don't need; `spout clean` for bulk

### `tmux not found` running `spout run`

```bash
brew install tmux                          # macOS
sudo apt install tmux                       # Debian/Ubuntu
sudo dnf install tmux                       # Fedora/RHEL
sudo pacman -S tmux                          # Arch
```

Or use pipe mode (`cmd | spout`) which doesn't need tmux.

### `spout kill` says "tmux session not found"

Either the run already ended (tmux session was cleaned up) or it's on
a different machine. `spout ls` shows current state.

## Storage

### Where are runs saved locally?

```
~/.config/spout/storage/<run-name>/
  ├── meta.json
  ├── data.raw            # stdout bytes
  ├── data.err            # stderr bytes [planned]
  └── events.jsonl        # exit code, structured events [planned]
```

Tar the directory to back up everything.

### Delete a run

```bash
spout delete <name>                        # both server and local
spout delete --keep-local <name>            # server only [planned]
spout clean                                 # interactive bulk
```

### Recover after deleting local archive

If still within `spout.sh` 7-day TTL:
```bash
curl -H "Authorization: Bearer $TOKEN" https://spout.sh/api/run/<name>/raw > recovered.txt
```

After TTL: gone. Local archive is the canonical copy by design.

## Errors and warnings

### `spout: streaming to spout.sh — but I haven't set this`

That's the first-run UX note. `spout.sh` is the default. To override:

```yaml
# ~/.config/spout/spout.yaml
default_server: localhost:3000
```

The note is suppressed after the first invocation.

### `spout: spout.sh unreachable — streaming local only`

Server is down or your network can't reach it. Run completes locally
(local mirror is canonical). Run `spout share <name>` to upload when
the server is back.

### `spout: invalid: observe rule "x" references unknown stream 'foo'`

Foreign-key validation. Either add a stream with `label: foo` or
remove the reference from `observe.rules[].sources`. See
[observe.md](cli/observe.md).

### Status shows `killed` for a run I expected to be `success`

The run ended without sending an EXIT signal. Causes:
- User pressed Ctrl-c
- Tmux session was killed externally
- Process crashed before flushing

`spout run` and bare `spout` (streams) wire pipe-pane before the
command runs, so fast-failing commands' exit codes are normally
captured. If you see `killed` for a real exit, file an issue.

## Compatibility

### Is this server actually Spout?

```bash
curl https://example.com/api/health
# {"spout": true, "version": "..."}
```

`spout doctor` does this check automatically against your configured
server.

### Older CLI vs newer server (or vice versa)

CLI ↔ server release lockstep from the monorepo. Major version
mismatches refuse to connect (planned). Minor mismatches generally
work but warn.

Refresh: `go install github.com/hdadhich01/spout/cmd/spout@latest`

## Colab / Jupyter

Most Colab use is **inline Python** (`model.fit()`, `tqdm`) — not
shell. The Python SDK (planned) is the natural fit:

```python
!pip install spoutsh                                  # [planned]
import spoutsh

# Cell magic — easiest
%%spoutsh my-training
model.fit(X, y, epochs=100)

# OR context manager
with spoutsh.run(name="my-training"):
    model.fit(X, y, epochs=100)
```

Pip name is `spoutsh` because `spout` on PyPI is taken.

For `!shell` cells, use the CLI or `nc`:

```python
!apt-get install -y netcat-openbsd
!python train.py | nc spout.sh 1337                   # Tier 0
```

**Tab-close survival**: closing the Colab tab does NOT kill the WS
from the runtime to spout.sh. Bytes flow for ~90 min of idle timeout.
View live from your phone.

**Caveat**: Colab filesystem is ephemeral — local archive doesn't
survive the box dying. On Colab, spout.sh is your canonical copy.

For long runs: use Tier 3 self-hosted on a persistent box.

For full design see [ARCHITECTURE.md §14.1](../ARCHITECTURE.md#141-python-sdk--google-colab-locked-design).

## Misc

### Use Spout in CI

```bash
SPOUT_TOKEN_CI=$CI_SECRET pytest -v | spout -s ci
```

Set `history: false` if you don't want a local archive on disposable runners.

### Contribute

See [DEVELOPMENT.md](../DEVELOPMENT.md). Read [ARCHITECTURE.md](../ARCHITECTURE.md)
for design intent and [CLAUDE.md](../CLAUDE.md) for file map and conventions.
