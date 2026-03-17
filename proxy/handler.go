package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"time"

	"api-profiler/storage"
	traceCtx "api-profiler/trace"
)

// traceKey is the context key for trace IDs stored during ServeHTTP.
type traceKey struct{}

// traceIDs holds the trace and span IDs for a single request hop.
type traceIDs struct {
	traceID      string
	spanID       string
	parentSpanID string
}

// Config contains the proxy configuration.
type Config struct {
	Upstream       *url.URL
	Port           int
	Timeout        time.Duration
	Recorder       *storage.Recorder   // nil disables metrics capture
	TLSSkipVerify  bool
	Normalizer     func(string) string // nil disables path normalization
	RewriteHeaders func(http.Header)   // nil disables header rewriting
	TraceContext   bool                // propagate W3C TraceContext headers
	HealthPath     string              // path answered locally without hitting upstream (e.g. /_sauron/health)
}

// Handler implements http.Handler. It forwards requests to the upstream
// and writes the response back to the client without modification.
type Handler struct {
	rp  *httputil.ReverseProxy
	cfg Config
}

// New creates a validated Handler. Returns error if Config is invalid.
func New(cfg Config) (*Handler, error) {
	if cfg.Upstream == nil {
		return nil, fmt.Errorf("upstream URL is required")
	}
	if cfg.Upstream.Scheme == "" {
		return nil, fmt.Errorf("upstream URL must include scheme (http:// or https://)")
	}
	if cfg.Upstream.Scheme != "http" && cfg.Upstream.Scheme != "https" {
		return nil, fmt.Errorf("upstream URL scheme must be http or https, got %q", cfg.Upstream.Scheme)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}

	basePath := strings.TrimRight(cfg.Upstream.Path, "/")
	upstream := cfg.Upstream

	baseTransport := http.DefaultTransport
	if cfg.TLSSkipVerify {
		log.Println("warning: TLS verification disabled for upstream — do not use in production")
		baseTransport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.URL.Path = basePath + req.URL.Path
			if req.URL.RawPath != "" {
				req.URL.RawPath = basePath + req.URL.RawPath
			}
			// Set Host so the upstream receives its own hostname, not the proxy's.
			req.Host = upstream.Host
			if cfg.RewriteHeaders != nil {
				cfg.RewriteHeaders(req.Header)
			}
			// Propagate W3C TraceContext using IDs generated in ServeHTTP.
			if cfg.TraceContext {
				if ids, ok := req.Context().Value(traceKey{}).(traceIDs); ok {
					req.Header.Set("Traceparent", traceCtx.FormatTraceparent(ids.traceID, ids.spanID))
				}
			}
		},
		Transport: &timeoutTransport{
			transport: baseTransport,
			timeout:   cfg.Timeout,
		},
		// Do not follow redirects: pass 3xx responses to the client as-is.
		// httputil.ReverseProxy uses RoundTrip directly, so redirects are
		// never followed automatically.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.DeadlineExceeded) {
				http.Error(w, "504 Gateway Timeout", http.StatusGatewayTimeout)
				return
			}
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		},
	}

	return &Handler{rp: rp, cfg: cfg}, nil
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			buf := make([]byte, 64<<10)
			n := runtime.Stack(buf, false)
			log.Printf("panic in proxy handler: %v\n%s", rec, buf[:n])
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		}
	}()

	// Respond to health checks locally without touching the upstream.
	if h.cfg.HealthPath != "" && r.URL.Path == h.cfg.HealthPath {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}

	start := time.Now()

	// Extract or generate W3C TraceContext IDs before proxying so they are
	// available to both the Director (for propagation) and the Recorder.
	var tid, sid, parentSid string
	if h.cfg.TraceContext {
		existingTraceID, incomingParentID, ok := traceCtx.ParseTraceparent(r.Header.Get("Traceparent"))
		if ok {
			tid = existingTraceID
			parentSid = incomingParentID
		} else {
			tid = traceCtx.NewTraceID()
		}
		sid = traceCtx.NewSpanID()
		r = r.WithContext(context.WithValue(r.Context(), traceKey{}, traceIDs{tid, sid, parentSid}))
	}

	sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
	h.rp.ServeHTTP(sw, r)
	durationMs := float64(time.Since(start).Microseconds()) / 1000
	log.Printf("%s %s %d %.3fms", r.Method, r.URL.RequestURI(), sw.code, durationMs)
	if h.cfg.Recorder != nil {
		path := r.URL.Path
		if h.cfg.Normalizer != nil {
			path = h.cfg.Normalizer(path)
		}
		h.cfg.Recorder.Record(storage.Record{
			Timestamp:    start,
			Method:       r.Method,
			Path:         path,
			StatusCode:   sw.code,
			DurationMs:   durationMs,
			TraceID:      tid,
			SpanID:       sid,
			ParentSpanID: parentSid,
		})
	}
}

// statusWriter wraps http.ResponseWriter to capture the written status code.
type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.written {
		sw.code = code
		sw.written = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher so streaming responses work correctly.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// timeoutTransport wraps http.RoundTripper adding a per-request timeout via
// context. defer cancel() ensures the context is always released.
type timeoutTransport struct {
	transport http.RoundTripper
	timeout   time.Duration
}

func (t *timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	resp, err := t.transport.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	// Defer cancel until the response body is closed so the context stays
	// alive during body streaming. Without this, cancel() would fire as soon
	// as RoundTrip returns (after headers arrive) and truncate large bodies.
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose wraps a ReadCloser and calls cancel when the body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	c.cancel()
	return c.ReadCloser.Close()
}
