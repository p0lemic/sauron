# US-28 — Status Code Breakdown

## Context

El dashboard muestra error rate como porcentaje global pero no desglosa cuántas respuestas
son 2xx, 3xx, 4xx o 5xx. El breakdown de status codes permite ver de un vistazo la salud
de la API sin necesidad de filtrar endpoint por endpoint.

## Behavior

### 1. Nuevo método en `metrics.Engine`

```go
// StatusGroup holds aggregated counts for one HTTP status class (2xx, 3xx, 4xx, 5xx).
type StatusGroup struct {
    Class string `json:"class"` // "2xx", "3xx", "4xx", "5xx"
    Count int    `json:"count"`
    Rate  float64 `json:"rate"` // percentage of total requests (0–100, 1 decimal)
}

// StatusBreakdown returns counts and rates per status class for the engine's window.
func (e *Engine) StatusBreakdown() ([]StatusGroup, error)

// StatusBreakdownForRange returns counts and rates per status class for [from, to).
func (e *Engine) StatusBreakdownForRange(from, to time.Time) ([]StatusGroup, error)
```

- Siempre devuelve los 4 grupos en orden: `["2xx", "3xx", "4xx", "5xx"]`.
- Si un grupo tiene 0 requests, `count=0` y `rate=0.0`.
- `rate` se redondea a 1 decimal.
- Si no hay registros en total, todos los rates son `0.0`.

### 2. Nuevo endpoint HTTP

```
GET /metrics/status?from=<RFC3339>&to=<RFC3339>
```

- `from` / `to`: opcionales, mismo comportamiento que `parseTimeRange`.
- Respuesta: `[]StatusGroup` serializado a JSON.

```json
[
  { "class": "2xx", "count": 842, "rate": 91.2 },
  { "class": "3xx", "count": 12,  "rate": 1.3  },
  { "class": "4xx", "count": 68,  "rate": 7.4  },
  { "class": "5xx", "count": 2,   "rate": 0.2  }
]
```

### 3. Dashboard UI — sección "Status"

Nueva sección `<section id="status-breakdown">` entre `<section id="summary">` y
`<section id="alerts">`.

**Visualización:** cuatro tarjetas horizontales, una por grupo de status:

```
┌─────────────────────────────────────────────────────────┐
│  2xx        3xx        4xx        5xx                   │
│  842        12         68         2                     │
│  91.2%      1.3%       7.4%       0.2%                  │
│  [████████████████████░░░░░] (barra de progreso)        │
└─────────────────────────────────────────────────────────┘
```

- `2xx`: color verde (`--ok`)
- `3xx`: color amarillo (`--warning`)
- `4xx`: color naranja (`#fb923c`)
- `5xx`: color rojo (`--danger`)
- Cada tarjeta tiene: clase, count grande, porcentaje, barra de progreso proporcional al total
- La sección se actualiza en cada ciclo `refresh()`.

## API contract

| Endpoint | Método | Descripción |
|---|---|---|
| `GET /metrics/status` | GET | Retorna breakdown por clase de status code |

Parámetros query: `from` (RFC3339), `to` (RFC3339 o "now").

## Test cases

### metrics (metrics/engine_test.go adiciones)

| TC    | Descripción                                                                 |
|-------|-----------------------------------------------------------------------------|
| TC-01 | Sin registros → 4 grupos, todos count=0 y rate=0.0                         |
| TC-02 | Registros mixtos → counts y rates correctos (suma = 100%)                  |
| TC-03 | Solo 2xx → 2xx rate=100%, resto 0%                                          |
| TC-04 | StatusBreakdownForRange respeta el rango                                    |

### api (api/server_test.go adiciones)

| TC    | Descripción                                                                 |
|-------|-----------------------------------------------------------------------------|
| TC-05 | GET /metrics/status → 200, array de 4 elementos                             |
| TC-06 | Siempre devuelve exactamente 4 grupos en orden 2xx/3xx/4xx/5xx              |

## Files changed

| File                             | Change                                          |
|----------------------------------|-------------------------------------------------|
| `metrics/engine.go`              | `StatusGroup`, `StatusBreakdown`, `StatusBreakdownForRange` |
| `metrics/engine_test.go`         | TC-01..TC-04                                    |
| `api/server.go`                  | Ruta `GET /metrics/status` + handler            |
| `api/server_test.go`             | TC-05..TC-06                                    |
| `api/dashboard/index.html`       | `<section id="status-breakdown">`               |
| `api/dashboard/static/app.js`    | `fetchStatusBreakdown()`, añadir a `refresh()`  |
| `api/dashboard/static/style.css` | Estilos para las tarjetas de status             |

No cambia: `storage/`, `normalizer/`, `alerts/`, `proxy/`, `config/`, `cmd/`.

## Out of scope

- Breakdown por status code individual (ej. 200, 404, 500)
- Breakdown por endpoint
- Histórico de breakdowns en el tiempo
