package storage

import (
	"fmt"
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

// TC-01: FindRecent returns records newest-first (DESC).
func TestFindRecentOrderDesc(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	saveRecord(t, s, "GET", "/a", 200, now.Add(-30*time.Second), 10)
	saveRecord(t, s, "GET", "/b", 200, now.Add(-10*time.Second), 20)
	saveRecord(t, s, "GET", "/c", 200, now.Add(-5*time.Second), 30)

	recs, err := s.FindRecent(now.Add(-60*time.Second), now.Add(time.Second), 10)
	require.NoError(t, err)
	require.Len(t, recs, 3)
	assert.Equal(t, "/c", recs[0].Path)
	assert.Equal(t, "/b", recs[1].Path)
	assert.Equal(t, "/a", recs[2].Path)
}

// TC-02: FindRecent respects the limit.
func TestFindRecentLimit(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 10; i++ {
		saveRecord(t, s, "GET", fmt.Sprintf("/r%d", i), 200, now.Add(-time.Duration(i)*time.Second), 1)
	}

	recs, err := s.FindRecent(now.Add(-60*time.Second), now.Add(time.Second), 3)
	require.NoError(t, err)
	assert.Len(t, recs, 3)
}

// TC-03: FindRecent respects the [from, to) range.
func TestFindRecentRange(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	saveRecord(t, s, "GET", "/old", 200, now.Add(-3*time.Minute), 1)
	saveRecord(t, s, "GET", "/in", 200, now.Add(-30*time.Second), 1)

	recs, err := s.FindRecent(now.Add(-time.Minute), now.Add(time.Second), 10)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "/in", recs[0].Path)
}

// TC-04: FindRecent with no records returns empty (non-nil) slice.
func TestFindRecentEmpty(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC()
	recs, err := s.FindRecent(now.Add(-time.Minute), now, 10)
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

// TestFindByTraceID verifies that FindByTraceID returns only the records with
// the requested trace_id, ordered by timestamp ascending.
func TestFindByTraceID(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	tid := "aabbccddeeff00112233445566778899"

	// Three records with the target trace_id, inserted out of order.
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-10 * time.Second), Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 5, TraceID: tid, SpanID: "span2", ParentSpanID: "span1"}))
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-20 * time.Second), Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 10, TraceID: tid, SpanID: "span1", ParentSpanID: ""}))
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-5 * time.Second), Method: "GET", Path: "/c", StatusCode: 500, DurationMs: 3, TraceID: tid, SpanID: "span3", ParentSpanID: "span2"}))
	// One record with a different trace_id — must not appear.
	require.NoError(t, s.Save(Record{Timestamp: now, Method: "GET", Path: "/other", StatusCode: 200, DurationMs: 1, TraceID: "othertraceidothertraceidothertrace", SpanID: "spanX"}))

	recs, err := s.FindByTraceID(tid)
	require.NoError(t, err)
	require.Len(t, recs, 3)

	// Must be ordered by timestamp ASC.
	assert.Equal(t, "/a", recs[0].Path)
	assert.Equal(t, "/b", recs[1].Path)
	assert.Equal(t, "/c", recs[2].Path)

	// ParentSpanID must be round-tripped correctly.
	assert.Equal(t, "", recs[0].ParentSpanID)
	assert.Equal(t, "span1", recs[1].ParentSpanID)
	assert.Equal(t, "span2", recs[2].ParentSpanID)
}

// TestFindByTraceIDEmpty verifies an empty slice (not nil) when no records match.
func TestFindByTraceIDEmpty(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	recs, err := s.FindByTraceID("nonexistenttraceidnonexistentxxx")
	require.NoError(t, err)
	assert.NotNil(t, recs)
	assert.Empty(t, recs)
}
