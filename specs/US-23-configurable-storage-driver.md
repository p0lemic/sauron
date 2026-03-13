# US-23 — Configurable Storage Driver (SQLite / PostgreSQL)

## Context

Currently the profiler writes metrics to an embedded SQLite database. To support ECS sidecar
deployment, multiple task instances must write to a shared store so the dashboard can aggregate
metrics across all of them. PostgreSQL (RDS/Aurora) is the target shared store.

The storage driver must be selectable via config without changing any layer above `storage/`.

## Behavior

### 1. Config: new `storage` block
YAML accepts a new top-level `storage` key with two sub-fields:

```yaml
storage:
  driver: postgres                          # "sqlite" (default) | "postgres"
  dsn:    "postgres://user:pass@host/db"    # path for sqlite, connstring for postgres
```

Backward compatibility: the existing top-level `db_path` key continues to work and is equivalent
to `storage.driver: sqlite` + `storage.dsn: <value>`. If both `db_path` and `storage` are
provided, `storage` wins.

Defaults (unchanged behavior):
- `driver` → `"sqlite"`
- `dsn`    → `"profiler.db"`

### 2. Validation rules
- `driver` must be `"sqlite"` or `"postgres"` (case-sensitive). Any other value is an error.
- `driver: postgres` with empty `dsn` is an error.
- `driver: sqlite` with empty `dsn` falls back to default `"profiler.db"`.

### 3. Factory function
Replace `storage.New(dbPath string)` with:

```go
func Open(driver, dsn string) (StoreReader, error)
```

`Open` dispatches to the appropriate internal constructor. Callers (`cmd/profiler/main.go`) update
to use `Open(cfg.StorageDriver, cfg.StorageDSN)`.

The old `New` is removed (not aliased — it is only called from main, no other package uses it).

### 4. SQLite driver (unchanged behaviour)
- Uses `modernc.org/sqlite` (already in go.mod, pure Go, no CGo)
- Schema, queries, and placeholder style (`?`) unchanged
- Constructor: `openSQLite(dsn string) (StoreReader, error)` (internal)

### 5. PostgreSQL driver
- Dependency: `github.com/jackc/pgx/v5` with its `stdlib` adapter (`pgx/v5/stdlib`), used via
  the standard `database/sql` interface — same pattern as the SQLite driver.
- Schema (equivalent to SQLite):

```sql
CREATE TABLE IF NOT EXISTS requests (
    id          BIGSERIAL    PRIMARY KEY,
    timestamp   TIMESTAMPTZ  NOT NULL,
    method      TEXT         NOT NULL,
    path        TEXT         NOT NULL,
    status_code INTEGER      NOT NULL,
    duration_ms DOUBLE PRECISION NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_requests_path_method ON requests (method, path);
CREATE INDEX IF NOT EXISTS idx_requests_timestamp   ON requests (timestamp);
```

- `Save`: uses `$1…$5` placeholders; stores `timestamp` as native `TIMESTAMPTZ` (no string encoding).
- `FindByWindow`: same half-open `[from, to)` semantics; scans `TIMESTAMPTZ` directly into
  `time.Time` (pgx handles this natively).
- `Close`: calls `db.Close()`.

### 6. No changes above `storage/`
`metrics.Engine`, `alerts.Detector`, `proxy.Handler`, and `api.Server` do not change.
Only `cmd/profiler/main.go` updates the constructor call.

## API contract

No HTTP API changes.

## Config struct changes

```go
// New fields in Config:
StorageDriver string  // "sqlite" | "postgres" — default "sqlite"
StorageDSN    string  // path for sqlite, connstring for postgres — default "profiler.db"

// DBPath kept for backward compat, but deprecated:
DBPath string  // maps to StorageDSN when StorageDriver == "sqlite"
```

YAML struct addition:

```go
type yamlStorage struct {
    Driver string `yaml:"driver"`
    DSN    string `yaml:"dsn"`
}
// top-level yamlFile gains:
Storage yamlStorage `yaml:"storage"`
```

## Test cases

### Config (config/config_test.go)

| TC  | Description                                                                 |
|-----|-----------------------------------------------------------------------------|
| TC-01 | Default(): StorageDriver="sqlite", StorageDSN="profiler.db"               |
| TC-02 | YAML `storage.driver: postgres` + `storage.dsn: postgres://h/db` parsed   |
| TC-03 | YAML `db_path: /tmp/x.db` → StorageDriver="sqlite", StorageDSN="/tmp/x.db"|
| TC-04 | Both `db_path` and `storage` present → `storage` wins                     |
| TC-05 | Validate: driver="mysql" → error contains "unsupported storage driver"     |
| TC-06 | Validate: driver="postgres", dsn="" → error contains "storage dsn required"|
| TC-07 | Validate: driver="sqlite", dsn="" → coerced to "profiler.db", no error    |
| TC-08 | Merge: overrides.StorageDriver="postgres" replaces base "sqlite"           |

### Storage factory (storage/open_test.go)

| TC  | Description                                                                 |
|-----|-----------------------------------------------------------------------------|
| TC-09 | Open("sqlite", ":memory:") → returns non-nil StoreReader, no error        |
| TC-10 | Open("postgres", "invalid-dsn") → returns error (ping fails)              |
| TC-11 | Open("unknown", "") → returns error "unsupported driver"                  |
| TC-12 | Open("sqlite", ":memory:"): Save + FindByWindow round-trip works          |

### PostgreSQL store (storage/postgres_test.go) — integration, skipped without real DB

Integration tests are gated with:
```go
dsn := os.Getenv("TEST_POSTGRES_DSN")
if dsn == "" {
    t.Skip("TEST_POSTGRES_DSN not set")
}
```

| TC  | Description                                                                 |
|-----|-----------------------------------------------------------------------------|
| TC-13 | Save persists record; FindByWindow retrieves correct fields                |
| TC-14 | FindByWindow boundary: record at `from` included, record at `to` excluded  |
| TC-15 | FindByWindow empty window → empty slice (not nil)                          |
| TC-16 | Close is idempotent (double close returns nil)                             |
| TC-17 | migrate() is idempotent (schema applied twice → no error)                 |

## Out of scope

- Migration tool between SQLite and PostgreSQL data
- Connection pool tuning (max_open_conns, max_idle_conns)
- Read replicas or connection routing
- SSL/TLS config for PostgreSQL beyond what the DSN encodes
- Retention / TTL enforcement (future US)

## Dependencies

- New: `github.com/jackc/pgx/v5` (production dep — PostgreSQL driver)
- Existing: `modernc.org/sqlite` (unchanged)
- Build: `go mod tidy` required after adding pgx
