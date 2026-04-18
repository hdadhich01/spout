# Server

One binary, one codebase, runs everywhere. Behavior is determined by config,
not code. Two main "shapes":

- **Local server** - single user or small team, self-hosted
- **Prod server** - multi-tenant SaaS (`spout.sh`, future)

The CLI doesn't care which it's talking to. The dashboard HTML is the same.
Only env-var config + storage backend differ.

## What the server always does

```
Accept connections  (WebSocket from CLI, optional TCP from nc)
        │
        ▼
Store raw bytes     (memory + file / SQLite / Postgres + S3)
        │
        ▼
Broadcast to viewers (WebSocket to browsers)
        │
        ▼
Serve dashboard     (embedded HTML/JS via go:embed)
```

The server NEVER decrypts. If it receives ciphertext (Tier 2), it stores and
relays ciphertext. The browser decrypts using the key from the URL fragment.

## Local server (current focus)

```bash
spout server                        # default: localhost:3000, no auth
spout server -p 8080                # custom port
SPOUT_TOKEN=secret spout server     # team mode, auth required   [planned]
```

### Shipped

| Feature | Notes |
|---|---|
| WebSocket ingest (`/ingest/:name`) | |
| WebSocket viewer (`/ws/:name`) | |
| Dashboard (run list + terminal viewer) | xterm.js |
| Run metadata API | `/api/runs`, `/api/run/:name` |
| Health check | `/api/health` → `{"spout": true}` |
| File storage backend | folder-per-session (`meta.json` + `data.raw`) |
| Run name collision check | prompts on `-n` collision |
| Status detection | streaming / awaiting / success / error / killed |
| Exit code capture (run mode) | OSC 9999 marker |

### Not built

| Feature | Bucket |
|---|---|
| Token auth (`SPOUT_TOKEN`) | all |
| `spout server token create/list/revoke` | all |
| TCP listener for nc (`:1337`) | prod (Tier 1a) |
| E2E enforcement (`SPOUT_REQUIRE_E2E`) | compliance |
| Session TTL / cleanup | all |
| SQLiteStore backend | self-hosted at scale |
| Storage interface refactor | all |

### Modes

| Mode | Command | Auth | nc | Storage | Use case |
|---|---|---|---|---|---|
| Personal | `spout server` | None | On | Files | Just me |
| Team | `SPOUT_TOKEN=x spout server` | Token | Off | Files / SQLite | Small team, internal network |
| Compliance | `SPOUT_TOKEN=x SPOUT_REQUIRE_E2E=true spout server` | Token | Off | Files / SQLite | Server can't read output |

All three are the same binary.

## Prod server (`spout.sh`, future)

```bash
spout server --public --port 443      # what spout.sh would run   [planned]
```

Same `spout server` binary, different config. Requirements beyond local:

| Feature | Why prod needs it |
|---|---|
| No accounts (initial) | Anyone can stream; URL = access |
| TCP listener | Tier 1a: `cmd \| nc spout.sh 1337` |
| Privacy warnings on Tier 1 | UX safety on plaintext sessions |
| Auto-E2E for CLI | CLI defaults to encryption on `spout.sh` |
| Server-assigned names | Prevents squatting / cross-user collisions |
| Session TTL (14d) | Storage cost control |
| Rate limiting | Abuse prevention |
| PostgreSQL backend | Multi-server, replicated metadata |
| Object storage (R2/S3) | Cheap, scalable blob storage |
| Multi-server / horizontal scale | Traffic spikes |
| Metrics + observability | Operations |
| CDN + domain + TLS | Browser crypto needs HTTPS |

### Not in prod initially

User accounts, per-user dashboards, history browsing on the website,
saved tokens/API keys, team workspaces, billing. All "v2" features. MVP of
`spout.sh`: anonymous, URL-as-auth, 14-day retention.

### Recommended prod stack

```
Compute       → Fly.io or Railway       (free tier covers MVP)
Metadata DB   → Neon (Postgres serverless) (0.5 GB free)
Stream blobs  → Cloudflare R2           (10 GB free, $0 egress)
Domain        → spout.sh
TLS           → automatic (Fly / Railway / Cloudflare)
```

Cost at 100k users: ~$25/mo. At MVP hobby scale: ~$0.

## Server config reference

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
  --public            enables nc + privacy warnings    (prod mode) [planned]
```
