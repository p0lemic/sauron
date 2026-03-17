package storage

import (
	"fmt"
	"time"
)

// Reader queries persisted request records and inner spans.
type Reader interface {
	FindByWindow(from, to time.Time) ([]Record, error)
	// FindRecent returns up to limit records in [from, to), newest first.
	FindRecent(from, to time.Time, limit int) ([]Record, error)
	// FindByTraceID returns all proxy-level records with the given trace_id, ordered by timestamp ASC.
	FindByTraceID(traceID string) ([]Record, error)
	// FindSpansByTraceID returns all inner spans for a trace_id, ordered by start_time ASC.
	FindSpansByTraceID(traceID string) ([]InnerSpan, error)
}

// StoreReader combines write and read access; sqliteStore implements both.
type StoreReader interface {
	Store
	Reader
}

// FindRecent returns up to limit records in [from, to), newest first.
func (s *sqliteStore) FindRecent(from, to time.Time, limit int) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id
		 FROM requests
		 WHERE timestamp >= ? AND timestamp < ?
		 ORDER BY timestamp DESC
		 LIMIT ?`,
		from.UTC().Format(time.RFC3339Nano),
		to.UTC().Format(time.RFC3339Nano),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindRecent: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var ts string
		if err := rows.Scan(&ts, &r.Method, &r.Path, &r.StatusCode, &r.DurationMs, &r.TraceID, &r.SpanID, &r.ParentSpanID); err != nil {
			return nil, fmt.Errorf("storage: FindRecent scan: %w", err)
		}
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: FindRecent rows: %w", err)
	}
	if records == nil {
		records = []Record{}
	}
	return records, nil
}

// FindByWindow returns all records with timestamp in [from, to), ordered by timestamp.
func (s *sqliteStore) FindByWindow(from, to time.Time) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id
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
		if err := rows.Scan(&ts, &r.Method, &r.Path, &r.StatusCode, &r.DurationMs, &r.TraceID, &r.SpanID, &r.ParentSpanID); err != nil {
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

// FindSpansByTraceID returns all inner spans for the given trace_id, ordered by start_time ASC.
func (s *sqliteStore) FindSpansByTraceID(traceID string) ([]InnerSpan, error) {
	rows, err := s.db.Query(
		`SELECT trace_id, span_id, parent_span_id, name, kind, start_time, duration_ms, attributes, status
		 FROM spans
		 WHERE trace_id = ?
		 ORDER BY start_time ASC`,
		traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindSpansByTraceID: %w", err)
	}
	defer rows.Close()

	var out []InnerSpan
	for rows.Next() {
		var sp InnerSpan
		var ts, attrs string
		if err := rows.Scan(&sp.TraceID, &sp.SpanID, &sp.ParentSpanID, &sp.Name, &sp.Kind, &ts, &sp.DurationMs, &attrs, &sp.Status); err != nil {
			return nil, fmt.Errorf("storage: FindSpansByTraceID scan: %w", err)
		}
		sp.StartTime, _ = time.Parse(time.RFC3339Nano, ts)
		sp.Attributes = decodeAttrs(attrs)
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: FindSpansByTraceID rows: %w", err)
	}
	if out == nil {
		out = []InnerSpan{}
	}
	return out, nil
}

// FindByTraceID returns all records with the given trace_id, ordered by timestamp ASC.
func (s *sqliteStore) FindByTraceID(traceID string) ([]Record, error) {
	rows, err := s.db.Query(
		`SELECT timestamp, method, path, status_code, duration_ms, trace_id, span_id, parent_span_id
		 FROM requests
		 WHERE trace_id = ?
		 ORDER BY timestamp ASC`,
		traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: FindByTraceID: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var ts string
		if err := rows.Scan(&ts, &r.Method, &r.Path, &r.StatusCode, &r.DurationMs, &r.TraceID, &r.SpanID, &r.ParentSpanID); err != nil {
			return nil, fmt.Errorf("storage: FindByTraceID scan: %w", err)
		}
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: FindByTraceID rows: %w", err)
	}
	if records == nil {
		records = []Record{}
	}
	return records, nil
}
