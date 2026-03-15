package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type postgresStore struct {
	db *sql.DB
}

// openPostgres opens a PostgreSQL connection, applies the schema, and returns
// a StoreReader. dsn is a standard PostgreSQL connection string.
func openPostgres(dsn string) (StoreReader, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: opening postgres %q: %w", dsn, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: connecting to postgres: %w", err)
	}
	if err := migratePG(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: applying postgres schema: %w", err)
	}
	return &postgresStore{db: db}, nil
}

func migratePG(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS requests (
			id          BIGSERIAL        PRIMARY KEY,
			timestamp   TIMESTAMPTZ      NOT NULL,
			method      TEXT             NOT NULL,
			path        TEXT             NOT NULL,
			status_code INTEGER          NOT NULL,
			duration_ms DOUBLE PRECISION NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_path_method ON requests (method, path)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_timestamp   ON requests (timestamp)`,
		`ALTER TABLE requests ADD COLUMN IF NOT EXISTS trace_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE requests ADD COLUMN IF NOT EXISTS span_id  TEXT NOT NULL DEFAULT ''`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (s *postgresStore) Save(r Record) error {
	_, err := s.db.Exec(
		`INSERT INTO requests (timestamp, method, path, status_code, duration_ms, trace_id, span_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		r.Timestamp.UTC(),
		r.Method,
		r.Path,
		r.StatusCode,
		r.DurationMs,
		r.TraceID,
		r.SpanID,
	)
	if err != nil {
		return fmt.Errorf("storage: insert: %w", err)
	}
	return nil
}

func (s *postgresStore) FindByWindow(from, to time.Time) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id
		 FROM requests
		 WHERE timestamp >= $1 AND timestamp < $2
		 ORDER BY timestamp`,
		from.UTC(),
		to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: query: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		var rec Record
		var ts time.Time
		if err := rows.Scan(&ts, &rec.Method, &rec.Path, &rec.StatusCode, &rec.DurationMs, &rec.TraceID, &rec.SpanID); err != nil {
			return nil, fmt.Errorf("storage: scan: %w", err)
		}
		rec.Timestamp = ts.UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *postgresStore) FindRecent(from, to time.Time, limit int) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id
		 FROM requests
		 WHERE timestamp >= $1 AND timestamp < $2
		 ORDER BY timestamp DESC
		 LIMIT $3`,
		from.UTC(),
		to.UTC(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindRecent: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		var rec Record
		var ts time.Time
		if err := rows.Scan(&ts, &rec.Method, &rec.Path, &rec.StatusCode, &rec.DurationMs, &rec.TraceID, &rec.SpanID); err != nil {
			return nil, fmt.Errorf("storage: FindRecent scan: %w", err)
		}
		rec.Timestamp = ts.UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *postgresStore) Prune(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM requests WHERE timestamp < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("storage: prune: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: prune rows affected: %w", err)
	}
	return n, nil
}

func (s *postgresStore) Close() error {
	return s.db.Close()
}
