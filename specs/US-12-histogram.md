# US-12 — Histograma de latencias

**Épica:** Métricas avanzadas (Fase 3)
**Prioridad:** Could
**Historia:** Como developer, veo la distribución de latencias de un endpoint como histograma.
**AC:** `GET /metrics/histogram?method=GET&path=/x` devuelve los conteos por bucket de duración.

---

## Context

La tabla de endpoints (US-20) y la gráfica temporal (US-21) muestran percentiles y evolución en el tiempo. El histograma complementa esta vista mostrando la **distribución** de las latencias: cuántos requests cayeron en cada rango de duración durante la ventana actual.

---

## Behavior

### Buckets

Se usan 9 buckets con límites superiores fijos (en ms): `10, 25, 50, 100, 250, 500, 1000, 2500, +Inf`.

Cada bucket devuelve el conteo **acumulado** (`le` = less-or-equal, estilo Prometheus):
- Bucket `le=50` contiene todos los requests con duración ≤ 50 ms.
- Bucket `le=+Inf` siempre es igual a `total_count`.

### Filtro

- `method` + `path` **opcionales**. Si se omiten, el histograma agrega todos los requests de la ventana.
- Si se especifican ambos, filtra por ese endpoint.

### Ventana

Usa la ventana por defecto del engine (igual que `Endpoints()`).

### Frontend

El histograma se muestra como segunda pestaña en el panel de detalle del endpoint (junto a la gráfica de US-21). El panel tiene dos tabs: **Chart** (US-21) y **Histogram**. Las barras se renderizan con SVG inline.

---

## API contract

### `GET /metrics/histogram`

**Query params (opcionales):** `method`, `path`

**Response 200 OK:**
```json
{
  "buckets": [
    { "le": 10,    "count": 5  },
    { "le": 25,    "count": 18 },
    { "le": 50,    "count": 42 },
    { "le": 100,   "count": 67 },
    { "le": 250,   "count": 89 },
    { "le": 500,   "count": 95 },
    { "le": 1000,  "count": 98 },
    { "le": 2500,  "count": 99 },
    { "le": -1,    "count": 100 }
  ],
  "total_count": 100
}
```

`le = -1` representa `+Inf`. Conteos **acumulados**.

**Response 405** — método incorrecto.

---

## Cambios en código

### `metrics/engine.go`

```go
var HistogramBounds = []float64{10, 25, 50, 100, 250, 500, 1000, 2500}

type HistogramBucket struct {
    Le    float64 `json:"le"`    // upper bound; -1 = +Inf
    Count int     `json:"count"` // cumulative count
}

type HistogramStat struct {
    Buckets    []HistogramBucket `json:"buckets"`
    TotalCount int               `json:"total_count"`
}

// Histogram returns cumulative latency buckets for the current window.
// If method and path are both non-empty, filters to that endpoint.
func (e *Engine) Histogram(method, path string) (HistogramStat, error)
```

Implementación:
1. `reader.FindByWindow(now-window, now)`.
2. Filtrar por `method+path` si no están vacíos.
3. Para cada duración, incrementar todos los buckets con `le >= duración`.
4. Añadir bucket `+Inf` (le=-1) con `count = total`.

### `api/server.go`

Registrar `GET /metrics/histogram` → `handleHistogram`.

### `api/dashboard/static/app.js`

Añadir tabs al panel de detalle del endpoint. Tab **Histogram** llama a `/metrics/histogram?method=&path=` y renderiza barras SVG horizontales con el conteo de cada bucket (no acumulado — diferencia entre buckets consecutivos para mostrar distribución real).

### `api/dashboard/static/style.css`

Estilos para tabs `.panel-tabs`, `.panel-tab`, y barras `.hist-bar`.

---

## Test cases

### `metrics.Engine` — Histogram

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Sin datos | Todos los conteos 0, total_count 0 |
| TC-02 | Records dentro de un bucket | Ese bucket y todos los superiores incrementados |
| TC-03 | Filtro por endpoint | Solo cuenta los records del endpoint |
| TC-04 | Sin filtro | Agrega todos los endpoints |
| TC-05 | Siempre 9 buckets | `len(buckets) == 9` |

### `api.Server` — `GET /metrics/histogram`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-06 | Sin params | 200, 9 buckets |
| TC-07 | Con method+path | 200, filtra correctamente |
| TC-08 | Content-Type | `application/json` |
| TC-09 | POST | 405 |

---

## Out of scope

- Buckets configurables por query param — no en el PRD.
- Histograma global sin filtro en el dashboard (solo en el panel de detalle).
- Percentiles calculados desde el histograma — ya existen vía `Endpoints()`.

---

## Dependencies

- US-07: percentiles implementados (el histograma es complementario).
- US-21: panel de detalle del endpoint donde se muestra el histograma.
