# US-44 — Apdex score por endpoint

## Context

Apdex (Application Performance Index) es un estándar de la industria para medir la satisfacción
de los usuarios con la latencia de un servicio. A diferencia de los percentiles (que miden
distribuciones), Apdex colapsa la latencia en un único número en [0, 1] que comunica de forma
inmediata la calidad de la experiencia: 1.0 = excelente, <0.7 = inaceptable.

El proxy ya captura `duration_ms` por request y calcula percentiles. Añadir Apdex es puro
cálculo in-memory sobre los mismos registros, sin cambios de schema.

## Behavior

Para cada endpoint (method + path normalizado) se calcula:

```
T  = umbral de satisfacción (ms, configurable, default 500 ms)
4T = umbral de frustración

Satisfied  = requests con duration_ms ≤ T
Tolerating = requests con T < duration_ms ≤ 4T
Frustrated = requests con duration_ms > 4T

Apdex = (Satisfied + Tolerating / 2) / Total
        redondeado a 3 decimales
```

Reglas:
- Si `Total == 0` para un endpoint, Apdex = `null` (se omite el campo o se devuelve -1).
- T es configurable vía YAML (`metrics_apdex_t`) y flag `--apdex-t` (en ms, entero).
  Valor por defecto: **500 ms**.
- La ventana temporal es la misma que `--metrics-window` (ya configurable, US-08).
- Se respeta el query param `?window=` definido en US-08.

## API contract

### GET /metrics/apdex

Query params opcionales:
- `?window=60s` — sobrescribe la ventana por defecto (mismo mecanismo que /metrics/endpoints)
- `?t=250` — sobrescribe el umbral T en ms para esta llamada (no modifica la config)

Response 200:
```json
{
  "t_ms": 500,
  "window": "60s",
  "endpoints": [
    {
      "method": "GET",
      "path": "/users/{id}",
      "apdex": 0.942,
      "satisfied": 187,
      "tolerating": 11,
      "frustrated": 2,
      "total": 200
    }
  ]
}
```

Ordenado por `apdex` ascendente (los más problemáticos primero).

Errores:
- `?t` no numérico o ≤ 0 → 400 `{"error": "invalid t: must be a positive integer (ms)"}`
- `?window` inválido → comportamiento igual que /metrics/endpoints (ya implementado)

### Config YAML

```yaml
metrics_apdex_t: 500   # ms, entero positivo
```

### Flag CLI

```
--apdex-t int   Apdex satisfaction threshold in ms (default 500)
```

## Nuevos tipos y métodos (package metrics)

```go
// ApdexStat holds Apdex score and component counts for one endpoint.
type ApdexStat struct {
    Method     string  `json:"method"`
    Path       string  `json:"path"`
    Apdex      float64 `json:"apdex"`   // -1 if no data
    Satisfied  int     `json:"satisfied"`
    Tolerating int     `json:"tolerating"`
    Frustrated int     `json:"frustrated"`
    Total      int     `json:"total"`
}

// ApdexResult wraps the list with the active T and window for the response.
type ApdexResult struct {
    TMs      int         `json:"t_ms"`
    Window   string      `json:"window"`
    Endpoints []ApdexStat `json:"endpoints"`
}

// Apdex computes Apdex scores for all endpoints using threshold tMs (ms).
func (e *Engine) Apdex(tMs float64) ([]ApdexStat, error)

// ApdexForRange computes Apdex scores for an explicit [from, to] range.
func (e *Engine) ApdexForRange(tMs float64, from, to time.Time) ([]ApdexStat, error)
```

## Test cases

**metrics (TC-01..TC-06)**

TC-01 **Cálculo básico correcto**: 10 requests — 6 ≤ T, 3 ≤ 4T, 1 > 4T. Apdex esperado =
(6 + 3/2) / 10 = 0.750. Verificar que el método devuelve 0.750.

TC-02 **Apdex = 1.0 cuando todos son satisfied**: 5 requests con duration < T. Apdex = 1.000.

TC-03 **Apdex = 0.0 cuando todos son frustrated**: 5 requests con duration > 4T. Apdex = 0.000.

TC-04 **Endpoint sin requests devuelve Apdex -1**: ventana sin registros para un endpoint.
El campo `apdex` es -1 (o el endpoint no aparece en la lista).

TC-05 **Ordenado ascendente por Apdex**: 3 endpoints con distintos scores. Verificar orden.

TC-06 **ApdexForRange respeta el rango**: registros fuera del rango no cuentan.

**api (TC-07..TC-10)**

TC-07 **GET /metrics/apdex devuelve 200 con estructura correcta**: verificar campos
`t_ms`, `window`, `endpoints`.

TC-08 **?t=250 sobrescribe el umbral**: con T=500 default, llamar con ?t=250. Los conteos
deben reflejar el nuevo umbral.

TC-09 **?t=0 devuelve 400**: umbral inválido.

TC-10 **?t=abc devuelve 400**: umbral no numérico.

**config (TC-11)**

TC-11 **metrics_apdex_t en YAML se parsea correctamente**: config con `metrics_apdex_t: 250`
→ `Config.ApdexT == 250`.

**cmd/profiler (TC-12)**

TC-12 **--apdex-t flag**: el flag se parsea y pasa correctamente a config.

## Out of scope

- Apdex global (agregado de todos los endpoints) — puede añadirse en US futuro
- Alertas basadas en Apdex — US futuro
- Apdex en dashboard visual — US futuro (requiere US-18 refactor)
- Historial de Apdex a lo largo del tiempo

## Dependencies

- US-02 storage.Record (duration_ms)
- US-07 metrics.Engine, EndpointsForRange
- US-08 window query param (reutilizar parseWindow de api/server.go)
- US-26 path normalization (paths ya normalizados al guardar)
