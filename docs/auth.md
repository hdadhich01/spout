# Auth

Auth is independent of encryption. It controls **who can connect**, not
what the server sees. Encryption is covered in [tiers.md](tiers.md).

## Token auth `[planned]`

```
CLI    → WebSocket handshake with Authorization header
Server → checks against SPOUT_TOKEN env var
```

When `SPOUT_TOKEN` is set on the server:

- All WebSocket connections (ingest + viewer) must include the token
- nc is disabled (can't set HTTP headers)
- REST API endpoints require the token
- Dashboard HTML pages are served without auth (they're just static HTML;
  the WebSocket connection they open carries the auth)

## CLI token resolution

Auth is explicit-only. A server profile uses a token iff it declares a
`token:` field; the value of that field is the **name of the env var**
that holds the real token. The value itself lives in `.env`.

```yaml
# global spout.yaml
servers:
  work:
    url: spout.company.internal:3000
    token: SPOUT_TOKEN_WORK      # name of the env var holding the value

  home:
    url: home.lan:3000
    # no `token:` = no auth for this profile
```

```bash
# ~/.config/spout/.env
SPOUT_TOKEN_WORK=alice-secret-token

# <repo>/.env                 (project-specific override, walked up)
SPOUT_TOKEN_WORK=per-project-token
```

Resolution:

1. **`srv.Token`** — the CLI reads the env var named by this field.
2. No `token:` field → **no token is sent**. There is no implicit fallback
   (no `SPOUT_TOKEN_<PROFILE>`, no default `SPOUT_TOKEN`).
3. Raw `host:port` (no profile match) → **no token**.

Among `.env` files, the deepest file wins per variable.

```bash
spout -s work run python train.py    # picks $SPOUT_TOKEN_WORK
```

## No-auth mode

When `SPOUT_TOKEN` is not set on the server, everything is open. This is
correct for:

- Localhost development
- `spout.sh` Tier 1 (nc - anyone can create sessions)
- `spout.sh` Tier 2 (URL + `#key=` is the auth - link = access)

## `.env` discovery

`.env` travels with `spout.yaml` at every level. An `.env` is not required
to have a sibling yaml - a bare `.env` still contributes to the merged
environment.

```
<repo>/experiments/exp1/.env      ← highest priority
<repo>/.env
~/.config/spout/.env              ← lowest priority
```

## Future auth

Out of scope for now. Possible directions:

- OAuth / SSO for enterprise
- Per-session access tokens
- Viewer-only vs admin tokens
- IP allowlisting
- Accounts + `/dash` page on `spout.sh`
