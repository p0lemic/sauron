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
		}
	})

	cfg := config.Merge(base, overrides)
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	upstreamURL, _ := url.Parse(cfg.Upstream) // already validated

	store, err := storage.Open(cfg.StorageDriver, cfg.StorageDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening storage: %v\n", err)
		os.Exit(1)
	}
	rec := storage.NewRecorder(store, 0)

	h, err := proxy.New(proxy.Config{
		Upstream:      upstreamURL,
		Port:          cfg.Port,
		Timeout:       cfg.Timeout,
		Recorder:      rec,
		TLSSkipVerify: cfg.TLSSkipVerify,
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
