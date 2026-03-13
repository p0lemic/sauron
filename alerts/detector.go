package alerts

import (
	"sort"
	"sync"
	"time"

	"api-profiler/metrics"
)

const checkInterval = 10 * time.Second

// Alert describes an active latency anomaly for one endpoint.
type Alert struct {
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	CurrentP99  float64   `json:"current_p99"`
	BaselineP99 float64   `json:"baseline_p99"`
	Threshold   float64   `json:"threshold"`
	TriggeredAt time.Time `json:"triggered_at"`
}

// Detector runs a periodic background check comparing current P99 against the
// baseline. An alert is created when current_p99 > threshold * baseline_p99 and
// auto-resolved when the condition is no longer true.
type Detector struct {
	engine          *metrics.Engine
	threshold       float64
	baselineWindows int
	notifier        Notifier

	mu       sync.Mutex
	active   map[string]*Alert   // key: "METHOD:path"
	silences map[string]*Silence // key: "METHOD:path"
	history  []*AlertRecord

	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

// NewDetector creates a Detector backed by engine.
// threshold is the anomaly multiplier (e.g. 3.0).
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

// Silence suppresses alerts for the given endpoint until now+duration.
// Replaces any existing silence for that endpoint.
// The duration is not validated here; callers should ensure it is positive.
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

// Evaluate runs one detection cycle. It is exported so tests can trigger it
// directly without waiting for the ticker.
func (d *Detector) Evaluate() {
	current, err := d.engine.Endpoints()
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
	triggered := make(map[string]struct{}, len(current))
	var newAlerts []Alert

	d.mu.Lock()
	// Clean expired silences.
	for k, s := range d.silences {
		if !now.Before(s.ExpiresAt) {
			delete(d.silences, k)
		}
	}
	for _, ep := range current {
		bKey := epKey{ep.Method, ep.Path}
		baselineP99, hasBaseline := baselineMap[bKey]
		if !hasBaseline {
			continue
		}
		aKey := ep.Method + ":" + ep.Path
		// Skip silenced endpoints.
		if s, ok := d.silences[aKey]; ok && now.Before(s.ExpiresAt) {
			continue
		}
		if ep.P99 > d.threshold*baselineP99 {
			triggered[aKey] = struct{}{}
			_, wasActive := d.active[aKey]
			a := &Alert{
				Method:      ep.Method,
				Path:        ep.Path,
				CurrentP99:  ep.P99,
				BaselineP99: baselineP99,
				Threshold:   d.threshold,
				TriggeredAt: now,
			}
			d.active[aKey] = a
			if !wasActive {
				newAlerts = append(newAlerts, *a)
				rec := &AlertRecord{
					Method:      ep.Method,
					Path:        ep.Path,
					CurrentP99:  ep.P99,
					BaselineP99: baselineP99,
					Threshold:   d.threshold,
					TriggeredAt: now,
				}
				d.history = append(d.history, rec)
			}
		}
	}
	// Auto-resolve: remove alerts whose condition is no longer true.
	for k, a := range d.active {
		if _, ok := triggered[k]; !ok {
			// Stamp the most recent open history entry for this key.
			for i := len(d.history) - 1; i >= 0; i-- {
				rec := d.history[i]
				if rec.Method+":"+rec.Path == k && rec.ResolvedAt == nil {
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
