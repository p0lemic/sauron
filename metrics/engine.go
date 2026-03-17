package metrics

import (
	"fmt"
	"math"
	"sort"
	"time"

	"api-profiler/storage"
)

// Engine computes latency statistics from stored request records.
type Engine struct {
	reader storage.Reader
	window time.Duration
}

// NewEngine creates an Engine backed by reader.
// window is the lookback duration for each calculation (e.g. 60s).
func NewEngine(reader storage.Reader, window time.Duration) *Engine {
	return &Engine{reader: reader, window: window}
}

// EndpointStat holds the computed latency statistics for one method+path.
type EndpointStat struct {
	Method string  `json:"method"`
	Path   string  `json:"path"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
	Count  int     `json:"count"`
}

// Endpoints returns stats for all endpoints active in the engine's configured
// window, sorted by P99 descending (slowest endpoints first).
// Returns an empty (non-nil) slice when there are no records.
func (e *Engine) Endpoints() ([]EndpointStat, error) {
	return e.EndpointsForWindow(e.window)
}

// EndpointsForWindow computes stats using window instead of the engine default.
func (e *Engine) EndpointsForWindow(window time.Duration) ([]EndpointStat, error) {
	now := time.Now()
	return e.EndpointsForRange(now.Add(-window), now)
}

// EndpointsForRange computes stats for an explicit [from, to] time range.
func (e *Engine) EndpointsForRange(from, to time.Time) ([]EndpointStat, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	return endpointsFromRecords(records), nil
}

func endpointsFromRecords(records []storage.Record) []EndpointStat {
	type key struct{ method, path string }
	groups := make(map[key][]float64)
	for _, r := range records {
		k := key{r.Method, r.Path}
		groups[k] = append(groups[k], r.DurationMs)
	}
	stats := make([]EndpointStat, 0, len(groups))
	for k, durations := range groups {
		sort.Float64s(durations)
		stats = append(stats, EndpointStat{
			Method: k.method,
			Path:   k.path,
			P50:    pct(durations, 50),
			P95:    pct(durations, 95),
			P99:    pct(durations, 99),
			Count:  len(durations),
		})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].P99 > stats[j].P99 })
	return stats
}

// BaselineStat holds baseline latency and throughput statistics for one method+path.
type BaselineStat struct {
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	BaselineP99 float64 `json:"baseline_p99"`
	BaselineRPS float64 `json:"baseline_rps"` // average req/s over the baseline window
	SampleCount int     `json:"sample_count"`
}

// Baseline computes the P99 baseline for each endpoint over the last n complete
// windows: [now-(n+1)*window, now-window]. Sorted by BaselineP99 descending.
func (e *Engine) Baseline(n int) ([]BaselineStat, error) {
	now := time.Now()
	from := now.Add(-time.Duration(n+1) * e.window)
	to := now.Add(-e.window)
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}

	type key struct{ method, path string }
	groups := make(map[key][]float64)
	for _, r := range records {
		k := key{r.Method, r.Path}
		groups[k] = append(groups[k], r.DurationMs)
	}

	baselineWindowSecs := float64(n) * e.window.Seconds()
	stats := make([]BaselineStat, 0, len(groups))
	for k, durations := range groups {
		sort.Float64s(durations)
		stats = append(stats, BaselineStat{
			Method:      k.method,
			Path:        k.path,
			BaselineP99: pct(durations, 99),
			BaselineRPS: float64(len(durations)) / baselineWindowSecs,
			SampleCount: len(durations),
		})
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].BaselineP99 > stats[j].BaselineP99
	})

	return stats, nil
}

// currentWindow is the fixed lookback duration used for rps_current.
const currentWindow = 10 * time.Second

// ThroughputStat holds throughput statistics for one method+path.
type ThroughputStat struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	RPSCurrent float64 `json:"rps_current"`
	RPSAvg     float64 `json:"rps_avg"`
	TotalCount int     `json:"total_count"`
}

// Throughput returns throughput stats for all endpoints active in the current
// window, sorted by RPSAvg descending. A single DB read is performed; rps_current
// is computed in-memory from records in the last 10 seconds.
func (e *Engine) Throughput() ([]ThroughputStat, error) {
	now := time.Now()
	return e.throughputForRange(now.Add(-e.window), now, true)
}

// ThroughputForRange computes throughput stats for an explicit [from, to] range.
// rps_current is set to 0 for historical ranges (no "last 10s" concept).
func (e *Engine) ThroughputForRange(from, to time.Time) ([]ThroughputStat, error) {
	return e.throughputForRange(from, to, false)
}

func (e *Engine) throughputForRange(from, to time.Time, liveRPS bool) ([]ThroughputStat, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}

	windowSecs := to.Sub(from).Seconds()
	if windowSecs <= 0 {
		windowSecs = 1
	}

	var currentCutoff time.Time
	if liveRPS {
		currentCutoff = to.Add(-currentWindow)
	}

	type counts struct{ total, recent int }
	type key struct{ method, path string }
	groups := make(map[key]*counts)
	for _, r := range records {
		k := key{r.Method, r.Path}
		if groups[k] == nil {
			groups[k] = &counts{}
		}
		groups[k].total++
		if liveRPS && !r.Timestamp.Before(currentCutoff) {
			groups[k].recent++
		}
	}

	stats := make([]ThroughputStat, 0, len(groups))
	for k, c := range groups {
		var rpsCurrent float64
		if liveRPS {
			rpsCurrent = float64(c.recent) / currentWindow.Seconds()
		}
		stats = append(stats, ThroughputStat{
			Method:     k.method,
			Path:       k.path,
			RPSCurrent: rpsCurrent,
			RPSAvg:     float64(c.total) / windowSecs,
			TotalCount: c.total,
		})
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].RPSAvg > stats[j].RPSAvg
	})

	return stats, nil
}

// ErrorStat holds the error rate statistics for one method+path.
type ErrorStat struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	ErrorRate  float64 `json:"error_rate"`
	ErrorCount int     `json:"error_count"`
	TotalCount int     `json:"total_count"`
}

// Errors returns error rate stats for all endpoints active in the current window,
// sorted by ErrorRate descending. Endpoints with 0% error rate are included.
func (e *Engine) Errors() ([]ErrorStat, error) {
	now := time.Now()
	return e.ErrorsForRange(now.Add(-e.window), now)
}

// ErrorsForRange computes error stats for an explicit [from, to] time range.
func (e *Engine) ErrorsForRange(from, to time.Time) ([]ErrorStat, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	return errorsFromRecords(records), nil
}

func errorsFromRecords(records []storage.Record) []ErrorStat {
	type counts struct{ total, errors int }
	type key struct{ method, path string }
	groups := make(map[key]*counts)
	for _, r := range records {
		k := key{r.Method, r.Path}
		if groups[k] == nil {
			groups[k] = &counts{}
		}
		groups[k].total++
		if r.StatusCode >= 400 {
			groups[k].errors++
		}
	}
	stats := make([]ErrorStat, 0, len(groups))
	for k, c := range groups {
		stats = append(stats, ErrorStat{
			Method:     k.method,
			Path:       k.path,
			ErrorRate:  float64(c.errors) / float64(c.total) * 100.0,
			ErrorCount: c.errors,
			TotalCount: c.total,
		})
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].ErrorRate != stats[j].ErrorRate {
			return stats[i].ErrorRate > stats[j].ErrorRate
		}
		return stats[i].TotalCount > stats[j].TotalCount
	})
	return stats
}

// HistogramBounds are the fixed upper-bound latency buckets in milliseconds.
var HistogramBounds = []float64{10, 25, 50, 100, 250, 500, 1000, 2500}

// HistogramBucket is one cumulative bucket. Le = -1 represents +Inf.
type HistogramBucket struct {
	Le    float64 `json:"le"`
	Count int     `json:"count"`
}

// HistogramStat holds cumulative latency buckets for a window.
type HistogramStat struct {
	Buckets    []HistogramBucket `json:"buckets"`
	TotalCount int               `json:"total_count"`
}

// Histogram returns cumulative latency buckets for the current window.
// If method and path are both non-empty, only records for that endpoint are counted.
func (e *Engine) Histogram(method, path string) (HistogramStat, error) {
	now := time.Now()
	return e.HistogramForRange(method, path, now.Add(-e.window), now)
}

// HistogramForRange returns cumulative latency buckets for an explicit [from, to] range.
func (e *Engine) HistogramForRange(method, path string, from, to time.Time) (HistogramStat, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return HistogramStat{}, err
	}

	counts := make([]int, len(HistogramBounds)+1)
	total := 0
	for _, r := range records {
		if method != "" && path != "" && (r.Method != method || r.Path != path) {
			continue
		}
		total++
		for i, bound := range HistogramBounds {
			if r.DurationMs <= bound {
				counts[i]++
			}
		}
		counts[len(HistogramBounds)]++
	}

	buckets := make([]HistogramBucket, len(HistogramBounds)+1)
	for i, bound := range HistogramBounds {
		buckets[i] = HistogramBucket{Le: bound, Count: counts[i]}
	}
	buckets[len(HistogramBounds)] = HistogramBucket{Le: -1, Count: counts[len(HistogramBounds)]}

	return HistogramStat{Buckets: buckets, TotalCount: total}, nil
}

// BucketStat holds the P99 for one time bucket.
type BucketStat struct {
	Ts  time.Time `json:"ts"`
	P99 float64   `json:"p99"`
}

// Latency returns 60 one-minute P99 buckets for the given endpoint covering the
// last 60 minutes. Buckets with no data have P99 = 0. The slice always has
// exactly 60 elements, ordered oldest first.
func (e *Engine) Latency(method, path string) ([]BucketStat, error) {
	const buckets = 60
	minute := time.Minute
	actualNow := time.Now().UTC()
	now := actualNow.Truncate(minute)
	from := now.Add(-buckets * minute)

	records, err := e.reader.FindByWindow(from, actualNow)
	if err != nil {
		return nil, err
	}

	groups := make(map[time.Time][]float64, buckets)
	for _, r := range records {
		if r.Method != method || r.Path != path {
			continue
		}
		bucket := r.Timestamp.UTC().Truncate(minute)
		groups[bucket] = append(groups[bucket], r.DurationMs)
	}

	result := make([]BucketStat, buckets)
	for i := 0; i < buckets; i++ {
		ts := from.Add(time.Duration(i) * minute)
		result[i] = BucketStat{Ts: ts}
		if durs, ok := groups[ts]; ok {
			sort.Float64s(durs)
			result[i].P99 = pct(durs, 99)
		}
	}
	return result, nil
}

// SummaryStat holds aggregated global statistics across all endpoints.
type SummaryStat struct {
	TotalRequests   int     `json:"total_requests"`
	GlobalErrorRate float64 `json:"global_error_rate"`
	GlobalP99       float64 `json:"global_p99"`
	ActiveEndpoints int     `json:"active_endpoints"`
}

// Summary returns aggregated global statistics for the current window.
func (e *Engine) Summary() (SummaryStat, error) {
	now := time.Now()
	return e.SummaryForRange(now.Add(-e.window), now)
}

// SummaryForRange returns aggregated global statistics for an explicit [from, to] range.
func (e *Engine) SummaryForRange(from, to time.Time) (SummaryStat, error) {
	endpoints, err := e.EndpointsForRange(from, to)
	if err != nil {
		return SummaryStat{}, err
	}
	errors, err := e.ErrorsForRange(from, to)
	if err != nil {
		return SummaryStat{}, err
	}

	var totalRequests, totalErrors int
	var globalP99 float64
	for _, ep := range endpoints {
		totalRequests += ep.Count
		if ep.P99 > globalP99 {
			globalP99 = ep.P99
		}
	}
	for _, er := range errors {
		totalErrors += er.ErrorCount
	}

	var errorRate float64
	if totalRequests > 0 {
		errorRate = math.Round(float64(totalErrors)/float64(totalRequests)*100*10) / 10
	}

	return SummaryStat{
		TotalRequests:   totalRequests,
		GlobalErrorRate: errorRate,
		GlobalP99:       globalP99,
		ActiveEndpoints: len(endpoints),
	}, nil
}

// TableRow holds combined per-endpoint stats for the dashboard table.
type TableRow struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	P50        float64 `json:"p50"`
	P95        float64 `json:"p95"`
	P99        float64 `json:"p99"`
	RPSCurrent float64 `json:"rps_current"`
	ErrorRate  float64 `json:"error_rate"`
	Count      int     `json:"count"`
}

// Table returns combined per-endpoint stats (latency + throughput + errors) for
// the current window, sorted by P99 descending.
func (e *Engine) Table() ([]TableRow, error) {
	now := time.Now()
	return e.tableForRange(now.Add(-e.window), now, true)
}

// TableForRange returns combined per-endpoint stats for an explicit [from, to] range.
func (e *Engine) TableForRange(from, to time.Time) ([]TableRow, error) {
	return e.tableForRange(from, to, false)
}

func (e *Engine) tableForRange(from, to time.Time, liveRPS bool) ([]TableRow, error) {
	endpoints, err := e.EndpointsForRange(from, to)
	if err != nil {
		return nil, err
	}
	throughput, err := e.throughputForRange(from, to, liveRPS)
	if err != nil {
		return nil, err
	}
	errors, err := e.ErrorsForRange(from, to)
	if err != nil {
		return nil, err
	}

	type key struct{ method, path string }

	rpsMap := make(map[key]float64, len(throughput))
	for _, t := range throughput {
		rpsMap[key{t.Method, t.Path}] = t.RPSCurrent
	}
	errMap := make(map[key]float64, len(errors))
	for _, er := range errors {
		errMap[key{er.Method, er.Path}] = er.ErrorRate
	}

	rows := make([]TableRow, 0, len(endpoints))
	for _, ep := range endpoints {
		k := key{ep.Method, ep.Path}
		rows = append(rows, TableRow{
			Method:     ep.Method,
			Path:       ep.Path,
			P50:        ep.P50,
			P95:        ep.P95,
			P99:        ep.P99,
			RPSCurrent: rpsMap[k],
			ErrorRate:  errMap[k],
			Count:      ep.Count,
		})
	}
	return rows, nil
}

// StatusGroup holds aggregated counts for one HTTP status class.
type StatusGroup struct {
	Class string  `json:"class"` // "2xx", "3xx", "4xx", "5xx"
	Count int     `json:"count"`
	Rate  float64 `json:"rate"` // percentage of total (0–100, 1 decimal)
}

var statusClasses = []string{"2xx", "3xx", "4xx", "5xx"}

// StatusBreakdown returns counts and rates per status class for the engine's window.
// Always returns 4 groups in order: 2xx, 3xx, 4xx, 5xx.
func (e *Engine) StatusBreakdown() ([]StatusGroup, error) {
	now := time.Now()
	return e.StatusBreakdownForRange(now.Add(-e.window), now)
}

// StatusBreakdownForRange returns counts and rates per status class for [from, to).
func (e *Engine) StatusBreakdownForRange(from, to time.Time) ([]StatusGroup, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, 4)
	for _, c := range statusClasses {
		counts[c] = 0
	}
	for _, r := range records {
		switch {
		case r.StatusCode >= 500:
			counts["5xx"]++
		case r.StatusCode >= 400:
			counts["4xx"]++
		case r.StatusCode >= 300:
			counts["3xx"]++
		default:
			counts["2xx"]++
		}
	}
	total := len(records)
	groups := make([]StatusGroup, len(statusClasses))
	for i, c := range statusClasses {
		cnt := counts[c]
		var rate float64
		if total > 0 {
			rate = math.Round(float64(cnt)/float64(total)*100*10) / 10
		}
		groups[i] = StatusGroup{Class: c, Count: cnt, Rate: rate}
	}
	return groups, nil
}

// StatusBreakdownForEndpoint returns status breakdown for a single endpoint
// within the engine's default window.
func (e *Engine) StatusBreakdownForEndpoint(method, path string) ([]StatusGroup, error) {
	now := time.Now()
	return e.StatusBreakdownForEndpointRange(method, path, now.Add(-e.window), now)
}

// StatusBreakdownForEndpointRange returns status breakdown for a single endpoint
// within [from, to).
func (e *Engine) StatusBreakdownForEndpointRange(method, path string, from, to time.Time) ([]StatusGroup, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, 4)
	for _, c := range statusClasses {
		counts[c] = 0
	}
	total := 0
	for _, r := range records {
		if r.Method != method || r.Path != path {
			continue
		}
		total++
		switch {
		case r.StatusCode >= 500:
			counts["5xx"]++
		case r.StatusCode >= 400:
			counts["4xx"]++
		case r.StatusCode >= 300:
			counts["3xx"]++
		default:
			counts["2xx"]++
		}
	}
	groups := make([]StatusGroup, len(statusClasses))
	for i, c := range statusClasses {
		cnt := counts[c]
		var rate float64
		if total > 0 {
			rate = math.Round(float64(cnt)/float64(total)*100*10) / 10
		}
		groups[i] = StatusGroup{Class: c, Count: cnt, Rate: rate}
	}
	return groups, nil
}

const maxSlowestRequests = 100

// SlowestRequests returns the n slowest individual requests in the engine's window,
// sorted by duration_ms descending. n is clamped to [1, 100].
func (e *Engine) SlowestRequests(n int) ([]storage.Record, error) {
	now := time.Now()
	return e.SlowestRequestsForRange(now.Add(-e.window), now, n)
}

// SlowestRequestsForRange returns the n slowest individual requests in [from, to),
// sorted by duration_ms descending. n is clamped to [1, 100].
func (e *Engine) SlowestRequestsForRange(from, to time.Time, n int) ([]storage.Record, error) {
	if n < 1 {
		n = 1
	}
	if n > maxSlowestRequests {
		n = maxSlowestRequests
	}
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].DurationMs > records[j].DurationMs
	})
	if n < len(records) {
		records = records[:n]
	}
	if records == nil {
		records = []storage.Record{}
	}
	return records, nil
}

const maxRequests = 1000

// Requests returns the most recent n requests within the engine's default window.
// n is clamped to [1, 1000].
func (e *Engine) Requests(n int) ([]storage.Record, error) {
	now := time.Now()
	return e.RequestsForRange(now.Add(-e.window), now, n)
}

// RequestsForRange returns the most recent n requests in [from, to).
// n is clamped to [1, 1000].
func (e *Engine) RequestsForRange(from, to time.Time, n int) ([]storage.Record, error) {
	if n < 1 {
		n = 1
	}
	if n > maxRequests {
		n = maxRequests
	}
	return e.reader.FindRecent(from, to, n)
}

// Slowest returns the top n endpoints by P99 descending within the engine's window.
// If fewer than n endpoints exist, all are returned. n must be > 0.
func (e *Engine) Slowest(n int) ([]EndpointStat, error) {
	all, err := e.Endpoints()
	if err != nil {
		return nil, err
	}
	if n < len(all) {
		all = all[:n]
	}
	return all, nil
}

// Window returns the engine's configured lookback duration.
func (e *Engine) Window() time.Duration { return e.window }

// ApdexStat holds Apdex score and component counts for one endpoint.
// Apdex = -1 when there are no records for the endpoint.
type ApdexStat struct {
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Apdex      float64 `json:"apdex"`
	Satisfied  int     `json:"satisfied"`
	Tolerating int     `json:"tolerating"`
	Frustrated int     `json:"frustrated"`
	Total      int     `json:"total"`
}

// Apdex computes Apdex scores for all endpoints using threshold tMs (ms).
// Uses the engine's default window. Sorted by Apdex ascending (most problematic first).
func (e *Engine) Apdex(tMs float64) ([]ApdexStat, error) {
	now := time.Now()
	return e.ApdexForRange(tMs, now.Add(-e.window), now)
}

// ApdexForRange computes Apdex scores for an explicit [from, to] range.
func (e *Engine) ApdexForRange(tMs float64, from, to time.Time) ([]ApdexStat, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	return apdexFromRecords(records, tMs), nil
}

func apdexFromRecords(records []storage.Record, tMs float64) []ApdexStat {
	type counts struct{ satisfied, tolerating, frustrated int }
	type key struct{ method, path string }
	groups := make(map[key]*counts)
	for _, r := range records {
		k := key{r.Method, r.Path}
		if groups[k] == nil {
			groups[k] = &counts{}
		}
		switch {
		case r.DurationMs <= tMs:
			groups[k].satisfied++
		case r.DurationMs <= 4*tMs:
			groups[k].tolerating++
		default:
			groups[k].frustrated++
		}
	}
	stats := make([]ApdexStat, 0, len(groups))
	for k, c := range groups {
		total := c.satisfied + c.tolerating + c.frustrated
		var score float64
		if total == 0 {
			score = -1
		} else {
			score = math.Round((float64(c.satisfied)+float64(c.tolerating)/2)/float64(total)*1000) / 1000
		}
		stats = append(stats, ApdexStat{
			Method:     k.method,
			Path:       k.path,
			Apdex:      score,
			Satisfied:  c.satisfied,
			Tolerating: c.tolerating,
			Frustrated: c.frustrated,
			Total:      total,
		})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Apdex < stats[j].Apdex })
	return stats
}

// ErrorFingerprint holds aggregated error stats for one (method, path, status_code) tuple.
type ErrorFingerprint struct {
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	Count      int       `json:"count"`
	Rate       float64   `json:"rate"`      // % of total requests for this endpoint
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	P50Ms      float64   `json:"p50_ms"`
	P95Ms      float64   `json:"p95_ms"`
	IsNew      bool      `json:"is_new"`
}

// ErrorFingerprints returns error fingerprints for the engine's current window.
// Sorted by count descending.
func (e *Engine) ErrorFingerprints() ([]ErrorFingerprint, error) {
	now := time.Now()
	return e.ErrorFingerprintsForRange(now.Add(-e.window), now)
}

// ErrorFingerprintsForRange returns error fingerprints for an explicit [from, to] range.
func (e *Engine) ErrorFingerprintsForRange(from, to time.Time) ([]ErrorFingerprint, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}
	return errorFingerprintsFromRecords(records, from, to), nil
}

func errorFingerprintsFromRecords(records []storage.Record, from, to time.Time) []ErrorFingerprint {
	type endpointKey struct{ method, path string }
	type fpKey struct {
		method, path string
		statusCode   int
	}
	type fpData struct {
		durations  []float64
		timestamps []time.Time
	}

	// Count totals per endpoint (for rate calculation).
	endpointTotals := make(map[endpointKey]int)
	for _, r := range records {
		endpointTotals[endpointKey{r.Method, r.Path}]++
	}

	// Group 4xx/5xx records by fingerprint.
	fpGroups := make(map[fpKey]*fpData)
	for _, r := range records {
		if r.StatusCode < 400 {
			continue
		}
		k := fpKey{r.Method, r.Path, r.StatusCode}
		if fpGroups[k] == nil {
			fpGroups[k] = &fpData{}
		}
		fpGroups[k].durations = append(fpGroups[k].durations, r.DurationMs)
		fpGroups[k].timestamps = append(fpGroups[k].timestamps, r.Timestamp)
	}

	// Threshold for is_new: first_seen in the last 10% of the window.
	windowLen := to.Sub(from)
	newThreshold := from.Add(time.Duration(float64(windowLen) * 0.9))

	result := make([]ErrorFingerprint, 0, len(fpGroups))
	for k, data := range fpGroups {
		total := endpointTotals[endpointKey{k.method, k.path}]
		var rate float64
		if total > 0 {
			rate = math.Round(float64(len(data.durations))/float64(total)*100*10) / 10
		}

		sort.Float64s(data.durations)

		firstSeen := data.timestamps[0]
		lastSeen := data.timestamps[0]
		for _, ts := range data.timestamps[1:] {
			if ts.Before(firstSeen) {
				firstSeen = ts
			}
			if ts.After(lastSeen) {
				lastSeen = ts
			}
		}

		result = append(result, ErrorFingerprint{
			Method:     k.method,
			Path:       k.path,
			StatusCode: k.statusCode,
			Count:      len(data.durations),
			Rate:       rate,
			FirstSeen:  firstSeen,
			LastSeen:   lastSeen,
			P50Ms:      pct(data.durations, 50),
			P95Ms:      pct(data.durations, 95),
			IsNew:      firstSeen.After(newThreshold),
		})
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Count > result[j].Count })
	return result
}

// ── US-47: Traffic Heatmap ────────────────────────────────────────────────────

// HeatmapPoint is one cell in the 7×24 traffic grid.
type HeatmapPoint struct {
	Weekday int     `json:"weekday"` // 0=Sunday .. 6=Saturday
	Hour    int     `json:"hour"`    // 0..23
	Value   float64 `json:"value"`
}

// HeatmapResult is the response payload for GET /metrics/heatmap.
type HeatmapResult struct {
	Metric string         `json:"metric"`
	Cells  []HeatmapPoint `json:"cells"` // always 168 entries (7×24)
	Max    float64        `json:"max"`
}

// Heatmap aggregates request data for the given [from, to) range into a 7×24
// weekday/hour grid. metric must be "rps" or "error_rate".
func (e *Engine) Heatmap(metric string, from, to time.Time) (*HeatmapResult, error) {
	if metric != "rps" && metric != "error_rate" {
		return nil, fmt.Errorf("unsupported metric %q: must be rps or error_rate", metric)
	}
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}

	type cellData struct{ count, errors int }
	var cells [7][24]cellData

	for _, r := range records {
		wd := int(r.Timestamp.Weekday())
		h := r.Timestamp.Hour()
		cells[wd][h].count++
		if r.StatusCode >= 400 {
			cells[wd][h].errors++
		}
	}

	// Each (weekday, hour) slot covers 1 hour per week cycle.
	// If the window spans multiple weeks, the slot recurs; we normalize by that count.
	windowSecs := to.Sub(from).Seconds()
	windowWeeks := windowSecs / (7 * 24 * 3600)
	if windowWeeks < 1 {
		windowWeeks = 1
	}
	slotSecs := windowWeeks * 3600

	result := &HeatmapResult{Metric: metric, Cells: make([]HeatmapPoint, 0, 168)}
	var maxVal float64
	for wd := 0; wd < 7; wd++ {
		for h := 0; h < 24; h++ {
			d := cells[wd][h]
			var val float64
			if d.count > 0 {
				switch metric {
				case "error_rate":
					val = math.Round(float64(d.errors)/float64(d.count)*100*100) / 100
				default:
					val = float64(d.count) / slotSecs
				}
			}
			if val > maxVal {
				maxVal = val
			}
			result.Cells = append(result.Cells, HeatmapPoint{Weekday: wd, Hour: h, Value: val})
		}
	}
	result.Max = maxVal
	return result, nil
}

// ── US-48: Statistical Anomaly Detection ─────────────────────────────────────

// AnomalyScore holds statistical anomaly data for one endpoint.
type AnomalyScore struct {
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	CurrentP99  float64 `json:"current_p99"`
	MeanP99     float64 `json:"mean_p99"`
	StddevP99   float64 `json:"stddev_p99"`
	ZScore      float64 `json:"z_score"`
	HasBaseline bool    `json:"has_baseline"`
}

// AnomalyScores computes z-scores for all active endpoints by comparing the
// current window P99 against the mean and stddev of the last n complete
// historical windows. Endpoints with fewer than 3 historical data points
// return HasBaseline=false. Results are sorted by ZScore descending.
func (e *Engine) AnomalyScores(n int) ([]AnomalyScore, error) {
	if n < 3 {
		n = 3
	}
	now := time.Now()

	// Current P99 per endpoint.
	current, err := e.Table()
	if err != nil {
		return nil, err
	}
	if len(current) == 0 {
		return []AnomalyScore{}, nil
	}

	// Fetch all historical windows in one DB read, then bucket in Go.
	bigFrom := now.Add(-time.Duration(n+1) * e.window)
	bigTo := now.Add(-e.window)
	historical, err := e.reader.FindByWindow(bigFrom, bigTo)
	if err != nil {
		return nil, err
	}

	type key struct{ method, path string }

	// Group durations by (endpoint, window-index).
	windowDurs := make(map[key]map[int][]float64)
	for _, r := range historical {
		// window index 0 = most recent historical window, n-1 = oldest.
		age := bigTo.Sub(r.Timestamp)
		idx := int(age / e.window)
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			continue
		}
		k := key{r.Method, r.Path}
		if windowDurs[k] == nil {
			windowDurs[k] = make(map[int][]float64)
		}
		windowDurs[k][idx] = append(windowDurs[k][idx], r.DurationMs)
	}

	// Compute per-window P99s, then mean+stddev.
	type stats struct{ mean, stddev float64; count int }
	endpointStats := make(map[key]stats)
	for k, byWindow := range windowDurs {
		var p99s []float64
		for _, durs := range byWindow {
			sort.Float64s(durs)
			p99s = append(p99s, pct(durs, 99))
		}
		if len(p99s) == 0 {
			continue
		}
		var sum float64
		for _, v := range p99s {
			sum += v
		}
		mean := sum / float64(len(p99s))
		var variance float64
		for _, v := range p99s {
			d := v - mean
			variance += d * d
		}
		stddev := math.Sqrt(variance / float64(len(p99s)))
		endpointStats[k] = stats{mean: mean, stddev: stddev, count: len(p99s)}
	}

	scores := make([]AnomalyScore, 0, len(current))
	for _, row := range current {
		k := key{row.Method, row.Path}
		st, ok := endpointStats[k]
		if !ok || st.count < 3 {
			scores = append(scores, AnomalyScore{
				Method: row.Method, Path: row.Path,
				CurrentP99: math.Round(row.P99*100) / 100,
				HasBaseline: false,
			})
			continue
		}
		var z float64
		if st.stddev > 0 {
			z = (row.P99 - st.mean) / st.stddev
		}
		scores = append(scores, AnomalyScore{
			Method:      row.Method,
			Path:        row.Path,
			CurrentP99:  math.Round(row.P99*100) / 100,
			MeanP99:     math.Round(st.mean*100) / 100,
			StddevP99:   math.Round(st.stddev*100) / 100,
			ZScore:      math.Round(z*100) / 100,
			HasBaseline: true,
		})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].ZScore > scores[j].ZScore })
	return scores, nil
}

// TraceSummary holds aggregated information about a distributed trace.
type TraceSummary struct {
	TraceID         string    `json:"trace_id"`
	StartTime       time.Time `json:"start_time"`
	EndTime         time.Time `json:"end_time"`
	TotalDurationMs float64   `json:"total_duration_ms"`
	SpanCount       int       `json:"span_count"`
	HasErrors       bool      `json:"has_errors"`
}

// Traces returns a summary of all unique traces active in the engine's window,
// ordered by start_time descending (most recent first). Traces with an empty
// trace_id are excluded.
func (e *Engine) Traces() ([]TraceSummary, error) {
	now := time.Now()
	return e.TracesForRange(now.Add(-e.window), now)
}

// TracesForRange returns trace summaries for records in [from, to).
func (e *Engine) TracesForRange(from, to time.Time) ([]TraceSummary, error) {
	records, err := e.reader.FindByWindow(from, to)
	if err != nil {
		return nil, err
	}

	type agg struct {
		start     time.Time
		end       time.Time
		spanCount int
		hasErrors bool
	}
	m := make(map[string]*agg)
	order := []string{} // preserve first-seen order for determinism

	for _, r := range records {
		if r.TraceID == "" {
			continue
		}
		a, ok := m[r.TraceID]
		if !ok {
			a = &agg{start: r.Timestamp, end: r.Timestamp}
			m[r.TraceID] = a
			order = append(order, r.TraceID)
		}
		if r.Timestamp.Before(a.start) {
			a.start = r.Timestamp
		}
		spanEnd := r.Timestamp.Add(time.Duration(r.DurationMs * float64(time.Millisecond)))
		if spanEnd.After(a.end) {
			a.end = spanEnd
		}
		a.spanCount++
		if r.StatusCode >= 400 {
			a.hasErrors = true
		}
	}

	out := make([]TraceSummary, 0, len(m))
	for _, id := range order {
		a := m[id]
		totalMs := float64(a.end.Sub(a.start).Microseconds()) / 1000
		out = append(out, TraceSummary{
			TraceID:         id,
			StartTime:       a.start,
			EndTime:         a.end,
			TotalDurationMs: totalMs,
			SpanCount:       a.spanCount,
			HasErrors:       a.hasErrors,
		})
	}

	// Sort by start time descending (newest first).
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartTime.After(out[j].StartTime)
	})
	return out, nil
}

// TraceSpans returns all proxy-level spans for a given trace_id, ordered by timestamp ASC.
func (e *Engine) TraceSpans(traceID string) ([]storage.Record, error) {
	return e.reader.FindByTraceID(traceID)
}

// InnerSpans returns all application-level spans for a given trace_id, ordered by start_time ASC.
func (e *Engine) InnerSpans(traceID string) ([]storage.InnerSpan, error) {
	return e.reader.FindSpansByTraceID(traceID)
}

// pct computes the p-th percentile (0–100) of a sorted slice using the
// nearest-rank method (1-indexed, ceil). Single-element slices return that element.
func pct(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	idx := int(math.Ceil(p/100.0*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}
