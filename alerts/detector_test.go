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

func (r *splitReader) FindRecent(_ time.Time, _ time.Time, limit int) ([]storage.Record, error) {
	out := r.currentRecords
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (r *splitReader) FindByTraceID(traceID string) ([]storage.Record, error) {
	var out []storage.Record
	for _, rec := range r.currentRecords {
		if rec.TraceID == traceID {
			out = append(out, rec)
		}
	}
	if out == nil {
		out = []storage.Record{}
	}
	return out, nil
}

func (r *splitReader) FindSpansByTraceID(_ string) ([]storage.InnerSpan, error) {
	return []storage.InnerSpan{}, nil
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

// --- US-40: Error rate alerts ---

// helper: reader with error records for error rate tests.
func makeErrRecs(method, path string, total, errors int) []storage.Record {
	out := make([]storage.Record, total)
	for i := range out {
		sc := 200
		if i < errors {
			sc = 500
		}
		out[i] = storage.Record{Method: method, Path: path, StatusCode: sc, DurationMs: 10, Timestamp: time.Now()}
	}
	return out
}

// TC-01 (US-40): ErrorRateThreshold=0 → disabled, no alert even at 100% errors.
func TestErrorRateThresholdZeroDisabled(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:         window,
		currentRecords: makeErrRecs("GET", "/x", 10, 10), // 100% errors
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 5) // latency threshold very high to ignore
	// SetErrorRateThreshold NOT called → defaults to 0
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-02 (US-40): error_rate > threshold → alert with kind="error_rate".
func TestErrorRateAlertFires(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:         window,
		currentRecords: makeErrRecs("GET", "/x", 10, 5), // 50% errors
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 5)
	d.SetErrorRateThreshold(10.0) // threshold 10%
	d.Evaluate()

	active := d.Active()
	require.Len(t, active, 1)
	assert.Equal(t, alerts.KindErrorRate, active[0].Kind)
	assert.Equal(t, "GET", active[0].Method)
	assert.Equal(t, "/x", active[0].Path)
	assert.Greater(t, active[0].ErrorRate, active[0].ErrorRateThreshold)
}

// TC-03 (US-40): error_rate <= threshold → no alert.
func TestErrorRateAlertBelowThreshold(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:         window,
		currentRecords: makeErrRecs("GET", "/x", 10, 1), // 10% errors
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 5)
	d.SetErrorRateThreshold(10.0) // exactly 10% → NOT > 10%, no alert
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-04 (US-40): both latency and error_rate conditions → 2 alerts for same endpoint.
func TestBothLatencyAndErrorRateAlerts(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window: window,
		currentRecords: []storage.Record{
			{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 400, Timestamp: time.Now()},
			{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 400, Timestamp: time.Now()},
			{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 400, Timestamp: time.Now()},
		},
		baselineRecords: []storage.Record{
			{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		},
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 3.0, 5)
	d.SetErrorRateThreshold(10.0) // 66% > 10% → error_rate alert
	d.Evaluate()

	active := d.Active()
	require.Len(t, active, 2)
	kinds := map[string]bool{active[0].Kind: true, active[1].Kind: true}
	assert.True(t, kinds[alerts.KindLatency])
	assert.True(t, kinds[alerts.KindErrorRate])
}

// TC-05 (US-40): error rate drops below threshold → error_rate alert auto-resolved.
func TestErrorRateAlertAutoResolves(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:         window,
		currentRecords: makeErrRecs("GET", "/x", 10, 5), // 50% errors
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 5)
	d.SetErrorRateThreshold(10.0)
	d.Evaluate()
	require.Len(t, d.Active(), 1)

	reader.currentRecords = makeErrRecs("GET", "/x", 10, 0) // 0% errors
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// --- US-41: Throughput drop alerts ---

// makeTpRecs builds records with fixed duration to control RPS (count / window).
func makeTpRecs(method, path string, count int) []storage.Record {
	out := make([]storage.Record, count)
	for i := range out {
		out[i] = storage.Record{Method: method, Path: path, StatusCode: 200, DurationMs: 10, Timestamp: time.Now()}
	}
	return out
}

// newTpDetector builds a detector with high latency/error thresholds so only
// the throughput condition is under test.
func newTpDetector(dropPct float64, currentCount, baselineCount int) (*alerts.Detector, *splitReader) {
	window := time.Minute
	reader := &splitReader{
		window:          window,
		currentRecords:  makeTpRecs("GET", "/x", currentCount),
		baselineRecords: makeTpRecs("GET", "/x", baselineCount),
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 1) // latency threshold very high
	d.SetThroughputDropThreshold(dropPct)
	return d, reader
}

// TC-01 (US-41): ThroughputDropThreshold=0 → disabled.
func TestThroughputThresholdZeroDisabled(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:          window,
		currentRecords:  makeTpRecs("GET", "/x", 1),
		baselineRecords: makeTpRecs("GET", "/x", 100),
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 1)
	// SetThroughputDropThreshold NOT called → 0
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-02 (US-41): current RPS < threshold% of baseline → alert kind="throughput".
func TestThroughputDropAlertFires(t *testing.T) {
	// baseline=100 reqs/min → ~1.67 RPS; current=10 reqs → ~0.17 RPS
	// threshold=50: alert when current < 50% of baseline (0.83 RPS) → fires
	d, _ := newTpDetector(50.0, 10, 100)
	d.Evaluate()

	active := d.Active()
	require.Len(t, active, 1)
	assert.Equal(t, alerts.KindThroughput, active[0].Kind)
	assert.Equal(t, "GET", active[0].Method)
	assert.Less(t, active[0].CurrentRPS, active[0].BaselineRPS*active[0].DropPct/100)
}

// TC-03 (US-41): current RPS >= threshold% of baseline → no alert.
func TestThroughputDropNoAlertAboveThreshold(t *testing.T) {
	// baseline=100, current=80, threshold=50: 80/60 >= (100/60)*0.5 → no alert
	d, _ := newTpDetector(50.0, 80, 100)
	d.Evaluate()
	assert.Empty(t, d.Active())
}

// TC-04 (US-41): endpoint disappears from current traffic (0 RPS) → alert.
func TestThroughputDropZeroCurrentRPS(t *testing.T) {
	window := time.Minute
	reader := &splitReader{
		window:          window,
		currentRecords:  nil, // no traffic at all
		baselineRecords: makeTpRecs("GET", "/x", 100),
	}
	engine := metrics.NewEngine(reader, window)
	d := alerts.NewDetector(engine, 999.0, 1)
	d.SetThroughputDropThreshold(50.0)
	d.Evaluate()

	active := d.Active()
	require.Len(t, active, 1)
	assert.Equal(t, alerts.KindThroughput, active[0].Kind)
	assert.Equal(t, float64(0), active[0].CurrentRPS)
}

// TC-05 (US-41): RPS recovers → throughput alert auto-resolved.
func TestThroughputDropAutoResolves(t *testing.T) {
	d, reader := newTpDetector(50.0, 10, 100)
	d.Evaluate()
	require.Len(t, d.Active(), 1)

	reader.currentRecords = makeTpRecs("GET", "/x", 80) // back above threshold
	d.Evaluate()
	assert.Empty(t, d.Active())
}
