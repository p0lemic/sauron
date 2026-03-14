package health

import (
	"net/http"
	"sync"
	"time"
)

// Status represents the state of the upstream.
type Status string

const (
	StatusUnknown  Status = "unknown"  // not enough checks yet
	StatusHealthy  Status = "healthy"  // 0 consecutive failures
	StatusDegraded Status = "degraded" // 1..threshold-1 consecutive failures
	StatusDown     Status = "down"     // >= threshold consecutive failures
)

// State is a snapshot of the current checker state.
type State struct {
	Status              Status    `json:"status"`
	LatencyMs           float64   `json:"latency_ms"`   // of last successful check; 0 if none
	LastCheck           time.Time `json:"last_check"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

// Checker performs periodic HEAD requests to a target URL and maintains state.
type Checker struct {
	target    string
	interval  time.Duration
	threshold int
	client    *http.Client

	mu       sync.RWMutex
	state    State
	stop     chan struct{}
	stopOnce sync.Once
}

// New creates a Checker. Zero values for interval/timeout/threshold use defaults
// (10s / 5s / 3). target is the full URL for HEAD requests.
func New(target string, interval, timeout time.Duration, threshold int) *Checker {
	if interval == 0 {
		interval = 10 * time.Second
	}
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if threshold == 0 {
		threshold = 3
	}
	return &Checker{
		target:    target,
		interval:  interval,
		threshold: threshold,
		client:    &http.Client{Timeout: timeout},
		state:     State{Status: StatusUnknown},
		stop:      make(chan struct{}),
	}
}

// Start begins the periodic ping loop in a background goroutine.
// An initial ping is performed immediately before the first interval elapses.
func (c *Checker) Start() {
	go c.run()
}

// Stop signals the ping loop to exit. Safe to call multiple times.
func (c *Checker) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
}

// State returns the current state snapshot.
func (c *Checker) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Checker) run() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.ping()
	for {
		select {
		case <-ticker.C:
			c.ping()
		case <-c.stop:
			return
		}
	}
}

func (c *Checker) ping() {
	start := time.Now()
	req, err := http.NewRequest(http.MethodHead, c.target, nil)
	if err != nil {
		c.recordFailure(start)
		return
	}
	resp, err := c.client.Do(req)
	latency := time.Since(start)
	if err != nil {
		c.recordFailure(time.Now())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		c.recordFailure(time.Now())
		return
	}
	c.recordSuccess(time.Now(), latency)
}

func (c *Checker) recordSuccess(t time.Time, latency time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = State{
		Status:              StatusHealthy,
		LatencyMs:           float64(latency.Microseconds()) / 1000,
		LastCheck:           t,
		ConsecutiveFailures: 0,
	}
}

func (c *Checker) recordFailure(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	failures := c.state.ConsecutiveFailures + 1
	status := StatusDegraded
	if failures >= c.threshold {
		status = StatusDown
	}
	c.state = State{
		Status:              status,
		LatencyMs:           c.state.LatencyMs, // preserve last successful latency
		LastCheck:           t,
		ConsecutiveFailures: failures,
	}
}
