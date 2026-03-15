package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// ── Interfaces ────────────────────────────────────────────────────────────────

// Notifier is the legacy single-method interface (backward compat).
type Notifier interface {
	Notify(a Alert)
}

// ResolveNotifier receives both fired and resolved events.
type ResolveNotifier interface {
	NotifyFired(a Alert)
	NotifyResolved(a Alert, resolvedAt time.Time)
}

// notifierAdapter wraps a Notifier so it satisfies ResolveNotifier.
type notifierAdapter struct{ n Notifier }

func (a *notifierAdapter) NotifyFired(al Alert)                       { a.n.Notify(al) }
func (a *notifierAdapter) NotifyResolved(_ Alert, _ time.Time)        {}

// ── WebhookNotifier (legacy, single target) ───────────────────────────────────

// WebhookNotifier sends Alert payloads via HTTP POST.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// NewWebhookNotifier creates a WebhookNotifier with a 5-second timeout.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:    url,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify marshals a and POSTs it to w.URL. Errors are logged and swallowed.
func (w *WebhookNotifier) Notify(a Alert) {
	body, err := json.Marshal(a)
	if err != nil {
		log.Printf("webhook: marshal error: %v", err)
		return
	}
	resp, err := w.Client.Post(w.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("webhook: POST %s failed: %v", w.URL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("webhook: POST %s returned %s", w.URL, resp.Status)
	}
}

// ── WebhookEvent (multi-target payload) ──────────────────────────────────────

const (
	EventFired    = "fired"
	EventResolved = "resolved"
)

// WebhookEvent is the JSON payload sent to multi-target webhooks.
type WebhookEvent struct {
	Event       string     `json:"event"`
	Kind        string     `json:"kind"`
	Method      string     `json:"method"`
	Path        string     `json:"path"`
	TriggeredAt time.Time  `json:"triggered_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	DurationMs  int64      `json:"duration_ms,omitempty"`
	// Latency-specific
	CurrentP99  float64 `json:"current_p99,omitempty"`
	BaselineP99 float64 `json:"baseline_p99,omitempty"`
	Threshold   float64 `json:"threshold,omitempty"`
	// Error-rate-specific
	ErrorRate          float64 `json:"error_rate,omitempty"`
	ErrorRateThreshold float64 `json:"error_rate_threshold,omitempty"`
	// Throughput-specific
	CurrentRPS  float64 `json:"current_rps,omitempty"`
	BaselineRPS float64 `json:"baseline_rps,omitempty"`
	DropPct     float64 `json:"drop_pct,omitempty"`
	// Statistical-specific
	ZScore float64 `json:"z_score,omitempty"`
}

func eventFromAlert(event string, a Alert) WebhookEvent {
	return WebhookEvent{
		Event:              event,
		Kind:               a.Kind,
		Method:             a.Method,
		Path:               a.Path,
		TriggeredAt:        a.TriggeredAt,
		CurrentP99:         a.CurrentP99,
		BaselineP99:        a.BaselineP99,
		Threshold:          a.Threshold,
		ErrorRate:          a.ErrorRate,
		ErrorRateThreshold: a.ErrorRateThreshold,
		CurrentRPS:         a.CurrentRPS,
		BaselineRPS:        a.BaselineRPS,
		DropPct:            a.DropPct,
		ZScore:             a.ZScore,
	}
}

// ── MultiNotifier ─────────────────────────────────────────────────────────────

// WebhookTarget configures one destination within a MultiNotifier.
type WebhookTarget struct {
	URL    string
	Format string   // "json" (default) | "slack"
	Events []string // ["fired"] | ["resolved"] | both; default ["fired"]
}

func (t WebhookTarget) wantsEvent(event string) bool {
	if len(t.Events) == 0 {
		return event == EventFired
	}
	for _, e := range t.Events {
		if e == event {
			return true
		}
	}
	return false
}

// MultiNotifier dispatches to multiple webhook targets.
type MultiNotifier struct {
	targets []webhookTarget
}

type webhookTarget struct {
	cfg    WebhookTarget
	client *http.Client
}

// NewMultiNotifier creates a MultiNotifier from a list of WebhookTarget configs.
func NewMultiNotifier(targets []WebhookTarget) *MultiNotifier {
	m := &MultiNotifier{}
	for _, t := range targets {
		m.targets = append(m.targets, webhookTarget{
			cfg:    t,
			client: &http.Client{Timeout: 5 * time.Second},
		})
	}
	return m
}

// NotifyFired dispatches a "fired" event to all subscribed targets.
func (m *MultiNotifier) NotifyFired(a Alert) { m.dispatch(EventFired, a, time.Time{}) }

// NotifyResolved dispatches a "resolved" event to all subscribed targets.
func (m *MultiNotifier) NotifyResolved(a Alert, resolvedAt time.Time) {
	m.dispatch(EventResolved, a, resolvedAt)
}

func (m *MultiNotifier) dispatch(event string, a Alert, resolvedAt time.Time) {
	ev := eventFromAlert(event, a)
	if !resolvedAt.IsZero() {
		ev.ResolvedAt = &resolvedAt
		ev.DurationMs = resolvedAt.Sub(a.TriggeredAt).Milliseconds()
	}
	for _, t := range m.targets {
		if !t.cfg.wantsEvent(event) {
			continue
		}
		t := t // capture
		go func() {
			if err := postWebhook(t.client, t.cfg.URL, t.cfg.Format, ev, a); err != nil {
				log.Printf("webhook: %s: %v", t.cfg.URL, err)
			}
		}()
	}
}

func postWebhook(client *http.Client, url, format string, ev WebhookEvent, a Alert) error {
	var body []byte
	var err error
	if format == "slack" {
		body, err = json.Marshal(map[string]string{"text": slackText(ev, a)})
	} else {
		body, err = json.Marshal(ev)
	}
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}

func slackText(ev WebhookEvent, a Alert) string {
	var sb strings.Builder
	if ev.Event == EventFired {
		sb.WriteString("🔴 *Alert fired*")
	} else {
		sb.WriteString("🟢 *Alert resolved*")
	}
	fmt.Fprintf(&sb, " — `%s` on `%s %s`\n", ev.Kind, ev.Method, ev.Path)
	switch ev.Event {
	case EventFired:
		switch a.Kind {
		case KindLatency:
			fmt.Fprintf(&sb, ">P99: %.1fms (baseline: %.1fms, threshold: %.1fx)\n", a.CurrentP99, a.BaselineP99, a.Threshold)
		case KindErrorRate:
			fmt.Fprintf(&sb, ">Error rate: %.1f%% (threshold: %.1f%%)\n", a.ErrorRate, a.ErrorRateThreshold)
		case KindThroughput:
			fmt.Fprintf(&sb, ">RPS: %.2f (baseline: %.2f, drop threshold: %.0f%%)\n", a.CurrentRPS, a.BaselineRPS, a.DropPct)
		case KindStatistical:
			fmt.Fprintf(&sb, ">Z-score: %.2f (P99: %.1fms, mean: %.1fms)\n", a.ZScore, a.CurrentP99, a.MeanP99)
		}
	case EventResolved:
		if ev.DurationMs > 0 {
			dur := time.Duration(ev.DurationMs) * time.Millisecond
			fmt.Fprintf(&sb, ">Duration: %s\n", dur.Round(time.Second))
		}
	}
	return sb.String()
}
