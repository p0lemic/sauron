# US-11 — Throughput en tiempo real

**Épica:** Métricas y agregación (Fase 2)
**Prioridad:** Should
**Historia:** Como operator, puedo ver requests/segundo actual y promedio por endpoint.
**AC:** `GET /metrics/throughput` devuelve `rps_current` y `rps_avg`.

---

## Context

Con los registros en SQLite y la infraestructura de `metrics.Engine` ya en marcha, US-11 añade una vista de rendimiento expresada en **requests por segundo (RPS)**: cuánto tráfico recibe cada endpoint en este momento y en promedio.

Esta historia añade:
1. Un nuevo struct `ThroughputStat` y método `Throughput()` en `metrics.Engine`.
2. El endpoint `GET /metrics/throughput` en `api.Server`.

---

## Behavior

### Definiciones

| Campo | Definición |
|-------|-----------|
| `rps_avg` | `total_count` en la ventana configurada dividido entre la duración de esa ventana en segundos. |
| `rps_current` | Número de requests de los **últimos 10 segundos** dividido entre 10.0. Representa la tasa "en este momento". |

El intervalo de 10 segundos para `rps_current` es fijo e interno al motor; no es configurable por el operador en esta historia.

### Una sola lectura

`Throughput()` realiza **una sola llamada** a `reader.FindByWindow(now-window, now)` y calcula ambas métricas en memoria:
- Todos los registros del resultado contribuyen a `total_count` (para `rps_avg`).
- Los registros con `timestamp >= now - 10s` contribuyen a `rps_current`.

### Ordenación

Resultados ordenados por `rps_avg` **descendente** (endpoint con más tráfico promedio primero).

### Sin datos

Si no hay registros en la ventana → `[]`, status 200.

---

## API contract

### `GET /metrics/throughput`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/users",
    "rps_current": 4.2,
    "rps_avg": 2.37,
    "total_count": 142
  },
  {
    "method": "POST",
    "path": "/api/orders",
    "rps_current": 0.3,
    "rps_avg": 0.63,
    "total_count": 38
  }
]
```

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `method` | string | Método HTTP |
| `path` | string | Path del endpoint |
| `rps_current` | float64 | Requests/segundo en los últimos 10s |
| `rps_avg` | float64 | Requests/segundo promedio en la ventana configurada |
| `total_count` | int | Total de requests en la ventana |

**Response 405:** `POST /metrics/throughput` → `405 Method Not Allowed`.

---

## Cambios en código

### `metrics/engine.go`

```go
// currentWindow is the fixed lookback for rps_current.
const currentWindow = 10 * time.Second

// ThroughputStat holds throughput statistics for one method+path.
type ThroughputStat struct {
    Method     string  `json:"method"`
    Path       string  `json:"path"`
    RPSCurrent float64 `json:"rps_current"`
    RPSAvg     float64 `json:"rps_avg"`
    TotalCount int     `json:"total_count"`
}

// Throughput returns throughput stats for all endpoints active in the current
// window, sorted by RPSAvg descending.
func (e *Engine) Throughput() ([]ThroughputStat, error)
```

### `api/server.go`

- Registrar `GET /metrics/throughput` en el mux.
- Llamar `engine.Throughput()` y serializar como JSON.

---

## Test cases

### `metrics.Engine.Throughput`

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-01 | Sin registros | Slice vacío (no nil), sin error |
| TC-02 | 60 req en ventana de 60s, todos en los últimos 10s | `rps_avg=1.0`, `rps_current=6.0`, `total_count=60` |
| TC-03 | 60 req en ventana de 60s, ninguno en los últimos 10s | `rps_avg=1.0`, `rps_current=0.0` |
| TC-04 | 30 req en ventana de 60s, 10 en los últimos 10s | `rps_avg=0.5`, `rps_current=1.0` |
| TC-05 | Dos endpoints con distinto `rps_avg` | Ordenados por `rps_avg` desc |
| TC-06 | `total_count` refleja todos los registros en la ventana | Valor correcto |

### `api.Server` — `GET /metrics/throughput`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-07 | Sin datos | 200, `[]` |
| TC-08 | Registros presentes | 200, JSON con campos correctos |
| TC-09 | Content-Type | `application/json` |
| TC-10 | `POST /metrics/throughput` | 405 Method Not Allowed |

---

## Out of scope

- **`?window=` en este endpoint** — consistencia con US-08 queda para futura iteración.
- **`rps_current` configurable** — el intervalo de 10s es fijo en esta historia.
- **Streaming / SSE** de throughput en tiempo real.

---

## Dependencies

- **US-07** implementado: `metrics.Engine`, `storage.Reader.FindByWindow`.
- Sin nuevas dependencias externas.
