# US-17 — Historial de alertas

**Épica:** Detección de anomalías (Fase 3)
**Prioridad:** Should
**Historia:** Como operator, puedo consultar el historial de alertas (activas y resueltas) para diagnosticar incidentes pasados.
**AC:** `GET /alerts/history` devuelve la lista de alertas registradas con timestamps de inicio y fin.

---

## Context

El `Detector` (US-14) mantiene un mapa `active` de alertas en curso. Cuando una alerta se auto-resuelve (la condición deja de cumplirse), desaparece de `active` sin dejar rastro. US-17 añade un historial en memoria que acumula todas las alertas que alguna vez se activaron, incluyendo cuándo se resolvieron.

---

## Behavior

### Registro de alertas

- Cuando una alerta **nueva** se crea en `Evaluate()`, se añade al historial con `ResolvedAt = nil` (nil indica aún activa).
- Cuando una alerta se **resuelve** (sale de `active`), se busca en el historial y se registra `ResolvedAt = now`.
- Si una alerta ya resuelta vuelve a dispararse, se crea una **nueva entrada** en el historial (mismo endpoint, nueva fila).
- El historial crece indefinidamente en memoria (se pierde al reiniciar — sin persistencia en esta historia).

### Listado

`GET /alerts/history` devuelve todas las entradas del historial, ordenadas por `triggered_at` descendente (más reciente primero).

Cada entrada incluye:
- `method`, `path`
- `current_p99`, `baseline_p99`, `threshold`
- `triggered_at`
- `resolved_at` — `null` si la alerta sigue activa, timestamp si ya fue resuelta.

---

## API contract

### `GET /alerts/history`

**Response 200 OK:**
```json
[
  {
    "method": "GET",
    "path": "/api/reports",
    "current_p99": 450.0,
    "baseline_p99": 100.0,
    "threshold": 3.0,
    "triggered_at": "2026-03-13T10:00:00Z",
    "resolved_at": "2026-03-13T10:05:00Z"
  },
  {
    "method": "POST",
    "path": "/api/orders",
    "current_p99": 800.0,
    "baseline_p99": 200.0,
    "threshold": 3.0,
    "triggered_at": "2026-03-13T10:10:00Z",
    "resolved_at": null
  }
]
```

Array vacío si no hay historial.

**Response 405** — método incorrecto.

---

## Cambios en código

### `alerts/history.go` (nuevo)

```go
// AlertRecord is one entry in the alert history.
type AlertRecord struct {
    Method      string     `json:"method"`
    Path        string     `json:"path"`
    CurrentP99  float64    `json:"current_p99"`
    BaselineP99 float64    `json:"baseline_p99"`
    Threshold   float64    `json:"threshold"`
    TriggeredAt time.Time  `json:"triggered_at"`
    ResolvedAt  *time.Time `json:"resolved_at"`
}
```

### `alerts/detector.go` (modificado)

Añadir campo `history []*AlertRecord` protegido por `d.mu`.

En `Evaluate()`:
1. Cuando se crea una alerta nueva (`!wasActive`): añadir entrada al historial con `ResolvedAt = nil`.
2. Cuando se resuelve una alerta (`k` en `active` pero no en `triggered`): buscar la entrada más reciente sin `ResolvedAt` para ese key, y poner `ResolvedAt = &now`.

Añadir método:
```go
// History returns a snapshot of all alert records, sorted by TriggeredAt desc.
func (d *Detector) History() []AlertRecord
```

### `api/server.go` (modificado)

Registrar `GET /alerts/history` → `handleAlertsHistory`.

---

## Test cases

### `alerts.Detector` — historial

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Alert fires → History() has 1 entry, ResolvedAt nil | Len 1, ResolvedAt nil |
| TC-02 | Alert fires then resolves → ResolvedAt set | ResolvedAt not nil, after TriggeredAt |
| TC-03 | Alert fires, resolves, fires again → 2 entries | Len 2, second has ResolvedAt nil |
| TC-04 | No alerts ever → History() empty | Len 0 |
| TC-05 | Multiple endpoints fire → all in history | Len matches fired count |

### `api.Server` — `GET /alerts/history`

| TC | Input | Respuesta esperada |
|----|-------|--------------------|
| TC-06 | No history | 200, `[]` |
| TC-07 | History present | 200, JSON with correct fields |
| TC-08 | Content-Type | `application/json` |
| TC-09 | POST /alerts/history | 405 Method Not Allowed |

---

## Out of scope

- **Persistencia** del historial — en memoria, se pierde al reiniciar.
- **Paginación** — no en el PRD.
- **Filtrado** por endpoint o tiempo — no en el PRD.
- **Límite de tamaño** del historial — no en el PRD.

---

## Dependencies

- **US-14** implementado: `alerts.Detector`, `Evaluate()`, `Active()`.
- **US-16** implementado: estructura de `Evaluate()` con silences.
- Sin nuevas dependencias externas.
