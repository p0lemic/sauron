# US-06 — Preservación de Headers

**Épica:** Proxy core (Fase 1)
**Prioridad:** Must
**Historia:** Como developer, los headers originales del cliente (Authorization, etc.) se reenvían al upstream intactos.
**AC:** Test con header `Authorization`, `X-Custom-Header`.

---

## Context

`httputil.ReverseProxy` ya reenvía los headers del cliente al upstream como parte de su comportamiento base (implementado en US-01). US-06 formaliza ese contrato con una suite de tests explícita que cubre los headers más relevantes para APIs: autenticación, correlación, cookies y multi-valor.

Esta historia no requiere nuevos cambios en el código de producción. Su entregable principal es la suite de tests que garantiza que ninguna historia futura rompa el reenvío de headers.

---

## Behavior

### Headers de request (cliente → upstream)

- Todos los headers del cliente se reenvían al upstream sin modificación de nombre, valor ni orden dentro de un mismo header.
- Headers de múltiples valores (ej. `Accept: text/html, application/json`) se reenvían completos.
- Headers con múltiples líneas del mismo nombre (ej. dos líneas `Set-Cookie`) se reenvían todos.
- La capitalización canónica de Go (`Authorization`, `X-Request-Id`) se aplica al reenviar, lo cual es conforme a HTTP/1.1 (los nombres de header son case-insensitive según RFC 7230).

### Headers de response (upstream → cliente)

- Todos los headers de la respuesta del upstream se reenvían al cliente sin modificación.
- Headers de múltiples valores en la respuesta también se preservan.

### Headers excluidos (hop-by-hop — RFC 7230 §6.1)

Los siguientes headers **no** se reenvían al upstream (comportamiento estándar de proxy, ya cubierto en US-01 TC-16):
`Connection`, `Transfer-Encoding`, `Upgrade`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`.

### Headers no logueados

`Authorization` y `Cookie` son reenviados al upstream pero **nunca** aparecen en los logs del proxy (seguridad, NFR 4.3 del PRD). El comportamiento de no-logging no es responsabilidad de esta historia (ya garantizado en US-01), pero se documenta aquí para claridad.

---

## API contract

No hay cambios en el API de producción. Esta historia es íntegramente tests.

---

## Test cases

### Request headers (cliente → upstream)

1. **Authorization Bearer:** `Authorization: Bearer token123` llega al upstream intacto.

2. **Authorization Basic:** `Authorization: Basic dXNlcjpwYXNz` llega al upstream intacto.

3. **X-Custom-Header genérico:** `X-Custom-Header: my-value` llega al upstream con el mismo valor.

4. **Cookie header:** `Cookie: session=abc; user=alice` llega al upstream intacto.

5. **Múltiples headers distintos:** `Authorization`, `X-Request-ID` y `X-Tenant-ID` enviados juntos llegan todos al upstream.

6. **Header con múltiples valores en una línea:** `Accept: text/html, application/json` llega al upstream con el valor exacto.

7. **Múltiples líneas del mismo header de request:** Dos entradas `X-Forwarded-For` (si el cliente las envía) llegan ambas al upstream.

8. **Content-Type en POST:** `Content-Type: application/json` llega al upstream en requests con body.

9. **Header con valor vacío:** `X-Empty:` (valor vacío) se reenvía sin error.

### Response headers (upstream → cliente)

10. **Header de respuesta personalizado:** Upstream devuelve `X-Trace-ID: xyz`. El cliente recibe `X-Trace-ID: xyz`.

11. **Múltiples Set-Cookie en respuesta:** Upstream devuelve dos headers `Set-Cookie`. El cliente recibe ambos.

12. **Content-Type en respuesta:** `Content-Type: application/json; charset=utf-8` llega al cliente exacto.

### Hop-by-hop (ya cubiertos en US-01 TC-16, se documentan aquí para completitud)

13. **Connection no llega al upstream** (ya en US-01 TC-16).

---

## Out of scope

- **Enmascaramiento de headers en logs** — comportamiento de logging ya definido en US-01.
- **Modificación o inyección de headers** — el proxy nunca altera headers de negocio.
- **Header `X-Forwarded-For`** — `httputil.ReverseProxy` lo gestiona automáticamente (añade la IP del cliente); no es responsabilidad de esta historia modificar ese comportamiento.
- **Trailing headers (HTTP/1.1 trailers)** — no en MVP.

---

## Dependencies

- **US-01** implementado: `proxy.Handler`, `httputil.ReverseProxy` como base.
- No hay dependencias adicionales.
