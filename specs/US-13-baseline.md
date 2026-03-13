# US-13 — Baseline automático

**Épica:** Detección de anomalías (Fase 3)
**Prioridad:** Must
**Historia:** Como sistema, calculo el baseline de latencia normal de cada endpoint a partir de las últimas N ventanas.
**AC:** Baseline disponible tras 5 ventanas completas. Parámetro `baseline_windows`.

---

## Context

El baseline es la referencia de "latencia normal" para cada endpoint. US-14 lo usará para detectar cuando un endpoint supera N veces su baseline. Esta historia calcula y expone ese baseline.

**Ventana activa vs ventanas completas:**
- La *ventana activa* es `[now - window, now]` — la que usan `Endpoints()`, `Errors()`, etc.
- Las *ventanas completas* son las N intervalos inmediatamente anteriores a la ventana activa.
- El rango de consulta para el baseline es: `[now - (baseline_windows+1)*window, now - window]`.

Por ejemplo, con `window=60s` y `baseline_windows=5`:
- Ventana activa: `[now-60s, now]`
- Rango baseline: `[now-360s, now-60s]` (5 ventanas de 60s)

Esta historia añade:
1. Campo `BaselineWindows int` en `config.Config` (default: 5).
2. Flag `--baseline-windows` y YAML `baseline_windows`.
3. Método `Baseline(n int)` en `metrics.Engine`.
4. Endpoint `GET /metrics/baseline` en `api.Server`.

---

## Behavior

### Cálculo

1. `Baseline(n)` llama a `reader.FindByWindow(now-(n+1)*window, now-window)`.
2. Agrupa registros por `(method, path)`.
3. Para cada grupo calcula P99 sobre todos los `duration_ms` del rango.
4. Devuelve resultados ordenados por `baseline_p99` descendente.

### "Disponible tras N ventanas"

El AC dice que el baseline está disponible tras 5 ventanas completas. En esta historia eso se implementa así:

- El campo `windows_covered` indica cuántos intervalos de `window` abarca el rango consultado (siempre igual a `n`).
- Si **no hay registros** en el rango → el endpoint no aparece en el resultado (sin baseline calculable).
- US-14 decidirá si actuar o no según si el endpoint tiene entrada en el baseline.

> No se implementa un contador de "ventanas con al menos 1 registro" porque requeriría N queries o lógica compleja. El invariante es: si hay registros en el rango `[now-(n+1)*window, now-window]`, el baseline es válido.

### Sin datos

Si no hay registros en el rango → `[]`, status 200.

---

## API contract

### `GET /metrics/baseline`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/users",
    "baseline_p99": 45.2,
    "sample_count": 710
  },
  {
    "method": "POST",
    "path": "/api/orders",
    "baseline_p99": 120.0,
    "sample_count": 190
  }
]
```

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `method` | string | Método HTTP |
| `path` | string | Path del endpoint |
| `baseline_p99` | float64 | P99 calculado sobre las últimas N ventanas completas |
| `sample_count` | int | Número de registros usados para el cálculo |

Ordenado por `baseline_p99` descendente.

**Response 405:** `POST /metrics/baseline` → `405 Method Not Allowed`.

---

## Cambios en código

### `config/config.go`

```go
// Config — nuevo campo:
BaselineWindows int  // default: 5
```

Validación en `Validate`: `BaselineWindows >= 1`.

### `cmd/profiler/main.go`

```
--baseline-windows  int   number of past windows used for baseline (default: 5)
```

### `metrics/engine.go`

```go
// BaselineStat holds baseline latency for one method+path.
type BaselineStat struct {
    Method      string  `json:"method"`
    Path        string  `json:"path"`
    BaselineP99 float64 `json:"baseline_p99"`
    SampleCount int     `json:"sample_count"`
}

// Baseline computes the P99 baseline for each endpoint over the last n complete
// windows (i.e., [now-(n+1)*window, now-window]).
func (e *Engine) Baseline(n int) ([]BaselineStat, error)
```

### `api/server.go`

- Registrar `GET /metrics/baseline` en el mux.
- El handler llama `engine.Baseline(baselineWindows)` donde `baselineWindows` viene del config.
- Para exponer el parámetro al handler, `NewServer` recibe `baselineWindows int` como argumento adicional.

---

## Test cases

### `config` — BaselineWindows

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-01 | Default | `BaselineWindows == 5` |
| TC-02 | YAML `baseline_windows: 10` | `BaselineWindows == 10` |
| TC-03 | Flag `--baseline-windows 3` overrides YAML `10` | `BaselineWindows == 3` |
| TC-04 | `BaselineWindows = 0` → Validate error | Mensaje contiene `"baseline_windows"` |
| TC-05 | `BaselineWindows = -1` → Validate error | Mensaje contiene `"baseline_windows"` |
| TC-06 | `BaselineWindows = 1` → Validate OK | Sin error |

### `metrics.Engine.Baseline`

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-07 | Sin registros | Slice vacío (no nil), sin error |
| TC-08 | Registros presentes | `baseline_p99` y `sample_count` correctos |
| TC-09 | Rango temporal pasado al reader | `from ≈ now-(n+1)*window`, `to ≈ now-window` |
| TC-10 | Dos endpoints | Ordenados por `baseline_p99` desc |
| TC-11 | `sample_count` refleja todos los registros del grupo | Valor correcto |

### `api.Server` — `GET /metrics/baseline`

| TC | Input | Resultado esperado |
|----|-------|--------------------|
| TC-12 | Sin datos | 200, `[]` |
| TC-13 | Registros presentes | 200, JSON con campos correctos |
| TC-14 | Content-Type | `application/json` |
| TC-15 | `POST /metrics/baseline` | 405 Method Not Allowed |

---

## Out of scope

- **Baseline por percentil distinto de P99** — no en el PRD.
- **Persistencia del baseline** — se recalcula en cada consulta desde SQLite.
- **Detección de anomalías** — cubierto en US-14.
- **`?window=` en este endpoint** — consistencia futura.

---

## Dependencies

- **US-07** implementado: `metrics.Engine`, `storage.Reader.FindByWindow`, pct().
- **US-03/US-04** implementados: `config.Config`, `flag.Visit`.
- Sin nuevas dependencias externas.
