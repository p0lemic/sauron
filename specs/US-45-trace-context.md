# US-45 — W3C TraceContext propagation

## Context

W3C TraceContext (https://www.w3.org/TR/trace-context/) es el estándar para propagar contexto
de trazas distribuidas entre servicios HTTP mediante los headers `traceparent` y `tracestate`.
El proxy está en posición ideal para ser el punto de entrada de trazas: puede generar trace IDs
cuando el cliente no los provee, propagarlos al upstream, y almacenarlos por request. Esto
permite correlacionar un request registrado en Sauron con trazas en otros sistemas (Jaeger,
Tempo, etc.) sin depender de SDKs externos.

## Behavior

### Lectura del header entrante

Al recibir un request del cliente, el proxy intenta parsear el header `traceparent`:

```
traceparent: 00-<trace-id>-<parent-id>-<flags>
```

- `trace-id`: 32 hex chars (16 bytes), debe ser distinto de 32 ceros.
- `parent-id`: 16 hex chars (8 bytes), debe ser distinto de 16 ceros.
- `flags`: 2 hex chars; solo se lee el bit 0 (sampled).

Si `traceparent` está presente y es válido → se reutiliza el `trace-id`.
Si `traceparent` está ausente o es inválido → se genera un nuevo `trace-id` aleatorio.

### Generación del span propio

El proxy siempre genera un nuevo `span-id` de 8 bytes aleatorios (16 hex chars). Este span
representa el hop proxy→upstream.

### Propagación al upstream

El proxy reescribe (o añade) `traceparent` en el request hacia el upstream:

```
traceparent: 00-<trace-id>-<nuevo-span-id>-01
```

`flags` siempre `01` (sampled) — el proxy samplea el 100 % de los requests.

El header `tracestate` del cliente (si existe) se propaga sin modificaciones al upstream.

### Almacenamiento

Se añaden dos columnas a la tabla `requests`:

```sql
trace_id TEXT NOT NULL DEFAULT '',
span_id  TEXT NOT NULL DEFAULT ''
```

Migration automática: `ALTER TABLE ADD COLUMN IF NOT EXISTS` (SQLite via CREATE TABLE IF NOT
EXISTS en nueva migración; PostgreSQL via ALTER TABLE IF NOT EXISTS).

El struct `storage.Record` añade los campos:

```go
TraceID string `json:"trace_id"`
SpanID  string `json:"span_id"`
```

Los endpoints existentes que devuelven `Record` (e.g. `/metrics/requests`, `/metrics/slowest-requests`)
incluirán automáticamente estos campos sin cambios adicionales.

### Configuración

La propagación de TraceContext está **activa por defecto**. Se puede desactivar con:

```yaml
trace_context: false
```

o flag:

```
--no-trace-context   Disable W3C TraceContext header propagation
```

Cuando desactivado: el proxy no lee ni escribe `traceparent`; `trace_id` y `span_id` se
almacenan como strings vacíos.

## API contract

No hay endpoint nuevo. Los cambios son:

1. `storage.Record` tiene dos nuevos campos JSON: `trace_id`, `span_id`.
2. Los endpoints `/metrics/requests` y `/metrics/slowest-requests` devuelven estos campos.
3. Config YAML: `trace_context: bool` (default `true`).
4. Flag CLI: `--no-trace-context`.

### Ejemplo de record con trace_id

```json
{
  "timestamp": "2026-03-15T10:00:00Z",
  "method": "GET",
  "path": "/users/123",
  "status_code": 200,
  "duration_ms": 42.3,
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "a2fb4a1d1a96d312"
}
```

## Nuevos tipos y funciones (package trace)

Nuevo package `trace`:

```go
// ParseTraceparent parses a W3C traceparent header value.
// Returns trace-id and parent-id (hex strings) and ok=true if valid.
func ParseTraceparent(header string) (traceID, parentID string, ok bool)

// NewTraceID generates a random 16-byte trace ID as 32 lowercase hex chars.
func NewTraceID() string

// NewSpanID generates a random 8-byte span ID as 16 lowercase hex chars.
func NewSpanID() string

// FormatTraceparent formats a W3C traceparent header value (flags always 01).
func FormatTraceparent(traceID, spanID string) string
```

El proxy (`proxy/handler.go`) llama a este package en el `Director` del `ReverseProxy`.

## Test cases

**package trace (TC-01..TC-06)**

TC-01 **ParseTraceparent válido**: `"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"` →
traceID=`"4bf92f3577b34da6a3ce929d0e0e4736"`, parentID=`"00f067aa0ba902b7"`, ok=true.

TC-02 **ParseTraceparent ausente/vacío**: header vacío → ok=false.

TC-03 **ParseTraceparent trace-id todo ceros**: inválido → ok=false.

TC-04 **ParseTraceparent formato incorrecto**: menos de 4 segmentos → ok=false.

TC-05 **NewTraceID genera 32 hex chars distintos de ceros**: llamar dos veces, verificar
longitud=32, caracteres hex válidos, y que los dos valores son distintos (probabilidad
negligible de colisión).

TC-06 **FormatTraceparent produce string correcto**: verificar formato
`"00-<traceID>-<spanID>-01"`.

**proxy integration (TC-07..TC-10)**

TC-07 **Request sin traceparent: proxy genera uno y lo propaga**: el upstream recibe un header
`traceparent` válido; `trace_id` en el Record almacenado es non-empty y tiene 32 chars.

TC-08 **Request con traceparent válido: proxy reutiliza trace-id**: upstream recibe el mismo
`trace_id` pero con un nuevo `span_id` generado por el proxy.

TC-09 **Request con traceparent inválido: proxy genera nuevo trace-id**: header malformado →
el proxy ignora el header entrante y genera uno nuevo.

TC-10 **trace_context=false: proxy no añade ni modifica traceparent**: upstream no recibe
`traceparent`; `trace_id` en Record es string vacío.

**storage migration (TC-11..TC-12)**

TC-11 **Schema migration añade columnas**: DB existente sin columnas trace_id/span_id; tras
migrate(), las columnas existen y admiten INSERT con valores vacíos.

TC-12 **Save y FindByWindow preservan trace_id y span_id**: insertar Record con trace_id y
span_id no vacíos; recuperar y verificar que los valores se conservan.

## Out of scope

- Sampling configurable (por ahora siempre 100 %)
- Parseo y propagación de `tracestate` con modificaciones (se propaga verbatim)
- Endpoint de búsqueda por trace_id (US futuro)
- Integración con backends externos (Jaeger, Tempo) — cubierto por US-46 OTel si se implementa
- Instrumentación de los spans de downstream (el proxy solo captura su propio hop)

## Dependencies

- US-01 proxy/handler.go (Director del ReverseProxy)
- US-02 storage.Record, store.Save
- US-23 openSQLite, openPostgres (migrate)
- US-03 config.Config (nuevo campo TraceContext bool)
