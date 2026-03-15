package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"api-profiler/config"
	"api-profiler/normalizer"
	"api-profiler/proxy"
	"api-profiler/storage"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file (optional)")
	upstreamFlag := flag.String("upstream", "", "upstream base URL, e.g. http://localhost:3000")
	portFlag := flag.Int("port", 0, "proxy listen port (default: 8080)")
	timeoutFlag := flag.Duration("timeout", 0, "upstream request timeout (default: 30s)")
	tlsSkipFlag := flag.Bool("tls-skip-verify", false, "disable TLS certificate verification for upstream")
	storageDriverFlag := flag.String("storage-driver", "", "storage driver: sqlite or postgres (default: sqlite)")
	storageDSNFlag := flag.String("storage-dsn", "", "storage DSN: file path for sqlite, connection string for postgres (default: profiler.db)")
	retentionFlag := flag.Duration("retention", 0, "how long to keep request records, e.g. 7d, 24h (default: 0 = disabled)")
	noTraceContextFlag := flag.Bool("no-trace-context", false, "disable W3C TraceContext header propagation")

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
		case "port":
			overrides.Port = *portFlag
		case "timeout":
			overrides.Timeout = *timeoutFlag
		case "tls-skip-verify":
			overrides.TLSSkipVerify = *tlsSkipFlag
		case "storage-driver":
			overrides.StorageDriver = *storageDriverFlag
		case "storage-dsn":
			overrides.StorageDSN = *storageDSNFlag
		case "retention":
			overrides.Retention = *retentionFlag
		}
	})

	cfg := config.Merge(base, overrides)
	// --no-trace-context toggles a default-true bool; handle outside Merge.
	if *noTraceContextFlag {
		cfg.TraceContext = false
	}
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	upstreamURL, _ := url.Parse(cfg.Upstream) // already validated

	var rewriteFn func(http.Header)
	if len(cfg.HeaderRules) > 0 {
		rules := cfg.HeaderRules
		rewriteFn = func(h http.Header) {
			for _, r := range rules {
				key := http.CanonicalHeaderKey(r.Header)
				switch r.Action {
				case "set":
					h.Set(key, r.Value)
				case "remove":
					h.Del(key)
				}
			}
		}
	}

	var normFn func(string) string
	if cfg.NormalizePaths {
		rules := make([]normalizer.Rule, len(cfg.PathRules))
		for i, r := range cfg.PathRules {
			rules[i] = normalizer.Rule{Pattern: r.Pattern, Replacement: r.Replacement}
		}
		n, err := normalizer.New(rules, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: building path normalizer: %v\n", err)
			os.Exit(1)
		}
		normFn = n.Normalize
	}

	store, err := storage.Open(cfg.StorageDriver, cfg.StorageDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening storage: %v\n", err)
		os.Exit(1)
	}

	var pruner *storage.Pruner
	if cfg.Retention > 0 {
		pruner = storage.NewPruner(store, cfg.Retention, storage.DefaultPruneInterval)
		pruner.Start()
		log.Printf("data retention enabled: %s", cfg.Retention)
	}

	// Statistical anomaly detection is only in the dashboard binary, but the
	// profiler binary can still be extended here if needed in the future.
	_ = cfg.AnomalySensitivity // suppress unused-field warning

	rec := storage.NewRecorder(store, 0)

	h, err := proxy.New(proxy.Config{
		Upstream:       upstreamURL,
		Port:           cfg.Port,
		Timeout:        cfg.Timeout,
		Recorder:       rec,
		TLSSkipVerify:  cfg.TLSSkipVerify,
		Normalizer:     normFn,
		RewriteHeaders: rewriteFn,
		TraceContext:   cfg.TraceContext,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot listen on %s: %v\n", addr, err)
		os.Exit(1)
	}
	proxySrv := &http.Server{Handler: h}
	go func() {
		log.Printf("API Profiler proxy on %s → %s", addr, upstreamURL)
		if err := proxySrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("shutting down (max 30s)...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := proxySrv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error during proxy shutdown: %v\n", err)
	}
	if pruner != nil {
		pruner.Stop()
	}
	if err := rec.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: recorder close: %v\n", err)
	}
	if err := store.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: store close: %v\n", err)
	}
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
