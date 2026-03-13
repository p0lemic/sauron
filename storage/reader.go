package storage

import (
	"fmt"
	"time"
)

// Reader queries persisted request records.
type Reader interface {
	FindByWindow(from, to time.Time) ([]Record, error)
}

// StoreReader combines write and read access; sqliteStore implements both.
type StoreReader interface {
	Store
	Reader
}

// FindByWindow returns all records with timestamp in [from, to), ordered by timestamp.
func (s *sqliteStore) FindByWindow(from, to time.Time) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms
		 FROM requests
		 WHERE timestamp >= ? AND timestamp < ?
		 ORDER BY timestamp`,
		from.UTC().Format(time.RFC3339Nano),
		to.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindByWindow: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var ts string
		if err := rows.Scan(&ts, &r.Method, &r.Path, &r.StatusCode, &r.DurationMs); err != nil {
			return nil, fmt.Errorf("storage: FindByWindow scan: %w", err)
		}
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: FindByWindow rows: %w", err)
	}
	if records == nil {
		records = []Record{}
	}
	return records, nil
}
