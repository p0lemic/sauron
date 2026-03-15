package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"api-profiler/alerts"
	"api-profiler/api"
	"api-profiler/config"
	"api-profiler/health"
	"api-profiler/metrics"
	"api-profiler/storage"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file (optional)")
	listenFlag := flag.String("listen", "", "dashboard listen address (default: :9090)")
	upstreamFlag := flag.String("upstream", "", "upstream base URL for health check (e.g. http://localhost:8080)")
	storageDriverFlag := flag.String("storage-driver", "", "storage driver: sqlite or postgres (default: sqlite)")
	storageDSNFlag := flag.String("storage-dsn", "", "storage DSN: file path for sqlite, connection string for postgres (default: profiler.db)")
	metricsWindowFlag := flag.Duration("metrics-window", 0, "metrics aggregation window (default: 30m)")
	baselineWindowsFlag := flag.Int("baseline-windows", 0, "number of past windows used for baseline (default: 5)")
	anomalyThresholdFlag := flag.Float64("anomaly-threshold", 0, "anomaly detection multiplier (default: 3.0)")
	webhookURLFlag := flag.String("webhook-url", "", "URL to POST alert notifications to (optional)")
	errorRateThresholdFlag := flag.Float64("error-rate-threshold", 0, "error rate % to trigger alert, e.g. 10.0 (0 = disabled)")
	throughputDropFlag := flag.Float64("throughput-drop-threshold", 0, "min RPS % of baseline before alerting, e.g. 50.0 (0 = disabled)")
	apdexTFlag := flag.Int("apdex-t", 0, "Apdex satisfaction threshold in ms (default: 500)")

	if cp := findConfigFlag(os.Args[1:]); cp != "" && *configPath == "" {
		*configPath = cp
	}
	flag.Parse()

	base := config.Default()
	if *configPath != "" {
		var err error
		base, err = config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	envCfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	base = config.Merge(base, envCfg)

	var overrides config.Config
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "upstream":
			overrides.Upstream = *upstreamFlag
		case "listen":
			overrides.APIAddr = *listenFlag
		case "storage-driver":
			overrides.StorageDriver = *storageDriverFlag
		case "storage-dsn":
			overrides.StorageDSN = *storageDSNFlag
		case "metrics-window":
			overrides.MetricsWindow = *metricsWindowFlag
		case "baseline-windows":
			overrides.BaselineWindows = *baselineWindowsFlag
		case "anomaly-threshold":
			overrides.AnomalyThreshold = *anomalyThresholdFlag
		case "webhook-url":
			overrides.WebhookURL = *webhookURLFlag
		case "error-rate-threshold":
			overrides.ErrorRateThreshold = *errorRateThresholdFlag
		case "throughput-drop-threshold":
			overrides.ThroughputDropThreshold = *throughputDropFlag
		case "apdex-t":
			overrides.ApdexT = *apdexTFlag
		}
	})

	cfg := config.Merge(base, overrides)
	if err := config.ValidateDashboard(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	store, err := storage.Open(cfg.StorageDriver, cfg.StorageDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening storage: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	engine := metrics.NewEngine(store, cfg.MetricsWindow)
	detector := alerts.NewDetector(engine, cfg.AnomalyThreshold, cfg.BaselineWindows)
	if targets := cfg.EffectiveWebhooks(); len(targets) > 0 {
		wts := make([]alerts.WebhookTarget, len(targets))
		for i, t := range targets {
			wts[i] = alerts.WebhookTarget{URL: t.URL, Format: t.Format, Events: t.Events}
		}
		detector.SetMultiNotifier(alerts.NewMultiNotifier(wts))
	}
	if cfg.ErrorRateThreshold > 0 {
		detector.SetErrorRateThreshold(cfg.ErrorRateThreshold)
	}
	if cfg.ThroughputDropThreshold > 0 {
		detector.SetThroughputDropThreshold(cfg.ThroughputDropThreshold)
	}
	if cfg.AnomalySensitivity > 0 && cfg.StatisticalWindows >= 3 {
		detector.SetStatisticalParams(cfg.AnomalySensitivity, cfg.StatisticalWindows)
	}
	detector.Start()

	var checker *health.Checker
	if cfg.HealthCheck.Enabled && cfg.Upstream != "" {
		path := cfg.HealthCheck.Path
		if path == "" {
			path = "/"
		}
		interval := cfg.HealthCheck.Interval
		if interval == 0 {
			interval = 10 * time.Second
		}
		timeout := cfg.HealthCheck.Timeout
		if timeout == 0 {
			timeout = 5 * time.Second
		}
		threshold := cfg.HealthCheck.Threshold
		if threshold == 0 {
			threshold = 3
		}
		checker = health.New(cfg.Upstream+path, interval, timeout, threshold)
		checker.Start()
		log.Printf("health check enabled: target=%s interval=%s", cfg.Upstream+path, interval)
	}

	apiSrv := api.NewServer(engine, cfg.APIAddr, cfg.BaselineWindows, cfg.ApdexT, detector, checker)
	if err := apiSrv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: starting dashboard server: %v\n", err)
		os.Exit(1)
	}
	log.Printf("API Profiler dashboard on %s (storage: %s)", apiSrv.Addr(), cfg.StorageDriver)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("shutting down (max 30s)...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := apiSrv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error during dashboard shutdown: %v\n", err)
	}
	if checker != nil {
		checker.Stop()
	}
	detector.Stop()
	log.Println("shutdown complete")
}

func findConfigFlag(args []string) string {
	for i, arg := range args {
		for _, prefix := range []string{"--config=", "-config="} {
			if strings.HasPrefix(arg, prefix) {
				return strings.TrimPrefix(arg, prefix)
			}
		}
		if (arg == "--config" || arg == "-config") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
