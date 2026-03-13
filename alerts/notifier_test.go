package alerts_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"api-profiler/alerts"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testAlert = alerts.Alert{
	Method:      "GET",
	Path:        "/api/slow",
	CurrentP99:  500.0,
	BaselineP99: 100.0,
	Threshold:   3.0,
	TriggeredAt: time.Now(),
}

// TC-01: Webhook receives POST with correct JSON payload.
func TestWebhookNotifierSendsPost(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := alerts.NewWebhookNotifier(srv.URL)
	n.Notify(testAlert)

	var got alerts.Alert
	require.NoError(t, json.Unmarshal(received, &got))
	assert.Equal(t, testAlert.Method, got.Method)
	assert.Equal(t, testAlert.Path, got.Path)
	assert.Equal(t, testAlert.CurrentP99, got.CurrentP99)
	assert.Equal(t, testAlert.BaselineP99, got.BaselineP99)
	assert.Equal(t, testAlert.Threshold, got.Threshold)
}

// TC-02: Content-Type header of the POST is application/json.
func TestWebhookNotifierContentType(t *testing.T) {
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	alerts.NewWebhookNotifier(srv.URL).Notify(testAlert)
	assert.Equal(t, "application/json", ct)
}

// TC-03: Server returns 500 → no panic, error is logged and swallowed.
func TestWebhookNotifierServer500NoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	assert.NotPanics(t, func() {
		alerts.NewWebhookNotifier(srv.URL).Notify(testAlert)
	})
}

// TC-04: Unreachable URL → no panic.
func TestWebhookNotifierUnreachableNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // close immediately → connection refused

	assert.NotPanics(t, func() {
		n := alerts.NewWebhookNotifier(url)
		n.Client.Timeout = 500 * time.Millisecond
		n.Notify(testAlert)
	})
}
