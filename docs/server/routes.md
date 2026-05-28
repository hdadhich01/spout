# Server Routes

HTTP, WebSocket, and (Tier 0) raw TCP endpoints exposed by `spout server`.

For the wire protocol details (TLV framing, opcodes), see
[ARCHITECTURE.md §4.1](../../ARCHITECTURE.md#41-wire-framing--tlv-binary-type-length-value).

## Auth

Two credentials (see [config.md → Access model](config.md#access-model)). Auth
activates once a token or `SPOUT_ADMIN_PASSWORD` is configured:

- **Ingest token** (`Authorization: Bearer <token>`) — the CLI's send credential.
  Grants `/ingest`, the run-data APIs, and events. Not token management.
- **Admin** — same-box (loopback) requests, or a remote browser that logged in
  at `/admin/login` (cookie). Grants the dashboard pages, viewer WS, and token
  management (`/api/tokens`).

Always public: `/api/health`, `/static/*`, `/admin/login`. `SPOUT_PUBLIC=true`
bypasses everything (URL-as-auth). Locked page requests redirect to
`/admin/login`; locked API/WS requests get `401`.

## REST endpoints

### `GET /api/health`

Liveness + Spout-compatibility probe. **Always public.**

```bash
$ curl https://spout.sh/api/health
{"spout": true, "version": "1.0.0"}
```

Used by `probeSpout` to confirm a server is Spout (vs. some other thing
on port 3000).

### `GET /api/runs`

List runs. **Disabled when `SPOUT_PUBLIC=true`** (no enumeration on hosted).

```bash
$ curl -H "Authorization: Bearer $TOKEN" https://localhost:3000/api/runs
[
  {
    "name": "wolf-a3f2",
    "status": "streaming",
    "started_at": "2026-04-29T10:00:00Z",
    "byte_count": 1234567,
    "exit_code": null,
    "has_exit": false,
    "mode": "pipe",
    "job": "ml-training",
    "label": "training"
  },
  ...
]
```

### `GET /api/run/:name`

Single run metadata.

```bash
$ curl -H "Authorization: Bearer $TOKEN" https://localhost:3000/api/run/wolf-a3f2
{
  "name": "wolf-a3f2",
  "status": "streaming",
  "started_at": "2026-04-29T10:00:00Z",
  "byte_count": 1234567,
  "encrypted": true,                                 // Tier 2 [planned]
  "encrypted_meta": "base64(AES-GCM(...))"           // Tier 2 [planned]
}
```

For Tier 2, sensitive fields (command, dir, host, exit code) are inside
`encrypted_meta` and require the URL fragment key to decrypt.

### `POST /api/run`

Pre-create a run with metadata. Returns the assigned name.

```bash
$ curl -X POST \
       -H "Authorization: Bearer $TOKEN" \
       -H "Content-Type: application/json" \
       -d '{"name": "exp-1", "job": "ml-training", "mode": "run"}' \
       https://localhost:3000/api/run
{"name": "exp-1", "url": "https://localhost:3000/r/exp-1"}
```

Hosted (`SPOUT_PUBLIC=true`): server assigns the name regardless of
client suggestion (prevents squatting).

### `GET /api/run/:name/raw`

Replay full bytes for a run.

```bash
$ curl -H "Authorization: Bearer $TOKEN" https://localhost:3000/api/run/wolf-a3f2/raw
[full byte stream]
```

| Query param | What |
|---|---|
| `?download=1` | Sets `Content-Disposition: attachment` for browser file download |
| `?stream=stdout` | Filter to stdout opcode bytes only `[planned]` |
| `?stream=stderr` | Filter to stderr opcode bytes only `[planned]` |
| `?from=N` | Byte offset for partial replay `[planned]` |

For Tier 2, returns ciphertext bytes with TLV framing intact. Caller
must decrypt with the key.

### `POST /api/run/:name/rename`

```bash
$ curl -X POST \
       -H "Authorization: Bearer $TOKEN" \
       -H "Content-Type: application/json" \
       -d '{"name": "training-final"}' \
       https://localhost:3000/api/run/wolf-a3f2/rename
{"name": "training-final"}
```

### `DELETE /api/run/:name`

```bash
$ curl -X DELETE -H "Authorization: Bearer $TOKEN" https://localhost:3000/api/run/wolf-a3f2
{"ok": true}
```

### `POST /api/run/:name/events`

Append one observability event (the CLI observer → server). The body is a
single event as JSON; the server stores it **opaquely** (it never parses the
payload), so Tier-2 encrypted envelopes pass through unchanged. Appended to the
run's `events.jsonl`. See [observe.md](../cli/observe.md).

```bash
$ curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
       -d '{"id":"wolf-1","run":"wolf-a3f2","kind":"observation","obs":{"status":"on_track","summary":"…"}}' \
       https://localhost:3000/api/run/wolf-a3f2/events
ok
```

### `GET /api/run/:name/events`

Return a run's event log as newline-delimited JSON (`application/x-ndjson`),
or empty when there are none. Used by the dashboard's observe panel.

```bash
$ curl -H "Authorization: Bearer $TOKEN" https://localhost:3000/api/run/wolf-a3f2/events
{"id":"wolf-1","run":"wolf-a3f2","kind":"observation","obs":{...}}
{"id":"wolf-2","run":"wolf-a3f2","kind":"synthesis","obs":{...}}
```

## WebSocket endpoints

Both ingest and viewer use TLV framing (`[planned]` — today they use
raw bytes; OSC 9999 inline marker for exit codes, will migrate to EXIT
opcode).

### `WS /ingest/:name` (CLI ingest)

```javascript
const ws = new WebSocket("wss://spout.sh/ingest/wolf-a3f2", {
  headers: { Authorization: `Bearer ${token}` }
});

// Each TLV frame: [1B opcode][4B BE length][N bytes payload]
ws.send(buildFrame(0x01, stdoutBytes));   // STDOUT
ws.send(buildFrame(0x02, stderrBytes));   // STDERR  [planned]
ws.send(buildFrame(0x03, [exitCode]));    // EXIT
ws.send(buildFrame(0x04, metadataJSON));  // META
ws.send(buildFrame(0x05, []));            // HEARTBEAT (every 30s)
```

On `EXIT` opcode + WS close, server triggers end-of-run flush to R2 (in hosted mode).

### `WS /ws/:name` (browser viewer)

```javascript
const ws = new WebSocket("wss://spout.sh/ws/wolf-a3f2");

ws.onmessage = (event) => {
  const frame = parseFrame(event.data);
  switch (frame.opcode) {
    case 0x01: term.write(frame.payload); break;            // STDOUT
    case 0x02: term.write(styleStderr(frame.payload)); break;   // STDERR
    case 0x03: showExitCode(frame.payload[0]); break;       // EXIT
    case 0x04: updateMetadata(JSON.parse(frame.payload)); break;
    case 0x05: /* heartbeat */ break;
  }
};
```

Server sends history first (replay from local NVMe / R2), then live
tail. Bounded outbound channel (256 frames) — server drops slow viewers
with WS close code `1011`.

## TLV opcode reference

| Opcode | Name | Direction | Payload |
|---|---|---|---|
| `0x01` | STDOUT | both | raw stdout bytes |
| `0x02` | STDERR | both | raw stderr bytes (`[planned]`) |
| `0x03` | EXIT | CLI → server | 1 byte exit code (0–255) |
| `0x04` | META | both | small JSON metadata patch |
| `0x05` | HEARTBEAT | CLI → server | empty (every 30s) |
| `0x10–0x1F` | reserved | | Tier 2 encrypted variants `[planned]` |
| `0x20–0xFF` | reserved | | future opcodes |

## Tier 0 endpoints

These are enabled when `SPOUT_PUBLIC=true`.

### `TCP :1337` (raw)

```bash
$ python train.py | nc spout.sh 1337
https://spout.sh/r/wolf-a3f2
[output continues until disconnect]
```

First line returned: assigned URL. Subsequent inbound bytes treated as
one big STDOUT opcode (no multiplexing). TCP close = end of run.

### `POST /ingest/:name` (HTTP chunked)

For `curl` users and proxy-blocked WS clients.

```bash
$ python train.py | curl -T - https://spout.sh/ingest
https://spout.sh/r/wolf-a3f2
```

Body uses `Transfer-Encoding: chunked`. Carries TLV stream (or raw
bytes for true Tier 0 use).

For Tier 1/2 CLI users behind WS-blocking corporate proxies, this is
the fallback transport — CLI tries WS first, falls back to HTTP chunked
POST on `Upgrade` failure.

## Status codes

| Code | Meaning |
|---|---|
| 200 | OK |
| 201 | Created (`POST /api/run`) |
| 202 | Accepted (alert routing `[planned]`) |
| 401 | Unauthorized (token missing or wrong) |
| 403 | Forbidden (rate limited / cap exceeded `[planned]`) |
| 404 | Run not found (or expired off the server) |
| 409 | Conflict (run name collision on `POST /api/run`) |
| 413 | Payload too large (per-session size cap hit `[planned]`) |
| 429 | Too many requests (per-IP rate limit `[planned]`) |
| 500 | Server error |

Rate-limit responses include `Retry-After`.

## Pages

| URL | What |
|---|---|
| `/` | Redirects to `/admin` (self-hosted) OR landing page (`SPOUT_PUBLIC=true`) |
| `/admin` | Dashboard: run list + ingest-token management (admin only) |
| `/admin/login` | Remote admin login form (POST sets the session cookie) |
| `/admin/logout` | Clears the admin session cookie |
| `/r/:name` | Single run viewer (xterm.js) |
| `/j/:job` | Job overview — all runs in a job `[planned]` |

## Token management (admin only)

| Method | Path | What |
|---|---|---|
| `GET` | `/api/tokens` | List ingest tokens (`[{label, token, created}]`) |
| `POST` | `/api/tokens` | Mint a token — body `{"label":"alice"}`; returns the new entry |
| `POST` | `/api/tokens/revoke` | Revoke — body `{"label":"alice"}`. Bulletproof (no URL encoding). Dashboard uses this. |
| `DELETE` | `/api/tokens/:label` | Revoke by label (or token value). URL-decoded server-side. |

These require **admin** (loopback or `/admin/login`) — an ingest token can't
manage tokens. Same operations are available on the CLI via
`spout server token …`.

## Admin

| Method | Path | What |
|---|---|---|
| `GET` | `/api/admin/whoami` | `{"local": bool, "remote_login_ready": bool}` — lets the dashboard hide the "log out" link for same-box admins and surface remote-access setup hints |
| `GET` | `/admin/login` | Login form (HTML) |
| `POST` | `/admin/login` | Form-encoded `password=...` against `SPOUT_ADMIN_PASSWORD`. On match, sets the `spout_admin` session cookie and 302s to `/admin`; mismatch 302s to `/admin/login?error=1` |
| `GET` | `/admin/logout` | Clears the cookie, 302s to `/admin/login` |

`SPOUT_ADMIN_PASSWORD` unset → remote login is disabled entirely (login POST returns 403). The dashboard is reachable only from the same box (loopback admin) in that case.

## See also

- [config.md](config.md) — server env vars + deployment recipes
- [future.md](future.md) — planned endpoints and protocol additions
