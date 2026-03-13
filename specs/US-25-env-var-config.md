# US-25 — Environment Variable Configuration

## Context

In ECS, configuration is injected as environment variables per container (directly in the task
definition or via SSM Parameter Store / Secrets Manager). Currently the profiler only reads
config from a YAML file and CLI flags, making ECS deployment awkward.

This US adds a `config.FromEnv()` function that reads all known env vars and plugs into the
existing merge chain between YAML and CLI flags.

## Behavior

### Priority chain (highest → lowest)

```
CLI flags  >  env vars  >  YAML file  >  defaults
```

An explicitly passed CLI flag always wins. If no flag is passed, the env var is used. If no env
var is set, the YAML value is used. If neither, the default applies.

### Env var names

All variables use the `PROFILER_` prefix. Both binaries recognise the full set; each binary
ignores the variables it does not use.

| Env var                    | Config field       | Type     | Used by           |
|----------------------------|--------------------|----------|-------------------|
| `PROFILER_UPSTREAM`        | `Upstream`         | string   | proxy             |
| `PROFILER_PORT`            | `Port`             | int      | proxy             |
| `PROFILER_TIMEOUT`         | `Timeout`          | duration | proxy             |
| `PROFILER_TLS_SKIP_VERIFY` | `TLSSkipVerify`    | bool     | proxy             |
| `PROFILER_STORAGE_DRIVER`  | `StorageDriver`    | string   | both              |
| `PROFILER_STORAGE_DSN`     | `StorageDSN`       | string   | both              |
| `PROFILER_LISTEN`          | `APIAddr`          | string   | dashboard         |
| `PROFILER_METRICS_WINDOW`  | `MetricsWindow`    | duration | dashboard         |
| `PROFILER_BASELINE_WINDOWS`| `BaselineWindows`  | int      | dashboard         |
| `PROFILER_ANOMALY_THRESHOLD`| `AnomalyThreshold`| float64  | dashboard         |
| `PROFILER_WEBHOOK_URL`     | `WebhookURL`       | string   | dashboard         |

### Type parsing rules

- **string**: set as-is if non-empty.
- **int**: `strconv.Atoi`; error if value is non-empty but not a valid integer.
- **float64**: `strconv.ParseFloat`; error if value is non-empty but not a valid number.
- **duration**: existing `parseDuration` (`30s`, `5m`, `2h`, `7d`); error if invalid.
- **bool** (`TLS_SKIP_VERIFY`): `"true"` or `"1"` → `true`; anything else (including empty)
  → not set (zero value, same convention as `Merge` for booleans).

### New function

```go
// FromEnv reads PROFILER_* environment variables and returns a Config containing
// only the values that were explicitly set. Unset variables leave their
// corresponding field at the zero value so Merge treats them as "not provided".
// Returns an error if any set variable cannot be parsed.
func FromEnv() (Config, error)
```

### Changes to `cmd/profiler/main.go`

Insert `FromEnv` between YAML load and CLI flag merge:

```go
base := config.Default()
if *configPath != "" {
    base, err = config.Load(*configPath)   // YAML
}
envCfg, err := config.FromEnv()           // env vars
if err != nil { fatal(err) }
base = config.Merge(base, envCfg)
// flag.Visit collects CLI overrides → config.Merge(base, cliOverrides)
```

Same change applied to `cmd/dashboard/main.go`.

### No API contract changes

No HTTP endpoints change.

## Test cases

### config/env_test.go (new file)

| TC  | Description                                                                          |
|-----|--------------------------------------------------------------------------------------|
| TC-01 | No env vars set → FromEnv returns zero Config, no error                            |
| TC-02 | `PROFILER_UPSTREAM=http://x` → cfg.Upstream == "http://x"                         |
| TC-03 | `PROFILER_PORT=9000` → cfg.Port == 9000                                            |
| TC-04 | `PROFILER_PORT=abc` → error containing "PROFILER_PORT"                             |
| TC-05 | `PROFILER_TIMEOUT=45s` → cfg.Timeout == 45s                                        |
| TC-06 | `PROFILER_TIMEOUT=bad` → error containing "PROFILER_TIMEOUT"                       |
| TC-07 | `PROFILER_TLS_SKIP_VERIFY=true` → cfg.TLSSkipVerify == true                       |
| TC-08 | `PROFILER_TLS_SKIP_VERIFY=1` → cfg.TLSSkipVerify == true                          |
| TC-09 | `PROFILER_TLS_SKIP_VERIFY=false` → cfg.TLSSkipVerify == false (zero, not set)     |
| TC-10 | `PROFILER_STORAGE_DRIVER=postgres` → cfg.StorageDriver == "postgres"               |
| TC-11 | `PROFILER_STORAGE_DSN=postgres://h/db` → cfg.StorageDSN == "postgres://h/db"      |
| TC-12 | `PROFILER_LISTEN=:8080` → cfg.APIAddr == ":8080"                                  |
| TC-13 | `PROFILER_METRICS_WINDOW=1h` → cfg.MetricsWindow == time.Hour                     |
| TC-14 | `PROFILER_METRICS_WINDOW=bad` → error containing "PROFILER_METRICS_WINDOW"        |
| TC-15 | `PROFILER_BASELINE_WINDOWS=10` → cfg.BaselineWindows == 10                        |
| TC-16 | `PROFILER_ANOMALY_THRESHOLD=2.5` → cfg.AnomalyThreshold == 2.5                   |
| TC-17 | `PROFILER_ANOMALY_THRESHOLD=xyz` → error containing "PROFILER_ANOMALY_THRESHOLD"  |
| TC-18 | `PROFILER_WEBHOOK_URL=http://h/w` → cfg.WebhookURL == "http://h/w"               |
| TC-19 | Multiple vars set → all fields populated correctly                                  |

### Merge chain integration (config/config_test.go additions)

| TC  | Description                                                                          |
|-----|--------------------------------------------------------------------------------------|
| TC-20 | Env var overrides YAML default (env > YAML)                                        |
| TC-21 | CLI flag overrides env var (CLI > env)                                             |

## Files changed

| File                       | Change                                                |
|----------------------------|-------------------------------------------------------|
| `config/env.go`            | New — `FromEnv()` implementation                      |
| `config/env_test.go`       | New — TC-01..TC-19                                    |
| `config/config_test.go`    | TC-20..TC-21 appended                                 |
| `cmd/profiler/main.go`     | Insert `FromEnv` in merge chain                       |
| `cmd/dashboard/main.go`    | Insert `FromEnv` in merge chain                       |

No changes to: `api/`, `metrics/`, `alerts/`, `storage/`, `proxy/`.

## Out of scope

- `.env` file loading
- Secret redaction in logs
- ECS SSM / Secrets Manager integration (handled at infra level, transparent to the binary)
