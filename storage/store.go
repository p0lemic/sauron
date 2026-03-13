package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Record represents the metadata of a single proxied request.
type Record struct {
	Timestamp  time.Time
	Method     string
	Path       string
	StatusCode int
	DurationMs float64
}

// Store persists request records.
type Store interface {
	Save(r Record) error
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
	return nil
}

func (s *sqliteStore) Save(r Record) error {
	_, err := s.db.Exec(
		`INSERT INTO requests (timestamp, method, path, status_code, duration_ms)
		 VALUES (?, ?, ?, ?, ?)`,
		r.Timestamp.UTC().Format(time.RFC3339Nano),
		r.Method,
		r.Path,
		r.StatusCode,
		r.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("storage: insert: %w", err)
	}
	return nil
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
