# US-10 — Tasa de errores por endpoint

**Épica:** Métricas y agregación (Fase 2)
**Prioridad:** Must
**Historia:** Como developer, puedo ver el porcentaje de respuestas 4xx y 5xx por endpoint.
**AC:** `GET /metrics/errors` devuelve `error_rate` por endpoint.

---

## Context

Los registros en SQLite incluyen `status_code`. US-10 agrega esos datos por `(method, path)` dentro de la ventana activa y calcula la tasa de error: qué fracción de las peticiones terminó con 4xx o 5xx.

La infraestructura de lectura (`storage.Reader.FindByWindow`), motor (`metrics.Engine`) y servidor API (`api.Server`) ya existen. Esta historia añade:
1. Un nuevo struct `ErrorStat` y método `Errors()` en `metrics.Engine`.
2. El endpoint `GET /metrics/errors` en `api.Server`.

---

## Behavior

### Definición de error

Se considera **error** cualquier respuesta con `status_code >= 400`. Esto incluye:
- **4xx** — errores de cliente (400, 401, 403, 404…)
- **5xx** — errores de servidor (500, 502, 503…)

Las respuestas 1xx, 2xx y 3xx **no** son errores.

### Cálculo

Para cada grupo `(method, path)` dentro de la ventana:

```
error_rate = error_count / total_count * 100.0
```

- `error_rate` es un `float64` entre `0.0` y `100.0` (porcentaje).
- `error_count` = número de registros con `status_code >= 400`.
- `total_count` = total de registros del grupo.

### Ordenación

Los resultados se ordenan por `error_rate` **descendente** (endpoint con más errores primero). En caso de empate en `error_rate`, se ordena por `total_count` descendente como criterio secundario.

### Sin datos

Si no hay registros en la ventana, devuelve `[]` con status 200.

### Endpoints con 0% error

Se incluyen **todos** los endpoints activos en la ventana, incluso los que tienen `error_rate = 0.0`. Esto permite al developer ver que el endpoint existe y funciona correctamente.

---

## API contract

### `GET /metrics/errors`

**Response 200 OK:**
```json
[
  {
    "method": "POST",
    "path": "/api/checkout",
    "error_rate": 12.5,
    "error_count": 5,
    "total_count": 40
  },
  {
    "method": "GET",
    "path": "/api/users",
    "error_rate": 0.0,
    "error_count": 0,
    "total_count": 142
  }
]
```

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `method` | string | Método HTTP |
| `path` | string | Path del endpoint |
| `error_rate` | float64 | Porcentaje de errores (0.0–100.0) |
| `error_count` | int | Número de respuestas con status ≥ 400 |
| `total_count` | int | Total de requests del grupo en la ventana |

**Response 405 — método incorrecto:**
`POST /metrics/errors` → `405 Method Not Allowed`.

---

## Cambios en código

### `metrics/engine.go`

```go
// ErrorStat holds the error rate statistics for one method+path.
type ErrorStat struct {
    Method     string  `json:"method"`
    Path       string  `json:"path"`
    ErrorRate  float64 `json:"error_rate"`
    ErrorCount int     `json:"error_count"`
    TotalCount int     `json:"total_count"`
}

// Errors returns error rate stats for all endpoints active in the current window,
// sorted by ErrorRate descending (highest error rate first).
func (e *Engine) Errors() ([]ErrorStat, error)
```

### `api/server.go`

- Registrar `GET /metrics/errors` en el mux.
- Llamar `engine.Errors()` y serializar como JSON.

---

## Test cases

### `metrics.Engine.Errors`

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-01 | Sin registros | Slice vacío (no nil), sin error |
| TC-02 | 4 req: 2 OK (200), 2 error (500) | `error_rate=50.0`, `error_count=2`, `total_count=4` |
| TC-03 | 4 req: todos 200 | `error_rate=0.0`, endpoint incluido en resultado |
| TC-04 | 4 req: todos 500 | `error_rate=100.0` |
| TC-05 | Status 400 cuenta como error | `error_count` incluye 4xx |
| TC-06 | Status 399 no cuenta como error | Solo `>= 400` son errores |
| TC-07 | Dos endpoints: uno con errores, otro sin | Ordenados por `error_rate` desc; el de mayor tasa primero |
| TC-08 | Empate en `error_rate`, distinto `total_count` | El de mayor `total_count` primero |
| TC-09 | Mezcla 4xx y 5xx | Ambos cuentan como error; `error_count` suma ambos |

### `api.Server` — `GET /metrics/errors`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-10 | Sin datos | 200, `[]` |
| TC-11 | Registros presentes | 200, JSON con campos correctos |
| TC-12 | Content-Type | `application/json` |
| TC-13 | `POST /metrics/errors` | 405 Method Not Allowed |

---

## Out of scope

- **Desglose 4xx vs 5xx separado** — no en el PRD.
- **`?window=` en este endpoint** — consistencia con US-08 queda para futura iteración.
- **Umbral de alerta por error_rate** — cubierto en US-14.
- **Filtrado por rango de status** — no en el PRD.

---

## Dependencies

- **US-02** implementado: `storage.Record.StatusCode` disponible.
- **US-07** implementado: `metrics.Engine`, `storage.Reader.FindByWindow`.
- Sin nuevas dependencias externas.
