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
		`ALTER TABLE requests ADD COLUMN IF NOT EXISTS trace_id       TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE requests ADD COLUMN IF NOT EXISTS span_id        TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE requests ADD COLUMN IF NOT EXISTS parent_span_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_requests_trace_id ON requests (trace_id)`,
		`CREATE TABLE IF NOT EXISTS spans (
			id             BIGSERIAL        PRIMARY KEY,
			trace_id       TEXT             NOT NULL,
			span_id        TEXT             NOT NULL,
			parent_span_id TEXT             NOT NULL DEFAULT '',
			name           TEXT             NOT NULL,
			kind           TEXT             NOT NULL DEFAULT '',
			start_time     TIMESTAMPTZ      NOT NULL,
			duration_ms    DOUBLE PRECISION NOT NULL,
			attributes     TEXT             NOT NULL DEFAULT '{}',
			status         TEXT             NOT NULL DEFAULT 'ok'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_spans_trace_id ON spans (trace_id)`,
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
		`INSERT INTO requests (timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		r.Timestamp.UTC(),
		r.Method,
		r.Path,
		r.StatusCode,
		r.DurationMs,
		r.TraceID,
		r.SpanID,
		r.ParentSpanID,
	)
	if err != nil {
		return fmt.Errorf("storage: insert: %w", err)
	}
	return nil
}

func (s *postgresStore) FindByWindow(from, to time.Time) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id
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
		if err := rows.Scan(&ts, &rec.Method, &rec.Path, &rec.StatusCode, &rec.DurationMs, &rec.TraceID, &rec.SpanID, &rec.ParentSpanID); err != nil {
			return nil, fmt.Errorf("storage: scan: %w", err)
		}
		rec.Timestamp = ts.UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *postgresStore) FindRecent(from, to time.Time, limit int) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id
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
		if err := rows.Scan(&ts, &rec.Method, &rec.Path, &rec.StatusCode, &rec.DurationMs, &rec.TraceID, &rec.SpanID, &rec.ParentSpanID); err != nil {
			return nil, fmt.Errorf("storage: FindRecent scan: %w", err)
		}
		rec.Timestamp = ts.UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *postgresStore) FindByTraceID(traceID string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id
		 FROM requests
		 WHERE trace_id = $1
		 ORDER BY timestamp ASC`,
		traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindByTraceID: %w", err)
	}
	defer rows.Close()

	out := []Record{}
	for rows.Next() {
		var rec Record
		var ts time.Time
		if err := rows.Scan(&ts, &rec.Method, &rec.Path, &rec.StatusCode, &rec.DurationMs, &rec.TraceID, &rec.SpanID, &rec.ParentSpanID); err != nil {
			return nil, fmt.Errorf("storage: FindByTraceID scan: %w", err)
		}
		rec.Timestamp = ts.UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *postgresStore) SaveSpan(sp InnerSpan) error {
	_, err := s.db.Exec(
		`INSERT INTO spans (trace_id, span_id, parent_span_id, name, kind, start_time, duration_ms, attributes, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		sp.TraceID,
		sp.SpanID,
		sp.ParentSpanID,
		sp.Name,
		sp.Kind,
		sp.StartTime.UTC(),
		sp.DurationMs,
		encodeAttrs(sp.Attributes),
		sp.Status,
	)
	if err != nil {
		return fmt.Errorf("storage: SaveSpan: %w", err)
	}
	return nil
}

func (s *postgresStore) FindSpansByTraceID(traceID string) ([]InnerSpan, error) {
	rows, err := s.db.Query(
		`SELECT trace_id, span_id, parent_span_id, name, kind, start_time, duration_ms, attributes, status
		 FROM spans
		 WHERE trace_id = $1
		 ORDER BY start_time ASC`,
		traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindSpansByTraceID: %w", err)
	}
	defer rows.Close()

	out := []InnerSpan{}
	for rows.Next() {
		var sp InnerSpan
		var ts time.Time
		var attrs string
		if err := rows.Scan(&sp.TraceID, &sp.SpanID, &sp.ParentSpanID, &sp.Name, &sp.Kind, &ts, &sp.DurationMs, &attrs, &sp.Status); err != nil {
			return nil, fmt.Errorf("storage: FindSpansByTraceID scan: %w", err)
		}
		sp.StartTime = ts.UTC()
		sp.Attributes = decodeAttrs(attrs)
		out = append(out, sp)
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
