# US-22 — Indicador de alertas activas

**Épica:** Dashboard web (Fase 4)
**Prioridad:** Should
**Historia:** Como developer, veo un badge con el número de alertas activas en el header.
**AC:** Badge rojo cuando hay alertas activas. Click lleva a la lista.

---

## Context

US-18 añadió `<span id="alert-badge">` vacío en el header (CSS ya oculta el badge cuando está vacío). US-22 lo rellena con el conteo de alertas activas y añade una sección `#alerts` en el body con la lista detallada. No se añaden endpoints de servidor nuevos — usa el existente `GET /alerts/active`.

---

## Behavior

### Badge

- **Sin alertas**: `#alert-badge` permanece vacío → oculto (ya implementado en CSS con `#alert-badge:empty { display: none }`).
- **Con alertas**: badge muestra `N alerts` (p.ej. `2 alerts`) en rojo con punto parpadeante.
- Click en el badge hace scroll hasta la sección `#alerts`.
- Actualización automática cada 5s junto con el resto del dashboard.

### Sección `#alerts`

Añadida a `index.html` entre `#summary` y `#endpoints`. Oculta cuando no hay alertas activas.

Cuando hay alertas, muestra una tabla con:

```
METHOD  PATH            CURRENT P99   BASELINE P99   RATIO    TRIGGERED
────────────────────────────────────────────────────────────────────────
GET     /api/reports    450 ms        100 ms         4.5×     10:03:21
POST    /api/orders     900 ms        200 ms         4.5×     10:05:44
```

- `ratio` = `current_p99 / baseline_p99`, redondeado a 1 decimal con `×`.
- `triggered_at` formateado como `HH:MM:SS` local.
- La sección se oculta automáticamente cuando `Active()` devuelve `[]`.

---

## API contract

Sin cambios en el servidor. Usa `GET /alerts/active` (ya implementado en US-14).

---

## Cambios en código

### `api/dashboard/index.html`

Añadir sección entre `#summary` y `#endpoints`:

```html
<section id="alerts" style="display:none">
  <div class="section-header">
    <span class="section-title"><span class="dot dot-danger"></span>Active Alerts</span>
  </div>
  <div class="section-body"></div>
</section>
```

### `api/dashboard/static/app.js`

```js
async function fetchAlerts() { ... }
```

- GET `/alerts/active`.
- Si vacío: oculta `#alerts`, vacía `#alert-badge`.
- Si hay alertas: muestra `#alerts` con tabla, pone `N alerts` en `#alert-badge`, badge click → `document.getElementById('alerts').scrollIntoView(...)`.

`refresh()` llama a `fetchAlerts()`.

### `api/dashboard/static/style.css`

- `.dot-danger` — punto rojo (variante del `.dot` existente).
- `.alerts-table` — igual que `.data-table` pero con borde izquierdo de acento danger.

---

## Test cases

Esta historia es puramente frontend. No hay nuevos endpoints de servidor, así que no hay test cases de Go.

Los comportamientos son verificables manualmente:
- Con alertas activas: badge visible, sección `#alerts` visible con datos.
- Sin alertas: badge oculto, sección `#alerts` oculta.
- Click en badge: scroll a la sección.

---

## Out of scope

- Sonido / notificación de escritorio al aparecer nueva alerta — no en PRD.
- Historial de alertas en el dashboard (US-17 ya lo expone vía API).
- Silenciar alertas desde el dashboard — no en PRD para Fase 4.

---

## Dependencies

- US-14: `GET /alerts/active` implementado.
- US-18: `#alert-badge` en el header, shell del dashboard.
- US-22 no bloquea a US-21.
