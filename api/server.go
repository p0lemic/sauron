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
	"time"

	"api-profiler/alerts"
	"api-profiler/metrics"
)

//go:embed all:dashboard
var dashboardFS embed.FS

// Server is the internal HTTP server for metrics queries and the dashboard.
type Server struct {
	engine          *metrics.Engine
	baselineWindows int
	detector        *alerts.Detector
	srv             *http.Server
	ln              net.Listener
}

// NewServer creates a Server backed by engine.
// addr is the listen address (e.g. "localhost:9090").
// baselineWindows is the number of past windows used for baseline computation.
// detector provides active alert data for GET /alerts/active.
func NewServer(engine *metrics.Engine, addr string, baselineWindows int, detector *alerts.Detector) *Server {
	s := &Server{engine: engine, baselineWindows: baselineWindows, detector: detector}
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
	mux.HandleFunc("/health", s.handleHealth)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
}
