# US-20 — Tabla de endpoints

**Épica:** Dashboard web (Fase 4)
**Prioridad:** Must
**Historia:** Como developer, veo una tabla de todos los endpoints con p50/p95/p99, RPS y error rate.
**AC:** Ordenable por cualquier columna. Filtrable por path.

---

## Context

Los endpoints `/metrics/endpoints`, `/metrics/errors` y `/metrics/throughput` ya exponen los datos necesarios, pero por separado. US-20 añade `GET /metrics/table` que combina todo en una sola respuesta lista para la tabla, y rellena la sección `#endpoints` del dashboard.

---

## Behavior

### Tabla en el dashboard

```
Filter: [________________]

METHOD  PATH          P50     P95     P99     RPS     ERR%
──────────────────────────────────────────────────────────
GET     /api/users    12 ms   45 ms   120 ms  3.2     0.0%
POST    /api/orders   80 ms  210 ms   450 ms  1.1     2.5%  ← rojo si ≥5%
```

- **Filtro**: input de texto que filtra filas por `path` (substring, case-insensitive). Filtra en cliente, sin nueva petición.
- **Ordenación**: click en cabecera de columna alterna ASC/DESC. Indicador visual de columna activa y dirección.
- **Color de ERR%**: ≥5% → danger, ≥1% → warning, 0% → texto normal.
- **Color de P99**: ≥1000 ms → danger.
- Si no hay endpoints → fila "No data".
- Actualización automática cada 5s (vía `refresh()` de US-18).

### Datos

Cada fila combina:
- `method`, `path`
- `p50`, `p95`, `p99` — de `Endpoints()`
- `rps_current` — de `Throughput()`
- `error_rate` — de `Errors()`
- `count` — total de requests

---

## API contract

### `GET /metrics/table`

**Response 200 OK:**
```json
[
  {
    "method":      "GET",
    "path":        "/api/users",
    "p50":         12.0,
    "p95":         45.0,
    "p99":         120.0,
    "rps_current": 3.2,
    "error_rate":  0.0,
    "count":       192
  }
]
```

Array vacío si no hay datos. Ordenado por P99 desc (el servidor entrega en ese orden; la UI reordena en cliente).

**Response 405** — método incorrecto.

---

## Cambios en código

### `metrics/engine.go`

Nuevo struct y método:

```go
type TableRow struct {
    Method     string  `json:"method"`
    Path       string  `json:"path"`
    P50        float64 `json:"p50"`
    P95        float64 `json:"p95"`
    P99        float64 `json:"p99"`
    RPSCurrent float64 `json:"rps_current"`
    ErrorRate  float64 `json:"error_rate"`
    Count      int     `json:"count"`
}

func (e *Engine) Table() ([]TableRow, error)
```

Implementación: llama a `Endpoints()`, `Throughput()`, y `Errors()` (tres lecturas al storage), construye maps por `method:path`, combina en `[]TableRow` ordenado por P99 desc.

### `api/server.go`

Registrar `GET /metrics/table` → `handleTable`.

### `api/dashboard/static/app.js`

```js
async function fetchEndpoints() { ... }
```

- GET `/metrics/table`, renderiza tabla HTML en `#endpoints`.
- Gestiona estado de filtro y orden en variables de módulo.
- `refresh()` llama a `fetchEndpoints()`.

### `api/dashboard/static/style.css`

Estilos para tabla: `.data-table`, `.filter-input`, cabeceras ordenables, celdas coloreadas.

---

## Test cases

### `metrics.Engine` — Table

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Sin datos | Slice vacío |
| TC-02 | Un endpoint, sin errores | Row con campos correctos, error_rate 0 |
| TC-03 | Endpoint con errores | error_rate calculado |
| TC-04 | Dos endpoints → ordenados P99 desc | El más lento primero |

### `api.Server` — `GET /metrics/table`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-05 | Sin datos | 200, `[]` |
| TC-06 | Con datos | 200, JSON con campos correctos |
| TC-07 | Content-Type | `application/json` |
| TC-08 | POST /metrics/table | 405 |

---

## Out of scope

- Paginación — no en el PRD.
- Ordenación server-side — la UI ordena en cliente.
- Columnas configurables — no en el PRD.

---

## Dependencies

- US-18/19 implementados: shell + `refresh()`.
- `metrics.Engine.Endpoints()`, `Throughput()`, `Errors()` disponibles.
