package config_test

import (
	"testing"
	"time"

	"api-profiler/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setEnv sets env vars for the duration of the test and restores them on cleanup.
func setEnv(t *testing.T, pairs ...string) {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		key, val := pairs[i], pairs[i+1]
		t.Setenv(key, val)
	}
}

// TC-01: No env vars set → FromEnv returns zero Config, no error.
func TestFromEnvEmpty(t *testing.T) {
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, config.Config{}, cfg)
}

// TC-02: PROFILER_UPSTREAM set → cfg.Upstream populated.
func TestFromEnvUpstream(t *testing.T) {
	setEnv(t, "PROFILER_UPSTREAM", "http://localhost:3000")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:3000", cfg.Upstream)
}

// TC-03: PROFILER_PORT=9000 → cfg.Port == 9000.
func TestFromEnvPort(t *testing.T) {
	setEnv(t, "PROFILER_PORT", "9000")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 9000, cfg.Port)
}

// TC-04: PROFILER_PORT=abc → error containing "PROFILER_PORT".
func TestFromEnvPortInvalid(t *testing.T) {
	setEnv(t, "PROFILER_PORT", "abc")
	_, err := config.FromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROFILER_PORT")
}

// TC-05: PROFILER_TIMEOUT=45s → cfg.Timeout == 45s.
func TestFromEnvTimeout(t *testing.T) {
	setEnv(t, "PROFILER_TIMEOUT", "45s")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 45*time.Second, cfg.Timeout)
}

// TC-06: PROFILER_TIMEOUT=bad → error containing "PROFILER_TIMEOUT".
func TestFromEnvTimeoutInvalid(t *testing.T) {
	setEnv(t, "PROFILER_TIMEOUT", "bad")
	_, err := config.FromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROFILER_TIMEOUT")
}

// TC-07: PROFILER_TLS_SKIP_VERIFY=true → cfg.TLSSkipVerify == true.
func TestFromEnvTLSSkipVerifyTrue(t *testing.T) {
	setEnv(t, "PROFILER_TLS_SKIP_VERIFY", "true")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.TLSSkipVerify)
}

// TC-08: PROFILER_TLS_SKIP_VERIFY=1 → cfg.TLSSkipVerify == true.
func TestFromEnvTLSSkipVerifyOne(t *testing.T) {
	setEnv(t, "PROFILER_TLS_SKIP_VERIFY", "1")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.TLSSkipVerify)
}

// TC-09: PROFILER_TLS_SKIP_VERIFY=false → TLSSkipVerify stays false (zero value).
func TestFromEnvTLSSkipVerifyFalse(t *testing.T) {
	setEnv(t, "PROFILER_TLS_SKIP_VERIFY", "false")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.False(t, cfg.TLSSkipVerify)
}

// TC-10: PROFILER_STORAGE_DRIVER=postgres → cfg.StorageDriver == "postgres".
func TestFromEnvStorageDriver(t *testing.T) {
	setEnv(t, "PROFILER_STORAGE_DRIVER", "postgres")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "postgres", cfg.StorageDriver)
}

// TC-11: PROFILER_STORAGE_DSN set → cfg.StorageDSN populated.
func TestFromEnvStorageDSN(t *testing.T) {
	setEnv(t, "PROFILER_STORAGE_DSN", "postgres://user:pass@host/db")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "postgres://user:pass@host/db", cfg.StorageDSN)
}

// TC-12: PROFILER_LISTEN=:8080 → cfg.APIAddr == ":8080".
func TestFromEnvListen(t *testing.T) {
	setEnv(t, "PROFILER_LISTEN", ":8080")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.APIAddr)
}

// TC-13: PROFILER_METRICS_WINDOW=1h → cfg.MetricsWindow == time.Hour.
func TestFromEnvMetricsWindow(t *testing.T) {
	setEnv(t, "PROFILER_METRICS_WINDOW", "1h")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, time.Hour, cfg.MetricsWindow)
}

// TC-14: PROFILER_METRICS_WINDOW=bad → error containing "PROFILER_METRICS_WINDOW".
func TestFromEnvMetricsWindowInvalid(t *testing.T) {
	setEnv(t, "PROFILER_METRICS_WINDOW", "bad")
	_, err := config.FromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROFILER_METRICS_WINDOW")
}

// TC-15: PROFILER_BASELINE_WINDOWS=10 → cfg.BaselineWindows == 10.
func TestFromEnvBaselineWindows(t *testing.T) {
	setEnv(t, "PROFILER_BASELINE_WINDOWS", "10")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 10, cfg.BaselineWindows)
}

// TC-16: PROFILER_ANOMALY_THRESHOLD=2.5 → cfg.AnomalyThreshold == 2.5.
func TestFromEnvAnomalyThreshold(t *testing.T) {
	setEnv(t, "PROFILER_ANOMALY_THRESHOLD", "2.5")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 2.5, cfg.AnomalyThreshold)
}

// TC-17: PROFILER_ANOMALY_THRESHOLD=xyz → error containing "PROFILER_ANOMALY_THRESHOLD".
func TestFromEnvAnomalyThresholdInvalid(t *testing.T) {
	setEnv(t, "PROFILER_ANOMALY_THRESHOLD", "xyz")
	_, err := config.FromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROFILER_ANOMALY_THRESHOLD")
}

// TC-18: PROFILER_WEBHOOK_URL set → cfg.WebhookURL populated.
func TestFromEnvWebhookURL(t *testing.T) {
	setEnv(t, "PROFILER_WEBHOOK_URL", "http://hooks.example.com/alert")
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "http://hooks.example.com/alert", cfg.WebhookURL)
}

// TC-19: Multiple vars set → all fields populated correctly.
func TestFromEnvMultiple(t *testing.T) {
	setEnv(t,
		"PROFILER_UPSTREAM", "https://api.example.com",
		"PROFILER_PORT", "8081",
		"PROFILER_STORAGE_DRIVER", "postgres",
		"PROFILER_STORAGE_DSN", "postgres://h/db",
		"PROFILER_LISTEN", ":9091",
		"PROFILER_METRICS_WINDOW", "15m",
	)
	cfg, err := config.FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com", cfg.Upstream)
	assert.Equal(t, 8081, cfg.Port)
	assert.Equal(t, "postgres", cfg.StorageDriver)
	assert.Equal(t, "postgres://h/db", cfg.StorageDSN)
	assert.Equal(t, ":9091", cfg.APIAddr)
	assert.Equal(t, 15*time.Minute, cfg.MetricsWindow)
}
