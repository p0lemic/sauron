# US-29 — Top N Slowest Requests

## Context

El dashboard ya muestra los endpoints más lentos por P99 agregado (US-09), pero no permite
ver qué requests individuales fueron las más lentas. Esto facilita el debugging de outliers:
identificar exactamente qué request concreta tardó 8 segundos, a qué hora y con qué status.

## Behavior

### 1. Nuevos métodos en `metrics.Engine`

```go
// SlowestRequests returns the n slowest individual requests in the engine's window,
// sorted by duration_ms descending.
func (e *Engine) SlowestRequests(n int) ([]storage.Record, error)

// SlowestRequestsForRange returns the n slowest individual requests in [from, to),
// sorted by duration_ms descending.
func (e *Engine) SlowestRequestsForRange(from, to time.Time, n int) ([]storage.Record, error)
```

- Usa `FindByWindow` + sort in-memory por `duration_ms` DESC.
- `n` se coarta en `[1, 100]` internamente.
- Devuelve slice vacío (no nil) si no hay registros.

### 2. Nuevo endpoint HTTP

```
GET /metrics/slowest-requests?n=10&from=<RFC3339>&to=<RFC3339>
```

- `n`: número de registros, default `10`, máx `100`.
- `from` / `to`: opcionales, mismo comportamiento que `parseTimeRange`.
- Respuesta: `[]storage.Record` serializado a JSON.

```json
[
  {
    "timestamp": "2026-03-14T12:00:01.234Z",
    "method":    "POST",
    "path":      "/api/orders",
    "status_code": 200,
    "duration_ms": 4821.3
  },
  ...
]
```

### 3. Dashboard UI — sección "Slowest Requests"

Nueva sección `<section id="slowest-requests">` entre `<section id="endpoints">` y
`<section id="request-log">`.

**Tabla:**

| Time | Method | Path | Status | Duration |
|------|--------|------|--------|----------|

- Mismos estilos que el Request Log (badges de método, colores de status).
- `Duration`: resaltada en rojo si ≥ 1000 ms.
- Ordenada de mayor a menor duración (la API ya devuelve DESC).
- Máximo 10 filas (default `n=10`).
- Sin filtros — el propósito es ver los outliers directamente.
- La sección se actualiza en cada ciclo `refresh()`.

## API contract

| Endpoint | Método | Descripción |
|---|---|---|
| `GET /metrics/slowest-requests` | GET | Retorna las N requests individuales más lentas |

Parámetros query: `n` (int, default 10, max 100), `from` (RFC3339), `to` (RFC3339 o "now").

## Test cases

### metrics (metrics/engine_test.go adiciones)

| TC    | Descripción                                                                 |
|-------|-----------------------------------------------------------------------------|
| TC-01 | SlowestRequests devuelve registros ordenados por duration_ms DESC           |
| TC-02 | SlowestRequests respeta el límite n                                         |
| TC-03 | SlowestRequests con n > total devuelve todos                                |
| TC-04 | SlowestRequests sin registros → slice vacío (no nil)                        |
| TC-05 | n coartado a máximo 100                                                     |

### api (api/server_test.go adiciones)

| TC    | Descripción                                                                 |
|-------|-----------------------------------------------------------------------------|
| TC-06 | GET /metrics/slowest-requests → 200, JSON array                             |
| TC-07 | GET /metrics/slowest-requests?n=abc → 400 bad request                       |

## Files changed

| File                             | Change                                                       |
|----------------------------------|--------------------------------------------------------------|
| `metrics/engine.go`              | `SlowestRequests`, `SlowestRequestsForRange`                 |
| `metrics/engine_test.go`         | TC-01..TC-05                                                 |
| `api/server.go`                  | Ruta `GET /metrics/slowest-requests` + handler               |
| `api/server_test.go`             | TC-06..TC-07                                                 |
| `api/dashboard/index.html`       | `<section id="slowest-requests">`                            |
| `api/dashboard/static/app.js`    | `fetchSlowestRequests()`, añadir a `refresh()`               |

No cambia: `storage/`, `normalizer/`, `alerts/`, `proxy/`, `config/`, `cmd/`, `style.css`
(reutiliza los estilos de badges y status de US-27/US-28).

## Out of scope

- Filtrado por endpoint o método
- Persistencia del top N entre reinicios
- Percentil de la duración respecto al histórico
