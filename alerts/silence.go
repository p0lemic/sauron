package alerts

import "time"

// Silence represents a timed suppression of alerts for one endpoint.
type Silence struct {
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	ExpiresAt time.Time `json:"expires_at"`
}
