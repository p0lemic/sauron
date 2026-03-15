# US-47 — Traffic Heatmap

**Épica:** Observabilidad avanzada
**Prioridad:** Should
**Historia:** Como operator, veo un heatmap de tráfico hora×día para identificar patrones de carga recurrentes.
**AC:** Grid de 7×24 celdas con intensidad de color proporcional a RPS o error rate. Configurable por métrica y ventana temporal.

---

## Context

El dashboard muestra métricas en tiempo real pero no revela patrones temporales recurrentes (picos de lunes a las 9h, caídas de fin de semana, etc.). Este heatmap agrega todos los requests del periodo seleccionado agrupando por día-de-semana × hora-del-día, permitiendo identificar patrones estructurales.

---

## Behavior

### Agregación

Para cada celda `(weekday, hour)`:
- `weekday` ∈ [0..6] (0 = domingo, siguiendo `strftime('%w', ...)`)
- `hour` ∈ [0..23]
- Se acumulan todos los requests en ese slot durante la ventana seleccionada
- Si hay N ocurrencias del mismo slot (ej: 7 lunes en una ventana de 7 semanas), se promedian

Métricas soportadas:
- `rps` — requests/segundo promedio en ese slot
- `error_rate` — porcentaje de requests con status ≥ 400

### Normalización

El campo `max` de la respuesta es el valor máximo entre todas las celdas, para que el cliente pueda normalizar la intensidad de color sin una escala prefijada.

Celdas sin datos (ningún request en ese slot durante la ventana) retornan `value: 0`.

### Ventana temporal

Acepta el mismo mecanismo `?from=...&to=...` que el resto de endpoints. Si no se especifica, usa la ventana del motor (`cfg.MetricsWindow`).

---

## API contract

### `GET /metrics/heatmap`

**Query params:**

| Param | Tipo | Descripción |
|-------|------|-------------|
| `metric` | string | `rps` (default) \| `error_rate` |
| `from` | RFC3339 | inicio del rango (opcional) |
| `to` | RFC3339 | fin del rango (opcional) |

**Response 200 OK:**
```json
{
  "metric": "rps",
  "cells": [
    { "weekday": 0, "hour": 0,  "value": 0.0 },
    { "weekday": 0, "hour": 1,  "value": 1.23 },
    { "weekday": 1, "hour": 9,  "value": 42.7 },
    ...
  ],
  "max": 42.7
}
```

Las `cells` siempre tienen exactamente 168 entradas (7 × 24), una por cada combinación posible. Ordenadas por `weekday` ASC, `hour` ASC.

**Response 400** — `metric` inválida.
**Response 405** — método no GET.

---

## Cambios en código

### `storage/reader.go` — nuevo método `HeatmapCells`

```go
// HeatmapCell holds aggregated request data for one (weekday, hour) slot.
type HeatmapCell struct {
    Weekday int     // 0=Sunday..6=Saturday
    Hour    int     // 0..23
    Count   int     // total requests in this slot across all occurrences
    Errors  int     // requests with status_code >= 400
    Slots   int     // number of distinct occurrences of this (weekday, hour)
}

// HeatmapCells returns aggregated request data grouped by (weekday, hour)
// for requests with timestamp in [from, to).
HeatmapCells(from, to time.Time) ([]HeatmapCell, error)
```

Implementación SQLite:
```sql
SELECT
  CAST(strftime('%w', timestamp) AS INTEGER) AS weekday,
  CAST(strftime('%H', timestamp) AS INTEGER) AS hour,
  COUNT(*) AS count,
  SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) AS errors
FROM requests
WHERE timestamp >= ? AND timestamp < ?
GROUP BY weekday, hour
ORDER BY weekday, hour
```

`Slots` se calcula dividiendo la duración total de la ventana entre 1 semana para determinar cuántos ciclos hay; o bien usando COUNT(DISTINCT date(timestamp)) por slot (más preciso pero más caro). Para simplicidad, usar la duración: `slots = max(1, round(windowDays/7.0))`.

### `metrics/engine.go` — nuevo método `Heatmap`

```go
// HeatmapResult is the full response for the heatmap endpoint.
type HeatmapResult struct {
    Metric string         `json:"metric"`
    Cells  []HeatmapPoint `json:"cells"`
    Max    float64        `json:"max"`
}

type HeatmapPoint struct {
    Weekday int     `json:"weekday"`
    Hour    int     `json:"hour"`
    Value   float64 `json:"value"`
}

// Heatmap returns aggregated traffic data for a heatmap grid.
// metric: "rps" | "error_rate"
func (e *Engine) Heatmap(metric string, from, to time.Time) (*HeatmapResult, error)
```

La lógica convierte `HeatmapCell` → `HeatmapPoint`:
- `rps`: `value = count / (slots * 3600.0)`
- `error_rate`: `value = errors * 100.0 / max(count, 1)`

Siempre genera las 168 celdas, rellenando con 0 las que no tengan datos.

### `api/server.go` — nuevo handler `handleHeatmap`

Registrar `GET /metrics/heatmap`. Parsear `?metric=` (default `"rps"`), validar, llamar `engine.Heatmap(metric, from, to)`.

### Dashboard — nueva sección `#heatmap`

Posición: entre Overview y Status.

Grid CSS de 24 columnas (horas) × 7 filas (días). Cada celda es un `div` coloreado con `opacity` proporcional a `value/max`. Color base: `var(--accent)` para RPS, `var(--danger)` para error rate.

Tooltip al hover: `{día} {hora}:00 — {value} {unidad}`.

Selector de métrica: `[RPS] [Error rate]` en el section-header.

---

## Test cases

### `storage` — HeatmapCells

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | DB vacía | Slice vacío |
| TC-02 | 3 requests el lunes a las 9h, 2 el martes a las 10h | 2 cells con counts correctos |
| TC-03 | Request fuera del rango `[from,to)` | No aparece en resultado |
| TC-04 | Requests con status 400 y 200 | `errors` cuenta solo los ≥400 |

### `metrics` — Heatmap

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-05 | metric="rps", 3600 requests en 1 slot de 1h | value ≈ 1.0 rps |
| TC-06 | metric="error_rate", 10 errors de 100 requests | value = 10.0 |
| TC-07 | Resultado siempre tiene exactamente 168 celdas | len(cells) == 168 |
| TC-08 | Max es el mayor valor entre todas las celdas | max == max(cells[*].value) |
| TC-09 | metric inválida | devuelve error |

### `api` — GET /metrics/heatmap

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-10 | Sin datos | 200, 168 celdas con value=0, max=0 |
| TC-11 | Con datos | 200, cells y max correctos |
| TC-12 | metric=error_rate | Calcula error rate en lugar de RPS |
| TC-13 | metric=invalid | 400 Bad Request |
| TC-14 | POST | 405 Method Not Allowed |

---

## Out of scope

- Heatmap por endpoint específico (solo global).
- Métrica de latencia (P99) en heatmap — complejidad adicional de percentiles por celda.
- Postgres: el SQL es estándar y compatible, pero los tests solo cubren SQLite.

---

## Dependencies

- US-02: storage con timestamps.
- US-07: metrics.Engine con acceso al reader.
- US-08: rangeParams en dashboard JS.
