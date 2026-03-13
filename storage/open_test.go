package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-09: Open("sqlite", ":memory:") returns a working StoreReader.
func TestOpenSQLiteMemory(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NotNil(t, s)
	defer s.Close()
}

// TC-10: Open("postgres", invalid-dsn) returns an error (ping fails).
func TestOpenPostgresInvalidDSN(t *testing.T) {
	_, err := Open("postgres", "postgres://invalid-host-xyz:5432/nodb")
	require.Error(t, err)
}

// TC-11: Open("unknown", "") returns an error containing "unsupported driver".
func TestOpenUnknownDriver(t *testing.T) {
	_, err := Open("unknown", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported driver")
}

// TC-12: Open("sqlite", ":memory:") round-trip: Save + FindByWindow works.
func TestOpenSQLiteRoundTrip(t *testing.T) {
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer s.Close()

	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, s.Save(Record{
		Timestamp:  now.Add(-5 * time.Second),
		Method:     "GET",
		Path:       "/ping",
		StatusCode: 200,
		DurationMs: 3.14,
	}))

	recs, err := s.FindByWindow(now.Add(-time.Minute), now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "GET", recs[0].Method)
	assert.Equal(t, "/ping", recs[0].Path)
	assert.Equal(t, 200, recs[0].StatusCode)
	assert.InDelta(t, 3.14, recs[0].DurationMs, 0.001)
}
