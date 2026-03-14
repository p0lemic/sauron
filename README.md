# API Profiler

Transparent HTTP reverse proxy that sits between clients and an existing API, measuring latency, detecting anomalies, and exposing a live dashboard — with zero code changes to the upstream service.

```
Client → API Profiler (port 8080) → Your API (any port)
                  ↓
           Dashboard (port 9090)
```

## Features

- **Transparent proxy** — adds < 1 ms overhead at p99; the upstream never knows the proxy exists
- **Per-endpoint metrics** — p50 / p95 / p99 latency, RPS, error rate, request count
- **Anomaly detection** — fires alerts when latency exceeds N× the historical baseline
- **Error rate alerts** — fires when 4xx/5xx rate exceeds a configurable threshold
- **Throughput drop alerts** — fires when RPS falls below a configurable % of baseline
- **Alert silencing** — suppress alerts for a specific endpoint for a fixed duration
- **Webhook notifications** — POST alert payloads to any HTTP endpoint (Slack, PagerDuty, etc.)
- **Live dashboard** — auto-refreshing web UI with charts, histograms, request log, and status breakdown
- **Prometheus endpoint** — `GET /metrics/prometheus` for scraping with Grafana
- **Upstream health check** — periodic HEAD probe with healthy / degraded / down status
- **Path normalization** — collapses `/users/123` and `/users/456` into a single `/users/{id}` metric
- **Header rewriting** — inject or remove request headers before forwarding
- **Storage backends** — SQLite (zero-config) or PostgreSQL (production)
- **Data retention** — automatic cleanup of old records to keep the database bounded
- **Single binary** — no runtime, no Docker required; one process for the proxy, one for the dashboard

---

## Quick start

```bash
# Build both binaries
go build -o profiler ./cmd/profiler
go build -o dashboard ./cmd/dashboard

# Start the proxy (forwards to your API)
./profiler --upstream http://localhost:3000

# In another terminal, start the dashboard
./dashboard

# Open the dashboard
open http://localhost:9090
```

The proxy listens on `:8080` by default. Point your clients at `http://localhost:8080` instead of your API.

---

## Architecture

Two independent binaries share a SQLite database (or PostgreSQL in production):

| Binary | Role | Default port |
|---|---|---|
| `cmd/profiler` | Reverse proxy — intercepts requests, records metrics | `8080` |
| `cmd/dashboard` | Dashboard server — reads metrics, runs anomaly detection, serves UI | `9090` |

Both binaries can also run as a single combined process — the proxy writes to the store and the dashboard reads from it via the same file path or connection string.

---

## Configuration

Configuration is resolved in order of increasing precedence:

1. Built-in defaults
2. YAML config file (`--config`)
3. Environment variables (`PROFILER_*`)
4. CLI flags

### Proxy (`cmd/profiler`)

```bash
./profiler [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--config` | — | Path to YAML config file |
| `--upstream` | — | **Required.** Upstream base URL, e.g. `http://localhost:3000` |
| `--port` | `8080` | Port the proxy listens on |
| `--timeout` | `30s` | Upstream request timeout |
| `--tls-skip-verify` | `false` | Disable TLS certificate verification |
| `--storage-driver` | `sqlite` | Storage backend: `sqlite` or `postgres` |
| `--storage-dsn` | `profiler.db` | SQLite file path or PostgreSQL connection string |
| `--retention` | `0` (disabled) | Delete records older than this, e.g. `7d`, `24h` |

### Dashboard (`cmd/dashboard`)

```bash
./dashboard [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--config` | — | Path to YAML config file |
| `--listen` | `:9090` | Dashboard listen address |
| `--upstream` | — | Upstream URL for health check (optional) |
| `--storage-driver` | `sqlite` | Storage backend: `sqlite` or `postgres` |
| `--storage-dsn` | `profiler.db` | SQLite file path or PostgreSQL connection string |
| `--metrics-window` | `30m` | Aggregation window for current metrics |
| `--baseline-windows` | `5` | Number of past windows used for baseline |
| `--anomaly-threshold` | `3.0` | Latency anomaly multiplier (fires when p99 > N× baseline) |
| `--webhook-url` | — | URL to POST alert notifications to |
| `--error-rate-threshold` | `0` (disabled) | Error rate % to trigger alert, e.g. `10.0` |
| `--throughput-drop-threshold` | `0` (disabled) | Minimum RPS % of baseline before alerting, e.g. `50.0` |

### YAML config

Copy the example files and adjust:

```bash
cp profiler.yaml.example profiler.yaml
cp dashboard.yaml.example dashboard.yaml
```

**`profiler.yaml`** — full reference:

```yaml
upstream: http://localhost:3000
port: 8080
timeout: 30s
tls_skip_verify: false

storage:
  driver: sqlite        # "sqlite" | "postgres"
  dsn: profiler.db

retention: 7d           # delete records older than 7 days (0 = disabled)

normalize_paths: true   # collapse /users/123 → /users/{id}

path_rules:             # custom normalization rules (applied before built-ins)
  - pattern: "^/v[0-9]+/"
    replacement: "/v{version}/"

header_rules:           # rewrite headers on every proxied request
  - action: set
    header: X-Forwarded-By
    value: api-profiler
  - action: remove
    header: X-Internal-Token
```

**`dashboard.yaml`** — full reference:

```yaml
storage:
  driver: sqlite
  dsn: profiler.db

api_addr: ":9090"
metrics_window: 30m
baseline_windows: 5
anomaly_threshold: 3.0

webhook_url: "https://hooks.slack.com/services/xxx"
error_rate_threshold: 10.0      # alert when error rate > 10%
throughput_drop_threshold: 50.0 # alert when RPS < 50% of baseline

health_check:
  enabled: true
  path: /health
  interval: 10s
  timeout: 5s
  threshold: 3    # consecutive failures to mark upstream as "down"
```

### Environment variables

All config fields can be set via `PROFILER_*` environment variables, useful for containers:

| Variable | Equivalent flag |
|---|---|
| `PROFILER_UPSTREAM` | `--upstream` |
| `PROFILER_PORT` | `--port` |
| `PROFILER_TIMEOUT` | `--timeout` |
| `PROFILER_TLS_SKIP_VERIFY` | `--tls-skip-verify` (`true` or `1`) |
| `PROFILER_STORAGE_DRIVER` | `--storage-driver` |
| `PROFILER_STORAGE_DSN` | `--storage-dsn` |
| `PROFILER_LISTEN` | `--listen` |
| `PROFILER_METRICS_WINDOW` | `--metrics-window` |
| `PROFILER_BASELINE_WINDOWS` | `--baseline-windows` |
| `PROFILER_ANOMALY_THRESHOLD` | `--anomaly-threshold` |
| `PROFILER_WEBHOOK_URL` | `--webhook-url` |

---

## Storage

### SQLite (default)

No setup needed. Both binaries point at the same file:

```yaml
# profiler.yaml and dashboard.yaml
storage:
  driver: sqlite
  dsn: ./profiler.db
```

### PostgreSQL

```yaml
storage:
  driver: postgres
  dsn: "postgres://user:pass@localhost:5432/profiler?sslmode=disable"
```

Schema is applied automatically on first start (`CREATE TABLE IF NOT EXISTS`).

---

## Dashboard

Open `http://localhost:9090` after starting the dashboard binary.

| Section | Description |
|---|---|
| **Summary** | Total requests, global error rate, global p99, active endpoint count |
| **Endpoints** | Sortable/filterable table with p50/p95/p99, RPS, error rate per endpoint |
| **Latency chart** | Click any endpoint row to expand a 60-minute p99 time series |
| **Histogram** | Latency distribution by bucket for the selected endpoint |
| **Status breakdown** | Request counts and percentages by 2xx / 3xx / 4xx / 5xx |
| **Slowest requests** | Top 10 individual slowest requests |
| **Request log** | Paginated log of recent requests with method/path/status/duration filters |
| **Alerts** | Active anomaly alerts with kind, endpoint, value, and threshold |

The time range bar at the top lets you switch between live mode and historical windows (15m, 1h, 6h, 24h, 7d, or custom range).

---

## Alerts

The dashboard evaluates conditions every 10 seconds. Alerts are auto-resolved when the condition clears.

### Alert kinds

| Kind | Triggers when |
|---|---|
| `latency` | p99 > `anomaly_threshold` × baseline p99 |
| `error_rate` | error rate > `error_rate_threshold` % |
| `throughput` | current RPS < `throughput_drop_threshold` % of baseline RPS |

### Webhook payload

When `webhook_url` is set, each new alert fires a `POST` with JSON:

```json
{
  "kind": "latency",
  "method": "GET",
  "path": "/users",
  "current_p99": 1540.2,
  "baseline_p99": 45.1,
  "threshold": 3.0,
  "triggered_at": "2026-03-14T12:00:00Z"
}
```

For `error_rate` alerts:

```json
{
  "kind": "error_rate",
  "method": "POST",
  "path": "/orders",
  "error_rate": 23.5,
  "error_rate_threshold": 10.0,
  "triggered_at": "2026-03-14T12:01:00Z"
}
```

### Silencing

Suppress all alerts for an endpoint for a fixed duration:

```bash
curl -X POST http://localhost:9090/alerts/silence \
  -H 'Content-Type: application/json' \
  -d '{"method":"GET","path":"/users","duration":"1h"}'
```

---

## REST API

All endpoints are served by the dashboard on port `9090`.

### Metrics

| Endpoint | Description |
|---|---|
| `GET /metrics/summary` | Global stats: total requests, error rate, p99, active endpoints |
| `GET /metrics/table` | Per-endpoint stats: p50/p95/p99, RPS, error rate, count |
| `GET /metrics/endpoints` | Per-endpoint latency percentiles (supports `?window=` override) |
| `GET /metrics/slowest?n=10` | Top N endpoints by p99 |
| `GET /metrics/errors` | Error rate per endpoint |
| `GET /metrics/throughput` | Current and average RPS per endpoint |
| `GET /metrics/baseline` | Historical baseline p99 and RPS per endpoint |
| `GET /metrics/latency?method=GET&path=/users` | 60-minute p99 time series for one endpoint |
| `GET /metrics/histogram?method=GET&path=/users` | Latency distribution buckets for one endpoint |
| `GET /metrics/status` | Request counts by status class (2xx/3xx/4xx/5xx) |
| `GET /metrics/status?method=GET&path=/users` | Status breakdown for one endpoint |
| `GET /metrics/requests?n=100` | Recent request log |
| `GET /metrics/slowest-requests?n=10` | Top N slowest individual requests |
| `GET /metrics/prometheus` | Prometheus text exposition format (all gauges) |

All endpoints that aggregate over time accept optional `?from=<RFC3339>&to=<RFC3339>` query params for historical queries.

### Alerts

| Endpoint | Method | Description |
|---|---|---|
| `/alerts/active` | GET | Currently active alerts |
| `/alerts/history` | GET | All alert records (includes resolved) |
| `/alerts/silence` | POST | Create a silence for an endpoint |
| `/alerts/silences` | GET | List active silences |

### Health

| Endpoint | Description |
|---|---|
| `GET /health` | `{"status":"ok"}` or `{"status":"ok","upstream":{...}}` when health check is enabled |

---

## Path normalization

Enabled by default (`normalize_paths: true`). Dynamic path segments are automatically collapsed:

| Pattern | Normalized to |
|---|---|
| `/users/123` | `/users/{id}` |
| `/orders/abc-def-123` | `/orders/{id}` |
| `/items/a1b2c3d4` (hex) | `/items/{id}` |

Custom rules run before the built-in detection:

```yaml
path_rules:
  - pattern: "^/api/v[0-9]+/(.*)"
    replacement: "/api/v{n}/$1"
```

Disable normalization entirely:

```yaml
normalize_paths: false
```

---

## Deployment

### Docker (single-host)

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o profiler ./cmd/profiler && go build -o dashboard ./cmd/dashboard

FROM alpine:3.20
COPY --from=builder /app/profiler /app/dashboard /usr/local/bin/
```

```yaml
# docker-compose.yml
services:
  profiler:
    image: api-profiler
    command: profiler --upstream http://api:3000 --storage-dsn /data/profiler.db
    ports: ["8080:8080"]
    volumes: ["data:/data"]

  dashboard:
    image: api-profiler
    command: dashboard --storage-dsn /data/profiler.db --listen :9090
    ports: ["9090:9090"]
    volumes: ["data:/data"]

volumes:
  data:
```

### Production with PostgreSQL

```bash
# Proxy sidecar
PROFILER_UPSTREAM=http://api-service:3000 \
PROFILER_STORAGE_DRIVER=postgres \
PROFILER_STORAGE_DSN="postgres://profiler:secret@db:5432/profiler" \
./profiler --retention 30d

# Dashboard
./dashboard \
  --storage-driver postgres \
  --storage-dsn "postgres://profiler:secret@db:5432/profiler" \
  --metrics-window 30m \
  --anomaly-threshold 3.0 \
  --webhook-url https://hooks.slack.com/services/xxx \
  --error-rate-threshold 5.0
```

---

## Development

```bash
# Run all tests
go test ./...

# Run tests for a specific package
go test ./metrics/... -v

# Build both binaries
go build ./cmd/profiler ./cmd/dashboard
```

The project follows **Spec Driven Development (SDD)**: each feature has a spec in `specs/` that is reviewed and approved before implementation. See `specs/` for the full history.

---

## License

MIT
