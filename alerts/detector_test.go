package alerts_test

import (
	"testing"
	"time"

	"api-profiler/alerts"
	"api-profiler/metrics"
	"api-profiler/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// splitReader returns currentRecords for queries whose 'to' is close to now
// (current window), and baselineRecords for older queries (baseline windows).
type splitReader struct {
	window          time.Duration
	currentRecords  []storage.Record
	baselineRecords []storage.Record
}

func (r *splitReader) FindByWindow(from, to time.Time) ([]storage.Record, error) {
	if time.Since(to) < r.window/2 {
		return r.currentRecords, nil
	}
	return r.baselineRecords, nil
}

func makeRecs(method, path string, durations []float64) []storage.Record {
	out := make([]storage.Record, len(durations))
	for i, d := range durations {
		out[i] = storage.Record{
			Method: method, Path: path,
			StatusCode: 200, DurationMs: d, Timestamp: time.Now(),
		}
	}
	return out
}

func newSplitDetector(threshold float64, currentDurations, baselineDurations []float64) (*alerts.Detector, *splitReader) {
	window := time.Minute
	reader := &splitReader{
		window:          window,
		currentRecords:  makeRecs("GET", "/x", currentDurations),
		baselineRecords: makeRecs("GET", "/x", baselineDurations),
	}
	engine := metrics.NewEngine(reader, window)
	return alerts.NewDetector(engine, threshold, 5), reader
}

// TC-06: current_p99 > threshold * baseline_p99 → alert appears in Active().
func TestDetectorFiresAlert(t *testing.T) {
	// current P99=400, baseline P99=100, threshold=3.0 → 400 > 300 → alert
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Evaluate()
	active := d.Active()
	require.Len(t, active, 1)
	assert.Equal(t, "GET", active[0].Method)
	assert.Equal(t, "/x", active[0].Path)
	assert.Greater(t, active[0].CurrentP99, active[0].Threshold*active[0].BaselineP99)
}

// TC-07: current_p99 <= threshold * baseline_p99 → Active() is empty.
func TestDetectorNoAlertBelowThreshold(t *testing.T) {
	// current P99=100, baseline P99=100, threshold=3.0 → 100 <= 300 → no alert
	d, _ := newSplitDetector(3.0, []float64{100}, []float64{100})
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-08: No baseline for endpoint → Active() is empty.
func TestDetectorNoAlertWithoutBaseline(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:          window,
		currentRecords:  makeRecs("GET", "/x", []float64{9999}),
		baselineRecords: nil, // no baseline records
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 3.0, 5)
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-09: Condition resolves → alert auto-removed from Active().
func TestDetectorAutoResolves(t *testing.T) {
	d, reader := newSplitDetector(3.0, []float64{400}, []float64{100})

	d.Evaluate() // triggers
	require.Len(t, d.Active(), 1)

	// Drop latency so condition no longer holds
	reader.currentRecords = makeRecs("GET", "/x", []float64{100})
	d.Evaluate() // resolves
	assert.Empty(t, d.Active())
}

// TC-10: Same condition on two Evaluate() calls → one alert, triggered_at updated.
func TestDetectorDeduplicatesAlert(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})

	d.Evaluate()
	first := d.Active()
	require.Len(t, first, 1)
	t1 := first[0].TriggeredAt

	time.Sleep(2 * time.Millisecond)
	d.Evaluate()
	second := d.Active()
	require.Len(t, second, 1)
	assert.False(t, second[0].TriggeredAt.Before(t1), "triggered_at should be updated")
}

// --- US-15: Notifier integration ---

// mockNotifier counts Notify calls.
type mockNotifier struct {
	calls []alerts.Alert
}

func (m *mockNotifier) Notify(a alerts.Alert) {
	m.calls = append(m.calls, a)
}

// TC-05: New alert → notifier called exactly once.
func TestDetectorNotifierCalledOnNewAlert(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	n := &mockNotifier{}
	d.SetNotifier(n)

	d.Evaluate()
	assert.Len(t, n.calls, 1)
	assert.Equal(t, "/x", n.calls[0].Path)
}

// TC-06: Alert already active on next tick → notifier NOT called again.
func TestDetectorNotifierNotCalledOnRepeat(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	n := &mockNotifier{}
	d.SetNotifier(n)

	d.Evaluate() // first: fires
	d.Evaluate() // second: same condition, no new alert
	assert.Len(t, n.calls, 1)
}

// TC-07: Alert resolved then re-triggered → notifier called again.
func TestDetectorNotifierCalledAfterResolve(t *testing.T) {
	d, reader := newSplitDetector(3.0, []float64{400}, []float64{100})
	n := &mockNotifier{}
	d.SetNotifier(n)

	d.Evaluate() // fires (call #1)
	reader.currentRecords = makeRecs("GET", "/x", []float64{100})
	d.Evaluate() // resolves
	reader.currentRecords = makeRecs("GET", "/x", []float64{400})
	d.Evaluate() // fires again (call #2)

	assert.Len(t, n.calls, 2)
}

// TC-08: No notifier (nil) → Evaluate() works without panic.
func TestDetectorNilNotifierNoPanic(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	// No SetNotifier call
	assert.NotPanics(t, func() { d.Evaluate() })
}

// TC-09: Endpoint without baseline → notifier not called.
func TestDetectorNotifierNotCalledWithoutBaseline(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:          window,
		currentRecords:  makeRecs("GET", "/x", []float64{9999}),
		baselineRecords: nil,
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 3.0, 5)
	n := &mockNotifier{}
	d.SetNotifier(n)

	d.Evaluate()
	assert.Empty(t, n.calls)
}

// --- US-17: History ---

// TC-01: Alert fires → History() has 1 entry, ResolvedAt nil.
func TestDetectorHistoryNewAlert(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Evaluate()
	h := d.History()
	require.Len(t, h, 1)
	assert.Equal(t, "GET", h[0].Method)
	assert.Equal(t, "/x", h[0].Path)
	assert.Nil(t, h[0].ResolvedAt)
}

// TC-02: Alert fires then resolves → ResolvedAt set.
func TestDetectorHistoryResolved(t *testing.T) {
	d, reader := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Evaluate()
	reader.currentRecords = makeRecs("GET", "/x", []float64{100})
	d.Evaluate()
	h := d.History()
	require.Len(t, h, 1)
	require.NotNil(t, h[0].ResolvedAt)
	assert.True(t, h[0].ResolvedAt.After(h[0].TriggeredAt) || h[0].ResolvedAt.Equal(h[0].TriggeredAt))
}

// TC-03: Alert fires, resolves, fires again → 2 entries.
func TestDetectorHistoryRefired(t *testing.T) {
	d, reader := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Evaluate()
	reader.currentRecords = makeRecs("GET", "/x", []float64{100})
	d.Evaluate()
	reader.currentRecords = makeRecs("GET", "/x", []float64{400})
	d.Evaluate()
	h := d.History()
	require.Len(t, h, 2)
	assert.Nil(t, h[0].ResolvedAt)       // most recent — still active
	assert.NotNil(t, h[1].ResolvedAt)    // older — resolved
}

// TC-04: No alerts → History() empty.
func TestDetectorHistoryEmpty(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{100}, []float64{100})
	d.Evaluate()
	assert.Empty(t, d.History())
}

// TC-05: Two endpoints fire → both in history.
func TestDetectorHistoryMultipleEndpoints(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window: window,
		currentRecords: []storage.Record{
			{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 400, Timestamp: time.Now()},
			{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 400, Timestamp: time.Now()},
		},
		baselineRecords: []storage.Record{
			{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
			{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		},
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 3.0, 5)
	d.Evaluate()
	assert.Len(t, d.History(), 2)
}

// --- US-16: Silence ---

// TC-01: Active silence → alert NOT in Active().
func TestDetectorSilenceSuppressesAlert(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Silence("GET", "/x", time.Hour)
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-02: Active silence → notifier NOT called.
func TestDetectorSilenceSuppressesNotifier(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	n := &mockNotifier{}
	d.SetNotifier(n)
	d.Silence("GET", "/x", time.Hour)
	d.Evaluate()
	assert.Empty(t, n.calls)
}

// TC-03: Expired silence → alert appears.
func TestDetectorExpiredSilenceAllowsAlert(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Silence("GET", "/x", -time.Millisecond) // already expired
	d.Evaluate()
	require.Len(t, d.Active(), 1)
}

// TC-04: Replacing silence → new duration applied.
func TestDetectorSilenceReplaced(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Silence("GET", "/x", time.Minute)
	s2 := d.Silence("GET", "/x", 2*time.Hour)
	assert.True(t, s2.ExpiresAt.After(time.Now().Add(time.Hour)))
	silences := d.ActiveSilences()
	require.Len(t, silences, 1)
	assert.Equal(t, s2.ExpiresAt.Unix(), silences[0].ExpiresAt.Unix())
}

// TC-05: ActiveSilences cleans expired entries.
func TestDetectorActiveSilencesCleansExpired(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{100}, []float64{100})
	d.Silence("GET", "/x", -time.Millisecond) // expired immediately
	d.Silence("GET", "/y", time.Hour)          // still active
	silences := d.ActiveSilences()
	require.Len(t, silences, 1)
	assert.Equal(t, "/y", silences[0].Path)
}

// TC-06: Alert active when silence created → disappears on next Evaluate().
func TestDetectorSilenceRemovesExistingAlert(t *testing.T) {
	d, _ := newSplitDetector(3.0, []float64{400}, []float64{100})
	d.Evaluate()
	require.Len(t, d.Active(), 1)
	d.Silence("GET", "/x", time.Hour)
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-11: Two endpoints — only anomalous one appears in Active().
func TestDetectorOnlyAnomalousEndpoints(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window: window,
		currentRecords: []storage.Record{
			{Method: "GET", Path: "/slow", StatusCode: 200, DurationMs: 400, Timestamp: time.Now()},
			{Method: "GET", Path: "/ok", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
		},
		baselineRecords: []storage.Record{
			{Method: "GET", Path: "/slow", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
			{Method: "GET", Path: "/ok", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		},
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 3.0, 5)
	d.Evaluate()

	active := d.Active()
	require.Len(t, active, 1)
	assert.Equal(t, "/slow", active[0].Path)
}
