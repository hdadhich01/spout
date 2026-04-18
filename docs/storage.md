# Storage

Raw stream bytes and metadata have different access patterns and get stored
in different backends:

| Data | Access pattern | Best fit |
|---|---|---|
| Stream bytes (`data.raw`) | Append-only, sequential read, never queried | Files / object store |
| Metadata (name, mode, dir, host, exit code, ...) | Random read/write, filtered queries, sort | KV / SQL |

Every production logging system (Loki, Datadog, Tempo, GitHub Actions,
Vercel) separates these. Spout follows the same pattern.

## Storage interface `[planned]`

```go
type Store interface {
    Create(meta RunMeta) (*Session, error)
    Get(name string) *Session
    List(filter Filter) []*Session
    Rename(old, new string) error
    Delete(name string) error
    Status() string   // backend identifier
}

type Session interface {
    Write(data []byte)
    Subscribe() (chan []byte, []byte, bool)   // history + live
    Close()
    Status() string
}
```

Three implementations behind the same interface.

## 1. FileStore (default, current)

```
~/.config/spout/storage/
└── wolf-a3f2/
    ├── meta.json     (metadata, small)
    └── data.raw      (raw stream, append-only)
```

- Used by: CLI local archive, self-hosted single-server, dev
- Pros: zero deps, atomic per-session, easy backup/restore, no setup
- Cons: file descriptor limits (~1000 concurrent sessions), `List` is
  O(n) directory scan
- Status: current implementation

## 2. SQLiteStore (self-hosted at scale) `[planned]`

```
~/.config/spout/spout.db         ← SQLite (metadata only, WAL mode)
~/.config/spout/storage/<name>/
    └── data.raw                 ← raw stream (still files)
```

```sql
CREATE TABLE sessions (
    name        TEXT PRIMARY KEY,
    mode        TEXT,
    command     TEXT,
    dir         TEXT,
    host        TEXT,
    user        TEXT,
    git_branch  TEXT,
    started_at  INTEGER,
    ended_at    INTEGER,
    bytes_recv  INTEGER,
    lines       INTEGER,
    exit_code   INTEGER,
    has_exit    INTEGER,
    active      INTEGER
);
CREATE INDEX idx_started_at ON sessions(started_at DESC);
CREATE INDEX idx_dir        ON sessions(dir);
CREATE INDEX idx_active     ON sessions(active);
```

- Used by: self-hosted with 100s-1000s of sessions, filtered queries
- Pros: pure Go (`modernc.org/sqlite`, no CGo), fast `WHERE dir=? AND active=1`,
  WAL handles concurrent reads, single-file backup
- Cons: single-server only (file lock)
- Migration from FileStore: walk each `meta.json`, insert into the table

## 3. PostgresStore + S3 (prod hosted) `[planned]`

```
PostgreSQL              ← metadata (multi-server, replicated)
S3 / R2 / object store  ← raw stream (cheap blob storage)
```

Postgres schema = SQLite schema + `user_id`, `tier`, `expires_at`.

Streams flow to object storage:

- During the run, server buffers in memory and flushes to S3 in 1MB chunks
- One S3 object per session: `runs/<user_id>/<name>/data.raw`
- On viewer connect, server streams bytes straight from S3
- Cheap (~$0.023/GB/mo on S3, less on R2, $0 egress on R2)

- Used by: `spout.sh` production, multi-server deployments
- Pros: horizontally scalable (shared DB + blob store), cheap long-term
  storage, multi-region with S3 replication
- Cons: more moving parts, S3 read latency on viewer connect

## CLI local archive (always on)

Regardless of which remote server the CLI talks to, **the CLI always saves
runs locally** to `~/.config/spout/storage/` via FileStore. So:

- Streaming to `spout.sh` still leaves a local copy
- `spout.sh` TTL (14d) doesn't destroy your local history
- `spout ls` and bare `spout` merge local + server views
- No data loss on server failure

Local store is independent of the remote store; CLI writes to both in parallel.

## Configuration

```bash
# Default: file store
spout server

# SQLite for self-hosted at scale
SPOUT_STORE=sqlite spout server

# Postgres + S3 for prod
SPOUT_STORE=postgres SPOUT_DB=postgres://... SPOUT_S3=s3://bucket spout server
```

CLI doesn't configure a remote backend - it just talks WebSocket. Its local
archive path is the `storage` field in the global `spout.yaml` (default
`~/.config/spout/storage/`).

> **Naming note:** the yaml `storage:` (CLI-side, a path) and the
> `SPOUT_STORE` env var (server-side, a backend kind) are different things
> in different scopes. The yaml field never refers to a backend; the env
> var never refers to a path.

## Trade-offs by deployment

| Deployment | Sessions | Backend | Why |
|---|---|---|---|
| Local CLI archive | ~100 | FileStore | Zero deps, your machine |
| Personal home server | ~1k | FileStore | Simple, fast enough |
| Team self-hosted | ~10k | SQLiteStore | Fast queries, single binary |
| Enterprise self-hosted | ~100k | SQLiteStore | Same |
| `spout.sh` hosted | millions | Postgres + S3 | Horizontally scalable |

## Migration paths

- FileStore ↔ SQLiteStore: rebuild SQLite from filesystem walk
- FileStore → Postgres: same, plus upload each `data.raw` to S3
- SQLite → Postgres: SQL dump + import + S3 upload

All one-way scripts the operator runs once. The server binary only ever
speaks to one backend at a time, determined by env var.
