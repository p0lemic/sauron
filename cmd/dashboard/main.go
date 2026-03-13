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
	"api-profiler/metrics"
	"api-profiler/storage"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file (optional)")
	listenFlag := flag.String("listen", "", "dashboard listen address (default: :9090)")
	storageDriverFlag := flag.String("storage-driver", "", "storage driver: sqlite or postgres (default: sqlite)")
	storageDSNFlag := flag.String("storage-dsn", "", "storage DSN: file path for sqlite, connection string for postgres (default: profiler.db)")
	metricsWindowFlag := flag.Duration("metrics-window", 0, "metrics aggregation window (default: 30m)")
	baselineWindowsFlag := flag.Int("baseline-windows", 0, "number of past windows used for baseline (default: 5)")
	anomalyThresholdFlag := flag.Float64("anomaly-threshold", 0, "anomaly detection multiplier (default: 3.0)")
	webhookURLFlag := flag.String("webhook-url", "", "URL to POST alert notifications to (optional)")

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
	if cfg.WebhookURL != "" {
		detector.SetNotifier(alerts.NewWebhookNotifier(cfg.WebhookURL))
	}
	detector.Start()

	apiSrv := api.NewServer(engine, cfg.APIAddr, cfg.BaselineWindows, detector)
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
