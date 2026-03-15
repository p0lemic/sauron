# US-46 — Error fingerprinting

## Context

Los endpoints `/metrics/errors` y `/metrics/status` ya muestran tasas de error agregadas.
Sin embargo, no permiten distinguir entre tipos de error distintos en el mismo endpoint:
por ejemplo, un endpoint que devuelve tanto 401 como 503 aparece como un único bloque de
"error rate". El fingerprinting de errores agrupa los errores 4xx/5xx por `(method, path,
status_code)` y hace tracking de cuándo aparecen, con qué frecuencia, y cuánto tardan.

Es puro cálculo in-memory sobre los registros existentes — no requiere cambios de schema.

## Behavior

Un **fingerprint de error** es la combinación `(method, path normalizado, status_code)`.

Para cada fingerprint con `status_code >= 400` en la ventana activa se computa:

- `count` — número de ocurrencias en la ventana
- `rate` — porcentaje del total de requests del mismo endpoint (0–100, 1 decimal)
- `first_seen` — timestamp del primer request con este fingerprint en la ventana
- `last_seen` — timestamp del último request con este fingerprint en la ventana
- `p50_ms`, `p95_ms` — percentiles de duración de estos requests erróneos
- `is_new` — true si `first_seen` está en el último 10 % de la ventana (recién apareció)

Reglas:
- Solo se incluyen fingerprints con al menos 1 ocurrencia.
- Ordenados por `count` descendente (los errores más frecuentes primero).
- Se respeta la ventana configurable (query param `?window=` de US-08).
- Los status 1xx, 2xx, 3xx no generan fingerprints.

## API contract

### GET /metrics/errors/fingerprints

Query params opcionales:
- `?window=60s` — sobrescribe la ventana por defecto
- `?status=5xx` — filtra por clase (4xx o 5xx); si ausente, devuelve ambas clases

Response 200:
```json
{
  "window": "60s",
  "total_errors": 47,
  "fingerprints": [
    {
      "method": "GET",
      "path": "/users/{id}",
      "status_code": 503,
      "count": 23,
      "rate": 11.5,
      "first_seen": "2026-03-15T09:58:00Z",
      "last_seen": "2026-03-15T10:00:00Z",
      "p50_ms": 1240.0,
      "p95_ms": 2800.0,
      "is_new": false
    },
    {
      "method": "POST",
      "path": "/orders",
      "status_code": 422,
      "count": 14,
      "rate": 7.0,
      "first_seen": "2026-03-15T09:59:50Z",
      "last_seen": "2026-03-15T10:00:00Z",
      "p50_ms": 12.0,
      "p95_ms": 18.0,
      "is_new": true
    }
  ]
}
```

Errores:
- `?status` con valor distinto de `4xx` o `5xx` → 400 `{"error": "invalid status filter: use 4xx or 5xx"}`

## Nuevos tipos y métodos (package metrics)

```go
// ErrorFingerprint holds aggregated error stats for one (method, path, status_code) tuple.
type ErrorFingerprint struct {
    Method     string    `json:"method"`
    Path       string    `json:"path"`
    StatusCode int       `json:"status_code"`
    Count      int       `json:"count"`
    Rate       float64   `json:"rate"`    // % of total requests for this endpoint
    FirstSeen  time.Time `json:"first_seen"`
    LastSeen   time.Time `json:"last_seen"`
    P50Ms      float64   `json:"p50_ms"`
    P95Ms      float64   `json:"p95_ms"`
    IsNew      bool      `json:"is_new"`
}

// ErrorFingerprints returns error fingerprints for the engine's current window.
func (e *Engine) ErrorFingerprints() ([]ErrorFingerprint, error)

// ErrorFingerprintsForRange returns error fingerprints for an explicit [from, to] range.
func (e *Engine) ErrorFingerprintsForRange(from, to time.Time) ([]ErrorFingerprint, error)
```

`is_new` se calcula como: `first_seen.After(from.Add(0.9 * (to - from)))`.

## Test cases

**metrics (TC-01..TC-07)**

TC-01 **Fingerprint básico correcto**: 5 requests GET /users/{id}, 3 con status=503 y
durations [100, 200, 300], 2 con status=200. Esperado: 1 fingerprint (503), count=3,
rate=60.0, p50=200, p95=300.

TC-02 **Dos fingerprints distintos para mismo endpoint**: GET /orders → 2 con 422, 1 con
503. Devuelve 2 fingerprints distintos, ordenados por count desc.

TC-03 **Fingerprints de endpoints distintos**: múltiples endpoints con errores. Verificar
que rate se calcula sobre el total del endpoint propio, no global.

TC-04 **is_new=true cuando first_seen está en el último 10 % de la ventana**: ventana de
60s, first_seen a 5s del final → is_new=true.

TC-05 **is_new=false cuando first_seen está al inicio de la ventana**: first_seen al
principio → is_new=false.

TC-06 **Requests 2xx/3xx no generan fingerprints**: solo status ≥ 400 aparece en resultado.

TC-07 **Ventana vacía devuelve slice vacío**: sin registros → fingerprints=[].

**api (TC-08..TC-11)**

TC-08 **GET /metrics/errors/fingerprints devuelve 200 con estructura correcta**: verificar
campos `window`, `total_errors`, `fingerprints`.

TC-09 **?status=5xx filtra solo errores 5xx**: en presencia de fingerprints 4xx y 5xx,
solo devuelve los 5xx.

TC-10 **?status=4xx filtra solo errores 4xx**.

TC-11 **?status=3xx devuelve 400 con mensaje de error**.

## Out of scope

- Persistencia de fingerprints entre reinicios (en-memory, limitado a la ventana activa)
- Alertas basadas en fingerprints (US futuro)
- Agrupación por mensaje de error en body (requeriría capturar el body del upstream)
- Dashboard visual para fingerprints (US futuro)
- Deduplicación entre ventanas (el fingerprint vive solo en la ventana actual)

## Dependencies

- US-02 storage.Record (method, path, status_code, duration_ms, timestamp)
- US-07 metrics.Engine, FindByWindow
- US-08 window query param
- US-26 path normalization (paths ya normalizados)
