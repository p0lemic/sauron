# US-16 — Silenciado de alertas

**Épica:** Detección de anomalías (Fase 3)
**Prioridad:** Should
**Historia:** Como operator, puedo silenciar alertas de un endpoint durante un periodo definido.
**AC:** `POST /alerts/silence` con body `{endpoint, duration}`.

---

## Context

El `Detector` (US-14) dispara alertas y llama al webhook (US-15) cuando un endpoint supera el umbral. US-16 permite al operator suprimir temporalmente ese comportamiento para un endpoint concreto — útil durante mantenimientos o despliegues donde la latencia alta es esperada.

Esta historia añade:
1. Struct `Silence` y métodos de gestión en `alerts.Detector`.
2. `POST /alerts/silence` — crear un silencio.
3. `GET /alerts/silences` — listar silencios activos.
4. Efecto en `Evaluate()`: los endpoints silenciados no generan alerta activa ni disparan webhook.

---

## Behavior

### Creación de un silencio

El operator hace `POST /alerts/silence` con:
```json
{
  "method": "GET",
  "path": "/api/reports",
  "duration": "1h"
}
```

- `method` + `path` identifican el endpoint.
- `duration`: string de duración Go (`30m`, `1h`, `2h30m`) o días (`1d`). Debe ser positiva.
- El silencio expira automáticamente en `now + duration`.
- Si ya existe un silencio para ese endpoint, se **reemplaza** (se aplica la nueva duración desde el momento de la petición).

### Efecto en evaluación

Mientras un silencio está activo para un endpoint:
- La alerta **no** aparece en `GET /alerts/active`.
- El webhook **no** se dispara.
- Si la alerta ya estaba activa cuando se crea el silencio, se elimina de `active` en el próximo ciclo.
- Cuando el silencio expira, el endpoint vuelve a ser evaluado normalmente en el siguiente tick.

### Expiración

Los silencios expirados se limpian automáticamente en cada ciclo de `Evaluate()` y también al consultar `GET /alerts/silences`.

### Listado de silencios

`GET /alerts/silences` devuelve los silencios activos (no expirados), ordenados por `expires_at` ascendente.

---

## API contract

### `POST /alerts/silence`

**Request body:**
```json
{
  "method": "GET",
  "path": "/api/reports",
  "duration": "1h"
}
```

**Response 200 OK:**
```json
{
  "method": "GET",
  "path": "/api/reports",
  "expires_at": "2026-03-13T11:05:22Z"
}
```

**Response 400** — body inválido o duración no parseable:
```json
{"error": "invalid duration \"xyz\": ..."}
```

**Response 405** — método incorrecto en `POST /alerts/silences`.

---

### `GET /alerts/silences`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/reports",
    "expires_at": "2026-03-13T11:05:22Z"
  }
]
```

Array vacío si no hay silencios activos.

---

## Cambios en código

### `alerts/silence.go` (nuevo)

```go
// Silence represents a timed suppression of alerts for one endpoint.
type Silence struct {
    Method    string    `json:"method"`
    Path      string    `json:"path"`
    ExpiresAt time.Time `json:"expires_at"`
}
```

### `alerts/detector.go` (modificado)

Añadir campo `silences map[string]*Silence` (clave `"METHOD:path"`), protegido por `d.mu`.

```go
// Silence suppresses alerts for the given endpoint until now+duration.
// Replaces any existing silence for that endpoint.
func (d *Detector) Silence(method, path string, duration time.Duration)

// ActiveSilences returns non-expired silences, sorted by ExpiresAt asc.
func (d *Detector) ActiveSilences() []Silence
```

En `Evaluate()`: antes de procesar un endpoint, comprobar si está silenciado. Si está silenciado, saltarlo (no añadir a `triggered`, no generar `newAlerts`). Al final del ciclo, limpiar silencios expirados de `d.silences`.

### `api/server.go` (modificado)

- Registrar `POST /alerts/silence` → `handleCreateSilence`.
- Registrar `GET /alerts/silences` → `handleListSilences`.
- Parsear body JSON; usar `parseDuration` existente para el campo `duration`.

---

## Test cases

### `alerts.Detector` — silencio

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Silencio activo → alerta NO aparece en `Active()` | `Active()` vacío |
| TC-02 | Silencio activo → notifier NO llamado | `mockNotifier.calls` vacío |
| TC-03 | Silencio expirado → alerta SÍ aparece | `Active()` contiene la alerta |
| TC-04 | Silencio reemplazado → nueva duración aplicada | `ExpiresAt` actualizado |
| TC-05 | `ActiveSilences()` limpia expirados al consultar | Solo devuelve los vigentes |
| TC-06 | Alerta activa cuando se crea silencio → desaparece en siguiente `Evaluate()` | `Active()` vacío tras `Evaluate()` |

### `api.Server` — `POST /alerts/silence`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-07 | Body válido | 200, JSON con `expires_at` |
| TC-08 | `duration` inválida | 400, JSON `{"error":"..."}` |
| TC-09 | `duration` negativa | 400, JSON `{"error":"..."}` |
| TC-10 | Body JSON malformado | 400 |
| TC-11 | `GET /alerts/silence` | 405 Method Not Allowed |

### `api.Server` — `GET /alerts/silences`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-12 | Sin silencios activos | 200, `[]` |
| TC-13 | Con silencio activo | 200, JSON con `expires_at` correcto |
| TC-14 | Content-Type | `application/json` |
| TC-15 | `POST /alerts/silences` | 405 Method Not Allowed |

---

## Out of scope

- **Silencio global** (todos los endpoints a la vez) — no en el PRD.
- **Persistencia de silencios** — en memoria, se pierden al reiniciar.
- **Historial de silencios** — no en el PRD.
- **Autenticación** del endpoint de silence — Fase 2.

---

## Dependencies

- **US-14** implementado: `alerts.Detector`, `Evaluate()`, `Active()`.
- **US-15** implementado: `Notifier`, `SetNotifier`.
- Sin nuevas dependencias externas.
