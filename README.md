# Spout

Pipe any terminal command into a live, mobile-friendly web dashboard with smart
parsing, metrics, and notifications.

```bash
python train.py | spout
```

Scan the QR code. Watch progress, metrics, and logs update in real time on your
phone. Get push notifications when it finishes or something goes wrong.

## What It Does

Spout reads stdout from any long-running command, cleans up messy output
(tqdm progress bars, `\r` spam, ANSI escape sequences), extracts metrics
(loss, accuracy, epoch, ETA, and generic `key=value` pairs), and streams
everything to a beautiful dashboard with:

- Large live progress bar
- Sparkline charts for tracked metrics
- Cleaned log view with search/filter
- Optional raw terminal view (xterm.js)
- NLP-powered watch rules ("notify me when loss drops below 0.15")
- PWA installable on your phone with push notifications
- QR code for instant mobile access

Works with ML training, builds, data pipelines, encoding, tests, AI agent
sessions, and any other long-running CLI process.

## Usage

### Pipe Mode - watch while it runs

```bash
python train.py | spout
```

```
Spout • resnet-cifar-run-74
Progress:  62% • Epoch 31/50 • Loss: 0.192 • Acc: 93.4%

Encrypted session created
https://spout.sh/v/silent-rain-837#key=9vK7mPq2xL...

Scan this QR code with your phone:
    [ QR CODE ]

Privacy: End-to-end encrypted. Only you can read this.
```

Your output is encrypted locally before it leaves your machine. The server only
sees ciphertext. The decryption key lives in the URL fragment and never touches
the server.

### Run Mode - fire and forget

```bash
spout run --name "llama-finetune" "python train.py --epochs 100"
```

Creates a detached tmux session. Your terminal returns immediately. Check back
anytime:

```bash
spout ls                    # list managed sessions
spout attach llama-finetune # re-attach to the tmux session
```

Works great with AI coding agents too:

```bash
spout run --agent claude "claude code --task 'refactor auth module'"
```

### Zero-Install (Tier 1, unencrypted)

No binary needed. Works with `nc`:

```bash
python train.py | nc spout.sh 1337
```

Fast and frictionless, but unencrypted - the server can see your output.

### Fully Local (Tier 3)

Run everything on your own machine:

```bash
spout server
```

No data leaves your machine. Optional tunnel via localhost.run for remote access.

## Three Tiers

| Tier | How | Privacy | Requires CLI |
|------|-----|---------|--------------|
| 1 - Zero-Install | `nc spout.sh 1337` | None (server sees plaintext) | No |
| 2 - Encrypted Hosted | `command \| spout` | E2E encrypted (AES-GCM, key in URL fragment) | Yes |
| 3 - Self-Hosted | `spout server` | Full (everything local) | Yes |

Tier 2 is the recommended default. Tier 1 exists for zero-friction onboarding.
Tier 3 is for users who want full control.

## Smart Features

**Parser presets** - Built-in rules for tqdm, cargo, ffmpeg, pytest, Claude Code,
and Aider output. Extracts progress, metrics, and status automatically.

**Watchdog** - Fast regex layer for real-time metric tracking. Optional LLM layer
(Haiku, GPT-4o-mini, Ollama) analyzes recent chunks with a rolling summary for
intelligent alerts ("training stalled", "needs human input", "loss plateaued").

**Notifications** - ntfy, Slack, Discord, webhooks. PWA push to your phone. cmux
socket forwarding when running inside cmux.

**cmux compatible** - Parses OSC 777/99 sequences. `spout notify` mirrors the
`cmux notify` CLI. Existing cmux hooks and Claude Code scripts work without
modification. Spout is a remote companion to cmux, not a replacement.

## Architecture

```
command | spout CLI ---[AES-GCM ciphertext]--> spout.sh ---[SSE]--> Browser
                                                                     |
                                                            WebCrypto decrypts
                                                            with #key fragment
```

Go monorepo with two binaries sharing internal packages:

- `cmd/cli/` - the `spout` binary (pipe mode, run mode, notify, ls, attach)
- `cmd/server/` - the spout.sh hosted backend
- `internal/` - shared parser, types, state, crypto, watchdog, notifier
- `web/static/` - HTMX + Tailwind + Chart.js dashboard with PWA support (embedded via `go:embed`)

For Tier 3, the CLI embeds and runs the same server locally.

## Tech Stack

Go / Fiber / HTMX / Tailwind / Chart.js / xterm.js / AES-GCM / WebCrypto /
SSE / PWA / SQLite / PostgreSQL

## Development

```bash
go build ./cmd/cli/
go build ./cmd/server/

go test ./...
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for current status, architecture decisions,
and development priorities.

## License

TBD
