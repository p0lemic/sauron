# US-09 — Top endpoints más lentos

**Épica:** Métricas y agregación (Fase 2)
**Prioridad:** Must
**Historia:** Como developer, puedo ver un ranking de los N endpoints con mayor p99.
**AC:** `GET /metrics/slowest?n=10` devuelve lista ordenada.

---

## Context

`GET /metrics/endpoints` ya devuelve todos los endpoints ordenados por P99 descendente. US-09 expone una vista recortada: los top N por P99, útil para diagnosticar rápidamente los cuellos de botella sin procesar la lista completa.

La lógica de cálculo, agrupación y ordenación ya existe en `metrics.Engine`. Esta historia solo añade:
1. Un método `Slowest(n int)` en `metrics.Engine` que devuelve los primeros N resultados.
2. El endpoint `GET /metrics/slowest` en `api.Server` con el parámetro `?n=`.

---

## Behavior

### Parámetro `?n=`

- **Tipo:** entero positivo.
- **Por defecto:** `10` (si se omite el parámetro).
- `?n=0` → `400 Bad Request`.
- `?n=-5` → `400 Bad Request`.
- `?n=abc` → `400 Bad Request`.
- Si `n` es mayor que el número de endpoints disponibles, se devuelven todos los disponibles (sin error).

### Ordenación

La lista está ordenada por **P99 descendente** (más lento primero), igual que `Endpoints()`. El recorte a `n` se aplica después de ordenar.

### Sin datos

Si no hay registros en la ventana activa, se devuelve `[]` con status 200.

---

## API contract

### `GET /metrics/slowest`

**Query params:**

| Param | Tipo | Por defecto | Descripción |
|-------|------|-------------|-------------|
| `n` | int > 0 | `10` | Número máximo de endpoints a devolver |

**Response 200 OK** (mismo schema que `/metrics/endpoints`):
```json
[
  {
    "method": "POST",
    "path": "/api/checkout",
    "p50": 95.0,
    "p95": 310.0,
    "p99": 520.0,
    "count": 38
  },
  {
    "method": "GET",
    "path": "/api/reports",
    "p50": 40.0,
    "p95": 180.0,
    "p99": 290.0,
    "count": 142
  }
]
```

**Response 400 — `n` inválido:**
```json
{"error": "invalid n \"abc\": strconv.Atoi: parsing \"abc\": invalid syntax"}
```
`Content-Type: application/json`.

**Response 405 — método incorrecto:**
`POST /metrics/slowest` → `405 Method Not Allowed`.

---

## Cambios en código

### `metrics/engine.go`

```go
// Slowest returns the top n endpoints by P99 descending within the engine's window.
// If there are fewer than n endpoints, all are returned.
// n must be > 0.
func (e *Engine) Slowest(n int) ([]EndpointStat, error)
```

Internamente llama a `Endpoints()` y recorta el slice a los primeros `n` elementos.

### `api/server.go`

- Registrar `/metrics/slowest` en el mux.
- Parsear `?n=` con `strconv.Atoi`; si no está presente, usar `10`.
- Si `n <= 0` → `400`.
- Llamar `engine.Slowest(n)` y serializar.

---

## Test cases

### `metrics.Engine.Slowest`

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-01 | 5 endpoints, `n=3` | Devuelve los 3 con mayor P99 |
| TC-02 | 3 endpoints, `n=10` | Devuelve los 3 disponibles (sin error) |
| TC-03 | 0 registros, `n=5` | Devuelve slice vacío (no nil), sin error |
| TC-04 | 5 endpoints, `n=1` | Devuelve solo el endpoint con mayor P99 |
| TC-05 | 5 endpoints, `n=5` | Igual que `Endpoints()` |

### `api.Server` — `GET /metrics/slowest`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-06 | `?n=3`, 5 endpoints disponibles | 200, array con 3 elementos ordenados por P99 desc |
| TC-07 | Sin `?n=`, 5 endpoints disponibles | 200, array con los primeros 10 (o todos si < 10) |
| TC-08 | `?n=100`, 3 endpoints disponibles | 200, array con los 3 disponibles |
| TC-09 | `?n=abc` | 400, JSON `{"error": "..."}` |
| TC-10 | `?n=0` | 400, JSON `{"error": "..."}` |
| TC-11 | `?n=-1` | 400, JSON `{"error": "..."}` |
| TC-12 | Sin datos | 200, `[]` |
| TC-13 | `POST /metrics/slowest` | 405 Method Not Allowed |
| TC-14 | Content-Type | `application/json` en respuesta 200 |

---

## Out of scope

- **Parámetro `?window=`** en este endpoint — consistencia con US-08 queda para futura iteración si se necesita.
- **Paginación** — no en el PRD.
- **Filtrado por método o path** — no en el PRD.

---

## Dependencies

- **US-07** implementado: `metrics.Engine.Endpoints()`, `api.Server`.
- **US-08** implementado: `writeJSONError`, `parseDuration` (reutilizados desde `api/server.go`).
- Sin nuevas dependencias externas.
