package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newChecker(target string) *Checker {
	return New(target, 10*time.Second, 5*time.Second, 3)
}

// TC-01: Estado inicial es unknown.
func TestCheckerInitialState(t *testing.T) {
	c := newChecker("http://127.0.0.1:0")
	s := c.State()
	assert.Equal(t, StatusUnknown, s.Status)
	assert.Equal(t, 0, s.ConsecutiveFailures)
	assert.Equal(t, float64(0), s.LatencyMs)
	assert.True(t, s.LastCheck.IsZero())
}

// TC-02: Ping exitoso → healthy, latency > 0.
func TestCheckerSuccessBecomesHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newChecker(srv.URL)
	c.ping()

	s := c.State()
	assert.Equal(t, StatusHealthy, s.Status)
	assert.Greater(t, s.LatencyMs, float64(0))
	assert.Equal(t, 0, s.ConsecutiveFailures)
	assert.False(t, s.LastCheck.IsZero())
}

// TC-03: Fallos < threshold → degraded.
func TestCheckerFailuresBelowThresholdDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newChecker(srv.URL) // threshold = 3
	c.ping()                 // 1 failure
	c.ping()                 // 2 failures

	s := c.State()
	assert.Equal(t, StatusDegraded, s.Status)
	assert.Equal(t, 2, s.ConsecutiveFailures)
}

// TC-04: Fallos >= threshold → down.
func TestCheckerFailuresAtThresholdDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newChecker(srv.URL) // threshold = 3
	c.ping()
	c.ping()
	c.ping() // 3 failures = threshold

	s := c.State()
	assert.Equal(t, StatusDown, s.Status)
	assert.Equal(t, 3, s.ConsecutiveFailures)
}

// TC-05: Tras down, ping exitoso → healthy (reset).
func TestCheckerRecoveryFromDown(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := newChecker(srv.URL)
	c.ping()
	c.ping()
	c.ping()
	assert.Equal(t, StatusDown, c.State().Status)

	fail = false
	c.ping()

	s := c.State()
	assert.Equal(t, StatusHealthy, s.Status)
	assert.Equal(t, 0, s.ConsecutiveFailures)
}
