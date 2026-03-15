# US-50 — Alert History Dashboard

**Épica:** Alertas y notificaciones
**Prioridad:** Should
**Historia:** Como operator, veo el historial de alertas en el dashboard con una línea de tiempo que muestra cuándo se disparó y resolvió cada alerta.
**AC:** Sección "Alert History" con timeline visual, filtro por kind, y duración de cada incidente.

---

## Context

US-17 implementó `GET /alerts/history` que devuelve todas las alertas con `triggered_at` y `resolved_at`. Sin embargo, no existe sección en el dashboard que consuma este endpoint. US-50 añade esa sección con:
1. Tabla con timeline visual (barra de duración)
2. Filtro por `kind`
3. Indicador visual de estado (activa vs resuelta)
4. Duración del incidente

No se modifica el backend. Solo UI.

---

## Behavior

### Sección en el dashboard

Posición: justo debajo de "Active Alerts" (que ya existe). Solo visible si hay registros en el historial (igual que Active Alerts se oculta si está vacío).

### Tabla de historial

Columnas:
- **Kind** — badge coloreado (mismo estilo que alerts activas)
- **Method** — badge de método HTTP
- **Path** — cell-path
- **Triggered** — hora formateada (HH:mm:ss)
- **Resolved** — hora formateada, o badge `active` si `resolved_at == null`
- **Duration** — tiempo entre triggered y resolved, o "ongoing" si activa
- **Timeline** — barra visual proporcional al tiempo dentro del rango visible

### Barra de timeline

Cada fila tiene una barra horizontal que representa cuándo ocurrió el incidente dentro de una ventana de tiempo contextual (definida por el rango más antiguo/reciente del historial). La barra:
- Empieza en `triggered_at` normalizado al rango total
- Termina en `resolved_at` (o "ahora" si activa)
- Color: rojo si activa, gris si resuelta

### Filtros

Selector de kind en el section-header: `[All] [Latency] [Error Rate] [Throughput] [Statistical]`. Solo muestra kinds que tengan al menos una entrada.

### Polling

El historial se refresca con el mismo ciclo de `refresh()` (cada 5s). Solo se llama `fetchAlertHistory()` — no afecta a otras secciones.

---

## API contract

Sin cambios. Usa `GET /alerts/history` existente (US-17).

Formato de respuesta existente:
```json
[
  {
    "kind": "error_rate",
    "method": "GET",
    "path": "/api/users",
    "error_rate": 12.5,
    "error_rate_threshold": 5.0,
    "triggered_at": "2026-03-15T12:00:00Z",
    "resolved_at": "2026-03-15T12:05:00Z"
  }
]
```

---

## Cambios en código

Solo frontend (`api/dashboard/index.html`, `static/app.js`, `static/style.css`).

### `index.html`

Nueva `<section id="alert-history" style="display:none">` debajo de `#alerts`.

### `app.js`

```js
let _ahKindFilter = 'all';

async function fetchAlertHistory() { ... }
function renderAlertHistory(records) { ... }
```

`fetchAlertHistory()`:
1. `GET /alerts/history`
2. Si array vacío → ocultar sección
3. Si no → renderizar y mostrar sección

`renderAlertHistory(records)`:
- Calcular `minTime` y `maxTime` del rango (entre el `triggered_at` más antiguo y `max(resolved_at, now)`)
- Para cada record, calcular left% y width% de la barra de timeline
- Aplicar filtro `_ahKindFilter`
- Renderizar tabla con barra SVG/CSS inline

### `style.css`

```css
/* Timeline bar */
.ah-timeline-wrap { ... }
.ah-timeline-bar  { ... }
.ah-active        { background: var(--danger); }
.ah-resolved      { background: var(--text-muted); }

/* Duration badge */
.ah-ongoing { color: var(--danger); font-weight: 600; }

/* Kind filter */
.ah-kind-filter { display: flex; gap: 4px; }
.ah-kind-btn    { ... } /* same as panel-tab */
```

---

## Test cases (UI)

Los test cases son visuales / de integración manual. No se añaden tests Go.

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | Historial vacío | Sección oculta |
| TC-02 | 1 alerta resuelta | Sección visible, 1 fila, barra gris, duration visible |
| TC-03 | 1 alerta activa (resolved_at null) | Badge "active", barra roja, "ongoing" en duration |
| TC-04 | Filtro por kind | Solo muestra entradas del kind seleccionado |
| TC-05 | Filtros muestran solo kinds presentes | Si no hay "statistical", el botón no aparece |
| TC-06 | Timeline bars proporcionales | La alerta más larga tiene la barra más ancha |

---

## Out of scope

- Persistencia del historial entre reinicios (ya excluido en US-17).
- Paginación del historial.
- Filtrado por rango de tiempo (usa el historial completo en memoria).
- Gráfica de frecuencia de alertas por día.

---

## Dependencies

- US-17: `GET /alerts/history` implementado con `AlertRecord.Kind`, `ResolvedAt`.
- US-40/US-41: `kind` field en AlertRecord.
- US-49: `KindStatistical` para el filtro (si implementado antes).
- Dashboard existente: `panel-tab` CSS class, `METHOD_COLORS`, `statusClass`.
