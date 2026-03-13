# US-03 — Configuración por YAML

**Épica:** Proxy core (Fase 1)
**Prioridad:** Must
**Historia:** Como operator, puedo definir upstream, puerto y retención en un fichero `profiler.yaml`.
**AC:** El binario arranca con `--config profiler.yaml` y aplica la configuración.

---

## Context

US-04 (flags CLI) ya permite arrancar el proxy con parámetros en línea de comandos. US-03 añade una fuente de configuración declarativa en YAML, pensada para entornos donde el binario se gestiona como servicio (systemd, Docker, CI/CD) y no es práctico pasar todos los parámetros como flags en cada arranque.

El YAML actúa como capa base: los flags CLI sobreescriben cualquier valor del fichero. Esto permite usar el YAML para configuración estable y sobreescribir parámetros puntuales en tiempo de ejecución sin modificar el fichero.

---

## Behavior

### Carga del fichero

1. Si se proporciona `--config <ruta>`, el binario lee el fichero YAML antes de aplicar los demás flags.
2. Si el fichero no existe o no es un YAML válido, el binario imprime un error descriptivo en stderr y sale con exit code 1.
3. Si `--config` no se proporciona, el comportamiento es idéntico al de US-04: solo se usan flags.

### Precedencia de valores

```
flags CLI  >  profiler.yaml  >  valores por defecto
```

Un flag presente en línea de comandos siempre tiene prioridad sobre el mismo campo en el YAML. Esto permite sobreescribir parámetros individuales sin editar el fichero.

### Campos soportados en YAML (Fase 1)

```yaml
upstream: http://localhost:3000   # URL base del upstream (requerido si no hay --upstream)
port: 8080                        # Puerto de escucha del proxy
timeout: 30s                      # Timeout de peticiones al upstream (formato Go duration)
db_path: profiler.db              # Ruta del fichero SQLite de métricas
retention: 7d                     # Retención de datos (aceptado pero no aplicado hasta Fase 2)
```

Campos no reconocidos en el YAML son ignorados silenciosamente (forward-compatibility).

### Validación

Las mismas reglas de validación que en US-04 se aplican después de resolver la configuración final:
- `upstream` requerido si no se proporciona `--upstream`.
- `upstream` debe incluir scheme `http://` o `https://`.
- `port` debe ser 1–65535.
- `timeout` debe ser una duración Go válida y positiva.
- `retention` debe ser una duración válida y positiva si se especifica.

### Fichero de ejemplo

El repositorio incluye `profiler.yaml.example` que sirve como documentación viva del formato:

```yaml
# profiler.yaml.example
upstream: http://localhost:3000
port: 8080
timeout: 30s
db_path: profiler.db
retention: 7d
```

---

## API contract

### Paquete `config`

```go
// Config is the resolved configuration after merging YAML and CLI flags.
type Config struct {
    Upstream  string        // URL base del upstream
    Port      int           // Puerto de escucha (defecto: 8080)
    Timeout   time.Duration // Timeout upstream (defecto: 30s)
    DBPath    string        // Ruta SQLite (defecto: "profiler.db")
    Retention time.Duration // Retención de datos (defecto: 0 = sin límite)
}

// Load reads the YAML file at path and returns a Config with defaults applied.
// Returns error if the file cannot be read or parsed.
func Load(path string) (Config, error)

// Default returns a Config with all default values and no YAML source.
func Default() Config

// Merge applies overrides on top of base. Only non-zero values in overrides
// replace the corresponding field in base.
func Merge(base, overrides Config) Config
```

### Integración en `cmd/profiler/main.go`

```
1. Parsear --config flag (si presente, llamar config.Load)
2. Construir Config de flags (solo los flags explícitamente provistos)
3. config.Merge(yamlConfig, flagConfig)  →  resolvedConfig
4. Validar resolvedConfig
5. Arrancar proxy con resolvedConfig
```

Para detectar qué flags fueron explícitamente provistos se usa `flag.Visit` (solo visita los flags que aparecen en la línea de comandos).

### Binding YAML ↔ struct

Se usa `gopkg.in/yaml.v3` (ya disponible como dependencia transitiva de testify).

```go
type yamlFile struct {
    Upstream  string `yaml:"upstream"`
    Port      int    `yaml:"port"`
    Timeout   string `yaml:"timeout"`   // string para parsear duración
    DBPath    string `yaml:"db_path"`
    Retention string `yaml:"retention"` // string para parsear duración
}
```

---

## Test cases

### Happy path

1. **YAML completo:** `--config profiler.yaml` con todos los campos rellenos. La config resultante refleja los valores del YAML.

2. **Flag sobreescribe YAML:** YAML tiene `port: 9000`, flag `--port 8080`. La config resultante usa `port: 8080`.

3. **Flag parcial + YAML base:** YAML tiene `upstream`, `port` y `timeout`. Solo se pasa `--port 7070` en CLI. La config resultante usa `upstream` y `timeout` del YAML y `port` del flag.

4. **Solo flags (sin --config):** Funciona igual que US-04. `--upstream http://localhost:3000` arranca sin YAML.

5. **YAML con campos parciales:** YAML solo tiene `upstream`. Los demás campos toman sus valores por defecto.

6. **Timeout en YAML:** `timeout: 5s` se parsea correctamente a `5 * time.Second`.

7. **Retention en YAML:** `retention: 7d` — actualmente aceptado y almacenado en Config; no tiene efecto en comportamiento hasta Fase 2.

8. **Campos desconocidos en YAML son ignorados:** YAML con `foo: bar` no produce error.

### Edge cases

9. **Fichero no existe:** `--config /tmp/no_existe.yaml` → error con el path en el mensaje, exit 1.

10. **YAML mal formado:** Contenido no parseable como YAML → error descriptivo, exit 1.

11. **upstream vacío en YAML y sin --upstream flag:** Error "upstream is required", exit 1.

12. **Port fuera de rango:** `port: 99999` en YAML → error de validación, exit 1.

13. **Timeout inválido en YAML:** `timeout: "not-a-duration"` → error de validación, exit 1.

14. **Retention inválida en YAML:** `retention: "bad"` → error de validación, exit 1.

15. **YAML vacío (fichero existe pero sin contenido):** Se trata como config parcial vacía; los campos por defecto se aplican; upstream sigue siendo requerido.

16. **upstream en YAML sin scheme:** `upstream: localhost:3000` → error de validación (debe incluir http/https), exit 1.

---

## Out of scope

- **Aplicación de la retención** (purga de registros antiguos) — Fase 2.
- **Hot reload del fichero de configuración** (SIGHUP) — no en MVP.
- **Variables de entorno como fuente de configuración** — Fase 2.
- **Configuración de TLS** (`tls_skip_verify`) — cubierta en US-05.
- **Configuración de la ventana de agregación** (`window`) — cubierta en US-08.
- **Configuración de anomalías** (`anomaly_threshold`, etc.) — Épica 3.

---

## Dependencies

- **US-01** y **US-04** implementados: `proxy.Config`, flags `--upstream`, `--port`, `--timeout`.
- **US-02** implementado: flag `--db-path`.
- **`gopkg.in/yaml.v3`** ya disponible en `go.sum` como dependencia transitiva.
