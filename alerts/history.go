package alerts

import "time"

// AlertRecord is one entry in the alert history.
type AlertRecord struct {
	Kind               string     `json:"kind"`
	Method             string     `json:"method"`
	Path               string     `json:"path"`
	CurrentP99         float64    `json:"current_p99"`
	BaselineP99        float64    `json:"baseline_p99"`
	Threshold          float64    `json:"threshold"`
	ErrorRate          float64    `json:"error_rate"`
	ErrorRateThreshold float64    `json:"error_rate_threshold"`
	CurrentRPS         float64    `json:"current_rps"`
	BaselineRPS        float64    `json:"baseline_rps"`
	DropPct            float64    `json:"drop_pct"`
	TriggeredAt        time.Time  `json:"triggered_at"`
	ResolvedAt         *time.Time `json:"resolved_at"`
}
