# US-42 — Prometheus metrics endpoint

## Context

API Profiler exposes rich per-endpoint metrics (latency percentiles, error rate, RPS)
through its own JSON API. Many infrastructure teams already run Prometheus + Grafana
and want to scrape API Profiler directly without adapting their tooling to a custom
JSON format. The PRD (section 4.4 Operabilidad) explicitly lists
`GET /metrics/prometheus` as a required operability endpoint.

## Behavior

The dashboard server exposes `GET /metrics/prometheus` that responds with a
Prometheus text-format (exposition format v0.0.4) response. The content-type header
is `text/plain; version=0.0.4; charset=utf-8`.

Metrics exported, all prefixed with `apiprofiler_`:

| Metric name | Type | Labels | Description |
|---|---|---|---|
| `apiprofiler_request_duration_p50_ms` | gauge | `method`, `path` | P50 latency in ms for current window |
| `apiprofiler_request_duration_p95_ms` | gauge | `method`, `path` | P95 latency in ms for current window |
| `apiprofiler_request_duration_p99_ms` | gauge | `method`, `path` | P99 latency in ms for current window |
| `apiprofiler_request_error_rate` | gauge | `method`, `path` | Error rate percentage (0–100) for current window |
| `apiprofiler_request_rps_current` | gauge | `method`, `path` | Current RPS for the active window |
| `apiprofiler_request_total` | gauge | `method`, `path` | Total request count recorded in current window |
| `apiprofiler_active_alerts_total` | gauge | `kind` | Number of currently active alerts per kind |

Each metric includes a `# HELP` line and a `# TYPE` line. Metric names and labels
follow Prometheus naming conventions (lowercase, underscores).

Label values (method, path) must be sanitized: characters not in `[a-zA-Z0-9_]`
replaced with `_`, but path values are kept as-is since Prometheus supports arbitrary
label values.

No external `prometheus/client_golang` library is used — the endpoint is implemented
with simple `fmt.Fprintf` writes to produce valid exposition format text.

## API contract

```
GET /metrics/prometheus
Response: 200 OK
Content-Type: text/plain; version=0.0.4; charset=utf-8

# HELP apiprofiler_request_duration_p50_ms P50 request latency in milliseconds
# TYPE apiprofiler_request_duration_p50_ms gauge
apiprofiler_request_duration_p50_ms{method="GET",path="/users"} 12.3
apiprofiler_request_duration_p50_ms{method="POST",path="/orders"} 45.1
# HELP apiprofiler_request_duration_p99_ms P99 request latency in milliseconds
# TYPE apiprofiler_request_duration_p99_ms gauge
apiprofiler_request_duration_p99_ms{method="GET",path="/users"} 98.7
...
# HELP apiprofiler_active_alerts_total Number of currently active alerts
# TYPE apiprofiler_active_alerts_total gauge
apiprofiler_active_alerts_total{kind="latency"} 1
apiprofiler_active_alerts_total{kind="error_rate"} 0
apiprofiler_active_alerts_total{kind="throughput"} 0
```

If there are no endpoints (no traffic yet), the per-endpoint metric families are
still emitted with `# HELP` and `# TYPE` but with zero metric lines.

## Test cases

TC-01 **Happy path — single endpoint**: insert one TableRow (GET /users, p50=10,
p95=20, p99=30, error_rate=5.0, rps_current=2.5, count=100), call
`GET /metrics/prometheus`, verify status 200, `Content-Type` contains
`text/plain`, and the body contains:
- `apiprofiler_request_duration_p99_ms{method="GET",path="/users"} 30`
- `apiprofiler_request_error_rate{method="GET",path="/users"} 5`
- `apiprofiler_request_rps_current{method="GET",path="/users"} 2.5`

TC-02 **HELP and TYPE lines**: verify the body contains `# HELP apiprofiler_request_duration_p99_ms`
and `# TYPE apiprofiler_request_duration_p99_ms gauge`.

TC-03 **Multiple endpoints**: insert two different endpoints and verify both appear
as separate label sets in the output.

TC-04 **Active alerts counter**: configure one active `latency` alert, verify
`apiprofiler_active_alerts_total{kind="latency"} 1` and `kind="error_rate"` and
`kind="throughput"` are 0.

TC-05 **No traffic**: with no data in the engine, verify status 200, correct headers,
and no per-endpoint metric lines (only HELP/TYPE lines).

TC-06 **Path with special chars**: endpoint path `/api/v1/users/{id}` appears
correctly as a label value (curly braces are valid label value characters).

## Out of scope

- Prometheus histogram/summary types (only gauges for now)
- Histogram buckets in Prometheus format
- Runtime Go metrics (goroutines, GC, etc.)
- Authentication for the scrape endpoint
- Configurable metric name prefix

## Dependencies

- US-07 metrics.Engine (Table() method)
- US-22 alerts.Detector (Active() method)
- US-18 api.Server (new handler registered on mux)
