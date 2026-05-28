# Observability (`observe`)

Spout can watch a run's output as it streams and emit a small feed of
structured **events** — live status, extracted metrics, an end-of-run
synthesis, and built-in detectors for the ways long-running agents go wrong
(looping, drifting off-task, losing context). It's the same idea as an APM,
but it works from raw stdout, so it covers anything you pipe — builds,
migrations, training, deploys, coding agents — with no SDK and no code changes.

It's **off by default** and **opt-in per project**.

## The one mechanism

Everything is one loop, on the CLI side:

```
your command's stdout
  → spout tees it (to your terminal, the local copy, and the server)
  → the observer taps the same bytes, cheaply pre-filters them, and every
    so often asks a model: "what's happening, and is anything wrong?"
  → the model returns one small JSON observation
  → that becomes an Event: written to the local copy, pushed to the server
    (for the dashboard), and optionally exported as an OTLP span
```

Because the observer runs at the producer, it sees plaintext **before** any
Tier-2 encryption — and only the small derived events leave the machine.

## Turning it on

In `spout.yaml` (project or global):

```yaml
observe:
  enabled: true
  model:
    type: api                 # api | local | cli
    model: claude-haiku-4-5
    # token: ANTHROPIC_API_KEY # env-var NAME; this is the default
```

Put the key in `.env` (e.g. `~/.config/spout/.env`):

```
ANTHROPIC_API_KEY=sk-ant-...
```

Without a key the observer still runs but uses a built-in **stub** (no network,
no cost) that reports byte/velocity status and a heuristic run type — handy for
trying the flow. Stub summaries are prefixed `[stub]`.

### Per-run overrides

| Flag | Effect |
|---|---|
| `--observe` | Force the observer on for this run, even if config says off |
| `--no-observe` | Force it off (e.g. a sensitive run) — wins any conflict |
| `--otel <endpoint>` | Also export events as OTLP/HTTP spans to `<endpoint>` |

```bash
python train.py --lr 0.01 | spout --observe
spout run --no-observe "claude --task 'rotate the prod secrets'"
python train.py | spout --otel http://localhost:4318
```

## What it produces

Each check yields an **Observation**:

| Field | Meaning |
|---|---|
| `status` | `on_track` \| `warning` \| `failed` \| `done` |
| `summary` | one phone-readable sentence |
| `severity` | `info` \| `warn` \| `crit` |
| `run_type` | auto-classified: build / test / migration / deploy / training / data / agent / generic |
| `metrics` | numbers pulled from the output (loss, rows/s, …) |
| `detectors` | which built-ins triggered, with one-line evidence |
| `decisions` | decision-trail entries (agent runs) |

### Built-in detectors

On by default when `observe` is enabled; toggle individually under
`observe.detectors`:

| Detector | Fires when… |
|---|---|
| `classify` | (always) sets `run_type` from the output's character |
| `loop` | the process repeats the same failing action/error with no progress |
| `drift` | an agent diverges from its stated goal |
| `amnesia` | an agent re-asks/re-does settled work or drops a constraint it was following |

```yaml
observe:
  enabled: true
  detectors:
    amnesia: false   # everything else stays on
```

### Custom rules

Your own prompts ride the **same** LLM call as the built-ins (one call, not
N), so they're nearly free:

```yaml
observe:
  rules:
    - name: training-stuck
      prompt: "Alert if loss hasn't decreased for 3+ epochs."
      sources: [training, gpu]   # optional; must match stream labels
```

## Cost & cadence

The observer is cheap by design:

- A local **pre-filter** (velocity, repeated lines, error keywords, idle) gates
  LLM calls — a stable run barely calls the model; trouble calls it more.
- The model returns a `next_check_secs` hint, clamped to **10s–5m**.
- The frozen instruction block is sent behind a prompt-cache breakpoint.
- A per-run hard cap bounds total calls.

Most runs cost well under a cent.

## Privacy & the tiers

The observer is a Tier 1+ feature (it needs the CLI; Tier 0 `nc` has none).

The catch: a **cloud** model (`type: api`) *reads* your output, even on a
Tier-2 (E2E) run where the server stays blind. For private runs, either:

- use `type: local` (Ollama / any OpenAI-compatible endpoint) or `type: cli`
  to keep everything on-box, or
- `--no-observe` to skip it entirely.

`type: api` is the convenient default for development, not the Tier-2 privacy
story.

## Where events live

- **Local (canonical):** `~/.spout/local/<run>/events.jsonl` (newline-delimited
  JSON, one Event per line).
- **Server:** posted to `POST /api/run/:name/events`, served back from
  `GET /api/run/:name/events`. Stored opaquely — the server never parses them.

## Reading observations

- **Dashboard:** the run page grows a right-hand **observe panel** — status,
  metrics, triggered detectors, the decision trail, a timeline, and run
  **lineage** (sibling runs of the same command + dir).
- **CLI:** `spout observe <run>` prints the latest assessment and the timeline
  from the local copy.

```bash
spout observe exp-2
spout observe exp-2/training
```

## Run lineage

Zero-LLM, always on: Spout records each run's command, cwd, git branch, and git
commit. The dashboard groups runs that share a command + directory into a
lineage list, so re-runs with different flags (`--lr 0.01` → `--lr 0.001`) line
up automatically — experiment tracking with no setup.

## OTLP export

`--otel <endpoint>` (or `observe.otel:` in yaml) also ships each event to an
OTLP/HTTP collector as a span (`spout.observe` / `spout.synthesis`, one trace
per run), so Spout runs show up alongside your services in Datadog, Grafana
Tempo, Honeycomb, etc. No collector? It's a no-op.
