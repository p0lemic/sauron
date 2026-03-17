package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allRecords queries every record from the store (test helper).
func allRecords(t *testing.T, s Store) []Record {
	t.Helper()
	rows, err := s.(*sqliteStore).db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms FROM requests ORDER BY id`,
	)
	require.NoError(t, err)
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var rec Record
		var ts string
		require.NoError(t, rows.Scan(&ts, &rec.Method, &rec.Path, &rec.StatusCode, &rec.DurationMs))
		rec.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, rec)
	}
	return out
}

// TC-13: New database is created with correct schema; first Save succeeds.
func TestNewCreatesDatabase(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	err = s.Save(Record{
		Timestamp:  time.Now(),
		Method:     "GET",
		Path:       "/ping",
		StatusCode: 200,
		DurationMs: 1.5,
	})
	require.NoError(t, err)
}

// TC-14: Invalid path returns descriptive error.
func TestNewInvalidPath(t *testing.T) {
	_, err := Open("sqlite","/nonexistent-dir-xyz/profiler.db")
	require.Error(t, err)
}

// TC-01 (store): Saved record contains all correct fields.
func TestSaveFields(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	ts := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.Save(Record{
		Timestamp:  ts,
		Method:     "POST",
		Path:       "/api/users",
		StatusCode: 201,
		DurationMs: 4.567,
	}))

	recs := allRecords(t, s)
	require.Len(t, recs, 1)
	assert.Equal(t, "POST", recs[0].Method)
	assert.Equal(t, "/api/users", recs[0].Path)
	assert.Equal(t, 201, recs[0].StatusCode)
	assert.InDelta(t, 4.567, recs[0].DurationMs, 0.001)
}

// TC-15: DurationMs is never negative.
func TestSaveDurationNonNegative(t *testing.T) {
	s, err := Open("sqlite",":memory:")
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.Save(Record{
		Timestamp:  time.Now(),
		Method:     "GET",
		Path:       "/fast",
		StatusCode: 200,
		DurationMs: 0.0,
	}))
	recs := allRecords(t, s)
	assert.GreaterOrEqual(t, recs[0].DurationMs, 0.0)
}

// TC-16: Records survive close and reopen of the database.
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Write record, close.
	s1, err := Open("sqlite",dbPath)
	require.NoError(t, err)
	require.NoError(t, s1.Save(Record{
		Timestamp: time.Now(), Method: "DELETE", Path: "/item/1", StatusCode: 204, DurationMs: 2.0,
	}))
	require.NoError(t, s1.Close())

	// Reopen, record must still be there.
	s2, err := Open("sqlite",dbPath)
	require.NoError(t, err)
	defer s2.Close()

	recs := allRecords(t, s2)
	require.Len(t, recs, 1)
	assert.Equal(t, "DELETE", recs[0].Method)
	assert.Equal(t, "/item/1", recs[0].Path)
}

// ── US-43: Prune ─────────────────────────────────────────────────────────────

// TC-01: Prune removes old rows and returns correct count.
func TestPruneRemovesOldRecords(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC()
	threshold := now.Add(-30 * time.Minute)

	require.NoError(t, s.Save(Record{Timestamp: now.Add(-2 * time.Hour), Method: "GET", Path: "/a", StatusCode: 200, DurationMs: 1}))
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-1 * time.Hour), Method: "GET", Path: "/b", StatusCode: 200, DurationMs: 1}))
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-5 * time.Minute), Method: "GET", Path: "/c", StatusCode: 200, DurationMs: 1}))

	n, err := s.Prune(threshold)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	remaining := allRecords(t, s)
	require.Len(t, remaining, 1)
	assert.Equal(t, "/c", remaining[0].Path)
}

// TC-02: Prune returns 0 when no rows match.
func TestPruneNoEligibleRows(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.Save(Record{Timestamp: time.Now(), Method: "GET", Path: "/x", StatusCode: 200, DurationMs: 1}))

	n, err := s.Prune(time.Now().Add(-1 * time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	assert.Len(t, allRecords(t, s), 1)
}

// TC-03: Prune on empty DB returns 0 without error.
func TestPruneEmptyDB(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	n, err := s.Prune(time.Now())
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
}

// TC-04: Row exactly at the threshold is NOT deleted (strict < before).
func TestPruneExactThreshold(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	threshold := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.Save(Record{Timestamp: threshold, Method: "GET", Path: "/edge", StatusCode: 200, DurationMs: 1}))

	n, err := s.Prune(threshold)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	assert.Len(t, allRecords(t, s), 1)
}

// Verify that the db file is created on disk (not just in memory).
func TestNewCreatesFileOnDisk(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "profiler.db")

	s, err := Open("sqlite",dbPath)
	require.NoError(t, err)
	s.Close()

	_, err = os.Stat(dbPath)
	assert.NoError(t, err, "database file should exist on disk")
}

// --- US-45: TraceContext storage ---

// TC-11: migrate() añade columnas trace_id y span_id; INSERTs con valores vacíos funcionan.
func TestMigrateAddsTraceColumns(t *testing.T) {
	store, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer store.Close()

	err = store.Save(Record{
		Timestamp:  time.Now(),
		Method:     "GET",
		Path:       "/x",
		StatusCode: 200,
		DurationMs: 1.0,
		TraceID:    "",
		SpanID:     "",
	})
	require.NoError(t, err)
}

// ── US-51: SaveSpan + FindSpansByTraceID ─────────────────────────────────────

// TC-08: SaveSpan persists all fields; FindSpansByTraceID retrieves them.
func TestSaveSpanAndFindSpansByTraceID(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Round(0) // strip monotonic clock
	sp := InnerSpan{
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:       "a2fb4a1d1a96d312",
		ParentSpanID: "00f067aa0ba902b7",
		Name:         `App\Controller\UserController::index`,
		Kind:         "controller",
		StartTime:    now,
		DurationMs:   42.3,
		Attributes:   map[string]string{"http.method": "GET"},
		Status:       "ok",
	}
	require.NoError(t, s.SaveSpan(sp))

	spans, err := s.FindSpansByTraceID(sp.TraceID)
	require.NoError(t, err)
	require.Len(t, spans, 1)
	assert.Equal(t, sp.TraceID, spans[0].TraceID)
	assert.Equal(t, sp.SpanID, spans[0].SpanID)
	assert.Equal(t, sp.ParentSpanID, spans[0].ParentSpanID)
	assert.Equal(t, sp.Name, spans[0].Name)
	assert.Equal(t, sp.Kind, spans[0].Kind)
	assert.InDelta(t, sp.DurationMs, spans[0].DurationMs, 0.001)
	assert.Equal(t, sp.Status, spans[0].Status)
	assert.Equal(t, sp.Attributes, spans[0].Attributes)
	assert.True(t, now.Equal(spans[0].StartTime.UTC()), "start_time round-trip")
}

// TC-09: FindSpansByTraceID returns spans ordered by start_time ASC.
func TestFindSpansByTraceIDOrderedByStartTime(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	base := time.Date(2026, 3, 17, 10, 0, 0, 0, time.UTC)
	spans := []InnerSpan{
		{TraceID: "trace1", SpanID: "s3", StartTime: base.Add(2 * time.Second), DurationMs: 1, Status: "ok", Name: "c"},
		{TraceID: "trace1", SpanID: "s1", StartTime: base,                      DurationMs: 1, Status: "ok", Name: "a"},
		{TraceID: "trace1", SpanID: "s2", StartTime: base.Add(1 * time.Second), DurationMs: 1, Status: "ok", Name: "b"},
	}
	for _, sp := range spans {
		require.NoError(t, s.SaveSpan(sp))
	}

	got, err := s.FindSpansByTraceID("trace1")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "s1", got[0].SpanID)
	assert.Equal(t, "s2", got[1].SpanID)
	assert.Equal(t, "s3", got[2].SpanID)
}

// TC-10: FindSpansByTraceID filters by trace_id correctly.
func TestFindSpansByTraceIDFiltersCorrectly(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC()
	require.NoError(t, s.SaveSpan(InnerSpan{TraceID: "traceA", SpanID: "s1", StartTime: now, DurationMs: 1, Status: "ok", Name: "a"}))
	require.NoError(t, s.SaveSpan(InnerSpan{TraceID: "traceB", SpanID: "s2", StartTime: now, DurationMs: 1, Status: "ok", Name: "b"}))
	require.NoError(t, s.SaveSpan(InnerSpan{TraceID: "traceA", SpanID: "s3", StartTime: now, DurationMs: 1, Status: "ok", Name: "c"}))

	got, err := s.FindSpansByTraceID("traceA")
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, sp := range got {
		assert.Equal(t, "traceA", sp.TraceID)
	}
}

// TC-11: Span with nil attributes round-trips as empty map (not nil).
func TestSaveSpanNilAttributesRoundTrip(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.SaveSpan(InnerSpan{
		TraceID:    "trace1",
		SpanID:     "s1",
		StartTime:  time.Now().UTC(),
		DurationMs: 1,
		Status:     "ok",
		Name:       "test",
		Attributes: nil,
	}))

	got, err := s.FindSpansByTraceID("trace1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.NotNil(t, got[0].Attributes, "attributes must not be nil")
	assert.Empty(t, got[0].Attributes, "attributes must be empty map")
}

// TC-12: FindSpansByTraceID for unknown trace_id returns empty slice (not nil).
func TestFindSpansByTraceIDNotFound(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	got, err := s.FindSpansByTraceID("nonexistent-trace")
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TC-12 (store): Save y FindByWindow preservan trace_id y span_id.
func TestSaveAndFindPreservesTraceIDs(t *testing.T) {
	store, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	err = store.Save(Record{
		Timestamp:  now,
		Method:     "GET",
		Path:       "/traced",
		StatusCode: 200,
		DurationMs: 42.0,
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:     "a2fb4a1d1a96d312",
	})
	require.NoError(t, err)

	records, err := store.FindByWindow(now.Add(-time.Second), now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", records[0].TraceID)
	assert.Equal(t, "a2fb4a1d1a96d312", records[0].SpanID)
}
