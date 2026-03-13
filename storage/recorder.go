package storage

import (
	"fmt"
	"log"
	"sync"
	"time"
)

const defaultBufferSize = 1000

// Recorder accepts Records non-blockingly and persists them via a Store
// in a background goroutine.
type Recorder struct {
	store Store
	ch    chan Record
	wg    sync.WaitGroup
	once  sync.Once
}

// NewRecorder creates a Recorder backed by store.
// bufferSize controls the internal channel capacity (0 → default 1000).
func NewRecorder(store Store, bufferSize int) *Recorder {
	if bufferSize <= 0 {
		bufferSize = defaultBufferSize
	}
	r := &Recorder{
		store: store,
		ch:    make(chan Record, bufferSize),
	}
	r.wg.Add(1)
	go r.drain()
	return r
}

// Record enqueues rec for persistence. Never blocks: if the internal buffer
// is full the record is dropped and a warning is logged.
func (r *Recorder) Record(rec Record) {
	select {
	case r.ch <- rec:
	default:
		log.Println("storage: record buffer full, dropping record")
	}
}

// drain runs in a background goroutine, consuming records until the channel
// is closed by Close().
func (r *Recorder) drain() {
	defer r.wg.Done()
	for rec := range r.ch {
		if err := r.store.Save(rec); err != nil {
			log.Printf("storage: failed to save record: %v", err)
		}
	}
}

// Close drains the remaining records and stops the background goroutine.
// Waits up to 5 seconds; returns an error if the drain does not complete
// in time. Safe to call multiple times.
func (r *Recorder) Close() error {
	var closeErr error
	r.once.Do(func() {
		close(r.ch)
		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			closeErr = fmt.Errorf("storage: recorder close timed out after 5s")
		}
	})
	return closeErr
}
