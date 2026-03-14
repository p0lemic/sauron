# US-26 — Path Normalization

## Context

APIs REST incluyen identificadores dinámicos en los paths: `/users/123`, `/orders/uuid-...`,
`/products/abc123`. Sin normalización cada ID único genera una entrada de métricas separada,
fragmentando los datos y haciendo imposible ver tendencias por endpoint.

## Behavior

### 1. Nuevo paquete `normalizer`

```
normalizer/
  normalizer.go
  normalizer_test.go
```

Expone:

```go
type Rule struct {
    Pattern     string // regexp aplicado a cada segmento del path
    Replacement string // texto de sustitución, e.g. ":id"
}

type Normalizer struct { ... }

// New compila las reglas y, si autoDetect=true, añade las reglas built-in al final.
func New(rules []Rule, autoDetect bool) (*Normalizer, error)

// Normalize aplica las reglas segmento a segmento y devuelve el path normalizado.
// Preserva barras iniciales y finales. Paths vacíos o "/" se devuelven sin cambio.
func (n *Normalizer) Normalize(path string) string
```

### 2. Reglas built-in (auto-detect, aplicadas cuando `normalize_paths: true`)

Se aplican en orden a **cada segmento** del path (entre `/`):

| Nombre   | Patrón                                                              | Sustitución |
|----------|---------------------------------------------------------------------|-------------|
| uuid     | `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$` | `:id` |
| integer  | `^[0-9]+$`                                                          | `:id`       |
| hex      | `^[0-9a-fA-F]{12,}$`                                               | `:id`       |

### 3. Config

Nuevos campos en `Config`:

```go
NormalizePaths bool       // default: true
PathRules      []PathRule // reglas custom, aplicadas ANTES de las built-in
```

```go
type PathRule struct {
    Pattern     string
    Replacement string
}
```

YAML:

```yaml
normalize_paths: true   # default true; false deshabilita toda normalización

path_rules:             # opcional; se aplican antes de las reglas built-in
  - pattern: "^v[0-9]+$"
    replacement: ":version"
  - pattern: "^[a-z]{2}-[A-Z]{2}$"
    replacement: ":locale"
```

`Default()` → `NormalizePaths: true`, `PathRules: nil`.

### 4. Integración en el proxy

`proxy.Config` gana un campo opcional:

```go
Normalizer func(path string) string // nil = no normalization
```

En `ServeHTTP`, antes de llamar a `Recorder.Record`:

```go
path := r.URL.Path
if h.cfg.Normalizer != nil {
    path = h.cfg.Normalizer(path)
}
h.cfg.Recorder.Record(storage.Record{..., Path: path, ...})
```

El log también usa el path normalizado.

### 5. Wiring en `cmd/profiler/main.go`

```go
var normFn func(string) string
if cfg.NormalizePaths {
    rules := make([]normalizer.Rule, len(cfg.PathRules))
    for i, r := range cfg.PathRules { rules[i] = normalizer.Rule{r.Pattern, r.Replacement} }
    n, err := normalizer.New(rules, true)
    if err != nil { fatal(...) }
    normFn = n.Normalize
}
// proxy.Config{..., Normalizer: normFn}
```

### 6. Comportamiento exacto

- Normalización es **por segmento**: cada parte entre `/` se evalúa independientemente.
- Las reglas **custom** se prueban primero; si ninguna coincide se prueban las built-in.
- El **primer match** gana (no se encadenan reglas sobre el mismo segmento).
- Segmentos que no matchean ninguna regla se dejan intactos.
- Query string y fragmento **no se tocan**.
- Trailing slash se preserva.

Ejemplos:

| Input                                      | Output                          |
|--------------------------------------------|---------------------------------|
| `/users/123`                               | `/users/:id`                    |
| `/users/123/orders/456`                    | `/users/:id/orders/:id`         |
| `/orders/550e8400-e29b-41d4-a716-446655440000` | `/orders/:id`               |
| `/tokens/a1b2c3d4e5f6`                     | `/tokens/:id`                   |
| `/api/v2/products`                         | `/api/v2/products` (sin cambio) |
| `/health`                                  | `/health` (sin cambio)          |
| `/users/john`                              | `/users/john` (sin cambio)      |

## API contract

No hay cambios en los endpoints HTTP. El cambio es transparente: los datos que llegan al
dashboard ya vienen normalizados desde el momento en que se graban.

## Test cases

### normalizer/normalizer_test.go

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-01 | Integer segment `/users/123` → `/users/:id`                              |
| TC-02 | Multiple integers `/a/1/b/2` → `/a/:id/b/:id`                            |
| TC-03 | UUID segment → `:id`                                                     |
| TC-04 | Hex ≥12 chars → `:id`                                                    |
| TC-05 | Hex < 12 chars → sin cambio                                              |
| TC-06 | Mixed text segment `john` → sin cambio                                   |
| TC-07 | Root `/` → sin cambio                                                    |
| TC-08 | Trailing slash preservada `/users/123/` → `/users/:id/`                  |
| TC-09 | Custom rule aplicada antes de built-in                                   |
| TC-10 | Custom rule inválida (regex roto) → `New` devuelve error                 |
| TC-11 | `autoDetect=false` + sin custom rules → path sin cambio                  |
| TC-12 | `autoDetect=false` + custom rules → solo custom rules aplicadas          |
| TC-13 | Query string no se toca (`/users/123?foo=bar` → `/users/:id?foo=bar`)    |
| TC-14 | `/api/v2/users` → sin cambio (v2 no es puro integer ni UUID)            |

### config (adiciones a config_test.go)

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-15 | `Default()` → `NormalizePaths=true`, `PathRules=nil`                    |
| TC-16 | YAML `normalize_paths: false` → `NormalizePaths=false`                  |
| TC-17 | YAML `path_rules` → slice de PathRule parseado correctamente            |

### proxy (adiciones a proxy handler_test.go)

| TC    | Descripción                                                              |
|-------|--------------------------------------------------------------------------|
| TC-18 | Normalizer configurado → Record.Path contiene path normalizado           |
| TC-19 | Normalizer nil → Record.Path contiene path original                      |

## Files changed

| File                          | Change                                              |
|-------------------------------|-----------------------------------------------------|
| `normalizer/normalizer.go`    | Nuevo paquete                                       |
| `normalizer/normalizer_test.go` | TC-01..TC-14                                      |
| `config/config.go`            | Campos `NormalizePaths`, `PathRule`, `PathRules`    |
| `config/config_test.go`       | TC-15..TC-17                                        |
| `proxy/handler.go`            | Campo `Normalizer` en `proxy.Config`; aplicar en `ServeHTTP` |
| `proxy/handler_test.go`       | TC-18..TC-19                                        |
| `cmd/profiler/main.go`        | Build del normalizer y wiring en `proxy.Config`     |

No cambia: `storage/`, `metrics/`, `alerts/`, `api/`, `cmd/dashboard/`.

## Out of scope

- Normalización de query params
- Normalización retroactiva de datos ya grabados en la DB
- Reglas basadas en OpenAPI / Swagger spec
