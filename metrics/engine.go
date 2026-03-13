package metrics

import (
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

// BaselineStat holds baseline latency statistics for one method+path.
type BaselineStat struct {
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	BaselineP99 float64 `json:"baseline_p99"`
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

	stats := make([]BaselineStat, 0, len(groups))
	for k, durations := range groups {
		sort.Float64s(durations)
		stats = append(stats, BaselineStat{
			Method:      k.method,
			Path:        k.path,
			BaselineP99: pct(durations, 99),
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
	now := time.Now().Truncate(minute)
	from := now.Add(-buckets * minute)

	records, err := e.reader.FindByWindow(from, now)
	if err != nil {
		return nil, err
	}

	groups := make(map[time.Time][]float64, buckets)
	for _, r := range records {
		if r.Method != method || r.Path != path {
			continue
		}
		bucket := r.Timestamp.Truncate(minute)
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
