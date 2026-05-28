# Server Config

Env vars, flags, deployment recipes. Same binary serves all four
tiers — behavior is determined by env vars and flags, not branched code.

For the architectural rationale, see
[ARCHITECTURE.md §11.5-11.6](../../ARCHITECTURE.md#115-operations-env-vars--deployment).

## Environment variables

| Var | Default | What |
|---|---|---|
| `SPOUT_TOKEN` | unset | Legacy single ingest token (still accepted). Prefer `spout server token …` for named, revocable tokens. |
| `SPOUT_ADMIN_PASSWORD` | unset | Enables remote admin login at `/admin/login`. Unset → the dashboard is reachable only from the same box (loopback). |
| `SPOUT_TOKENS` | `~/.config/spout/tokens.json` | Path to the ingest-token store (rarely overridden). |
| `SPOUT_PORT` | `3000` | Listen port |
| `SPOUT_PUBLIC` | `false` | Hosted-mode: enables Tier 0 TCP, server-assigned naming, rate limits, swaps `/` to landing page, disables `/api/runs` |
| `SPOUT_STORE` | `file` | Backend: `file` / `sqlite` / `postgres` `[planned]` |
| `SPOUT_DB_PATH` | `~/.config/spout/spout.db` | SQLite file (when `SPOUT_STORE=sqlite`) `[planned]` |
| `SPOUT_DB` | unset | Postgres DSN (when `SPOUT_STORE=postgres`) `[planned]` |
| `SPOUT_S3_ENDPOINT` | unset | R2 endpoint (e.g., `https://<acct>.r2.cloudflarestorage.com`) `[planned]` |
| `SPOUT_S3_BUCKET` | unset | R2 bucket name `[planned]` |
| `SPOUT_S3_KEY` | unset | R2 access key `[planned]` |
| `SPOUT_S3_SECRET` | unset | R2 secret key `[planned]` |
| `SPOUT_TTL` | `0` (unlimited) | Session TTL (e.g., `7d` on hosted) `[planned]` |
| `SPOUT_VAPID_PUBLIC` | unset | VAPID public key (PWA push notifications) `[planned]` |
| `SPOUT_VAPID_PRIVATE` | unset | VAPID private key `[planned]` |
| `SPOUT_REPLAY_CAP` | `4MB` | Initial viewer replay window `[planned]` |

## Flags

| Flag | What |
|---|---|
| `-p, --port <N>` | Listen port (overrides `SPOUT_PORT`) |
| `--public` | Hosted-mode (overrides env) `[planned]` |

## Access model

Two distinct credentials (Tiers 1 + 3); auth activates only once you configure
something (a token or an admin password). With nothing set up the server is
open — same-box use just works.

| Credential | What it unlocks | How |
|---|---|---|
| **Ingest token** | *Sending* output: `POST /api/run`, `/ingest`, `/api/run/:name/events`, and the run-data APIs | A `Bearer` token the CLI presents. Manage with `spout server token create/list/revoke` or the `/admin` UI. (Browsers can't send Bearer headers, so this is a CLI/API credential.) |
| **Admin** | The `/admin` dashboard + token management (`/api/tokens`) | **Same box (loopback) = automatic.** Remote = log in at `/admin/login` with `SPOUT_ADMIN_PASSWORD` (sets a session cookie). |

- An ingest token can send + read run data but **cannot** manage tokens (no privilege escalation).
- The loopback check ignores requests carrying `X-Forwarded-For`/`X-Forwarded-Host`, so a same-box reverse proxy can't make every request look local. Behind a proxy, use `SPOUT_ADMIN_PASSWORD` for remote admin.
- `SPOUT_PUBLIC=true` bypasses all of this (hosted / URL-as-auth).

## Self-hosted vs hosted (the runtime split)

Same binary, different env vars:

|  | Self-hosted (default) | Hosted (`SPOUT_PUBLIC=true`) |
|---|---|---|
| `/` | Redirects to `/admin` | Landing page (marketing) |
| `/admin` | Dashboard + token mgmt (loopback or login) | n/a |
| `/api/runs` | Admin or ingest token | Disabled / 404 |
| Auth | Named ingest tokens (`spout server token`) + same-box/login admin | Ignored — URL is the auth |
| `/ingest/:name` | Ingest token (or same-box) | Open (rate limits prevent abuse) |
| `/ws/:name` | Admin (browser viewer) | Open |
| Tier 0 TCP listener `:1337` | Off | On |
| Server-assigned naming | Off (client picks) | On (no squatting) |
| Per-IP / per-session caps | Off | On |
| Session TTL sweeper | Off (unlimited) | On (default `7d`) |

The two HTML files for `/`:
- `static/index.html` — dashboard run list (self-hosted default)
- `static/landing.html` — marketing / install / Seashells comparison (hosted `[planned]`)

Both embedded into the binary via `go:embed`. Runtime picks based on `SPOUT_PUBLIC`.

**If both `SPOUT_TOKEN` and `SPOUT_PUBLIC=true` are set**: public-mode wins, token is ignored. Server warns at startup.

## Recipes

### Personal / single-user

```bash
spout server                                      # localhost:3000, no auth
```

### Team self-hosted with token auth

Mint a per-teammate ingest token (a running server picks it up live):

```bash
spout server -p 3000                       # on the box
spout server token create alice            # prints alice's token
```

Give Alice her token; her CLI sends it via:

```yaml
# Alice's ~/.config/spout/spout.yaml
servers:
  acme:
    url: spout.acme.com:443
    token: SPOUT_TOKEN_ACME    # name of the env var holding her token
```

The dashboard is reachable from the server box itself; for remote admin set
`SPOUT_ADMIN_PASSWORD` and log in at `/admin/login`:

```bash
SPOUT_ADMIN_PASSWORD=$(openssl rand -hex 16) spout server -p 3000
```

(Legacy `SPOUT_TOKEN=<value>` still works as a single shared ingest token.)

### Mid-scale self-host (SQLite metadata) `[planned]`

```bash
SPOUT_STORE=sqlite \
SPOUT_DB_PATH=/opt/spout/spout.db \
SPOUT_TOKEN=... \
spout server
```

Use when 10K+ runs makes `spout ls` slow. Bytes still on disk; only
metadata moves to SQLite.

### Hosted (`spout.sh`) `[planned]`

```bash
SPOUT_PUBLIC=true \
SPOUT_STORE=postgres \
SPOUT_DB=postgres://user:pass@neon-host/spout \
SPOUT_S3_ENDPOINT=https://<acct>.r2.cloudflarestorage.com \
SPOUT_S3_BUCKET=spout-runs \
SPOUT_S3_KEY=... SPOUT_S3_SECRET=... \
SPOUT_TTL=7d \
spout server -p 443
```

## Deployment

### Caddy reverse proxy + TLS (recommended for self-host)

```caddy
# /etc/caddy/Caddyfile
spout.acme.com {
    reverse_proxy localhost:3000
}
```

Caddy auto-issues TLS via Let's Encrypt.

### Nginx

```nginx
server {
    listen 443 ssl;
    server_name spout.acme.com;
    ssl_certificate /etc/letsencrypt/live/spout.acme.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/spout.acme.com/privkey.pem;

    location / {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 86400s;
    }
}
```

### Docker `[planned]`

```bash
docker run -d \
  -p 3000:3000 \
  -v /opt/spout:/data \
  -e SPOUT_TOKEN=$(openssl rand -hex 32) \
  spout/spout server
```

### Helm chart `[planned]`

For k8s self-host. See `deploy/helm/`.

## Caps + abuse prevention (when `SPOUT_PUBLIC=true`) `[planned]`

| Layer | Default |
|---|---|
| Per-IP new streams/min | 10 |
| Per-IP concurrent streams | 5 free / 25 paid |
| Per-stream byte rate | 1 MB/s sustained, 5 MB burst |
| Per-stream size cap | 100 MB (rolling window — oldest bytes drop) |
| Connection idle timeout | 24h |

Self-hosted has no caps by default.

## Recommended hosted stack `[planned]`

```
Compute       → Hetzner CX52 ($55/mo, 32 GB RAM, 320 GB NVMe)
                or Fly.io / Railway (auto-scaling)
Metadata DB   → Neon (Postgres serverless, 0.5 GB free)
Stream blobs  → Cloudflare R2 (10 GB free, $0 egress)
Domain + CDN  → Cloudflare
TLS           → Caddy / Cloudflare auto
```

Cost at 100K active streams: ~$500/mo (mostly R2 storage). See
[ARCHITECTURE.md §10.2](../../ARCHITECTURE.md#102-cost-back-of-envelope-hosted-spoutsh).

## See also

- [routes.md](routes.md) — every endpoint
- [future.md](future.md) — planned env vars, backends, features
- [../cli/config.md](../cli/config.md) — CLI-side env vars + `spout.yaml` schema
