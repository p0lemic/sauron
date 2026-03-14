# US-43 — Data retention (limpieza automática de registros antiguos)

## Context

El campo `retention` ya existe en la configuración (`config.Config.Retention`) y se
parsea correctamente desde YAML y variables de entorno, pero el comentario en el
código dice "accepted but not enforced until Phase 2". Sin este mecanismo, la base
de datos SQLite (y el equivalente en PostgreSQL) crece indefinidamente. En producción
con ~100 req/s esto supone ~8 M de filas/día y la DB puede alcanzar varios GB en
semanas.

## Behavior

Al iniciar el proxy (cmd/profiler), si `retention > 0`, se lanza una goroutine de
limpieza periódica que elimina de la tabla `requests` todas las filas cuyo `timestamp`
sea anterior a `now - retention`. El intervalo de limpieza es configurable
internamente con un valor razonable por defecto (cada hora). La goroutine se detiene
limpiamente cuando el proceso recibe SIGTERM/SIGINT (respeta el mismo ciclo de vida
que el resto del sistema).

Reglas:
- `retention = 0` (valor por defecto) → limpieza desactivada; comportamiento actual
  sin cambios.
- `retention > 0` → al arrancar se elimina el backlog histórico que exceda la
  retención, luego se repite cada hora.
- La limpieza se hace con una única sentencia SQL eficiente aprovechando el índice
  existente `idx_requests_timestamp`.
- Si la limpieza falla (error de DB), se loguea el error y se reintenta en el
  siguiente ciclo; el proxy no se detiene.
- El número de filas eliminadas se loguea en nivel INFO:
  `pruned N records older than <threshold>`.

## API contract

No hay endpoint nuevo. La funcionalidad es puramente interna (background goroutine +
método en storage).

Nueva firma en la interfaz `Store` / implementación SQLite y PostgreSQL:

```go
// Prune elimina todos los registros con timestamp anterior a before.
// Devuelve el número de filas eliminadas.
Prune(before time.Time) (int64, error)
```

Nueva función en el package `storage`:

```go
// NewPruner returns a Pruner that deletes records older than retention
// from store every interval. Call Start() to begin and Stop() to halt.
func NewPruner(store Store, retention time.Duration, interval time.Duration) *Pruner
```

`Pruner` tiene los métodos `Start()` y `Stop()`.

## Test cases

**storage (TC-01..TC-04)**

TC-01 **Prune elimina filas antiguas**: insertar 3 registros con timestamps
distintos (2 en el pasado más allá del umbral, 1 reciente). Llamar
`Prune(time.Now().Add(-30*time.Minute))`. Verificar que devuelve `n=2` y que
solo queda 1 fila en la DB.

TC-02 **Prune sin filas elegibles**: todos los registros son recientes. Llamar
`Prune`. Verificar que devuelve `n=0` y todos los registros siguen presentes.

TC-03 **Prune en DB vacía**: llamar `Prune` sin registros. Devuelve `n=0`, sin error.

TC-04 **Prune elimina exactamente en el umbral**: fila con timestamp == `before` NO
se elimina (comparación `< before`, no `<= before`).

**Pruner (TC-05..TC-06)**

TC-05 **Pruner ejecuta limpieza automáticamente**: crear un Pruner con intervalo
corto (10ms), insertar registros antiguos, llamar `Start()`, esperar 2 intervalos,
llamar `Stop()`, verificar que los registros antiguos han sido eliminados.

TC-06 **Pruner con retention=0 no elimina nada**: aunque se llame `Start()`, si
`retention == 0` el Pruner no ejecuta ninguna limpieza (early-return en el tick
handler).

**cmd/profiler integration (TC-07)**

TC-07 **Flag --retention**: el binario `cmd/profiler` acepta `--retention 7d` y
lo pasa correctamente a `config.Merge`. Verificado mediante la suite de tests
existente en `cmd/dashboard` (patrón similar a otros flags).

## Out of scope

- Limpieza de datos de PostgreSQL con particiones o TTL nativo
- API para consultar el espacio en disco o número de filas totales
- Limpieza de alert history (in-memory, fuera de scope por ahora)
- Configurar el intervalo de limpieza vía YAML/flag (hardcoded: 1h)

## Dependencies

- US-02 storage.Store (Save, Reader)
- US-03 config.Config.Retention (ya existe, solo falta wire-up)
- US-23 openSQLite, openPostgres (ambos necesitan implementar Prune)
