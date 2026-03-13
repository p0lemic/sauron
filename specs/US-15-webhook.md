# US-15 — Webhook de alertas

**Épica:** Detección de anomalías (Fase 3)
**Prioridad:** Must
**Historia:** Como operator, las alertas se envían a una URL webhook en formato JSON.
**AC:** POST al webhook con payload: endpoint, current_p99, baseline_p99, timestamp.

---

## Context

US-14 detecta anomalías y las mantiene en memoria. US-15 añade la entrega activa: cuando se crea una alerta **nueva** (primera vez que un endpoint supera el umbral), el sistema hace un HTTP POST a una URL configurada por el operator.

Esta historia añade:
1. Campo `WebhookURL string` en `config.Config` (opcional; si vacío, no se envía nada).
2. Interfaz `Notifier` en el paquete `alerts` + implementación `WebhookNotifier`.
3. El `Detector` acepta un `Notifier` opcional y lo invoca solo en alertas **nuevas** (no en actualizaciones de alertas ya activas).
4. Flag `--webhook-url` y YAML `webhook_url`.

---

## Behavior

### Cuándo se dispara el webhook

- **Solo en alertas nuevas**: cuando un endpoint pasa de "no anómalo" a "anómalo" en un ciclo de evaluación.
- **No se repite**: si el endpoint sigue anómalo en el siguiente tick, el webhook **no** se re-dispara. Solo se dispara si la alerta se resolvió y vuelve a activarse.
- Si `WebhookURL` está vacío → no se hace ningún POST (webhook es opcional).

### Payload HTTP

```
POST <webhook_url>
Content-Type: application/json

{
  "method": "GET",
  "path": "/api/reports",
  "current_p99": 850.0,
  "baseline_p99": 120.0,
  "threshold": 3.0,
  "triggered_at": "2026-03-13T10:05:22Z"
}
```

El payload es el struct `Alert` serializado a JSON (ya contiene todos los campos requeridos por el AC).

### Error handling

- Si el POST falla (timeout, conexión rechazada, status ≥ 400): se registra el error con `log.Printf` y se continúa. No se reintenta, no se crashea.
- Timeout de la petición HTTP: **5 segundos**.
- La llamada al notifier es **síncrona** dentro del ciclo de evaluación (el intervalo de 10s absorbe el tiempo de la llamada sin problema).

### Seguridad

- El webhook no incluye headers de autenticación por defecto (fuera de scope del PRD).
- Si la URL usa HTTPS, se respetan los certificados del sistema (no se ignoran).

---

## API contract

No hay cambios en la API REST. El webhook es outbound (el sistema lo llama, no lo sirve).

---

## Cambios en código

### `config/config.go`

```go
WebhookURL string  // optional; YAML: webhook_url; flag: --webhook-url
```

Sin validación (campo opcional). Si está presente debe ser una URL http/https válida — validación ligera con `url.Parse`.

### `alerts/notifier.go` (nuevo)

```go
// Notifier is called once when a new alert is created.
type Notifier interface {
    Notify(a Alert)
}

// WebhookNotifier sends alert payloads via HTTP POST to a URL.
type WebhookNotifier struct {
    URL    string
    Client *http.Client  // default: &http.Client{Timeout: 5s}
}

func NewWebhookNotifier(url string) *WebhookNotifier

func (w *WebhookNotifier) Notify(a Alert)
```

### `alerts/detector.go` (modificado)

- Añadir campo `notifier Notifier` (puede ser nil).
- `NewDetector` no cambia de firma. Añadir `SetNotifier(n Notifier)` para inyección.
- En `Evaluate()`: al crear una alerta **nueva** (key no existía en `d.active`), llamar `d.notifier.Notify(alert)` si `d.notifier != nil`.

### `cmd/profiler/main.go`

- Flag `--webhook-url`.
- Si `cfg.WebhookURL != ""`: crear `alerts.NewWebhookNotifier(cfg.WebhookURL)` y llamar `detector.SetNotifier(n)`.

---

## Test cases

### `alerts.WebhookNotifier`

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Webhook URL válida → servidor recibe POST | Body es JSON con los campos de Alert correctos |
| TC-02 | Content-Type del POST | `application/json` |
| TC-03 | Servidor devuelve 500 | Error se registra con log, sin panic |
| TC-04 | URL inaccesible (timeout) | Error registrado, sin panic |

### `alerts.Detector` con notifier

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-05 | Nueva alerta → notifier llamado exactamente 1 vez |
| TC-06 | Alerta ya activa en tick siguiente → notifier NO llamado de nuevo |
| TC-07 | Alerta resuelta y re-disparada → notifier llamado de nuevo (1 vez) |
| TC-08 | Sin notifier (nil) → Evaluate() funciona sin panic |
| TC-09 | Endpoint sin baseline → notifier no llamado |

### `config`

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-10 | YAML `webhook_url: http://x` | `cfg.WebhookURL == "http://x"` |
| TC-11 | Sin `webhook_url` en YAML | `cfg.WebhookURL == ""` |
| TC-12 | Validate con URL vacía | Sin error (campo opcional) |
| TC-13 | Validate con URL inválida (no http/https) | Error con `"webhook_url"` |

---

## Out of scope

- **Reintentos** de entrega fallida.
- **Headers de autenticación** en el webhook.
- **Silenciado de alertas** (US-16).
- **Historial de alertas** (US-17).

---

## Dependencies

- **US-14** implementado: `alerts.Detector`, `alerts.Alert`.
- **US-03/US-04** implementados: config y flags.
- Sin nuevas dependencias externas (`net/http` stdlib).
