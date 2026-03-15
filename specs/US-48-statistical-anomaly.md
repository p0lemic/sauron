# US-48 — Detección estadística de anomalías

**Épica:** Observabilidad avanzada
**Prioridad:** Should
**Historia:** Como operator, el sistema detecta automáticamente endpoints con comportamiento anómalo comparando su P99 actual contra la media y desviación estándar de su historial reciente, sin necesidad de configurar umbrales manuales.
**AC:** Nuevo kind de alerta `statistical`. Score de anomalía (z-score) visible en la tabla de endpoints del dashboard.

---

## Context

US-14 implementa detección basada en multiplicador de baseline (`current_p99 > N × baseline_p99`). Esto requiere que el operator calibre `anomaly_threshold` para cada contexto. US-48 añade una detección **sin configuración**: usa la media y desviación estándar del historial para calcular un z-score, disparando una alerta cuando la desviación supera un umbral expresado en sigmas.

Ambos mecanismos coexisten. US-48 añade un **nuevo kind** (`statistical`) y no modifica los existentes.

---

## Behavior

### Z-score por endpoint

Para cada endpoint `(method, path)`:

```
z = (current_p99 - mean_p99) / stddev_p99
```

Donde:
- `current_p99` — P99 de la ventana activa del motor
- `mean_p99` y `stddev_p99` — calculados sobre las últimas `statistical_windows` ventanas del baseline

Condición de disparo:
```
z > sensitivity  AND  stddev_p99 > 0  AND  baseline_windows >= 3
```

Defaults:
- `sensitivity: 2.0` (equivale a "más de 2σ sobre la media")
- `statistical_windows: 10` (número de ventanas históricas para calcular media/stddev)

El requisito `baseline_windows >= 3` evita falsos positivos con datos insuficientes.

### Resolución

Auto-resolve cuando `z <= sensitivity`. Mismo mecanismo que US-14.

### Alert struct — campo adicional

```go
// Statistical-specific (zero for other kinds).
ZScore      float64 `json:"z_score"`
MeanP99     float64 `json:"mean_p99"`
StddevP99   float64 `json:"stddev_p99"`
Sensitivity float64 `json:"sensitivity"`
```

### Dashboard — columna AnomalyScore en endpoints table

Añadir columna `Z-Score` a la tabla de endpoints, visible cuando hay datos estadísticos disponibles. Color:
- `z < 1.5` — sin color (normal)
- `1.5 ≤ z < 2.0` — warning
- `z ≥ 2.0` — danger

Nuevo endpoint `GET /metrics/anomaly-scores` que retorna z-score por endpoint para que el dashboard los muestre sin esperar a que se dispare una alerta.

---

## API contract

### `GET /metrics/anomaly-scores`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/reports",
    "current_p99": 850.0,
    "mean_p99": 120.0,
    "stddev_p99": 30.0,
    "z_score": 24.3,
    "has_baseline": true
  }
]
```

Array vacío si no hay baseline calculado aún.
Ordenado por `z_score` descendente.

**Response 405** — método no GET.

---

## Cambios en código

### `config/config.go`

```go
AnomalySensitivity float64  // default: 2.0; YAML: anomaly_sensitivity; flag: --anomaly-sensitivity
StatisticalWindows int      // default: 10;  YAML: statistical_windows;  flag: --statistical-windows
```

Validación: `AnomalySensitivity > 0`, `StatisticalWindows >= 3`.

### `metrics/engine.go` — nuevo método

```go
// AnomalyScore holds the statistical anomaly data for one endpoint.
type AnomalyScore struct {
    Method      string  `json:"method"`
    Path        string  `json:"path"`
    CurrentP99  float64 `json:"current_p99"`
    MeanP99     float64 `json:"mean_p99"`
    StddevP99   float64 `json:"stddev_p99"`
    ZScore      float64 `json:"z_score"`
    HasBaseline bool    `json:"has_baseline"`
}

// AnomalyScores computes z-scores for all endpoints using the last n windows.
// Endpoints with fewer than 3 baseline windows return HasBaseline=false and ZScore=0.
func (e *Engine) AnomalyScores(n int) ([]AnomalyScore, error)
```

Implementación:
1. Llama a `engine.Table()` → current P99 por endpoint.
2. Llama a `engine.Baseline(n)` → P99 histórico medio. Necesita extender `BaselineStat` con `StddevP99 float64`.
3. Calcula z-score para cada endpoint con datos suficientes.
4. Ordena por z-score descendente.

### `metrics/engine.go` — extensión de `Baseline`

Añadir `StddevP99 float64` a `BaselineStat`:
```go
type BaselineStat struct {
    // ... campos existentes ...
    StddevP99 float64 `json:"stddev_p99"`
}
```

### `alerts/detector.go` — nuevo campo y check estadístico

```go
sensitivity        float64
statisticalWindows int
```

Nuevo método `SetStatisticalParams(sensitivity float64, windows int)`.

En `Evaluate()`, nuevo bloque statistical check:
```go
scores, err := d.engine.AnomalyScores(d.statisticalWindows)
for _, s := range scores {
    if s.HasBaseline && s.ZScore > d.sensitivity {
        // crear/actualizar alerta KindStatistical
    }
}
```

```go
const KindStatistical = "statistical"
```

### `api/server.go`

Registrar `GET /metrics/anomaly-scores` → `handleAnomalyScores`.

### Dashboard

- Columna `Z` en endpoints table (de `/metrics/anomaly-scores`).
- Badge de kind `statistical` en la tabla de alertas activas/historial.

---

## Test cases

### `metrics.Engine` — AnomalyScores

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Sin baseline | HasBaseline=false, ZScore=0 |
| TC-02 | P99 actual == media | ZScore ≈ 0 |
| TC-03 | P99 actual = media + 3×stddev | ZScore ≈ 3.0 |
| TC-04 | stddev = 0 (todos los valores iguales) | ZScore = 0, no panic |
| TC-05 | Ordenado por ZScore desc | Primer elemento tiene mayor ZScore |

### `alerts.Detector` — statistical alerts

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-06 | ZScore > sensitivity → alerta KindStatistical | Alert.Kind == "statistical" |
| TC-07 | ZScore ≤ sensitivity | Sin alerta |
| TC-08 | HasBaseline=false | Sin alerta |
| TC-09 | Alerta statistical se auto-resuelve cuando ZScore baja | Active() vacío |
| TC-10 | Notifier llamado exactamente 1 vez en nueva alerta statistical | |

### `api` — GET /metrics/anomaly-scores

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-11 | Sin datos | 200, [] |
| TC-12 | Con scores | 200, JSON con campos correctos |
| TC-13 | POST | 405 |

---

## Out of scope

- Anomaly detection para error_rate o RPS (solo P99 en esta historia).
- Persistencia de scores históricos.
- Configuración por endpoint (mismo sensitivity para todos).

---

## Dependencies

- US-13: `engine.Baseline(n)` implementado.
- US-14: `alerts.Detector`, `KindLatency`, estructura de `Evaluate()`.
- US-40/US-41: `Alert.Kind` field.
