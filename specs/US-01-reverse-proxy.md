# US-01 — Reverse Proxy HTTP (Core)

**Épica:** Proxy core (Fase 1)
**Prioridad:** Must
**Historia:** Como developer, puedo apuntar mi cliente a API Profiler y las peticiones llegan al upstream sin modificación.

---

## Context

API Profiler se sitúa de forma transparente entre el cliente y el servicio API real (upstream). El upstream no sabe que el proxy existe: recibe exactamente la misma petición que habría recibido el cliente directamente. El cliente recibe exactamente la misma respuesta que habría enviado el upstream.

Esta historia es el núcleo fundacional del producto. Sin un proxy HTTP correcto y de baja latencia, ninguna otra historia tiene sentido.

**Problema que resuelve:** El equipo necesita un punto de intercepción para medir y observar el tráfico HTTP sin modificar ningún código de la aplicación observada.

---

## Behavior

### Ciclo de vida de un request

1. El proxy escucha en un puerto TCP configurable (defecto: `8080`).
2. Al recibir un request HTTP del cliente, el proxy captura el método, path, query string, headers y body.
3. Construye una nueva petición hacia la URL upstream configurada, preservando:
   - Método HTTP (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS).
   - Path completo y query string sin alteración.
   - Todos los headers originales del cliente.
   - El body del request (incluyendo peticiones con body vacío o sin Content-Length).
4. Ejecuta la petición al upstream usando un cliente HTTP con timeout configurable.
5. Recibe la respuesta del upstream y la reenvía al cliente preservando:
   - Status code exacto.
   - Todos los response headers del upstream.
   - El body completo de la respuesta (incluyendo responses de streaming y chunked transfer encoding).
6. La latencia añadida por el proxy es inferior a **1 ms en p99** bajo carga normal.

### Comportamiento ante errores upstream

- Si el upstream no está disponible (conexión rechazada), el proxy devuelve `502 Bad Gateway`.
- Si el upstream no responde dentro del timeout configurado, el proxy devuelve `504 Gateway Timeout`.
- Si el upstream devuelve un error HTTP (4xx, 5xx), el proxy lo reenvía tal cual al cliente sin intervención.

### Comportamiento ante errores de configuración

- Si no se especifica un upstream en el arranque, el binario falla con un mensaje de error claro y exit code 1.
- Si la URL upstream es malformada, el binario falla antes de arrancar el servidor.

### Graceful shutdown

- Al recibir `SIGTERM` o `SIGINT`, el proxy deja de aceptar nuevas conexiones.
- Espera a que los requests en vuelo completen (máximo 30 segundos).
- Sale con exit code 0.

### Logging mínimo

- Cada request logueado en stdout con formato: `<timestamp> <method> <path> <status> <duration_ms>`.
- Los headers `Authorization` y `Cookie` **no** se incluyen en los logs (seguridad).
- El body del request/response **no** se loguea.

---

## API contract

Esta historia no expone endpoints REST propios. Su contrato es el del protocolo HTTP estándar en el puerto de escucha.

### Configuración mínima (flags)

```
profiler --upstream <url> [--port <n>] [--timeout <duration>]
```

| Flag | Tipo | Defecto | Descripción |
|------|------|---------|-------------|
| `--upstream` | `string` | *(requerido)* | URL base del servicio upstream. Ej: `http://localhost:3000` |
| `--port` | `int` | `8080` | Puerto donde escucha el proxy. |
| `--timeout` | `duration` | `30s` | Timeout de la petición al upstream. |

### Interfaz interna Go (paquete `proxy`)

```go
// Config contiene la configuración del proxy.
type Config struct {
    Upstream *url.URL
    Port     int
    Timeout  time.Duration
}

// Handler implementa http.Handler. Recibe requests del cliente,
// los reenvía al upstream y escribe la respuesta de vuelta.
type Handler struct { ... }

// New crea un Handler validado. Devuelve error si Config es inválida.
func New(cfg Config) (*Handler, error)

// ServeHTTP implementa http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

---

## Test cases

### Happy path

1. **Proxy GET sin body:** El cliente hace `GET /api/users`. El upstream responde `200` con JSON. El cliente recibe exactamente el mismo JSON, status 200 y los mismos headers.

2. **Proxy POST con body:** El cliente hace `POST /api/users` con body `{"name":"Alice"}`. El upstream recibe el mismo body intacto y responde `201`. El cliente recibe `201` con el mismo body de respuesta.

3. **Proxy con query string:** `GET /search?q=foo&page=2` llega al upstream con `?q=foo&page=2` exacto.

4. **Proxy preserva headers personalizados:** El cliente envía `X-Request-ID: abc123`. El upstream recibe `X-Request-ID: abc123`.

5. **Proxy preserva Authorization:** El cliente envía `Authorization: Bearer token123`. El upstream recibe el header idéntico.

6. **Proxy con response headers múltiples:** El upstream responde con `X-RateLimit-Remaining: 99` y `Content-Type: application/json`. El cliente recibe ambos headers.

7. **Proxy con body grande (1 MB):** El body completo llega al upstream y la respuesta completa llega al cliente.

8. **Proxy con upstream que devuelve 404:** El cliente recibe `404` con el mismo body de error del upstream.

9. **Proxy con upstream que devuelve 500:** El cliente recibe `500` con el body de error del upstream.

10. **Todos los métodos HTTP:** GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS funcionan correctamente.

### Edge cases

11. **Upstream no disponible (conexión rechazada):** El cliente recibe `502 Bad Gateway` con body descriptivo.

12. **Upstream timeout:** Con `--timeout 1ms` y un upstream lento, el cliente recibe `504 Gateway Timeout`.

13. **Request con body vacío (GET):** No se intenta leer ni enviar body; el upstream no recibe Content-Length espurio.

14. **Response sin body (204 No Content):** El cliente recibe `204` sin body. No hay escritura de bytes de body.

15. **Upstream con redirect (301):** El proxy NO sigue el redirect automáticamente. Reenvía el `301` con el header `Location` al cliente para que sea él quien decida.

16. **Cabeceras hop-by-hop filtradas:** Los headers `Connection`, `Transfer-Encoding`, `Upgrade`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer` no se reenvían al upstream (comportamiento estándar de proxy HTTP/1.1).

17. **Upstream URL con path base:** Si `--upstream http://localhost:3000/v1`, un request a `/users` se reenvía a `http://localhost:3000/v1/users`.

18. **Puerto en uso:** Si el puerto de escucha ya está ocupado, el binario falla con mensaje de error claro y exit code 1.

19. **SIGTERM con request en vuelo:** El proxy espera a que el request en curso complete antes de cerrar.

20. **URL upstream sin scheme:** `--upstream localhost:3000` (sin `http://`) falla con error descriptivo al arrancar.

---

## Out of scope

- **Soporte HTTPS upstream** (tls_skip_verify) — cubierto en US-05.
- **Captura y persistencia de métricas** — cubierto en US-02.
- **Configuración por YAML** — cubierto en US-03.
- **Soporte gRPC** — fase 2 (v1.1).
- **Autenticación del dashboard** — fase 2.
- **Modificación de headers o body** de requests/responses — no está en el alcance del MVP.
- **Load balancing** hacia múltiples upstreams — no está en el alcance del MVP.

---

## Dependencies

- **Ninguna historia anterior.** US-01 es la primera historia y no tiene dependencias de otras historias de usuario.
- **Entorno técnico:** Go 1.22+, módulos estándar (`net/http`, `net/url`, `context`, `os/signal`).
- **Sin librerías externas** para el proxy core. `httputil.ReverseProxy` de la stdlib puede usarse como base, pero debe validarse que cumple todos los test cases (especialmente el filtrado de hop-by-hop headers y el no-seguimiento de redirects).
