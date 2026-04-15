# Spout - Architecture & Product Overview

## Table of Contents

1. [The CLI](#1-the-cli) - modes, encryption, config, short flags
2. [The Server](#2-the-server)
   - [2a. Local Server](#2a-local-server-current-focus) - what we're building now
   - [2b. Prod Server](#2b-prod-server-spoutsh-future) - `spout.sh`, comes later
3. [Tiers](#3-tiers-recap) - encryption levels, data flow
4. [Config System](#4-config-system) - runs vs sessions, two config files, inheritance, profiles
5. [Auth](#5-auth) - token auth, no-auth mode
6. [Storage](#6-storage) - FileStore, SQLiteStore, Postgres+S3
7. [Dashboard](#7-dashboard) - pages, status model, tier behavior
8. [Pieces in Motion](#8-pieces-in-motion-summary) - full system diagram
9. [Design Notes](#9-design-notes-not-implemented-yet) - naming, retention, history, UX
10. [What to Build Next](#10-what-to-build-next-ordered) - prioritized roadmap

---

## Product

Spout pipes terminal output to a live web dashboard. Two pieces:

- **CLI** (`spout`) - captures output, sends to server
- **Server** (`spout server`) - receives output, stores runs, serves dashboard

Same binaries everywhere. Config determines behavior.

> **Current focus:** Local server (Tier 3) is the active build target. The CLI
> is being designed to also work with Tier 1/2 against `spout.sh` once that's
> deployed, but the prod server itself comes later. See §2a (local) and §2b (prod).

---

## 1. The CLI

The CLI does three things:
1. Reads terminal output (pipe or tmux)
2. Optionally encrypts it (Tier 2)
3. Sends it to a server via WebSocket

It doesn't care what server it talks to.

```bash
command | spout                              # spout.sh (default, E2E on)
command | spout --server company.internal    # self-hosted
command | spout --local                      # localhost:3000
```

### CLI modes

| Mode | What happens | Server needed |
|------|-------------|---------------|
| `command \| spout` | Foreground. Reads stdin, sends to server, passes through to stdout | Yes |
| `spout run command` | Creates detached tmux session. Returns immediately. Output streamed via pipe-pane | Yes |
| `spout server` | Starts the server locally | - |
| `spout attach/kill/ls` | Manages tmux sessions | No (tmux only) |

### Config (two levels, project overrides system)

**System config** (`~/.config/spout/config.yaml`) - global defaults + server profiles:

```yaml
default_server: spout.sh

servers:
  work:
    url: spout.company.internal:3000
    token: secret-token
  home:
    url: myserver.com:3000
```

**Project config** (`spout.yaml` in project dir) - per-project overrides:

```yaml
server: work              # use the "work" profile for this project
name: ml-training         # session name prefix
# companions:
#   - name: gpu
#     command: watch -n 2 nvidia-smi
```

Project config is discovered by walking up from cwd (like `.git`). Deeper
files override parent. Project overrides system on conflicts. CLI flags
override everything.

```
Priority: CLI flags > project config > system config > defaults
```

Usage:
```bash
command | spout                 # uses project server, or system default (spout.sh)
command | spout -s work        # uses "work" profile (url + token from system config)
command | spout -s host:port   # raw address, no profile lookup
command | spout -l             # localhost:3000 (shorthand)
```

### Short flags

| Short | Long | What |
|-------|------|------|
| `-s` | `--server` | Server address or profile name |
| `-l` | `--local` | Use localhost:3000 |
| `-n` | `--name` | Session name |
| `-p` | `--port` | Port (server command) |

### Encryption (CLI-side decision)

The CLI decides whether to encrypt. The server doesn't know or care.

| Flag | Behavior |
|------|----------|
| (default) | E2E on. CLI connects to spout.sh, encrypts with AES-256-GCM. |
| `--no-encrypt` | Plaintext. Server sees everything. Privacy warning shown. |
| `--local` | Connects to localhost:3000. No encryption (trusted). |
| `--server host` | Custom server. No encryption by default (trusted self-hosted). |
| `--server host --encrypt` | Custom server with E2E forced on. |

---

## 2. The Server

One binary. One codebase. Runs everywhere. Behavior is determined by config,
not code. There are two main "shapes" the server takes:

- **Local server** - single user, single machine (dev, personal, team self-hosted)
- **Prod server** - multi-tenant SaaS (`spout.sh`)

The CLI doesn't care which one it's talking to. The server binary doesn't fork.
The dashboard HTML is the same. Only config + storage backend differ.

### What the server always does

```
Accept connections (WebSocket from CLI, optional TCP from nc)
        │
        ▼
Store raw bytes (memory + disk/database)
        │
        ▼
Broadcast to viewers (WebSocket to browsers)
        │
        ▼
Serve dashboard (embedded HTML/JS)
```

The server NEVER decrypts. If it receives ciphertext (Tier 2), it stores and
relays ciphertext. The browser decrypts using the key from the URL fragment.

---

### 2a. Local Server (current focus)

The server you run on your laptop, homelab, or company VPS. Single user or
small team. No accounts. No multi-tenancy.

```bash
spout server                        # default: localhost:3000, no auth
spout server -p 8080                # custom port
SPOUT_TOKEN=secret spout server     # team mode, auth required
```

#### What's included

| Feature | Status | Notes |
|---|---|---|
| WebSocket ingest (`/ingest/:name`) | ✅ Done | |
| WebSocket viewer (`/ws/:name`) | ✅ Done | |
| Dashboard (run list + terminal viewer) | ✅ Done | xterm.js |
| Run metadata API | ✅ Done | `/api/runs`, `/api/run/:name` |
| Health check API | ✅ Done | `/api/health` returns `{"spout": true}` |
| File storage backend | ✅ Done | folder-per-session |
| Run name collision check | ✅ Done | prompts on `-n` collision |
| Status detection (streaming/awaiting/ended) | ✅ Done | |
| Exit code capture (run mode) | ✅ Done | OSC 9999 marker |
| Banner + clean error output | ✅ Done | |
| Token auth (`SPOUT_TOKEN`) | ❌ Not built | |
| `spout server token create/list/revoke` | ❌ Not built | |
| TCP listener for nc (`:1337`) | ❌ Not built | Tier 1a |
| E2E enforcement (`SPOUT_REQUIRE_E2E`) | ❌ Not built | |
| Session TTL / cleanup | ❌ Not built | |
| SQLiteStore backend | ❌ Not built | for >1k sessions |
| Storage interface refactor | ❌ Not built | abstract over backends |

#### Local server modes

| Mode | Command | Auth | nc | Storage | Use case |
|---|---|---|---|---|---|
| **Personal** | `spout server` | None | On | Files | Just me on my machine |
| **Team** | `SPOUT_TOKEN=x spout server` | Token | Off | Files/SQLite | Small team, internal network |
| **Compliance** | `SPOUT_TOKEN=x SPOUT_REQUIRE_E2E=true spout server` | Token | Off | Files/SQLite | Server can't read output even with admin access |

All three are the same binary. The CLI connects to all of them the same way.

---

### 2b. Prod Server (`spout.sh`, future)

The hosted version. Different shape because it's multi-tenant and public.

```bash
spout server --public --port 443      # what spout.sh would run
```

This is **NOT a different binary** - it's the same `spout server` with different
config. But the requirements are very different from local.

#### What prod adds on top of local

| Feature | Why prod needs it | Local doesn't need it |
|---|---|---|
| **No accounts (initial)** | Anyone can stream, URL = access | Same model works |
| **TCP listener** | Tier 1: `cmd \| nc spout.sh 1337` | Local doesn't expose nc |
| **Privacy warnings on Tier 1** | UX safety on plaintext sessions | No nc, no warnings |
| **Auto-E2E for CLI** | CLI defaults to encryption when target is spout.sh | Local trusts itself |
| **Server-assigned names** | Prevents squatting / collisions across users | Local lets you choose |
| **Session TTL (14 days)** | Storage cost control | Local keeps forever |
| **Rate limiting** | Abuse prevention | Single-user, no abuse |
| **PostgreSQL backend** | Multi-server, replicated metadata | SQLite/files fine |
| **Object storage (R2/S3) for blobs** | Cheap, scalable, multi-region | Local disk fine |
| **Multi-server / horizontal scale** | Survives traffic spikes | Single binary fine |
| **Prometheus metrics + observability** | Operations need it | Optional |
| **CDN in front of dashboard** | Global low latency | localhost is fast already |
| **Domain + TLS** | HTTPS required for browser crypto | http://localhost is fine |

#### What prod does NOT have (initially)

- User accounts / login
- Per-user dashboards (no `/dash` page)
- History browsing on the website (URL or nothing)
- Saved tokens / API keys per user
- Team workspaces
- Billing

These are all "v2" features. The MVP of spout.sh is: anonymous, URL-as-auth,
14-day retention. Same as seashells.io but with E2E by default.

#### Prod-only roadmap

| Item | Effort | Notes |
|---|---|---|
| `--public` flag in server | Small | Enables nc + warnings |
| Server-side name generation | Small | Returns name to CLI |
| TCP listener on `:1337` | Small | Tier 1a entry point |
| Privacy banner on Tier 1 dashboard | Small | "This is unencrypted" |
| Session TTL (14d default) | Small | Background goroutine |
| PostgresStore (`SPOUT_STORE=postgres`) | Medium | jackc/pgx |
| S3/R2 blob storage (`SPOUT_S3=...`) | Medium | aws-sdk-go-v2 |
| Rate limiting middleware | Small | Per-IP |
| Deploy automation (Fly.io / Railway) | Small | Dockerfile + config |
| Domain + TLS termination | Small | Proxy handles it |
| (later) Accounts + `/dash` page | Large | Full auth system |

#### Recommended prod stack

```
Compute       → Fly.io or Railway          (free tier covers MVP)
Metadata DB   → Neon (Postgres serverless) (0.5 GB free, ~1-5ms latency)
Stream blobs  → Cloudflare R2              (10 GB free, $0 egress)
Domain        → spout.sh
TLS           → automatic (Fly/Railway/Cloudflare handle it)
```

**Cost at 100k users**: ~$25/month (Neon Pro $19, R2 ~$1.50, compute ~free).
**Cost at MVP (hobby)**: $0/month, all on free tiers.

---

### Server config reference

```
Env vars:
  SPOUT_TOKEN         enables auth, disables nc        (local team mode)
  SPOUT_REQUIRE_E2E   rejects unencrypted sessions     (compliance)
  SPOUT_STORE         file (default) | sqlite | postgres
  SPOUT_DB            postgres connection string       (when SPOUT_STORE=postgres)
  SPOUT_S3            s3://bucket or r2://bucket       (blob storage)
  SPOUT_PORT          listen port                      (default 3000)

Flags:
  --port, -p          listen port
  --public            enables nc + privacy warnings    (prod mode)
```

---

## 3. Tiers (Recap)

Tiers describe the **encryption level**, not different products or servers.

```
┌─────────────────────────────────────────────────────────┐
│                                                          │
│   Tier 1a: nc ──plaintext TCP──→ Server                  │
│                                   │                      │
│   Tier 1b: CLI ──plaintext WS──→ Server  ──WS──→ Browser │
│                                   │         (no decrypt)  │
│   Tier 2:  CLI ──encrypted WS──→ Server  ──WS──→ Browser │
│                                   │         (WebCrypto    │
│                                   │          decrypts)    │
│                                   │                      │
│                              Same server.                │
│                              Same dashboard.             │
│                              Same storage.               │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

| Tier | Transport | Server sees | nc? | CLI needed? | Browser decrypts? |
|------|-----------|-------------|-----|-------------|-------------------|
| 1a | TCP | Plaintext | Yes | No | No |
| 1b | WebSocket | Plaintext | - | Yes | No |
| 2 | WebSocket | Ciphertext | - | Yes | Yes (WebCrypto) |

### Tier 2 encryption details

```
CLI:
  key = random 256-bit AES-GCM key
  for each chunk:
    nonce = random 96-bit
    ciphertext = AES-GCM-Encrypt(key, nonce, chunk)
    send(nonce + ciphertext)
  print URL: spout.sh/r/wolf-a3f2#key=base64(key)

Server:
  receives bytes
  stores bytes (doesn't know or care if encrypted)
  relays bytes to viewers

Browser:
  reads #key= from URL fragment (never sent to server)
  imports key via crypto.subtle.importKey()
  for each message:
    split nonce (first 12 bytes) and ciphertext
    plaintext = crypto.subtle.decrypt(key, nonce, ciphertext)
    term.write(plaintext)
```

---

## 4. Config System

### Data model

```
Session (= directory context, implicit)
├── Run (standalone - from pipe or spout run)
└── Run (group - from bare `spout` with config)
    ├── Sub-run [training]     ← labels are freeform
    ├── Sub-run [gpu]
    └── Sub-run [logs]
```

**Session** = directory context. Implicit. Groups runs on the dashboard.
**Run** = single invocation of spout.
**Sub-runs** = processes started together from config, each with a user-defined label.

### One file format, one schema

Every config file is named `spout.yaml` and has the same fields. There is no
separate "system" vs "project" format - they're the same file in different
locations.

```
CLI flags > env vars > deepest spout.yaml > ... > git-root spout.yaml > ~/.config/spout/spout.yaml > defaults
```

| Field | Type | What it does |
|-------|------|-------------|
| `default_server` | string | Server when nothing else specified |
| `server` | string | Server profile name or `host:port` to use |
| `servers` | map | Named profiles `{name: {url: "..."}}` |
| `name` | string | Session name |
| `runs` | list | Sub-runs for bare `spout` command |

**Override rules:**
- Scalars (`server`, `name`): deepest wins
- Maps (`servers`): merge, deepest wins per key
- Lists (`runs`): deepest replaces entirely
- Tokens: `SPOUT_TOKEN_<PROFILE>` in `.env`, deepest `.env` wins

### Config files

```yaml
# ~/.config/spout/spout.yaml (system - lowest priority, applies everywhere)
default_server: spout.sh
servers:
  work:
    url: spout.company.internal:3000

# ~/.config/spout/.env (system secrets - not in git)
# SPOUT_TOKEN_WORK=alice-secret-token

# ~/project/spout.yaml (project - overrides system, in git)
server: work
name: ml-training
servers:
  staging:
    url: staging.internal:4000
runs:
  - label: training
    command: python train.py --epochs 100
  - label: gpu
    command: watch -n 2 nvidia-smi

# ~/project/.env (project secrets - gitignored)
# SPOUT_TOKEN_STAGING=staging-token

# ~/project/exp1/spout.yaml (folder - overrides project, in git)
runs:
  - label: training
    command: python train.py --lr 0.001
```

The system file used to be named `config.yaml`. It's now `spout.yaml` to match
project files. The CLI auto-migrates the old name on first run.

### Config discovery

Walk UP from cwd to project root. Ceiling is:
1. Git root (`git rev-parse --show-toplevel`) if in a git repo
2. Home directory if not in a git repo

All `spout.yaml` and `.env` files found along the way are collected and
merged (deepest wins). This means a folder config at `project/exp1/`
always inherits from `project/spout.yaml` - even deep in a monorepo.

### `spout config`

Run `spout config` to see exactly what files are loaded and what they resolved
to. It only displays - it never edits or prompts.

```
$ spout config

  loaded
  yaml      ~/project/spout.yaml
  yaml      ~/.config/spout/spout.yaml
  env       ~/.config/spout/.env

  resolved
  server    localhost:3000
  token     alice-se…
```

To create or edit files:
- `spout login` - adds or updates a profile in `~/.config/spout/spout.yaml`
- `spout init` - creates a project `spout.yaml` in the current directory

### How `spout` decides what to do

```
spout (bare)     → load config → has runs? → start sub-runs in tmux
command | spout  → load config (server only) → standalone run
spout run cmd    → load config (server only) → standalone run in tmux
```

---

## 5. Auth

Auth is independent of encryption. It controls WHO can connect, not WHAT
the server sees.

### Token auth (simple)

```
CLI → WebSocket handshake with Authorization header
Server → checks against SPOUT_TOKEN env var
```

When `SPOUT_TOKEN` is set:
- All WebSocket connections (ingest + viewer) must include the token
- nc is disabled (can't set HTTP headers)
- API endpoints require the token
- Dashboard pages are served without auth (they're just static HTML;
  the WebSocket connection carries the auth)

CLI resolves tokens from `.env` (never from yaml):

```bash
# ~/.config/spout/.env
SPOUT_TOKEN_WORK=alice-secret-token

# Or a project .env for project-specific tokens
# ~/project/.env
SPOUT_TOKEN=project-specific-token
```

```bash
spout -s work run python train.py    # token from SPOUT_TOKEN_WORK
SPOUT_TOKEN=secret command | spout   # explicit env var
```

See §4 Config System above for the full token resolution chain.

### No-auth mode

When `SPOUT_TOKEN` is not set, everything is open. This is correct for:
- Localhost development
- spout.sh Tier 1 (nc - anyone can create sessions)
- spout.sh Tier 2 (URL is the auth - knowing the link = access)

### Future auth (not for now)

- OAuth / SSO for enterprise
- Per-session access tokens
- Viewer-only vs admin tokens
- IP allowlisting

---

## 6. Storage

### Design: separate metadata from stream bytes

Raw stream bytes and metadata have different access patterns:

| Data | Access pattern | Best fit |
|---|---|---|
| Stream bytes (`data.raw`) | Append-only, sequential read, never queried | Files / object store |
| Metadata (name, mode, dir, host, exit code, etc.) | Random read/write, filtered queries, sort | KV / SQL |

Every production logging system (Loki, Datadog, Tempo, GitHub Actions, Vercel)
separates these. Spout follows the same pattern.

### Storage interface

```go
type Store interface {
    Create(meta RunMeta) (*Session, error)
    Get(name string) *Session
    List(filter Filter) []*Session
    Rename(old, new string) error
    Delete(name string) error
    Status() string  // backend identifier
}

type Session interface {
    Write(data []byte)        // append to stream
    Subscribe() (chan []byte, []byte, bool)  // history + live
    Close()
    Status() string
}
```

Three implementations behind the same interface:

### Tier 1: FileStore (default)

Layout:
```
~/.config/spout/sessions/
└── wolf-a3f2/
    ├── meta.json   ← metadata (small)
    └── data.raw    ← raw stream (append-only)
```

- **Used by:** CLI local archive, self-hosted single-server, dev
- **Pros:** Zero dependencies, atomic per-session, easy backup/restore, no setup
- **Cons:** File descriptor limits (~1000 concurrent sessions), `List` is O(n) directory scan
- **Status:** Current implementation

### Tier 2: SQLiteStore (self-hosted at scale)

Layout:
```
~/.config/spout/spout.db          ← SQLite (metadata only, WAL mode)
~/.config/spout/sessions/<name>/
    └── data.raw                   ← raw stream (still files)
```

Schema:
```sql
CREATE TABLE sessions (
    name        TEXT PRIMARY KEY,
    mode        TEXT,
    command     TEXT,
    dir         TEXT,
    host        TEXT,
    user        TEXT,
    git_branch  TEXT,
    started_at  INTEGER,
    ended_at    INTEGER,
    bytes_recv  INTEGER,
    lines       INTEGER,
    exit_code   INTEGER,
    has_exit    INTEGER,
    active      INTEGER
);
CREATE INDEX idx_started_at ON sessions(started_at DESC);
CREATE INDEX idx_dir ON sessions(dir);
CREATE INDEX idx_active ON sessions(active);
```

- **Used by:** Self-hosted with 100s-1000s of sessions, fast filtered queries
- **Pros:**
  - Pure Go via `modernc.org/sqlite` - no CGo
  - Fast filtered list (`WHERE dir = ? AND active = 1 ORDER BY started_at DESC`)
  - WAL mode handles concurrent reads + single writer well
  - Single file backup
- **Cons:** Single-server only (file lock)
- **Migration:** `FileStore → SQLiteStore` reads each `meta.json` and inserts into the table

### Tier 3: PostgresStore + S3 (spout.sh prod)

Layout:
```
PostgreSQL              ← metadata (multi-server, replicated)
S3 / R2 / object store  ← raw stream (cheap blob storage)
```

PostgreSQL schema is the same as SQLite plus:
- `user_id` for multi-tenancy
- `tier` (1, 2, 3) for tier-specific behavior
- `expires_at` for TTL cleanup (default 14 days on hosted)

Raw streams written to object storage:
- During the run, server buffers in memory + flushes to S3 in 1MB chunks
- Each session = one S3 object: `runs/<user_id>/<name>/data.raw`
- On viewer connect, server streams from S3
- Cheap (~$0.023/GB/mo on S3, less on R2)

- **Used by:** spout.sh production, multi-server deployments
- **Pros:**
  - Horizontally scalable - multiple server instances share Postgres + S3
  - Stream bytes don't bloat the DB
  - Cheap long-term storage
  - Multi-region with S3 replication
- **Cons:** More moving parts, network latency for S3 reads on viewer connect

### CLI local archive (always on)

Regardless of which server the CLI talks to, **the CLI always saves runs locally** to `~/.config/spout/sessions/` using `FileStore`. This means:

- Even if you stream to spout.sh (Tier 1/2), the run is also saved locally
- Even if spout.sh deletes the run after 14 days, you still have your local copy
- `spout ls` and `spout` (status) merge local + server data
- No data loss on server failure

The local store is independent of the remote store. CLI handles writing to both
in parallel during the stream.

### Configuration

Storage backend is determined by env var:

```bash
# Default: file store
spout server

# SQLite for self-hosted at scale
SPOUT_STORE=sqlite spout server

# Postgres + S3 for prod
SPOUT_STORE=postgres SPOUT_DB=postgres://... SPOUT_S3=s3://bucket spout server
```

CLI doesn't need any config - its local store is always file-based at
`~/.config/spout/sessions/` (configurable via `storage_dir` in config.yaml).

### Trade-offs by deployment

| Deployment | Sessions | Backend | Why |
|---|---|---|---|
| Local CLI archive | ~100 | FileStore | Zero deps, your machine |
| Personal home server | ~1k | FileStore | Simple, fast enough |
| Team self-hosted | ~10k | SQLiteStore | Fast queries, single binary |
| Enterprise self-hosted | ~100k | SQLiteStore | Same |
| spout.sh hosted | millions | Postgres + S3 | Horizontally scalable |

### Migration paths

- FileStore ↔ SQLiteStore: rebuild SQLite from filesystem walk
- FileStore → Postgres: same, plus upload `data.raw` files to S3
- SQLite → Postgres: SQL dump + import + S3 upload

All migrations are one-way scripts the operator runs once. The server binary
itself only ever speaks to one backend at a time, determined by config.



### File storage (default)

```
~/.config/spout/sessions/
├── wolf-a3f2.json     # metadata (name, mode, command, dir, host, user, git_branch, timestamps)
└── wolf-a3f2.raw      # raw terminal bytes (or ciphertext for E2E)
```

Good for: localhost, small self-hosted deployments, single server.

### PostgreSQL (planned, for hosted/scale)

```
sessions table:
  id, name, mode, command, dir, host, user, git_branch,
  started_at, ended_at, bytes_recv, active

session_data table:
  session_id, seq, data (bytea), created_at
```

Good for: spout.sh, multi-server, large deployments.

Switched via env var:
```bash
SPOUT_STORE=postgres://user:pass@host/spout spout server
```

Server code uses a storage interface. File and Postgres implement it.
Same server binary.

---

## 7. Dashboard

One set of HTML/JS files. Embedded in the server binary. Same everywhere.

### Pages

| URL | Page | What it shows |
|-----|------|---------------|
| `/` | Run list | All runs grouped by directory, status dots, mode, time |
| `/r/:name` | Run viewer | xterm.js terminal, status badge, metadata, copy buttons |

### Status model (consistent across dashboard and API)

| Status | Color | Meaning |
|--------|-------|---------|
| streaming | Green | Command actively producing output (>50 bytes/sec) |
| awaiting | Yellow | Session active but idle >1s (waiting for input) |
| ended | Red | Session closed (command finished or killed) |

### Tier-specific dashboard behavior (planned)

| Feature | Tier 1 (plaintext) | Tier 2 (E2E) |
|---------|-------------------|--------------|
| Terminal output | Rendered directly | Decrypted via WebCrypto first |
| Privacy banner | "This session is unencrypted" | None (it's E2E) |
| Server-side search | Works (server has plaintext) | Not possible |
| Watchdog | Server-side | CLI-side (before encryption) |

---

## 8. Pieces in Motion (Summary)

```
┌──────────────────────────────────────────────────────┐
│                      CLI                              │
│                                                       │
│  Captures output (pipe or tmux)                       │
│  Optionally encrypts (Tier 2)                         │
│  Sends to ANY server (--server flag)                  │
│  Collects metadata (host, user, dir, git branch)      │
│  Manages tmux sessions (run/attach/kill/ls)           │
│                                                       │
│  Doesn't care about auth model or storage backend.    │
│  Just sends bytes and a token if configured.          │
└───────────────────────┬──────────────────────────────┘
                        │ WebSocket
                        ▼
┌──────────────────────────────────────────────────────┐
│                    SERVER                             │
│                                                       │
│  Accepts connections (WebSocket + optional TCP)       │
│  Checks auth token (if configured)                    │
│  Stores bytes (files or PostgreSQL)                   │
│  Broadcasts to viewers (WebSocket)                    │
│  Serves dashboard (embedded HTML/JS)                  │
│  Exposes REST API (/api/health, /api/runs,            │
│  /api/run/:name)                                      │
│                                                       │
│  Doesn't care about encryption. Stores whatever       │
│  bytes it receives. Never decrypts.                   │
│                                                       │
│  Config (env vars) determines:                        │
│    - Storage backend                                  │
│    - Auth on/off                                      │
│    - nc listener on/off                               │
│    - E2E required or optional                         │
└───────────────────────┬──────────────────────────────┘
                        │ WebSocket
                        ▼
┌──────────────────────────────────────────────────────┐
│                   DASHBOARD                           │
│                                                       │
│  Run list (status dots, mode, metadata, time)         │
│  Terminal viewer (xterm.js, full ANSI rendering)      │
│  Status badges (streaming / awaiting / ended)         │
│  Copy buttons (command, directory)                    │
│                                                       │
│  If #key= fragment present in URL:                    │
│    → decrypt each chunk via WebCrypto before render   │
│  If no fragment:                                      │
│    → render raw bytes directly (Tier 1)               │
│                                                       │
│  Same HTML/JS everywhere. No server-side rendering    │
│  differences between tiers.                           │
└──────────────────────────────────────────────────────┘
```

---

## 9. Design Notes (Not Implemented Yet)

### .env discovery

`.env` is discovered the same way as `spout.yaml` - walk up from cwd to
git root (or home dir). Deepest `.env` wins on conflicts per variable.
System `.env` (`~/.config/spout/.env`) is lowest priority.

```
~/project/exp1/.env      ← checked first (highest priority)
~/project/.env           ← checked next
~/.config/spout/.env     ← checked last (lowest priority)
```

A `.env` is "associated" with whatever `spout.yaml` is at the same level.
But it doesn't need one - `.env` alone works.

### Token key naming

```bash
SPOUT_TOKEN=default-token           # used when no profile-specific token
SPOUT_TOKEN_WORK=alice-token        # used when server resolves to "work" profile
SPOUT_TOKEN_STAGING=staging-token   # used when server resolves to "staging"
```

Resolution: CLI resolves the profile name first (from config chain), then
checks `SPOUT_TOKEN_<PROFILE>`, falls back to `SPOUT_TOKEN`. Scanned from
all `.env` files found during walk-up (deepest wins).

### History storage

History is saved by default (server stores `.raw` files). Config option
to disable:

```yaml
# spout.yaml or config.yaml
history: false    # don't persist .raw files (in-memory only, lost on restart)
```

Useful for: CI/CD where you don't care about replaying past runs, or
sensitive environments where you don't want terminal output on disk.

Default: `true` at all levels. Project or folder config can override.

### Run naming conventions

For configured runs (from `runs:` in spout.yaml), the run name can use
templates with auto-incrementing counters or timestamps:

```yaml
# spout.yaml
name: ml-training
run_name: "exp-{n}"        # exp-1, exp-2, exp-3, ...

runs:
  - label: training
    command: python train.py
  - label: gpu
    command: watch -n 2 nvidia-smi
```

Running `spout` three times from this directory produces:
- `exp-1` (sub-runs: [training] [gpu])
- `exp-2` (sub-runs: [training] [gpu])
- `exp-3` (sub-runs: [training] [gpu])

Template variables:

| Variable | Expands to | Example |
|----------|-----------|---------|
| `{n}` | Auto-incrementing integer (per session) | `1`, `2`, `3` |
| `{date}` | Date | `2026-04-03` |
| `{time}` | Time | `14-30-05` |
| `{ts}` | Unix timestamp | `1712345678` |
| `{word}` | Random word | `ember` |

The counter for `{n}` is tracked per session name - the server knows
that session "ml-training" has had 3 runs, so the next one is 4.

If `run_name` is not set, uses the default `word-xxxx` format.

Standalone runs (`spout run cmd` or `cmd | spout`) always use `word-xxxx`
regardless of config. The template only applies to configured multi-runs.

### URL structure

```
/                               Dashboard home (all sessions/runs)
/r/<run-name>                   Single run viewer (current)
/s/<session-name>               Session view (all runs in that session)  [planned]
/s/<session>/<run>              Specific run within a session            [planned]
```

For prod (spout.sh) with auth in the future:

```
spout.sh/                       Landing page / login
spout.sh/dash                   Authenticated dashboard (user's sessions)
spout.sh/r/<run>                Public run link (anyone with URL can view)
spout.sh/r/<run>#key=...        E2E encrypted run (need key to view)
```

The `/r/<run>` URL is always the shareable link - no auth required to
view a specific run if you have the URL. This is the Google Docs model.
The dashboard (`/dash`) shows your runs and requires auth.

For self-hosted: no auth by default, everything at `/`. Same as now.
Add token auth → `/` requires token, but `/r/<run>` links still work
without auth (URL = access, like sharing a Google Doc link).

### Auto-incrementing configured runs

When you repeatedly run `spout` from the same configured project:

```
$ cd ~/project/ml
$ spout              → creates "exp-1" with sub-runs [training, gpu]
$ spout              → creates "exp-2" with sub-runs [training, gpu]
$ spout              → creates "exp-3" with sub-runs [training, gpu]
```

Dashboard:
```
ml-training
  ● exp-3  [training] [gpu]   just now
  ○ exp-2  [training] [gpu]   2h ago
  ○ exp-1  [training] [gpu]   yesterday
```

The counter is stored server-side (per session name). The CLI asks the
server "what's the next number for session ml-training?" before creating
the run.

Sub-runs within a single configured run do NOT auto-increment - they're
always `[training]`, `[gpu]`, etc. as defined in config. Only the top-level
run name increments.

### Naming: who generates names, collisions, retention

**Who generates the name:**

| Context | Who | Why |
|---------|-----|-----|
| Tier 3 / self-hosted | CLI generates `word-xxxx` | User controls their own namespace |
| Tier 3 with `-n` flag | User picks, CLI checks collision | Prompt to replace or rename if exists |
| Tier 1/2 (spout.sh) | **Server generates** | Prevents namespace squatting, ensures uniqueness |

For spout.sh: the CLI does NOT send a name. It sends the command/metadata,
the server assigns a name and returns it. The CLI prints the server-assigned
name + URL. Users cannot choose names on the hosted service.

For self-hosted: CLI generates the name locally, then checks with the
server before creating. If collision:
- Auto-generated name → silently retry (up to 10 times)
- User-chosen name (`-n`) → prompt: `[r] replace  [n] rename`

**Retention and recycling (spout.sh):**

| State | Retention | What happens |
|-------|-----------|-------------|
| Active run | Indefinite | Stays alive until stream closes |
| Ended run | 2 weeks | Viewable, replayable via URL |
| After 2 weeks | Deleted | Name becomes available for reuse |
| E2E encrypted (ended) | 2 weeks | Same - server stores ciphertext, deletes after TTL |

Self-hosted: no automatic deletion by default. `session_ttl` config option
planned (e.g., `session_ttl: 30d`).

**Who sees history:**

| Context | Can browse past runs? | Source |
|---------|----------------------|--------|
| spout.sh (Tier 1/2) | **No dashboard history.** You have the URL or you don't. | - |
| spout.sh (Tier 1/2) | **Local history** via `spout ls --history` | CLI reads local `.raw` files |
| Self-hosted (Tier 3) | **Yes - server dashboard shows all.** | Server storage |
| Self-hosted (Tier 3) | **Merged view** - server runs + local-only runs | Server + local combined |

On spout.sh, the dashboard is ephemeral - you access a run by its URL,
not by browsing a list. There's no "my runs" page on spout.sh. If you
lose the URL, you can still find it in your local history (`spout ls --history`
reads `~/.config/spout/sessions/`).

On self-hosted, the dashboard shows everything the server knows about.
The CLI can also merge in runs that were stored locally but the server
no longer has (e.g., after server restart without persistence, or TTL
expiry). This gives a complete picture: server-side active/recent runs
+ local archive of older runs.

**Local storage (all tiers):**

Every run is stored locally on the CLI side regardless of tier. This means
the user always has a local copy of their output even if the server deletes
it after TTL.

```yaml
# ~/.config/spout/config.yaml
storage_dir: ~/.config/spout/sessions    # default, configurable
```

Local storage location is a system config option. Useful for:
- Pointing to a larger disk (`storage_dir: /data/spout/sessions`)
- Shared storage in team environments
- Disabling with `history: false`

### Prod vs self-hosted (dashboard differences)

| Feature | Self-hosted (default) | Prod (spout.sh, future) |
|---------|----------------------|------------------------|
| Dashboard at `/` | All runs, no auth | Landing page / login |
| Authenticated dashboard | Not needed (single user/team) | `/dash` (user's runs only) |
| Run links `/r/<name>` | Always accessible | Always accessible (URL = access) |
| History browsing | On by default | Behind auth (show user's history) |
| E2E runs | Key in fragment | Key in fragment (same) |
| Privacy warnings | None | Tier 1 gets "unencrypted" banner |
| User accounts | No | Eventually (OAuth / API keys) |

Self-hosted is prod minus multi-tenancy. Same binary, same dashboard code.
The server just skips the auth middleware and shows everything at `/`.

---

### UX touches (planned)

**Zero-friction first use:**
- Auto-copy URL to clipboard when a run starts
- QR code in terminal (for long-running commands only, skip for <2s runs)
- `spout server` auto-opens browser on first local run
- `command | spout` works immediately against spout.sh - no setup

**Team onboarding:**
- `spout login <server>` - interactive prompt for URL + token, writes config + .env
- `spout config show` - prints fully resolved config chain (files, values, sources)
- `spout doctor` - checks tmux, clipboard, browser, config, and server (probes
  `/api/health` to distinguish unreachable / auth required / reachable but not
  spout-compatible / spout-compatible)

**Smart CLI:**
- Bare `spout` (no pipe, no config) shows status: active runs, server, recent history
- Tab completion for run names in attach/kill/rename (Cobra completion function)
- `spout share <run>` - prints shareable URL (with `#key=` if E2E)
- `spout share --expires 1h <run>` - expiring links for sensitive output
- `spout config set key value` / `spout config get key` - one-liner config edits

**Dashboard polish:**
- Search/filter runs
- Delete runs from UI
- Keyboard shortcuts (j/k navigate, Enter open, Esc back)
- Click session name to filter
- Mobile share button (copy URL)

---

## 10. What to Build Next (Ordered)

| # | Feature | What changes | Effort |
|---|---------|-------------|--------|
| 1 | **Bare `spout` with sub-runs** | CLI reads config, starts multiple tmux panes | Medium |
| 2 | **Server-side naming (spout.sh)** | Server assigns names, CLI receives them | Small |
| 3 | **Token auth** | Server middleware + CLI token resolution | Small |
| 4 | **E2E encryption** | CLI: AES-GCM encrypt. Dashboard JS: WebCrypto decrypt | Medium |
| 5 | **TCP listener** | Server: goroutine listening on :1337, creates sessions | Small |
| 6 | **Run naming templates** | CLI: `{n}`, `{date}` in run_name, server counter API | Small |
| 7 | **Session view `/s/:name`** | Dashboard: group runs by session, tabbed view | Medium |
| 8 | **History config** | `history: false` option, server respects it | Small |
| 9 | **Session TTL** | Server: goroutine cleans old sessions, 2w default on hosted | Small |
| 10 | **Local run storage** | CLI saves .raw locally regardless of tier | Small |
| 11 | **Storage dir config** | `storage_dir` in system config | Small |
| 12 | **QR code** | CLI: print QR to terminal after session creation | Small |
| 13 | **Storage interface** | Refactor to interface, FileStore as default impl | Small |
| 13a | **SQLiteStore** | modernc.org/sqlite + meta in DB, data.raw on disk | Medium |
| 13b | **PostgresStore + S3** | For prod hosted, multi-server | Large |
| 14 | **PWA + push** | Dashboard: manifest, service worker, VAPID | Medium |
| 15 | **Watchdog** | Regex layer (server-side T1, CLI-side T2) | Large |

