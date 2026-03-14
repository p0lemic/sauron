# US-37 — Header Rewrite Rules

## Context

En despliegues reales el proxy necesita manipular headers antes de reenviar la
request al upstream: inyectar API keys, propagar `X-Forwarded-By`, eliminar
headers internos que no deben salir, etc. Las reglas se definen en YAML y se
aplican a cada request entrante antes del forwarding.

## Behavior

### 1. Nuevo tipo `HeaderRule` en config

```go
// HeaderRule defines one header manipulation to apply to every proxied request.
type HeaderRule struct {
    Action string // "set" | "remove"
    Header string // canonical header name, e.g. "X-Api-Key"
    Value  string // used only for action "set"
}
```

Config YAML:
```yaml
header_rules:
  - action: set
    header: X-Forwarded-By
    value:  api-profiler
  - action: remove
    header: X-Internal-Secret
```

- `action: set` — añade el header si no existe, sobreescribe si ya existe.
- `action: remove` — elimina el header de la request.
- `value` se ignora para `action: remove`.
- Los nombres de header se canonicalizan automáticamente (`x-api-key` → `X-Api-Key`).
- Las reglas se aplican en orden.

### 2. `proxy.Config` — campo `RewriteHeaders`

```go
type Config struct {
    // ...existing fields...
    RewriteHeaders func(h http.Header) // nil disables header rewriting
}
```

La función se construye en `cmd/profiler/main.go` a partir de `cfg.HeaderRules`
y se pasa al proxy. El paquete `proxy` no depende de `config`.

### 3. Aplicación en `proxy/handler.go`

En el `Director` del reverse proxy, tras fijar scheme/host/path:

```go
if h.cfg.RewriteHeaders != nil {
    h.cfg.RewriteHeaders(req.Header)
}
```

### 4. Validación en `config.Validate`

- Si `action` no es `"set"` ni `"remove"` → error.
- Si `header` está vacío → error.
- Si `action == "set"` y `value` está vacío → error.

### 5. `cmd/profiler/main.go` — construcción de `RewriteHeaders`

```go
var rewriteFn func(http.Header)
if len(cfg.HeaderRules) > 0 {
    rules := cfg.HeaderRules // capture
    rewriteFn = func(h http.Header) {
        for _, r := range rules {
            key := http.CanonicalHeaderKey(r.Header)
            switch r.Action {
            case "set":
                h.Set(key, r.Value)
            case "remove":
                h.Del(key)
            }
        }
    }
}
```

## API contract

No hay cambios de API HTTP. La funcionalidad es exclusivamente en el proxy.

## Test cases

### config (config/config_test.go adiciones)

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-01 | YAML con header_rules → carga correctamente                              |
| TC-02 | action inválido → Validate retorna error                                 |
| TC-03 | header vacío → Validate retorna error                                    |
| TC-04 | action=set con value vacío → Validate retorna error                      |

### proxy (proxy/handler_test.go adiciones)

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-05 | RewriteHeaders=nil → no modifica headers                                 |
| TC-06 | action=set → header presente en request recibida por upstream            |
| TC-07 | action=set → sobreescribe header existente                               |
| TC-08 | action=remove → header eliminado de la request al upstream              |
| TC-09 | Reglas aplicadas en orden (set + remove sobre mismo header)              |

## Files changed

| File                        | Change                                                    |
|-----------------------------|-----------------------------------------------------------|
| `config/config.go`          | `HeaderRule` struct, `HeaderRules []HeaderRule` en Config, YAML parsing, Validate |
| `config/config_test.go`     | TC-01..TC-04                                              |
| `proxy/handler.go`          | `RewriteHeaders func(http.Header)` en Config, aplicar en Director |
| `proxy/handler_test.go`     | TC-05..TC-09                                              |
| `cmd/profiler/main.go`      | Construir `rewriteFn` a partir de `cfg.HeaderRules`       |

No cambia: `storage/`, `metrics/`, `api/`, `alerts/`, `normalizer/`, `cmd/dashboard/`.

## Out of scope

- Reescritura de headers de respuesta (solo request)
- Interpolación de variables en `value` (ej. `${client_ip}`)
- Reglas condicionales (solo si header existe/no existe)
- Modificar headers de la respuesta al cliente
