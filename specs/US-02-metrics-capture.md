# US-02 — Captura de métricas por request

**Épica:** Proxy core (Fase 1)
**Prioridad:** Must
**Historia:** Como developer, cada request queda registrado con timestamp, método, path, status y duración.
**AC:** Registro disponible en storage < 100 ms tras completarse el request.

---

## Context

US-01 ya proxía las peticiones de forma transparente. US-02 añade la capa de observabilidad: cada request que pasa por el proxy genera un registro persistente que servirá de base para los percentiles (US-07), el top de endpoints lentos (US-09) y el dashboard (US-18/US-20).

El registro NO modifica el flujo del request: el cliente recibe su respuesta exactamente igual que en US-01. Si el almacenamiento falla, el proxy sigue funcionando en modo degradado (requisito NFR 4.2).

---

## Behavior

### Ciclo de vida de un registro

1. El proxy completa la respuesta al cliente (la última llamada a `Write` o `WriteHeader` ha finalizado).
2. El proxy emite un `Record` con los campos: timestamp de inicio del request, método HTTP, path (sin query string), status code devuelto al cliente, duración total en milisegundos.
3. El `Record` se entrega de forma **no bloqueante** al `Recorder`; el goroutine del request no espera a que la escritura en storage termine.
4. El `Recorder` persiste el registro en SQLite. El registro es consultable en un tiempo máximo de **100 ms** tras la emisión.

### Semántica de los campos

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `Timestamp` | `time.Time` | Instante en que el proxy recibió la petición (antes de reenviarla). |
| `Method` | `string` | Método HTTP en mayúsculas: `GET`, `POST`, etc. |
| `Path` | `string` | Path del request **sin query string**. Ej: `/api/users`. |
| `StatusCode` | `int` | Status code devuelto al cliente (incluye 502/504 generados por el proxy). |
| `DurationMs` | `float64` | Duración total desde recepción hasta último byte de respuesta, en milisegundos. |

### Comportamiento en modo degradado

- Si el `Recorder` no puede escribir en storage (disco lleno, corrupción, etc.), descarta el registro y loguea el error en stderr.
- El goroutine del request **no** es bloqueado ni afectado.
- No se loguea el error por cada request fallido; se usa un contador de errores interno para limitar el ruido.

### No-blocking contract

- La entrega del `Record` al `Recorder` usa un canal interno con capacidad `bufferSize` (configurable, defecto: 1000).
- Si el canal está lleno (backpressure extremo), el registro se descarta con un warning en stderr y el request no se bloquea.
- Un goroutine background drena el canal y escribe en storage. Se lanza al crear el `Recorder` y se para en `Close()`.

### Persistencia SQLite

La tabla `requests` almacena los registros:

```sql
CREATE TABLE IF NOT EXISTS requests (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp   DATETIME NOT NULL,
    method      TEXT     NOT NULL,
    path        TEXT     NOT NULL,
    status_code INTEGER  NOT NULL,
    duration_ms REAL     NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_requests_path_method
    ON requests (method, path);

CREATE INDEX IF NOT EXISTS idx_requests_timestamp
    ON requests (timestamp);
```

La base de datos se crea en la ruta configurada (defecto: `profiler.db` en el directorio de trabajo). Si el fichero no existe, se crea al arrancar.

---

## API contract

### Tipo `Record` (paquete `storage`)

```go
// Record representa los metadatos de un único request proxiado.
type Record struct {
    Timestamp  time.Time
    Method     string
    Path       string
    StatusCode int
    DurationMs float64
}
```

### Interfaz `Store` (paquete `storage`)

```go
// Store persiste y consulta registros de requests.
type Store interface {
    // Save persiste un registro. Retorna error si la escritura falla.
    Save(r Record) error

    // Close libera recursos (cierra la conexión SQLite).
    Close() error
}

// New abre o crea la base de datos SQLite en dbPath y aplica el schema.
// Retorna error si la ruta es inválida o la base de datos no puede abrirse.
func New(dbPath string) (Store, error)
```

### Interfaz `Recorder` (paquete `storage`)

```go
// Recorder acepta records de forma no bloqueante y los persiste en background.
type Recorder struct { ... }

// NewRecorder crea un Recorder sobre el Store dado.
// bufferSize controla la capacidad del canal interno (defecto si 0: 1000).
func NewRecorder(store Store, bufferSize int) *Recorder

// Record encola el registro para persistencia. No bloqueante.
// Si el canal está lleno, descarta el registro y loguea un warning.
func (rec *Recorder) Record(r Record)

// Close drena el canal y para el goroutine background. Espera máximo 5s.
func (rec *Recorder) Close() error
```

### Integración con `proxy.Handler`

`proxy.Config` acepta un `Recorder` opcional:

```go
type Config struct {
    Upstream *url.URL
    Port     int
    Timeout  time.Duration
    Recorder *storage.Recorder // nil = captura desactivada
}
```

Cuando `Recorder != nil`, al finalizar cada `ServeHTTP` el handler llama:

```go
cfg.Recorder.Record(storage.Record{
    Timestamp:  start,
    Method:     r.Method,
    Path:       r.URL.Path,
    StatusCode: sw.code,
    DurationMs: float64(time.Since(start).Microseconds()) / 1000,
})
```

---

## Test cases

### Happy path

1. **Registro completo:** Tras un `GET /api/users` con respuesta 200 de 5 ms, el storage contiene un registro con `Method=GET`, `Path=/api/users`, `StatusCode=200`, `DurationMs≈5`.

2. **Path sin query string:** Un `GET /search?q=foo` genera `Path=/search` (la query string no se persiste).

3. **POST con status 201:** El registro refleja `Method=POST`, `StatusCode=201`.

4. **Error proxy 502:** Cuando el upstream no está disponible el registro refleja `StatusCode=502`.

5. **Error proxy 504:** Cuando el upstream hace timeout el registro refleja `StatusCode=504`.

6. **Timestamp correcto:** El `Timestamp` del registro está dentro de ±1s del instante en que el test lanzó el request.

7. **Múltiples requests:** 100 requests concurrentes generan exactamente 100 registros en storage.

8. **Recorder desactivado (nil):** Con `Config.Recorder = nil`, el proxy funciona normalmente y no escribe nada en storage.

9. **Disponibilidad < 100 ms:** Tras la respuesta al cliente, el registro es consultable en storage en menos de 100 ms.

### Edge cases

10. **Buffer lleno:** Con `bufferSize=1` y un store que tarda 200 ms por escritura, el segundo registro concurrente es descartado sin bloquear al cliente. El proxy devuelve la respuesta con latencia normal.

11. **Store falla en Save:** Si `Save` retorna error, el Recorder loguea en stderr pero el request del cliente completa con normalidad (status correcto, sin panic).

12. **Close drena el canal:** Tras `Recorder.Close()`, todos los registros encolados antes del cierre han sido escritos en storage.

13. **Base de datos nueva:** Si `profiler.db` no existe, `storage.New` la crea con el schema correcto y la primera escritura tiene éxito.

14. **Ruta de base de datos inválida:** `storage.New("/ruta/inexistente/profiler.db")` retorna error descriptivo.

15. **Duración mínima:** Un request que tarda < 1 ms tiene `DurationMs >= 0` (nunca negativo).

16. **Restart — datos persistentes:** Los registros escritos antes de un cierre y reapertura de la base de datos son accesibles tras reabrir con `storage.New` sobre el mismo fichero.

---

## Out of scope

- **Agregación de percentiles** (p50/p95/p99) — cubierto en US-07.
- **Retención y purga de datos antiguos** — cubierto en la historia de configuración de retención (Fase 2).
- **Captura del body del request/response** — excluido explícitamente por seguridad (NFR 4.3).
- **Captura de headers** — excluido; solo se registran los metadatos listados.
- **Exportación a sistemas externos** (Prometheus, InfluxDB) — fase posterior.
- **Configuración de la ruta de la base de datos por YAML** — cubierto en US-03; aquí se implementa el parámetro pero se expone solo por flag `--db-path`.

---

## Dependencies

- **US-01** debe estar implementado: `proxy.Handler`, `proxy.Config`, `statusWriter`.
- **Sin dependencias de otras historias pendientes.**
- **Librería externa:** `github.com/mattn/go-sqlite3` (CGO) o `modernc.org/sqlite` (pure Go, sin CGO). Se prefiere `modernc.org/sqlite` para mantener el binario estático cross-platform sin CGO.
