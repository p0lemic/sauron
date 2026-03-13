# US-18 — Dashboard accesible en browser

**Épica:** Dashboard web (Fase 4)
**Prioridad:** Must
**Historia:** Como developer, abro `http://localhost:9090` y veo el dashboard sin instalar nada más.
**AC:** La UI sirve desde el mismo proceso Go. Sin build step en producción.

---

## Context

El servidor de métricas (`api.Server`) ya expone la API REST en el puerto 9090. US-18 añade la ruta `GET /` que sirve el dashboard HTML. El frontend está embebido en el binario Go mediante `embed.FS` — no hay archivos externos ni pasos de compilación extra.

Esta historia crea la **estructura base** del dashboard. Las historias US-19 (resumen global), US-20 (tabla de endpoints) y US-22 (badge de alertas) completarán el contenido.

---

## Behavior

### Rutas

- `GET /` → devuelve `index.html` con `Content-Type: text/html`.
- `GET /static/*` → sirve archivos estáticos embebidos (CSS, JS) con `Content-Type` apropiado.

### Página

Al abrir `http://localhost:9090` el browser muestra:

```
┌─────────────────────────────────────────────────────┐
│  API Profiler                          [badge alertas] │  ← header fijo
├─────────────────────────────────────────────────────┤
│                                                       │
│  [Resumen global]       ← placeholder US-19           │
│                                                       │
│  [Tabla de endpoints]   ← placeholder US-20           │
│                                                       │
└─────────────────────────────────────────────────────┘
```

- Header fijo con título "API Profiler".
- Secciones con IDs `#summary` y `#endpoints` (vacías en US-18, se rellenarán en US-19/US-20).
- La página incluye el JS de polling base: `setInterval(refresh, 5000)` donde `refresh()` está definida pero vacía en US-18.
- Sin dependencias externas (no CDN, no npm). CSS y JS inline o en archivos embebidos.

### Embed

Los assets viven en `dashboard/` y se embeben con:

```go
//go:embed dashboard
var assets embed.FS
```

---

## API contract

### `GET /`

**Response 200 OK** — `Content-Type: text/html; charset=utf-8`
Cuerpo: contenido de `index.html`.

**Response 405** — método distinto de GET.

---

## Cambios en código

### `dashboard/index.html` (nuevo)

HTML5 completo con:
- `<meta charset="utf-8">`, viewport responsive.
- `<title>API Profiler</title>`.
- `<header>` con el título y un `<span id="alert-badge">` vacío (US-22).
- `<main>` con `<section id="summary">` y `<section id="endpoints">`.
- `<script>` con `function refresh() {}` y `setInterval(refresh, 5000)`.
- CSS mínimo inline (o `<link rel="stylesheet" href="/static/style.css">`).

### `dashboard/static/style.css` (nuevo)

Estilos base: reset, tipografía, layout header+main, colores neutros.

### `dashboard/static/app.js` (nuevo)

Función `refresh()` vacía + `setInterval(refresh, 5000)`. Preparado para US-19/US-20.

### `api/server.go` (modificado)

```go
//go:embed all:dashboard
var dashboardFS embed.FS

// En NewServer:
mux.HandleFunc("/", s.handleDashboard)
mux.Handle("/static/", http.FileServer(http.FS(dashboardFS)))
```

`handleDashboard` sirve `dashboard/index.html`.

---

## Test cases

| TC | Scenario | Resultado esperado |
|----|----------|--------------------|
| TC-01 | `GET /` | 200, `Content-Type: text/html` |
| TC-02 | Body de `GET /` contiene `<title>API Profiler</title>` | Body incluye el título |
| TC-03 | Body contiene `id="summary"` e `id="endpoints"` | Secciones presentes |
| TC-04 | `POST /` | 405 Method Not Allowed |
| TC-05 | `GET /static/style.css` | 200, `Content-Type: text/css` |

---

## Out of scope

- Contenido real de las secciones — US-19, US-20, US-21, US-22.
- Autenticación del dashboard — Fase 2.
- Tema oscuro / modo claro — no en el PRD.
- PWA / service workers — no en el PRD.

---

## Dependencies

- `api.Server` existente (US-14).
- Paquete `embed` de Go stdlib.
- Sin nuevas dependencias externas.
