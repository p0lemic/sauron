package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the resolved configuration after merging YAML and CLI flags.
type Config struct {
	Upstream      string
	Port          int
	Timeout       time.Duration
	DBPath        string        // deprecated: use StorageDSN; kept for backward compat
	Retention     time.Duration // accepted but not enforced until Phase 2
	TLSSkipVerify    bool
	APIAddr          string
	MetricsWindow    time.Duration
	BaselineWindows  int
	AnomalyThreshold float64
	WebhookURL       string
	StorageDriver    string // "sqlite" (default) | "postgres"
	StorageDSN       string // file path for sqlite, connection string for postgres
}

// Default returns a Config with all default values applied.
func Default() Config {
	return Config{
		Port:             8080,
		Timeout:          30 * time.Second,
		DBPath:           "profiler.db",
		APIAddr:          "localhost:9090",
		MetricsWindow:    30 * time.Minute,
		BaselineWindows:  5,
		AnomalyThreshold: 3.0,
		StorageDriver:    "sqlite",
		StorageDSN:       "profiler.db",
	}
}

// yamlStorage is the nested storage block in the YAML config.
type yamlStorage struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// yamlFile is the raw on-disk structure; durations are strings for flexible parsing.
type yamlFile struct {
	Upstream      string `yaml:"upstream"`
	Port          int    `yaml:"port"`
	Timeout       string `yaml:"timeout"`
	DBPath        string `yaml:"db_path"`
	Retention     string `yaml:"retention"`
	TLSSkipVerify bool   `yaml:"tls_skip_verify"`
	APIAddr          string      `yaml:"api_addr"`
	MetricsWindow    string      `yaml:"metrics_window"`
	BaselineWindows  int         `yaml:"baseline_windows"`
	AnomalyThreshold float64     `yaml:"anomaly_threshold"`
	WebhookURL       string      `yaml:"webhook_url"`
	Storage          yamlStorage `yaml:"storage"`
}

// Load reads the YAML file at path and returns a Config with defaults applied
// to any unset fields. Returns error if the file cannot be read or parsed.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: reading %q: %w", path, err)
	}

	var yf yamlFile
	if err := yaml.Unmarshal(data, &yf); err != nil {
		return Config{}, fmt.Errorf("config: parsing %q: %w", path, err)
	}

	cfg := Default()

	if yf.Upstream != "" {
		cfg.Upstream = yf.Upstream
	}
	if yf.Port != 0 {
		cfg.Port = yf.Port
	}
	// Storage config: `storage` block takes precedence over legacy `db_path`.
	if yf.Storage.Driver != "" || yf.Storage.DSN != "" {
		if yf.Storage.Driver != "" {
			cfg.StorageDriver = yf.Storage.Driver
		}
		if yf.Storage.DSN != "" {
			cfg.StorageDSN = yf.Storage.DSN
		}
		// Keep DBPath in sync when using sqlite for backward compat.
		if cfg.StorageDriver == "sqlite" {
			cfg.DBPath = cfg.StorageDSN
		}
	} else if yf.DBPath != "" {
		// Legacy db_path: treat as sqlite.
		cfg.DBPath = yf.DBPath
		cfg.StorageDriver = "sqlite"
		cfg.StorageDSN = yf.DBPath
	}
	if yf.Timeout != "" {
		d, err := parseDuration(yf.Timeout)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid timeout %q: %w", yf.Timeout, err)
		}
		cfg.Timeout = d
	}
	if yf.Retention != "" {
		d, err := parseDuration(yf.Retention)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid retention %q: %w", yf.Retention, err)
		}
		cfg.Retention = d
	}
	cfg.TLSSkipVerify = yf.TLSSkipVerify
	if yf.APIAddr != "" {
		cfg.APIAddr = yf.APIAddr
	}
	if yf.MetricsWindow != "" {
		d, err := parseDuration(yf.MetricsWindow)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid metrics_window %q: %w", yf.MetricsWindow, err)
		}
		cfg.MetricsWindow = d
	}
	if yf.BaselineWindows != 0 {
		cfg.BaselineWindows = yf.BaselineWindows
	}
	if yf.AnomalyThreshold != 0 {
		cfg.AnomalyThreshold = yf.AnomalyThreshold
	}
	if yf.WebhookURL != "" {
		cfg.WebhookURL = yf.WebhookURL
	}

	return cfg, nil
}

// Merge returns a new Config where any non-zero field in overrides replaces
// the corresponding field in base. Fields not present in overrides keep their
// base value, so flags-only and YAML-only fields coexist naturally.
func Merge(base, overrides Config) Config {
	result := base
	if overrides.Upstream != "" {
		result.Upstream = overrides.Upstream
	}
	if overrides.Port != 0 {
		result.Port = overrides.Port
	}
	if overrides.Timeout != 0 {
		result.Timeout = overrides.Timeout
	}
	if overrides.DBPath != "" {
		result.DBPath = overrides.DBPath
	}
	if overrides.Retention != 0 {
		result.Retention = overrides.Retention
	}
	// bool: only propagate true — false is the zero value and indistinguishable
	// from "flag not provided". The default is false, so this is always correct.
	if overrides.TLSSkipVerify {
		result.TLSSkipVerify = true
	}
	if overrides.APIAddr != "" {
		result.APIAddr = overrides.APIAddr
	}
	if overrides.MetricsWindow != 0 {
		result.MetricsWindow = overrides.MetricsWindow
	}
	if overrides.BaselineWindows != 0 {
		result.BaselineWindows = overrides.BaselineWindows
	}
	if overrides.AnomalyThreshold != 0 {
		result.AnomalyThreshold = overrides.AnomalyThreshold
	}
	if overrides.WebhookURL != "" {
		result.WebhookURL = overrides.WebhookURL
	}
	if overrides.StorageDriver != "" {
		result.StorageDriver = overrides.StorageDriver
	}
	if overrides.StorageDSN != "" {
		result.StorageDSN = overrides.StorageDSN
	}
	return result
}

// Validate checks that cfg is consistent and complete. Returns a descriptive
// error for the first violation found.
func Validate(cfg Config) error {
	if cfg.Upstream == "" {
		return fmt.Errorf("upstream is required (set in YAML or via --upstream)")
	}
	u, err := url.Parse(cfg.Upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream URL %q: %w", cfg.Upstream, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("upstream URL must include scheme (http:// or https://), got %q", cfg.Upstream)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream URL scheme must be http or https, got %q", u.Scheme)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", cfg.Port)
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %v", cfg.Timeout)
	}
	if cfg.Retention < 0 {
		return fmt.Errorf("retention must be positive or zero, got %v", cfg.Retention)
	}
	if cfg.MetricsWindow <= 0 {
		return fmt.Errorf("metrics_window must be positive, got %v", cfg.MetricsWindow)
	}
	if cfg.BaselineWindows < 1 {
		return fmt.Errorf("baseline_windows must be >= 1, got %d", cfg.BaselineWindows)
	}
	if cfg.AnomalyThreshold <= 0 {
		return fmt.Errorf("anomaly_threshold must be positive, got %v", cfg.AnomalyThreshold)
	}
	if cfg.WebhookURL != "" {
		u, err := url.Parse(cfg.WebhookURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("webhook_url must be an http/https URL, got %q", cfg.WebhookURL)
		}
	}
	driver := cfg.StorageDriver
	if driver == "" {
		driver = "sqlite" // treat unset as default; only fails if explicitly wrong
	}
	if driver != "sqlite" && driver != "postgres" {
		return fmt.Errorf("unsupported storage driver %q: must be \"sqlite\" or \"postgres\"", cfg.StorageDriver)
	}
	if driver == "postgres" && cfg.StorageDSN == "" {
		return fmt.Errorf("storage dsn required when using postgres driver")
	}
	return nil
}

// ValidateDashboard checks that cfg has everything the dashboard binary needs.
// It does NOT require proxy fields (Upstream, Port, Timeout, TLSSkipVerify).
func ValidateDashboard(cfg Config) error {
	driver := cfg.StorageDriver
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" && driver != "postgres" {
		return fmt.Errorf("unsupported storage driver %q: must be \"sqlite\" or \"postgres\"", cfg.StorageDriver)
	}
	if driver == "postgres" && cfg.StorageDSN == "" {
		return fmt.Errorf("storage dsn required when using postgres driver")
	}
	if cfg.MetricsWindow <= 0 {
		return fmt.Errorf("metrics_window must be positive, got %v", cfg.MetricsWindow)
	}
	if cfg.BaselineWindows < 1 {
		return fmt.Errorf("baseline_windows must be >= 1, got %d", cfg.BaselineWindows)
	}
	if cfg.AnomalyThreshold <= 0 {
		return fmt.Errorf("anomaly_threshold must be positive, got %v", cfg.AnomalyThreshold)
	}
	if cfg.WebhookURL != "" {
		u, err := url.Parse(cfg.WebhookURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("webhook_url must be an http/https URL, got %q", cfg.WebhookURL)
		}
	}
	return nil
}

// parseDuration extends time.ParseDuration to support "d" (days) as a unit.
// "7d" → 7 * 24 * time.Hour. Other strings are forwarded to time.ParseDuration.
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
