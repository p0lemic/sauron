# US-40 — Alertas por Error Rate

## Context

Las alertas actuales solo detectan anomalías de latencia (P99). Añadir alertas cuando
el error rate de un endpoint supera un umbral configurable permite detectar degradaciones
de calidad antes de que los usuarios lo reporten.

## Behavior

### 1. Constantes y tipos en `alerts`

```go
const (
    KindLatency   = "latency"
    KindErrorRate = "error_rate"
)
```

### 2. Extender `Alert` y `AlertRecord`

Añadir campo `Kind` a ambos structs:

```go
type Alert struct {
    Kind                string    `json:"kind"`                  // "latency" | "error_rate"
    Method              string    `json:"method"`
    Path                string    `json:"path"`
    // latency-specific (cero en alertas error_rate)
    CurrentP99          float64   `json:"current_p99"`
    BaselineP99         float64   `json:"baseline_p99"`
    Threshold           float64   `json:"threshold"`
    // error_rate-specific (cero en alertas latency)
    ErrorRate           float64   `json:"error_rate"`
    ErrorRateThreshold  float64   `json:"error_rate_threshold"`
    TriggeredAt         time.Time `json:"triggered_at"`
}
```

`AlertRecord` recibe el mismo campo `Kind`.

### 3. Detector — `SetErrorRateThreshold`

```go
func (d *Detector) SetErrorRateThreshold(pct float64)
```

- `pct` es un porcentaje (e.g. `10.0` = 10%). Valor `0` significa **deshabilitado**.
- Llamar antes de `Start()`.
- Patrón consistente con `SetNotifier`.

### 4. Clave de alerta activa — incluye `kind`

Clave del mapa `d.active`: `"kind:METHOD:path"` (e.g. `"latency:GET:/x"`, `"error_rate:GET:/x"`).

Esto permite que un endpoint tenga simultáneamente una alerta de latencia **y** una de
error rate. Las silencias siguen usando `"METHOD:path"` y suprimen **todos** los tipos
para ese endpoint.

### 5. `Evaluate()` — detección de error rate

Después del bloque de latencia existente, para cada endpoint:
```
if errorRateThreshold > 0 && ep.ErrorRate > errorRateThreshold:
    aKey = "error_rate:METHOD:path"
    si silenciado → skip
    upsert en d.active con Kind="error_rate", ErrorRate, ErrorRateThreshold
```

### 6. Config — `ErrorRateThreshold`

```go
type Config struct {
    ...
    ErrorRateThreshold float64 // porcentaje; 0 = deshabilitado
}
```

```yaml
error_rate_threshold: 10   # fire cuando error_rate > 10%
```

Default: `0` (deshabilitado).

### 7. CLI flag en `cmd/dashboard`

```
--error-rate-threshold float  error rate threshold % to trigger alert (0 = disabled)
```

### 8. Wiring en `cmd/dashboard/main.go`

```go
if cfg.ErrorRateThreshold > 0 {
    detector.SetErrorRateThreshold(cfg.ErrorRateThreshold)
}
```

### 9. Dashboard — kind badge en tabla de alertas

La tabla de alertas activas muestra una columna "Kind" con un badge:
- `latency` → badge azul
- `error_rate` → badge naranja

Para alertas `error_rate`, mostrar `error_rate%` en lugar de `current_p99`.

## API contract

| Endpoint          | Cambio                                                            |
|-------------------|-------------------------------------------------------------------|
| `GET /alerts/active`  | `Alert` ahora incluye `kind`, `error_rate`, `error_rate_threshold` |
| `GET /alerts/history` | `AlertRecord` incluye `kind`                                    |

## Test cases

### alerts (alerts/detector_test.go)

| TC    | Descripción                                                                       |
|-------|-----------------------------------------------------------------------------------|
| TC-01 | `ErrorRateThreshold=0` → deshabilitado, no alerta aunque error_rate = 100%       |
| TC-02 | `error_rate > threshold` → alerta con `kind="error_rate"` en `Active()`          |
| TC-03 | `error_rate <= threshold` → sin alerta                                            |
| TC-04 | Ambas condiciones (latency + error_rate) → 2 alertas activas para mismo endpoint |
| TC-05 | Error rate baja → alerta error_rate se auto-resuelve                              |

## Files changed

| File                          | Change                                                             |
|-------------------------------|--------------------------------------------------------------------|
| `alerts/detector.go`          | `Kind` constantes, campo en `Alert`, `SetErrorRateThreshold`, `Evaluate` extendido, clave con kind |
| `alerts/history.go`           | Campo `Kind` en `AlertRecord`                                      |
| `alerts/detector_test.go`     | TC-01..TC-05 (error_rate)                                          |
| `config/config.go`            | `ErrorRateThreshold float64`, YAML, Merge                          |
| `cmd/dashboard/main.go`       | Flag `--error-rate-threshold`, llamada a `SetErrorRateThreshold`   |
| `api/dashboard/static/app.js` | Kind badge en tabla de alertas; mostrar error_rate en alertas correspondientes |
| `api/dashboard/static/style.css` | `.kind-badge`, `.kind-latency`, `.kind-error-rate` styles       |

No cambia: `storage/`, `metrics/`, `proxy/`, `normalizer/`, `health/`, `api/server.go`.

## Out of scope

- Umbral de error rate diferente por endpoint
- Alertas por error rate a nivel global (solo por endpoint)
- Nueva ruta `/alerts/active?kind=` para filtrar por tipo
- Notificaciones diferenciadas por tipo de alerta
