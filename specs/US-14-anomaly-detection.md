# US-14 — Alerta por latencia anómala

**Épica:** Detección de anomalías (Fase 3)
**Prioridad:** Must
**Historia:** Como operator, recibo una alerta cuando un endpoint supera N veces su baseline.
**AC:** Umbral configurable `anomaly_threshold` (defecto: 3x). Alerta emitida < 30s.

---

## Context

US-13 expone el baseline P99 de cada endpoint. US-14 compara continuamente el P99 actual (de la ventana activa) contra ese baseline y emite una alerta cuando se supera el umbral.

Esta historia introduce un nuevo paquete **`alerts`** con:
1. `Alert` — struct que describe una anomalía.
2. `Detector` — goroutine de fondo que evalúa condiciones periódicamente.
3. `GET /alerts/active` en `api.Server` — lista de alertas activas.
4. Config `anomaly_threshold` (YAML + flag).

US-15 añadirá la entrega vía webhook. US-14 solo detecta y expone en memoria.

---

## Behavior

### Condición de disparo

Para cada endpoint `(method, path)`:

```
current_p99 > anomaly_threshold * baseline_p99
```

Requisitos adicionales:
- El endpoint debe tener un baseline calculado (aparece en `Engine.Baseline(n)`).
- Si no hay baseline (endpoint nuevo sin historial), **no se emite alerta**.

### Condición de resolución (auto-clear)

Cuando en una comprobación posterior:
```
current_p99 <= anomaly_threshold * baseline_p99
```
La alerta se elimina del estado activo (auto-resolved). No persiste en historial (eso es US-17).

### Deduplicación

Cada endpoint tiene como máximo **una alerta activa**. Si el endpoint sigue anómalo en la siguiente comprobación, la alerta existente se **actualiza** (nuevos valores de `current_p99` y `triggered_at`) en lugar de crear una duplicada.

### Intervalo de comprobación

El `Detector` comprueba cada **10 segundos** (constante interna, no configurable en esta historia). Esto garantiza el AC "alerta emitida < 30s".

### Ciclo de evaluación

En cada tick:
1. Llama a `engine.Endpoints()` → mapa de `(method+path) → current_p99`.
2. Llama a `engine.Baseline(baselineWindows)` → mapa de `(method+path) → baseline_p99`.
3. Para cada endpoint con baseline:
   - Si `current_p99 > threshold * baseline_p99` → insertar/actualizar alerta activa.
   - Si no → eliminar alerta activa (si existía).
4. Endpoints sin baseline se ignoran.

---

## API contract

### `GET /alerts/active`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/reports",
    "current_p99": 850.0,
    "baseline_p99": 120.0,
    "threshold": 3.0,
    "triggered_at": "2026-03-13T10:05:22Z"
  }
]
```

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `method` | string | Método HTTP |
| `path` | string | Path del endpoint |
| `current_p99` | float64 | P99 actual en la ventana activa |
| `baseline_p99` | float64 | P99 de referencia (baseline) |
| `threshold` | float64 | Multiplicador configurado |
| `triggered_at` | string (RFC3339) | Timestamp del último disparo/actualización |

Ordenado por `triggered_at` descendente (más reciente primero).
Array vacío `[]` si no hay alertas activas.

**Response 405:** `POST /alerts/active` → `405 Method Not Allowed`.

---

## Cambios en código

### `config/config.go`

```go
AnomalyThreshold float64  // default: 3.0
```

YAML key: `anomaly_threshold`. Flag: `--anomaly-threshold`.
Validación: `AnomalyThreshold > 0`.

### Nuevo paquete `alerts/detector.go`

```go
package alerts

// Alert describes an active latency anomaly.
type Alert struct {
    Method      string    `json:"method"`
    Path        string    `json:"path"`
    CurrentP99  float64   `json:"current_p99"`
    BaselineP99 float64   `json:"baseline_p99"`
    Threshold   float64   `json:"threshold"`
    TriggeredAt time.Time `json:"triggered_at"`
}

// Detector runs a background goroutine that evaluates anomaly conditions
// at a fixed 10-second interval.
type Detector struct { ... }

// NewDetector creates a Detector.
//   engine           — metrics engine for current and baseline data
//   threshold        — anomaly_threshold multiplier (e.g. 3.0)
//   baselineWindows  — number of past windows used for baseline
func NewDetector(engine *metrics.Engine, threshold float64, baselineWindows int) *Detector

// Start launches the background evaluation goroutine.
func (d *Detector) Start()

// Stop shuts down the goroutine gracefully.
func (d *Detector) Stop()

// Active returns a snapshot of currently active alerts, sorted by TriggeredAt desc.
func (d *Detector) Active() []Alert
```

### `api/server.go`

- `NewServer` recibe `*alerts.Detector` como nuevo parámetro.
- Registrar `GET /alerts/active`.

### `cmd/profiler/main.go`

- Flag `--anomaly-threshold`.
- Crear `alerts.Detector`, llamar `Start()`, `Stop()` en shutdown.

---

## Test cases

### `config` — AnomalyThreshold

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-01 | Default | `AnomalyThreshold == 3.0` |
| TC-02 | YAML `anomaly_threshold: 2.5` | `AnomalyThreshold == 2.5` |
| TC-03 | Flag `--anomaly-threshold 5` overrides YAML | `AnomalyThreshold == 5.0` |
| TC-04 | `AnomalyThreshold = 0` → Validate error | Mensaje contiene `"anomaly_threshold"` |
| TC-05 | `AnomalyThreshold = -1` → Validate error | Mensaje contiene `"anomaly_threshold"` |

### `alerts.Detector`

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-06 | current_p99 > threshold * baseline_p99 | Alert aparece en `Active()` |
| TC-07 | current_p99 <= threshold * baseline_p99 | `Active()` vacío |
| TC-08 | Sin baseline para el endpoint | `Active()` vacío (no alert) |
| TC-09 | Alerta activa → condición resuelta → `evaluate()` | Alerta eliminada de `Active()` |
| TC-10 | Misma condición en dos `evaluate()` | Solo una alerta (deduplicada, `triggered_at` actualizado) |
| TC-11 | Dos endpoints: uno anómalo, otro no | Solo el anómalo en `Active()` |

### `api.Server` — `GET /alerts/active`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-12 | Sin alertas | 200, `[]` |
| TC-13 | Alertas activas | 200, JSON con campos correctos |
| TC-14 | Content-Type | `application/json` |
| TC-15 | `POST /alerts/active` | 405 Method Not Allowed |

---

## Out of scope

- **Entrega por webhook** — US-15.
- **Silenciado de alertas** — US-16.
- **Historial de alertas** — US-17.
- **Intervalo de comprobación configurable** — fijo en 10s para esta historia.

---

## Dependencies

- **US-13** implementado: `metrics.Engine.Baseline(n)`, `metrics.Engine.Endpoints()`.
- **US-07** implementado: `metrics.Engine`, `api.Server`.
- Sin nuevas dependencias externas.
