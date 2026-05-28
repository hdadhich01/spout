# Spout — Architecture

The master architecture spec. Every other doc defers to this one when
they disagree. If you're an AI agent or a new contributor catching up,
read this top to bottom.

For the user-facing intro, see [README.md](README.md). For the running
dev log, see [DEVELOPMENT.md](DEVELOPMENT.md). For the AI quick-ref of
file paths and conventions, see [CLAUDE.md](CLAUDE.md).

## Topic docs

ARCHITECTURE.md (this doc) is the single source of truth for design.
Only four topic docs supplement it — focused references that aren't
design-narrative:

| Doc | What's in it |
|---|---|
| [docs/cli/](docs/cli/) | CLI: `commands.md` (every command + flag), `config.md` (`spout.yaml` schema), `future.md` (planned items) |
| [docs/server/](docs/server/) | Server: `routes.md` (HTTP/WS/TCP endpoints), `config.md` (env vars + deployment), `future.md` (planned items) |
| [docs/faq.md](docs/faq.md) | Troubleshooting + common gotchas |

Tiers, server deployment, storage, encryption, and notifications are
all covered in this document directly (§3, §11.5, §6, §5, §7).

---

## 1. North star

> **Spout** — the reliable, persistent, self-hostable evolution of
> Seashells. Live terminal streaming that works, named runs, optional
> end-to-end encryption, AI-driven alerts, and zero-install demos.
> Same binary runs hosted, self-hosted, or piped through `nc`.

Positioning lead: *"Pipe any terminal output to a live web dashboard.
Free, hosted, encrypted by default, or self-host the whole thing."*

## 2. Two operating principles

Everything else follows from these:

1. **Local archive is canonical.** The CLI writes every byte to local
   disk. The server is a TTL-bounded convenience layer. Hosted server
   downtime never destroys user data.
2. **Server is a thin fan-out hub.** Producer + browser do the
   expensive work (compression, encryption, rendering). The server
   moves bytes between them. This is what makes Spout cheap to host
   at any scale.

## 3. The four tiers

Tiers describe **use cases**, not encryption levels. Same single binary
serves all four; the tier is determined by HOW the user invokes Spout
and which server they target.

> **Naming model (locked)**:
> ```
> job        (optional grouping label — pure metadata, set in yaml)
> └─ run     (one user invocation — owns the URL, top-level addressable thing)
>    └─ stream(s)  (labeled byte stream(s) within a run)
> ```
> Single-stream runs (pipe / `spout run cmd`) have one stream with an
> auto-derived label. Multi-stream runs (blueprint from `streams:` in
> yaml, or grown via `spout add-stream`) have N streams, each with its
> yaml `label:`.
> User-facing strings say "run" / "stream"; "session" stays internal
> (tmux / server). For full locked terminology see
> [docs/cli/commands.md](docs/cli/commands.md).

| Tier | Invocation | Use case | Storage | Encryption |
|---|---|---|---|---|
| **0** | `cmd \| nc spout.sh 1337` or `cmd \| curl -T - https://spout.sh/ingest` | Zero-install, viral demo, hobbyist, locked-down boxes | Hosted (server stores during run) | None |
| **1** | `cmd \| spout` (target = `spout.sh`) | Mass usability, casual users | Hosted + local dual-write | Plaintext over wss:// |
| **2** | `cmd \| spout` (target = `spout.sh`, default) | Privacy-conscious, research labs, IP-sensitive workloads | Hosted (ciphertext) + local dual-write | **AES-256-GCM, key in URL fragment** |
| **3** | `spout server` (self-hosted) + `cmd \| spout -s acme` | Enterprise, internal infra, regulated environments | Self-hosted local disk, configurable retention | Plaintext over ws:// or wss:// (user trusts their server) |

**Tier 2 is the default for `spout.sh`.** Users opt out with
`--no-encrypt` to get Tier 1.

**Tier 3 doesn't get E2E** because encrypting against a server you
control is theater. The code path supports it if a self-hosted user
opts in, but most won't bother.

For tier-specific examples and trade-offs, see §3.1 below.

### 3.1 Per-tier walkthroughs

**Tier 0 — `nc` / `curl`, zero install**

```bash
python train.py | nc spout.sh 1337
python train.py | curl -T - https://spout.sh/ingest
```

What you get: live streaming, 7-day retention. What you don't: encryption,
local mirror, stdout/stderr split, exit code (TCP close is the terminus).
Encryption can't be bridged through `nc` cleanly — `openssl enc | nc`
pipelines have static-IV problems. Privacy → install the CLI.

**Tier 1 — CLI + hosted, plaintext**

```bash
python train.py | spout --no-encrypt
```

Same as Tier 2 below but server can read your bytes. Useful for public
CI logs / demos, and when you want simpler URLs (no `#k=...` fragment).

**Tier 2 — CLI + hosted, E2E (default for `spout.sh`)**

```bash
python train.py | spout
# → https://spout.sh/r/wolf-a3f2#k=YWJjZGVmZ2hpamtsbW5vcHFyc3R1...
```

Server stores ciphertext. Browser decrypts via WebCrypto using key from
URL fragment. Server is encryption-blind — same code path, same cost.
Lose the URL = lose the data (no recovery; that's E2E). See §5.

**Tier 3 — self-hosted**

```bash
# On your server:
SPOUT_TOKEN=$(openssl rand -hex 32) spout server -p 3000
# Behind Caddy for TLS (see §11.5 for full recipes)

# On your CLI machines:
# ~/.config/spout/spout.yaml:
servers:
  acme:
    url: spout.acme.com:443
    token: SPOUT_TOKEN_ACME

# Then:
python train.py | spout -s acme
```

E2E mode is supported but theater on a server you control — most don't bother.

## 4. Streaming architecture

### 4.1 Wire framing — TLV (binary type-length-value)

Every byte that flows over a Spout WebSocket is wrapped in a TLV frame.
Same format on CLI ingest, server local NVMe, R2 retention object, and
viewer broadcast.

**Frame format:**

```
+----------+----------+-----------+
| 1B opcode| 4B BE len| N bytes   |
+----------+----------+-----------+
```

**Opcode table:**

| Opcode | Name | Payload |
|---|---|---|
| `0x01` | STDOUT | raw stdout bytes |
| `0x02` | STDERR | raw stderr bytes |
| `0x03` | EXIT | 1 byte exit code (0–255) |
| `0x04` | META | small JSON patch (e.g., metadata update) |
| `0x05` | HEARTBEAT | empty (every 30s, proves CLI is alive) |
| `0x10–0x1F` | ENC variants | reserved for Tier 2 encrypted variants |
| `0x20–0xFF` | reserved | future opcodes |

**Why TLV (not JSONL or inline byte opcodes):**
- Length-prefixed avoids escaping (terminal output legitimately contains `\x01`/`\x02`)
- Compact (~5 bytes overhead per chunk; <0.05% on 64 KB chunks)
- Easy to add new opcodes (extensibility)
- No JSON parsing on hot path
- Tier 2 wraps payloads, framing unchanged

**Why this beats inline OSC 9999 markers** (current exit-code mechanism):
OSC was fragile (false positives if a user's program emitted the same
sequence) and mode-locked (only tmux holding-shell wrote it). EXIT
opcode is robust and works across all modes.

### 4.2 Transports

Three transports, one framing format on top.

| Transport | Used by | Auth | Notes |
|---|---|---|---|
| **WebSocket** (`wss://`) | Tier 1/2/3 CLI ingest + viewer | Bearer token in Upgrade header | Primary path; goes through HTTPS firewalls |
| **Raw TCP** (`:1337`) | Tier 0 (`nc`) | None | Server detects raw bytes, treats as one big STDOUT opcode |
| **HTTP chunked POST** (`/ingest/:name`) | Tier 0 (`curl`) and proxy fallback | Bearer or none | One-way; HTTP body carries TLV stream |

**Deferred / rejected**: Server-Sent Events (deferred — fallback only if
real users hit WS-blocking proxies); WebRTC (rejected — NAT hell,
doesn't survive CLI exit); gRPC (rejected — no browser support).

### 4.3 Strategy E: storage during run + R2 retention

**The model:**

```
DURING RUN:
  CLI: writes every byte to local FileStore (canonical) + WebSocket to server
  Server: receives bytes, appends to local NVMe (working storage),
          fans out to any connected viewers,
          updates DB row on lifecycle events only (started/heartbeat/ended)

END OF RUN (CLI sends EXIT opcode):
  Server: finalizes local NVMe file
          uploads to R2 via multipart (5 MB parts)
          deletes local file after successful R2 upload
          starts 7-day TTL clock

7 DAYS LATER:
  R2 lifecycle rule deletes the object
  User's local mirror is unaffected (FileStore retention is unlimited)
```

**Why Strategy E (over alternatives we rejected):**

| Strategy | Verdict |
|---|---|
| A. No server storage during run; RAM ring buffer when viewers | Ring buffer adds complexity; CLI replay for late viewers is slow on home internet |
| B. No buffer, every viewer triggers CLI replay | Terrible UX, multiple uploads from CLI per run |
| C. RAM cache only | At scale: 8 MB × 10K viewers = 80 GB RAM; even 256 KB × 100K = 25 GB |
| D. Always write to R2 during run | Catastrophic ops cost ($11K+/mo at 100K streams) due to RAM-vs-ops tension |
| **E. Local NVMe during run + R2 multipart at end** | **Chosen.** NVMe is free on Hetzner; same architecture for all tiers; predictable cost |

The key insight: local NVMe on a $50/mo Hetzner box (320–500 GB
included) is essentially free. Disk writes are ~10 µs (negligible CPU).
The only cloud cost is R2 storage volume × retention (bounded by caps +
TTL) and bandwidth (free egress on R2).

### 4.4 End-of-run lifecycle

| Case | What happens |
|---|---|
| **Default — server up the whole run** | Server already has full data on local NVMe (dual-write streamed it). On EXIT: finalize, multipart upload to R2, delete local, set status=success, start 7d TTL. **No "push at end" needed beyond the multipart upload itself.** |
| **Server was down at run start** | CLI streamed local-only with a warning. User runs `spout share <name>` later to upload local copy in one shot |
| **Server died mid-run** | Server has partial bytes; local has full. User runs `spout share <name>` to replace partial with complete |
| **User opted local-only** (`--offline`) | Pure local. User runs `spout share <name>` if/when they want to push to a server |

**`spout share <name>`** is the unified upload-from-local command. Used
for all recovery / explicit-push cases.

### 4.5 Viewer flow + replay UX

Three rules for the server → viewer side:

1. **Drop slow viewers.** Each viewer has a bounded outbound channel (256 frames). On overflow, server closes WS with status `1011`. Browser auto-reconnects. Producer is never back-pressured.

2. **Snapshot-then-stream replay.** On viewer connect: server reads from local NVMe (or R2 post-run), sends history in 64 KB WS frames, then switches to live tail.

3. **Cap initial render at 4 MB.** xterm.js can hold ~50 MB before sluggish. Default render = last 4 MB (covers ~60K lines). Older bytes via:
   - Auto-load on scroll-up (Twitter/Reddit infinite scroll)
   - Jump-to-time markers ("1h ago", "6h ago", "start")
   - Download full log button (HTTP file download for >50 MB cases)

**Producer disconnect mid-run**: CLI's WS dies. CLI's local mirror is
the durability backstop. User can `spout share` to re-upload.
Resume-from-offset on the producer side is **deferred**.

**Viewer reconnect after blip**: just rejoin; replay from start runs
again. No cursor-based resume in v1. Resume-from-offset for viewer is
**deferred**.

### 4.6 Per-tier methodology

| Tier | Streaming | Storage during run | Storage post-run |
|---|---|---|---|
| **0 (nc/curl)** | Raw TCP / HTTP chunked → server batches into local writes | Server's local NVMe (no other choice; nc can't dual-write) | R2 multipart, 7d TTL |
| **1 (CLI plaintext)** | WebSocket TLV; CLI dual-writes to local + server | Server's local NVMe (same as Tier 0) | R2 multipart, 7d TTL |
| **2 (CLI E2E)** | WebSocket TLV; CLI encrypts payloads with AES-GCM before sending | Server's local NVMe (ciphertext) | R2 multipart, 7d TTL (ciphertext) |
| **3 (self-host)** | WebSocket TLV; CLI dual-writes; user's own server | Server's local disk | Same disk; configurable retention (default unlimited) |

**Same architecture, different post-run destinations.**

## 5. Tier 2 encryption

Server is **encryption-blind**. Same Strategy E architecture, same code
path, same cost. Encryption is producer/consumer only.

**Flow:**

```
1. CLI generates fresh AES-256 key per run (in memory only)
2. CLI registers run; server returns name (e.g., wolf-a3f2)
3. CLI prints URL with key in fragment:
     https://spout.sh/r/wolf-a3f2#k=base64key
   ── browsers DO NOT send fragment to server, ever ──
4. Per chunk: random 96-bit nonce + AES-GCM-encrypt
   send [nonce(12B) || ciphertext]
5. Server stores ciphertext bytes; identical to Tier 1
6. Viewer opens URL; browser extracts key from window.location.hash
7. Browser uses crypto.subtle.importKey + decrypt per chunk; renders plaintext
8. End of run: multipart upload to R2 (still ciphertext)
```

**What server sees vs doesn't:**

| Visible to server | Encrypted (server can't read) |
|---|---|
| Run name (routing) | Stream bytes |
| Status, timestamps | Exit code (in encrypted_meta blob) |
| Byte count | Command name |
| Connection IP | Working directory |
| | Git branch, host, user |

Sensitive metadata fields go inside an `encrypted_meta` blob in the DB
row. Server stores opaque ciphertext for both stream and metadata.

**Crypto rules:**
- AES-256-GCM (authenticated encryption — protects against tampering)
- Random 96-bit nonce per chunk; never reuse (key, nonce) pair
- Key 256 bits, base64 = 44 chars, fits cleanly in URL fragment
- HTTPS required (WebCrypto won't operate over plain HTTP)
- Per-run key (not per-user); URL = access; revoke = delete the run

**Cost overhead**: 28 bytes per chunk (12-byte nonce + 16-byte auth
tag), ~0.04% on 64 KB chunks. Server CPU/RAM/disk/ops: identical to
Tier 1.

### 5.1 Browser UX for keyless viewers

Browser handles five cases inline. **Never renders ciphertext as text**
(it corrupts xterm's terminal state).

| Case | UX |
|---|---|
| URL has full `#k=...` | Normal viewer; decrypt + render |
| URL missing fragment | "🔒 End-to-end encrypted run" inline state. Show plaintext metadata. Provide paste-form for full URL. **Don't open WS.** |
| Key present but decryption fails (wrong key or tampered bytes) | "⚠️ Couldn't decrypt" — AES-GCM auth tag failure surfaces both wrong-key and tampered cases identically |
| HTTP (no TLS) | "🚫 End-to-end encryption requires HTTPS" — WebCrypto refuses on plain HTTP |
| Run started, no bytes yet | "⏳ Waiting for encrypted bytes…" with live indicator |

**Validation flow (canary check)**: before opening the stream WS, browser
attempts to decrypt the small `encrypted_meta` blob. Success → key is
correct → open WS. Failure → show "couldn't decrypt", don't open WS.

### 5.2 Threat model

| Attacker | Tier 1 | Tier 2 |
|---|---|---|
| Network sniffer (CLI ↔ server) | TLS protects (wss://) | TLS + ciphertext both protect |
| Compromised server (operator goes rogue) | ❌ Sees everything | ✓ Sees ciphertext only |
| Compromised cloud provider | ❌ Can read your bytes | ✓ Can't decrypt |
| Government subpoena | ❌ Server can hand over plaintext | ✓ Only ciphertext |
| Anyone with the URL (incl. fragment) | Can view | Can view (intended — URL = access) |
| Anyone WITHOUT the URL | Can't view | Can't view AND can't decrypt |

The big win for Tier 2: **compromised server / cloud provider**. Even
if someone breaks into `spout.sh`, they can't read your stream.

### 5.3 Holes audit

No catastrophic holes; all standard mitigations.

| Hole | Severity | Mitigation |
|---|---|---|
| Nonce reuse | Critical | Random 96-bit nonces (`crypto/rand`) + tests |
| URL in browser history | Acceptable | Part of the model (URL = access) |
| Referer header leak | None | Browsers strip fragments per RFC 7231 |
| Malicious server JS | Real | SRI on dashboard JS + open source + audits |
| Key loss = data loss | Acceptable | Optional local key cache (off by default) |
| Metadata timing leak | Acceptable | Generic E2E limit; not specific to Spout |
| Tier 0 can't E2E | By design | Users wanting privacy use Tier 2 |
| WebCrypto needs HTTPS | Trivial | spout.sh has TLS; self-host should too |
| Run name leaks via SNI | Minor | TLS 1.3 ECH (Cloudflare supports) |
| Metadata in plaintext columns | Implementation | Only `encrypted_meta`, no plaintext sensitive columns |

### 5.4 Why AES-256-GCM (not something else)

| Cipher | Verdict |
|---|---|
| **AES-256-GCM** | **Chosen.** Approved for SECRET-level US gov data. Used by TLS 1.3, Signal, WhatsApp, 1Password. Hardware-accelerated. WebCrypto-native. |
| AES-256-CBC | No tamper protection without separate MAC |
| ChaCha20-Poly1305 | Similar security; **WebCrypto support incomplete** |
| Post-quantum (Kyber, Dilithium) | Overkill for terminal logs; standards still evolving |
| Asymmetric (RSA/ECDH per-viewer) | Way more complex; no "share via link" UX |
| Stream-level encryption | Can't decrypt incrementally; breaks live viewing |
| Inline byte opcodes | Real terminal output contains those bytes; escaping endless |

### 5.5 Implementation sizing

- **CLI**: ~150 LOC Go (stdlib `crypto/aes` + `crypto/cipher`, no external deps)
- **Browser**: ~50 LOC JS (`crypto.subtle`)
- **Server**: 0 LOC changes beyond a flag in DB row marking "this run is Tier 2"

## 6. Storage data model

### 6.1 What's stored, where

Three logical pieces per run:

| Piece | FileStore (CLI + Tier 3) | SQLiteStore (Tier 3 at scale) | Postgres + R2 (hosted) |
|---|---|---|---|
| Identity / metadata | `meta.json` | sqlite row | postgres row |
| stdout bytes | `data.raw` (append) | `data.raw` (append) | R2 object |
| stderr bytes | `data.err` (append) | `data.err` (append) | R2 object |
| Events (exit, errors) | `events.jsonl` | `events.jsonl` | R2 or postgres jsonb |

**Universal rule: bytes never live in a DB.** Always files (or R2
objects). DB only holds metadata. Pattern from Loki, Datadog, Vercel,
GitHub Actions.

### 6.2 Local mirror (CLI dual-write)

Every byte goes to **two places independently**: user's local disk
(canonical) and the configured server (convenience).

**Failure rules:**

| Failure | Behavior |
|---|---|
| Server unreachable at start | Warn, stream local-only. URL says "local only — `spout share` later to upload" |
| Server dies mid-run | Warn, fall back to local-only |
| Local disk full at start | Warn, stream server-only. Local archive disabled this run |
| Local disk full mid-run | Warn, stop local writes. Server continues. "local copy ends at byte N" |
| User opt-out | `--no-archive` flag or `archive: false` in profile. Server-only |

**Rule: either side failing never kills the run.** Bytes survive in at
least one place.

### 6.3 Backend matrix

| User type | Backend | Setup work |
|---|---|---|
| CLI on user's machine | FileStore | None — single binary, JSON metadata files |
| Hobbyist self-host | FileStore | Run binary; optional reverse proxy |
| Mid-scale self-host (10K+ runs) | SQLite | `SPOUT_STORE=sqlite` env var |
| Enterprise self-host | FileStore or SQLite | Same + Caddy/Nginx + token |
| spout.sh (hosted) | Postgres + R2 | `SPOUT_STORE=postgres` + DSN/bucket |

**No SQLite on CLI ever** (binary size, schema migration burden,
single-writer lock). JSON files are grep-friendly, backup-friendly,
debug-friendly. CLI never has more than ~1000 runs over a user's
lifetime; linear directory walk is fast at that scale.

**All three server backends compiled into one binary** (~25-35 MB
stripped). Runtime selection only — no build tags.

### 6.4 Naming + URL design

Three concepts:

- **Run name** — unique identifier (`word-xxxx` standalone, `run_name:` template for streams). URL primitive.
- **Job** — optional grouping (`job: ml-training` in yaml). Dashboard metadata only.
- **Label** — per-stream identifier within a multi-pane run.

**URL shape:**

```
/r/<run-name>            single run                 (canonical)
/j/<job-name>            job overview (all runs)    [planned]
/r/<run>#k=<key>         Tier 2 E2E (key in fragment)
```

**Don't nest job in URL** (e.g., `/j/job/run`). Job is mutable, run
name is immutable; URL stability requires immutable identifiers.

**Vanity URLs** (`/u/<user>/<run>` style) require user accounts —
**deferred** to v2+.

### 6.5 Retention

| Where | Default TTL | Configurable |
|---|---|---|
| CLI local archive | unlimited | `local_ttl: 30d` in profile [planned] |
| Self-hosted (Tier 3) | unlimited | `session_ttl: 30d` in server config [planned] |
| Hosted free | 7 days after run ends | No |
| Hosted paid (future) | longer | Per-tier [planned] |
| Active streams | indefinite | until producer disconnects |

The 7d hosted cap is the cost-control mechanism. CLI's local copy is
unaffected. `spout share <name>` re-uploads after TTL expiry.

### 6.6 "User comes back" matrix

| When user returns | Open URL | `spout open <n>` | `spout logs <n>` | `spout share <n>` |
|---|---|---|---|---|
| During run, same machine | live + replay | live + replay | dumps from local | n/a |
| After end, within TTL, same machine | full replay | full replay | dumps from local | no-op |
| After end, within TTL, different machine | full replay | full replay | server fallback | n/a |
| **After TTL, same machine** | 404 + hint | **opens local archive in ephemeral localhost viewer** | dumps from local | **uploads local → fresh server record + new TTL** |
| After TTL, different machine | 404 dead | 404 unless `spout sync` (future) | unavailable | unavailable |
| User wants to "redo" | n/a — runs are immutable. Start a new one; use `run_name:` templates and `job:` grouping |

**Runs are immutable once ended.** No append-to-finished. Re-running
creates a new run record (via `run_name: exp-{n}` templates).

## 7. Observability + notifications

Two producer-evaluated layers; the server stays thin (it stores and routes,
never scans bytes or evaluates rules):

1. **Observe engine — shipped.** A CLI-side loop taps each run's plaintext
   output, cheaply pre-filters it, and periodically asks a model to assess the
   run — emitting structured events (status, metrics, synthesis, and built-in
   loop/drift/amnesia detectors). Events are written to the local archive
   (canonical) and POSTed to `/api/run/<n>/events`; the server stores them
   opaquely and the dashboard renders them. See `internal/observe` and
   [docs/cli/observe.md](docs/cli/observe.md).
2. **Notification routing — planned.** When an event is alert-worthy, the CLI
   POSTs `/api/run/<n>/alert`; the server looks up the user's notification
   profile and fans out to sinks. No analysis server-side.

```
CLI (per run, in-stream):
  Taps its own byte stream (plaintext, pre-encryption)
  Pre-filters cheaply, then runs ONE model call per check:
    built-in detectors (classify/loop/drift/amnesia) + user prompt-rules
  Emits events → local events.jsonl + POST /api/run/<n>/events       [shipped]
  On alert-worthy event: POST /api/run/<n>/alert {rule, context, ...} [planned]

Server:
  Stores events opaquely; serves GET /api/run/<n>/events             [shipped]
  Routes alerts to the user's configured sinks                       [planned]
  No analysis, no byte scanning
```

**Per-tier behavior:**

| Tier | Observability |
|---|---|
| 0 (nc) | None (no CLI). Basic server-side state alerts only — "ended"/"failed"/"inactive" via query string. |
| 1 (CLI plaintext) | Full observe engine on the CLI; events to local + server; alerts route via server |
| 2 (CLI E2E) | Same as Tier 1; the engine runs on plaintext locally. A cloud `api` model still *sees* output — use `local`/`cli` to stay on-box. Event payloads encrypt like output [planned]; server sees only metadata |
| 3 (self-host) | Same as Tier 1; user-defined sinks (internal Slack, etc.) |

### 7.1 The observe engine (shipped)

One model call per check evaluates every built-in detector **and** every user
rule together (re-sending the window per detector would blow tokens and break
prompt caching):

| Built-in detector | Fires when… |
|---|---|
| `classify` | (always) sets the run type from the output's character |
| `loop` | the process repeats the same failing action/error with no progress |
| `drift` | an agent diverges from its stated goal |
| `amnesia` | an agent re-asks/re-does settled work or drops a constraint it was following |

User rules are free-text prompts (`observe.rules[].prompt`, optional `sources`),
folded into the same call. This **supersedes** the earlier `watch.rules[].when`
operator DSL (`regex_match`, `value_decreased_by`, `ai_check`, …): instead of a
bespoke expression language, the model evaluates a plain-English instruction
directly, and does the matching itself. A cheap local pre-filter (velocity /
repeated lines / error keywords / idle) gates calls so a stable run barely
spends one; the model returns a `next_check_secs` hint clamped to 10s–5m; a
per-run call cap bounds cost. With `--otel`, each event is also exported as an
OTLP/HTTP span. Full spec: [docs/cli/observe.md](docs/cli/observe.md).

### 7.2 Sinks (notification routing — planned)

| Sink | Implementation | Hosted cost |
|---|---|---|
| Slack webhook | POST to user-configured URL | Free |
| Discord webhook | POST to user-configured URL | Free |
| Email | SES or Postmark | ~$0.10/1K |
| Push (PWA) | Web Push via VAPID | Free, browser-native |
| Generic webhook | POST to user-configured URL | Free |

Total notification cost at 100K active streams: ~5K alerts/day,
dominated by email (~$0.10–1/day). Total <$10/month.

### 7.3 VAPID push setup (browser push)

Generate VAPID keys once on the server, set as env vars:

```bash
SPOUT_VAPID_PUBLIC=...  SPOUT_VAPID_PRIVATE=...  spout server
```

Browser viewer page registers a service worker, subscribes for push.
Subscription stored server-side keyed by run name. PWA install
(Phase 4) makes notifications work even when browser is closed.

### 7.4 Tier 0 server-side state alerts

Tier 0 has no CLI to evaluate rules, so server-side basic alerts via
query string:

```bash
cmd | nc spout.sh "1337?notify=email:foo@bar.com&on=ended"
```

Triggers: `started`, `ended`, `failed`, `inactive` (no bytes for N min).

### 7.5 Extensibility (deferred)

The model supports cleanly:
- Alert dedup within a window
- Batching / digests ("summary of last hour")
- Alert chaining (rule A → watch for rule B → escalate)
- Cross-run alerts (CLI tracks state via local archive)
- Snooze / mute (per-run or per-rule)

(The "AI watchdog" once parked here is now the shipped observe engine, §7.1.)

All extensions are CLI-side. Server stays a dumb router.

For yaml schema (sink config + rule shape), see [docs/cli/config.md](docs/cli/config.md).

## 8. CLI surface

### 8.1 Commands (flat structure)

| Group | Commands |
|---|---|
| Streaming | `spout` (pipe), `spout run` (incl. `run --into` to add a stream), `spout server` |
| Sessions | `ls`, `attach`, `kill`, `rename`, `delete`/`rm`, `share`, `open`, `logs`, `observe` |
| Setup | `init`, `login`, `config`, `doctor` |
| Meta | `stats`, `completion` |

**Stay flat.** Sub-actions only on `config` (`config show`/`set`/`get`).
Family form (`spout session attach`) was rejected — flat is faster to
type, tab completion handles discovery.

### 8.2 Tier mapping by invocation

```bash
cmd | nc spout.sh 1337         # Tier 0: zero-install, plaintext
cmd | curl -T - https://spout.sh/ingest   # Tier 0 alt
cmd | spout                    # Tier 2 (E2E) when target is spout.sh
cmd | spout                    # Tier 1 (plaintext) when target is self-hosted
cmd | spout --no-encrypt       # Force Tier 1 even on hosted
cmd | spout --offline        # Local-only, no server at all
cmd | spout -s acme            # Use 'acme' profile
```

**Tier 2 default for `spout.sh`**. Privacy is the right default; cost
is zero; URL fragment is fine in modern share UX.

### 8.3 stdout/stderr capture

Separation is handled at the source:

- **Pipe mode** (`cmd | spout`) merges stdout/stderr at the shell level.
  For tools like `tqdm` that print to stderr, use `cmd 2>&1 | spout`.
- **Multi-stream yaml** can declare separate stdout/stderr streams if
  the user wants them rendered as distinct panes.

A dedicated `spout exec` subprocess wrapper for byte-level
stdout/stderr separation was considered and dropped — pipe-mode merge
covers the common case and yaml multi-stream covers the rare case
where actual separation matters. Keeping the surface smaller wins.

### 8.4 First-run UX

```
$ python train.py | spout
spout: streaming to spout.sh (default; set SPOUT_SERVER to override)
spout: https://spout.sh/r/wolf-a3f2#k=YWJj... (copied)
[output flows]
```

One-time note printed on first invocation. Cached state in
`~/.config/spout/` suppresses thereafter.

### 8.5 tmux dependency

| Mode | Requires tmux |
|---|---|
| `cmd \| spout` (pipe) | No |
| `spout run cmd` | Yes |
| `spout add-stream` | Yes |
| `streams:` config | Yes |
| `spout ls`, `open`, `logs`, etc. | No |

Windows portability is not a v1 priority; the tmux-using commands are
Linux/macOS only. Pipe mode (`cmd | spout`) and the read-only commands
work everywhere.

See [docs/cli/commands.md](docs/cli/commands.md) for the complete command reference.

## 9. Caps + abuse prevention

### 9.1 Per-tier caps

| Tier | Cap | Behavior at cap |
|---|---|---|
| Free hosted (Tier 0/1/2) | 100 MB | Roll-over (oldest bytes drop, last 100 MB viewable) |
| Paid hosted (future) | 1 GB | Roll-over |
| Self-hosted (Tier 3) | unlimited (configurable) | n/a |

### 9.2 Defense-in-depth layers (hosted only)

| Layer | Limit | Purpose |
|---|---|---|
| Per-IP new streams/min | 10 | Stops connection-flood DDoS |
| Per-IP concurrent streams | 5 free / 25 paid | Prevents monopolization |
| Per-stream byte rate | 1 MB/s sustained, 5 MB burst | Stops `/dev/urandom` abuse |
| Per-stream size cap | 100 MB free / 1 GB paid | Final safety net |
| Connection idle timeout | 24h | Reaps zombie connections |

Self-hosted has no caps by default. Operator configures via env.

### 9.3 R2 ops batching (the cost saver)

**5 MB multipart upload chunks** is the magic number. R2 ops cost is
bounded by `total_bytes / 5 MB`, regardless of run duration.

```
Naive (per-byte writes): 50 KB/s × 100K streams × $4.50/M ops = $22.5K/sec. Fail.
Batched (5 MB chunks): ~22 ops per run × 10K runs/day = $30/month. Trivial.
```

## 10. Cost analysis

### 10.1 Cost dimensions ranked

| Dimension | Real cost? | Bounded by |
|---|---|---|
| Storage volume × time | 🔴 DOMINANT | retention + per-session caps |
| Egress bandwidth | 🔴 MAJOR | use R2 ($0 egress) + replay-window cap |
| Cloud blob ops | 🟡 if naive; 🟢 if batched | multipart upload (~22 ops/run regardless of size) |
| CPU per byte | 🟢 free | memcpy; modern boxes do millions/sec |
| RAM per connection | 🟢 cheap | ~30 KB per WS; bounded |
| DB writes | 🟢 free | indexed; thousands/sec on tiny boxes |
| Heartbeats | 🟢 free | 100 bytes × 30s = trivial |
| Ingress | 🟢 free | most cloud providers don't charge ingress |

**Optimize for storage volume × time and egress. Ignore the rest.**

### 10.2 Cost back-of-envelope (hosted spout.sh)

Assumptions: average run is 36 MB / 1 hour, 10K new runs/day, 7d
retention, R2 backend, Hetzner compute.

| Active streams | Box count | NVMe needed | R2 storage | R2 ops | Egress | Total |
|---|---|---|---|---|---|---|
| 1K | 1 | 100 GB | $5 | $0 | $0 | **~$15/mo** |
| 10K | 1 | 365 GB | $30 | $5 | $0 | **~$60/mo** |
| 100K | 10 | 365 GB each | $300 | $30 | $0 | **~$500/mo** |
| 1M | 100 | 1 TB each | $3K | $300 | $0 | **~$5K/mo** |

**Linear scaling.** R2's free egress is the killer feature — AWS S3 at
the same scales would add $1K-$30K/mo just in egress.

### 10.3 Multi-box sharding (when needed)

Single-box capacity is fine until ~10K simultaneous streams. Beyond,
multi-box sharding is required for connection count anyway.

```
hash(run_name) → assign to one box (consistent hashing)
CLI + browser routed to same box
Postgres metadata: shared single DB across all boxes
R2: shared (post-run retention)
```

Standard pattern (Discord, Cloudflare, Vercel for WS-heavy traffic).

## 11. Server work budget (per byte)

To make "server is thin" concrete:

| Step | Cost |
|---|---|
| Read WS frame | ~100 ns |
| Append to local NVMe | ~10 µs |
| Fan-out to N viewers | ~100 ns × N |
| Periodic DB update (every ~3s, not per byte) | ~1 ms |

**What server does NOT do per byte**: JSON parse, base64 transcode,
compress, encrypt (Tier 2 is producer + browser only), render, index
content, DB write.

**Per-connection idle cost**: ~30 KB RAM, ~0% CPU.

**Work budget across the system:**

```
PRODUCER (CLI)          SERVER             BROWSER (viewer)
───────────────         ──────             ────────────────
Read pipe               Receive bytes      Receive bytes
Write local disk        Append to NVMe     Decrypt (Tier 2,
Compress (TLS)          Fan-out to viewers   WebCrypto, HW-accel)
Encrypt (Tier 2)        Update meta DB     Render xterm.js
                         ~once per 3s

user pays CPU           you pay $$         viewer pays CPU
(FREE to you)           (cheap memcpy)     (FREE to you)
```

## 11.5 Operations (env vars + deployment)

### Server env vars

| Var | Default | What |
|---|---|---|
| `SPOUT_TOKEN` | unset | Legacy single ingest token (still accepted); prefer `spout server token …` |
| `SPOUT_ADMIN_PASSWORD` | unset | Enables remote `/admin/login`; unset → dashboard is same-box only |
| `SPOUT_TOKENS` | `~/.config/spout/tokens.json` | Ingest-token store path (rarely overridden) |
| `SPOUT_PORT` | `3000` | Listen port |
| `SPOUT_STORE` | `file` | Backend: `file` / `sqlite` / `postgres` |
| `SPOUT_DB_PATH` | `~/.config/spout/spout.db` | SQLite file (when `SPOUT_STORE=sqlite`) |
| `SPOUT_DB` | unset | Postgres DSN (when `SPOUT_STORE=postgres`) |
| `SPOUT_S3_ENDPOINT` | unset | R2 endpoint (e.g., `https://<acct>.r2.cloudflarestorage.com`) |
| `SPOUT_S3_BUCKET` | unset | R2 bucket name |
| `SPOUT_S3_KEY` | unset | R2 access key |
| `SPOUT_S3_SECRET` | unset | R2 secret key |
| `SPOUT_TTL` | `0` (unlimited) | Session TTL (e.g., `7d` for hosted, `0` for self-host) |
| `SPOUT_PUBLIC` | `false` | Hosted-mode: enables Tier 0 TCP, server-assigned naming, rate limits |
| `SPOUT_VAPID_PUBLIC` | unset | VAPID public key (PWA push notifications) |
| `SPOUT_VAPID_PRIVATE` | unset | VAPID private key |

CLI env vars (read by `spout`, not `spout server`):

| Var | What |
|---|---|
| `SPOUT_SERVER` | Override default server profile |
| `SPOUT_TOKEN_<NAME>` | Token value for profile `<name>` (referenced by `servers[<name>].token: SPOUT_TOKEN_<NAME>`) |

### Self-host deployment recipes

**Single binary** (default — FileStore, no auth):
```bash
spout server                          # localhost:3000
spout server -p 8080                   # custom port
SPOUT_TOKEN=$(openssl rand -hex 32) spout server   # team mode
```

**Caddy reverse proxy + TLS** (recommended; auto-issues Let's Encrypt):
```caddy
# /etc/caddy/Caddyfile
spout.acme.com {
    reverse_proxy localhost:3000
}
```

**Nginx reverse proxy**:
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

**Docker** [planned]:
```bash
docker run -d -p 3000:3000 -v /opt/spout:/data \
  -e SPOUT_TOKEN=$(openssl rand -hex 32) \
  spout/spout server
```

**Mid-scale self-host** (SQLite metadata, files for bytes):
```bash
SPOUT_STORE=sqlite SPOUT_DB_PATH=/opt/spout/spout.db SPOUT_TOKEN=... spout server
```

**Hosted (`spout.sh`) recipe**:
```bash
SPOUT_STORE=postgres \
SPOUT_DB=postgres://user:pass@neon-host/spout \
SPOUT_S3_ENDPOINT=https://<acct>.r2.cloudflarestorage.com \
SPOUT_S3_BUCKET=spout-runs \
SPOUT_S3_KEY=... SPOUT_S3_SECRET=... \
SPOUT_TTL=7d SPOUT_PUBLIC=true \
spout server -p 443
```

### Recommended hosted stack (for `spout.sh`)

```
Compute       → Hetzner CX52 ($55/mo, 32 GB RAM, 320 GB NVMe)
                or Fly.io / Railway (auto-scaling)
Metadata DB   → Neon (Postgres serverless, 0.5 GB free)
Stream blobs  → Cloudflare R2 (10 GB free, $0 egress)
Domain + CDN  → Cloudflare
TLS           → Automatic via Caddy or Cloudflare
```

Cost at 100K active streams: ~$500/mo (mostly R2 storage). See §10.2.

## 11.6 Self-hosted vs hosted: dashboard + auth differences

These are **runtime-config differences**, not separate code paths.

Two credentials, evaluated producer-side (see [docs/server/config.md → Access
model](docs/server/config.md#access-model)):

- **Ingest token** — the CLI's "send output" credential (`Bearer`). Named,
  revocable tokens via `spout server token …` or the `/admin` UI; legacy
  `SPOUT_TOKEN` still accepted. Browsers can't send Bearer headers, so this is
  a CLI/API credential.
- **Admin** — the `/admin` dashboard + token management. Automatic for same-box
  (loopback) requests; remote browsers log in at `/admin/login` with
  `SPOUT_ADMIN_PASSWORD` (session cookie). The loopback check ignores
  `X-Forwarded-For` so a same-box proxy can't impersonate local.

|  | Self-hosted (Tier 3, default) | Hosted (`SPOUT_PUBLIC=true`) |
|---|---|---|
| `/` route | Redirects to `/admin` | **Landing page** — marketing / quickstart |
| `/admin` | **Run-list dashboard + token management** (admin: loopback or login) | n/a |
| `/r/<name>` | Viewer (admin) | Same viewer, no auth (URL-as-auth) |
| `/api/runs` | Admin or ingest token | Disabled / 404 (no enumeration) |
| `/ingest/:name` | Ingest token (or same-box) | Open (run-name URL is the boundary; rate limits) |
| `/ws/:name` | Admin (browser viewer) | Open |
| Token mgmt (`/api/tokens`) | Admin only | n/a |

**Implementation**:
- Auth activates only once a token or `SPOUT_ADMIN_PASSWORD` is configured; with
  nothing set up, same-box use just works and remote is denied by default.
- Ingest tokens live in `config.TokensPath()` (`tokens.json`); the store reloads
  on file change so the CLI and a live server stay in sync.
- `SPOUT_PUBLIC=true` bypasses all auth and (planned) swaps `/` to the landing page.

**Why two `index.html` variants**:
- `static/index.html` (dashboard) — embedded for self-hosted, shows the run list.
- `static/landing.html` (marketing) — embedded for hosted, shows install / quickstart / Seashells comparison.

Both embedded into the same binary via `go:embed`. Runtime picks which
to serve at `/` based on `SPOUT_PUBLIC`. **Alternative** (decoupled):
host the landing page on Cloudflare Pages, point `spout.sh` DNS there;
the Go binary only serves API/WS/viewer routes. Pick when hosted-mode lands.

## 12. Monorepo structure

The repo stays a monorepo. Adding `clients/` and `deploy/` siblings as
needed.

```
cmd/spout/        Go CLI
cmd/server/       Go standalone server entry point
internal/         shared code
  store/            Store interface + FileStore (SQLiteStore/PostgresStore/R2 planned)
  server/           HTTP/WS handlers, middleware, auth
  config/           spout.yaml schema, walk-up discovery, templates
  observe/          CLI-side observability engine + LLM clients + sinks
  notify/           notification routing + sinks                 [planned]
  crypto/           Tier 2 AES-GCM helpers (CLI side)            [planned]
clients/python/   Thin Python SDK + Colab wrapper           [future]
clients/node/     Thin Node SDK                              [future]
deploy/           Dockerfile, Helm chart, Fly/Railway       [future]
web/static/       Dashboard HTML/JS/CSS source
docs/             Topic reference docs
```

**Why monorepo:**
- Same binary serves all four tiers via runtime config
- Protocol changes (new TLV opcodes, etc.) atomic in one PR
- Lockstep CLI ↔ server releases prevent version-skew bugs
- Lower ops burden (single release pipeline)

**Don't split CLI from server, ever.** That's the entire architectural
premise.

## 13. Status taxonomy

| Status | Color | Meaning |
|---|---|---|
| streaming | `#22c55e` (green) | Receiving data right now |
| awaiting | `#eab308` (yellow) | Run-mode shell idle at a prompt |
| success | `#166534` (dark green) | Run-mode command exited 0 |
| error | `#b91c1c` (red) | Run-mode command exited non-zero |
| killed | `#555` (gray) | Ended before any exit marker |

24-bit truecolor on CLI mirrors exact hex on web dashboard. Brand aqua
`#5ac8e2` for headers. **Changing one hex requires editing both
`cmd/spout/cmd/color.go` and `web/static/*.html`.**

## 14. Deferred items

Things in the architecture but not in the first release.

| Item | Why deferred |
|---|---|
| **License decision** (AGPL vs Apache vs BSL) | Not blocking; revisit before public launch |
| **Hosted HA** (multi-region failover) | Single-region with dual-write story is fine for launch |
| **Postgres + R2 prod deployment** | Backend code lands; actual hosted deployment is post-MVP ops work |
| **Vanity URLs** (`/u/<user>/<run>`) | Requires user accounts |
| **User accounts on `spout.sh`** | URL-as-auth is fine for MVP; accounts are Phase 3 |
| **Resume-from-offset** (CLI / browser reconnect with `?from=<bytes>`) | CLI dual-write is the durability backstop; browser just rejoins |
| **mDNS / Bonjour LAN discovery** | Phase 4 polish for self-host UX |
| **Group / team encryption keys** | v1 is per-run keys only |
| **Mid-stream key revocation** | User deletes the run to revoke |
| **Forward secrecy beyond per-run keys** | Per-run keys give 90% of this naturally |
| ~~AI watchdog rule type~~ | **Shipped** as the observe engine (§7.1, `internal/observe`) |
| **Notification routing → sinks** | Engine + events shipped; the `/alert` → Slack/email/push fan-out (§7.2) is the remaining deferred piece |
| **Cross-run alerts** | Phase 4 (CLI tracks state via local archive) |
| **Snooze / mute** | Phase 4 |
| **Job overview page** (`/j/<job>`) | Phase 2 polish |
| **Search across runs** | Phase 4 |
| **Run comparison view** | Phase 4 |
| **Public opt-in gallery** | Phase 4 (virality feature) |
| **PWA install + offline viewing** | Phase 4 |
| **Python SDK** (`pip install spoutsh`) | After CLI stabilizes — see §14.1 below. Pip name is `spoutsh` because the `spout` PyPI name is taken by an abandoned package (PEP 541 reclamation pursued in parallel) |
| **Node SDK** | After Python SDK is stable |
| **Helm chart** | If k8s users demand it |
| **Encrypted Client Hello (ECH)** | Cloudflare handles when available |
| **Constant-rate transmission for timing privacy** | Generic E2E hardening; rare need |
| **Local key cache for E2E recovery** | CLI optionally caches keys; off by default for paranoid users |
| **HTTP chunked POST as Tier 1/2 fallback** | Phase 2 (corporate proxy unlock) |
| **Server-Sent Events viewer fallback** | Phase 3 (only if real users hit WS-blocking proxies) |

### 14.1 Python SDK + Google Colab (locked design)

The Python SDK targets the Colab/Jupyter use case where most code is
inline Python (`model.fit()`, `tqdm` loops) — not `!shell` commands.
CLI piping doesn't fit; we need a Python-native API.

#### Pip name

**`spoutsh`** for both pip name and Python module:

```bash
pip install spoutsh
```
```python
import spoutsh
spoutsh.run(...)
```

The `spout` name on PyPI is taken by a 12+-month-stale data-streaming
package by `daviesjamie`. **PEP 541 abandonment request** filed in
parallel; if granted we migrate `spoutsh` → `spout` (with `spoutsh` as
back-compat alias). If refused, `spoutsh` stays.

#### The strategic play (Colab specifically)

Spout is uniquely valuable for Colab because the WebSocket from the
Colab runtime to spout.sh is **independent of the user's browser tab**:

```
User's browser tab ◄── Jupyter protocol ──► Colab runtime VM
                                                    │
                                                    │ WebSocket
                                                    │ (stays open as
                                                    │  long as runtime
                                                    │  is alive)
                                                    ▼
                                                 spout.sh
```

- User runs `%%spoutsh` cell → cell starts streaming via WS to spout.sh
- User closes laptop / browser tab → runtime continues for ~90 min
- During those 90 min, **bytes keep flowing from runtime to spout.sh**
- User opens spout.sh URL on phone → sees live output
- User reconnects to Colab → cell may or may not show in-progress output, but spout.sh has everything
- Runtime eventually times out → WS closes → spout.sh marks run as ended → notification fires

**Spout doesn't prevent Colab timeout** (Colab limits: 12h max, ~90 min
idle). But it captures everything up to the moment the runtime dies and
notifies the user when it does.

For long-running training, recommend Tier 3 self-hosted on a persistent
box.

#### API surface — four patterns

```python
import spoutsh

# 1. Cell magic (Colab/Jupyter — primary UX)
%%spoutsh my-training
model.fit(X, y, epochs=100)
# Inline iframe of the run viewer renders below the cell

# 2. Context manager (general Python)
with spoutsh.run(name="my-training") as r:
    print("starting…")
    model.fit(X, y, epochs=100)
    # r.url, r.status, r.bytes_recv accessible

# 3. Programmatic (custom log values, no stdout redirect)
run = spoutsh.start(name="my-experiment")
for epoch in range(100):
    spoutsh.log(f"epoch {epoch}: loss={loss:.3f}")
run.end()

# 4. Subprocess wrapper (captures stdout + stderr separately)
spoutsh.exec(["python", "train.py", "--epochs", "100"], name="my-training")
```

#### Implementation: pure Python WS client

**No binary download. No Go runtime ship in the wheel.** Reasons:
- Colab boxes are ephemeral; binary download is friction
- Python WS clients are mature
- TLV protocol is simple (~50 LOC encode/decode)
- AES-GCM via stdlib `cryptography.hazmat` for Tier 2

Layout:

```
spoutsh/
  __init__.py          public API (run, start, log, end, exec)
  _client.py           WebSocket client + TLV encoder
  _crypto.py           AES-GCM for Tier 2 (uses `cryptography`)
  _capture.py          sys.stdout/stderr tee
  _magic.py            IPython cell magic registration
  _config.py           Reads ~/.config/spout/spout.yaml + .env (same as CLI)
  _ipython.py          Iframe display helpers (only loaded if IPython present)
```

Dependencies: `websockets`, `cryptography`, `pyyaml`. Optional IPython
detection at import time. Total wheel ~2-3 MB.

**Total LOC**: ~400-600 Python.

#### Colab-specific behavior

When `import spoutsh` runs and detects Colab (`google.colab` importable):
- Register `%%spoutsh` cell magic via `IPython.core.magic`
- Auto-iframe of run viewer below cells using `%%spoutsh` (pending CSP test)
- Warn at run start: "Colab filesystem is ephemeral; spout.sh is your canonical store. Run loses state if Colab times out (~90 min idle / 12h max)."
- Default: enable an "ended" notification (so user gets pinged when training finishes/dies). Configured via the `notify:` map in `spout.yaml`. User opts out explicitly.

#### Colab caveats users must know

| Caveat | Mitigation |
|---|---|
| Runtime timeout kills runs | Document 90 min idle / 12h max. Recommend Tier 3 for multi-day. |
| `tqdm.notebook` writes to widgets, not stdout | Recommend plain `tqdm` for SDK capture. Auto-detect-and-handle is Phase 4. |
| Filesystem ephemeral | Local archive doesn't survive box death. spout.sh is canonical. |
| Pro+ "background execution" unreliable | Don't trust it. Plan for tab-close-and-watch-on-mobile UX instead. |
| Cell output replay on reconnect spotty | Colab UI is unreliable. spout.sh URL is the reliable view. |

#### Colab notebook template

Ship `spout.sh/colab-template.ipynb` with an **"Open in Colab"** button on
the landing page. Pre-built with imports + ML training boilerplate +
cell-magic examples. Goes live when SDK ships.

```markdown
[![Open In Colab](https://colab.research.google.com/assets/colab-badge.svg)](https://colab.research.google.com/github/hdadhich01/spout/blob/main/clients/python/colab-template.ipynb)
```

#### Verify before implementation

1. `spoutsh` availability on PyPI (`pip download spoutsh==9.9.9` returns 404 if free)
2. Cloudflare bot detection on spout.sh from Colab IPs (mitigation: pre-shared token mode)
3. Colab CSP allows self-iframe (fallback: clickable link)

#### Implementation order

1. Reserve `spoutsh` on PyPI (placeholder version)
2. File [PEP 541](https://peps.python.org/pep-0541/) request for `spout`
3. Build pure Python client (`_client.py`, `_crypto.py`, `_capture.py`)
4. Add cell magic + iframe (`_magic.py`, `_ipython.py`)
5. Build Colab template notebook (`clients/python/colab-template.ipynb`)
6. Wire "Open in Colab" button into spout.sh landing page
7. Smoke test against a real Colab notebook
8. PyPI publish v0.1.0

## 15. Trade-offs we considered and rejected

Documenting the road not taken so we don't relitigate.

| Approach | Why rejected |
|---|---|
| **CLI replay-from-CLI-on-demand model** (server has no bytes during run) | Slow over home internet; multiple replays per viewer painful; CLI death = no recovery |
| **Always write to R2 during run** | Catastrophic ops cost ($11K+/mo at scale) due to RAM-vs-ops tension |
| **RAM ring buffer for live viewing** | Local NVMe is free on Hetzner and just as fast; ring buffer adds complexity for no benefit |
| **Pure Go fallback for tmux** | Complexity for marginal Windows benefit; Windows portability is not a v1 priority |
| **Family-form CLI commands** (`spout session attach`) | Flat is faster to type; tab completion handles discovery; ~14 commands isn't actually that many |
| **`spout exec` subprocess wrapper** | Original justification (Windows portability + stdout/stderr split) didn't hold up: Windows isn't a v1 target, and `cmd 2>&1 \| spout` + multi-stream yaml cover the stderr cases. Surface stays smaller. |
| **End-of-run mass upload** (no during-run server storage) | DDoS vector when many runs end at once; 5GB upload at end is brutal on user's home internet |
| **WebRTC peer-to-peer** | NAT traversal hell, doesn't survive CLI exit, complex |
| **gRPC streaming** | Heavy dependency, no browser support, awkward UX |
| **Pure JSONL framing** | 33% bandwidth inflation from base64; JSON parse on hot path |
| **Inline byte opcodes** (`\x01...\x02...`) | Real terminal output legitimately contains these bytes; escaping is endless pain |
| **Stream-level encryption** (single AES-GCM op for entire run) | Can't decrypt incrementally; breaks live viewing |
| **ChaCha20-Poly1305** | Browser WebCrypto support incomplete |
| **Post-quantum crypto** | Overkill for terminal logs; standards still evolving |
| **Asymmetric per-viewer encryption** | Way more complex; no "share via link" UX |
| **Passphrase-derived keys** (default) | Weaker than random; could be optional alternative |
| **SQLite on CLI** | Binary size, schema migration burden, single-writer lock; CLI scale doesn't need indexing |
| **Bytes in DB** | Universal anti-pattern; bytes belong in files / blob storage |
| **Per-byte DB updates** | Every-3-seconds heartbeat is more than enough for any UI need |
| **Live status lights via constant meta updates** | Reload button is simpler and just as user-friendly |
| **Run nesting in URL** (`/j/<job>/<run>`) | Job is mutable; run name is immutable; URL stability requires immutable identifiers |
| **Two separate downloads** (CLI vs server) | Single binary with subcommands is industry standard |
| **Heartbeats optional** | Required every 30s simplifies connection-health detection |
| **META mid-run mutation** | Start-of-stream only in v1 |

## 16. Verification gates (full release)

Before declaring v1 done:

1. **All tests green**: `go test ./... && go vet ./...` clean
2. **Binary size**: `go build -ldflags="-s -w" ./...` produces 25-35 MB stripped
3. **Tier 1 end-to-end**: `cmd | spout` against local server; URL viewable; live + replay both work; bytes match local mirror byte-for-byte
4. **Tier 0 end-to-end**: `cmd | nc spout.sh 1337` and `cmd | curl -T - https://spout.sh/ingest` both work
5. **Tier 2 end-to-end**: encrypted CLI → ciphertext on server → browser decrypts; missing fragment shows keyless-viewer UX; wrong key shows decryption-failed UX
6. **Tier 3 end-to-end**: `spout server` running on a separate box; CLI configured profile points at it
7. **Auth gate**: `SPOUT_TOKEN=x spout server` rejects wrong tokens; `spout doctor` shows green
8. **Dual-write**: server-down scenario leaves intact local file; `spout share` recovers
9. **Cap behavior**: stream past 100 MB cap; rolling window keeps last 100 MB
10. **Notifications**: configured rule fires → CLI sends alert → server routes to webhook (test sink)
11. **Scale soak**: bench 100K connections + 10 concurrent streams; producer throughput stays ≥90% of unloaded; slow viewer dropped cleanly
12. **Multi-day soak**: 5-day run completes; bytes preserved; replay works
13. **R2 lifecycle**: end-of-run upload completes; lifecycle rule deletes after 7d; URL becomes 404 with friendly message; `spout open <name>` falls back to local archive
14. **Cross-platform**: Linux, macOS (pipe mode and read-only commands work on Windows too; tmux-using commands are Linux/macOS only — Windows isn't a v1 target)

## 17. Open strategic questions (decide before public launch)

| Question | Why it matters | Options |
|---|---|---|
| **License** | Hard to change after first public release | AGPL-3.0 / Apache-2.0 / BSL-1.1 → Apache after 4y / Defer |
| **Hosted HA aggressiveness** | Defines operational story | Single-region + dual-write story / Multi-region from day 1 / Both |
| **Anonymous abuse model** | URL-as-auth vs login-required | Hard caps + URL-only / GitHub-optional for higher caps / GitHub required for non-trivial use |
| **Run browsing on `spout.sh`** | Authenticated users see "their own" runs? | No browsing ever / Personal dashboard requires auth / Gallery for opt-in public runs |
| **Pricing tiers** | When and how to monetize | Free-only / Free + paid for higher caps / Enterprise self-host licenses |

These don't block implementation. Phase 1 work is the same regardless.
