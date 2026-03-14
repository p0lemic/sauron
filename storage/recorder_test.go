package storage_test

import (
	"sync"
	"testing"
	"time"

	"api-profiler/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncStore is an in-memory Store used in tests.
type syncStore struct {
	mu      sync.Mutex
	records []storage.Record
	saveErr error
	delay   time.Duration
}

func (s *syncStore) Save(r storage.Record) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
	return nil
}

func (s *syncStore) Prune(_ time.Time) (int64, error) { return 0, nil }
func (s *syncStore) Close() error                     { return nil }

func (s *syncStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// notifyStore notifies via a channel each time Save is called.
type notifyStore struct {
	saved chan storage.Record
}

func (s *notifyStore) Save(r storage.Record) error {
	s.saved <- r
	return nil
}
func (s *notifyStore) Prune(_ time.Time) (int64, error) { return 0, nil }
func (s *notifyStore) Close() error                     { return nil }

// TC-12: Close drains all enqueued records before returning.
func TestRecorderCloseDrainsChannel(t *testing.T) {
	store := &syncStore{}
	rec := storage.NewRecorder(store, 100)

	for i := 0; i < 50; i++ {
		rec.Record(storage.Record{Method: "GET", Path: "/x", StatusCode: 200})
	}
	require.NoError(t, rec.Close())
	assert.Equal(t, 50, store.len())
}

// TC-11: Store.Save failure does not panic or block the caller.
func TestRecorderSaveErrorDoesNotPanic(t *testing.T) {
	store := &syncStore{saveErr: assert.AnError}
	rec := storage.NewRecorder(store, 10)

	// Should not panic.
	rec.Record(storage.Record{Method: "GET", Path: "/err", StatusCode: 200})
	require.NoError(t, rec.Close())
}

// TC-10: Buffer full — second record dropped, caller never blocks.
func TestRecorderBufferFull(t *testing.T) {
	// Store with 200ms delay so the drain goroutine is slow.
	store := &syncStore{delay: 200 * time.Millisecond}
	rec := storage.NewRecorder(store, 1)

	start := time.Now()
	rec.Record(storage.Record{Method: "GET", Path: "/1", StatusCode: 200})
	rec.Record(storage.Record{Method: "GET", Path: "/2", StatusCode: 200}) // should be dropped
	elapsed := time.Since(start)

	// The call must return without blocking (well under 1ms).
	assert.Less(t, elapsed, 10*time.Millisecond, "Record() must not block when buffer is full")

	// Give the drain goroutine time to process one record, then close.
	time.Sleep(250 * time.Millisecond)
	require.NoError(t, rec.Close())

	// Only 1 record saved (the second was dropped).
	assert.Equal(t, 1, store.len())
}

// TC-09 (availability): Record is queryable within 100ms after Record() returns.
func TestRecorderAvailableWithin100ms(t *testing.T) {
	ns := &notifyStore{saved: make(chan storage.Record, 1)}
	rec := storage.NewRecorder(ns, 100)
	defer rec.Close()

	before := time.Now()
	rec.Record(storage.Record{Method: "GET", Path: "/fast", StatusCode: 200, DurationMs: 1})

	select {
	case <-ns.saved:
		assert.Less(t, time.Since(before), 100*time.Millisecond)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("record not persisted within 100ms")
	}
}

// Concurrent records: 100 goroutines each send 10 records — all 1000 arrive.
func TestRecorderConcurrent(t *testing.T) {
	store := &syncStore{}
	rec := storage.NewRecorder(store, 2000)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				rec.Record(storage.Record{Method: "GET", Path: "/x", StatusCode: 200})
			}
		}()
	}
	wg.Wait()
	require.NoError(t, rec.Close())
	assert.Equal(t, 1000, store.len())
}

// Close is idempotent — calling it twice must not panic.
func TestRecorderCloseIdempotent(t *testing.T) {
	store := &syncStore{}
	rec := storage.NewRecorder(store, 10)
	require.NoError(t, rec.Close())
	require.NoError(t, rec.Close()) // second call must be safe
}

// TC-08: With zero records and no store interaction, Close returns nil.
func TestRecorderEmptyClose(t *testing.T) {
	store := &syncStore{}
	rec := storage.NewRecorder(store, 10)
	require.NoError(t, rec.Close())
	assert.Equal(t, 0, store.len())
}
