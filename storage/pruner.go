package storage

import (
	"log"
	"sync"
	"time"
)

// DefaultPruneInterval is the production interval between cleanup cycles.
const DefaultPruneInterval = time.Hour

// Pruner periodically deletes records older than retention from store.
// A zero retention disables pruning entirely.
type Pruner struct {
	store     Store
	retention time.Duration
	interval  time.Duration

	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

// NewPruner returns a Pruner that deletes records older than retention every
// interval. Use storage.defaultPruneInterval (1h) as the production interval.
func NewPruner(store Store, retention, interval time.Duration) *Pruner {
	return &Pruner{
		store:     store,
		retention: retention,
		interval:  interval,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start launches the background pruning goroutine. Safe to call once.
func (p *Pruner) Start() {
	go func() {
		defer close(p.doneCh)
		if p.retention == 0 {
			return
		}
		// Run once immediately on start to clear existing backlog.
		p.prune()
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.prune()
			case <-p.stopCh:
				return
			}
		}
	}()
}

// Stop shuts down the background goroutine gracefully. Safe to call once.
func (p *Pruner) Stop() {
	p.once.Do(func() { close(p.stopCh) })
	<-p.doneCh
}

func (p *Pruner) prune() {
	before := time.Now().Add(-p.retention)
	n, err := p.store.Prune(before)
	if err != nil {
		log.Printf("storage: pruner error: %v", err)
		return
	}
	if n > 0 {
		log.Printf("storage: pruned %d records older than %s", n, p.retention)
	}
}
