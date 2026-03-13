package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveRecord(t *testing.T, s Store, method, path string, status int, ts time.Time, durationMs float64) {
	t.Helper()
	require.NoError(t, s.Save(Record{
		Timestamp:  ts,
		Method:     method,
		Path:       path,
		StatusCode: status,
		DurationMs: durationMs,
	}))
}

// FindByWindow returns records within [from, to).
func TestFindByWindowReturnsMatchingRecords(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	saveRecord(t, s, "GET", "/a", 200, now.Add(-30*time.Second), 10)
	saveRecord(t, s, "GET", "/b", 200, now.Add(-10*time.Second), 20)

	recs, err := s.FindByWindow(now.Add(-60*time.Second), now.Add(time.Second))
	require.NoError(t, err)
	assert.Len(t, recs, 2)
}

// Records before the window are excluded (TC-06).
func TestFindByWindowExcludesOldRecords(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	saveRecord(t, s, "GET", "/old", 200, now.Add(-2*time.Minute), 5) // outside window
	saveRecord(t, s, "GET", "/new", 200, now.Add(-30*time.Second), 5) // inside window

	recs, err := s.FindByWindow(now.Add(-time.Minute), now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "/new", recs[0].Path)
}

// Record exactly at `from` is included; record exactly at `to` is excluded (TC-07).
func TestFindByWindowBoundaryInclusion(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	from := now.Add(-time.Minute)
	to := now

	saveRecord(t, s, "GET", "/at-from", 200, from, 1) // included [from ...
	saveRecord(t, s, "GET", "/at-to", 200, to, 1)     // excluded ... to)

	recs, err := s.FindByWindow(from, to)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "/at-from", recs[0].Path)
}

// Empty window returns empty slice, not nil.
func TestFindByWindowEmpty(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC()
	recs, err := s.FindByWindow(now.Add(-time.Minute), now)
	require.NoError(t, err)
	assert.NotNil(t, recs)
	assert.Len(t, recs, 0)
}

// All fields are correctly returned.
func TestFindByWindowFieldsPreserved(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	saveRecord(t, s, "POST", "/api/users", 201, now.Add(-5*time.Second), 42.5)

	recs, err := s.FindByWindow(now.Add(-time.Minute), now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, recs, 1)
	r := recs[0]
	assert.Equal(t, "POST", r.Method)
	assert.Equal(t, "/api/users", r.Path)
	assert.Equal(t, 201, r.StatusCode)
	assert.InDelta(t, 42.5, r.DurationMs, 0.001)
}
