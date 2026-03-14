package alerts

import (
	"sort"
	"sync"
	"time"

	"api-profiler/metrics"
)

const checkInterval = 10 * time.Second

const (
	KindLatency    = "latency"
	KindErrorRate  = "error_rate"
	KindThroughput = "throughput"
)

// Alert describes an active anomaly for one endpoint.
type Alert struct {
	Kind               string    `json:"kind"` // "latency" | "error_rate"
	Method             string    `json:"method"`
	Path               string    `json:"path"`
	// Latency-specific (zero for error_rate alerts).
	CurrentP99         float64   `json:"current_p99"`
	BaselineP99        float64   `json:"baseline_p99"`
	Threshold          float64   `json:"threshold"`
	// Error-rate-specific (zero for latency/throughput alerts).
	ErrorRate          float64   `json:"error_rate"`
	ErrorRateThreshold float64   `json:"error_rate_threshold"`
	// Throughput-specific (zero for latency/error_rate alerts).
	CurrentRPS  float64   `json:"current_rps"`
	BaselineRPS float64   `json:"baseline_rps"`
	DropPct     float64   `json:"drop_pct"` // configured threshold
	TriggeredAt time.Time `json:"triggered_at"`
}

// Detector runs a periodic background check comparing current P99 against the
// baseline and current error rate against the configured threshold.
// Alerts are created when conditions are met and auto-resolved when they clear.
type Detector struct {
	engine                  *metrics.Engine
	threshold               float64
	baselineWindows         int
	errorRateThreshold      float64
	throughputDropThreshold float64
	notifier                Notifier

	mu       sync.Mutex
	active   map[string]*Alert   // key: "kind:METHOD:path"
	silences map[string]*Silence // key: "METHOD:path"
	history  []*AlertRecord

	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

// NewDetector creates a Detector backed by engine.
// threshold is the latency anomaly multiplier (e.g. 3.0).
// baselineWindows is the number of past windows used for baseline computation.
func NewDetector(engine *metrics.Engine, threshold float64, baselineWindows int) *Detector {
	return &Detector{
		engine:          engine,
		threshold:       threshold,
		baselineWindows: baselineWindows,
		active:          make(map[string]*Alert),
		silences:        make(map[string]*Silence),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
	}
}

// SetNotifier registers a Notifier to be called when a new alert is created.
// Must be called before Start(). Safe to call with nil to clear the notifier.
func (d *Detector) SetNotifier(n Notifier) {
	d.notifier = n
}

// SetErrorRateThreshold sets the error rate percentage that triggers an alert.
// pct is a percentage value (e.g. 10.0 = 10%). Zero disables error rate alerts.
// Must be called before Start().
func (d *Detector) SetErrorRateThreshold(pct float64) {
	d.errorRateThreshold = pct
}

// SetThroughputDropThreshold sets the minimum RPS percentage of baseline that
// must be maintained to avoid a throughput alert. pct is a percentage value
// (e.g. 50.0 means alert when current RPS drops below 50% of baseline).
// Zero disables throughput drop alerts. Must be called before Start().
func (d *Detector) SetThroughputDropThreshold(pct float64) {
	d.throughputDropThreshold = pct
}

// Start launches the background evaluation goroutine.
func (d *Detector) Start() {
	go func() {
		defer close(d.doneCh)
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.Evaluate()
			case <-d.stopCh:
				return
			}
		}
	}()
}

// Stop shuts down the background goroutine gracefully. Safe to call once.
func (d *Detector) Stop() {
	d.once.Do(func() { close(d.stopCh) })
	<-d.doneCh
}

// Active returns a snapshot of currently active alerts, sorted by TriggeredAt desc.
func (d *Detector) Active() []Alert {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Alert, 0, len(d.active))
	for _, a := range d.active {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TriggeredAt.After(out[j].TriggeredAt)
	})
	return out
}

// Silence suppresses all alert kinds for the given endpoint until now+duration.
// Replaces any existing silence for that endpoint.
func (d *Detector) Silence(method, path string, duration time.Duration) Silence {
	key := method + ":" + path
	s := Silence{
		Method:    method,
		Path:      path,
		ExpiresAt: time.Now().Add(duration),
	}
	d.mu.Lock()
	d.silences[key] = &s
	d.mu.Unlock()
	return s
}

// ActiveSilences returns non-expired silences sorted by ExpiresAt ascending.
// Expired silences are cleaned up as a side effect.
func (d *Detector) ActiveSilences() []Silence {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, s := range d.silences {
		if !now.Before(s.ExpiresAt) {
			delete(d.silences, k)
		}
	}
	out := make([]Silence, 0, len(d.silences))
	for _, s := range d.silences {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ExpiresAt.Before(out[j].ExpiresAt)
	})
	return out
}

// History returns a snapshot of all alert records, sorted by TriggeredAt desc.
func (d *Detector) History() []AlertRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]AlertRecord, len(d.history))
	for i, r := range d.history {
		out[i] = *r
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TriggeredAt.After(out[j].TriggeredAt)
	})
	return out
}

// Evaluate runs one detection cycle. Exported so tests can trigger it directly.
func (d *Detector) Evaluate() {
	current, err := d.engine.Table()
	if err != nil {
		return
	}
	baselines, err := d.engine.Baseline(d.baselineWindows)
	if err != nil {
		return
	}

	type epKey struct{ method, path string }
	baselineMap := make(map[epKey]float64, len(baselines))
	for _, b := range baselines {
		baselineMap[epKey{b.Method, b.Path}] = b.BaselineP99
	}

	now := time.Now()
	triggered := make(map[string]struct{})
	var newAlerts []Alert

	d.mu.Lock()
	// Clean expired silences.
	for k, s := range d.silences {
		if !now.Before(s.ExpiresAt) {
			delete(d.silences, k)
		}
	}

	for _, ep := range current {
		silenceKey := ep.Method + ":" + ep.Path
		silenced := false
		if s, ok := d.silences[silenceKey]; ok && now.Before(s.ExpiresAt) {
			silenced = true
		}

		// ── Latency check ────────────────────────────────────────────────
		bKey := epKey{ep.Method, ep.Path}
		baselineP99, hasBaseline := baselineMap[bKey]
		if hasBaseline && !silenced && ep.P99 > d.threshold*baselineP99 {
			latKey := KindLatency + ":" + ep.Method + ":" + ep.Path
			triggered[latKey] = struct{}{}
			_, wasActive := d.active[latKey]
			a := &Alert{
				Kind:        KindLatency,
				Method:      ep.Method,
				Path:        ep.Path,
				CurrentP99:  ep.P99,
				BaselineP99: baselineP99,
				Threshold:   d.threshold,
				TriggeredAt: now,
			}
			d.active[latKey] = a
			if !wasActive {
				newAlerts = append(newAlerts, *a)
				d.history = append(d.history, &AlertRecord{
					Kind:        KindLatency,
					Method:      ep.Method,
					Path:        ep.Path,
					CurrentP99:  ep.P99,
					BaselineP99: baselineP99,
					Threshold:   d.threshold,
					TriggeredAt: now,
				})
			}
		}

		// ── Error rate check ─────────────────────────────────────────────
		if d.errorRateThreshold > 0 && !silenced && ep.ErrorRate > d.errorRateThreshold {
			errKey := KindErrorRate + ":" + ep.Method + ":" + ep.Path
			triggered[errKey] = struct{}{}
			_, wasActive := d.active[errKey]
			a := &Alert{
				Kind:               KindErrorRate,
				Method:             ep.Method,
				Path:               ep.Path,
				ErrorRate:          ep.ErrorRate,
				ErrorRateThreshold: d.errorRateThreshold,
				TriggeredAt:        now,
			}
			d.active[errKey] = a
			if !wasActive {
				newAlerts = append(newAlerts, *a)
				d.history = append(d.history, &AlertRecord{
					Kind:               KindErrorRate,
					Method:             ep.Method,
					Path:               ep.Path,
					ErrorRate:          ep.ErrorRate,
					ErrorRateThreshold: d.errorRateThreshold,
					TriggeredAt:        now,
				})
			}
		}
	}

	// ── Throughput drop check ────────────────────────────────────────────────
	if d.throughputDropThreshold > 0 {
		throughput, tErr := d.engine.Throughput()
		if tErr == nil {
			type epKey struct{ method, path string }
			currentRPSMap := make(map[epKey]float64, len(throughput))
			for _, t := range throughput {
				currentRPSMap[epKey{t.Method, t.Path}] = t.RPSAvg
			}
			for _, b := range baselines {
				if b.BaselineRPS == 0 {
					continue
				}
				silenceKey := b.Method + ":" + b.Path
				if s, ok := d.silences[silenceKey]; ok && now.Before(s.ExpiresAt) {
					continue
				}
				currentRPS := currentRPSMap[epKey{b.Method, b.Path}]
				if currentRPS < b.BaselineRPS*d.throughputDropThreshold/100 {
					tpKey := KindThroughput + ":" + b.Method + ":" + b.Path
					triggered[tpKey] = struct{}{}
					_, wasActive := d.active[tpKey]
					a := &Alert{
						Kind:        KindThroughput,
						Method:      b.Method,
						Path:        b.Path,
						CurrentRPS:  currentRPS,
						BaselineRPS: b.BaselineRPS,
						DropPct:     d.throughputDropThreshold,
						TriggeredAt: now,
					}
					d.active[tpKey] = a
					if !wasActive {
						newAlerts = append(newAlerts, *a)
						d.history = append(d.history, &AlertRecord{
							Kind:        KindThroughput,
							Method:      b.Method,
							Path:        b.Path,
							CurrentRPS:  currentRPS,
							BaselineRPS: b.BaselineRPS,
							DropPct:     d.throughputDropThreshold,
							TriggeredAt: now,
						})
					}
				}
			}
		}
	}

	// Auto-resolve: remove alerts whose condition is no longer true.
	for k, a := range d.active {
		if _, ok := triggered[k]; !ok {
			for i := len(d.history) - 1; i >= 0; i-- {
				rec := d.history[i]
				if rec.Kind+":"+rec.Method+":"+rec.Path == k && rec.ResolvedAt == nil {
					t := now
					if a.TriggeredAt.After(now) {
						t = a.TriggeredAt
					}
					rec.ResolvedAt = &t
					break
				}
			}
			delete(d.active, k)
		}
	}
	d.mu.Unlock()

	// Fire notifier outside the lock to avoid holding it during I/O.
	if d.notifier != nil {
		for _, a := range newAlerts {
			d.notifier.Notify(a)
		}
	}
}
