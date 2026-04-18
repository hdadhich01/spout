# Roadmap

Everything in this doc is `[planned]` unless noted. For the running dev log
(what shipped when, what's in progress) see repo-root
[DEVELOPMENT.md](../DEVELOPMENT.md).

## Build order

| # | Feature | Effort | What changes |
|---|---|---|---|
| 1 | Bare `spout` with `streams:` | M | CLI reads merged config, starts multiple tmux panes |
| 2 | `run_name` templates (`{n}`, `{date}`, ...) | S | CLI expands, server stores counter per job |
| 3 | Watchdog (foreign-key validated rules) | L | LLM polling loops over stream history |
| 4 | Server-side naming (spout.sh) | S | Server assigns name, CLI receives it |
| 5 | Token auth | S | Server middleware + CLI `SPOUT_TOKEN_<PROFILE>` resolution |
| 6 | E2E encryption (Tier 2) | M | CLI AES-GCM encrypt; dashboard WebCrypto decrypt |
| 7 | TCP listener (Tier 1a) | S | Server goroutine on `:1337`, creates sessions from raw TCP |
| 8 | Session view `/s/:name` | M | Dashboard: group runs by `job`, tabbed view |
| 9 | `history: false` respect | S | Skip `.raw` persistence, in-memory only |
| 10 | Session TTL | S | Server goroutine cleans expired sessions (2w default on hosted) |
| 11 | `storage` config | S | Global yaml field (local archive path), wired through CLI |
| 12 | QR code in CLI | S | Print QR for long-running runs (>2s) |
| 13 | Storage interface refactor | S | `Store` + `Session` interfaces, FileStore as default impl |
| 13a | SQLiteStore | M | `modernc.org/sqlite`, metadata in DB, `data.raw` on disk |
| 13b | PostgresStore + S3 | L | For prod hosted, multi-server |
| 14 | PWA + push notifications | M | Dashboard: manifest, service worker, VAPID |

## Design notes (not implemented yet)

### Run naming templates

```yaml
job: ml-training
run_name: "exp-{n}"        # exp-1, exp-2, exp-3, ...
streams:
  - label: training
    command: python train.py
  - label: gpu
    command: watch -n 2 nvidia-smi
```

Running `spout` three times from this directory produces:

- `exp-1` (streams: [training] [gpu])
- `exp-2` (streams: [training] [gpu])
- `exp-3` (streams: [training] [gpu])

Template variables:

| Variable | Expands to | Example |
|---|---|---|
| `{n}` | Auto-incrementing integer (per job) | `1`, `2`, `3` |
| `{input}` | Value typed at an interactive prompt before launch | `lr-sweep` |
| `{date}` | Today (YYYY-MM-DD) | `2026-04-16` |
| `{time}` | Now (HH-mm-ss) | `14-30-05` |
| `{ts}` | Unix timestamp | `1712345678` |
| `{t:FORMAT}` | Custom datetime in Moment/Java-style tokens | `{t:dd/MM HH:mm}` → `16/04 14:30` |

FORMAT tokens for `{t:...}` (case-sensitive):
`YYYY` `YY` `MM` `DD` / `dd` `HH` / `hh` `mm` `ss`. Any other character is
emitted verbatim, so `{t:YYYY-MM-DD HH:mm}` works.

`{n}` counter is tracked per job, file-backed at
`~/.config/spout/counters/<job>.txt`. Standalone runs (`spout run cmd` /
`cmd | spout`) always use `word-xxxx` - the template only applies to bare
`spout` invocations that launch a `streams:` group.

### Naming / collisions / retention

**Who generates names:**

| Context | Who | Why |
|---|---|---|
| Tier 3 / self-hosted | CLI → `word-xxxx` | User owns their namespace |
| Tier 3 with `-n` | User picks, CLI checks | Prompts on collision |
| Tier 1/2 (spout.sh) | **Server** | No squatting, guaranteed unique |

For `spout.sh`, the CLI sends only metadata - the server returns a name.
Users cannot choose names on hosted.

**Retention (hosted):**

| State | TTL | What happens |
|---|---|---|
| Active | Indefinite | Alive while stream is open |
| Ended | 14 days | Viewable via URL |
| After 14d | Deleted | Name can be reused |

Self-hosted has no automatic deletion; `session_ttl: 30d` config option is
on the roadmap.

**Who sees history:**

| Context | Dashboard history? | Source |
|---|---|---|
| spout.sh (Tier 1/2) | No - URL or nothing | - |
| spout.sh (Tier 1/2) | Yes, local | `spout ls --history` (local `.raw`) |
| Self-hosted (Tier 3) | Yes - server dashboard | Server |
| Self-hosted (Tier 3) | Merged view | Server + local `.raw` |

### URL structure

```
/                       Dashboard home (active + ended split)
/r/<run-name>           Single run viewer
/s/<job-name>           Job view (all runs)                     [planned]
/s/<job>/<run>          Specific run within a job               [planned]
```

Prod (spout.sh), with future auth:

```
spout.sh/               Landing / login
spout.sh/dash           Authenticated user dashboard            [planned]
spout.sh/r/<run>        Shareable run link (no auth, URL = access)
spout.sh/r/<run>#key=.. E2E run (needs key from fragment)
```

Google Docs model: `/r/<run>` is always a shareable link; `/dash` requires
auth and shows your own runs.

## UX touches

**Zero-friction first use:**
- Auto-copy URL to clipboard on run start (done)
- QR code in terminal for long runs
- `spout server` auto-opens browser on first local run
- `cmd | spout` works immediately against `spout.sh` with zero setup

**Team onboarding:**
- `spout login [profile]` (done)
- `spout config` full chain (done)
- `spout doctor` env + connectivity checks (done)

**Smart CLI (done):**
- Bare `spout` status view (banner, server, runs, quick help)
- Tab completion for run names (attach/kill/rename)
- `spout share <name>` prints shareable URL + copies to clipboard
- Interactive `spout run` (no args) opens a tmux shell

**Smart CLI (planned):**
- `spout share --expires 1h <name>` (expiring links for sensitive output)
- `spout config set/get` one-liner edits

**Dashboard polish (planned):**
- Search / filter runs
- Delete from UI
- Keyboard shortcuts (j/k navigate, Enter open, Esc back)
- Click job name to filter
- Mobile share button
