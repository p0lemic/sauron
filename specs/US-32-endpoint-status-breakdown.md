# US-32 — Breakdown de Status por Endpoint

## Context

US-28 añadió un breakdown global de status codes (2xx/3xx/4xx/5xx). US-32 extiende
esa funcionalidad para filtrar por endpoint concreto, permitiendo ver la distribución
de status de un endpoint específico.

## Behavior

### 1. Extender `GET /metrics/status`

Se añaden parámetros opcionales `method` y `path`. Cuando ambos están presentes,
los registros se filtran a ese endpoint antes de agrupar.

```
GET /metrics/status?method=GET&path=/api/users/:id&from=<RFC3339>&to=<RFC3339>
```

- Si `method` y `path` están ausentes → comportamiento existente (global).
- Si solo uno está presente → 400 bad request ("method and path are required together").
- Respuesta: misma estructura `[]StatusGroup` de US-28.

### 2. Nuevo método en `metrics.Engine`

```go
// StatusBreakdownForEndpoint returns status breakdown filtered to one endpoint
// within the engine's default window.
func (e *Engine) StatusBreakdownForEndpoint(method, path string) ([]StatusGroup, error)

// StatusBreakdownForEndpointRange returns status breakdown filtered to one endpoint
// within [from, to).
func (e *Engine) StatusBreakdownForEndpointRange(method, path string, from, to time.Time) ([]StatusGroup, error)
```

Internamente: `FindByWindow` → filtrar por `method+path` en memoria → misma lógica
de agrupación que `StatusBreakdownForRange`.

### 3. Dashboard — tab "Status" en el panel de detalle

Al hacer clic en una fila de la tabla de endpoints, el panel lateral ya muestra
los tabs "Chart" e "Histogram". Se añade un tercer tab **"Status"**.

```
[ Chart ]  [ Histogram ]  [ Status ]
```

El tab "Status" muestra las mismas 4 tarjetas (2xx/3xx/4xx/5xx) de US-28
pero filtradas al endpoint seleccionado. Reutiliza los estilos `.status-grid`
y `.status-card` existentes.

La petición al API se hace cuando el usuario activa el tab (lazy load), no al
abrir el panel.

## API contract

| Endpoint | Params nuevos | Descripción |
|---|---|---|
| `GET /metrics/status` | `method`, `path` (opcionales, deben ir juntos) | Breakdown filtrado por endpoint |

## Test cases

### metrics (metrics/engine_test.go adiciones)

| TC    | Descripción                                                                    |
|-------|--------------------------------------------------------------------------------|
| TC-01 | StatusBreakdownForEndpoint filtra correctamente por method+path                |
| TC-02 | StatusBreakdownForEndpoint con endpoint sin registros → 4 grupos count=0       |

### api (api/server_test.go adiciones)

| TC    | Descripción                                                                    |
|-------|--------------------------------------------------------------------------------|
| TC-03 | GET /metrics/status?method=GET&path=/a → breakdown filtrado                    |
| TC-04 | GET /metrics/status?method=GET (sin path) → 400                                |

## Files changed

| File                             | Change                                                            |
|----------------------------------|-------------------------------------------------------------------|
| `metrics/engine.go`              | `StatusBreakdownForEndpoint`, `StatusBreakdownForEndpointRange`   |
| `metrics/engine_test.go`         | TC-01..TC-02                                                      |
| `api/server.go`                  | Extender `handleStatus` con params `method`/`path`                |
| `api/server_test.go`             | TC-03..TC-04                                                      |
| `api/dashboard/static/app.js`    | Tab "Status" en `fetchLatency`, `renderStatusTab(groups)`         |

No cambia: `storage/`, `index.html`, `style.css` (reutiliza estilos de US-28).

## Out of scope

- Breakdown por status code individual (200, 404…)
- Tabla separada de status por endpoint en la sección principal
- Tendencia temporal de status codes
