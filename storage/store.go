package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Record represents the metadata of a single proxied request.
type Record struct {
	Timestamp    time.Time `json:"timestamp"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	StatusCode   int       `json:"status_code"`
	DurationMs   float64   `json:"duration_ms"`
	TraceID      string    `json:"trace_id"`
	SpanID       string    `json:"span_id"`
	ParentSpanID string    `json:"parent_span_id"`
}

// Store persists request records and inner spans.
type Store interface {
	Save(r Record) error
	// SaveSpan persists an application-level inner span.
	SaveSpan(s InnerSpan) error
	// Prune deletes all records with timestamp strictly before before.
	// Returns the number of rows deleted.
	Prune(before time.Time) (int64, error)
	Close() error
}

type sqliteStore struct {
	db *sql.DB
}

// openSQLite opens or creates a SQLite database at dsn and applies the schema.
func openSQLite(dsn string) (StoreReader, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: opening %q: %w", dsn, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: connecting to %q: %w", dsn, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: applying schema: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS requests (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp   DATETIME NOT NULL,
			method      TEXT     NOT NULL,
			path        TEXT     NOT NULL,
			status_code INTEGER  NOT NULL,
			duration_ms REAL     NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_path_method ON requests (method, path)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_timestamp   ON requests (timestamp)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	if err := sqliteAddColumnIfNotExists(db, "requests", "trace_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := sqliteAddColumnIfNotExists(db, "requests", "span_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := sqliteAddColumnIfNotExists(db, "requests", "parent_span_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_requests_trace_id ON requests (trace_id)`); err != nil {
		return err
	}
	// spans table for inner (application-level) spans
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS spans (
		id            INTEGER  PRIMARY KEY AUTOINCREMENT,
		trace_id      TEXT     NOT NULL,
		span_id       TEXT     NOT NULL,
		parent_span_id TEXT    NOT NULL DEFAULT '',
		name          TEXT     NOT NULL,
		kind          TEXT     NOT NULL DEFAULT '',
		start_time    DATETIME NOT NULL,
		duration_ms   REAL     NOT NULL,
		attributes    TEXT     NOT NULL DEFAULT '{}',
		status        TEXT     NOT NULL DEFAULT 'ok'
	)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_spans_trace_id ON spans (trace_id)`); err != nil {
		return err
	}
	return nil
}

// sqliteAddColumnIfNotExists runs ALTER TABLE ADD COLUMN and ignores the error
// if the column already exists (SQLite reports "duplicate column name").
func sqliteAddColumnIfNotExists(db *sql.DB, table, column, def string) error {
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

func (s *sqliteStore) Save(r Record) error {
	_, err := s.db.Exec(
		`INSERT INTO requests (timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Timestamp.UTC().Format(time.RFC3339Nano),
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

func (s *sqliteStore) SaveSpan(sp InnerSpan) error {
	_, err := s.db.Exec(
		`INSERT INTO spans (trace_id, span_id, parent_span_id, name, kind, start_time, duration_ms, attributes, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.TraceID,
		sp.SpanID,
		sp.ParentSpanID,
		sp.Name,
		sp.Kind,
		sp.StartTime.UTC().Format(time.RFC3339Nano),
		sp.DurationMs,
		encodeAttrs(sp.Attributes),
		sp.Status,
	)
	if err != nil {
		return fmt.Errorf("storage: SaveSpan: %w", err)
	}
	return nil
}

func (s *sqliteStore) Prune(before time.Time) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM requests WHERE timestamp < ?`,
		before.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("storage: prune: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: prune rows affected: %w", err)
	}
	return n, nil
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
