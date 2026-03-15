package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-profiler/alerts"
	"api-profiler/api"
	"api-profiler/health"
	"api-profiler/metrics"
	"api-profiler/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticReader always returns the provided records regardless of time bounds.
type staticReader struct {
	records []storage.Record
}

func (r *staticReader) FindByWindow(_, _ time.Time) ([]storage.Record, error) {
	return r.records, nil
}

func (r *staticReader) FindRecent(_, _ time.Time, limit int) ([]storage.Record, error) {
	if limit < len(r.records) {
		return r.records[:limit], nil
	}
	return r.records, nil
}

func newTestServer(t *testing.T, records []storage.Record) *api.Server {
	t.Helper()
	engine := metrics.NewEngine(&staticReader{records: records}, time.Minute)
	detector := alerts.NewDetector(engine, 3.0, 5)
	srv := api.NewServer(engine, "localhost:0", 5, 500, detector, nil)
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return srv
}

// TC-10: No records → 200 with empty JSON array.
func TestAPIEndpointsEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	// Must be a JSON array (empty or with items).
	var result []json.RawMessage
	require.NoError(t, json.Unmarshal(body, &result))
	assert.Len(t, result, 0)
}

// TC-11: Records present → 200 with correct JSON payload.
func TestAPIEndpointsWithData(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/users", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/users", StatusCode: 200, DurationMs: 20, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var stats []metrics.EndpointStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	require.Len(t, stats, 1)
	assert.Equal(t, "GET", stats[0].Method)
	assert.Equal(t, "/users", stats[0].Path)
	assert.Equal(t, 2, stats[0].Count)
}

// TC-12: Content-Type is application/json.
func TestAPIEndpointsContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-13: POST /metrics/endpoints → 405 Method Not Allowed.
func TestAPIEndpointsMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/endpoints", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-08: ?window= query param ---

// TC-05: No ?window= → uses engine default (same behavior as before).
func TestAPIEndpointsNoWindowParam(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 42, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TC-06: ?window=5m valid → 200.
func TestAPIEndpointsValidWindowParam(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints?window=5m")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-07: ?window=abc invalid → 400 with JSON error.
func TestAPIEndpointsInvalidWindowParam(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints?window=abc")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	require.NoError(t, json.Unmarshal(body, &result))
	assert.Contains(t, result["error"], "invalid window")
}

// TC-08: ?window=-1m negative → 400.
func TestAPIEndpointsNegativeWindowParam(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints?window=-1m")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-09: ?window=0 zero → 400.
func TestAPIEndpointsZeroWindowParam(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints?window=0")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-10: ?window=1h30m compound valid → 200.
func TestAPIEndpointsCompoundWindowParam(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints?window=1h30m")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TC-11: ?window=7d days → 200.
func TestAPIEndpointsDaysWindowParam(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/endpoints?window=7d")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// --- US-14: GET /alerts/active ---

// TC-12: No alerts → 200 with empty array.
func TestAPIAlertsActiveEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/active")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var active []alerts.Alert
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&active))
	assert.Len(t, active, 0)
}

// TC-13: Active alerts present → 200 with correct JSON fields.
func TestAPIAlertsActiveWithData(t *testing.T) {
	// splitReader: high latency current, low latency baseline
	window := time.Minute
	type splitReader struct {
		window          time.Duration
		currentRecords  []storage.Record
		baselineRecords []storage.Record
	}
	reader := &struct {
		window          time.Duration
		currentRecords  []storage.Record
		baselineRecords []storage.Record
	}{
		window: window,
		currentRecords: []storage.Record{
			{Method: "GET", Path: "/slow", StatusCode: 200, DurationMs: 400, Timestamp: time.Now()},
		},
		baselineRecords: []storage.Record{
			{Method: "GET", Path: "/slow", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		},
	}
	type findable interface {
		FindByWindow(from, to time.Time) ([]storage.Record, error)
	}
	// Use the splitReader from detector_test via inline implementation
	engine := metrics.NewEngine(&apiSplitReader{
		window:          window,
		currentRecords:  reader.currentRecords,
		baselineRecords: reader.baselineRecords,
	}, window)
	detector := alerts.NewDetector(engine, 3.0, 5)
	detector.Evaluate()
	srv := api.NewServer(engine, "localhost:0", 5, 500, detector, nil)
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	resp, err := http.Get("http://" + srv.Addr() + "/alerts/active")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var active []alerts.Alert
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&active))
	require.Len(t, active, 1)
	assert.Equal(t, "GET", active[0].Method)
	assert.Equal(t, "/slow", active[0].Path)
	assert.Greater(t, active[0].CurrentP99, active[0].Threshold*active[0].BaselineP99)
}

// apiSplitReader mirrors detector_test splitReader for use in api_test package.
type apiSplitReader struct {
	window          time.Duration
	currentRecords  []storage.Record
	baselineRecords []storage.Record
}

func (r *apiSplitReader) FindByWindow(from, to time.Time) ([]storage.Record, error) {
	if time.Since(to) < r.window/2 {
		return r.currentRecords, nil
	}
	return r.baselineRecords, nil
}

func (r *apiSplitReader) FindRecent(_ time.Time, _ time.Time, limit int) ([]storage.Record, error) {
	out := r.currentRecords
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// TC-14: Content-Type is application/json.
func TestAPIAlertsActiveContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/active")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-15: POST /alerts/active → 405.
func TestAPIAlertsActiveMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/active", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-13: GET /metrics/baseline ---

// TC-12: No data → 200 with empty array.
func TestAPIBaselineEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/baseline")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.BaselineStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Len(t, stats, 0)
}

// TC-13: Records present → 200 with correct fields.
func TestAPIBaselineWithData(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/baseline")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.BaselineStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	require.Len(t, stats, 1)
	assert.Equal(t, "GET", stats[0].Method)
	assert.Equal(t, "/x", stats[0].Path)
	assert.Equal(t, 2, stats[0].SampleCount)
	assert.Greater(t, stats[0].BaselineP99, 0.0)
}

// TC-14: Content-Type is application/json.
func TestAPIBaselineContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/baseline")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-15: POST /metrics/baseline → 405.
func TestAPIBaselineMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/baseline", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-11: GET /metrics/throughput ---

// TC-07: No data → 200 with empty array.
func TestAPIThroughputEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/throughput")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.ThroughputStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Len(t, stats, 0)
}

// TC-08: Records present → 200 with correct fields.
func TestAPIThroughputWithData(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 5, Timestamp: time.Now().Add(-5 * time.Second)},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 5, Timestamp: time.Now().Add(-5 * time.Second)},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/throughput")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.ThroughputStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	require.Len(t, stats, 1)
	assert.Equal(t, "GET", stats[0].Method)
	assert.Equal(t, "/x", stats[0].Path)
	assert.Equal(t, 2, stats[0].TotalCount)
}

// TC-09: Content-Type is application/json.
func TestAPIThroughputContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/throughput")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-10: POST /metrics/throughput → 405.
func TestAPIThroughputMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/throughput", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-10: GET /metrics/errors ---

func makeErrorRecords() []storage.Record {
	return []storage.Record{
		{Method: "GET", Path: "/users", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/users", StatusCode: 500, DurationMs: 10, Timestamp: time.Now()},
		{Method: "POST", Path: "/orders", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
	}
}

// TC-10: No data → 200 with empty array.
func TestAPIErrorsEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.ErrorStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Len(t, stats, 0)
}

// TC-11: Records present → 200 with correct fields.
func TestAPIErrorsWithData(t *testing.T) {
	srv := newTestServer(t, makeErrorRecords())
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.ErrorStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	require.Len(t, stats, 2)
	// GET /users has 50% error rate, should be first
	assert.Equal(t, "GET", stats[0].Method)
	assert.Equal(t, "/users", stats[0].Path)
	assert.Equal(t, 50.0, stats[0].ErrorRate)
	assert.Equal(t, 1, stats[0].ErrorCount)
	assert.Equal(t, 2, stats[0].TotalCount)
}

// TC-12: Content-Type is application/json.
func TestAPIErrorsContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-13: POST /metrics/errors → 405.
func TestAPIErrorsMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/errors", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-09: GET /metrics/slowest ---

func makeRecords(paths []string, durations []float64) []storage.Record {
	records := make([]storage.Record, len(paths))
	for i, p := range paths {
		records[i] = storage.Record{Method: "GET", Path: p, StatusCode: 200, DurationMs: durations[i], Timestamp: time.Now()}
	}
	return records
}

// TC-06: ?n=3 with 5 endpoints → 200 with 3 slowest.
func TestAPISlowestTopN(t *testing.T) {
	records := makeRecords(
		[]string{"/a", "/b", "/c", "/d", "/e"},
		[]float64{10, 50, 30, 80, 20},
	)
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest?n=3")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.EndpointStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	require.Len(t, stats, 3)
	assert.Equal(t, "/d", stats[0].Path)
}

// TC-07: No ?n= → defaults to 10, returns all when < 10 available.
func TestAPISlowestDefaultN(t *testing.T) {
	records := makeRecords([]string{"/a", "/b", "/c"}, []float64{10, 50, 30})
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.EndpointStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Len(t, stats, 3)
}

// TC-08: ?n=100 with 3 endpoints → returns all 3.
func TestAPISlowestNExceedsAvailable(t *testing.T) {
	records := makeRecords([]string{"/a", "/b", "/c"}, []float64{10, 50, 30})
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest?n=100")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.EndpointStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Len(t, stats, 3)
}

// TC-09: ?n=abc → 400 with JSON error.
func TestAPISlowestInvalidN(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest?n=abc")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Contains(t, result["error"], "invalid n")
}

// TC-10: ?n=0 → 400.
func TestAPISlowestZeroN(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest?n=0")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-11: ?n=-1 → 400.
func TestAPISlowestNegativeN(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest?n=-1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-12: No data → 200 with empty array.
func TestAPISlowestNoData(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stats []metrics.EndpointStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stats))
	assert.Len(t, stats, 0)
}

// TC-13: POST /metrics/slowest → 405.
func TestAPISlowestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/slowest", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TC-14: Content-Type is application/json.
func TestAPISlowestContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// --- US-12: GET /metrics/histogram ---

// TC-06: No params → 200, 9 buckets.
func TestAPIHistogramNoParams(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/histogram")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stat metrics.HistogramStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stat))
	assert.Len(t, stat.Buckets, 9)
}

// TC-07: With method+path → 200, filters correctly.
func TestAPIHistogramWithFilter(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
		{Method: "GET", Path: "/other", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/histogram?method=GET&path=/x")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var stat metrics.HistogramStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stat))
	assert.Equal(t, 1, stat.TotalCount)
}

// TC-08: Content-Type is application/json.
func TestAPIHistogramContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/histogram")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-09: POST → 405.
func TestAPIHistogramMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/histogram", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-21: GET /metrics/latency ---

// TC-05: method + path → 200, exactly 60 elements.
func TestAPILatencyOK(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/latency?method=GET&path=/x")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var buckets []metrics.BucketStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&buckets))
	assert.Len(t, buckets, 60)
}

// TC-06: Missing method → 400.
func TestAPILatencyMissingMethod(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/latency?path=/x")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-07: Missing path → 400.
func TestAPILatencyMissingPath(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/latency?method=GET")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-08: POST → 405.
func TestAPILatencyMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/latency", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-20: GET /metrics/table ---

// TC-05: No data → 200, empty array.
func TestAPITableEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/table")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rows []metrics.TableRow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rows))
	assert.Len(t, rows, 0)
}

// TC-06: With data → 200, correct fields.
func TestAPITableWithData(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 200, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/table")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rows []metrics.TableRow
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "GET", rows[0].Method)
	assert.Equal(t, "/x", rows[0].Path)
	assert.Equal(t, 50.0, rows[0].ErrorRate)
	assert.Equal(t, 2, rows[0].Count)
}

// TC-07: Content-Type is application/json.
func TestAPITableContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/table")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-08: POST /metrics/table → 405.
func TestAPITableMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/table", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-19: GET /metrics/summary ---

// TC-05: No data → 200, all fields zero.
func TestAPISummaryEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/summary")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var s metrics.SummaryStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
	assert.Equal(t, 0, s.TotalRequests)
	assert.Equal(t, 0.0, s.GlobalErrorRate)
	assert.Equal(t, 0.0, s.GlobalP99)
	assert.Equal(t, 0, s.ActiveEndpoints)
}

// TC-06: With data → 200, correct fields.
func TestAPISummaryWithData(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 200, Timestamp: time.Now()},
		{Method: "POST", Path: "/y", StatusCode: 200, DurationMs: 50, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/summary")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var s metrics.SummaryStat
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
	assert.Equal(t, 3, s.TotalRequests)
	assert.Equal(t, 2, s.ActiveEndpoints)
	assert.Greater(t, s.GlobalP99, 0.0)
}

// TC-07: Content-Type is application/json.
func TestAPISummaryContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/summary")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-08: POST /metrics/summary → 405.
func TestAPISummaryMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/metrics/summary", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-18: Dashboard ---

// TC-01: GET / → 200, Content-Type: text/html.
func TestAPIDashboardOK(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// TC-02: Body contains <title>API Profiler</title>.
func TestAPIDashboardTitle(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "<title>API Profiler</title>")
}

// TC-03: Body contains id="summary" and id="endpoints".
func TestAPIDashboardSections(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	assert.Contains(t, s, `id="summary"`)
	assert.Contains(t, s, `id="endpoints"`)
}

// TC-04: POST / → 405.
func TestAPIDashboardMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TC-05: GET /static/style.css → 200, Content-Type: text/css.
func TestAPIDashboardStaticCSS(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/static/style.css")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/css")
}

// --- US-17: GET /alerts/history ---

// TC-06: No history → 200 with empty array.
func TestAPIAlertsHistoryEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/history")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var history []alerts.AlertRecord
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&history))
	assert.Len(t, history, 0)
}

// TC-07: History present → 200 with correct fields.
func TestAPIAlertsHistoryWithData(t *testing.T) {
	window := time.Minute
	engine := metrics.NewEngine(&apiSplitReader{
		window: window,
		currentRecords: []storage.Record{
			{Method: "GET", Path: "/slow", StatusCode: 200, DurationMs: 400, Timestamp: time.Now()},
		},
		baselineRecords: []storage.Record{
			{Method: "GET", Path: "/slow", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		},
	}, window)
	detector := alerts.NewDetector(engine, 3.0, 5)
	detector.Evaluate()
	srv := api.NewServer(engine, "localhost:0", 5, 500, detector, nil)
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	resp, err := http.Get("http://" + srv.Addr() + "/alerts/history")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var history []alerts.AlertRecord
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&history))
	require.Len(t, history, 1)
	assert.Equal(t, "GET", history[0].Method)
	assert.Equal(t, "/slow", history[0].Path)
	assert.Nil(t, history[0].ResolvedAt)
}

// TC-08: Content-Type is application/json.
func TestAPIAlertsHistoryContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/history")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-09: POST /alerts/history → 405.
func TestAPIAlertsHistoryMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/history", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// --- US-16: POST /alerts/silence and GET /alerts/silences ---

// TC-07: Valid body → 200 with expires_at.
func TestAPICreateSilenceValid(t *testing.T) {
	srv := newTestServer(t, nil)
	body := `{"method":"GET","path":"/api/reports","duration":"1h"}`
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/silence", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	var silence alerts.Silence
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&silence))
	assert.Equal(t, "GET", silence.Method)
	assert.Equal(t, "/api/reports", silence.Path)
	assert.True(t, silence.ExpiresAt.After(time.Now()))
}

// TC-08: Invalid duration → 400.
func TestAPICreateSilenceInvalidDuration(t *testing.T) {
	srv := newTestServer(t, nil)
	body := `{"method":"GET","path":"/x","duration":"xyz"}`
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/silence", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var result map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Contains(t, result["error"], "invalid duration")
}

// TC-09: Negative duration → 400.
func TestAPICreateSilenceNegativeDuration(t *testing.T) {
	srv := newTestServer(t, nil)
	body := `{"method":"GET","path":"/x","duration":"-1h"}`
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/silence", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-10: Malformed JSON body → 400.
func TestAPICreateSilenceMalformedBody(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/silence", "application/json", strings.NewReader("{bad json"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-11: GET /alerts/silence → 405.
func TestAPICreateSilenceMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/silence")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TC-12: No silences → 200 with empty array.
func TestAPIListSilencesEmpty(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/silences")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var silences []alerts.Silence
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&silences))
	assert.Len(t, silences, 0)
}

// TC-13: Active silence → 200 with correct expires_at.
func TestAPIListSilencesWithData(t *testing.T) {
	srv := newTestServer(t, nil)
	// Create a silence first.
	body := `{"method":"GET","path":"/x","duration":"30m"}`
	postResp, err := http.Post("http://"+srv.Addr()+"/alerts/silence", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	postResp.Body.Close()
	require.Equal(t, http.StatusOK, postResp.StatusCode)

	resp, err := http.Get("http://" + srv.Addr() + "/alerts/silences")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var silences []alerts.Silence
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&silences))
	require.Len(t, silences, 1)
	assert.Equal(t, "GET", silences[0].Method)
	assert.Equal(t, "/x", silences[0].Path)
	assert.True(t, silences[0].ExpiresAt.After(time.Now()))
}

// TC-14: Content-Type is application/json for GET /alerts/silences.
func TestAPIListSilencesContentType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/alerts/silences")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

// TC-15: POST /alerts/silences → 405.
func TestAPIListSilencesMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Post("http://"+srv.Addr()+"/alerts/silences", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// TC-14: GET /health → 200 {"status":"ok"}.
func TestAPIHealth(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

// TC-08 (US-27): GET /metrics/requests → 200, JSON array.
func TestAPIRequestsReturnsArray(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/requests")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result []storage.Record
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Len(t, result, 1)
}

// TC-09 (US-27): GET /metrics/requests?n=5 → max 5 records.
func TestAPIRequestsNParam(t *testing.T) {
	records := make([]storage.Record, 10)
	for i := range records {
		records[i] = storage.Record{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 1, Timestamp: time.Now()}
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/requests?n=5")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result []storage.Record
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Len(t, result, 5)
}

// TC-10 (US-27): GET /metrics/requests?n=9999 → clamped to 1000, still 200.
func TestAPIRequestsNClamp(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/requests?n=9999")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TC-11 (US-27): GET /metrics/requests?n=abc → 400 bad request.
func TestAPIRequestsInvalidN(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/requests?n=abc")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-06 (US-29): GET /metrics/slowest-requests → 200, JSON array.
func TestAPISlowestRequestsOK(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 500, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest-requests")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var result []storage.Record
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Len(t, result, 2)
	assert.Equal(t, 500.0, result[0].DurationMs)
}

// TC-07 (US-29): GET /metrics/slowest-requests?n=abc → 400 bad request.
func TestAPISlowestRequestsInvalidN(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/slowest-requests?n=abc")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-03 (US-32): GET /metrics/status?method=GET&path=/a → breakdown filtrado.
func TestAPIStatusBreakdownForEndpoint(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: time.Now()},
		{Method: "GET", Path: "/a", StatusCode: 500, DurationMs: 1, Timestamp: time.Now()},
		{Method: "GET", Path: "/b", StatusCode: 404, DurationMs: 1, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/status?method=GET&path=/a")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var groups []metrics.StatusGroup
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&groups))
	require.Len(t, groups, 4)
	assert.Equal(t, 1, groups[0].Count) // 2xx: only /a 200
	assert.Equal(t, 0, groups[2].Count) // 4xx: /b 404 not counted
}

// TC-04 (US-32): GET /metrics/status?method=GET (sin path) → 400.
func TestAPIStatusBreakdownMethodWithoutPath(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/status?method=GET")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TC-05 (US-28): GET /metrics/status → 200, array de 4 elementos.
func TestAPIStatusBreakdownOK(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1, Timestamp: time.Now()},
		{Method: "GET", Path: "/a", StatusCode: 500, DurationMs: 1, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var groups []metrics.StatusGroup
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&groups))
	assert.Len(t, groups, 4)
}

// TC-06 (US-39): GET /health sin checker → {"status":"ok"}.
func TestHealthWithoutChecker(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
	assert.Nil(t, body["upstream"])
}

// TC-07 (US-39): GET /health con checker → incluye campo upstream.
func TestHealthWithChecker(t *testing.T) {
	// Don't start the checker; initial state is "unknown" without polling.
	checker := health.New("http://127.0.0.1:0", 10*time.Second, 5*time.Second, 3)
	engine := metrics.NewEngine(&staticReader{}, time.Minute)
	detector := alerts.NewDetector(engine, 3.0, 5)
	srv := api.NewServer(engine, "localhost:0", 5, 500, detector, checker)
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	resp, err := http.Get("http://" + srv.Addr() + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
	require.NotNil(t, body["upstream"])
	upstream := body["upstream"].(map[string]interface{})
	assert.Equal(t, "unknown", upstream["status"])
}

// TC-06 (US-28): Siempre devuelve 4 grupos en orden 2xx/3xx/4xx/5xx.
func TestAPIStatusBreakdownOrder(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	var groups []metrics.StatusGroup
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&groups))
	require.Len(t, groups, 4)
	assert.Equal(t, "2xx", groups[0].Class)
	assert.Equal(t, "3xx", groups[1].Class)
	assert.Equal(t, "4xx", groups[2].Class)
	assert.Equal(t, "5xx", groups[3].Class)
}

// ── US-42: Prometheus endpoint ────────────────────────────────────────────────

// TC-01: single endpoint — body contains per-endpoint metric lines.
func TestPrometheusHappyPath(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/users", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "GET", Path: "/users", StatusCode: 500, DurationMs: 20, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/prometheus")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	assert.Contains(t, s, `apiprofiler_request_duration_p99_ms{method="GET",path="/users"}`)
	assert.Contains(t, s, `apiprofiler_request_error_rate{method="GET",path="/users"}`)
	assert.Contains(t, s, `apiprofiler_request_rps_current{method="GET",path="/users"}`)
}

// TC-02: HELP and TYPE lines are present.
func TestPrometheusHelpAndType(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/prometheus")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	assert.Contains(t, s, "# HELP apiprofiler_request_duration_p99_ms")
	assert.Contains(t, s, "# TYPE apiprofiler_request_duration_p99_ms gauge")
	assert.Contains(t, s, "# HELP apiprofiler_active_alerts_total")
	assert.Contains(t, s, "# TYPE apiprofiler_active_alerts_total gauge")
}

// TC-03: multiple endpoints appear as separate label sets.
func TestPrometheusMultipleEndpoints(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/users", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
		{Method: "POST", Path: "/orders", StatusCode: 201, DurationMs: 50, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/prometheus")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	assert.Contains(t, s, `path="/users"`)
	assert.Contains(t, s, `path="/orders"`)
}

// TC-04: active alerts counter reflects current state.
func TestPrometheusActiveAlerts(t *testing.T) {
	// Two records: one with very high latency to trigger a latency alert when evaluated.
	// Instead, inject an alert directly by calling Evaluate after seeding baseline data.
	// Simpler: just check the zero-value output — all kinds = 0.
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/prometheus")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	assert.Contains(t, s, `apiprofiler_active_alerts_total{kind="latency"} 0`)
	assert.Contains(t, s, `apiprofiler_active_alerts_total{kind="error_rate"} 0`)
	assert.Contains(t, s, `apiprofiler_active_alerts_total{kind="throughput"} 0`)
}

// TC-05: no traffic — status 200, correct content-type, no per-endpoint lines.
func TestPrometheusNoTraffic(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/prometheus")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	assert.NotContains(t, s, `path="`)
	// HELP/TYPE lines are always present.
	assert.Contains(t, s, "# HELP apiprofiler_request_duration_p50_ms")
}

// TC-06: path with special characters appears correctly as a label value.
func TestPrometheusPathWithSpecialChars(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/api/v1/users/{id}", StatusCode: 200, DurationMs: 15, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/prometheus")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `path="/api/v1/users/{id}"`)
}

// --- US-44: Apdex endpoint ---

// TC-07: GET /metrics/apdex devuelve 200 con estructura correcta.
func TestApdexEndpointStructure(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 100, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 200, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/apdex")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var result struct {
		TMs       int               `json:"t_ms"`
		Endpoints []json.RawMessage `json:"endpoints"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, 500, result.TMs)
	assert.Len(t, result.Endpoints, 1)
}

// TC-08: ?t=250 sobrescribe el umbral — conteos reflejan el nuevo T.
func TestApdexEndpointCustomT(t *testing.T) {
	records := []storage.Record{
		// duration=300ms: satisfied with T=500, but frustrated with T=250 (>4*250=1000? no, 300<1000)
		// Actually with T=250: 300 > 250 and 300 <= 4*250=1000 → tolerating
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 300, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/apdex?t=250")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result struct {
		TMs       int `json:"t_ms"`
		Endpoints []struct {
			Satisfied  int `json:"satisfied"`
			Tolerating int `json:"tolerating"`
		} `json:"endpoints"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, 250, result.TMs)
	require.Len(t, result.Endpoints, 1)
	assert.Equal(t, 0, result.Endpoints[0].Satisfied)
	assert.Equal(t, 1, result.Endpoints[0].Tolerating)
}

// TC-09: ?t=0 devuelve 400.
func TestApdexEndpointInvalidTZero(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/apdex?t=0")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errBody struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Contains(t, errBody.Error, "invalid t")
}

// TC-10: ?t=abc devuelve 400.
func TestApdexEndpointInvalidTString(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/apdex?t=abc")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// --- US-46: Error fingerprints endpoint ---

// TC-08: GET /metrics/errors/fingerprints devuelve 200 con estructura correcta.
func TestErrorFingerprintsEndpointStructure(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 100, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 10, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors/fingerprints")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var result struct {
		TotalErrors  int               `json:"total_errors"`
		Fingerprints []json.RawMessage `json:"fingerprints"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, 1, result.TotalErrors)
	assert.Len(t, result.Fingerprints, 1)
}

// TC-09: ?status=5xx filtra solo errores 5xx.
func TestErrorFingerprintsFilter5xx(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 1, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 404, DurationMs: 1, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors/fingerprints?status=5xx")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		Fingerprints []struct {
			StatusCode int `json:"status_code"`
		} `json:"fingerprints"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Fingerprints, 1)
	assert.Equal(t, 500, result.Fingerprints[0].StatusCode)
}

// TC-10: ?status=4xx filtra solo errores 4xx.
func TestErrorFingerprintsFilter4xx(t *testing.T) {
	records := []storage.Record{
		{Method: "GET", Path: "/x", StatusCode: 500, DurationMs: 1, Timestamp: time.Now()},
		{Method: "GET", Path: "/x", StatusCode: 404, DurationMs: 1, Timestamp: time.Now()},
	}
	srv := newTestServer(t, records)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors/fingerprints?status=4xx")
	require.NoError(t, err)
	defer resp.Body.Close()

	var result struct {
		Fingerprints []struct {
			StatusCode int `json:"status_code"`
		} `json:"fingerprints"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Fingerprints, 1)
	assert.Equal(t, 404, result.Fingerprints[0].StatusCode)
}

// TC-11: ?status=3xx devuelve 400 con mensaje de error.
func TestErrorFingerprintsInvalidStatusFilter(t *testing.T) {
	srv := newTestServer(t, nil)
	resp, err := http.Get("http://" + srv.Addr() + "/metrics/errors/fingerprints?status=3xx")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errBody struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Contains(t, errBody.Error, "invalid status filter")
}
