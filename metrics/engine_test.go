package metrics_test

import (
	"sync"
	"testing"
	"time"

	"api-profiler/metrics"
	"api-profiler/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockReader implements storage.Reader with in-memory records.
type mockReader struct {
	mu        sync.Mutex
	records   []storage.Record
	lastFrom  time.Time
	lastTo    time.Time
}

func (m *mockReader) FindByWindow(from, to time.Time) ([]storage.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastFrom = from
	m.lastTo = to
	return m.records, nil
}

func (m *mockReader) FindRecent(_ time.Time, _ time.Time, limit int) ([]storage.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.records
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (m *mockReader) captured() (time.Time, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastFrom, m.lastTo
}

func rec(method, path string, durationMs float64) storage.Record {
	return storage.Record{
		Timestamp:  time.Now(),
		Method:     method,
		Path:       path,
		StatusCode: 200,
		DurationMs: durationMs,
	}
}

func newEngine(records []storage.Record, window time.Duration) *metrics.Engine {
	return metrics.NewEngine(&mockReader{records: records}, window)
}

// TC-01: No records → empty slice, no error.
func TestEndpointsEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Len(t, stats, 0)
}

// TC-02: One record → p50 = p95 = p99 = that value; count = 1.
func TestEndpointsSingleRecord(t *testing.T) {
	e := newEngine([]storage.Record{rec("GET", "/x", 42)}, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	s := stats[0]
	assert.Equal(t, 42.0, s.P50)
	assert.Equal(t, 42.0, s.P95)
	assert.Equal(t, 42.0, s.P99)
	assert.Equal(t, 1, s.Count)
}

// TC-03: 100 records [1..100] → p50=50, p95=95, p99=99 (nearest rank, exact).
func TestEndpointsPercentiles100Values(t *testing.T) {
	records := make([]storage.Record, 100)
	for i := 0; i < 100; i++ {
		records[i] = rec("GET", "/api", float64(i+1))
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	s := stats[0]
	assert.Equal(t, 50.0, s.P50)
	assert.Equal(t, 95.0, s.P95)
	assert.Equal(t, 99.0, s.P99)
	assert.Equal(t, 100, s.Count)
}

// TC-04: Same path, different methods → two independent EndpointStat entries.
func TestEndpointsGroupsByMethodAndPath(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/users", 10),
		rec("GET", "/users", 20),
		rec("POST", "/users", 100),
		rec("POST", "/users", 200),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	require.Len(t, stats, 2)

	byMethod := map[string]metrics.EndpointStat{}
	for _, s := range stats {
		byMethod[s.Method] = s
	}
	assert.Equal(t, 2, byMethod["GET"].Count)
	assert.Equal(t, 2, byMethod["POST"].Count)
	assert.Greater(t, byMethod["POST"].P99, byMethod["GET"].P99)
}

// TC-05: Same method, different paths → two independent entries.
func TestEndpointsGroupsByPath(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/users", 10),
		rec("GET", "/orders", 50),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	assert.Len(t, stats, 2)
}

// TC-08: Results ordered by P99 descending.
func TestEndpointsSortedByP99Desc(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/fast", 1),
		rec("GET", "/fast", 2),
		rec("GET", "/slow", 100),
		rec("GET", "/slow", 200),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, "/slow", stats[0].Path)
	assert.Equal(t, "/fast", stats[1].Path)
}

// TC-09: Window is passed correctly to the reader.
func TestEndpointsWindowPassedToReader(t *testing.T) {
	reader := &mockReader{}
	window := 5 * time.Second
	e := metrics.NewEngine(reader, window)

	before := time.Now()
	_, err := e.Endpoints()
	after := time.Now()
	require.NoError(t, err)

	from, to := reader.captured()
	assert.WithinDuration(t, before.Add(-window), from, 100*time.Millisecond)
	assert.WithinDuration(t, after, to, 100*time.Millisecond)
}

// TC-15: Single record per group → p50 = p95 = p99.
func TestEndpointsSingleRecordPerGroup(t *testing.T) {
	e := newEngine([]storage.Record{rec("DELETE", "/item", 7.5)}, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	s := stats[0]
	assert.Equal(t, s.P50, s.P95)
	assert.Equal(t, s.P95, s.P99)
}

// TC-16: Two records [10, 100] → p50=10, p99=100.
func TestEndpointsTwoRecords(t *testing.T) {
	e := newEngine([]storage.Record{rec("GET", "/x", 10), rec("GET", "/x", 100)}, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	s := stats[0]
	assert.Equal(t, 10.0, s.P50)
	assert.Equal(t, 100.0, s.P99)
}

// TC-17: Identical latencies → p50 = p95 = p99.
func TestEndpointsIdenticalLatencies(t *testing.T) {
	records := []storage.Record{rec("GET", "/x", 5), rec("GET", "/x", 5), rec("GET", "/x", 5)}
	e := newEngine(records, time.Minute)
	stats, err := e.Endpoints()
	require.NoError(t, err)
	s := stats[0]
	assert.Equal(t, 5.0, s.P50)
	assert.Equal(t, 5.0, s.P95)
	assert.Equal(t, 5.0, s.P99)
}

// --- US-13: Baseline ---

// TC-07: No records → empty non-nil slice, no error.
func TestBaselineEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Baseline(5)
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Len(t, stats, 0)
}

// TC-08: Records present → correct baseline_p99 and sample_count.
func TestBaselineComputed(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/x", 10), rec("GET", "/x", 50), rec("GET", "/x", 100),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Baseline(5)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 3, stats[0].SampleCount)
	assert.Equal(t, 100.0, stats[0].BaselineP99)
}

// TC-09: Time range passed to reader: from ≈ now-(n+1)*window, to ≈ now-window.
func TestBaselineTimeRange(t *testing.T) {
	reader := &mockReader{}
	window := time.Minute
	n := 5
	e := metrics.NewEngine(reader, window)

	before := time.Now()
	_, err := e.Baseline(n)
	after := time.Now()
	require.NoError(t, err)

	from, to := reader.captured()
	assert.WithinDuration(t, before.Add(-time.Duration(n+1)*window), from, 200*time.Millisecond)
	assert.WithinDuration(t, after.Add(-window), to, 200*time.Millisecond)
}

// TC-10: Two endpoints → sorted by baseline_p99 desc.
func TestBaselineSortedByP99Desc(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/fast", 10), rec("GET", "/slow", 500),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Baseline(5)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, "/slow", stats[0].Path)
	assert.Equal(t, "/fast", stats[1].Path)
}

// TC-11: sample_count reflects all records in the group.
func TestBaselineSampleCount(t *testing.T) {
	records := make([]storage.Record, 20)
	for i := range records {
		records[i] = rec("POST", "/x", float64(i+1))
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Baseline(5)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 20, stats[0].SampleCount)
}

// TC-06 (US-41): Baseline() returns BaselineRPS > 0 when records exist.
func TestBaselineRPS(t *testing.T) {
	// 60 records, window=1min, n=1 → BaselineRPS = 60 / (1*60) = 1.0
	records := make([]storage.Record, 60)
	for i := range records {
		records[i] = rec("GET", "/x", 10)
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Baseline(1)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.InDelta(t, 1.0, stats[0].BaselineRPS, 0.01)
}

// TC-07 (US-41): Baseline() with no records → BaselineRPS = 0.
func TestBaselineRPSEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Baseline(5)
	require.NoError(t, err)
	assert.Empty(t, stats)
}

// --- US-11: Throughput ---

func recsAt(method, path string, n int, ts time.Time) []storage.Record {
	out := make([]storage.Record, n)
	for i := range out {
		out[i] = storage.Record{Method: method, Path: path, StatusCode: 200, DurationMs: 5, Timestamp: ts}
	}
	return out
}

// TC-01: No records → empty non-nil slice, no error.
func TestThroughputEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Throughput()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Len(t, stats, 0)
}

// TC-02: 60 req in 60s window, all within last 10s → rps_avg=1.0, rps_current=6.0.
func TestThroughputAllCurrent(t *testing.T) {
	recent := time.Now().Add(-5 * time.Second)
	records := recsAt("GET", "/x", 60, recent)
	e := newEngine(records, 60*time.Second)
	stats, err := e.Throughput()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.InDelta(t, 1.0, stats[0].RPSAvg, 0.001)
	assert.InDelta(t, 6.0, stats[0].RPSCurrent, 0.001)
	assert.Equal(t, 60, stats[0].TotalCount)
}

// TC-03: 60 req in 60s window, none in last 10s → rps_avg=1.0, rps_current=0.0.
func TestThroughputNoneCurrent(t *testing.T) {
	old := time.Now().Add(-30 * time.Second)
	records := recsAt("GET", "/x", 60, old)
	e := newEngine(records, 60*time.Second)
	stats, err := e.Throughput()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.InDelta(t, 1.0, stats[0].RPSAvg, 0.001)
	assert.InDelta(t, 0.0, stats[0].RPSCurrent, 0.001)
}

// TC-04: 20 req at 30s ago + 10 req at 5s ago → rps_avg=0.5, rps_current=1.0.
func TestThroughputMixed(t *testing.T) {
	old := recsAt("GET", "/x", 20, time.Now().Add(-30*time.Second))
	recent := recsAt("GET", "/x", 10, time.Now().Add(-5*time.Second))
	e := newEngine(append(old, recent...), 60*time.Second)
	stats, err := e.Throughput()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.InDelta(t, 0.5, stats[0].RPSAvg, 0.001)
	assert.InDelta(t, 1.0, stats[0].RPSCurrent, 0.001)
}

// TC-05: Two endpoints → sorted by rps_avg desc.
func TestThroughputSortedByRPSAvg(t *testing.T) {
	ts := time.Now().Add(-30 * time.Second)
	records := append(
		recsAt("GET", "/busy", 60, ts),
		recsAt("GET", "/quiet", 6, ts)...,
	)
	e := newEngine(records, 60*time.Second)
	stats, err := e.Throughput()
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, "/busy", stats[0].Path)
	assert.Equal(t, "/quiet", stats[1].Path)
}

// TC-06: total_count reflects all records in the window.
func TestThroughputTotalCount(t *testing.T) {
	ts := time.Now().Add(-30 * time.Second)
	e := newEngine(recsAt("POST", "/x", 42, ts), 60*time.Second)
	stats, err := e.Throughput()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 42, stats[0].TotalCount)
}

// --- US-10: Errors ---

func recWithStatus(method, path string, status int) storage.Record {
	return storage.Record{
		Timestamp: time.Now(), Method: method, Path: path,
		StatusCode: status, DurationMs: 10,
	}
}

// TC-01: No records → empty non-nil slice, no error.
func TestErrorsEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Len(t, stats, 0)
}

// TC-02: 2 OK + 2 error → error_rate=50, error_count=2, total_count=4.
func TestErrorsHalfErrors(t *testing.T) {
	records := []storage.Record{
		recWithStatus("GET", "/x", 200), recWithStatus("GET", "/x", 200),
		recWithStatus("GET", "/x", 500), recWithStatus("GET", "/x", 500),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 50.0, stats[0].ErrorRate)
	assert.Equal(t, 2, stats[0].ErrorCount)
	assert.Equal(t, 4, stats[0].TotalCount)
}

// TC-03: All 200 → error_rate=0.0, endpoint still included.
func TestErrorsAllOK(t *testing.T) {
	records := []storage.Record{
		recWithStatus("GET", "/x", 200), recWithStatus("GET", "/x", 201),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 0.0, stats[0].ErrorRate)
	assert.Equal(t, 0, stats[0].ErrorCount)
}

// TC-04: All 500 → error_rate=100.0.
func TestErrorsAllErrors(t *testing.T) {
	records := []storage.Record{
		recWithStatus("GET", "/x", 500), recWithStatus("GET", "/x", 503),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 100.0, stats[0].ErrorRate)
}

// TC-05: Status 400 counts as error.
func TestErrors4xxCounts(t *testing.T) {
	records := []storage.Record{
		recWithStatus("GET", "/x", 200), recWithStatus("GET", "/x", 400),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	assert.Equal(t, 1, stats[0].ErrorCount)
}

// TC-06: Status 399 does not count as error.
func TestErrors399NotAnError(t *testing.T) {
	records := []storage.Record{
		recWithStatus("GET", "/x", 200), recWithStatus("GET", "/x", 399),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	assert.Equal(t, 0, stats[0].ErrorCount)
}

// TC-07: Two endpoints — sorted by error_rate desc.
func TestErrorsSortedByRateDesc(t *testing.T) {
	records := []storage.Record{
		recWithStatus("GET", "/ok", 200), recWithStatus("GET", "/ok", 200),
		recWithStatus("GET", "/bad", 500), recWithStatus("GET", "/bad", 200),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, "/bad", stats[0].Path)
	assert.Equal(t, "/ok", stats[1].Path)
}

// TC-08: Tie in error_rate → higher total_count first.
func TestErrorsTieBreakByTotalCount(t *testing.T) {
	records := []storage.Record{
		// /a: 1/2 = 50%
		recWithStatus("GET", "/a", 500), recWithStatus("GET", "/a", 200),
		// /b: 2/4 = 50%
		recWithStatus("GET", "/b", 500), recWithStatus("GET", "/b", 500),
		recWithStatus("GET", "/b", 200), recWithStatus("GET", "/b", 200),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, "/b", stats[0].Path) // total_count=4 > 2
}

// TC-09: Mix of 4xx and 5xx — both counted.
func TestErrorsMix4xxAnd5xx(t *testing.T) {
	records := []storage.Record{
		recWithStatus("POST", "/x", 404),
		recWithStatus("POST", "/x", 500),
		recWithStatus("POST", "/x", 200),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Errors()
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 2, stats[0].ErrorCount)
	assert.InDelta(t, 66.67, stats[0].ErrorRate, 0.01)
}

// --- US-09: Slowest ---

// TC-01: n < total endpoints → returns only top n by P99.
func TestSlowestTopN(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/a", 10), rec("GET", "/b", 50),
		rec("GET", "/c", 30), rec("GET", "/d", 80),
		rec("GET", "/e", 20),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Slowest(3)
	require.NoError(t, err)
	require.Len(t, stats, 3)
	assert.Equal(t, "/d", stats[0].Path) // P99=80 first
	assert.Equal(t, "/b", stats[1].Path) // P99=50
	assert.Equal(t, "/c", stats[2].Path) // P99=30
}

// TC-02: n > total endpoints → returns all available.
func TestSlowestNExceedsTotal(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/a", 10), rec("GET", "/b", 50), rec("GET", "/c", 30),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Slowest(10)
	require.NoError(t, err)
	assert.Len(t, stats, 3)
}

// TC-03: No records → empty non-nil slice, no error.
func TestSlowestEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Slowest(5)
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Len(t, stats, 0)
}

// TC-04: n=1 → only the single slowest endpoint.
func TestSlowestN1(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/fast", 5), rec("GET", "/slow", 200),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Slowest(1)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, "/slow", stats[0].Path)
}

// TC-05: n == total → same result as Endpoints().
func TestSlowestNEqualsTotal(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/a", 10), rec("GET", "/b", 50), rec("GET", "/c", 30),
	}
	e := newEngine(records, time.Minute)
	all, _ := e.Endpoints()
	top, err := e.Slowest(3)
	require.NoError(t, err)
	assert.Equal(t, all, top)
}

// --- US-08: EndpointsForWindow ---

// TC-US08-01: EndpointsForWindow with same window as engine default returns same results.
func TestEndpointsForWindowSameAsDefault(t *testing.T) {
	records := []storage.Record{rec("GET", "/x", 42)}
	e := newEngine(records, time.Minute)
	got1, err1 := e.Endpoints()
	got2, err2 := e.EndpointsForWindow(time.Minute)
	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.Equal(t, got1, got2)
}

// TC-US08-02: EndpointsForWindow with shorter window: reader gets narrower [from, to).
func TestEndpointsForWindowShorterWindow(t *testing.T) {
	reader := &mockReader{records: []storage.Record{rec("GET", "/x", 10)}}
	e := metrics.NewEngine(reader, time.Minute)

	before := time.Now()
	_, err := e.EndpointsForWindow(5 * time.Second)
	after := time.Now()
	require.NoError(t, err)

	from, to := reader.captured()
	assert.WithinDuration(t, before.Add(-5*time.Second), from, 100*time.Millisecond)
	assert.WithinDuration(t, after, to, 100*time.Millisecond)
}

// TC-US08-03: EndpointsForWindow with longer window: reader gets wider [from, to).
func TestEndpointsForWindowLongerWindow(t *testing.T) {
	reader := &mockReader{records: []storage.Record{rec("GET", "/x", 10)}}
	e := metrics.NewEngine(reader, time.Minute)

	before := time.Now()
	_, err := e.EndpointsForWindow(10 * time.Minute)
	after := time.Now()
	require.NoError(t, err)

	from, to := reader.captured()
	assert.WithinDuration(t, before.Add(-10*time.Minute), from, 100*time.Millisecond)
	assert.WithinDuration(t, after, to, 100*time.Millisecond)
}

// TC-US08-04: Endpoints() delegates to EndpointsForWindow(e.window).
func TestEndpointsDelegatesToEndpointsForWindow(t *testing.T) {
	reader := &mockReader{records: []storage.Record{rec("GET", "/x", 10)}}
	window := 3 * time.Minute
	e := metrics.NewEngine(reader, window)

	before := time.Now()
	_, err := e.Endpoints()
	after := time.Now()
	require.NoError(t, err)

	from, to := reader.captured()
	assert.WithinDuration(t, before.Add(-window), from, 100*time.Millisecond)
	assert.WithinDuration(t, after, to, 100*time.Millisecond)
}

// TC-18: Concurrent Endpoints() calls do not race.
func TestEndpointsConcurrent(t *testing.T) {
	records := make([]storage.Record, 50)
	for i := range records {
		records[i] = rec("GET", "/x", float64(i))
	}
	e := newEngine(records, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Endpoints()
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}

// --- US-19: Summary ---

// TC-01: No data → all fields zero.
func TestSummaryEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	s, err := e.Summary()
	require.NoError(t, err)
	assert.Equal(t, 0, s.TotalRequests)
	assert.Equal(t, 0.0, s.GlobalErrorRate)
	assert.Equal(t, 0.0, s.GlobalP99)
	assert.Equal(t, 0, s.ActiveEndpoints)
}

// TC-02: Multiple endpoints, no errors → correct totals, error_rate 0.
func TestSummaryNoErrors(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/a", 100),
		rec("GET", "/a", 200),
		rec("POST", "/b", 50),
	}
	e := newEngine(records, time.Minute)
	s, err := e.Summary()
	require.NoError(t, err)
	assert.Equal(t, 3, s.TotalRequests)
	assert.Equal(t, 0.0, s.GlobalErrorRate)
	assert.Equal(t, 2, s.ActiveEndpoints)
}

// TC-03: Errors present → global_error_rate computed correctly.
func TestSummaryWithErrors(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	s, err := e.Summary()
	require.NoError(t, err)
	assert.Equal(t, 4, s.TotalRequests)
	assert.Equal(t, 50.0, s.GlobalErrorRate)
}

// TC-04: global_p99 = max P99 across all endpoints.
func TestSummaryGlobalP99IsMax(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/fast", 50),
		rec("GET", "/slow", 900),
		rec("GET", "/slow", 1000),
	}
	e := newEngine(records, time.Minute)
	s, err := e.Summary()
	require.NoError(t, err)
	assert.Equal(t, 2, s.ActiveEndpoints)
	assert.Equal(t, 1000.0, s.GlobalP99)
}

// --- US-20: Table ---

// TC-01: No data → empty slice.
func TestTableEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	rows, err := e.Table()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// TC-02: One endpoint, no errors → row with correct fields, error_rate 0.
func TestTableSingleEndpoint(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 200, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	rows, err := e.Table()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "GET", rows[0].Method)
	assert.Equal(t, "/x", rows[0].Path)
	assert.Equal(t, 2, rows[0].Count)
	assert.Equal(t, 0.0, rows[0].ErrorRate)
	assert.Greater(t, rows[0].P99, 0.0)
}

// TC-03: Endpoint with errors → error_rate set.
func TestTableWithErrors(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 50, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	rows, err := e.Table()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 50.0, rows[0].ErrorRate)
}

// TC-04: Two endpoints → sorted by P99 desc.
func TestTableSortedByP99(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/fast", 50),
		rec("GET", "/slow", 500),
		rec("GET", "/slow", 600),
	}
	e := newEngine(records, time.Minute)
	rows, err := e.Table()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "/slow", rows[0].Path)
	assert.Equal(t, "/fast", rows[1].Path)
}

// --- US-21: Latency ---

// TC-01: No data for endpoint → 60 buckets, all P99 = 0.
func TestLatencyEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	buckets, err := e.Latency("GET", "/x")
	require.NoError(t, err)
	require.Len(t, buckets, 60)
	for _, b := range buckets {
		assert.Equal(t, 0.0, b.P99)
	}
}

// TC-02: Records in one bucket → correct P99 in that bucket.
func TestLatencyBucketHasData(t *testing.T) {
	now := time.Now().Truncate(time.Minute)
	bucketTime := now.Add(-5 * time.Minute)
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 300, Timestamp: bucketTime.Add(10 * time.Second)},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 500, Timestamp: bucketTime.Add(20 * time.Second)},
	}
	e := newEngine(records, time.Minute)
	buckets, err := e.Latency("GET", "/x")
	require.NoError(t, err)
	require.Len(t, buckets, 60)

	found := false
	for _, b := range buckets {
		if b.Ts.Equal(bucketTime) {
			assert.Greater(t, b.P99, 0.0)
			found = true
		}
	}
	assert.True(t, found, "expected bucket not found")
}

// TC-03: Records for different endpoint → queried endpoint buckets all 0.
func TestLatencyOtherEndpointIgnored(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/other", StatusCode: 200, DurationMs: 999, Timestamp: time.Now().Add(-2 * time.Minute)},
	}
	e := newEngine(records, time.Minute)
	buckets, err := e.Latency("GET", "/x")
	require.NoError(t, err)
	for _, b := range buckets {
		assert.Equal(t, 0.0, b.P99)
	}
}

// TC-04: Always exactly 60 elements.
func TestLatencyAlways60Buckets(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/x", 100),
		rec("GET", "/x", 200),
	}
	e := newEngine(records, time.Minute)
	buckets, err := e.Latency("GET", "/x")
	require.NoError(t, err)
	assert.Len(t, buckets, 60)
}

// --- US-12: Histogram ---

// TC-01: No data → 9 buckets all count 0, total_count 0.
func TestHistogramEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	h, err := e.Histogram("", "")
	require.NoError(t, err)
	assert.Len(t, h.Buckets, 9)
	assert.Equal(t, 0, h.TotalCount)
	for _, b := range h.Buckets {
		assert.Equal(t, 0, b.Count)
	}
}

// TC-02: Record at 50ms → bucket ≤50 and all higher buckets incremented.
func TestHistogramCumulative(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	h, err := e.Histogram("", "")
	require.NoError(t, err)
	assert.Equal(t, 1, h.TotalCount)
	for _, b := range h.Buckets {
		if b.Le == -1 || b.Le >= 50 {
			assert.Equal(t, 1, b.Count, "le=%v should be 1", b.Le)
		} else {
			assert.Equal(t, 0, b.Count, "le=%v should be 0", b.Le)
		}
	}
}

// TC-03: Filter by endpoint → only counts that endpoint.
func TestHistogramFilterEndpoint(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
		{Method: "GET", Path: "/other", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	h, err := e.Histogram("GET", "/x")
	require.NoError(t, err)
	assert.Equal(t, 1, h.TotalCount)
}

// TC-04: No filter → all endpoints aggregated.
func TestHistogramNoFilterAggregates(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
		{Method: "POST", Path: "/b", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	h, err := e.Histogram("", "")
	require.NoError(t, err)
	assert.Equal(t, 2, h.TotalCount)
}

// TC-05: Always 9 buckets.
func TestHistogramAlways9Buckets(t *testing.T) {
	e := newEngine([]storage.Record{rec("GET", "/x", 300)}, time.Minute)
	h, err := e.Histogram("", "")
	require.NoError(t, err)
	assert.Len(t, h.Buckets, 9)
}

// TC-05 (US-27): Requests(n) returns up to n records.
func TestRequestsReturnsN(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/a", 10),
		rec("GET", "/b", 20),
		rec("GET", "/c", 30),
	}
	e := newEngine(records, time.Minute)
	got, err := e.Requests(2)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// TC-06 (US-27): Requests: n clamped to max 1000.
func TestRequestsNClampedToMax(t *testing.T) {
	records := make([]storage.Record, 5)
	for i := range records {
		records[i] = rec("GET", "/x", 1)
	}
	e := newEngine(records, time.Minute)
	got, err := e.Requests(9999)
	require.NoError(t, err)
	assert.Len(t, got, 5)
}

// TC-07 (US-27): RequestsForRange delegates correctly.
func TestRequestsForRange(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: now.Add(-30 * time.Second)},
	}
	e := newEngine(records, time.Minute)
	got, err := e.RequestsForRange(now.Add(-time.Minute), now, 10)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// TC-01 (US-28): Sin registros → 4 grupos, todos count=0 rate=0.0.
func TestStatusBreakdownEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	groups, err := e.StatusBreakdown()
	require.NoError(t, err)
	require.Len(t, groups, 4)
	for _, g := range groups {
		assert.Equal(t, 0, g.Count)
		assert.Equal(t, 0.0, g.Rate)
	}
	assert.Equal(t, "2xx", groups[0].Class)
	assert.Equal(t, "3xx", groups[1].Class)
	assert.Equal(t, "4xx", groups[2].Class)
	assert.Equal(t, "5xx", groups[3].Class)
}

// TC-02 (US-28): Registros mixtos → counts y rates correctos.
func TestStatusBreakdownMixed(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/a", StatusCode: 301, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/a", StatusCode: 404, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/a", StatusCode: 500, DurationMs: 1, Timestamp: now},
	}
	e := newEngine(records, time.Minute)
	groups, err := e.StatusBreakdown()
	require.NoError(t, err)
	require.Len(t, groups, 4)
	assert.Equal(t, 2, groups[0].Count) // 2xx
	assert.Equal(t, 1, groups[1].Count) // 3xx
	assert.Equal(t, 1, groups[2].Count) // 4xx
	assert.Equal(t, 1, groups[3].Count) // 5xx
	assert.InDelta(t, 40.0, groups[0].Rate, 0.05)
}

// TC-03 (US-28): Solo 2xx → rate=100%, resto 0%.
func TestStatusBreakdownOnly2xx(t *testing.T) {
	records := []storage.Record{
		{StatusCode: 200, DurationMs: 1, Timestamp: time.Now()},
		{StatusCode: 201, DurationMs: 1, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	groups, err := e.StatusBreakdown()
	require.NoError(t, err)
	assert.Equal(t, 100.0, groups[0].Rate)
	assert.Equal(t, 0.0, groups[1].Rate)
	assert.Equal(t, 0.0, groups[2].Rate)
	assert.Equal(t, 0.0, groups[3].Rate)
}

// TC-01 (US-32): StatusBreakdownForEndpoint filtra correctamente por method+path.
func TestStatusBreakdownForEndpoint(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/a", StatusCode: 500, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/b", StatusCode: 404, DurationMs: 1, Timestamp: now}, // different endpoint
	}
	e := newEngine(records, time.Minute)
	groups, err := e.StatusBreakdownForEndpoint("GET", "/a")
	require.NoError(t, err)
	require.Len(t, groups, 4)
	assert.Equal(t, 1, groups[0].Count) // 2xx
	assert.Equal(t, 0, groups[2].Count) // 4xx — /b not counted
	assert.Equal(t, 1, groups[3].Count) // 5xx
	assert.InDelta(t, 50.0, groups[0].Rate, 0.05)
}

// TC-02 (US-32): StatusBreakdownForEndpoint con endpoint sin registros → 4 grupos count=0.
func TestStatusBreakdownForEndpointNoRecords(t *testing.T) {
	e := newEngine(nil, time.Minute)
	groups, err := e.StatusBreakdownForEndpoint("GET", "/missing")
	require.NoError(t, err)
	require.Len(t, groups, 4)
	for _, g := range groups {
		assert.Equal(t, 0, g.Count)
		assert.Equal(t, 0.0, g.Rate)
	}
}

// TC-01 (US-29): SlowestRequests devuelve registros ordenados por duration_ms DESC.
func TestSlowestRequestsOrder(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 500, Timestamp: time.Now()},
		{Method: "GET", Path: "/c", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	got, err := e.SlowestRequests(10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, 500.0, got[0].DurationMs)
	assert.Equal(t, 50.0, got[1].DurationMs)
	assert.Equal(t, 10.0, got[2].DurationMs)
}

// TC-02 (US-29): SlowestRequests respeta el límite n.
func TestSlowestRequestsLimit(t *testing.T) {
	records := []storage.Record{
		{DurationMs: 100, Timestamp: time.Now()},
		{DurationMs: 200, Timestamp: time.Now()},
		{DurationMs: 300, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	got, err := e.SlowestRequests(2)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, 300.0, got[0].DurationMs)
}

// TC-03 (US-29): SlowestRequests con n > total devuelve todos.
func TestSlowestRequestsNGreaterThanTotal(t *testing.T) {
	records := []storage.Record{
		{DurationMs: 10, Timestamp: time.Now()},
		{DurationMs: 20, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	got, err := e.SlowestRequests(50)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// TC-04 (US-29): SlowestRequests sin registros → slice vacío (no nil).
func TestSlowestRequestsEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	got, err := e.SlowestRequests(10)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Len(t, got, 0)
}

// TC-05 (US-29): n coartado a máximo 100.
func TestSlowestRequestsNClamp(t *testing.T) {
	records := make([]storage.Record, 5)
	for i := range records {
		records[i] = storage.Record{DurationMs: float64(i + 1), Timestamp: time.Now()}
	}
	e := newEngine(records, time.Minute)
	got, err := e.SlowestRequests(9999)
	require.NoError(t, err)
	assert.Len(t, got, 5) // clamped to 100, only 5 exist
}

// TC-04 (US-28): StatusBreakdownForRange respeta el rango.
func TestStatusBreakdownForRange(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{StatusCode: 200, DurationMs: 1, Timestamp: now.Add(-30 * time.Second)},
		{StatusCode: 500, DurationMs: 1, Timestamp: now.Add(-30 * time.Second)},
	}
	e := newEngine(records, time.Minute)
	groups, err := e.StatusBreakdownForRange(now.Add(-time.Minute), now)
	require.NoError(t, err)
	assert.Equal(t, 1, groups[0].Count) // 2xx
	assert.Equal(t, 1, groups[3].Count) // 5xx
}

// --- US-44: Apdex score ---

// TC-01: Cálculo básico correcto: 6 satisfied, 3 tolerating, 1 frustrated → 0.750.
func TestApdexBasicCalculation(t *testing.T) {
	const T = 500.0
	records := []storage.Record{
		rec("GET", "/api", 100), // satisfied
		rec("GET", "/api", 200), // satisfied
		rec("GET", "/api", 300), // satisfied
		rec("GET", "/api", 400), // satisfied
		rec("GET", "/api", 450), // satisfied
		rec("GET", "/api", 500), // satisfied (== T)
		rec("GET", "/api", 600), // tolerating
		rec("GET", "/api", 800), // tolerating
		rec("GET", "/api", 1999), // tolerating (<= 4T=2000)
		rec("GET", "/api", 2001), // frustrated (> 4T)
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Apdex(T)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	// (6 + 3/2) / 10 = 7.5/10 = 0.750
	assert.Equal(t, 0.75, stats[0].Apdex)
	assert.Equal(t, 6, stats[0].Satisfied)
	assert.Equal(t, 3, stats[0].Tolerating)
	assert.Equal(t, 1, stats[0].Frustrated)
	assert.Equal(t, 10, stats[0].Total)
}

// TC-02: Todos satisfied → Apdex 1.000.
func TestApdexAllSatisfied(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/x", 10),
		rec("GET", "/x", 20),
		rec("GET", "/x", 30),
		rec("GET", "/x", 40),
		rec("GET", "/x", 50),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Apdex(500)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 1.0, stats[0].Apdex)
}

// TC-03: Todos frustrated → Apdex 0.000.
func TestApdexAllFrustrated(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/x", 3000), // > 4*500=2000
		rec("GET", "/x", 5000),
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Apdex(500)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 0.0, stats[0].Apdex)
}

// TC-04: Sin registros → slice vacío.
func TestApdexEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	stats, err := e.Apdex(500)
	require.NoError(t, err)
	assert.Empty(t, stats)
}

// TC-05: Múltiples endpoints ordenados por Apdex ascendente.
func TestApdexSortedAscending(t *testing.T) {
	records := []storage.Record{
		rec("GET", "/good", 10),  // satisfied → apdex 1.0
		rec("GET", "/bad", 3000), // frustrated → apdex 0.0
		rec("GET", "/mid", 600),  // tolerating → apdex 0.5
	}
	e := newEngine(records, time.Minute)
	stats, err := e.Apdex(500)
	require.NoError(t, err)
	require.Len(t, stats, 3)
	assert.Equal(t, 0.0, stats[0].Apdex)   // /bad first
	assert.Equal(t, 0.5, stats[1].Apdex)   // /mid second
	assert.Equal(t, 1.0, stats[2].Apdex)   // /good last
}

// TC-06: ApdexForRange respeta el rango — registros fuera no cuentan.
func TestApdexForRange(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 3000, Timestamp: now.Add(-30 * time.Second)},
	}
	e := newEngine(records, time.Minute)
	// Rango que incluye los registros
	stats, err := e.ApdexForRange(500, now.Add(-time.Minute), now)
	require.NoError(t, err)
	require.Len(t, stats, 1)
	assert.Equal(t, 0.0, stats[0].Apdex)
}

// --- US-46: Error fingerprinting ---

// TC-01: Fingerprint básico — rate y percentiles correctos.
func TestErrorFingerprintsBasic(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/users/{id}", StatusCode: 503, DurationMs: 100, Timestamp: now},
		{Method: "GET", Path: "/users/{id}", StatusCode: 503, DurationMs: 200, Timestamp: now},
		{Method: "GET", Path: "/users/{id}", StatusCode: 503, DurationMs: 300, Timestamp: now},
		{Method: "GET", Path: "/users/{id}", StatusCode: 200, DurationMs: 50, Timestamp: now},
		{Method: "GET", Path: "/users/{id}", StatusCode: 200, DurationMs: 50, Timestamp: now},
	}
	e := newEngine(records, time.Minute)
	fps, err := e.ErrorFingerprints()
	require.NoError(t, err)
	require.Len(t, fps, 1)
	assert.Equal(t, "GET", fps[0].Method)
	assert.Equal(t, "/users/{id}", fps[0].Path)
	assert.Equal(t, 503, fps[0].StatusCode)
	assert.Equal(t, 3, fps[0].Count)
	assert.InDelta(t, 60.0, fps[0].Rate, 0.1) // 3/5 = 60%
	assert.Equal(t, 200.0, fps[0].P50Ms)
	assert.Equal(t, 300.0, fps[0].P95Ms)
}

// TC-02: Dos fingerprints distintos para el mismo endpoint, ordenados por count desc.
func TestErrorFingerprintsTwoForSameEndpoint(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/orders", StatusCode: 422, DurationMs: 10, Timestamp: now},
		{Method: "GET", Path: "/orders", StatusCode: 422, DurationMs: 10, Timestamp: now},
		{Method: "GET", Path: "/orders", StatusCode: 503, DurationMs: 10, Timestamp: now},
	}
	e := newEngine(records, time.Minute)
	fps, err := e.ErrorFingerprints()
	require.NoError(t, err)
	require.Len(t, fps, 2)
	// Sorted by count desc: 422 (2) then 503 (1).
	assert.Equal(t, 422, fps[0].StatusCode)
	assert.Equal(t, 503, fps[1].StatusCode)
}

// TC-03: Rate calculada sobre total del endpoint propio, no global.
func TestErrorFingerprintsRatePerEndpoint(t *testing.T) {
	now := time.Now()
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 500, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: now},
		// /b has 10 requests, none errors; should not affect /a's rate
		{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 1, Timestamp: now},
		{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 1, Timestamp: now},
	}
	e := newEngine(records, time.Minute)
	fps, err := e.ErrorFingerprints()
	require.NoError(t, err)
	require.Len(t, fps, 1)
	// /a has 1 error out of 2 total → rate = 50%
	assert.InDelta(t, 50.0, fps[0].Rate, 0.1)
}

// TC-04: is_new=true cuando first_seen está en el último 10% de la ventana.
func TestErrorFingerprintsIsNewTrue(t *testing.T) {
	now := time.Now()
	// Window of 60s, first_seen at 55s into the window (> 90% = 54s threshold)
	from := now.Add(-60 * time.Second)
	firstSeen := from.Add(55 * time.Second) // 55s into 60s window → after 90% (54s)
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 1, Timestamp: firstSeen},
	}
	e := newEngine(records, time.Minute)
	fps, err := e.ErrorFingerprintsForRange(from, now)
	require.NoError(t, err)
	require.Len(t, fps, 1)
	assert.True(t, fps[0].IsNew)
}

// TC-05: is_new=false cuando first_seen está al principio de la ventana.
func TestErrorFingerprintsIsNewFalse(t *testing.T) {
	now := time.Now()
	from := now.Add(-60 * time.Second)
	firstSeen := from.Add(5 * time.Second) // only 5s in → not new
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 1, Timestamp: firstSeen},
	}
	e := newEngine(records, time.Minute)
	fps, err := e.ErrorFingerprintsForRange(from, now)
	require.NoError(t, err)
	require.Len(t, fps, 1)
	assert.False(t, fps[0].IsNew)
}

// TC-06: Requests 2xx/3xx no generan fingerprints.
func TestErrorFingerprintsOnlyErrors(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 1, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 301, DurationMs: 1, Timestamp: time.Now()},
	}
	e := newEngine(records, time.Minute)
	fps, err := e.ErrorFingerprints()
	require.NoError(t, err)
	assert.Empty(t, fps)
}

// TC-07: Ventana vacía → slice vacío.
func TestErrorFingerprintsEmpty(t *testing.T) {
	e := newEngine(nil, time.Minute)
	fps, err := e.ErrorFingerprints()
	require.NoError(t, err)
	assert.Empty(t, fps)
}
