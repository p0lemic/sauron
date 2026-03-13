# US-05 — Soporte HTTPS Upstream

**Épica:** Proxy core (Fase 1)
**Prioridad:** Should
**Historia:** Como operator, el upstream puede ser HTTPS con certificado autofirmado (skip verify configurable).
**AC:** Opción `tls_skip_verify` en yaml y `--tls-skip-verify` en flags.

---

## Context

Muchos entornos internos exponen APIs con HTTPS pero usan certificados autofirmados o emitidos por una CA privada no instalada en el sistema donde corre API Profiler (entornos de desarrollo, Kubernetes internos, staging). Sin `tls_skip_verify`, el proxy rechazaría estas conexiones con un error de certificado, impidiendo su uso en esos entornos.

Esta historia añade la opción de deshabilitar la verificación TLS del upstream, manteniendo la verificación activa por defecto (seguro para producción).

---

## Behavior

### TLS con verificación (por defecto)

- Si el upstream es `https://...`, el proxy valida el certificado usando el pool de CAs del sistema operativo.
- Si la verificación falla (certificado autofirmado, CA desconocida, nombre incorrecto), el proxy devuelve `502 Bad Gateway` al cliente.
- Comportamiento idéntico al de `http.DefaultTransport`.

### TLS sin verificación (`tls_skip_verify: true`)

- El proxy acepta cualquier certificado del upstream sin validarlo.
- Se registra un warning en stderr al arrancar: `warning: TLS verification disabled for upstream — do not use in production`.
- Esta opción afecta únicamente a la conexión proxy → upstream. La conexión cliente → proxy no se ve afectada (el proxy siempre escucha en HTTP plano en Fase 1).

### HTTP upstream (sin cambios)

- Un upstream `http://...` sigue funcionando exactamente igual, independientemente del valor de `tls_skip_verify`.

---

## API contract

### Cambios en `config.Config`

```go
type Config struct {
    Upstream      string
    Port          int
    Timeout       time.Duration
    DBPath        string
    Retention     time.Duration
    TLSSkipVerify bool          // nuevo campo
}
```

### Cambios en el YAML

```yaml
tls_skip_verify: true   # defecto: false
```

```go
type yamlFile struct {
    // ...campos existentes...
    TLSSkipVerify bool `yaml:"tls_skip_verify"`
}
```

### Nuevo flag CLI

```
--tls-skip-verify    disable TLS certificate verification for upstream (default: false)
```

### Cambios en `proxy.Config`

```go
type Config struct {
    Upstream      *url.URL
    Port          int
    Timeout       time.Duration
    Recorder      *storage.Recorder
    TLSSkipVerify bool          // nuevo campo
}
```

### Cambios en `proxy.New`

Cuando `cfg.TLSSkipVerify` es `true`, el transport interno se construye con:

```go
&http.Transport{
    TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}
```

Cuando es `false` (defecto), se usa `http.DefaultTransport`.

---

## Test cases

### Happy path

1. **HTTPS upstream con cert válido:** El proxy reenvía peticiones a un upstream HTTPS con certificado firmado por una CA de confianza. La respuesta llega al cliente correctamente.

2. **HTTPS upstream con cert autofirmado + skip_verify=true:** Con `TLSSkipVerify: true`, el proxy acepta el certificado autofirmado y la petición llega al upstream correctamente.

3. **HTTP upstream + skip_verify=true:** Un upstream `http://` sigue funcionando con `TLSSkipVerify: true`; el campo es ignorado para conexiones no-TLS.

4. **HTTP upstream + skip_verify=false (defecto):** Sigue funcionando igual que en US-01.

5. **Warning en arranque:** Con `TLSSkipVerify: true`, el log de stderr incluye el mensaje de warning antes del primer request.

### Edge cases

6. **HTTPS upstream con cert autofirmado + skip_verify=false (defecto):** El proxy devuelve `502 Bad Gateway`; la respuesta al cliente no contiene datos del upstream.

7. **HTTPS upstream con cert expirado + skip_verify=false:** El proxy devuelve `502 Bad Gateway`.

8. **HTTPS upstream con cert expirado + skip_verify=true:** El proxy acepta el certificado expirado y reenvía la petición (skip_verify desactiva toda validación).

9. **`tls_skip_verify: false` en YAML (explícito):** Comportamiento idéntico al defecto; la verificación TLS está activa.

10. **Flag `--tls-skip-verify` sobreescribe YAML `tls_skip_verify: false`:** La precedencia flags > YAML se mantiene para este campo booleano.

---

## Out of scope

- **TLS en la conexión cliente → proxy** (proxy escuchando en HTTPS) — no en Fase 1.
- **Certificados de cliente** (mTLS hacia el upstream) — Fase 2.
- **CA personalizada** (`--tls-ca-cert`) — Fase 2; para MVP se usa el pool del sistema o skip_verify.
- **SNI personalizado** — no en MVP.

---

## Dependencies

- **US-01** implementado: `proxy.Handler`, `timeoutTransport`.
- **US-03** implementado: `config.Config`, YAML parsing, flag.Visit.
