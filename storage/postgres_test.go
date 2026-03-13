package storage

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postgresDS returns the test DSN or skips the test if not configured.
func postgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set — skipping PostgreSQL integration tests")
	}
	return dsn
}

// TC-13: Save persists a record; FindByWindow retrieves it with correct fields.
func TestPostgresSaveAndFind(t *testing.T) {
	s, err := Open("postgres", postgresDSN(t))
	require.NoError(t, err)
	defer s.Close()
	cleanTable(t, s)

	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, s.Save(Record{
		Timestamp:  now.Add(-5 * time.Second),
		Method:     "POST",
		Path:       "/api/users",
		StatusCode: 201,
		DurationMs: 12.5,
	}))

	recs, err := s.FindByWindow(now.Add(-time.Minute), now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "POST", recs[0].Method)
	assert.Equal(t, "/api/users", recs[0].Path)
	assert.Equal(t, 201, recs[0].StatusCode)
	assert.InDelta(t, 12.5, recs[0].DurationMs, 0.001)
}

// TC-14: FindByWindow boundary: record at `from` included, record at `to` excluded.
func TestPostgresFindByWindowBoundary(t *testing.T) {
	s, err := Open("postgres", postgresDSN(t))
	require.NoError(t, err)
	defer s.Close()
	cleanTable(t, s)

	now := time.Now().UTC().Truncate(time.Millisecond)
	from := now.Add(-time.Minute)
	to := now

	require.NoError(t, s.Save(Record{Timestamp: from, Method: "GET", Path: "/at-from", StatusCode: 200, DurationMs: 1}))
	require.NoError(t, s.Save(Record{Timestamp: to, Method: "GET", Path: "/at-to", StatusCode: 200, DurationMs: 1}))

	recs, err := s.FindByWindow(from, to)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "/at-from", recs[0].Path)
}

// TC-15: FindByWindow with no matching records returns empty slice, not nil.
func TestPostgresFindByWindowEmpty(t *testing.T) {
	s, err := Open("postgres", postgresDSN(t))
	require.NoError(t, err)
	defer s.Close()
	cleanTable(t, s)

	now := time.Now().UTC()
	recs, err := s.FindByWindow(now.Add(-time.Minute), now)
	require.NoError(t, err)
	assert.NotNil(t, recs)
	assert.Len(t, recs, 0)
}

// TC-16: Close is idempotent — double close does not panic.
func TestPostgresCloseIdempotent(t *testing.T) {
	s, err := Open("postgres", postgresDSN(t))
	require.NoError(t, err)
	require.NoError(t, s.Close())
	// Second close may return an error (sql.DB returns "sql: database is closed")
	// but must not panic.
	_ = s.Close()
}

// TC-17: migratePG is idempotent — applying schema twice succeeds.
func TestPostgresMigrateIdempotent(t *testing.T) {
	s, err := Open("postgres", postgresDSN(t))
	require.NoError(t, err)
	defer s.Close()

	db := s.(*postgresStore).db
	require.NoError(t, migratePG(db))
}

// cleanTable truncates the requests table for test isolation.
func cleanTable(t *testing.T, s StoreReader) {
	t.Helper()
	db := s.(*postgresStore).db
	_, err := db.Exec(`TRUNCATE TABLE requests`)
	require.NoError(t, err)
}
