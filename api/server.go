package api

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api-profiler/alerts"
	"api-profiler/health"
	"api-profiler/metrics"
	"api-profiler/storage"
)

//go:embed all:dashboard
var dashboardFS embed.FS

// Server is the internal HTTP server for metrics queries and the dashboard.
type Server struct {
	engine          *metrics.Engine
	store           storage.Store // for ingest endpoints; may be nil
	baselineWindows int
	apdexT          int
	detector        *alerts.Detector
	checker         *health.Checker // nil if health check disabled
	srv             *http.Server
	ln              net.Listener
}

// NewServer creates a Server backed by engine.
// addr is the listen address (e.g. "localhost:9090").
// baselineWindows is the number of past windows used for baseline computation.
// apdexT is the default Apdex satisfaction threshold in ms (0 → use 500).
// detector provides active alert data for GET /alerts/active.
// checker is optional (nil = disabled); when non-nil, GET /health includes upstream state.
// store is optional (nil = ingest disabled); when non-nil, POST /ingest/spans is enabled.
func NewServer(engine *metrics.Engine, store storage.Store, addr string, baselineWindows int, apdexT int, detector *alerts.Detector, checker *health.Checker) *Server {
	if apdexT <= 0 {
		apdexT = 500
	}
	s := &Server{engine: engine, store: store, baselineWindows: baselineWindows, apdexT: apdexT, detector: detector, checker: checker}
	mux := http.NewServeMux()
	sub, _ := fs.Sub(dashboardFS, "dashboard")
	mux.Handle("/static/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/", s.handleDashboard)
	mux.HandleFunc("/metrics/histogram", s.handleHistogram)
	mux.HandleFunc("/metrics/latency", s.handleLatency)
	mux.HandleFunc("/metrics/summary", s.handleSummary)
	mux.HandleFunc("/metrics/table", s.handleTable)
	mux.HandleFunc("/metrics/endpoints", s.handleEndpoints)
	mux.HandleFunc("/metrics/slowest", s.handleSlowest)
	mux.HandleFunc("/metrics/errors", s.handleErrors)
	mux.HandleFunc("/metrics/throughput", s.handleThroughput)
	mux.HandleFunc("/metrics/baseline", s.handleBaseline)
	mux.HandleFunc("/alerts/active", s.handleAlertsActive)
	mux.HandleFunc("/alerts/history", s.handleAlertsHistory)
	mux.HandleFunc("/alerts/silence", s.handleCreateSilence)
	mux.HandleFunc("/alerts/silences", s.handleListSilences)
	mux.HandleFunc("/metrics/requests", s.handleRequests)
	mux.HandleFunc("/metrics/slowest-requests", s.handleSlowestRequests)
	mux.HandleFunc("/metrics/status", s.handleStatus)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics/prometheus", s.handlePrometheus)
	mux.HandleFunc("/metrics/apdex", s.handleApdex)
	mux.HandleFunc("/metrics/errors/fingerprints", s.handleErrorFingerprints)
	mux.HandleFunc("/metrics/heatmap", s.handleHeatmap)
	mux.HandleFunc("/metrics/anomaly-scores", s.handleAnomalyScores)
	mux.HandleFunc("/traces", s.handleTraces)
	mux.HandleFunc("/traces/", s.handleTraceDetail)
	mux.HandleFunc("/ingest/spans", s.handleIngestSpans)
	s.srv = &http.Server{Addr: addr, Handler: mux}
	return s
}

// Start binds the listener and serves in a background goroutine.
// Returns an error if the address is already in use.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	go s.srv.Serve(ln) //nolint:errcheck
	return nil
}

// Addr returns the address the server is listening on.
// Only valid after a successful Start().
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.srv.Addr
}

// Shutdown gracefully stops the server, waiting up to the context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := dashboardFS.ReadFile("dashboard/index.html")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

func (s *Server) handleHistogram(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	method := r.URL.Query().Get("method")
	path := r.URL.Query().Get("path")
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var stat metrics.HistogramStat
	if from.IsZero() {
		stat, err = s.engine.Histogram(method, path)
	} else {
		stat, err = s.engine.HistogramForRange(method, path, from, to)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stat) //nolint:errcheck
}

func (s *Server) handleLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	method := r.URL.Query().Get("method")
	path := r.URL.Query().Get("path")
	if method == "" {
		writeJSONError(w, http.StatusBadRequest, "missing query param: method")
		return
	}
	if path == "" {
		writeJSONError(w, http.StatusBadRequest, "missing query param: path")
		return
	}
	buckets, err := s.engine.Latency(method, path)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buckets) //nolint:errcheck
}

func (s *Server) handleTable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var rows []metrics.TableRow
	if from.IsZero() {
		rows, err = s.engine.Table()
	} else {
		rows, err = s.engine.TableForRange(from, to)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []metrics.TableRow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rows) //nolint:errcheck
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var stat metrics.SummaryStat
	if from.IsZero() {
		stat, err = s.engine.Summary()
	} else {
		stat, err = s.engine.SummaryForRange(from, to)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stat) //nolint:errcheck
}

func (s *Server) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var stats []metrics.EndpointStat
	var err error

	if raw := r.URL.Query().Get("window"); raw != "" {
		win, parseErr := parseWindow(raw)
		if parseErr != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid window %q: %v", raw, parseErr))
			return
		}
		stats, err = s.engine.EndpointsForWindow(win)
	} else {
		stats, err = s.engine.Endpoints()
	}

	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []metrics.EndpointStat{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats) //nolint:errcheck
}

// parseWindow parses a duration string (Go syntax + "Nd" days) and validates
// that it is strictly positive.
func parseWindow(s string) (time.Duration, error) {
	d, err := parseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("window must be positive, got %v", d)
	}
	return d, nil
}

// parseDuration mirrors config.parseDuration: supports "Nd" (days) plus standard Go durations.
func parseDuration(s string) (time.Duration, error) {
	if len(s) > 1 && s[len(s)-1] == 'd' {
		n := 0
		for _, c := range s[:len(s)-1] {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("time: invalid duration %q", s)
			}
			n = n*10 + int(c-'0')
		}
		if n <= 0 {
			return 0, fmt.Errorf("time: invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// parseTimeRange parses optional "from" and "to" RFC3339 query params.
// Returns zero times when params are absent (caller should use engine defaults).
// "to=now" or absent "to" means time.Now().
func parseTimeRange(r *http.Request) (from, to time.Time, err error) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" && toStr == "" {
		return // both zero → use engine defaults
	}
	if fromStr != "" {
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			err = fmt.Errorf("invalid from %q: %w", fromStr, err)
			return
		}
	}
	if toStr == "" || toStr == "now" {
		to = time.Now()
	} else {
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			err = fmt.Errorf("invalid to %q: %w", toStr, err)
			return
		}
	}
	if !from.IsZero() && to.Before(from) {
		err = fmt.Errorf("to must be after from")
	}
	return
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func (s *Server) handleSlowest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	n := 10
	if raw := r.URL.Query().Get("n"); raw != "" {
		var err error
		n, err = strconv.Atoi(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid n %q: %v", raw, err))
			return
		}
		if n <= 0 {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("n must be positive, got %d", n))
			return
		}
	}

	stats, err := s.engine.Slowest(n)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []metrics.EndpointStat{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats) //nolint:errcheck
}

func (s *Server) handleErrors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.engine.Errors()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []metrics.ErrorStat{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats) //nolint:errcheck
}

func (s *Server) handleThroughput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.engine.Throughput()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []metrics.ThroughputStat{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats) //nolint:errcheck
}

func (s *Server) handleBaseline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.engine.Baseline(s.baselineWindows)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []metrics.BaselineStat{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats) //nolint:errcheck
}

func (s *Server) handleAlertsActive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	active := s.detector.Active()
	if active == nil {
		active = []alerts.Alert{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(active) //nolint:errcheck
}

func (s *Server) handleAlertsHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	history := s.detector.History()
	if history == nil {
		history = []alerts.AlertRecord{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history) //nolint:errcheck
}

func (s *Server) handleCreateSilence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Method   string `json:"method"`
		Path     string `json:"path"`
		Duration string `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	dur, err := parseDuration(body.Duration)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid duration %q: %v", body.Duration, err))
		return
	}
	if dur <= 0 {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("duration must be positive, got %v", body.Duration))
		return
	}
	silence := s.detector.Silence(body.Method, body.Path, dur)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(silence) //nolint:errcheck
}

func (s *Server) handleListSilences(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	silences := s.detector.ActiveSilences()
	if silences == nil {
		silences = []alerts.Silence{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(silences) //nolint:errcheck
}

func (s *Server) handleSlowestRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n := 10
	if raw := r.URL.Query().Get("n"); raw != "" {
		var err error
		n, err = strconv.Atoi(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid n %q: %v", raw, err))
			return
		}
	}
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var records []storage.Record
	if from.IsZero() {
		records, err = s.engine.SlowestRequests(n)
	} else {
		records, err = s.engine.SlowestRequestsForRange(from, to, n)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []storage.Record{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records) //nolint:errcheck
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	method := r.URL.Query().Get("method")
	path := r.URL.Query().Get("path")
	if (method == "") != (path == "") {
		writeJSONError(w, http.StatusBadRequest, "method and path are required together")
		return
	}
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var groups []metrics.StatusGroup
	if method != "" {
		if from.IsZero() {
			groups, err = s.engine.StatusBreakdownForEndpoint(method, path)
		} else {
			groups, err = s.engine.StatusBreakdownForEndpointRange(method, path, from, to)
		}
	} else {
		if from.IsZero() {
			groups, err = s.engine.StatusBreakdown()
		} else {
			groups, err = s.engine.StatusBreakdownForRange(from, to)
		}
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups) //nolint:errcheck
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n := 100
	if raw := r.URL.Query().Get("n"); raw != "" {
		var err error
		n, err = strconv.Atoi(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid n %q: %v", raw, err))
			return
		}
	}
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var records []storage.Record
	if from.IsZero() {
		records, err = s.engine.Requests(n)
	} else {
		records, err = s.engine.RequestsForRange(from, to, n)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []storage.Record{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records) //nolint:errcheck
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.checker == nil {
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
		return
	}
	resp := struct {
		Status   string       `json:"status"`
		Upstream health.State `json:"upstream"`
	}{
		Status:   "ok",
		Upstream: s.checker.State(),
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *Server) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rows, err := s.engine.Table()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	type metric struct {
		name string
		help string
		key  func(metrics.TableRow) float64
	}
	gauges := []metric{
		{"apiprofiler_request_duration_p50_ms", "P50 request latency in milliseconds", func(r metrics.TableRow) float64 { return r.P50 }},
		{"apiprofiler_request_duration_p95_ms", "P95 request latency in milliseconds", func(r metrics.TableRow) float64 { return r.P95 }},
		{"apiprofiler_request_duration_p99_ms", "P99 request latency in milliseconds", func(r metrics.TableRow) float64 { return r.P99 }},
		{"apiprofiler_request_error_rate", "Request error rate percentage (0-100)", func(r metrics.TableRow) float64 { return r.ErrorRate }},
		{"apiprofiler_request_rps_current", "Current requests per second", func(r metrics.TableRow) float64 { return r.RPSCurrent }},
		{"apiprofiler_request_total", "Total request count in current window", func(r metrics.TableRow) float64 { return float64(r.Count) }},
	}

	for _, g := range gauges {
		fmt.Fprintf(w, "# HELP %s %s\n", g.name, g.help)
		fmt.Fprintf(w, "# TYPE %s gauge\n", g.name)
		for _, row := range rows {
			fmt.Fprintf(w, "%s{method=%q,path=%q} %g\n", g.name, row.Method, row.Path, g.key(row))
		}
	}

	// Active alerts counter by kind.
	active := s.detector.Active()
	counts := map[string]int{
		alerts.KindLatency:    0,
		alerts.KindErrorRate:  0,
		alerts.KindThroughput: 0,
	}
	for _, a := range active {
		counts[a.Kind]++
	}
	fmt.Fprintf(w, "# HELP apiprofiler_active_alerts_total Number of currently active alerts\n")
	fmt.Fprintf(w, "# TYPE apiprofiler_active_alerts_total gauge\n")
	for _, kind := range []string{alerts.KindLatency, alerts.KindErrorRate, alerts.KindThroughput} {
		fmt.Fprintf(w, "apiprofiler_active_alerts_total{kind=%q} %d\n", kind, counts[kind])
	}
}

func (s *Server) handleApdex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tMs := float64(s.apdexT)
	if raw := r.URL.Query().Get("t"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid t: must be a positive integer (ms)")
			return
		}
		tMs = float64(n)
	}

	var (
		stats []metrics.ApdexStat
		err   error
	)
	if raw := r.URL.Query().Get("window"); raw != "" {
		win, parseErr := parseWindow(raw)
		if parseErr != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid window %q: %v", raw, parseErr))
			return
		}
		now := time.Now()
		stats, err = s.engine.ApdexForRange(tMs, now.Add(-win), now)
	} else {
		stats, err = s.engine.Apdex(tMs)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if stats == nil {
		stats = []metrics.ApdexStat{}
	}

	resp := struct {
		TMs       int                 `json:"t_ms"`
		Endpoints []metrics.ApdexStat `json:"endpoints"`
	}{
		TMs:       int(tMs),
		Endpoints: stats,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *Server) handleErrorFingerprints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statusFilter := r.URL.Query().Get("status")
	if statusFilter != "" && statusFilter != "4xx" && statusFilter != "5xx" {
		writeJSONError(w, http.StatusBadRequest, "invalid status filter: use 4xx or 5xx")
		return
	}

	var (
		fps []metrics.ErrorFingerprint
		err error
	)
	if raw := r.URL.Query().Get("window"); raw != "" {
		win, parseErr := parseWindow(raw)
		if parseErr != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid window %q: %v", raw, parseErr))
			return
		}
		now := time.Now()
		fps, err = s.engine.ErrorFingerprintsForRange(now.Add(-win), now)
	} else {
		fps, err = s.engine.ErrorFingerprints()
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Apply optional status class filter.
	if statusFilter != "" {
		filtered := fps[:0]
		for _, fp := range fps {
			if statusFilter == "4xx" && fp.StatusCode >= 400 && fp.StatusCode < 500 {
				filtered = append(filtered, fp)
			} else if statusFilter == "5xx" && fp.StatusCode >= 500 {
				filtered = append(filtered, fp)
			}
		}
		fps = filtered
	}
	if fps == nil {
		fps = []metrics.ErrorFingerprint{}
	}

	totalErrors := 0
	for _, fp := range fps {
		totalErrors += fp.Count
	}

	resp := struct {
		TotalErrors  int                        `json:"total_errors"`
		Fingerprints []metrics.ErrorFingerprint `json:"fingerprints"`
	}{
		TotalErrors:  totalErrors,
		Fingerprints: fps,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *Server) handleHeatmap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		metric = "rps"
	}
	if metric != "rps" && metric != "error_rate" {
		writeJSONError(w, http.StatusBadRequest, "metric must be rps or error_rate")
		return
	}
	from, to, err := parseTimeRange(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if from.IsZero() {
		now := time.Now()
		from = now.Add(-s.engine.Window())
		to = now
	}
	result, err := s.engine.Heatmap(metric, from, to)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result) //nolint:errcheck
}

func (s *Server) handleAnomalyScores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	n := s.baselineWindows
	if raw := r.URL.Query().Get("windows"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 3 {
			writeJSONError(w, http.StatusBadRequest, "windows must be an integer >= 3")
			return
		}
		n = parsed
	}
	scores, err := s.engine.AnomalyScores(n)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scores) //nolint:errcheck
}

func (s *Server) handleTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var (
		summaries []metrics.TraceSummary
		err       error
	)
	if raw := r.URL.Query().Get("window"); raw != "" {
		win, parseErr := parseWindow(raw)
		if parseErr != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid window %q: %v", raw, parseErr))
			return
		}
		now := time.Now()
		summaries, err = s.engine.TracesForRange(now.Add(-win), now)
	} else {
		summaries, err = s.engine.Traces()
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if summaries == nil {
		summaries = []metrics.TraceSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries) //nolint:errcheck
}

// traceSpan is the unified span view used for waterfall rendering.
type traceSpan struct {
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id"`
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`        // proxy|controller|db|cache|event|view|rpc
	StartMs      float64           `json:"start_ms"`    // offset from trace root start
	DurationMs   float64           `json:"duration_ms"`
	StatusCode   int               `json:"status_code"` // proxy spans only; 0 for inner spans
	Attributes   map[string]string `json:"attributes"`
	Status       string            `json:"status"`      // ok|error
}

// traceDetailResponse is the response for GET /traces/{traceID}.
type traceDetailResponse struct {
	TraceID string       `json:"trace_id"`
	TotalMs float64      `json:"total_ms"`
	Spans   []traceSpan  `json:"spans"`
}

func (s *Server) handleTraceDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	traceID := strings.TrimPrefix(r.URL.Path, "/traces/")
	if traceID == "" {
		writeJSONError(w, http.StatusBadRequest, "missing trace_id")
		return
	}

	proxyRecords, err := s.engine.TraceSpans(traceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	innerSpans, err := s.engine.InnerSpans(traceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Find the earliest start time across all spans to compute offsets.
	var root time.Time
	for i, rec := range proxyRecords {
		if i == 0 || rec.Timestamp.Before(root) {
			root = rec.Timestamp
		}
	}
	for _, sp := range innerSpans {
		if root.IsZero() || sp.StartTime.Before(root) {
			root = sp.StartTime
		}
	}
	if root.IsZero() {
		root = time.Now()
	}

	spans := make([]traceSpan, 0, len(proxyRecords)+len(innerSpans))
	var traceEnd time.Time

	for _, rec := range proxyRecords {
		startMs := float64(rec.Timestamp.Sub(root).Microseconds()) / 1000
		end := rec.Timestamp.Add(time.Duration(rec.DurationMs * float64(time.Millisecond)))
		if end.After(traceEnd) {
			traceEnd = end
		}
		status := "ok"
		if rec.StatusCode >= 400 {
			status = "error"
		}
		spans = append(spans, traceSpan{
			SpanID:       rec.SpanID,
			ParentSpanID: rec.ParentSpanID,
			Name:         rec.Method + " " + rec.Path,
			Kind:         "proxy",
			StartMs:      startMs,
			DurationMs:   rec.DurationMs,
			StatusCode:   rec.StatusCode,
			Attributes:   map[string]string{},
			Status:       status,
		})
	}
	for _, sp := range innerSpans {
		startMs := float64(sp.StartTime.Sub(root).Microseconds()) / 1000
		end := sp.StartTime.Add(time.Duration(sp.DurationMs * float64(time.Millisecond)))
		if end.After(traceEnd) {
			traceEnd = end
		}
		attrs := sp.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		spans = append(spans, traceSpan{
			SpanID:       sp.SpanID,
			ParentSpanID: sp.ParentSpanID,
			Name:         sp.Name,
			Kind:         sp.Kind,
			StartMs:      startMs,
			DurationMs:   sp.DurationMs,
			StatusCode:   0,
			Attributes:   attrs,
			Status:       sp.Status,
		})
	}

	totalMs := float64(traceEnd.Sub(root).Microseconds()) / 1000
	if totalMs < 0 {
		totalMs = 0
	}

	resp := traceDetailResponse{
		TraceID: traceID,
		TotalMs: totalMs,
		Spans:   spans,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (s *Server) handleIngestSpans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil {
		http.Error(w, "ingest not configured", http.StatusServiceUnavailable)
		return
	}

	var spans []storage.InnerSpan
	if err := json.NewDecoder(r.Body).Decode(&spans); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	for i := range spans {
		if spans[i].TraceID == "" || spans[i].SpanID == "" {
			writeJSONError(w, http.StatusBadRequest, "each span must have trace_id and span_id")
			return
		}
		if spans[i].Status == "" {
			spans[i].Status = "ok"
		}
		if spans[i].StartTime.IsZero() {
			spans[i].StartTime = time.Now()
		}
		if err := s.store.SaveSpan(spans[i]); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"accepted":%d}`, len(spans))
}
