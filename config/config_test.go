package config_test

import (
	"os"
	"testing"
	"time"

	"api-profiler/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeYAML writes content to a temp file and returns its path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "profiler-*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// TC-01: Full YAML — all fields loaded correctly.
func TestLoadFullYAML(t *testing.T) {
	path := writeYAML(t, `
upstream: http://localhost:3000
port: 9090
timeout: 5s
db_path: /tmp/test.db
retention: 7d
`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:3000", cfg.Upstream)
	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, 5*time.Second, cfg.Timeout)
	assert.Equal(t, "/tmp/test.db", cfg.DBPath)
	assert.Equal(t, 7*24*time.Hour, cfg.Retention)
}

// TC-02: Flag overrides YAML value.
func TestMergeFlagOverridesYAML(t *testing.T) {
	base := config.Config{Upstream: "http://up", Port: 9000, Timeout: 10 * time.Second, DBPath: "x.db"}
	overrides := config.Config{Port: 8080} // only port set explicitly
	result := config.Merge(base, overrides)
	assert.Equal(t, "http://up", result.Upstream)
	assert.Equal(t, 8080, result.Port)
	assert.Equal(t, 10*time.Second, result.Timeout)
	assert.Equal(t, "x.db", result.DBPath)
}

// TC-03: Partial flag + YAML base — each source contributes its own fields.
func TestMergePartialFlagPlusYAML(t *testing.T) {
	base := config.Config{Upstream: "http://up", Port: 9000, Timeout: 5 * time.Second, DBPath: "a.db"}
	overrides := config.Config{Port: 7070}
	result := config.Merge(base, overrides)
	assert.Equal(t, "http://up", result.Upstream)
	assert.Equal(t, 7070, result.Port)
	assert.Equal(t, 5*time.Second, result.Timeout)
}

// TC-04: No YAML (Default) — fields carry default values.
func TestDefaultValues(t *testing.T) {
	cfg := config.Default()
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, "profiler.db", cfg.DBPath)
	assert.Equal(t, "", cfg.Upstream)
}

// TC-05: Partial YAML — unset fields keep defaults.
func TestLoadPartialYAML(t *testing.T) {
	path := writeYAML(t, `upstream: http://localhost:4000`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:4000", cfg.Upstream)
	assert.Equal(t, 8080, cfg.Port)       // default
	assert.Equal(t, 30*time.Second, cfg.Timeout) // default
	assert.Equal(t, "profiler.db", cfg.DBPath)   // default
}

// TC-06: Timeout in YAML parsed correctly.
func TestLoadTimeoutParsed(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\ntimeout: 45s")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cfg.Timeout)
}

// TC-07: Retention "7d" parsed to 7 * 24h.
func TestLoadRetentionDays(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nretention: 7d")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 7*24*time.Hour, cfg.Retention)
}

// TC-08: Unknown fields in YAML are silently ignored.
func TestLoadUnknownFieldsIgnored(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nfoo: bar\nbaz: 42")
	_, err := config.Load(path)
	assert.NoError(t, err)
}

// TC-09: File does not exist → descriptive error.
func TestLoadFileNotFound(t *testing.T) {
	_, err := config.Load("/tmp/does-not-exist-xyz.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist-xyz.yaml")
}

// TC-10: Malformed YAML → descriptive error.
func TestLoadMalformedYAML(t *testing.T) {
	path := writeYAML(t, "upstream: [unclosed")
	_, err := config.Load(path)
	require.Error(t, err)
}

// TC-11: upstream empty after merge → Validate returns error.
func TestValidateUpstreamRequired(t *testing.T) {
	cfg := config.Default() // Upstream is ""
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream")
}

// TC-12: Port out of range → Validate returns error.
func TestValidatePortOutOfRange(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nport: 99999")
	cfg, err := config.Load(path)
	require.NoError(t, err) // Load does not validate
	err = config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port")
}

// TC-13: Invalid timeout string → Load returns error.
func TestLoadInvalidTimeout(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\ntimeout: not-a-duration")
	_, err := config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

// TC-14: Invalid retention string → Load returns error.
func TestLoadInvalidRetention(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nretention: bad")
	_, err := config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retention")
}

// TC-15: Empty YAML file → defaults applied; upstream still required by Validate.
func TestLoadEmptyFile(t *testing.T) {
	path := writeYAML(t, "")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "", cfg.Upstream)
	assert.Error(t, config.Validate(cfg))
}

// TC-16: upstream without scheme → Validate returns error.
func TestValidateUpstreamNoScheme(t *testing.T) {
	path := writeYAML(t, "upstream: localhost:3000")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	err = config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http")
}

// Validate passes for a fully valid config.
func TestValidateOK(t *testing.T) {
	cfg := config.Config{
		Upstream:         "http://localhost:3000",
		Port:             8080,
		Timeout:          30 * time.Second,
		DBPath:           "profiler.db",
		MetricsWindow:    60 * time.Second,
		BaselineWindows:  5,
		AnomalyThreshold: 3.0,
	}
	assert.NoError(t, config.Validate(cfg))
}

// Merge with empty overrides returns base unchanged.
func TestMergeEmptyOverrides(t *testing.T) {
	base := config.Config{Upstream: "http://x", Port: 9000, Timeout: 5 * time.Second, DBPath: "a.db"}
	result := config.Merge(base, config.Config{})
	assert.Equal(t, base, result)
}

// --- US-05: TLS ---

// TC-01: tls_skip_verify: true in YAML is loaded correctly.
func TestLoadTLSSkipVerifyTrue(t *testing.T) {
	path := writeYAML(t, "upstream: https://x\ntls_skip_verify: true")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.True(t, cfg.TLSSkipVerify)
}

// TC-09: tls_skip_verify: false in YAML — explicit false, same as default.
func TestLoadTLSSkipVerifyFalse(t *testing.T) {
	path := writeYAML(t, "upstream: https://x\ntls_skip_verify: false")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.False(t, cfg.TLSSkipVerify)
}

// Default: TLSSkipVerify is false.
func TestDefaultTLSSkipVerifyIsFalse(t *testing.T) {
	assert.False(t, config.Default().TLSSkipVerify)
}

// TC-10: Flag true overrides YAML false.
func TestMergeTLSFlagOverridesYAML(t *testing.T) {
	base := config.Config{Upstream: "https://x", Port: 8080, Timeout: 30 * time.Second, DBPath: "x.db", TLSSkipVerify: false}
	overrides := config.Config{TLSSkipVerify: true}
	result := config.Merge(base, overrides)
	assert.True(t, result.TLSSkipVerify)
}

// --- US-08: MetricsWindow ---

// TC-16: MetricsWindow = 0 → Validate returns error.
func TestValidateMetricsWindowZero(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://localhost:3000",
		Port:     8080,
		Timeout:  30 * time.Second,
		DBPath:   "profiler.db",
		// MetricsWindow is 0 (zero value)
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics_window")
}

// TC-17: MetricsWindow negative → Validate returns error.
func TestValidateMetricsWindowNegative(t *testing.T) {
	cfg := config.Config{
		Upstream:      "http://localhost:3000",
		Port:          8080,
		Timeout:       30 * time.Second,
		DBPath:        "profiler.db",
		MetricsWindow: -1 * time.Second,
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics_window")
}

// TC-18: MetricsWindow positive → Validate passes.
func TestValidateMetricsWindowPositive(t *testing.T) {
	cfg := config.Config{
		Upstream:         "http://localhost:3000",
		Port:             8080,
		Timeout:          30 * time.Second,
		DBPath:           "profiler.db",
		MetricsWindow:    30 * time.Second,
		BaselineWindows:  5,
		AnomalyThreshold: 3.0,
	}
	assert.NoError(t, config.Validate(cfg))
}

// TC-12: metrics_window in YAML parsed correctly.
func TestLoadMetricsWindow(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nmetrics_window: 5m")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 5*time.Minute, cfg.MetricsWindow)
}

// TC-15: --metrics-window flag overrides YAML.
func TestMergeMetricsWindowFlagOverridesYAML(t *testing.T) {
	base := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: 5 * time.Minute,
	}
	overrides := config.Config{MetricsWindow: time.Hour}
	result := config.Merge(base, overrides)
	assert.Equal(t, time.Hour, result.MetricsWindow)
}

// --- US-13: BaselineWindows ---

// TC-01: Default BaselineWindows is 5.
func TestDefaultBaselineWindows(t *testing.T) {
	assert.Equal(t, 5, config.Default().BaselineWindows)
}

// TC-02: YAML baseline_windows loaded correctly.
func TestLoadBaselineWindows(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nbaseline_windows: 10")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 10, cfg.BaselineWindows)
}

// TC-03: Flag overrides YAML.
func TestMergeBaselineWindowsFlagOverridesYAML(t *testing.T) {
	base := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 10,
	}
	overrides := config.Config{BaselineWindows: 3}
	result := config.Merge(base, overrides)
	assert.Equal(t, 3, result.BaselineWindows)
}

// TC-04: BaselineWindows = 0 → Validate error.
func TestValidateBaselineWindowsZero(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 0,
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseline_windows")
}

// TC-05: BaselineWindows = -1 → Validate error.
func TestValidateBaselineWindowsNegative(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: -1,
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseline_windows")
}

/// TC-06: BaselineWindows = 1 → Validate OK.
func TestValidateBaselineWindowsOne(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 1,
		AnomalyThreshold: 3.0,
	}
	assert.NoError(t, config.Validate(cfg))
}

// --- US-14: AnomalyThreshold ---

// TC-01: Default AnomalyThreshold is 3.0.
func TestDefaultAnomalyThreshold(t *testing.T) {
	assert.Equal(t, 3.0, config.Default().AnomalyThreshold)
}

// TC-02: YAML anomaly_threshold loaded correctly.
func TestLoadAnomalyThreshold(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nanomaly_threshold: 2.5")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 2.5, cfg.AnomalyThreshold)
}

// TC-03: Flag overrides YAML.
func TestMergeAnomalyThresholdFlagOverridesYAML(t *testing.T) {
	base := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 5,
		AnomalyThreshold: 2.5,
	}
	overrides := config.Config{AnomalyThreshold: 5.0}
	result := config.Merge(base, overrides)
	assert.Equal(t, 5.0, result.AnomalyThreshold)
}

// TC-04: AnomalyThreshold = 0 → Validate error.
func TestValidateAnomalyThresholdZero(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 5,
		AnomalyThreshold: 0,
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anomaly_threshold")
}

// TC-05: AnomalyThreshold = -1 → Validate error.
func TestValidateAnomalyThresholdNegative(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 5,
		AnomalyThreshold: -1.0,
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anomaly_threshold")
}

// --- US-15: WebhookURL ---

// TC-10: YAML webhook_url loaded correctly.
func TestLoadWebhookURL(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nwebhook_url: http://hooks.example.com/alert")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://hooks.example.com/alert", cfg.WebhookURL)
}

// TC-11: No webhook_url in YAML → empty string.
func TestLoadWebhookURLAbsent(t *testing.T) {
	path := writeYAML(t, "upstream: http://x")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.WebhookURL)
}

// TC-12: Validate with empty WebhookURL → no error (field is optional).
func TestValidateWebhookURLEmpty(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 5,
		AnomalyThreshold: 3.0,
		// WebhookURL intentionally empty
	}
	assert.NoError(t, config.Validate(cfg))
}

// TC-13: Validate with non-http WebhookURL → error containing "webhook_url".
func TestValidateWebhookURLInvalid(t *testing.T) {
	cfg := config.Config{
		Upstream: "http://x", Port: 8080, Timeout: 30 * time.Second,
		DBPath: "x.db", MetricsWindow: time.Minute, BaselineWindows: 5,
		AnomalyThreshold: 3.0, WebhookURL: "ftp://bad.example.com",
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_url")
}

// Merge with TLSSkipVerify false in overrides does not override base true.
func TestMergeTLSFalseOverridesDoesNotClear(t *testing.T) {
	base := config.Config{Upstream: "https://x", Port: 8080, Timeout: 30 * time.Second, DBPath: "x.db", TLSSkipVerify: true}
	overrides := config.Config{TLSSkipVerify: false} // false = "not set" sentinel
	result := config.Merge(base, overrides)
	assert.True(t, result.TLSSkipVerify) // base value preserved
}

// --- US-23: Configurable Storage Driver ---

// TC-01: Default StorageDriver is "sqlite" and StorageDSN is "profiler.db".
func TestDefaultStorageDriver(t *testing.T) {
	cfg := config.Default()
	assert.Equal(t, "sqlite", cfg.StorageDriver)
	assert.Equal(t, "profiler.db", cfg.StorageDSN)
}

// TC-02: YAML storage.driver=postgres + storage.dsn parsed correctly.
func TestLoadStoragePostgres(t *testing.T) {
	path := writeYAML(t, `
upstream: http://x
storage:
  driver: postgres
  dsn: "postgres://user:pass@db.example.com/profiler"
`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "postgres", cfg.StorageDriver)
	assert.Equal(t, "postgres://user:pass@db.example.com/profiler", cfg.StorageDSN)
}

// TC-03: Legacy db_path sets StorageDriver=sqlite and StorageDSN accordingly.
func TestLoadLegacyDBPath(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\ndb_path: /tmp/legacy.db")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "sqlite", cfg.StorageDriver)
	assert.Equal(t, "/tmp/legacy.db", cfg.StorageDSN)
	assert.Equal(t, "/tmp/legacy.db", cfg.DBPath) // backward compat
}

// TC-04: When both db_path and storage block are set, storage block wins.
func TestLoadStorageBlockWinsOverDBPath(t *testing.T) {
	path := writeYAML(t, `
upstream: http://x
db_path: /tmp/old.db
storage:
  driver: postgres
  dsn: "postgres://host/db"
`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "postgres", cfg.StorageDriver)
	assert.Equal(t, "postgres://host/db", cfg.StorageDSN)
}

// TC-05: Validate returns error for unsupported driver.
func TestValidateUnsupportedDriver(t *testing.T) {
	cfg := config.Default()
	cfg.Upstream = "http://x"
	cfg.StorageDriver = "mysql"
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported storage driver")
}

// TC-06: Validate returns error for driver=postgres with empty dsn.
func TestValidatePostgresEmptyDSN(t *testing.T) {
	cfg := config.Default()
	cfg.Upstream = "http://x"
	cfg.StorageDriver = "postgres"
	cfg.StorageDSN = ""
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage dsn required")
}

// TC-07: Validate with driver=sqlite and empty dsn coerces to default, no error.
func TestValidateSQLiteEmptyDSNOK(t *testing.T) {
	cfg := config.Default()
	cfg.Upstream = "http://x"
	cfg.StorageDriver = "sqlite"
	cfg.StorageDSN = "" // empty but sqlite is fine without explicit dsn
	err := config.Validate(cfg)
	assert.NoError(t, err)
}

// TC-08: Merge overrides StorageDriver and StorageDSN.
func TestMergeStorageDriver(t *testing.T) {
	base := config.Default()
	base.Upstream = "http://x"
	overrides := config.Config{StorageDriver: "postgres", StorageDSN: "postgres://h/db"}
	result := config.Merge(base, overrides)
	assert.Equal(t, "postgres", result.StorageDriver)
	assert.Equal(t, "postgres://h/db", result.StorageDSN)
}

// --- US-24: ValidateDashboard ---

func validDashboardConfig() config.Config {
	cfg := config.Default()
	// Default() already sets StorageDriver, MetricsWindow, BaselineWindows, AnomalyThreshold.
	return cfg
}

// TC-01: ValidateDashboard with valid config → no error.
func TestValidateDashboardOK(t *testing.T) {
	assert.NoError(t, config.ValidateDashboard(validDashboardConfig()))
}

// TC-02: ValidateDashboard: postgres driver with empty dsn → error.
func TestValidateDashboardPostgresEmptyDSN(t *testing.T) {
	cfg := validDashboardConfig()
	cfg.StorageDriver = "postgres"
	cfg.StorageDSN = ""
	err := config.ValidateDashboard(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage dsn required")
}

// TC-03: ValidateDashboard: MetricsWindow=0 → error.
func TestValidateDashboardMetricsWindowZero(t *testing.T) {
	cfg := validDashboardConfig()
	cfg.MetricsWindow = 0
	err := config.ValidateDashboard(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "metrics_window")
}

// TC-04: ValidateDashboard: BaselineWindows=0 → error.
func TestValidateDashboardBaselineWindowsZero(t *testing.T) {
	cfg := validDashboardConfig()
	cfg.BaselineWindows = 0
	err := config.ValidateDashboard(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseline_windows")
}

// TC-05: ValidateDashboard: AnomalyThreshold=0 → error.
func TestValidateDashboardAnomalyThresholdZero(t *testing.T) {
	cfg := validDashboardConfig()
	cfg.AnomalyThreshold = 0
	err := config.ValidateDashboard(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anomaly_threshold")
}

// TC-06: ValidateDashboard: invalid WebhookURL → error.
func TestValidateDashboardInvalidWebhook(t *testing.T) {
	cfg := validDashboardConfig()
	cfg.WebhookURL = "ftp://bad.example.com"
	err := config.ValidateDashboard(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_url")
}

// TC-07: ValidateDashboard does NOT require Upstream field.
func TestValidateDashboardNoUpstreamRequired(t *testing.T) {
	cfg := validDashboardConfig()
	cfg.Upstream = "" // explicitly empty
	assert.NoError(t, config.ValidateDashboard(cfg))
}

// --- US-25: FromEnv merge chain integration ---

// TC-20: Env var overrides YAML default (env > YAML).
func TestEnvOverridesYAML(t *testing.T) {
	path := writeYAML(t, "upstream: http://yaml-host\nport: 9000")
	base, err := config.Load(path)
	require.NoError(t, err)

	t.Setenv("PROFILER_PORT", "7777")
	envCfg, err := config.FromEnv()
	require.NoError(t, err)

	result := config.Merge(base, envCfg)
	assert.Equal(t, 7777, result.Port)
	assert.Equal(t, "http://yaml-host", result.Upstream) // unset env var keeps YAML value
}

// TC-21: CLI flag overrides env var (CLI > env).
func TestCLIOverridesEnv(t *testing.T) {
	t.Setenv("PROFILER_UPSTREAM", "http://from-env")

	base := config.Default()
	envCfg, err := config.FromEnv()
	require.NoError(t, err)
	base = config.Merge(base, envCfg)

	// Simulate CLI flag override.
	cliOverrides := config.Config{Upstream: "http://from-cli"}
	result := config.Merge(base, cliOverrides)

	assert.Equal(t, "http://from-cli", result.Upstream)
}

// --- US-26: Path normalization config ---

// TC-15: Default() sets NormalizePaths=true and PathRules=nil.
func TestDefaultNormalizePaths(t *testing.T) {
	cfg := config.Default()
	assert.True(t, cfg.NormalizePaths)
	assert.Nil(t, cfg.PathRules)
}

// TC-16: YAML normalize_paths: false → NormalizePaths=false.
func TestLoadNormalizePathsFalse(t *testing.T) {
	path := writeYAML(t, "upstream: http://x\nnormalize_paths: false")
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.False(t, cfg.NormalizePaths)
}

// TC-17: YAML path_rules parsed into []PathRule correctly.
func TestLoadPathRules(t *testing.T) {
	path := writeYAML(t, `
upstream: http://x
path_rules:
  - pattern: "^v[0-9]+$"
    replacement: ":version"
  - pattern: "^[a-z]{2}-[A-Z]{2}$"
    replacement: ":locale"
`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.PathRules, 2)
	assert.Equal(t, "^v[0-9]+$", cfg.PathRules[0].Pattern)
	assert.Equal(t, ":version", cfg.PathRules[0].Replacement)
	assert.Equal(t, "^[a-z]{2}-[A-Z]{2}$", cfg.PathRules[1].Pattern)
	assert.Equal(t, ":locale", cfg.PathRules[1].Replacement)
}

// TC-01 (US-37): YAML con header_rules → carga correctamente.
func TestLoadHeaderRules(t *testing.T) {
	path := writeYAML(t, `
upstream: http://localhost:3000
header_rules:
  - action: set
    header: X-Forwarded-By
    value: api-profiler
  - action: remove
    header: X-Internal-Secret
`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.HeaderRules, 2)
	assert.Equal(t, "set", cfg.HeaderRules[0].Action)
	assert.Equal(t, "X-Forwarded-By", cfg.HeaderRules[0].Header)
	assert.Equal(t, "api-profiler", cfg.HeaderRules[0].Value)
	assert.Equal(t, "remove", cfg.HeaderRules[1].Action)
	assert.Equal(t, "X-Internal-Secret", cfg.HeaderRules[1].Header)
}

// TC-02 (US-37): action inválido → Validate retorna error.
func TestValidateHeaderRuleInvalidAction(t *testing.T) {
	cfg := config.Default()
	cfg.Upstream = "http://localhost:3000"
	cfg.HeaderRules = []config.HeaderRule{
		{Action: "append", Header: "X-Foo", Value: "bar"},
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action")
}

// TC-03 (US-37): header vacío → Validate retorna error.
func TestValidateHeaderRuleEmptyHeader(t *testing.T) {
	cfg := config.Default()
	cfg.Upstream = "http://localhost:3000"
	cfg.HeaderRules = []config.HeaderRule{
		{Action: "set", Header: "", Value: "bar"},
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "header name")
}

// TC-04 (US-37): action=set con value vacío → Validate retorna error.
func TestValidateHeaderRuleSetEmptyValue(t *testing.T) {
	cfg := config.Default()
	cfg.Upstream = "http://localhost:3000"
	cfg.HeaderRules = []config.HeaderRule{
		{Action: "set", Header: "X-Foo", Value: ""},
	}
	err := config.Validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "value")
}

// TC-11 (US-44): metrics_apdex_t en YAML se parsea correctamente.
func TestLoadApdexT(t *testing.T) {
	path := writeYAML(t, `
upstream: http://localhost:3000
metrics_apdex_t: 250
`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 250, cfg.ApdexT)
}

// TC-11b (US-44): ApdexT default es 500 cuando no se especifica en YAML.
func TestLoadApdexTDefault(t *testing.T) {
	path := writeYAML(t, `upstream: http://localhost:3000`)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, 500, cfg.ApdexT)
}

// TC-12 (US-44): Merge propaga ApdexT desde overrides.
func TestMergeApdexT(t *testing.T) {
	base := config.Default()
	overrides := config.Config{ApdexT: 250}
	result := config.Merge(base, overrides)
	assert.Equal(t, 250, result.ApdexT)
}
