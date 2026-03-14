# US-41 — Alerta por Throughput Drop

## Context

Las alertas existentes cubren latencia (P99) y error rate. Cuando el proxy o el cliente
sufren una caída, el síntoma más directo es una bajada brusca del RPS. Detectarlo
permite alertar antes de que la latencia suba o los errores aparezcan.

## Behavior

### 1. Extender `BaselineStat` con `BaselineRPS`

```go
type BaselineStat struct {
    Method      string  `json:"method"`
    Path        string  `json:"path"`
    BaselineP99 float64 `json:"baseline_p99"`
    BaselineRPS float64 `json:"baseline_rps"` // NEW: req/s promedio en la ventana baseline
    SampleCount int     `json:"sample_count"`
}
```

Cálculo en `Baseline(n int)`: la ventana baseline abarca `n * window` segundos, por tanto:

```
BaselineRPS = float64(SampleCount) / (float64(n) * e.window.Seconds())
```

### 2. Nueva constante de kind

```go
const KindThroughput = "throughput"
```

### 3. Extender `Alert` y `AlertRecord`

```go
// throughput-specific (cero para otros kinds)
CurrentRPS  float64 `json:"current_rps"`
BaselineRPS float64 `json:"baseline_rps"`
DropPct     float64 `json:"drop_pct"` // umbral configurado (e.g. 50.0)
```

### 4. `SetThroughputDropThreshold(pct float64)` en Detector

- `pct` es el porcentaje mínimo del baseline que debe mantenerse (e.g. `50.0`).
- `0` = deshabilitado. Patrón consistente con `SetErrorRateThreshold`.

### 5. Lógica de detección en `Evaluate()`

Se añade un bloque de comprobación de throughput **después** del de error rate.

**Iteración**: sobre los endpoints del baseline (no del current), para detectar también
los endpoints con 0 tráfico actual:

```
si throughputDropThreshold > 0:
  currentRPSMap := buildMap(Throughput())   // endpoint → RPSAvg; 0 si ausente
  para cada b en baselines:
    si b.BaselineRPS == 0: skip             // evitar falsos positivos en endpoints nuevos
    current = currentRPSMap[b.Method:b.Path]  // 0 si no hay tráfico
    si silenciado: skip
    si current < b.BaselineRPS * throughputDropThreshold/100:
        aKey = "throughput:METHOD:path"
        upsert en d.active con Kind="throughput"
```

Auto-resolve: igual que los otros kinds — si no está en `triggered`, se elimina de `d.active`.

### 6. Config

```go
type Config struct {
    ...
    ThroughputDropThreshold float64 // porcentaje; 0 = deshabilitado
}
```

```yaml
throughput_drop_threshold: 50  # alerta cuando RPS < 50% del baseline
```

Default: `0` (deshabilitado).

### 7. CLI flag en `cmd/dashboard`

```
--throughput-drop-threshold float  min RPS % of baseline before alerting (0 = disabled)
```

### 8. Wiring

```go
if cfg.ThroughputDropThreshold > 0 {
    detector.SetThroughputDropThreshold(cfg.ThroughputDropThreshold)
}
```

### 9. Dashboard — kind badge en tabla de alertas

Añadir caso `kind === 'throughput'` al renderizado de filas:
- Badge verde oscuro (color `--ok` dim)
- Columnas: `current_rps` req/s / `baseline_rps` req/s / drop%

## API contract

| Endpoint              | Cambio                                                              |
|-----------------------|---------------------------------------------------------------------|
| `GET /metrics/baseline` | `BaselineStat` ahora incluye `baseline_rps`                      |
| `GET /alerts/active`  | Puede devolver alertas con `kind:"throughput"`, `current_rps`, etc.|
| `GET /alerts/history` | Ídem                                                                |

## Test cases

### alerts (alerts/detector_test.go)

| TC    | Descripción                                                                         |
|-------|-------------------------------------------------------------------------------------|
| TC-01 | `ThroughputDropThreshold=0` → deshabilitado                                         |
| TC-02 | RPS actual < threshold% del baseline → alerta `kind="throughput"`                  |
| TC-03 | RPS actual >= threshold% del baseline → sin alerta                                  |
| TC-04 | Endpoint desaparece del tráfico actual (0 RPS) → alerta si tenía baseline          |
| TC-05 | RPS se recupera → alerta auto-resuelta                                              |

### metrics (metrics/engine_test.go)

| TC    | Descripción                                                                         |
|-------|-------------------------------------------------------------------------------------|
| TC-06 | `Baseline()` devuelve `BaselineRPS > 0` cuando hay registros en la ventana         |
| TC-07 | `Baseline()` con 0 registros → `BaselineRPS = 0`                                   |

## Files changed

| File                             | Change                                                           |
|----------------------------------|------------------------------------------------------------------|
| `metrics/engine.go`              | `BaselineRPS` en `BaselineStat`, cálculo en `Baseline()`        |
| `metrics/engine_test.go`         | TC-06, TC-07                                                     |
| `alerts/detector.go`             | `KindThroughput`, campos en `Alert`, `SetThroughputDropThreshold`, `Evaluate` extendido |
| `alerts/history.go`              | Campos `CurrentRPS`, `BaselineRPS`, `DropPct` en `AlertRecord`  |
| `alerts/detector_test.go`        | TC-01..TC-05 (throughput)                                        |
| `config/config.go`               | `ThroughputDropThreshold`, YAML, Merge                           |
| `cmd/dashboard/main.go`          | Flag + wiring                                                    |
| `api/dashboard/static/app.js`    | Case `throughput` en renderizado de alertas                      |
| `api/dashboard/static/style.css` | `.kind-throughput` badge style                                   |

No cambia: `storage/`, `proxy/`, `normalizer/`, `health/`, `api/server.go`.

## Out of scope

- Umbral diferente por endpoint
- Throughput drop global (solo por endpoint)
- Detección de spike (subida repentina de RPS)
