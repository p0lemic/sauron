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
