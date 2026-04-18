# Dashboard

One set of HTML/JS files. Embedded in the server binary via `go:embed`.
Same HTML shipped to every server, regardless of tier.

Source: `web/static/*.html`
Embed mirror: `internal/server/static/*.html`
(Keep them synced - see repo-root [CLAUDE.md](../CLAUDE.md).)

## Pages

| URL | Page | What it shows |
|---|---|---|
| `/` | Run list | All runs, split into "active" / "ended" sections with status dots, mode, time |
| `/r/:name` | Run viewer | xterm.js terminal, status badge, metadata, copy buttons |

## Status model (CLI ↔ dashboard, one source of truth)

| Status | Color | Dot | Badge bg | Meaning |
|---|---|---|---|---|
| streaming | `#22c55e` | filled | `#166534` | Active output (>50 bytes/sec burst) |
| awaiting | `#eab308` | filled | `#854d0e` | Active but idle (>1s without a burst) |
| success | `#166534` | filled | `#166534` | Ended cleanly (exit 0) - run mode only |
| error | `#b91c1c` | filled | `#7f1d1d` | Ended non-zero - run mode only |
| killed | `#555` | filled | `#374151` | Stopped before any exit marker (user kill / close terminal) |

Brand aqua (`#5ac8e2`): section headers, `<h1>`, `#back:hover`.

All colors are mirrored in `cmd/spout/cmd/color.go` as 24-bit truecolor ANSI
so the CLI renders the exact same hex the browser paints. Changing a
status color means editing both files.

## Hover popups

The dashboard list has tooltip boxes on each status dot explaining what the
color means; the session page has one on the status badge. Both are driven
by the same `statusInfo(status)` helper in each HTML file. Keep them in sync.

## Tier-specific behavior `[planned]`

| Feature | Tier 1 (plaintext) | Tier 2 (E2E) |
|---|---|---|
| Terminal output | Rendered directly | Decrypted via WebCrypto before render |
| Privacy banner | "This session is unencrypted" | None (it's E2E) |
| Server-side search | Works (server has plaintext) | Not possible |
| Watchdog | Server-side | CLI-side (before encryption) |
