# Product

Spout pipes terminal output to a live web dashboard.

Two binaries, same codebase, same config format:

- **CLI** (`spout`) - captures output (pipe or tmux), sends to a server
- **Server** (`spout server`) - receives output, stores runs, serves dashboard

```bash
command | spout                              # pipe mode, default server
spout run python train.py                    # detached tmux run
spout run                                    # interactive shell, streamed
spout server                                 # localhost:3000
```

Config (merged from walk-up discovery) determines where the CLI connects and
what it does. See [config.md](config.md).

## Current focus

Local / self-hosted (Tier 3). The prod SaaS (`spout.sh`) is planned; the same
binary runs as both, with encryption, naming, retention, and storage chosen
by config.

## Pieces in motion

```
┌──────────────────────────────────────────────────────┐
│                      CLI                              │
│  Captures output (pipe or tmux)                       │
│  Optionally encrypts (Tier 2)                         │
│  Sends to ANY server (--server flag)                  │
│  Collects metadata (host, user, dir, git branch)      │
│  Manages tmux sessions (run/attach/kill/ls)           │
└───────────────────────┬──────────────────────────────┘
                        │ WebSocket
                        ▼
┌──────────────────────────────────────────────────────┐
│                    SERVER                             │
│  Accepts WebSocket + optional TCP                     │
│  Checks auth token (if configured)                    │
│  Stores bytes (files / SQLite / Postgres + S3)        │
│  Broadcasts to viewers                                │
│  Serves dashboard (embedded HTML/JS)                  │
│  Exposes REST API (/api/health, /api/runs, ...)       │
└───────────────────────┬──────────────────────────────┘
                        │ WebSocket
                        ▼
┌──────────────────────────────────────────────────────┐
│                   DASHBOARD                           │
│  Run list (status dots, mode, metadata, time)         │
│  Terminal viewer (xterm.js)                           │
│  Status badges (streaming / awaiting / success /      │
│                 error / killed)                       │
│  Copy buttons (command, directory)                    │
└──────────────────────────────────────────────────────┘
```
