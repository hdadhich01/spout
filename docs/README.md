# Spout - Docs

Modular architecture / design reference. Each file is focused enough to be
read on its own; start here for the reading order.

For a 30-second summary, read [product.md](product.md).
For the AI agent quick-ref, read the repo-root [CLAUDE.md](../CLAUDE.md).
For the running dev log, read the repo-root [DEVELOPMENT.md](../DEVELOPMENT.md).

## Reading order

| # | Doc | What it covers |
|---|-----|---------------|
| 1 | [product.md](product.md) | What spout is. Two binaries, same codebase. |
| 2 | [cli.md](cli.md) | CLI commands, modes, flags, interactive shell. |
| 3 | [server.md](server.md) | Server architecture - local vs prod, env vars. |
| 4 | [tiers.md](tiers.md) | Encryption tiers 1a / 1b / 2, Tier 2 crypto. |
| 5 | [config.md](config.md) | **Config system (authoritative spec).** |
| 6 | [auth.md](auth.md) | Token auth, `.env` discovery, future auth. |
| 7 | [storage.md](storage.md) | FileStore, SQLiteStore, Postgres + S3. |
| 8 | [dashboard.md](dashboard.md) | Pages, status model, tier behavior. |
| 9 | [roadmap.md](roadmap.md) | Design notes + prioritized build order. |

## When editing

- Keep each doc focused on its one subject. If a doc grows past ~400 lines,
  split it.
- The config spec is authoritative - every other doc should defer to it when
  the question is "what fields exist?" / "how do they merge?".
- When a doc describes a feature that isn't implemented yet, flag it with a
  **`[planned]`** tag inline so readers can tell spec from current behavior.
- The shipped starter yaml lives at `internal/config/templates/global.yaml`
  and `internal/config/templates/project.yaml` - they are `go:embed`'d
  into the binary, so changes land in new installs as soon as the
  binary is rebuilt.
