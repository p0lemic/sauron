# US-39 — Health Check del Upstream

## Context

El dashboard no tiene visibilidad de si el upstream está respondiendo. Un health check
periódico permite detectar caídas antes de que los usuarios lo reporten y exponer el
estado en `/health` y en la UI.

## Behavior

### 1. Nuevo paquete `health`

```go
// Status representa el estado del upstream.
type Status string

const (
    StatusUnknown  Status = "unknown"   // sin checks suficientes aún
    StatusHealthy  Status = "healthy"   // 0 fallos consecutivos
    StatusDegraded Status = "degraded"  // 1..threshold-1 fallos consecutivos
    StatusDown     Status = "down"      // >= threshold fallos consecutivos
)

// Result es el resultado de un único ping.
type Result struct {
    Time      time.Time
    Latency   time.Duration
    Err       error
}

// State es el snapshot del estado actual del checker.
type State struct {
    Status             Status    `json:"status"`
    LatencyMs          float64   `json:"latency_ms"`           // del último check exitoso; 0 si no hay
    LastCheck          time.Time `json:"last_check"`
    ConsecutiveFailures int      `json:"consecutive_failures"`
}

// Checker hace pings periódicos a una URL y mantiene el estado.
type Checker struct { ... }

func New(target string, interval, timeout time.Duration, threshold int) *Checker
func (c *Checker) Start()
func (c *Checker) Stop()
func (c *Checker) State() State
```

- `target`: URL completa a la que se hace HEAD request (e.g. `http://upstream/health`).
- `interval`: tiempo entre checks (default 10s).
- `timeout`: timeout del ping (default 5s).
- `threshold`: fallos consecutivos para pasar a `down` (default 3).
- Estado inicial: `unknown`.
- Transiciones:
  - Éxito → `consecutive_failures = 0` → `healthy`
  - Fallo → `consecutive_failures++`:
    - `< threshold` → `degraded`
    - `>= threshold` → `down`

### 2. Config — bloque `health_check` en YAML

```go
type HealthCheckConfig struct {
    Enabled   bool
    Path      string        // ruta del upstream a pinguear (default: "/")
    Interval  time.Duration // default: 10s
    Timeout   time.Duration // default: 5s
    Threshold int           // fallos consecutivos para "down" (default: 3)
}
```

```yaml
health_check:
  enabled: true
  path: /health
  interval: 10s
  timeout: 5s
  threshold: 3
```

### 3. Extender `GET /health` en `api.Server`

`api.NewServer` recibe un `*health.Checker` opcional (nil = deshabilitado).

Sin checker:
```json
{"status": "ok"}
```

Con checker:
```json
{
  "status": "ok",
  "upstream": {
    "status": "healthy",
    "latency_ms": 12.5,
    "last_check": "2026-03-14T10:00:00Z",
    "consecutive_failures": 0
  }
}
```

### 4. Dashboard UI — indicador en status bar

`<span id="upstream-health">` añadido a la status bar:

```
● connected   upstream: ● healthy  12ms   poll · 5s   last refresh 10:00:01
```

- Color del indicador: verde (`healthy`), amarillo (`degraded`), rojo (`down`), gris (`unknown`).
- El dashboard llama a `GET /health` en cada ciclo `refresh()` y actualiza el indicador.

### 5. Wiring en `cmd/dashboard/main.go`

Si `cfg.HealthCheck.Enabled` y `cfg.Upstream != ""`:
- Construir `target = cfg.Upstream + cfg.HealthCheck.Path`
- Crear `health.New(target, interval, timeout, threshold)`
- `checker.Start()` antes del servidor
- `checker.Stop()` en shutdown
- Pasar checker a `api.NewServer`

## API contract

| Endpoint | Cambio |
|---|---|
| `GET /health` | Respuesta extendida con `upstream` cuando checker está activo |

## Test cases

### health (health/checker_test.go)

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-01 | Estado inicial es `unknown`                                              |
| TC-02 | Ping exitoso → `healthy`, latency > 0                                   |
| TC-03 | Fallos < threshold → `degraded`                                          |
| TC-04 | Fallos >= threshold → `down`                                             |
| TC-05 | Tras `down`, ping exitoso → `healthy` (reset)                           |

### api (api/server_test.go adiciones)

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-06 | GET /health sin checker → `{"status":"ok"}` (comportamiento actual)     |
| TC-07 | GET /health con checker → incluye campo `upstream`                       |

## Files changed

| File                          | Change                                                        |
|-------------------------------|---------------------------------------------------------------|
| `health/checker.go`           | Nuevo paquete: `Checker`, `State`, `Status`                   |
| `health/checker_test.go`      | TC-01..TC-05                                                  |
| `config/config.go`            | `HealthCheckConfig` struct, campo en `Config`, YAML parsing   |
| `api/server.go`               | `*health.Checker` en `NewServer`, extender `handleHealth`     |
| `api/server_test.go`          | TC-06..TC-07                                                  |
| `cmd/dashboard/main.go`       | Crear y arrancar checker si configurado                       |
| `api/dashboard/index.html`    | `<span id="upstream-health">` en status bar                   |
| `api/dashboard/static/app.js` | `fetchHealth()`, actualizar status bar, añadir a `refresh()`  |
| `api/dashboard/static/style.css` | Estilos del indicador de upstream                          |

No cambia: `storage/`, `metrics/`, `alerts/`, `proxy/`, `normalizer/`.

## Out of scope

- Health check en el proxy binary (solo en dashboard)
- Histórico de health checks
- Notificaciones/alertas por caída del upstream
- Health check de la propia DB de storage
