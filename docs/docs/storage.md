# Storage backend

AgentTier's source of truth is Kubernetes — Sandboxes, templates, warm-pool
config, and governance policies all live in etcd, and lifecycle transitions are
Kubernetes Events. That model needs no external database and is what runs by
default.

The **optional SQL storage backend** is an opt-in sink for *historical records*
that need to outlive a Sandbox object — Kubernetes Events are garbage-collected
after about an hour, so they can't answer "who created which sandbox last
month?". When enabled, the Router persists three record types:

- **Audit events** — user/operator actions (`create`, `stop`, `share`, `exec`).
- **Sandbox events** — lifecycle transitions (phase, reason, message).
- **Cost snapshots** — point-in-time CPU / memory / estimated-USD samples.

!!! note "Best-effort by design"
    Recording is always best-effort. A backend error is logged and **never**
    blocks the request it describes. If the database is down, sandboxes keep
    being created, stopped, and shared exactly as before — you just lose the
    historical row. This is intentional graceful degradation: the SQL store is
    a reporting convenience, not a control-plane dependency.

## Drivers

| Driver | Notes |
| --- | --- |
| `sqlite` (bundled) | Pure-Go ([`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite)) — no CGO, works on every platform. Persists to a PVC. Good for single-replica installs and evaluation. |
| Postgres / MySQL | Bring-your-own. Point the Router at an external database you already operate. Requires wiring a driver in `cmd/router` (see below). |

## Enabling the bundled SQLite backend

```bash
helm upgrade --install agenttier agenttier/agenttier \
  --namespace agenttier \
  --set optional.storage.enabled=true
```

That provisions a small `ReadWriteOnce` PVC (`<release>-router-storage`,
default `1Gi`) mounted at the parent directory of the database path, and sets
`STORAGE_SQLITE_PATH` on the Router so it opens the store at startup.

```yaml
optional:
  storage:
    enabled: true
    driver: sqlite
    sqlite:
      path: /var/lib/agenttier/agenttier.db
      persistence:
        enabled: true        # set false for ephemeral testing (data lost on restart)
        size: 1Gi
        storageClassName: ""  # empty = cluster default StorageClass
```

Because SQLite is a single-writer store on a `ReadWriteOnce` volume, keep the
Router at a single replica when using it. For multi-replica Routers, use an
external Postgres backend.

## Using an external Postgres / MySQL database

The backend is built on `database/sql`, so any driver works. The bundled binary
only registers the SQLite driver to keep the image small and CGO-free; to use
Postgres or MySQL, import the driver in `cmd/router/main.go` and construct the
backend from your DSN:

```go
import _ "github.com/jackc/pgx/v5/stdlib" // or the mysql driver

db, err := sql.Open("pgx", os.Getenv("STORAGE_DSN"))
if err == nil {
    store, err := storage.NewSQLBackend(ctx, db, storage.DialectPostgres)
    if err == nil {
        config.StorageBackend = store
    }
}
```

`NewSQLBackend` initialises the schema (idempotent `CREATE TABLE IF NOT
EXISTS`) and handles placeholder rebinding for the dialect (`$1, $2, …` for
Postgres). Inject the DSN from a Kubernetes Secret — never inline credentials in
values.

## Reading the data

The schema is intentionally plain — three tables (`audit_events`,
`sandbox_events`, `cost_snapshots`) with RFC3339 timestamps — so you can point
any BI or SQL tool at it. `ListAuditEvents` supports filtering by actor,
sandbox, and time window for programmatic access.

## Disabling

Set `optional.storage.enabled=false` (the default). The Router falls back to a
no-op backend, the PVC and env var are not rendered, and Kubernetes remains the
sole source of truth.
