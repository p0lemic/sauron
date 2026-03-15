# US-49 — Webhook multi-target y notificación de resolución

**Épica:** Alertas y notificaciones
**Prioridad:** Must
**Historia:** Como operator, puedo configurar múltiples destinos webhook y recibir notificaciones tanto cuando una alerta se dispara como cuando se resuelve.
**AC:** Lista de webhooks en config. Cada webhook recibe eventos `fired` y `resolved`. Payload diferenciado por `event` y `kind`.

---

## Context

US-15 implementó un webhook único (`webhook_url`) que solo notifica en `alert_fired`. Esta historia extiende esa base con:
1. Lista de N webhooks (distintas URLs, mismos o distintos equipos)
2. Notificación de **resolución** (`alert_resolved`) con `duration_ms`
3. Campo `event` en el payload para distinguir fired vs resolved
4. Formato Slack opcional (si la URL contiene `hooks.slack.com` o se especifica `format: slack`)

La interfaz `Notifier` existente se extiende; `WebhookNotifier` existente se refactoriza.

---

## Behavior

### Configuración

```yaml
webhooks:
  - url: "https://hooks.slack.com/services/..."
    format: slack      # "json" (default) | "slack"
    events: [fired, resolved]   # default: [fired]
  - url: "https://pagerduty.example.com/webhook"
    events: [fired]
```

Si el campo `webhooks` no existe, se mantiene compatibilidad con `webhook_url` existente (se trata como un webhook con `events: [fired]`, `format: json`).

### Payload `json` — event fired

```json
{
  "event": "fired",
  "kind": "error_rate",
  "method": "GET",
  "path": "/api/users",
  "triggered_at": "2026-03-15T12:00:00Z",
  "error_rate": 12.5,
  "error_rate_threshold": 5.0
}
```

### Payload `json` — event resolved

```json
{
  "event": "resolved",
  "kind": "error_rate",
  "method": "GET",
  "path": "/api/users",
  "triggered_at": "2026-03-15T12:00:00Z",
  "resolved_at": "2026-03-15T12:05:00Z",
  "duration_ms": 300000
}
```

### Payload `slack` — event fired

```json
{
  "text": "🔴 *Alert fired* — `error_rate` on `GET /api/users`\n>Error rate: 12.5% (threshold: 5.0%)\nTriggered at: 2026-03-15T12:00:00Z"
}
```

### Payload `slack` — event resolved

```json
{
  "text": "🟢 *Alert resolved* — `error_rate` on `GET /api/users`\n>Duration: 5m 0s"
}
```

### Error handling

Misma política que US-15: log + continue, sin reintentos, timeout 5s.

---

## API contract

No hay cambios en la API REST. Los webhooks son outbound.

---

## Cambios en código

### `config/config.go`

```go
// WebhookConfig defines one webhook destination.
type WebhookConfig struct {
    URL    string   `yaml:"url"`
    Format string   `yaml:"format"`  // "json" | "slack"; default "json"
    Events []string `yaml:"events"`  // ["fired"] | ["resolved"] | ["fired","resolved"]
}

// En Config:
Webhooks   []WebhookConfig  `yaml:"webhooks"`
// WebhookURL string permanece por backward-compatibility
```

Validación: cada URL debe ser http/https válida. `Format` debe ser `json` o `slack`.
Si `Webhooks` está vacío y `WebhookURL != ""`, se crea un `WebhookConfig{URL: WebhookURL, Events: ["fired"], Format: "json"}`.

### `alerts/notifier.go` — extensión

```go
const (
    EventFired    = "fired"
    EventResolved = "resolved"
)

// WebhookEvent is the payload sent to a webhook.
type WebhookEvent struct {
    Event     string    `json:"event"`       // "fired" | "resolved"
    Kind      string    `json:"kind"`
    Method    string    `json:"method"`
    Path      string    `json:"path"`
    TriggeredAt time.Time `json:"triggered_at"`
    // resolved-only:
    ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
    DurationMs  int64      `json:"duration_ms,omitempty"`
    // kind-specific (same fields as Alert, omitempty):
    CurrentP99         float64 `json:"current_p99,omitempty"`
    BaselineP99        float64 `json:"baseline_p99,omitempty"`
    Threshold          float64 `json:"threshold,omitempty"`
    ErrorRate          float64 `json:"error_rate,omitempty"`
    ErrorRateThreshold float64 `json:"error_rate_threshold,omitempty"`
    CurrentRPS         float64 `json:"current_rps,omitempty"`
    BaselineRPS        float64 `json:"baseline_rps,omitempty"`
    ZScore             float64 `json:"z_score,omitempty"`
}

// MultiNotifier dispatches to multiple WebhookTargets.
type MultiNotifier struct {
    targets []webhookTarget
}

type webhookTarget struct {
    notifier *WebhookNotifier
    format   string
    events   map[string]bool
}

func NewMultiNotifier(cfgs []config.WebhookConfig) *MultiNotifier

// NotifyFired is called when a new alert fires.
func (m *MultiNotifier) NotifyFired(a Alert)

// NotifyResolved is called when an alert resolves.
func (m *MultiNotifier) NotifyResolved(a Alert, resolvedAt time.Time)
```

### `alerts/detector.go` — extensión

Añadir interfaz ampliada:
```go
// ResolveNotifier extends Notifier with resolution events.
type ResolveNotifier interface {
    NotifyFired(a Alert)
    NotifyResolved(a Alert, resolvedAt time.Time)
}
```

El campo `notifier` pasa a ser `ResolveNotifier`. Backward-compat: si se llama `SetNotifier(n Notifier)`, se wrappea en un adapter que solo implementa `NotifyFired`.

En el bloque de auto-resolve de `Evaluate()`: llamar `d.notifier.NotifyResolved(alert, resolvedAt)` si el notifier implementa `ResolveNotifier`.

### `cmd/profiler/main.go` y `cmd/dashboard/main.go`

Construir `MultiNotifier` desde `cfg.Webhooks` (con fallback a `cfg.WebhookURL`) y asignarlo al Detector.

---

## Test cases

### `MultiNotifier`

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | 2 targets, ambos suscritos a `fired` → NotifyFired | Ambos reciben POST |
| TC-02 | 1 target `fired`, 1 target `resolved` → NotifyFired | Solo el de `fired` recibe POST |
| TC-03 | NotifyResolved enviado a target con `events: [resolved]` | Payload contiene `event: "resolved"` y `duration_ms` |
| TC-04 | format=slack → payload tiene campo `text` | text contiene emoji y nombre del kind |
| TC-05 | format=json → payload tiene campo `event` | Payload es WebhookEvent |
| TC-06 | URL inaccesible | Error logueado, sin panic, otros targets continúan |

### `alerts.Detector` — resolved notification

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-07 | Alert fires → resolves → NotifyResolved llamado 1 vez | resolvedAt > triggeredAt |
| TC-08 | Alert activa en tick siguiente → NotifyResolved no llamado | |
| TC-09 | Sin notifier | Evaluate() sin panic |

### `config`

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-10 | YAML con 2 webhooks | Len(cfg.Webhooks) == 2 |
| TC-11 | webhook_url legacy → 1 WebhookConfig fired-only | Backward compat |
| TC-12 | format inválido → Validate error | |
| TC-13 | URL sin scheme → Validate error | |

---

## Out of scope

- Reintentos con backoff.
- Headers de autenticación (Bearer token, etc.).
- Filtrado por endpoint o kind en el webhook.
- PagerDuty Events API v2 native format.

---

## Dependencies

- US-15: `alerts.Notifier`, `WebhookNotifier`, `cfg.WebhookURL`.
- US-40/US-41: `Alert.Kind`, `KindErrorRate`, `KindThroughput`.
- US-48: `KindStatistical` (el notifier debe soportar el nuevo kind).
