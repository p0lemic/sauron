# Sauron — Integración Docker Compose para Symfony (nginx + php-fpm)

Guía paso a paso para añadir Sauron como capa de observabilidad a un proyecto Symfony existente que usa nginx + php-fpm + Docker Compose.

---

## Prerrequisitos

- Docker Compose v2 (`docker compose version`)
- Symfony bundle de Sauron instalado (`composer require your-org/sauron-bundle`)
- Tu proyecto tiene un servicio `nginx` en `docker-compose.yml` que escucha en el puerto 80

---

## Arquitectura

```
cliente
   │
   ▼
sauron-nginx :80          ← toma el puerto 80 del host
   │  (fallback automático si Sauron no está disponible)
   ├──────────────────────────────────▶ nginx :80 (tu nginx, ahora interno)
   │                                        │
   ▼                                        ▼
sauron-proxy :8080                     php-fpm :9000
   │  inyecta traceparent
   ▼
nginx :80 → php-fpm :9000

volumen compartido (SQLite)
   ├── escrito por sauron-proxy (spans HTTP)
   └── escrito por Symfony bundle (spans controller + doctrine)
        ▼
sauron-dashboard :9090   (lectura + POST /ingest/spans desde el bundle)
```

El nginx de Sauron intercepta el tráfico y lo envía a `sauron-proxy`, que añade el header `traceparent` antes de reenviarlo a tu nginx. Si Sauron cae, el tráfico cae directamente a tu nginx sin interrupción.

---

## Paso 1 — Copiar archivos al proyecto

Desde la raíz de tu proyecto Symfony:

```bash
# Crear directorio de configuración de Sauron
mkdir -p sauron

# Copiar archivos (ajusta la ruta a donde clonaste el repo de Sauron)
cp /path/to/sauron/integrations/docker-compose/docker-compose.sauron.yml .
cp /path/to/sauron/integrations/docker-compose/nginx.conf sauron/nginx.conf
```

Estructura resultante en tu proyecto:

```
mi-proyecto/
├── docker-compose.yml          # tu compose original
├── docker-compose.sauron.yml   # override de Sauron (recién copiado)
├── sauron/
│   └── nginx.conf              # config nginx de Sauron
└── ...
```

---

## Paso 2 — Arrancar el stack

```bash
docker compose -f docker-compose.yml -f docker-compose.sauron.yml up -d
```

O si prefieres que se aplique automáticamente:

```bash
cp docker-compose.sauron.yml docker-compose.override.yml
docker compose up -d
```

> **¿Qué hace el override?**
> - Elimina el puerto de host de tu servicio `nginx` (ya no escucha en `:80` externamente)
> - Añade `sauron-nginx` que toma el puerto 80
> - Añade `sauron-proxy` (el proxy Go) en el puerto interno 8080
> - Añade `sauron-dashboard` en el puerto 9090
> - Crea el volumen compartido `sauron-data` para la base de datos SQLite

---

## Paso 3 — Configurar el bundle de Symfony

Crea o edita `config/packages/sauron.yaml`:

```yaml
sauron:
  enabled: true
  endpoint: 'http://sauron-dashboard:9090/ingest/spans'
  service_name: '%env(APP_NAME)%'
  instrument_doctrine: true
  timeout_ms: 2000
```

Añade a tu `.env`:

```dotenv
APP_NAME=mi-app-symfony
```

> **Importante:** usa el nombre del servicio Docker (`sauron-dashboard`) como host, no `localhost`. Los contenedores se comunican por nombre de servicio dentro de la red de Compose.

---

## Paso 4 — Verificar

```bash
# El dashboard debe responder
curl http://localhost:9090/health

# El proxy debe responder
curl http://localhost/_sauron/health

# Haz una petición a tu app
curl http://localhost/

# Comprueba que se registraron métricas
curl http://localhost:9090/metrics/endpoints
```

Abre el dashboard en el navegador: [http://localhost:9090](http://localhost:9090)

---

## Personalización

### Tu servicio nginx tiene otro nombre

Si tu servicio no se llama `nginx` sino, por ejemplo, `webserver`:

```bash
NGINX_SERVICE=webserver docker compose -f docker-compose.yml -f docker-compose.sauron.yml up -d
```

O añade a tu `.env`:

```dotenv
NGINX_SERVICE=webserver
```

### Cambiar puertos

```dotenv
PROXY_PORT=8000       # puerto del host para el tráfico de la app (defecto: 80)
DASHBOARD_PORT=9191   # puerto del dashboard de Sauron (defecto: 9090)
```

### Usar PostgreSQL en lugar de SQLite

Reemplaza el volumen SQLite por una conexión a tu servicio de base de datos. En `docker-compose.sauron.yml`, modifica los servicios `sauron-proxy` y `sauron-dashboard`:

```yaml
environment:
  PROFILER_STORAGE_DRIVER: "postgres"
  PROFILER_STORAGE_DSN: "postgres://user:pass@postgres:5432/sauron?sslmode=disable"
```

Y elimina las referencias a `sauron-data` volume.

### Deshabilitar Sauron en producción o por entorno

No incluyas el archivo override en el entorno donde no quieras Sauron:

```bash
# desarrollo (con Sauron)
docker compose -f docker-compose.yml -f docker-compose.sauron.yml up -d

# producción (sin Sauron)
docker compose up -d
```

---

## Resolución de problemas

| Síntoma | Causa probable | Solución |
|---------|---------------|----------|
| Puerto 80 ya en uso | Otro proceso o contenedor escucha en :80 | `sudo lsof -i :80`; para el proceso conflictivo |
| `sauron-proxy` unhealthy | El endpoint `/_sauron/health` no responde | `docker compose logs sauron-proxy`; verifica que `PROFILER_UPSTREAM` apunta al nombre de servicio correcto |
| El dashboard no recibe spans del bundle | El bundle apunta a `localhost` en vez del servicio | Cambia `endpoint` en `sauron.yaml` a `http://sauron-dashboard:9090/ingest/spans` |
| nginx devuelve 502 | `sauron-proxy` no ha arrancado aún | Espera el healthcheck (`start_period: 5s`); el override de nginx tiene fallback automático |
| No se ven métricas en el dashboard | El volumen SQLite no está compartido | Verifica que ambos servicios montan `sauron-data:/data` |
| `ports: []` no elimina el puerto del nginx | Versión antigua de Docker Compose | Actualiza a Compose v2 (`docker compose version >= 2.0`) |
