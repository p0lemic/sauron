package alerts

import "time"

// AlertRecord is one entry in the alert history.
type AlertRecord struct {
	Method      string     `json:"method"`
	Path        string     `json:"path"`
	CurrentP99  float64    `json:"current_p99"`
	BaselineP99 float64    `json:"baseline_p99"`
	Threshold   float64    `json:"threshold"`
	TriggeredAt time.Time  `json:"triggered_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
}
