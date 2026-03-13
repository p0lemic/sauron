# US-07 — Percentiles por endpoint

**Épica:** Métricas y agregación (Fase 2)
**Prioridad:** Must
**Historia:** Como developer, puedo consultar p50, p95, p99 de latencia agrupados por método+path.
**AC:** API `GET /metrics/endpoints` devuelve JSON con percentiles calculados.

---

## Context

Los registros de requests ya se persisten en SQLite (US-02). US-07 expone esos datos como estadísticas accionables: percentiles de latencia por endpoint, agrupados por `método + path`, calculados sobre una ventana temporal deslizante (defecto: 1 minuto).

Esta historia introduce dos paquetes nuevos:
- `metrics` — motor de consulta y cálculo de percentiles.
- `api` — servidor HTTP interno que expone los endpoints REST para el dashboard y las queries.

---

## Behavior

### Cálculo de percentiles

1. Al recibir `GET /metrics/endpoints`, el servidor calcula los percentiles de todos los registros cuyo `timestamp` cae dentro de la ventana activa (`now - window` … `now`).
2. Los registros se agrupan por `(method, path)`.
3. Para cada grupo se calculan p50, p95, p99 y count sobre los valores de `duration_ms`.
4. **Algoritmo:** cálculo exacto — se obtienen los `duration_ms` del grupo, se ordenan y se usa indexación por percentil. Para el MVP es suficiente (ventana de 1 min, < 100k registros en RAM).
5. El resultado se ordena por p99 descendente (endpoints más lentos primero).

### Ventana temporal

- Defecto: últimos **60 segundos**.
- Configurable en US-08; para esta historia se expone como parámetro en `metrics.Engine` pero se fija a 60s desde `main`.

### Endpoint vacío o sin datos

- Si no hay registros en la ventana, `GET /metrics/endpoints` devuelve `[]` (array vacío), status 200.
- Si un endpoint tiene un único registro, p50 = p95 = p99 = ese único valor.

### Servidor API

- El servidor API escucha en un puerto separado al proxy (defecto: **9090**).
- En Fase 1, solo sirve el endpoint `/metrics/endpoints` (US-07) y `/health` (referenciado en NFR 4.4).
- Escucha solo en `localhost` por defecto (NFR 4.3).

---

## API contract

### `GET /metrics/endpoints`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/users",
    "p50": 12.5,
    "p95": 45.2,
    "p99": 89.1,
    "count": 142
  },
  {
    "method": "POST",
    "path": "/api/orders",
    "p50": 8.1,
    "p95": 31.0,
    "p99": 67.4,
    "count": 38
  }
]
```

Todos los valores de latencia en **milisegundos** (`float64`). Array vacío si no hay datos.

### `GET /health`

```json
{"status": "ok"}
```
Status 200. Sin lógica de negocio, solo confirma que el proceso está vivo.

---

### Interfaz `storage.Reader` (paquete `storage`)

Nueva interfaz de solo lectura que `sqliteStore` implementa:

```go
// Reader queries persisted request records.
type Reader interface {
    // FindByWindow returns all records with timestamp in [from, to).
    FindByWindow(from, to time.Time) ([]Record, error)
}
```

`sqliteStore` implementa `Reader` junto con `Store`. En `main`, el mismo objeto cumple ambas interfaces.

### Paquete `metrics`

```go
// Engine computes latency statistics from stored records.
type Engine struct { ... }

// NewEngine creates an Engine backed by the given Reader.
// window is the lookback duration for each calculation (e.g. 60s).
func NewEngine(reader storage.Reader, window time.Duration) *Engine

// EndpointStat holds the computed statistics for one method+path.
type EndpointStat struct {
    Method string  `json:"method"`
    Path   string  `json:"path"`
    P50    float64 `json:"p50"`
    P95    float64 `json:"p95"`
    P99    float64 `json:"p99"`
    Count  int     `json:"count"`
}

// Endpoints returns stats for all endpoints active in the current window,
// sorted by P99 descending.
func (e *Engine) Endpoints() ([]EndpointStat, error)
```

### Paquete `api`

```go
// Server is the internal HTTP server for metrics queries and the dashboard.
type Server struct { ... }

// NewServer creates a Server backed by the given Engine.
// addr is the listen address, e.g. "localhost:9090".
func NewServer(engine *metrics.Engine, addr string) *Server

// Start starts the server in a background goroutine.
// Returns error if the address is already in use.
func (s *Server) Start() error

// Shutdown gracefully stops the server (max 5s).
func (s *Server) Shutdown(ctx context.Context) error
```

### Integración en `config.Config` y `main.go`

```go
// New config field:
APIAddr string // default: "localhost:9090"
```

```
profiler --api-addr localhost:9090
```

YAML:
```yaml
api_addr: localhost:9090
```

---

## Test cases

### `metrics.Engine`

1. **Sin registros:** `Endpoints()` devuelve slice vacío, sin error.

2. **Un registro:** p50 = p95 = p99 = ese valor; count = 1.

3. **Múltiples registros, un endpoint:** Dados `[10, 20, 30, 40, 50, 60, 70, 80, 90, 100]` ms, p50 ≈ 50, p95 ≈ 95, p99 ≈ 99 (exactos por indexación).

4. **Agrupación por método+path:** `GET /users` y `POST /users` generan dos `EndpointStat` independientes con sus propias estadísticas.

5. **Agrupación por path:** `GET /users` y `GET /orders` generan dos entradas separadas.

6. **Registros fuera de la ventana ignorados:** Registros con `timestamp` anterior a `now - window` no se incluyen en el cálculo.

7. **Registros dentro de la ventana incluidos:** Registros con `timestamp` justo dentro de la ventana sí se incluyen.

8. **Ordenación por p99 desc:** El endpoint con mayor p99 aparece primero en el slice.

9. **Ventana configurable:** Con `window = 5s`, solo los registros de los últimos 5 segundos se usan.

### `api.Server` — `GET /metrics/endpoints`

10. **Response vacía:** Sin registros → `200 []`.

11. **Response con datos:** Registros presentes → `200` con array JSON correcto.

12. **Content-Type:** La respuesta tiene `Content-Type: application/json`.

13. **Método incorrecto:** `POST /metrics/endpoints` → `405 Method Not Allowed`.

### `api.Server` — `GET /health`

14. **Health OK:** `GET /health` → `200 {"status":"ok"}`.

### Edge cases

15. **Un solo registro por grupo:** p50 = p95 = p99 = ese valor.

16. **Dos registros:** `[10, 100]` — p50 = 10 o 100 (floor-index), p99 = 100.

17. **Latencias idénticas:** `[5, 5, 5]` — p50 = p95 = p99 = 5.

18. **Concurrent calls a `Endpoints()`:** Múltiples llamadas concurrentes no producen race conditions.

---

## Out of scope

- **Ventana configurable por flag/YAML** — cubierta en US-08.
- **Top N endpoints más lentos** — cubierta en US-09.
- **Error rate por endpoint** — cubierta en US-10.
- **Throughput (RPS)** — cubierto en US-11.
- **Dashboard HTML** — cubierto en US-18/US-20.
- **Autenticación del servidor API** — Fase 2.
- **DDSketch streaming** — el cálculo exacto es suficiente para MVP; DDSketch se puede adoptar en Fase 3 si el volumen lo requiere.

---

## Dependencies

- **US-02** implementado: `storage.Store`, `storage.Record`, tabla `requests` en SQLite.
- **US-03** implementado: `config.Config` y sistema de flags para añadir `--api-addr`.
- **Nueva dependencia de producción:** ninguna (`encoding/json`, `net/http`, `sort` de stdlib).
