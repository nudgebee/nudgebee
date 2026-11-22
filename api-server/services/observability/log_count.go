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

// Count mode of logs_list / logs_query: the number of log lines matching the where filter,
// counted by the log backend itself, per group and per time bucket. A count read off a page
// of fetched lines covers only that page — on a busy service a few minutes of an hour — so
// it cannot answer "how many errors per pod in the last hour".
//
// A request asks for it with query_request.count. FetchLogs then hands it to fetchLogCount,
// which owns every rule that is the same for all backends: validation, the window and its
// buckets, the limit, ordering and the zero-result diagnosis. A backend only resolves the
// fields to group on, writes the native counting queries and runs them (LogCountSource).
//
// The canonical count is a shortcut for a hand-written one: PlanLogCount writes the native
// queries a caller could have sent as `query`, RunLogCount runs and reads them.

// LogCountSource is a LogSource whose backend can count matching lines itself. A source
// that does not implement it refuses count mode, and its capabilities say so
// (supports_log_count).
type LogCountSource interface {
	// ResolveGroupByFields returns the provider field to group on for each name, in
	// order. Names arrive already mapped through the account's label mapping. An error
	// names the field that cannot be grouped on and, where the backend can list them,
	// the fields that can.
	ResolveGroupByFields(ctx *security.RequestContext, q LogCountQuery, names []string) ([]string, error)
	// PlanLogCount writes the native queries that count q.
	PlanLogCount(ctx *security.RequestContext, q LogCountQuery) (LogCountPlan, error)
	// RunLogCount runs a plan PlanLogCount wrote for q and reads the counts.
	RunLogCount(ctx *security.RequestContext, q LogCountQuery, plan LogCountPlan) (LogCountRows, error)
}

// LogCountQuery is what a LogCountSource counts.
type LogCountQuery struct {
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

// LogCountPlan is the native work that counts a LogCountQuery.
type LogCountPlan struct {
	// Queries are the native queries the count runs, in the provider's own language and
	// in the order they run: what the answer reports as its query, and what
	// logs_get_query previews.
	Queries []string
}

// LogCountRows is a LogCountSource's answer.
type LogCountRows struct {
	// Groups has one series per group, Metric keyed by the provider fields of GroupBy.
	// With an Interval, one point per bucket stamped at the bucket's start (unix
	// seconds); without one, a single point. Lines with no value for a group field
	// belong to no group: they are counted in Ungrouped.
	Groups []Result
	// Total counts every matching line in the window.
	Total int64
	// Ungrouped counts the matching lines with no value for some group field, from the
	// backend's own exact counts — never as Total minus groups the backend capped or
	// estimated, which would turn their shortfall into a plausible-looking number here.
	Ungrouped int64
	// GroupsTotal is how many groups the backend found, for a source that returned
	// fewer than it found. It is ignored when smaller than len(Groups).
	GroupsTotal int
	// Complete is false when the backend stopped listing groups at a cap of its own.
	Complete bool
}

const (
	logCountMaxGroupBy   = 3
	logCountDefaultLimit = 100
	logCountMaxLimit     = 1000
	// logCountMaxBuckets bounds the buckets per series. It sits under every backend's
	// own per-series cap (Loki: 11,000 points) and keeps an answer of a hundred groups
	// small enough to read.
	logCountMaxBuckets = 1000
)

// logCountSteps are the intervals a too-fine interval is widened to. The last one is also
// the largest interval accepted.
var logCountSteps = []time.Duration{
	time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

var (
	logCountMaxInterval = logCountSteps[len(logCountSteps)-1]
	// logCountMaxWindow is the longest window that still fits in buckets of the largest
	// interval; bounding it (and end_time) also keeps the window arithmetic clear of
	// int64 overflow.
	logCountMaxWindow = logCountMaxBuckets * logCountMaxInterval
	logCountMaxEndMs  = time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
)

// logCountPrep is a count-mode request checked, windowed and resolved: what both running
// the count (fetchLogCount) and previewing it (GetLogsQuery) need.
type logCountPrep struct {
	source           LogCountSource
	q                LogCountQuery
	names            []string
	requested        time.Duration
	filteringMap     map[string]string
	referencedLabels map[string]struct{}
	referencedValues map[string][]whereFieldValue
}

// prepareLogCount turns a count-mode request into the query its source counts.
func prepareLogCount(ctx *security.RequestContext, source LogSource, req FetchLogRequest) (logCountPrep, error) {
	countReq := *req.QueryRequest.Count
	if err := refuseLineOnlyFields(req); err != nil {
		return logCountPrep{}, err
	}
	limit, requested, err := validateLogCountRequest(req.StartTime, req.EndTime, countReq)
	if err != nil {
		return logCountPrep{}, err
	}
	countSource, ok := source.(LogCountSource)
	if !ok {
		return logCountPrep{}, fmt.Errorf("counting is not supported for %s; drop query_request.count to fetch lines", source.ProviderRef().Provider)
	}
	start, end, interval, err := fitLogCountWindow(req.StartTime, req.EndTime, requested)
	if err != nil {
		return logCountPrep{}, err
	}
	req.StartTime, req.EndTime = start, end

	filteringMap, expandedKeys, err := prepareLogRequestWhere(ctx, source, &req)
	if err != nil {
		return logCountPrep{}, err
	}
	prep := logCountPrep{
		source:       countSource,
		q:            LogCountQuery{Request: req, Interval: interval, Limit: limit},
		names:        countReq.GroupBy,
		requested:    requested,
		filteringMap: filteringMap,
	}
	prep.referencedLabels, prep.referencedValues = snapshotReferencedWhere(source, req.QueryRequest.Where, expandedKeys)
	if len(countReq.GroupBy) > 0 {
		if prep.q.GroupBy, err = resolveLogCountGroupBy(ctx, countSource, prep.q, countReq.GroupBy, filteringMap); err != nil {
			return logCountPrep{}, err
		}
	}
	return prep, nil
}

// fetchLogCount answers a count-mode request.
func fetchLogCount(ctx *security.RequestContext, source LogSource, req FetchLogRequest) (FetchLogsResult, error) {
	prep, err := prepareLogCount(ctx, source, req)
	if err != nil {
		return FetchLogsResult{}, err
	}
	plan, err := prep.source.PlanLogCount(ctx, prep.q)
	if err != nil {
		return FetchLogsResult{}, err
	}
	rows, runErr := prep.source.RunLogCount(ctx, prep.q, plan)

	q := prep.q
	result := FetchLogsResult{
		Mode:     LogResultModeCount,
		Logs:     []OutputLog{},
		Series:   []Result{},
		Query:    strings.Join(plan.Queries, "\n"),
		Provider: resolveResultLogProvider(ctx, req.AccountId, req.LogProvider, req.LogProviderSource),
		Count:    &LogCountDetails{Complete: true, StartTime: q.Request.StartTime, EndTime: q.Request.EndTime},
	}
	if result.Query == "" {
		result.Query = q.Request.Query
	}
	if q.Interval > 0 {
		result.Count.Interval = formatLogCountInterval(q.Interval)
	}

	// A count of zero is an answer someone will act on ("no errors in the last hour"),
	// and it is also what a mistyped label or value produces. Count mode always checks
	// before returning one, whether or not the request set validate_request.
	if runErr != nil || rows.Total == 0 {
		if suggestion := diagnoseEmptyLogQuery(ctx, source, q.Request, prep.referencedLabels, prep.referencedValues, prep.filteringMap, runErr, runErr == nil); suggestion != "" {
			result.Suggestion = suggestion
			if runErr == nil {
				result.setCountTotal(0)
			}
			return result, nil
		}
	}
	if runErr != nil {
		return FetchLogsResult{Mode: LogResultModeCount, Query: result.Query, Provider: result.Provider}, runErr
	}

	result.Series, result.Count.GroupsTotal, result.Count.GroupsOmitted = shapeLogCountGroups(rows, prep.names, q.GroupBy, q.Limit)
	if q.Interval > 0 {
		for i := range result.Series {
			result.Series[i] = fillLogCountBuckets(result.Series[i], q.Request.StartTime, q.Request.EndTime, q.Interval)
		}
	}
	result.setCountTotal(rows.Total)
	result.Count.Ungrouped = rows.Ungrouped
	result.Count.Complete = rows.Complete
	result.Truncated = result.Count.GroupsOmitted > 0 || !rows.Complete
	var notes []string
	if q.Interval != prep.requested {
		notes = append(notes, fmt.Sprintf("interval widened from %s to %s to stay within %d buckets per series",
			formatLogCountInterval(prep.requested), result.Count.Interval, logCountMaxBuckets))
	}
	if !rows.Complete {
		notes = append(notes, "the backend found more groups than it could list in one call: groups_total is a lower bound")
	}
	result.SeriesNote = strings.Join(notes, "; ")
	return result, nil
}

// previewLogCount writes a count-mode request's native queries without running them, for
// logs_get_query.
func previewLogCount(ctx *security.RequestContext, source LogSource, req FetchLogRequest) (OutputLogQuery, error) {
	prep, err := prepareLogCount(ctx, source, req)
	if err != nil {
		return OutputLogQuery{}, err
	}
	plan, err := prep.source.PlanLogCount(ctx, prep.q)
	if err != nil {
		return OutputLogQuery{}, err
	}
	return OutputLogQuery{Query: strings.Join(plan.Queries, "\n")}, nil
}

// setCountTotal records a count-mode total. It is exact, and present whenever the count
// ran, so a zero is a zero rather than an absent total.
func (r *FetchLogsResult) setCountTotal(total int64) {
	r.Total = &total
	r.TotalRelation = "eq"
}

// refuseLineOnlyFields rejects the request fields that only mean something for lines, so
// a count-mode request never has one silently ignored.
func refuseLineOnlyFields(req FetchLogRequest) error {
	switch {
	case strings.TrimSpace(req.Query) != "":
		return fmt.Errorf("query_request.count counts the where filter; a hand-written query does its own counting, so send one or the other")
	case req.Limit > 0:
		return fmt.Errorf("limit is a number of lines; set query_request.count.limit for the number of groups")
	case req.Offset > 0:
		return fmt.Errorf("offset pages through lines and does not apply to query_request.count")
	case len(req.SortFields) > 0:
		return fmt.Errorf("sort_fields orders lines; counts come back largest first")
	case req.StepInterval > 0:
		return fmt.Errorf("step_interval is for hand-written metric queries; set query_request.count.interval instead")
	case req.IncludeRaw:
		return fmt.Errorf("include_raw returns a hand-written query's raw fragments and does not apply to query_request.count")
	}
	return nil
}

// validateLogCountRequest checks the parts of a count-mode request no backend is involved
// in, and returns the effective group limit and the requested interval.
func validateLogCountRequest(startMs, endMs int64, countReq LogCountRequest) (int, time.Duration, error) {
	if startMs <= 0 || endMs <= startMs || endMs > logCountMaxEndMs {
		return 0, 0, fmt.Errorf("counting needs start_time and end_time (unix ms), with end_time after start_time")
	}
	// Bounded in milliseconds first: a window of centuries overflows a Duration.
	windowMs := endMs - startMs
	if windowMs > logCountMaxWindow.Milliseconds() {
		return 0, 0, fmt.Errorf("the window is %d days; counting covers at most %d days",
			windowMs/(24*time.Hour).Milliseconds(), int(logCountMaxWindow/(24*time.Hour)))
	}
	window := time.Duration(windowMs) * time.Millisecond
	if len(countReq.GroupBy) > logCountMaxGroupBy {
		return 0, 0, fmt.Errorf("query_request.count.group_by takes at most %d labels, got %d", logCountMaxGroupBy, len(countReq.GroupBy))
	}
	seen := map[string]bool{}
	for _, name := range countReq.GroupBy {
		if strings.TrimSpace(name) == "" {
			return 0, 0, fmt.Errorf("query_request.count.group_by has an empty label name")
		}
		if seen[name] {
			return 0, 0, fmt.Errorf("query_request.count.group_by names %q twice", name)
		}
		seen[name] = true
	}
	limit := countReq.Limit
	if limit <= 0 {
		limit = logCountDefaultLimit
	}
	limit = min(limit, logCountMaxLimit)
	interval, err := parseLogCountInterval(countReq.Interval)
	if err != nil {
		return 0, 0, err
	}
	// Buckets are aligned to whole intervals, so an interval longer than the window
	// would count far outside it (a 30d bucket around the last hour).
	if interval > window {
		return 0, 0, fmt.Errorf("interval %s is longer than the window (%s); omit interval for one count over the window",
			formatLogCountInterval(interval), window)
	}
	return limit, interval, nil
}

// resolveLogCountGroupBy maps each group_by name through the account's label mapping and
// asks the source for the field it groups on.
func resolveLogCountGroupBy(ctx *security.RequestContext, source LogCountSource, q LogCountQuery, names []string, filteringMap map[string]string) ([]string, error) {
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

// shapeLogCountGroups relabels the source's groups with the caller's names, orders them
// largest first and keeps limit of them. It returns the kept series, how many groups
// matched and how many were left out.
func shapeLogCountGroups(rows LogCountRows, names, fields []string, limit int) ([]Result, int, int) {
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

// fillLogCountBuckets gives a series one point per bucket of [startMs, endMs), in order,
// with 0 for the buckets the backend left out: backends report only buckets that counted
// something, and a reader of "errors per hour" needs the quiet hours too. A point stamped
// inside a bucket rather than at its start is counted in that bucket, never dropped.
func fillLogCountBuckets(series Result, startMs, endMs int64, interval time.Duration) Result {
	step := int64(interval / time.Second)
	if step <= 0 {
		return series
	}
	first := startMs / 1000
	counted := make(map[int64]float64, len(series.Timestamps))
	for i, ts := range series.Timestamps {
		if i < len(series.Values) && ts >= first {
			counted[first+(ts-first)/step*step] += series.Values[i]
		}
	}
	n := 0
	if endMs > startMs {
		n = int((endMs - startMs) / 1000 / step)
	}
	filled := Result{Metric: series.Metric, Timestamps: make([]int64, 0, n), Values: make([]float64, 0, n)}
	for ts := first; ts < endMs/1000; ts += step {
		filled.Timestamps = append(filled.Timestamps, ts)
		filled.Values = append(filled.Values, counted[ts])
	}
	return filled
}

// fitLogCountWindow returns the window to count and the interval to count it in. Without
// an interval the window is counted as asked. With one, the interval is widened until the
// window holds at most logCountMaxBuckets buckets, and the window is widened to whole
// buckets (aligned to the Unix epoch, so 1d buckets start at midnight UTC): a bucket cut
// by the window's edge would count a part of its interval and read low.
func fitLogCountWindow(startMs, endMs int64, interval time.Duration) (int64, int64, time.Duration, error) {
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
		if (alignedEnd-alignedStart)/step <= logCountMaxBuckets {
			return alignedStart, alignedEnd, interval, nil
		}
		wider := time.Duration(0)
		for _, s := range logCountSteps {
			if s > interval && time.Duration(endMs-startMs)*time.Millisecond <= s*logCountMaxBuckets {
				wider = s
				break
			}
		}
		if wider == 0 {
			return 0, 0, 0, fmt.Errorf("the window is too long to count in buckets: at most %d buckets of up to %s",
				logCountMaxBuckets, formatLogCountInterval(logCountMaxInterval))
		}
		interval = wider
	}
}

// parseLogCountInterval reads an interval: a Go duration (30s, 10m, 1h) or days (1d).
// Empty means no interval.
func parseLogCountInterval(s string) (time.Duration, error) {
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
		if n > float64(logCountMaxInterval/(24*time.Hour)) {
			return 0, fmt.Errorf("interval %q: at most %s", s, formatLogCountInterval(logCountMaxInterval))
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
	if d > logCountMaxInterval {
		return 0, fmt.Errorf("interval %q: at most %s", s, formatLogCountInterval(logCountMaxInterval))
	}
	return d.Truncate(time.Second), nil
}

// formatLogCountInterval writes an interval in the largest whole unit (1d, 3h, 10m, 90s).
func formatLogCountInterval(d time.Duration) string {
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
