# US-08 — Ventana temporal configurable

**Épica:** Métricas y agregación (Fase 2)
**Prioridad:** Must
**Historia:** Como operator, puedo configurar la ventana de agregación (1m, 5m, 1h).
**AC:** Parámetro `metrics_window` en yaml y flag `--metrics-window`. Por defecto: 1 minuto.

---

## Context

El motor de métricas (`metrics.Engine`) ya acepta una `window` en su constructor (US-07), pero esa ventana se fija en el arranque desde el valor por defecto de `config.MetricsWindow` (60s). No hay CLI flag `--metrics-window` ni soporte para que un cliente cambie la ventana en una consulta individual.

US-08 cubre:
1. **Flag CLI** `--metrics-window` para sobreescribir el YAML desde línea de comandos.
2. **Query param `?window=`** en `GET /metrics/endpoints` para consultar con una ventana diferente a la configurada, sin reiniciar el proceso.

La YAML key `metrics_window` ya existe desde la infraestructura de US-07 y no cambia.

---

## Behavior

### Flag CLI `--metrics-window`

- Acepta cualquier string válido para `time.ParseDuration` (`30s`, `5m`, `1h`, etc.) más la extensión `"Nd"` (días) ya implementada en `config.parseDuration`.
- Sigue la precedencia estándar del proyecto: **flag > YAML > default** (integrado mediante `flag.Visit`).
- Validación en `config.Validate`: `MetricsWindow` debe ser positiva (ya existe la validación implícita de Timeout; MetricsWindow necesita su propia regla).
- Error en arranque si el valor no es parseable o no es positivo.

### Query param `?window=` en `/metrics/endpoints`

- El cliente puede añadir `?window=5m` a la petición para obtener datos de los últimos 5 minutos, independientemente de la ventana configurada en el proceso.
- Si el parámetro está ausente, se usa la ventana del Engine (valor configurado).
- Si el parámetro está presente pero su valor es inválido → `400 Bad Request` con cuerpo JSON `{"error": "<mensaje>"}`.
- Si el parámetro está presente y es válido → se usa esa ventana solo para esta petición; el estado del Engine no se modifica.

### Cambio en `metrics.Engine`

Para soportar el query param se añade un segundo método en `Engine`:

```go
// EndpointsForWindow computes stats using the given window instead of the engine default.
func (e *Engine) EndpointsForWindow(window time.Duration) ([]EndpointStat, error)
```

`Endpoints()` se convierte en un delegado de `EndpointsForWindow(e.window)` y su contrato no cambia.

---

## API contract

### `GET /metrics/endpoints` (actualizado)

**Sin query param** (comportamiento previo, sin cambios):
```
GET /metrics/endpoints
```
Usa la ventana configurada (`MetricsWindow`).

**Con query param:**
```
GET /metrics/endpoints?window=5m
```
Usa la ventana especificada solo para esta petición. Formatos válidos: cualquier Go duration (`30s`, `5m`, `1h30m`) y días (`7d`).

**Error 400 — ventana inválida:**
```json
{"error": "invalid window \"abc\": time: invalid duration \"abc\""}
```
Status `400 Bad Request`, `Content-Type: application/json`.

---

## Test cases

### `metrics.Engine.EndpointsForWindow`

| TC | Descripción | Comportamiento esperado |
|----|-------------|------------------------|
| TC-01 | Llamar `EndpointsForWindow` con la misma ventana que `Endpoints()` | Resultados idénticos |
| TC-02 | Ventana más corta excluye registros fuera del rango | Solo registros dentro de la nueva ventana aparecen |
| TC-03 | Ventana más larga incluye registros que `Endpoints()` excluiría | Más registros incluidos que con la ventana por defecto |
| TC-04 | `Endpoints()` sigue delegando a `EndpointsForWindow(e.window)` | Resultados iguales que llamar `EndpointsForWindow` con la ventana del engine |

### `api.Server` — `GET /metrics/endpoints?window=`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-05 | Sin `?window=` | 200, usa ventana del Engine |
| TC-06 | `?window=5m` válido | 200, usa ventana de 5 minutos |
| TC-07 | `?window=abc` inválido | 400, JSON `{"error": "..."}` |
| TC-08 | `?window=-1m` (negativo) | 400, JSON `{"error": "..."}` |
| TC-09 | `?window=0` (cero) | 400, JSON `{"error": "..."}` |
| TC-10 | `?window=1h30m` (compuesto) | 200, usa ventana de 90 minutos |
| TC-11 | `?window=7d` (días) | 200, usa ventana de 7 días |

### `cmd/profiler/main.go` — flag `--metrics-window`

| TC | Input | Comportamiento esperado |
|----|-------|------------------------|
| TC-12 | `--metrics-window 5m` | `cfg.MetricsWindow == 5*time.Minute` |
| TC-13 | `--metrics-window 1h` | `cfg.MetricsWindow == time.Hour` |
| TC-14 | YAML `metrics_window: 5m`, sin flag | `cfg.MetricsWindow == 5*time.Minute` |
| TC-15 | YAML `metrics_window: 5m`, flag `--metrics-window 1h` | `cfg.MetricsWindow == time.Hour` (flag gana) |

> TC-12..TC-15 se validan en `config/config_test.go` con tests de Merge; el comportamiento de arranque se verifica en tests de integración si se añaden en el futuro.

### `config.Validate` — MetricsWindow

| TC | Input | Error esperado |
|----|-------|----------------|
| TC-16 | `MetricsWindow = 0` | `"metrics_window must be positive"` |
| TC-17 | `MetricsWindow = -1s` | `"metrics_window must be positive"` |
| TC-18 | `MetricsWindow = 30s` | Sin error |

---

## Cambios en código

### `config/config.go`
- `Validate`: añadir regla `MetricsWindow > 0`.

### `cmd/profiler/main.go`
- Añadir flag `metricsWindowFlag := flag.Duration("metrics-window", 0, ...)`.
- En `flag.Visit`, case `"metrics-window"`: `overrides.MetricsWindow = *metricsWindowFlag`.

### `metrics/engine.go`
- Añadir `EndpointsForWindow(window time.Duration) ([]EndpointStat, error)`.
- Refactorizar `Endpoints()` para delegar a `EndpointsForWindow(e.window)`.

### `api/server.go`
- En `handleEndpoints`: parsear `r.URL.Query().Get("window")`.
- Si presente y válido: llamar `engine.EndpointsForWindow(parsedWindow)`.
- Si presente e inválido: responder 400 con JSON `{"error": "..."}`.
- Si ausente: llamar `engine.Endpoints()` (sin cambio en comportamiento previo).

---

## Out of scope

- **Ventana configurable por endpoint individual** (no en el PRD).
- **Streaming / SSE** de resultados con ventana deslizante en tiempo real.
- **Cache** de resultados por ventana.
- **Validación** de ventana máxima (no hay límite definido en el PRD).

---

## Dependencies

- **US-07** implementado: `metrics.Engine`, `api.Server`, `storage.Reader`, `GET /metrics/endpoints`.
- **US-03/US-04** implementados: `config.Config`, `config.Merge`, `flag.Visit` pattern.
- Sin nuevas dependencias externas.
