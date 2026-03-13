# US-24 — Separate Proxy and Dashboard Binaries

## Context

Currently `cmd/profiler` runs everything in a single process: proxy, recorder, metrics engine,
alerts detector, and dashboard HTTP server. For ECS sidecar deployment we need:

- A **sidecar container** (`cmd/profiler`) that only forwards requests and writes metrics to a
  shared database. No dashboard, no metrics API.
- A **separate service** (`cmd/dashboard`) that reads from the same database, runs the metrics
  engine and alerts detector, and serves the dashboard + metrics API.

Both binaries share the same `storage` config block (US-23) so they can point to the same SQLite
file in development or the same PostgreSQL instance in production/ECS.

## Behavior

### `cmd/profiler` (proxy sidecar — simplified)

Responsibilities after this US: **proxy + recorder only**.

- Opens storage with `storage.Open(cfg.StorageDriver, cfg.StorageDSN)`
- Starts the reverse proxy on `cfg.Port`
- Records every request via `storage.Recorder`
- On SIGTERM/SIGINT: graceful shutdown (same 30 s budget as today)
- **Does NOT** start `metrics.Engine`, `alerts.Detector`, or `api.Server`

CLI flags retained:
```
--config            path to YAML config file
--upstream          upstream base URL (required)
--port              proxy listen port (default: 8080)
--timeout           upstream request timeout (default: 30s)
--tls-skip-verify   disable TLS certificate verification
--storage-driver    "sqlite" | "postgres" (default: sqlite)
--storage-dsn       db path or connection string (default: profiler.db)
```

Flags removed from `cmd/profiler`:
`--db-path`, `--api-addr`, `--metrics-window`, `--baseline-windows`,
`--anomaly-threshold`, `--webhook-url`

(`--db-path` was a legacy alias for `--storage-dsn`; it is removed in favour of the explicit
`--storage-driver` / `--storage-dsn` pair introduced in US-23.)

### `cmd/dashboard` (new binary)

New entry point at `cmd/dashboard/main.go`.

Responsibilities: **read storage + metrics engine + alerts detector + api.Server**.

- Opens storage (read-only path) with `storage.Open(cfg.StorageDriver, cfg.StorageDSN)`
- Creates `metrics.Engine` and `alerts.Detector`
- Starts `api.Server` on `cfg.APIAddr` (serves dashboard HTML, metrics API, alerts API)
- On SIGTERM/SIGINT: graceful shutdown (30 s budget)
- **Does NOT** start a proxy

CLI flags:
```
--config             path to YAML config file
--listen             dashboard listen address (default: :9090)
                     maps to cfg.APIAddr
--storage-driver     "sqlite" | "postgres" (default: sqlite)
--storage-dsn        db path or connection string (default: profiler.db)
--metrics-window     metrics aggregation window (default: 30m)
--baseline-windows   number of windows for baseline (default: 5)
--anomaly-threshold  std dev multiplier for anomalies (default: 3.0)
--webhook-url        URL to POST alert notifications to (optional)
```

### Config

Both binaries reuse the existing `Config` struct. No new fields are needed.

`config` package gains one new function:

```go
// ValidateDashboard validates only the fields relevant to the dashboard binary.
func ValidateDashboard(cfg Config) error
```

Validates: `StorageDriver`, `StorageDSN`, `APIAddr`, `MetricsWindow` > 0,
`BaselineWindows` >= 1, `AnomalyThreshold` > 0, optional `WebhookURL`.
Does NOT check `Upstream`, `Port`, `Timeout`, `TLSSkipVerify`.

`config.ValidateProxy(cfg Config) error` is added as an alias / renamed form of the current
`config.Validate` that only checks proxy fields (and storage fields). The existing
`config.Validate` is kept as-is for backward compatibility with existing tests.

### Deployment examples

**Development (shared SQLite)**

```bash
# Terminal 1 — proxy
./profiler --upstream http://localhost:80 --storage-dsn ./dev.db

# Terminal 2 — dashboard
./dashboard --storage-dsn ./dev.db --listen :9090
```

**ECS (shared PostgreSQL)**

Sidecar task definition environment:
```
UPSTREAM=http://localhost:80
STORAGE_DRIVER=postgres
STORAGE_DSN=postgres://user:pass@rds-host/profiler
```

Dashboard service environment:
```
STORAGE_DRIVER=postgres
STORAGE_DSN=postgres://user:pass@rds-host/profiler
LISTEN=:9090
```

### YAML config for dashboard (`dashboard.yaml.example`)

```yaml
# Storage to read metrics from.
storage:
  driver: postgres                        # or "sqlite" for local dev
  dsn: "postgres://user:pass@host/db"     # or "profiler.db" for local dev

# Listen address for the dashboard HTTP server.
api_addr: ":9090"

# Metrics and alerting config.
metrics_window: 30m
baseline_windows: 5
anomaly_threshold: 3.0
webhook_url: ""   # optional
```

## API contract

No changes to HTTP endpoints. `api.Server` is unchanged.

## Test cases

### config (config/config_test.go)

| TC  | Description                                                                    |
|-----|--------------------------------------------------------------------------------|
| TC-01 | ValidateDashboard: valid config → no error                                   |
| TC-02 | ValidateDashboard: missing StorageDSN for postgres → error "storage dsn"     |
| TC-03 | ValidateDashboard: MetricsWindow=0 → error "metrics_window"                  |
| TC-04 | ValidateDashboard: BaselineWindows=0 → error "baseline_windows"              |
| TC-05 | ValidateDashboard: AnomalyThreshold=0 → error "anomaly_threshold"            |
| TC-06 | ValidateDashboard: invalid WebhookURL → error "webhook_url"                  |
| TC-07 | ValidateDashboard: does NOT require Upstream field                            |

### cmd/profiler (integration smoke test)

| TC  | Description                                                                    |
|-----|--------------------------------------------------------------------------------|
| TC-08 | Profiler binary starts with valid config, records a request, shuts down      |

### cmd/dashboard (integration smoke test)

| TC  | Description                                                                    |
|-----|--------------------------------------------------------------------------------|
| TC-09 | Dashboard binary starts, GET /health returns 200, shuts down                 |
| TC-10 | Dashboard binary opens SQLite from --storage-dsn and serves /metrics/summary |

## Files changed

| File                             | Change                                             |
|----------------------------------|----------------------------------------------------|
| `cmd/profiler/main.go`           | Remove engine/detector/api.Server; add `--storage-driver`/`--storage-dsn` flags; remove legacy flags |
| `cmd/dashboard/main.go`          | New file                                           |
| `cmd/dashboard/main_test.go`     | New file — TC-09, TC-10                            |
| `config/config.go`               | Add `ValidateDashboard`                            |
| `config/config_test.go`          | Add TC-01..TC-07                                   |
| `profiler.yaml.example`          | Update with storage block, remove db_path          |
| `dashboard.yaml.example`         | New example config file for dashboard binary       |

No changes to: `api/`, `metrics/`, `alerts/`, `storage/`, `proxy/`.

## Out of scope

- Environment variable support (12-factor style `STORAGE_DSN=...`) — future US
- Docker / ECS task definition files
- Health check endpoint on the proxy binary
- Prometheus metrics export
