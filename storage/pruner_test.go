package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-05: Pruner automatically deletes old records after Start().
func TestPrunerDeletesOldRecords(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC()
	// Insert two old records and one recent one.
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-2 * time.Hour), Method: "GET", Path: "/old1", StatusCode: 200, DurationMs: 1}))
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-90 * time.Minute), Method: "GET", Path: "/old2", StatusCode: 200, DurationMs: 1}))
	require.NoError(t, s.Save(Record{Timestamp: now.Add(-1 * time.Minute), Method: "GET", Path: "/new", StatusCode: 200, DurationMs: 1}))

	// 1h retention, very short interval so the first tick fires quickly.
	p := NewPruner(s, time.Hour, 10*time.Millisecond)
	p.Start()
	time.Sleep(50 * time.Millisecond)
	p.Stop()

	remaining := allRecords(t, s)
	require.Len(t, remaining, 1)
	assert.Equal(t, "/new", remaining[0].Path)
}

// TC-06: Pruner with retention=0 does not delete any records.
func TestPrunerZeroRetentionNoOp(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.Save(Record{Timestamp: time.Now().Add(-24 * time.Hour), Method: "GET", Path: "/ancient", StatusCode: 200, DurationMs: 1}))

	p := NewPruner(s, 0, 10*time.Millisecond)
	p.Start()
	time.Sleep(30 * time.Millisecond)
	p.Stop()

	assert.Len(t, allRecords(t, s), 1)
}
