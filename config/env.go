package config

import (
	"fmt"
	"os"
	"strconv"
)

// FromEnv reads PROFILER_* environment variables and returns a Config containing
// only the values that were explicitly set. Unset variables leave their
// corresponding field at the zero value so Merge treats them as "not provided".
// Returns an error if any set variable cannot be parsed.
func FromEnv() (Config, error) {
	var cfg Config
	var err error

	if v := os.Getenv("PROFILER_UPSTREAM"); v != "" {
		cfg.Upstream = v
	}
	if v := os.Getenv("PROFILER_PORT"); v != "" {
		cfg.Port, err = strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("PROFILER_PORT: invalid integer %q", v)
		}
	}
	if v := os.Getenv("PROFILER_TIMEOUT"); v != "" {
		cfg.Timeout, err = parseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PROFILER_TIMEOUT: invalid duration %q", v)
		}
	}
	if v := os.Getenv("PROFILER_TLS_SKIP_VERIFY"); v == "true" || v == "1" {
		cfg.TLSSkipVerify = true
	}
	if v := os.Getenv("PROFILER_STORAGE_DRIVER"); v != "" {
		cfg.StorageDriver = v
	}
	if v := os.Getenv("PROFILER_STORAGE_DSN"); v != "" {
		cfg.StorageDSN = v
	}
	if v := os.Getenv("PROFILER_LISTEN"); v != "" {
		cfg.APIAddr = v
	}
	if v := os.Getenv("PROFILER_METRICS_WINDOW"); v != "" {
		cfg.MetricsWindow, err = parseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("PROFILER_METRICS_WINDOW: invalid duration %q", v)
		}
	}
	if v := os.Getenv("PROFILER_BASELINE_WINDOWS"); v != "" {
		cfg.BaselineWindows, err = strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("PROFILER_BASELINE_WINDOWS: invalid integer %q", v)
		}
	}
	if v := os.Getenv("PROFILER_ANOMALY_THRESHOLD"); v != "" {
		cfg.AnomalyThreshold, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return Config{}, fmt.Errorf("PROFILER_ANOMALY_THRESHOLD: invalid number %q", v)
		}
	}
	if v := os.Getenv("PROFILER_WEBHOOK_URL"); v != "" {
		cfg.WebhookURL = v
	}

	return cfg, nil
}
