package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"api-profiler/alerts"
	"api-profiler/api"
	"api-profiler/config"
	"api-profiler/metrics"
	"api-profiler/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startDashboard wires up a dashboard server backed by an in-memory SQLite store
// and returns the server and its base URL. Caller must call srv.Shutdown.
func startDashboard(t *testing.T) (*api.Server, string) {
	t.Helper()
	store, err := storage.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	cfg := config.Default()
	engine := metrics.NewEngine(store, cfg.MetricsWindow)
	detector := alerts.NewDetector(engine, cfg.AnomalyThreshold, cfg.BaselineWindows)
	detector.Start()
	t.Cleanup(detector.Stop)

	srv := api.NewServer(engine, "localhost:0", cfg.BaselineWindows, detector, nil)
	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		srv.Shutdown(context.Background()) //nolint:errcheck
	})
	return srv, "http://" + srv.Addr()
}

// TC-09: Dashboard starts, GET /health returns 200 {"status":"ok"}.
func TestDashboardHealthEndpoint(t *testing.T) {
	_, base := startDashboard(t)

	resp, err := http.Get(base + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

// TC-10: Dashboard serves /metrics/summary from SQLite storage-dsn.
func TestDashboardMetricsSummary(t *testing.T) {
	store, err := storage.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Seed one record.
	require.NoError(t, store.Save(storage.Record{
		Timestamp:  time.Now().Add(-5 * time.Second),
		Method:     "GET",
		Path:       "/ping",
		StatusCode: 200,
		DurationMs: 10.0,
	}))

	cfg := config.Default()
	engine := metrics.NewEngine(store, cfg.MetricsWindow)
	detector := alerts.NewDetector(engine, cfg.AnomalyThreshold, cfg.BaselineWindows)
	detector.Start()
	defer detector.Stop()

	srv := api.NewServer(engine, "localhost:0", cfg.BaselineWindows, detector, nil)
	require.NoError(t, srv.Start())
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	resp, err := http.Get("http://" + srv.Addr() + "/metrics/summary")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	// Should have at least 1 request recorded.
	totalReqs, ok := body["total_requests"]
	require.True(t, ok, "response should contain total_requests")
	assert.Equal(t, float64(1), totalReqs)
}

// Ensure the binary's findConfigFlag helper works correctly.
func TestFindConfigFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--config=foo.yaml"}, "foo.yaml"},
		{[]string{"--config", "bar.yaml"}, "bar.yaml"},
		{[]string{"-config=baz.yaml"}, "baz.yaml"},
		{[]string{"--upstream", "http://x"}, ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, findConfigFlag(c.args))
	}
}

// Ensure ValidateDashboard is wired correctly via config package.
func TestValidateDashboardFromMain(t *testing.T) {
	cfg := config.Default()
	cfg.StorageDriver = "postgres"
	cfg.StorageDSN = ""
	err := config.ValidateDashboard(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage dsn required")
}

// Prevent unused import warning — os is used in the build tag below.
var _ = os.DevNull
