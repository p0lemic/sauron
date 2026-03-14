# US-27 — Request Log

## Context

El dashboard muestra métricas agregadas por endpoint pero no permite ver requests
individuales. Un request log facilita el debugging: ver exactamente qué requests llegaron,
cuándo, y cuánto tardaron — sin necesidad de buscar en logs de servidor.

## Behavior

### 1. Nueva query en storage — `FindRecent`

Se añade `FindRecent` a la interfaz `Reader` y se implementa en ambos drivers:

```go
// FindRecent returns up to `limit` records in [from, to) ordered newest-first.
FindRecent(from, to time.Time, limit int) ([]Record, error)
```

SQL (SQLite):
```sql
SELECT timestamp, method, path, status_code, duration_ms
FROM requests
WHERE timestamp >= ? AND timestamp < ?
ORDER BY timestamp DESC
LIMIT ?
```

SQL (PostgreSQL): misma semántica, placeholders `$1/$2/$3`.

### 2. Nuevo método en `metrics.Engine`

```go
// Requests returns the most recent n requests within the default window.
func (e *Engine) Requests(n int) ([]storage.Record, error)

// RequestsForRange returns the most recent n requests in [from, to).
func (e *Engine) RequestsForRange(from, to time.Time, n int) ([]storage.Record, error)
```

`n` se coarta en `[1, 1000]` internamente para evitar respuestas masivas.

### 3. Nuevo endpoint HTTP

```
GET /metrics/requests?n=100&from=<RFC3339>&to=<RFC3339>
```

- `n`: número de registros, default `100`, máx `1000`.
- `from` / `to`: opcionales, mismo comportamiento que `parseTimeRange`.
- Respuesta: `[]storage.Record` serializado a JSON.

```json
[
  {
    "timestamp": "2026-03-13T12:00:01.234Z",
    "method":    "GET",
    "path":      "/users/:id",
    "status_code": 200,
    "duration_ms": 12.5
  },
  ...
]
```

### 4. Dashboard UI — sección "Request Log"

Nueva sección `<section id="request-log">` entre "Endpoints" y el final de `<main>`.

**Controles:**
- Input de texto "Filter path" — filtra client-side por substring en `path`
- Select "Method" — All / GET / POST / PUT / PATCH / DELETE
- Select "Status" — All / 2xx / 3xx / 4xx / 5xx

**Tabla:**

| Time | Method | Path | Status | Duration |
|------|--------|------|--------|----------|

- `Time`: hora local en formato `HH:MM:SS.mmm`
- `Method`: badge coloreado (GET azul, POST verde, DELETE rojo, etc.)
- `Status`: celda coloreada (2xx verde, 3xx amarillo, 4xx naranja, 5xx rojo)
- `Duration`: en `ms` con 1 decimal
- Ordenada más reciente primero (la API ya devuelve DESC)
- Máximo 100 filas visibles (el servidor devuelve las 100 más recientes)

La sección se actualiza en cada ciclo de `refresh()` en modo live, y una vez en modo histórico.

## API contract

| Endpoint | Método | Descripción |
|---|---|---|
| `GET /metrics/requests` | GET | Retorna las N requests más recientes |

Parámetros query: `n` (int, default 100, max 1000), `from` (RFC3339), `to` (RFC3339 o "now").

## Test cases

### storage (storage/reader_test.go adiciones)

| TC    | Descripción                                                                   |
|-------|-------------------------------------------------------------------------------|
| TC-01 | FindRecent devuelve registros más recientes primero (DESC)                    |
| TC-02 | FindRecent respeta el límite: si hay 10 registros y limit=3, devuelve 3       |
| TC-03 | FindRecent respeta el rango [from, to)                                        |
| TC-04 | FindRecent sin registros → slice vacío (no nil)                               |

### metrics (metrics/engine_test.go adiciones)

| TC    | Descripción                                                                   |
|-------|-------------------------------------------------------------------------------|
| TC-05 | Requests(n) devuelve los n más recientes                                      |
| TC-06 | Requests: n coartado a máximo 1000                                            |
| TC-07 | RequestsForRange respeta el rango de tiempo                                   |

### api (api/server_test.go adiciones)

| TC    | Descripción                                                                   |
|-------|-------------------------------------------------------------------------------|
| TC-08 | GET /metrics/requests → 200, array JSON                                       |
| TC-09 | GET /metrics/requests?n=5 → máximo 5 registros                               |
| TC-10 | GET /metrics/requests?n=9999 → coartado a 1000                               |
| TC-11 | GET /metrics/requests?n=abc → 400 bad request                                 |

## Files changed

| File                          | Change                                                     |
|-------------------------------|------------------------------------------------------------|
| `storage/reader.go`           | Añadir `FindRecent` a interfaz + implementación SQLite     |
| `storage/postgres.go`         | Implementación PostgreSQL de `FindRecent`                  |
| `storage/reader_test.go`      | TC-01..TC-04                                               |
| `metrics/engine.go`           | `Requests`, `RequestsForRange`                             |
| `metrics/engine_test.go`      | TC-05..TC-07                                               |
| `api/server.go`               | Ruta `GET /metrics/requests` + handler                     |
| `api/server_test.go`          | TC-08..TC-11                                               |
| `api/dashboard/static/app.js` | `fetchRequests()`, nueva sección en UI                     |
| `api/dashboard/index.html`    | `<section id="request-log">`                               |
| `api/dashboard/static/style.css` | Estilos para request log table y badges                 |

No cambia: `normalizer/`, `alerts/`, `proxy/`, `config/`, `storage/store.go`, `cmd/`.

## Out of scope

- Paginación (más allá de los 100 más recientes)
- Filtrado server-side por method/status (se hace client-side)
- Captura de body o headers de la request
- Exportar el log a CSV desde la UI
