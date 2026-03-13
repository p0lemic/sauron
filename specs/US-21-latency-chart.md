# US-21 — Gráfica de latencia por endpoint

**Épica:** Dashboard web (Fase 4)
**Prioridad:** Should
**Historia:** Como developer, al hacer click en un endpoint veo una gráfica temporal de su latencia.
**AC:** Serie temporal de P99 en los últimos 60 minutos.

---

## Context

La tabla de endpoints (US-20) ya muestra los valores instantáneos. US-21 añade dimensión temporal: al clicar una fila, se abre un panel con una gráfica SVG del P99 minuto a minuto durante la última hora. No se usa ninguna librería de gráficas externa.

---

## Behavior

### Interacción

- Click en cualquier fila de `#endpoints` → abre panel de detalle (inline bajo la tabla, no modal).
- El panel muestra el nombre del endpoint (`METHOD /path`) y la gráfica.
- Click de nuevo en la misma fila (o en un botón ×) → cierra el panel.
- Click en otra fila → reemplaza el panel con los datos del nuevo endpoint.

### Gráfica

- **Tipo**: línea SVG responsiva.
- **X**: tiempo — últimos 60 minutos, de izquierda (más antiguo) a derecha (más reciente).
- **Y**: P99 en ms. Eje Y comienza en 0, máximo = mayor P99 del rango × 1.1.
- **Puntos vacíos** (buckets sin datos): sin punto ni segmento de línea (gap en la curva).
- Etiquetas en eje X: cada 15 minutos (`-60m`, `-45m`, `-30m`, `-15m`, `now`).
- Etiqueta en eje Y: valor máximo y 0.
- Línea de color accent (`--accent`). Fondo `--bg`. Sin dependencias externas.

### Datos

`GET /metrics/latency?method=GET&path=/api/users` devuelve 60 buckets de 1 minuto cada uno para los últimos 60 minutos. Buckets sin datos tienen `p99 = 0`.

---

## API contract

### `GET /metrics/latency`

**Query params:**
- `method` — HTTP method (requerido)
- `path` — endpoint path (requerido)

**Response 200 OK:**
```json
[
  { "ts": "2026-03-13T10:00:00Z", "p99": 120.5 },
  { "ts": "2026-03-13T10:01:00Z", "p99": 0 },
  { "ts": "2026-03-13T10:02:00Z", "p99": 145.0 }
]
```

Array de exactamente 60 elementos, ordenados por `ts` ascendente (más antiguo primero). `p99 = 0` indica bucket sin datos.

**Response 400** — falta `method` o `path`.
**Response 405** — método incorrecto.

---

## Cambios en código

### `metrics/engine.go`

```go
type BucketStat struct {
    Ts  time.Time `json:"ts"`
    P99 float64   `json:"p99"`
}

// Latency returns 60 one-minute P99 buckets for the given endpoint
// covering the last 60 minutes. Buckets with no data have P99 = 0.
func (e *Engine) Latency(method, path string) ([]BucketStat, error)
```

Implementación:
1. `now` = tiempo actual truncado al minuto.
2. `from` = `now - 60 * minute`.
3. Una sola llamada `reader.FindByWindow(from, now)`.
4. Para cada record, calcular `bucket = record.Timestamp.Truncate(minute)`.
5. Agrupar duraciones por bucket para el endpoint `method+path`.
6. Generar slice de 60 `BucketStat` de `from` a `now-1m`; calcular P99 donde haya datos, 0 si no.

### `api/server.go`

Registrar `GET /metrics/latency` → `handleLatency`.
Validar que `method` y `path` estén presentes, devolver 400 si faltan.

### `api/dashboard/static/app.js`

```js
function renderChart(buckets, containerId) { ... }  // SVG inline
async function fetchLatency(method, path) { ... }   // GET /metrics/latency
```

- Click en fila de tabla → `fetchLatency(row.method, row.path)`.
- Panel de detalle insertado bajo la tabla con `id="chart-panel"`.
- `renderChart()` dibuja el SVG responsivo con `viewBox`.

### `api/dashboard/static/style.css`

Estilos para `#chart-panel`: fondo, padding, botón de cierre, SVG responsive.

---

## Test cases

### `metrics.Engine` — Latency

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Sin datos para el endpoint | 60 buckets, todos P99 = 0 |
| TC-02 | Records en un bucket → P99 calculado | El bucket correcto tiene P99 > 0 |
| TC-03 | Records de otro endpoint → no afectan | Buckets del endpoint consultado = 0 |
| TC-04 | Siempre 60 elementos | `len(result) == 60` |

### `api.Server` — `GET /metrics/latency`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-05 | Con `method` y `path` | 200, 60 elementos |
| TC-06 | Sin `method` | 400 |
| TC-07 | Sin `path` | 400 |
| TC-08 | POST | 405 |

---

## Out of scope

- Gráficas de P50/P95 — solo P99 en el PRD.
- Zoom / pan en la gráfica — no en el PRD.
- Ventana configurable (distinta de 60 min) — no en el PRD.
- Múltiples endpoints en la misma gráfica — no en el PRD.

---

## Dependencies

- US-20 implementado: tabla de endpoints con filas clicables.
- US-18: shell del dashboard.
- Sin nuevas dependencias externas.
