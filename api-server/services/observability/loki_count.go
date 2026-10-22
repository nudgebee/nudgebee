package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Count mode on Loki (LogCountSource), in-cluster through the relay and hosted over HTTP.
// Every number is a sum, in Go, of count_over_time points run through query_range (the
// relay forwards nothing else).
//
// The block rule: every count_over_time range lies inside one epoch-aligned 15-minute
// block (lokiCountBlock, Loki's split_queries_by_interval). A range that crosses a block
// boundary over-counts on a Loki that splits queries. Measured on dev Loki 3.5
// (split_queries_by_interval: 15m), one pod, one hour:
//   - window ending on a 15m mark: one [3600s] range read 102,435 lines and [1200s]
//     ranges 79,326, where [1800s], [900s], [600s] and [300s] ranges all read 71,956, the
//     same as Elasticsearch;
//   - window 10 minutes past a 15m mark: 15m ranges anchored at its end (each crossing a
//     boundary) read 81,026, where 5m and 10m buckets read 79,917, the same as
//     Elasticsearch.
// So a range is either a whole block or a part of one, never a span across two.
//
// Lines whose stream lacks a group label are filtered out of the grouped query with
// `| L != ""`; Ungrouped is the total minus the sum of the groups — both exact sums of
// the same pieces over disjoint lines.

var (
	_ LogCountSource = (*LokiSource)(nil)
	_ LogCountSource = (*LokiSaasSource)(nil)
)

// lokiQueryRange runs one query_range with the given URL-encoded parameters and returns
// the Loki API body ({status, data: {resultType, result}}).
type lokiQueryRange func(params string) (map[string]any, error)

// ResolveGroupByFields implements LogCountSource.
func (s *LokiSource) ResolveGroupByFields(ctx *security.RequestContext, q LogCountQuery, names []string) ([]string, error) {
	labels, err := s.QueryLabels(ctx, FetchLogLabelRequest{AccountId: q.Request.AccountId, StartTime: q.Request.StartTime, EndTime: q.Request.EndTime})
	if err != nil {
		return nil, fmt.Errorf("listing Loki's stream labels to group by: %w", err)
	}
	return resolveLokiGroupByFields(labels, names)
}

// PlanLogCount implements LogCountSource.
func (s *LokiSource) PlanLogCount(_ *security.RequestContext, q LogCountQuery) (LogCountPlan, error) {
	return lokiPlanLogCount(q)
}

// RunLogCount implements LogCountSource.
func (s *LokiSource) RunLogCount(_ *security.RequestContext, q LogCountQuery, plan LogCountPlan) (LogCountRows, error) {
	return lokiRunLogCount(q, plan, func(params string) (map[string]any, error) {
		resp, err := relay.Execute(relay.RelayExecuteRequest{
			NoSinks: true,
			Cache:   false,
			Body: relay.ActionExecuteBody{
				AccountID:    q.Request.AccountId,
				ActionName:   "query_loki",
				ActionParams: map[string]any{"query": params},
				NoSinks:      true,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to execute loki count: %w", err)
		}
		return lokiRelayBody(resp, "loki count")
	})
}

// ResolveGroupByFields implements LogCountSource.
func (s *LokiSaasSource) ResolveGroupByFields(ctx *security.RequestContext, q LogCountQuery, names []string) ([]string, error) {
	labels, err := s.QueryLabels(ctx, FetchLogLabelRequest{AccountId: q.Request.AccountId, StartTime: q.Request.StartTime, EndTime: q.Request.EndTime})
	if err != nil {
		return nil, fmt.Errorf("listing Loki's stream labels to group by: %w", err)
	}
	return resolveLokiGroupByFields(labels, names)
}

// PlanLogCount implements LogCountSource.
func (s *LokiSaasSource) PlanLogCount(_ *security.RequestContext, q LogCountQuery) (LogCountPlan, error) {
	return lokiPlanLogCount(q)
}

// RunLogCount implements LogCountSource.
func (s *LokiSaasSource) RunLogCount(ctx *security.RequestContext, q LogCountQuery, plan LogCountPlan) (LogCountRows, error) {
	cfg, err := GetLokiConfig(ctx, q.Request.AccountId)
	if err != nil {
		return LogCountRows{}, err
	}
	return lokiRunLogCount(q, plan, func(params string) (map[string]any, error) {
		resp, err := lokiRequest(context.Background(), http.MethodGet, cfg.Url+"/loki/api/v1/query_range?"+params, "", "", cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to execute loki count: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, err := readLokiResponse(resp, "count")
		if err != nil {
			return nil, err
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("failed to decode loki count: %w", err)
		}
		return body, nil
	})
}

// resolveLokiGroupByFields accepts names that are indexed stream labels. Only those can
// be grouped on without parsing every line.
func resolveLokiGroupByFields(labels []OutputLogLabel, names []string) ([]string, error) {
	stream := make(map[string]bool, len(labels))
	available := make([]string, 0, len(labels))
	for _, l := range labels {
		if !stream[l.Label] {
			stream[l.Label] = true
			available = append(available, l.Label)
		}
	}
	sort.Strings(available)
	for _, name := range names {
		if name == "log" || name == "trace_id" {
			return nil, fmt.Errorf("cannot group by %q: it is the log line's content, not a label", name)
		}
		if !stream[name] {
			return nil, fmt.Errorf("cannot group by %q: it is not a stream label of this Loki (stream labels: %s); structured metadata and fields parsed from the line cannot be grouped on yet",
				name, strings.Join(available, ", "))
		}
	}
	return names, nil
}

// lokiCountBlock is the block every counting range stays inside (see the file comment).
// lokiMaxPoints is Loki's per-series cap on a query_range answer.
const (
	lokiCountBlock = 15 * time.Minute
	lokiMaxPoints  = 11000
)

// lokiStepChoices are the fine steps an interval count can use, largest first. Each
// divides lokiCountBlock, so a range of one step ending on a multiple of it stays inside
// one block.
var lokiStepChoices = []time.Duration{
	15 * time.Minute, 5 * time.Minute, 3 * time.Minute, time.Minute,
	30 * time.Second, 15 * time.Second, 5 * time.Second, time.Second,
}

// lokiPiece is one query_range call of a count: points at firstMs, firstMs+stepMs, …,
// endMs, each counting (t-rangeMs, t]. An instant piece has firstMs == endMs.
type lokiPiece struct {
	rangeMs, firstMs, endMs, stepMs int64
}

func (p lokiPiece) params(logql string) string {
	v := url.Values{}
	v.Set("query", logql)
	v.Set("start", strconv.FormatInt(p.firstMs*1_000_000, 10))
	v.Set("end", strconv.FormatInt(p.endMs*1_000_000, 10))
	v.Set("step", strconv.FormatFloat(float64(p.stepMs)/1000, 'f', -1, 64))
	return v.Encode()
}

func lokiInstantPiece(fromMs, toMs int64) lokiPiece {
	return lokiPiece{rangeMs: toMs - fromMs, firstMs: toMs, endMs: toMs, stepMs: toMs - fromMs}
}

// lokiFineStep is the largest step that divides both the interval and lokiCountBlock, so
// whole sub-points fold into each bucket and no range crosses a block; 0 if none does.
func lokiFineStep(interval time.Duration) time.Duration {
	for _, s := range lokiStepChoices {
		if interval%s == 0 {
			return s
		}
	}
	return 0
}

// lokiCountPieces splits q's window into pieces that keep the block rule, in run order.
//
// With an interval the window is aligned to whole intervals (fetchLogCount does it), so
// one range query at the fine step covers it: points start+S … end, each a whole step
// ending on a multiple of S. Without one the window is arbitrary: whole blocks run as
// one range query at the block step, and the parts before the first boundary and after
// the last one as instant queries over just that part; a window inside one block is a
// single instant query.
func lokiCountPieces(q LogCountQuery) ([]lokiPiece, error) {
	start, end := q.Request.StartTime, q.Request.EndTime
	if q.Interval > 0 {
		step := lokiFineStep(q.Interval)
		stepMs := step.Milliseconds()
		if step == 0 || start%stepMs != 0 || end%stepMs != 0 {
			return nil, fmt.Errorf("loki: an interval count needs a window aligned to whole seconds of the interval")
		}
		if points := (end - start) / stepMs; points > lokiMaxPoints {
			return nil, lokiTooManyPointsError(step, points)
		}
		return []lokiPiece{{rangeMs: stepMs, firstMs: start + stepMs, endMs: end, stepMs: stepMs}}, nil
	}

	blockMs := lokiCountBlock.Milliseconds()
	firstB := (start + blockMs - 1) / blockMs * blockMs
	lastB := end / blockMs * blockMs
	if firstB > lastB {
		return []lokiPiece{lokiInstantPiece(start, end)}, nil
	}
	var pieces []lokiPiece
	if start < firstB {
		pieces = append(pieces, lokiInstantPiece(start, firstB))
	}
	if lastB > firstB {
		if points := (lastB - firstB) / blockMs; points > lokiMaxPoints {
			return nil, lokiTooManyPointsError(lokiCountBlock, points)
		}
		pieces = append(pieces, lokiPiece{rangeMs: blockMs, firstMs: firstB + blockMs, endMs: lastB, stepMs: blockMs})
	}
	if end > lastB {
		pieces = append(pieces, lokiInstantPiece(lastB, end))
	}
	return pieces, nil
}

func lokiTooManyPointsError(step time.Duration, points int64) error {
	return fmt.Errorf("the window is too long to count on Loki at %s resolution: %d points per series, Loki allows %d",
		lokiRange(step.Milliseconds()), points, lokiMaxPoints)
}

// lokiCountSelector is the stream selector and line filters a count reads: q's native
// filter. Loki refuses a query without a label matcher, as the log search does.
func lokiCountSelector(q LogCountQuery) (string, error) {
	sel := strings.TrimSpace(q.Request.Query)
	if sel == "" || strings.HasPrefix(sel, "{}") {
		return "", fmt.Errorf("loki: counting needs at least one label filter")
	}
	return sel, nil
}

// lokiPlanLogCount writes the queries that count q, one per piece: the total over every
// piece and, with group_by, the count per group of the lines that have every group label
// over every piece. The order is the order lokiRunLogCount runs them in.
func lokiPlanLogCount(q LogCountQuery) (LogCountPlan, error) {
	sel, err := lokiCountSelector(q)
	if err != nil {
		return LogCountPlan{}, err
	}
	pieces, err := lokiCountPieces(q)
	if err != nil {
		return LogCountPlan{}, err
	}
	queries := lokiPieceQueries(pieces, func(rng string) string {
		return fmt.Sprintf("sum(count_over_time(%s [%s]))", sel, rng)
	})
	if len(q.GroupBy) > 0 {
		queries = append(queries, lokiPieceQueries(pieces, func(rng string) string {
			return lokiGroupedQuery(sel, q.GroupBy, rng)
		})...)
	}
	return LogCountPlan{Queries: queries}, nil
}

func lokiPieceQueries(pieces []lokiPiece, query func(rng string) string) []string {
	queries := make([]string, len(pieces))
	for i, p := range pieces {
		queries[i] = query(lokiRange(p.rangeMs))
	}
	return queries
}

func lokiGroupedQuery(sel string, groupBy []string, rng string) string {
	return fmt.Sprintf("sum by (%s) (count_over_time(%s [%s]))", strings.Join(groupBy, ", "), sel+lokiHasLabelsFilter(groupBy), rng)
}

func lokiPieceParams(pieces []lokiPiece, queries []string) []string {
	params := make([]string, len(pieces))
	for i, p := range pieces {
		params[i] = p.params(queries[i])
	}
	return params
}

// lokiRunLogCount runs a plan lokiPlanLogCount wrote for q through run, on the pieces q
// derives, and folds the points into rows. When the grouped queries trip Loki's series
// limit it falls back to lokiCountTopGroups, whose queries the plan could not know.
func lokiRunLogCount(q LogCountQuery, plan LogCountPlan, run lokiQueryRange) (LogCountRows, error) {
	sel, err := lokiCountSelector(q)
	if err != nil {
		return LogCountRows{}, err
	}
	pieces, err := lokiCountPieces(q)
	if err != nil {
		return LogCountRows{}, err
	}
	n := len(pieces)
	want := n
	if len(q.GroupBy) > 0 {
		want = 2 * n
	}
	if len(plan.Queries) != want {
		return LogCountRows{}, fmt.Errorf("loki: a count plan for this query has %d queries, got %d", want, len(plan.Queries))
	}

	params := lokiPieceParams(pieces, plan.Queries[:n])
	if len(q.GroupBy) > 0 {
		params = append(params, lokiPieceParams(pieces, plan.Queries[n:])...)
	}
	results, errs := runLokiQueries(run, params)
	for _, err := range errs[:n] {
		if err != nil {
			return LogCountRows{}, lokiCountError(err)
		}
	}
	totals := lokiParsed(results[:n])
	total := lokiPointsSum(totals)

	if len(q.GroupBy) == 0 {
		series := lokiFold(totals, nil, q)
		if len(series) == 0 {
			series = lokiFold([]Result{{Metric: map[string]string{}}}, nil, q)
		}
		return LogCountRows{Groups: series, Total: total, Complete: true}, nil
	}

	for _, err := range errs[n:] {
		if err == nil {
			continue
		}
		if lokiIsSeriesLimit(err) {
			return lokiCountTopGroups(q, run, pieces, sel, total)
		}
		return LogCountRows{}, lokiCountError(err)
	}
	groups := lokiFold(lokiParsed(results[n:]), q.GroupBy, q)
	var labelledTotal int64
	for _, g := range groups {
		labelledTotal += int64(lokiSeriesSum(g))
	}
	return LogCountRows{
		Groups:      groups,
		Total:       total,
		Ungrouped:   nonNegative(total - labelledTotal),
		GroupsTotal: len(groups),
		Complete:    true,
	}, nil
}

// lokiParsed reads and concatenates the series of several query_range answers.
func lokiParsed(bodies []map[string]any) []Result {
	var all []Result
	for _, b := range bodies {
		all = append(all, parsePrometheusMatrixOrVector(b)...)
	}
	return all
}

// lokiCountTopGroups counts the groups when there are too many for one query
// (max_query_series). A whole-window topk chooses the groups and count() says how many
// there are; the chosen groups are then counted exactly, piece by piece under the block
// rule. The choice comes from a wide range, which over-counts on Loki with query
// splitting, so the ranking can be off at the margin: the answer is marked incomplete.
// These queries are not in the plan: only the grouped queries' failure shows they are
// needed.
func lokiCountTopGroups(q LogCountQuery, run lokiQueryRange, pieces []lokiPiece, sel string, total int64) (LogCountRows, error) {
	by := strings.Join(q.GroupBy, ", ")
	labelled := sel + lokiHasLabelsFilter(q.GroupBy)
	whole := lokiInstantPiece(q.Request.StartTime, q.Request.EndTime)
	w := lokiRange(whole.rangeMs)
	topQ := fmt.Sprintf("topk(%d, sum by (%s) (count_over_time(%s [%s])))", q.Limit, by, labelled, w)
	countQ := fmt.Sprintf("count(sum by (%s) (count_over_time(%s [%s])))", by, labelled, w)
	labelledQs := lokiPieceQueries(pieces, func(rng string) string {
		return fmt.Sprintf("sum(count_over_time(%s [%s]))", labelled, rng)
	})
	params := append([]string{whole.params(topQ), whole.params(countQ)}, lokiPieceParams(pieces, labelledQs)...)
	results, errs := runLokiQueries(run, params)
	for _, err := range errs {
		if err != nil {
			return LogCountRows{}, lokiCountError(err)
		}
	}
	rows := LogCountRows{
		Total:       total,
		Ungrouped:   nonNegative(total - lokiPointsSum(lokiParsed(results[2:]))),
		GroupsTotal: int(lokiPointsSum(parsePrometheusMatrixOrVector(results[1]))),
		Complete:    false,
	}
	top := lokiGroupsOf(parsePrometheusMatrixOrVector(results[0]), q.GroupBy)
	if len(top) > 0 {
		narrowed := lokiNarrowSelector(sel, q.GroupBy, top)
		narrowedQs := lokiPieceQueries(pieces, func(rng string) string {
			return lokiGroupedQuery(narrowed, q.GroupBy, rng)
		})
		answers, errs := runLokiQueries(run, lokiPieceParams(pieces, narrowedQs))
		for _, err := range errs {
			if err != nil {
				return LogCountRows{}, lokiCountError(err)
			}
		}
		keep := make(map[string]bool, len(top))
		for _, g := range top {
			keep[lokiGroupKey(g.Metric, q.GroupBy)] = true
		}
		// A regex per label can match combinations outside the chosen set.
		for _, g := range lokiFold(lokiParsed(answers), q.GroupBy, q) {
			if keep[lokiGroupKey(g.Metric, q.GroupBy)] {
				rows.Groups = append(rows.Groups, g)
			}
		}
	}
	return rows, nil
}

// lokiFold turns the points of every piece into the series LogCountRows wants, one per
// group (the same group from different pieces merged): with an interval, each point —
// which counts the step ending at its timestamp — added to the bucket that step starts
// in, stamped at the bucket's start; without one, every point of the group summed into
// one point at the window's end.
func lokiFold(results []Result, labels []string, q LogCountQuery) []Result {
	type group struct {
		metric  map[string]string
		buckets map[int64]float64
	}
	var order []string
	groups := map[string]*group{}
	stepMs := lokiFineStep(q.Interval).Milliseconds()
	intervalMs := q.Interval.Milliseconds()
	for _, r := range lokiGroupsOf(results, labels) {
		key := lokiGroupKey(r.Metric, labels)
		g, ok := groups[key]
		if !ok {
			g = &group{metric: r.Metric, buckets: map[int64]float64{}}
			groups[key] = g
			order = append(order, key)
		}
		if q.Interval == 0 {
			g.buckets[q.Request.EndTime/1000] += lokiSeriesSum(r)
			continue
		}
		for i, ts := range r.Timestamps {
			from := ts*1000 - stepMs
			bucket := (q.Request.StartTime + (from-q.Request.StartTime)/intervalMs*intervalMs) / 1000
			g.buckets[bucket] += r.Values[i]
		}
	}
	out := make([]Result, 0, len(order))
	for _, key := range order {
		g := groups[key]
		stamps := make([]int64, 0, len(g.buckets))
		for ts := range g.buckets {
			stamps = append(stamps, ts)
		}
		sort.Slice(stamps, func(a, b int) bool { return stamps[a] < stamps[b] })
		folded := Result{Metric: g.metric, Timestamps: stamps, Values: make([]float64, len(stamps))}
		for i, ts := range stamps {
			folded.Values[i] = g.buckets[ts]
		}
		out = append(out, folded)
	}
	return out
}

// lokiGroupsOf keeps only the group labels of each series; nil labels keep none.
func lokiGroupsOf(results []Result, labels []string) []Result {
	groups := make([]Result, 0, len(results))
	for _, r := range results {
		metric := make(map[string]string, len(labels))
		for _, l := range labels {
			metric[l] = r.Metric[l]
		}
		groups = append(groups, Result{Metric: metric, Timestamps: r.Timestamps, Values: r.Values})
	}
	return groups
}

func lokiSeriesSum(r Result) float64 {
	sum := 0.0
	for _, v := range r.Values {
		sum += v
	}
	return sum
}

// lokiPointsSum sums every point of every series: the count a single-series sum() or
// count() query answers over the grid.
func lokiPointsSum(results []Result) int64 {
	var sum float64
	for _, r := range results {
		sum += lokiSeriesSum(r)
	}
	return int64(sum)
}

func nonNegative(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// runLokiQueries runs independent query_range calls concurrently, in order of params,
// returning each one's answer or error.
func runLokiQueries(run lokiQueryRange, params []string) ([]map[string]any, []error) {
	results := make([]map[string]any, len(params))
	errs := make([]error, len(params))
	var wg sync.WaitGroup
	for i, p := range params {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = run(p)
		}()
	}
	wg.Wait()
	return results, errs
}

// lokiRange renders a LogQL range in milliseconds.
func lokiRange(ms int64) string {
	return strconv.FormatInt(ms, 10) + "ms"
}

func lokiIsSeriesLimit(err error) bool {
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "maximum of series") || strings.Contains(lower, "max_query_series")
}

func lokiHasLabelsFilter(labels []string) string {
	var b strings.Builder
	for _, l := range labels {
		b.WriteString(" | " + l + `!=""`)
	}
	return b.String()
}

// lokiNarrowSelector adds, inside the stream selector, a regex matcher per label listing
// that label's values among the top groups, so the bucketed query reads only their
// streams. The selector's closing brace is found outside quoted values.
func lokiNarrowSelector(sel string, labels []string, groups []Result) string {
	closing := lokiSelectorEnd(sel)
	if closing < 0 {
		return sel
	}
	matchers := make([]string, 0, len(labels))
	for _, l := range labels {
		seen := map[string]bool{}
		var values []string
		for _, g := range groups {
			v := g.Metric[l]
			if !seen[v] {
				seen[v] = true
				values = append(values, escapeLokiLabelValue(escapeRegexValue(v)))
			}
		}
		sort.Strings(values)
		matchers = append(matchers, fmt.Sprintf(`%s=~"%s"`, l, strings.Join(values, "|")))
	}
	inner := strings.TrimSpace(sel[1:closing])
	if inner != "" {
		inner += ", "
	}
	return "{" + inner + strings.Join(matchers, ", ") + "}" + sel[closing+1:]
}

// lokiSelectorEnd returns the index of the brace closing a LogQL stream selector that
// starts at sel[0], skipping braces inside "…" (with escapes) and `…` values; -1 if none.
func lokiSelectorEnd(sel string) int {
	if !strings.HasPrefix(sel, "{") {
		return -1
	}
	var quote byte
	for i := 1; i < len(sel); i++ {
		c := sel[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '`':
			quote = c
		case c == '}':
			return i
		}
	}
	return -1
}

// lokiGroupKey identifies a group by its label values, in labels' order.
func lokiGroupKey(metric map[string]string, labels []string) string {
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = metric[l]
	}
	return strings.Join(parts, "\x00")
}

// lokiCountError turns Loki's limit refusals into something a caller can act on, keeping
// Loki's own message.
func lokiCountError(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "maximum of series") || strings.Contains(lower, "max_query_series"):
		return fmt.Errorf("too many streams for Loki's series limit (max_query_series): narrow the filter or group by fewer labels: %w", err)
	case strings.Contains(lower, "query time range exceeds") || strings.Contains(lower, "max_query_length") || strings.Contains(lower, "query too long"):
		return fmt.Errorf("the window is longer than this Loki allows (max_query_length): shorten it: %w", err)
	case strings.Contains(lower, "maximum resolution") || strings.Contains(lower, "too many points") || strings.Contains(lower, "maximum number of points"):
		return fmt.Errorf("too many buckets for Loki: use a wider interval: %w", err)
	default:
		return err
	}
}
