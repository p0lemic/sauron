package storage

import (
	"encoding/json"
	"time"
)

// InnerSpan represents an application-level span reported by an instrumented
// service via POST /ingest/spans. It lives in the spans table, separate from
// the proxy-level requests table.
type InnerSpan struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id"`
	Name         string            `json:"name"`        // e.g. "App\Controller\...", "doctrine.query"
	Kind         string            `json:"kind"`        // controller|db|cache|event|view|rpc
	StartTime    time.Time         `json:"start_time"`  // absolute UTC
	DurationMs   float64           `json:"duration_ms"`
	Attributes   map[string]string `json:"attributes"`  // free-form key-value pairs
	Status       string            `json:"status"`      // ok|error
}

func encodeAttrs(attrs map[string]string) string {
	if len(attrs) == 0 {
		return "{}"
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeAttrs(s string) map[string]string {
	m := map[string]string{}
	if s == "" || s == "{}" {
		return m
	}
	json.Unmarshal([]byte(s), &m) //nolint:errcheck
	return m
}
