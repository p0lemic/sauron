# US-19 — Vista de resumen global

**Épica:** Dashboard web (Fase 4)
**Prioridad:** Must
**Historia:** Como developer, veo un resumen con: total requests, error rate global, p99 global, endpoints activos.
**AC:** Actualización automática cada 5 segundos vía polling.

---

## Context

US-18 creó el shell del dashboard con `refresh()` vacía y el polling a 5s. US-19 rellena la sección `#summary` con 4 métricas globales, añadiendo un endpoint `GET /metrics/summary` al servidor y el JS correspondiente en `app.js`.

---

## Behavior

### Sección `#summary`

Muestra 4 tarjetas de métricas:

```
┌──────────────────────────────────────────────────────────┐
│  TOTAL REQUESTS    ERROR RATE    GLOBAL P99    ENDPOINTS  │
│    1 234           2.5%          450 ms        8          │
└──────────────────────────────────────────────────────────┘
```

- **Total requests**: suma de `count` de todos los endpoints en la ventana actual.
- **Error rate**: `(total_errors / total_requests) * 100`, redondeado a 1 decimal. `0.0%` si no hay requests.
- **Global P99**: máximo P99 entre todos los endpoints. `0 ms` si no hay datos.
- **Active endpoints**: número de endpoints distintos con al menos 1 request.

Las tarjetas se colorean según umbrales:
- Error rate ≥ 5% → color danger (`--danger`)
- Error rate ≥ 1% → color warning (`--warning`)
- P99 ≥ 1000 ms → color danger
- Sin datos → color muted

### Polling

`fetchSummary()` llamada desde `refresh()` cada 5s. Si la llamada falla (red), muestra el último valor conocido sin limpiar la UI.

---

## API contract

### `GET /metrics/summary`

**Response 200 OK:**
```json
{
  "total_requests":   1234,
  "global_error_rate": 2.5,
  "global_p99":       450.0,
  "active_endpoints":  8
}
```

- `global_error_rate`: porcentaje (0–100), 1 decimal.
- `global_p99`: milisegundos, float.
- Sin datos → todos los campos a `0`.

**Response 405** — método incorrecto.

---

## Cambios en código

### `metrics/engine.go`

Nuevo struct y método:

```go
type SummaryStat struct {
    TotalRequests   int     `json:"total_requests"`
    GlobalErrorRate float64 `json:"global_error_rate"`
    GlobalP99       float64 `json:"global_p99"`
    ActiveEndpoints int     `json:"active_endpoints"`
}

func (e *Engine) Summary() (SummaryStat, error)
```

Implementación:
1. Llama a `e.Endpoints()` para obtener stats por endpoint.
2. Llama a `e.Errors()` para obtener error counts por endpoint.
3. Calcula:
   - `total_requests` = suma de `ep.Count`
   - `global_p99` = máximo de `ep.P99`
   - `active_endpoints` = len(endpoints)
   - `global_error_rate` = `(sum(err.ErrorCount) / total_requests) * 100` (0 si total_requests == 0)

### `api/server.go`

Registrar `GET /metrics/summary` → `handleSummary`.

### `api/dashboard/static/app.js`

```js
async function fetchSummary() { ... }  // GET /metrics/summary, renderiza #summary
```

`refresh()` llama a `fetchSummary()`.

---

## Test cases

### `metrics.Engine` — Summary

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Sin datos | Todos los campos 0 |
| TC-02 | Varios endpoints, sin errores | total_requests correcto, error_rate 0 |
| TC-03 | Errores presentes | global_error_rate calculado correctamente |
| TC-04 | global_p99 = max de todos los endpoints | Valor correcto |

### `api.Server` — `GET /metrics/summary`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-05 | Sin datos | 200, todos a 0 |
| TC-06 | Con datos | 200, campos correctos |
| TC-07 | Content-Type | `application/json` |
| TC-08 | POST /metrics/summary | 405 |

---

## Out of scope

- Ventana configurable para el summary — usa la ventana por defecto del engine.
- Histórico de métricas globales — no en el PRD.
- Alertas en el summary — cubierto por US-22.

---

## Dependencies

- US-18 implementado: shell del dashboard, `refresh()`, polling.
- `metrics.Engine.Endpoints()` y `metrics.Engine.Errors()` disponibles.
