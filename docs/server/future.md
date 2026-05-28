# Server — Planned

Items spec'd but not yet shipped. They move out of this file as they ship.

For design context, see [ARCHITECTURE.md §14](../../ARCHITECTURE.md#14-deferred-items).

## Planned endpoints

| Endpoint | What |
|---|---|
| `POST /api/run/:name/alert` | Notification routing — CLI fires alerts here when an `observe` detector/rule trips. Server fans out to configured sinks. (Events themselves already land via `POST /api/run/:name/events`.) |
| `TCP :1337` | Tier 0 raw byte listener (when `SPOUT_PUBLIC=true`) |
| `POST /ingest/:name` | HTTP chunked alternative to WS — for proxy-blocked clients and `curl -T -` users |
| `GET /j/:job` | Job overview page (all runs in a `job:` group) |
| `GET /api/run/:name/raw?download=1` | Sets `Content-Disposition: attachment` for browser file download |

## Planned query parameters

| Endpoint | Param | What |
|---|---|---|
| `GET /api/runs` | `?status=streaming` | Filter by status |
| `GET /api/runs` | `?job=ml-training` | Filter by job |
| `GET /api/runs` | `?after=<timestamp>` | Pagination cursor |
| `GET /api/run/:name/raw` | `?stream=stdout` | Filter to stdout opcode bytes only |
| `GET /api/run/:name/raw` | `?stream=stderr` | Filter to stderr opcode bytes only |
| `GET /api/run/:name/raw` | `?from=<bytes>` | Start replay from byte offset |

## Planned wire protocol additions

- **TLV framing** (replaces today's raw bytes + OSC 9999 inline marker)
- **Frame-level encryption** for Tier 2 (AES-256-GCM, key in URL fragment)
- **HEARTBEAT enforcement** (server closes idle WS that doesn't ping every 30s)

## Planned TLV opcodes

| Opcode | Name | When |
|---|---|---|
| `0x02` | STDERR | Phase 2 (only used when a yaml stream declares `stderr` capture; `spout exec` was dropped) |
| `0x10–0x1F` | ENC variants | Phase 1 (ships with Tier 2 encryption) |
| `0x20–0xFF` | Reserved | Future (e.g., META mid-run mutation) |

## Planned status codes

| Code | Will mean |
|---|---|
| 401 | Token missing or wrong (auth middleware) |
| 403 | Per-IP / per-run cap exceeded (rate limit middleware) |
| 413 | Per-session size cap hit (caps middleware) |
| 429 | Per-IP rate limit hit (returns `Retry-After`) |

## Planned env vars

| Var | What |
|---|---|
| `SPOUT_STORE` | Backend: `file` (default) / `sqlite` / `postgres` |
| `SPOUT_DB_PATH` | SQLite file path |
| `SPOUT_DB` | Postgres DSN |
| `SPOUT_S3_ENDPOINT` / `SPOUT_S3_BUCKET` / `SPOUT_S3_KEY` / `SPOUT_S3_SECRET` | R2 / S3 blob storage credentials |
| `SPOUT_TTL` | Session TTL (default `0` = unlimited; `7d` for hosted) |
| `SPOUT_PUBLIC` | Hosted-mode flag |
| `SPOUT_VAPID_PUBLIC` / `SPOUT_VAPID_PRIVATE` | PWA push notification keys |
| `SPOUT_REPLAY_CAP` | Initial viewer replay window (default `4MB`) |

## Planned storage backends

| Backend | Status | When |
|---|---|---|
| `file` | ✅ shipping (FileStore) | Today |
| `sqlite` | `[planned]` | Mid-scale self-host (10K+ runs) |
| `postgres` | `[planned]` | Hosted (`spout.sh`) — multi-box, replicated metadata |
| R2 / S3 multipart for blobs | `[planned]` | Hosted only — bytes never live in DB |

## Planned middleware

| Middleware | What |
|---|---|
| Token auth (`internal/server/auth.go`) | Bearer `SPOUT_TOKEN` enforcement (active when `SPOUT_TOKEN` set AND `SPOUT_PUBLIC` unset) |
| Per-IP / per-stream rate limit (`internal/server/ratelimit.go`) | Token-bucket. Active when `SPOUT_PUBLIC=true`. |
| Per-session size cap + rolling window (`internal/server/caps.go`) | 100 MB free / 1 GB paid. |
| Session TTL sweeper (`internal/server/ttl.go`) | Goroutine that prunes expired runs. |
| Idle connection timeout | Reaps WS connections idle > 24h. |

## Planned dashboard / browser features

| Feature | What |
|---|---|
| WS-failure UX banner | Replace blinking-cursor-on-WS-failure with clear inline error (Seashells issue #17) |
| Auto-load on scroll-up (replay) | Twitter/Reddit-style infinite scroll into older bytes |
| Jump-to-time markers | "1h ago" / "6h ago" / "start" — seek by byte offset |
| stdout/stderr filter toggle | Color-coded, switchable view |
| Tier 2 keyless-viewer UX | Inline "needs encryption key" state with paste form |
| Browser WebCrypto decryption | `crypto.subtle.decrypt()` per WS frame for Tier 2 |
| PWA install + push notifications | Service worker + VAPID push |

## Planned operational features

| Feature | What |
|---|---|
| Server-assigned naming | Hosted ignores client `-n`; assigns to prevent squatting |
| HTTP chunked POST as fallback | CLI tries WS, falls back when `Upgrade` header is blocked by proxy |
| Tier 0 server-side state alerts | Basic alerts via query string for `nc`/`curl` users (`?notify=email:...&on=ended`) |
| Multi-box sharding | Consistent-hash on run name; CLI + browser routed to same box |
| Metrics endpoint | Prometheus `/metrics` (behind auth) |
| mDNS / Bonjour discovery | LAN-scoped discovery for self-hosted servers |

## Bigger-ticket items

For the comprehensive list (open questions, deferred features, rejected
approaches), see [ARCHITECTURE.md §14-15](../../ARCHITECTURE.md#14-deferred-items).
