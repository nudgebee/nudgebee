package observability

import (
	"fmt"
	"math"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// logs_aggregate: the number of log lines matching a filter, counted by the log backend
// itself, per group and per time bucket. A count read off a page of fetched lines covers
// only that page — on a busy service a few minutes of an hour — so it cannot answer "how
// many errors per pod in the last hour".
//
// FetchLogAggregate owns every rule that is the same for all backends: validation, the
// window and its buckets, the limit, ordering and the zero-result diagnosis. A backend
// only resolves the fields to group on and runs the count (LogAggregateSource).

// LogAggregateSource is a LogSource whose backend can count matching lines itself. A
// source that does not implement it cannot serve logs_aggregate, and its capabilities
// say so (supports_log_count_by).
type LogAggregateSource interface {
	// ResolveGroupByFields returns the provider field to group on for each name, in
	// order. Names arrive already mapped through the account's label mapping. An error
	// names the field that cannot be grouped on and, where the backend can list them,
	// the fields that can.
	ResolveGroupByFields(ctx *security.RequestContext, q LogAggregateQuery, names []string) ([]string, error)
	// AggregateLogs counts the lines matching q, per group and bucket.
	AggregateLogs(ctx *security.RequestContext, q LogAggregateQuery) (LogAggregateRows, error)
}

// LogAggregateQuery is what a LogAggregateSource counts.
type LogAggregateQuery struct {
	// Request is prepared as for QueryLogs: the where clause is in provider space,
	// Query holds the source's native filter built from it (GetQuery), and
	// StartTime/EndTime (unix ms) are the window to count, already aligned to Interval.
	Request FetchLogRequest
	// GroupBy are provider fields (ResolveGroupByFields' output); empty for one count.
	GroupBy []string
	// Interval is the bucket size; zero means one count per group over the window.
	Interval time.Duration
	// Limit is how many groups the caller keeps. A source that must cap groups in the
	// backend keeps at least this many, largest first, and reports how many it found in
	// GroupsTotal; a source that lists every group may ignore it.
	Limit int
}

// LogAggregateRows is a LogAggregateSource's answer.
type LogAggregateRows struct {
	// Groups has one series per group, Metric keyed by the provider fields of GroupBy.
	// With an Interval, one point per bucket stamped at the bucket's start (unix
	// seconds); without one, a single point. Lines with no value for a group field
	// belong to no group: they are counted in Ungrouped.
	Groups []Result
	// Total counts every matching line in the window.
	Total int64
	// Ungrouped counts the matching lines with no value for some group field, as the
	// backend measured it — never derived as Total minus the groups, which would turn
	// any undercount of the groups into a plausible-looking number here.
	Ungrouped int64
	// GroupsTotal is how many groups the backend found, for a source that returned
	// fewer than it found. It is ignored when smaller than len(Groups).
	GroupsTotal int
	// Complete is false when the backend stopped listing groups at a cap of its own.
	Complete bool
	// Query is the native counting query that ran, for display.
	Query string
}

const (
	logAggregateMaxGroupBy   = 3
	logAggregateDefaultLimit = 100
	logAggregateMaxLimit     = 1000
	// logAggregateMaxBuckets bounds the buckets per series. It sits under every
	// backend's own per-series cap (Loki: 11,000 points) and keeps an answer of a
	// hundred groups small enough to read.
	logAggregateMaxBuckets = 1000
)

// logAggregateSteps are the intervals a too-fine interval is widened to. The last one is
// also the largest interval accepted.
var logAggregateSteps = []time.Duration{
	time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

var (
	logAggregateMaxInterval = logAggregateSteps[len(logAggregateSteps)-1]
	// logAggregateMaxWindow is the longest window that still fits in buckets of the
	// largest interval; bounding it (and end_time) also keeps the window arithmetic
	// clear of int64 overflow.
	logAggregateMaxWindow = logAggregateMaxBuckets * logAggregateMaxInterval
	logAggregateMaxEndMs  = time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
)

// FetchLogAggregate counts the log lines matching req in the account's log backend.
func FetchLogAggregate(ctx *security.RequestContext, req FetchLogAggregateRequest) (FetchLogAggregateResult, error) {
	source, err := getLogSourceForAccount(ctx, req.AccountId, req.LogProvider, req.LogProviderSource)
	if err != nil {
		return FetchLogAggregateResult{}, err
	}
	return fetchLogAggregateFrom(ctx, source, req)
}

func fetchLogAggregateFrom(ctx *security.RequestContext, source LogSource, req FetchLogAggregateRequest) (FetchLogAggregateResult, error) {
	limit, requested, err := validateLogAggregateRequest(req)
	if err != nil {
		return FetchLogAggregateResult{}, err
	}
	aggSource, ok := source.(LogAggregateSource)
	if !ok {
		return FetchLogAggregateResult{}, fmt.Errorf("counting is not supported for %s; use logs_list", source.ProviderRef().Provider)
	}
	start, end, interval, err := fitLogAggregateWindow(req.StartTime, req.EndTime, requested)
	if err != nil {
		return FetchLogAggregateResult{}, err
	}

	fetchReq := FetchLogRequest{
		AccountId:         req.AccountId,
		LogProvider:       req.LogProvider,
		LogProviderSource: req.LogProviderSource,
		StartTime:         start,
		EndTime:           end,
		QueryRequest:      req.QueryRequest,
		Request:           req.Request,
	}
	filteringMap, expandedKeys, err := prepareLogRequestWhere(ctx, source, &fetchReq)
	if err != nil {
		return FetchLogAggregateResult{}, err
	}
	referencedLabels, referencedValues := snapshotReferencedWhere(source, fetchReq.QueryRequest.Where, expandedKeys)

	q := LogAggregateQuery{Request: fetchReq, Interval: interval, Limit: limit}
	if len(req.GroupBy) > 0 {
		if q.GroupBy, err = resolveLogAggregateGroupBy(ctx, aggSource, q, req.GroupBy, filteringMap); err != nil {
			return FetchLogAggregateResult{}, err
		}
	}

	rows, aggErr := aggSource.AggregateLogs(ctx, q)
	result := FetchLogAggregateResult{
		Series:    []Result{},
		Complete:  true,
		StartTime: start,
		EndTime:   end,
		Query:     rows.Query,
		Provider:  resolveResultLogProvider(ctx, req.AccountId, req.LogProvider, req.LogProviderSource),
	}
	if result.Query == "" {
		result.Query = fetchReq.Query
	}
	if interval > 0 {
		result.Interval = formatLogAggregateInterval(interval)
	}

	// A count of zero is an answer someone will act on ("no errors in the last hour"),
	// and it is also what a mistyped label or value produces. Always check before
	// returning one, as logs_query does for its validate_request callers.
	if aggErr != nil || rows.Total == 0 {
		if suggestion := diagnoseEmptyLogQuery(ctx, source, fetchReq, referencedLabels, referencedValues, filteringMap, aggErr, aggErr == nil); suggestion != "" {
			result.Suggestion = suggestion
			return result, nil
		}
	}
	if aggErr != nil {
		return FetchLogAggregateResult{}, aggErr
	}

	result.Series, result.GroupsTotal, result.GroupsOmitted = shapeLogAggregateGroups(rows, req.GroupBy, q.GroupBy, limit)
	result.Total = rows.Total
	result.Ungrouped = rows.Ungrouped
	result.Complete = rows.Complete
	var notes []string
	if interval != requested {
		notes = append(notes, fmt.Sprintf("interval widened from %s to %s to stay within %d buckets per series",
			formatLogAggregateInterval(requested), result.Interval, logAggregateMaxBuckets))
	}
	if !rows.Complete {
		notes = append(notes, "the backend found more groups than it could list in one call: groups_total is a lower bound")
	}
	result.SeriesNote = strings.Join(notes, "; ")
	return result, nil
}

// validateLogAggregateRequest checks the parts of req no backend is involved in, and
// returns the effective group limit and the requested interval.
func validateLogAggregateRequest(req FetchLogAggregateRequest) (int, time.Duration, error) {
	if req.StartTime <= 0 || req.EndTime <= req.StartTime || req.EndTime > logAggregateMaxEndMs {
		return 0, 0, fmt.Errorf("start_time and end_time (unix ms) are required, with end_time after start_time")
	}
	// Bounded in milliseconds first: a window of centuries overflows a Duration.
	windowMs := req.EndTime - req.StartTime
	if windowMs > logAggregateMaxWindow.Milliseconds() {
		return 0, 0, fmt.Errorf("the window is %d days; logs_aggregate counts at most %d days",
			windowMs/(24*time.Hour).Milliseconds(), int(logAggregateMaxWindow/(24*time.Hour)))
	}
	window := time.Duration(windowMs) * time.Millisecond
	if len(req.GroupBy) > logAggregateMaxGroupBy {
		return 0, 0, fmt.Errorf("group_by takes at most %d labels, got %d", logAggregateMaxGroupBy, len(req.GroupBy))
	}
	seen := map[string]bool{}
	for _, name := range req.GroupBy {
		if strings.TrimSpace(name) == "" {
			return 0, 0, fmt.Errorf("group_by has an empty label name")
		}
		if seen[name] {
			return 0, 0, fmt.Errorf("group_by names %q twice", name)
		}
		seen[name] = true
	}
	limit := req.Limit
	if limit <= 0 {
		limit = logAggregateDefaultLimit
	}
	limit = min(limit, logAggregateMaxLimit)
	interval, err := parseLogAggregateInterval(req.Interval)
	if err != nil {
		return 0, 0, err
	}
	// Buckets are aligned to whole intervals, so an interval longer than the window
	// would count far outside it (a 30d bucket around the last hour).
	if interval > window {
		return 0, 0, fmt.Errorf("interval %s is longer than the window (%s); omit interval for one count over the window",
			formatLogAggregateInterval(interval), window)
	}
	return limit, interval, nil
}

// resolveLogAggregateGroupBy maps each group_by name through the account's label mapping
// and asks the source for the field it groups on.
func resolveLogAggregateGroupBy(ctx *security.RequestContext, source LogAggregateSource, q LogAggregateQuery, names []string, filteringMap map[string]string) ([]string, error) {
	mapped := make([]string, len(names))
	for i, name := range names {
		mapped[i] = name
		if field := filteringMap[name]; field != "" {
			mapped[i] = field
		}
	}
	fields, err := source.ResolveGroupByFields(ctx, q, mapped)
	if err != nil {
		return nil, err
	}
	if len(fields) != len(names) {
		return nil, fmt.Errorf("resolving group_by: got %d fields for %d names", len(fields), len(names))
	}
	byField := map[string]string{}
	for i, field := range fields {
		if other, ok := byField[field]; ok {
			return nil, fmt.Errorf("group_by names %q and %q are the same field %q", other, names[i], field)
		}
		byField[field] = names[i]
	}
	return fields, nil
}

// shapeLogAggregateGroups relabels the source's groups with the caller's names, orders
// them largest first and keeps limit of them. It returns the kept series, how many groups
// matched and how many were left out.
func shapeLogAggregateGroups(rows LogAggregateRows, names, fields []string, limit int) ([]Result, int, int) {
	nameOf := make(map[string]string, len(fields))
	for i, field := range fields {
		nameOf[field] = names[i]
	}
	type group struct {
		series Result
		sum    float64
		key    string
	}
	groups := make([]group, 0, len(rows.Groups))
	for _, g := range rows.Groups {
		labels := make(map[string]string, len(fields))
		for field, value := range g.Metric {
			if name, ok := nameOf[field]; ok {
				labels[name] = value
			}
		}
		sum := 0.0
		for _, v := range g.Values {
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				sum += v
			}
		}
		keys := make([]string, 0, len(labels))
		for name, value := range labels {
			keys = append(keys, name+"="+value)
		}
		sort.Strings(keys)
		groups = append(groups, group{
			series: Result{Metric: labels, Timestamps: g.Timestamps, Values: g.Values},
			sum:    sum,
			key:    strings.Join(keys, ","),
		})
	}
	// Largest first; ties by labels, so the same counts always come back in the same order.
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].sum != groups[j].sum {
			return groups[i].sum > groups[j].sum
		}
		return groups[i].key < groups[j].key
	})

	total := max(rows.GroupsTotal, len(groups))
	kept := groups[:min(limit, len(groups))]
	series := make([]Result, len(kept))
	for i, g := range kept {
		series[i] = g.series
	}
	return series, total, total - len(series)
}

// fitLogAggregateWindow returns the window to count and the interval to count it in.
// Without an interval the window is counted as asked. With one, the interval is widened
// until the window holds at most logAggregateMaxBuckets buckets, and the window is widened
// to whole buckets (aligned to the Unix epoch, so 1d buckets start at midnight UTC): a
// bucket cut by the window's edge would count a part of its interval and read low.
func fitLogAggregateWindow(startMs, endMs int64, interval time.Duration) (int64, int64, time.Duration, error) {
	if interval == 0 {
		return startMs, endMs, 0, nil
	}
	for {
		step := interval.Milliseconds()
		alignedStart := startMs - startMs%step
		alignedEnd := endMs
		if rem := endMs % step; rem != 0 {
			alignedEnd += step - rem
		}
		if (alignedEnd-alignedStart)/step <= logAggregateMaxBuckets {
			return alignedStart, alignedEnd, interval, nil
		}
		wider := time.Duration(0)
		for _, s := range logAggregateSteps {
			if s > interval && time.Duration(endMs-startMs)*time.Millisecond <= s*logAggregateMaxBuckets {
				wider = s
				break
			}
		}
		if wider == 0 {
			return 0, 0, 0, fmt.Errorf("the window is too long to count in buckets: at most %d buckets of up to %s",
				logAggregateMaxBuckets, formatLogAggregateInterval(logAggregateSteps[len(logAggregateSteps)-1]))
		}
		interval = wider
	}
}

// parseLogAggregateInterval reads an interval: a Go duration (30s, 10m, 1h) or days (1d).
// Empty means no interval.
func parseLogAggregateInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		// Checked before converting: a float past the Duration range (Inf, 1e9) does
		// not convert to a defined value.
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, fmt.Errorf("interval %q: want a duration such as 30s, 10m, 1h or 1d", s)
		}
		if n > float64(logAggregateMaxInterval/(24*time.Hour)) {
			return 0, fmt.Errorf("interval %q: at most %s", s, formatLogAggregateInterval(logAggregateMaxInterval))
		}
		d = time.Duration(n * float64(24*time.Hour))
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("interval %q: want a duration such as 30s, 10m, 1h or 1d", s)
		}
	}
	if d < time.Second {
		return 0, fmt.Errorf("interval %q: must be at least 1s", s)
	}
	if d > logAggregateMaxInterval {
		return 0, fmt.Errorf("interval %q: at most %s", s, formatLogAggregateInterval(logAggregateMaxInterval))
	}
	return d.Truncate(time.Second), nil
}

// formatLogAggregateInterval writes an interval in the largest whole unit (1d, 3h, 10m, 90s).
func formatLogAggregateInterval(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	default:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
}
