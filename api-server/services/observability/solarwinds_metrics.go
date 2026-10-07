package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/security"
)

// SolarWindsMetricSource implements MetricSource for SolarWinds Observability.
//
// Time-series data is fetched from the SolarWinds REST API:
//
//	GET /v1/metrics/{metricName}/measurements
//
// All shared structs (swMeasurementsResponse, swPageInfo, swMeasurementGrouping, etc.)
// and the DoSolarWindsGET transport are defined in solarwinds_traces.go and
// solarwinds_logs.go — they are reused here without duplication.
//
// Query format:
//   - req.Queries is map[queryKey → SWO metric name], or the text GetQuery renders
//     ("<metric> | filter: … | groupBy: … | aggregateBy: …"), so a query shown in the
//     Code tab runs as written
//   - aggregateBy / groupBy / filter are read from req.Request (optional)
//   - req.LabelMatchers and req.Labels are translated into filter terms; a matcher
//     SWO cannot express fails the request instead of being dropped
//
// FetchMetricLabelValues requires "metric" in req.Request.
type SolarWindsMetricSource struct{}

// GetSupportedOperators returns an empty slice. FetchMetricsQuery and GetQuery now
// translate _eq, _neq, _in and _not_in label matchers (swMetricMatchersFilter), but the
// UI builder still renders SolarWinds metric chips into a filter string itself and only
// for `=`, so advertising more operators would let the builder offer chips it drops.
// The UI falls back to its minimal built-in descriptor set when this list is empty.
// See issue #29227 and the Gemini review pointer on #29233.
func (s *SolarWindsMetricSource) GetSupportedOperators() []string {
	return []string{}
}

// GetQuery renders the request SWO would be sent, since SWO has no query language to
// show: the metric name, then the filter, groupBy and aggregateBy parameters. The text
// round-trips — FetchMetricsQuery accepts it in place of a bare metric name — so a
// query copied from the Code tab runs exactly as displayed.
func (s *SolarWindsMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	keys := make([]string, 0, len(req.Queries))
	for k := range req.Queries {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return "", nil
	}
	// The lowest-sorted key is the representative one when a request carries several.
	sort.Strings(keys)

	matcherFilter, err := swMetricMatchersFilter(req.LabelMatchers, req.Labels)
	if err != nil {
		return "", err
	}
	name, q, err := swApplyQueryText(req.Queries[keys[0]], swMeasurementQueryFromRequest(req.Request, "", ""))
	if err != nil {
		return "", err
	}
	q.filter = swJoinFilters(q.filter, matcherFilter)
	return swRenderQueryText(name, swInferEntity(name, q)), nil
}

// WrapAggregate implements MetricAggregateWrapper. The default PromQL wrap turned the
// rendered text into `sum(<metric> | …)`, which nothing can run; the aggregate is SWO's
// aggregateBy parameter instead.
func (s *SolarWindsMetricSource) WrapAggregate(expr string, aggregateOperator string) (string, error) {
	if aggregateOperator == "" {
		return expr, nil
	}
	agg := strings.ToUpper(aggregateOperator)
	switch agg {
	case "SUM", "AVG", "MIN", "MAX", "COUNT":
	default:
		return "", fmt.Errorf("aggregate_operator %q is not supported for SolarWinds metrics; use sum, avg, min, max or count", aggregateOperator)
	}
	name, q, err := swApplyQueryText(expr, swMeasurementQuery{aggregateBy: "AVG"})
	if err != nil {
		return "", err
	}
	q.aggregateBy = agg
	return swRenderQueryText(name, swInferEntity(name, q)), nil
}

// swFamilyEntities maps a SWO Kubernetes metric family to the entity it is reported
// per -- the same mapping nubi's SolarWinds metrics tool uses. Longest prefix first.
var swFamilyEntities = []struct{ prefix, entity string }{
	{"k8s.container.", "k8s.pod.name,k8s.container.name"},
	{"k8s.pod.", "k8s.pod.name"},
	{"k8s.node.", "k8s.node.name"},
}

// swInferEntity names the entity for a SUM or COUNT that did not, when the metric's
// family says what it is reported per. Without one SWO adds every raw sample in a
// bucket, so a SUM scales with the bucket width: the UI builder's "sum" read a
// namespace's pod CPU 5x high at 6h and 25x at 24h. This is the metric's own shape,
// not a guess about the caller. Any other family is left as sent.
func swInferEntity(metric string, q swMeasurementQuery) swMeasurementQuery {
	if q.preGroupBy != "" || (q.aggregateBy != "SUM" && q.aggregateBy != "COUNT") {
		return q
	}
	for _, f := range swFamilyEntities {
		if strings.HasPrefix(metric, f.prefix) {
			q.preGroupBy, q.preGroupByMethod = f.entity, "AVG"
			return q
		}
	}
	return q
}

// swSumInflationNote is the caveat for a SUM or COUNT across entities that names none
// and whose family gives none, over a window long enough for buckets to hold several
// samples.
func swSumInflationNote(q swMeasurementQuery) string {
	if q.preGroupBy != "" || (q.aggregateBy != "SUM" && q.aggregateBy != "COUNT") {
		return ""
	}
	from, ferr := time.Parse(time.RFC3339, q.from)
	to, terr := time.Parse(time.RFC3339, q.to)
	if ferr != nil || terr != nil || to.Sub(from) <= time.Hour {
		return ""
	}
	return "SolarWinds " + q.aggregateBy + " adds every sample in a bucket, so totals over windows longer than an hour read high; " +
		"name the entity being added up in preGroupBy for a true total."
}

const swMetricConfigsErrFmt = "failed to get SolarWinds configs: %w"

// swMetricsListResponse is the response shape for GET /v1/metrics.
type swMetricsListResponse struct {
	MetricsInfo []swMetricInfo `json:"metricsInfo"`
	PageInfo    *swPageInfo    `json:"pageInfo"`
}

type swMetricInfo struct {
	Name string `json:"name"`
}

// swMeasurementQuery groups the optional query parameters for the measurements API.
// Separating these from the transport-level params (token, baseURL, path) keeps
// fetchAllMeasurements within the allowed parameter count.
type swMeasurementQuery struct {
	// aggregateBy must be uppercase: AVG, MAX, MIN, SUM, COUNT (SWO is case-sensitive).
	aggregateBy string
	// groupBy is a comma-separated list of SWO attribute keys (e.g. "k8s.namespace.name").
	// When empty, SWO returns a single ungrouped series with no attributes.
	groupBy string
	// filter is a SWO filter expression (e.g. "k8s.namespace.name: [nudgebee]").
	filter string
	// preGroupBy / preGroupByMethod reduce the samples of each named entity inside a
	// bucket BEFORE aggregateBy combines entities. Without them, SUM adds up every
	// raw sample in the bucket, so the total grows with the bucket width (measured:
	// 5x at 6h, 28x at 24h). SWO requires both or neither.
	preGroupBy       string
	preGroupByMethod string
	// from / to are ISO-8601 timestamps. When empty the API uses its own defaults.
	from string
	to   string
}

// FetchMetricsQuery fetches time-series data for each query in req.Queries.
//
// req.Request optional keys:
//
//	"aggregateBy"      – "AVG" (default), "MAX", "MIN", "SUM", "COUNT" (case-insensitive)
//	"groupBy"          – comma-separated SWO attribute keys, e.g. "k8s.namespace.name"
//	"filter"           – SWO filter expression, e.g. "k8s.namespace.name: [nudgebee]"
//	"preGroupBy"       – the entity whose samples are reduced first, e.g. "k8s.pod.name"
//	"preGroupByMethod" – how to reduce them; defaults to "AVG" when preGroupBy is set
//
// A SUM across entities is only a true total when the caller names the entity in
// preGroupBy. Without it SWO adds every raw sample in a bucket, so the answer scales
// with the bucket width. When the caller names none, the entity comes from the metric
// family (swInferEntity: k8s.pod.*, k8s.container.*, k8s.node.*); any other family is
// sent as asked, with a Note on windows over an hour.
//
// SUM with preGroupBy and preGroupByMethod AVG is answered as a time-weighted total
// (swTimeWeightedSum): SWO's own pre-grouped average counts an entity that lived
// through part of a bucket at its full value, which read a one-replica deployment's
// 0.2-core request as 0.39 and 0.78 in rollout buckets.
//
// Step interval (req.StepInterval) is not used: SWO picks the bucket width from the
// window and the API does not expose a resolution parameter.
//
// Buckets that end within one bucket width of the window's end are dropped: SWO
// returns the newest bucket while it is still filling (see swDropPartialBuckets).
func (s *SolarWindsMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricsQuery: failed to get configs", "error", err)
		return OutputMetricQuery{}, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)
	from, to := swTraceTimeRange(req.StartTime, req.EndTime)

	// Matchers apply to every query. Checked before anything is fetched, so a matcher SWO
	// cannot express fails the request rather than coming back as an unfiltered answer.
	matcherFilter, err := swMetricMatchersFilter(req.LabelMatchers, req.Labels)
	if err != nil {
		return OutputMetricQuery{}, err
	}
	base := swMeasurementQueryFromRequest(req.Request, from, to)

	type swPlannedQuery struct {
		key, metric string
		q           swMeasurementQuery
	}
	planned := make([]swPlannedQuery, 0, len(req.Queries))
	for queryKey, text := range req.Queries {
		metricName, q, err := swApplyQueryText(text, base)
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("query %q: %w", queryKey, err)
		}
		q.filter = swJoinFilters(q.filter, matcherFilter)
		planned = append(planned, swPlannedQuery{key: queryKey, metric: metricName, q: swInferEntity(metricName, q)})
	}

	results := OutputMetricQuery{Results: []QueryResult{}}
	for _, p := range planned {
		qr := s.fetchOneMetricQuery(ctx, apiToken, baseURL, p.key, p.metric, p.q, req.Instant)
		if note := swSumInflationNote(p.q); note != "" && qr.Note == "" {
			qr.Note = note
		}
		results.Results = append(results.Results, qr)
	}
	return results, nil
}

// swMeasurementQueryFromRequest reads the measurement parameters from the free-form
// request map.
func swMeasurementQueryFromRequest(r map[string]any, from, to string) swMeasurementQuery {
	q := swMeasurementQuery{
		// SWO rejects lowercase aggregateBy with HTTP 400.
		aggregateBy: strings.ToUpper(swStringFromRequest(r, "aggregateBy", "AVG")),
		groupBy:     swStringFromRequest(r, "groupBy", ""),
		filter:      swStringFromRequest(r, "filter", ""),
		preGroupBy:  swStringFromRequest(r, "preGroupBy", ""),
		from:        from,
		to:          to,
	}
	if q.preGroupBy != "" {
		q.preGroupByMethod = strings.ToUpper(swStringFromRequest(r, "preGroupByMethod", "AVG"))
	}
	return q
}

// swQueryTextSeparator splits the parameters in the text GetQuery renders. A "|"
// inside a value is written as "\|" (swEscapeQueryTextValue), so the separator is
// never ambiguous and any value round-trips.
const swQueryTextSeparator = " | "

// swRenderQueryText is the text GetQuery shows: the metric, then each parameter set.
func swRenderQueryText(metric string, q swMeasurementQuery) string {
	parts := []string{swEscapeQueryTextValue(metric)}
	add := func(name, value string) {
		if value != "" {
			parts = append(parts, name+": "+swEscapeQueryTextValue(value))
		}
	}
	add("filter", q.filter)
	add("groupBy", q.groupBy)
	add("preGroupBy", q.preGroupBy)
	if q.preGroupBy != "" {
		add("preGroupByMethod", q.preGroupByMethod)
	}
	add("aggregateBy", q.aggregateBy)
	return strings.Join(parts, swQueryTextSeparator)
}

// swEscapeQueryTextValue writes each "|" as "\|". Nothing else is escaped, so a
// backslash in a filter reads as typed.
func swEscapeQueryTextValue(v string) string {
	return strings.ReplaceAll(v, "|", `\|`)
}

// swSplitQueryText splits on every "|" not written as "\|", unescaping as it goes.
func swSplitQueryText(text string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\' && i+1 < len(text) && text[i+1] == '|':
			cur.WriteByte('|')
			i++
		case text[i] == '|':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(text[i])
		}
	}
	return append(parts, cur.String())
}

// swApplyQueryText reads a query as rendered by swRenderQueryText. A bare metric name
// keeps base unchanged; each parameter the text names replaces base's. An unknown
// parameter is an error -- ignoring it would run a different query than the one shown.
func swApplyQueryText(text string, base swMeasurementQuery) (string, swMeasurementQuery, error) {
	parts := swSplitQueryText(text)
	metric := strings.TrimSpace(parts[0])
	q := base
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, ":")
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return "", base, fmt.Errorf("SolarWinds query parameter %q must read \"name: value\"", strings.TrimSpace(part))
		}
		switch strings.TrimSpace(key) {
		case "filter":
			q.filter = value
		case "groupBy":
			q.groupBy = value
		case "preGroupBy":
			q.preGroupBy = value
		case "preGroupByMethod":
			q.preGroupByMethod = strings.ToUpper(value)
		case "aggregateBy":
			q.aggregateBy = strings.ToUpper(value)
		default:
			return "", base, fmt.Errorf("SolarWinds query parameter %q is not supported; use filter, groupBy, preGroupBy, preGroupByMethod or aggregateBy", strings.TrimSpace(key))
		}
	}
	if q.preGroupBy != "" && q.preGroupByMethod == "" {
		q.preGroupByMethod = "AVG"
	}
	if q.preGroupBy == "" {
		q.preGroupByMethod = ""
	}
	return metric, q, nil
}

// swJoinFilters ANDs filter expressions. SWO reads a space between terms as AND.
func swJoinFilters(filters ...string) string {
	parts := make([]string, 0, len(filters))
	for _, f := range filters {
		if f = strings.TrimSpace(f); f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, " ")
}

// swMetricMatchersFilter renders label matchers, and the legacy eq-only Labels map, as
// SWO metric filter terms ANDed together. Labels are used as given: SWO keys
// (k8s.namespace.name) and its plain cAdvisor-style keys (namespace) both exist, so
// mapping one onto the other would change which series match.
//
// What SWO's metric filter accepts, measured against the live API:
//   - attr:v is EXACT and attr:[a,b] is an OR of exact values;
//   - a trailing or interior * is a wildcard, but not inside a bracket list (0 rows),
//     and a leading * is refused (HTTP 400) -- several wildcards need (a OR b);
//   - NOT attr:v negates one term; NOT attr:[a,b], NOT (…) and -attr:v are all refused,
//     and NOT attr:v* is accepted but matches everything, so it is refused here.
//
// Anything else is an error naming the operator, never a dropped term.
func swMetricMatchersFilter(matchers []LabelMatcher, labels map[string]string) (string, error) {
	terms := make([]string, 0, len(matchers)+len(labels))
	for _, label := range sortedKeys(labels) {
		term, err := swMetricMatcherTerm(LabelMatcher{Label: label, Operator: "_eq", Value: labels[label]})
		if err != nil {
			return "", err
		}
		terms = append(terms, term)
	}
	for _, m := range matchers {
		term, err := swMetricMatcherTerm(m)
		if err != nil {
			return "", err
		}
		terms = append(terms, term)
	}
	return strings.Join(terms, " "), nil
}

func swMetricMatcherTerm(m LabelMatcher) (string, error) {
	label := strings.TrimSpace(m.Label)
	if label == "" || strings.ContainsAny(label, " \t:\"()[]") {
		return "", fmt.Errorf("SolarWinds metric filter: label %q is not a valid attribute name", m.Label)
	}
	values := []string{strings.TrimSpace(m.Value)}
	if m.Operator == "_in" || m.Operator == "_not_in" {
		values = splitMatcherList(m.Value)
	}
	if len(values) == 0 || values[0] == "" {
		return "", fmt.Errorf("SolarWinds metric filter: %s %s needs a value", label, m.Operator)
	}
	wildcard := false
	for _, v := range values {
		if strings.HasPrefix(v, "*") {
			return "", fmt.Errorf("SolarWinds metric filter: %s:%s starts with a wildcard, which SolarWinds refuses; anchor the start of the value", label, v)
		}
		if strings.Contains(v, "*") {
			wildcard = true
		}
	}

	switch m.Operator {
	case "_eq", "_in":
		return swOrTerms(label, values, wildcard), nil
	case "_neq", "_not_in":
		if wildcard {
			return "", fmt.Errorf("SolarWinds metric filter: %s %s with a wildcard is not supported; SolarWinds ignores a negated wildcard and would return every series", label, m.Operator)
		}
		terms := make([]string, len(values))
		for i, v := range values {
			terms[i] = "NOT " + label + ":" + swEscapeValue(v)
		}
		return strings.Join(terms, " "), nil
	default:
		return "", fmt.Errorf("SolarWinds metric filter: operator %q is not supported; use _eq, _neq, _in or _not_in", m.Operator)
	}
}

// swOrTerms matches any of values: a bracket list when every value is a plain exact
// one, otherwise an OR group (wildcards and quoted values do not work inside brackets).
func swOrTerms(label string, values []string, wildcard bool) string {
	plain := !wildcard
	for _, v := range values {
		if swEscapeValue(v) != v || strings.Contains(v, ",") {
			plain = false
		}
	}
	if len(values) == 1 {
		return label + ":" + swEscapeValue(values[0])
	}
	if plain {
		return label + ":[" + strings.Join(values, ",") + "]"
	}
	terms := make([]string, len(values))
	for i, v := range values {
		terms[i] = label + ":" + swEscapeValue(v)
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

// splitMatcherList splits an _in / _not_in value on commas, trimming and dropping
// blanks -- the shape the UI's set-membership chips send.
func splitMatcherList(raw string) []string {
	out := []string{}
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// fetchOneMetricQuery fetches measurements for a single metric name and wraps
// any error into the QueryResult rather than aborting the whole batch.
func (s *SolarWindsMetricSource) fetchOneMetricQuery(ctx *security.RequestContext, apiToken, baseURL, queryKey, metricName string, q swMeasurementQuery, instant bool) QueryResult {
	if strings.TrimSpace(metricName) == "" {
		errMsg := "metric name must not be empty"
		return QueryResult{QueryKey: queryKey, Error: &errMsg}
	}

	path := fmt.Sprintf("/v1/metrics/%s/measurements", url.PathEscape(metricName))
	ctx.GetLogger().Info("SolarWindsMetricSource.FetchMetricsQuery",
		"key", queryKey, "path", path, "aggregateBy", q.aggregateBy, "instant", instant)

	if swIsTimeWeightedSum(q) {
		qr, err := swTimeWeightedSum(apiToken, baseURL, path, queryKey, metricName, q, instant)
		if err != nil {
			ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricsQuery: fetch failed",
				"key", queryKey, "error", err)
			errMsg := err.Error()
			return QueryResult{QueryKey: queryKey, Error: &errMsg}
		}
		return qr
	}

	qr, err := swFetchQueryResult(apiToken, baseURL, path, queryKey, metricName, q, instant)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricsQuery: fetch failed",
			"key", queryKey, "error", err)
		errMsg := err.Error()
		return QueryResult{QueryKey: queryKey, Error: &errMsg}
	}
	return qr
}

// swFetchQueryResult fetches one measurements query, drops the still-filling buckets
// and converts the groupings into series. The result carries the bucket width -- the
// one SWO reported, else the closest pair of timestamps -- for the time-weighted
// division and the `count` conversion.
func swFetchQueryResult(apiToken, baseURL, path, queryKey, metricName string, q swMeasurementQuery, instant bool) (QueryResult, error) {
	groupings, bucketSeconds, err := fetchAllMeasurements(apiToken, baseURL, path, q)
	if err != nil {
		return QueryResult{}, err
	}
	if bucketSeconds <= 0 {
		bucketSeconds = swInferBucketSeconds(groupings)
	}
	if end, perr := time.Parse(time.RFC3339, q.to); perr == nil {
		swDropPartialBuckets(groupings, bucketSeconds, end)
	}
	qr := swGroupingsToQueryResult(groupings, queryKey, metricName, instant)
	qr.bucketSeconds = bucketSeconds
	return qr, nil
}

// swRawSampleWindow is the longest window SWO answers in 1-second buckets: measured,
// 900s came back in 1s buckets and 901s in 60s ones. A 1s bucket holds only the
// entities scraped in that second, so a SUM across entities read 1.2 cores for a
// cluster using 37.7.
const swRawSampleWindow = 15 * time.Minute

// swWidenRawWindow moves the start of a window SWO would answer in 1-second buckets back
// to just past swRawSampleWindow, so it is answered in whole buckets instead. It returns
// the original start, whose earlier buckets the caller trims, or zero when q is unchanged.
func swWidenRawWindow(q swMeasurementQuery) (swMeasurementQuery, time.Time) {
	from, ferr := time.Parse(time.RFC3339, q.from)
	to, terr := time.Parse(time.RFC3339, q.to)
	if ferr != nil || terr != nil || to.Sub(from) > swRawSampleWindow {
		return q, time.Time{}
	}
	q.from = to.Add(-swRawSampleWindow - time.Minute).Format(time.RFC3339)
	return q, from
}

// swTrimBefore drops the points of every series that start before start, and the
// series left with no points, so no data in the window is zero series.
func swTrimBefore(qr *QueryResult, start time.Time) {
	kept := qr.Payload[:0]
	for _, series := range qr.Payload {
		ts, vals := series.Timestamps[:0], series.Values[:0]
		for k, t := range series.Timestamps {
			if t >= start.Unix() {
				ts, vals = append(ts, t), append(vals, series.Values[k])
			}
		}
		if len(ts) > 0 {
			series.Timestamps, series.Values = ts, vals
			kept = append(kept, series)
		}
	}
	qr.Payload = kept
}

// swIsTimeWeightedSum reports whether q asks for a total across the entities it names.
func swIsTimeWeightedSum(q swMeasurementQuery) bool {
	return q.aggregateBy == "SUM" && q.preGroupBy != "" && q.preGroupByMethod == "AVG"
}

// swTimeWeightedSum answers SUM + preGroupBy=<entity> as a time-weighted total: the
// plain SUM of every sample in a bucket, divided by the samples one entity contributes
// to a full bucket. Each entity then weighs by the time it was actually present.
//
// Samples-per-bucket is MEASURED, never assumed from a scrape interval, since
// collectors are configured per customer: a second request COUNTs each entity's
// samples per bucket and keeps the MAX over entities (swSampleCountQuery). It always
// fetches the range, because the full-bucket count is read from the whole window.
// There is no batch transport for SWO here, so this is one extra request per key.
//
// A window SWO would answer in 1-second buckets is widened first (swWidenRawWindow) and
// trimmed back afterwards. A total is never divided across two bucket widths: the value
// and count responses must report the same one.
func swTimeWeightedSum(apiToken, baseURL, path, queryKey, metricName string, q swMeasurementQuery, instant bool) (QueryResult, error) {
	q, start := swWidenRawWindow(q)
	plain := q
	plain.preGroupBy, plain.preGroupByMethod = "", ""
	values, err := swFetchQueryResult(apiToken, baseURL, path, queryKey, metricName, plain, instant)
	if err != nil {
		return QueryResult{}, err
	}
	// An instant read is not trimmed: the newest complete bucket is the answer even when
	// it began before a window shorter than one bucket.
	if !start.IsZero() && !instant {
		swTrimBefore(&values, start)
	}
	if !swHasPoints(values) {
		// Nothing matched: zero series is the answer, and there is nothing to count.
		return values, nil
	}
	counts, err := swFetchQueryResult(apiToken, baseURL, path, queryKey, metricName, swSampleCountQuery(q), false)
	if err != nil {
		return QueryResult{}, fmt.Errorf("counting samples per bucket: %w", err)
	}
	if values.bucketSeconds > 0 && counts.bucketSeconds > 0 && values.bucketSeconds != counts.bucketSeconds {
		return QueryResult{}, fmt.Errorf("SolarWinds answered the values in %ds buckets and the sample counts in %ds buckets, so no time-weighted total can be given",
			values.bucketSeconds, counts.bucketSeconds)
	}
	results := []QueryResult{values}
	swDivideBySamplesPerBucket(results, []QueryResult{counts})
	return results[0], nil
}

// swHasPoints reports whether any series of qr holds a point.
func swHasPoints(qr QueryResult) bool {
	for _, s := range qr.Payload {
		if len(s.Timestamps) > 0 {
			return true
		}
	}
	return false
}

// swSampleCountQuery asks, per bucket, how many samples the best-covered entity has:
// each entity's samples are COUNTed, and the MAX over entities is kept. Same filter and
// groupBy as the value query, so its series line up with the values' series.
func swSampleCountQuery(q swMeasurementQuery) swMeasurementQuery {
	c := q
	c.aggregateBy, c.preGroupByMethod = "MAX", "COUNT"
	return c
}

// swDivideBySamplesPerBucket turns plain-SUM series into time-weighted totals, in place.
//
// A full bucket's sample count is the most common per-bucket count in the window (ties
// to the larger): measured, it is a clean 1 at 1h, 5 at 6h and 30 at 24h on a 60s
// collector, with lower counts only in buckets no entity lived through -- a rollout, a
// node replacement, the edge of the window. Taking each bucket's own count there would
// read a half-covered bucket as a full one (0.42 instead of 0.21 cores, measured). A
// bucket whose own count is higher than usual (a scrape landing twice) is divided by
// its own count, so it is not read double.
//
// Known limit: a scrape interval that changes mid-window under-reads the buckets on the
// less common side of the change -- they hold fewer samples than the modal count they
// are divided by. When the modal count covers less than half the buckets the result
// carries a Note saying so.
//
// A bucket with no count, or a count of zero, yields no point. A key whose count could
// not be read keeps no points and says why, rather than reporting the raw sum; a key
// with no points at all is left as it is.
func swDivideBySamplesPerBucket(values, counts []QueryResult) {
	countsByKey := make(map[string]QueryResult, len(counts))
	for _, c := range counts {
		countsByKey[c.QueryKey] = c
	}
	for i := range values {
		v := &values[i]
		if (v.Error != nil && *v.Error != "") || !swHasPoints(*v) {
			continue
		}
		c, ok := countsByKey[v.QueryKey]
		full, share := swFullBucketCount(c)
		if !ok || (c.Error != nil && *c.Error != "") || full <= 0 {
			msg := "SolarWinds: the samples per bucket could not be measured, so no time-weighted total can be given"
			if ok && c.Error != nil {
				msg += ": " + *c.Error
			}
			v.Payload, v.Error, v.Note = nil, &msg, msg
			continue
		}
		perSeries := make(map[string]map[int64]float64, len(c.Payload))
		for _, s := range c.Payload {
			byTs := make(map[int64]float64, len(s.Timestamps))
			for k, ts := range s.Timestamps {
				byTs[ts] = s.Values[k]
			}
			perSeries[swSeriesKey(s.Metric)] = byTs
		}
		kept := v.Payload[:0]
		for _, series := range v.Payload {
			bucketCounts := perSeries[swSeriesKey(series.Metric)]
			if bucketCounts == nil {
				continue
			}
			ts, vals := series.Timestamps[:0], series.Values[:0]
			for k, t := range series.Timestamps {
				n, ok := bucketCounts[t]
				if !ok || n <= 0 || math.IsNaN(n) {
					continue
				}
				ts, vals = append(ts, t), append(vals, series.Values[k]/math.Max(full, n))
			}
			// A series with no countable bucket left is dropped, so "no data" is zero series.
			if len(ts) == 0 {
				continue
			}
			series.Timestamps, series.Values = ts, vals
			kept = append(kept, series)
		}
		v.Payload = kept
		if share < 0.5 {
			v.Note = fmt.Sprintf("SolarWinds: samples per bucket varied across the window (the usual %g held in %.0f%% of buckets); "+
				"a scrape interval change would make the buckets with fewer samples read low", full, share*100)
		}
	}
}

// swFullBucketCount is the most common per-bucket sample count in a count result, ties
// going to the larger, and the share of buckets that hold it; 0 when it holds none.
func swFullBucketCount(c QueryResult) (float64, float64) {
	// Counts are whole numbers; key on the rounded integer, not the float.
	freq := map[int64]int{}
	total := 0
	for _, s := range c.Payload {
		for _, n := range s.Values {
			if n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
				freq[int64(math.Round(n))]++
				total++
			}
		}
	}
	var best int64
	for n, f := range freq {
		if f > freq[best] || (f == freq[best] && n > best) {
			best = n
		}
	}
	if total == 0 {
		return 0, 0
	}
	return float64(best), float64(freq[best]) / float64(total)
}

// swSeriesKey identifies a series by its labels, so a value series finds its count.
func swSeriesKey(metric map[string]string) string {
	keys := make([]string, 0, len(metric))
	for k := range metric {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + metric[k] + "\x00")
	}
	return b.String()
}

// FetchMetricList returns all available SWO metric names, optionally filtered
// by a case-insensitive substring match on req.Metric.
func (s *SolarWindsMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SolarWindsMetricSource.FetchMetricList: failed to get configs", "error", err)
		return nil, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	infos, err := swFetchAllMetricInfos(apiToken, baseURL)
	if err != nil {
		return nil, err
	}

	nameFilter := strings.ToLower(req.Metric)
	metrics := make([]OutputMetrics, 0, len(infos))
	for _, m := range infos {
		if nameFilter == "" || strings.Contains(strings.ToLower(m.Name), nameFilter) {
			metrics = append(metrics, OutputMetrics{Metric: m.Name, Attributes: map[string]any{}})
		}
	}
	return metrics, nil
}

// FetchMetricLabelValues returns distinct values for a label/dimension on a metric.
//
// req.Request required keys:
//
//	"metric" – the SWO metric name (e.g. "k8s.cluster.spec.memory.requests")
//
// If "metric" is absent, an empty slice is returned — the SWO attributes endpoint
// is per-metric and cannot enumerate values without a metric context.
//
// The values are those seen in the request's window (the last hour when none is
// given): without one SWO answers for its whole retention, which listed 222 nodes on a
// cluster that had 87 in the last hour.
func (s *SolarWindsMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	if req.Label == "" {
		return nil, fmt.Errorf("label name is required")
	}
	metricName := swStringFromRequest(req.Request, "metric", swStringFromRequest(req.Request, "metric_name", ""))
	if metricName == "" {
		return []OutputMetricsLabelValues{}, nil
	}

	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	from, to := swTraceTimeRange(req.StartTime, req.EndTime)
	values, err := swFetchMetricAttributeValues(apiToken, baseURL, metricName, req.Label, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]OutputMetricsLabelValues, 0, len(values))
	for _, v := range values {
		out = append(out, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return out, nil
}

// swFetchMetricAttributeValues pages GET /v1/metrics/{name}/attributes/{attr} for the
// window and returns the non-empty values.
func swFetchMetricAttributeValues(apiToken, baseURL, metricName, label, from, to string) ([]string, error) {
	path := fmt.Sprintf("/v1/metrics/%s/attributes/%s", url.PathEscape(metricName), url.PathEscape(label))
	var out []string
	err := swPageAttributes(apiToken, baseURL, path, from, to, func(body []byte) (*swPageInfo, error) {
		var resp struct {
			Values   []string    `json:"values"`
			PageInfo *swPageInfo `json:"pageInfo"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse SolarWinds label values response: %w", err)
		}
		for _, v := range resp.Values {
			if v != "" {
				out = append(out, v)
			}
		}
		return resp.PageInfo, nil
	})
	if err != nil {
		return nil, fmt.Errorf("SolarWinds label values: %w", err)
	}
	return out, nil
}

// swFetchMetricAttributeNames pages GET /v1/metrics/{name}/attributes for the window.
func swFetchMetricAttributeNames(apiToken, baseURL, metricName, from, to string) ([]string, error) {
	path := fmt.Sprintf("/v1/metrics/%s/attributes", url.PathEscape(metricName))
	var out []string
	err := swPageAttributes(apiToken, baseURL, path, from, to, func(body []byte) (*swPageInfo, error) {
		var resp struct {
			Names    []string    `json:"names"`
			PageInfo *swPageInfo `json:"pageInfo"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse SolarWinds attribute names response: %w", err)
		}
		for _, n := range resp.Names {
			if n != "" {
				out = append(out, n)
			}
		}
		return resp.PageInfo, nil
	})
	if err != nil {
		return nil, fmt.Errorf("SolarWinds labels: %w", err)
	}
	return out, nil
}

// swPageAttributes follows skipToken over one of the attributes endpoints, capped at
// swMaxPages, handing each page's body to read.
func swPageAttributes(apiToken, baseURL, path, from, to string, read func([]byte) (*swPageInfo, error)) error {
	params := map[string]string{}
	if from != "" {
		params["startTime"] = from
	}
	if to != "" {
		params["endTime"] = to
	}
	for page := 0; page < swMaxPages; page++ {
		body, statusCode, err := solarWindsDoGET(apiToken, baseURL, path, params)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		if statusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d: %s", statusCode, string(body))
		}
		pageInfo, err := read(body)
		if err != nil {
			return err
		}
		next := swNextSkipToken(pageInfo)
		if next == "" {
			return nil
		}
		params["skipToken"] = next
	}
	return nil
}

// FetchMetricsLabels returns the attribute (label) keys SWO has recorded for
// req.MetricName in the request's window (the last hour when none is given).
//
// It reads /v1/metrics/{name}/attributes. It used to sample measurements grouped by a
// fixed list of keys and read the keys back from the groupings, which could only ever
// return the keys it had asked for.
func (s *SolarWindsMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	if req.MetricName == "" {
		return []OutputMetricLabels{}, nil
	}

	apiToken, dataCenter, err := integrations.GetSolarWindsConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf(swMetricConfigsErrFmt, err)
	}
	baseURL := integrations.SolarWindsAPIBaseURL(dataCenter)

	from, to := swTraceTimeRange(req.StartTime, req.EndTime)
	names, err := swFetchMetricAttributeNames(apiToken, baseURL, req.MetricName, from, to)
	if err != nil {
		return nil, err
	}
	labels := make([]OutputMetricLabels, 0, len(names))
	for _, n := range names {
		labels = append(labels, OutputMetricLabels{Label: n, Attributes: map[string]any{}})
	}
	return labels, nil
}

// --- private helpers ---

// swFetchAllMetricInfos paginates GET /v1/metrics and returns all metric info
// records, capped at swMaxPages for safety.
func swFetchAllMetricInfos(apiToken, baseURL string) ([]swMetricInfo, error) {
	params := map[string]string{"pageSize": "500"}
	var all []swMetricInfo

	for page := 0; page < swMaxPages; page++ {
		body, statusCode, err := integrations.DoSolarWindsGET(apiToken, baseURL, "/v1/metrics", params)
		if err != nil {
			return nil, fmt.Errorf("SolarWinds metrics list request failed: %w", err)
		}
		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("SolarWinds metrics list API returned HTTP %d: %s", statusCode, string(body))
		}

		var resp swMetricsListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse SolarWinds metrics list response: %w", err)
		}
		all = append(all, resp.MetricsInfo...)

		next := swNextSkipToken(resp.PageInfo)
		if next == "" {
			break
		}
		params["skipToken"] = next
	}
	return all, nil
}

// swMetricMeasurementsResponse is the measurements response plus the bucket width
// SWO chose for the window, which only the metrics path needs.
type swMetricMeasurementsResponse struct {
	swMeasurementsResponse
	BucketSizeInSeconds int64 `json:"bucketSizeInSeconds"`
}

// Page sizes for the measurements API. SWO refuses a page whose body is too big
// ("Response payload too large", HTTP 500) -- many groups over a long window -- so the
// fetch restarts at half the page size, down to the floor.
const (
	swMeasurementsPageSize      = 100
	swMeasurementsPageSizeFloor = 10
)

// fetchAllMeasurements paginates GET /v1/metrics/{path} and returns all groupings
// and the bucket width SWO reported (0 when absent), capped at swMaxPages for safety.
func fetchAllMeasurements(apiToken, baseURL, path string, q swMeasurementQuery) ([]swMeasurementGrouping, int64, error) {
	params := swBuildMeasurementParams(q)
	pageSize := swMeasurementsPageSize
	params["pageSize"] = strconv.Itoa(pageSize)

	var all []swMeasurementGrouping
	var bucketSeconds int64
	for page := 0; page < swMaxPages; page++ {
		body, statusCode, err := solarWindsDoGET(apiToken, baseURL, path, params)
		if err != nil {
			return nil, 0, fmt.Errorf("request failed: %w", err)
		}
		if statusCode != http.StatusOK && swPayloadTooLarge(body) {
			if pageSize == swMeasurementsPageSizeFloor {
				return nil, 0, fmt.Errorf("SolarWinds refused the response as too large even at %d groups per page; "+
					"ask for a narrower window, filter to fewer series, or group by fewer attributes", pageSize)
			}
			// Start over from the first page at the smaller size. Nothing says a skipToken
			// issued for one page size addresses the same position at another, so the
			// groupings collected so far are discarded rather than risk gaps or repeats.
			pageSize = max(pageSize/2, swMeasurementsPageSizeFloor)
			params["pageSize"] = strconv.Itoa(pageSize)
			delete(params, "skipToken")
			all, bucketSeconds, page = nil, 0, -1
			continue
		}
		if statusCode != http.StatusOK {
			return nil, 0, fmt.Errorf("HTTP %d: %s", statusCode, string(body))
		}

		var resp swMetricMeasurementsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, 0, fmt.Errorf("failed to parse measurements response: %w", err)
		}
		all = append(all, resp.Groupings...)
		if resp.BucketSizeInSeconds > 0 {
			bucketSeconds = resp.BucketSizeInSeconds
		}

		next := swNextSkipToken(resp.PageInfo)
		if next == "" {
			break
		}
		params["skipToken"] = next
	}
	return all, bucketSeconds, nil
}

// swDropPartialBuckets removes, from every grouping, the buckets that may still be
// filling.
//
// SWO returns the newest bucket while data for it is still arriving: measured, one
// bucket read 6.34 GB and then 7.05 GB 75 seconds later, while older buckets stayed
// put. An instant read took that bucket, and an aggregate (min especially) or a
// cross-node percentile would read the shortfall as a real dip. A bucket is kept
// only when it ended at least one bucket width before the window's end -- one width
// of margin for late data, since a bucket that has merely closed can still be short.
//
// The width is the one SWO reported for the response; when it is absent it is taken
// from the closest pair of consecutive timestamps. With no width at all nothing is
// dropped, because there is nothing to judge completeness against.
func swDropPartialBuckets(groupings []swMeasurementGrouping, bucketSeconds int64, end time.Time) {
	if bucketSeconds <= 0 {
		bucketSeconds = swInferBucketSeconds(groupings)
	}
	if bucketSeconds <= 0 || end.IsZero() {
		return
	}
	width := time.Duration(bucketSeconds) * time.Second
	cutoff := end.Add(-width)
	for i := range groupings {
		kept := groupings[i].Measurements[:0]
		for _, m := range groupings[i].Measurements {
			start, err := time.Parse(time.RFC3339, m.Time)
			if err != nil || start.Add(width).After(cutoff) {
				continue
			}
			kept = append(kept, m)
		}
		groupings[i].Measurements = kept
	}
}

// swInferBucketSeconds is the smallest gap between consecutive bucket timestamps in
// any grouping, or 0 when no grouping has two parseable buckets.
func swInferBucketSeconds(groupings []swMeasurementGrouping) int64 {
	var smallest int64
	for _, g := range groupings {
		var prev time.Time
		for _, m := range g.Measurements {
			t, err := time.Parse(time.RFC3339, m.Time)
			if err != nil {
				continue
			}
			if !prev.IsZero() {
				if gap := int64(t.Sub(prev).Seconds()); gap > 0 && (smallest == 0 || gap < smallest) {
					smallest = gap
				}
			}
			prev = t
		}
	}
	return smallest
}

// swPayloadTooLarge reports whether SWO refused a page for its size.
func swPayloadTooLarge(body []byte) bool {
	return strings.Contains(strings.ToLower(string(body)), "payload too large")
}

// swBuildMeasurementParams converts a swMeasurementQuery into the HTTP query-param
// map expected by DoSolarWindsGET, omitting empty optional fields.
func swBuildMeasurementParams(q swMeasurementQuery) map[string]string {
	params := map[string]string{"aggregateBy": q.aggregateBy}
	if q.from != "" {
		params["startTime"] = q.from
	}
	if q.to != "" {
		params["endTime"] = q.to
	}
	if q.groupBy != "" {
		params["groupBy"] = q.groupBy
	}
	if q.filter != "" {
		params["filter"] = q.filter
	}
	if q.preGroupBy != "" {
		params["preGroupBy"] = q.preGroupBy
		params["preGroupByMethod"] = q.preGroupByMethod
	}
	return params
}

// swNextSkipToken extracts the skipToken cursor from a SWO PageInfo URL.
// Returns "" when there are no more pages or the URL cannot be parsed.
func swNextSkipToken(pageInfo *swPageInfo) string {
	if pageInfo == nil || pageInfo.NextPage == "" {
		return ""
	}
	u, err := url.Parse(pageInfo.NextPage)
	if err != nil {
		return ""
	}
	return u.Query().Get("skipToken")
}

// swAttrToLabel maps SolarWinds OTel attribute keys to the short canonical label names
// used in Result.Metric, matching the label convention of Prometheus/Datadog/NewRelic so
// the UI can render SWO responses uniformly without provider-specific handling.
var swAttrToLabel = map[string]string{
	"k8s.node.name":        "node",
	"k8s.namespace.name":   "namespace",
	"k8s.deployment.name":  "workload",
	"k8s.statefulset.name": "workload",
	"k8s.daemonset.name":   "workload",
	"k8s.pod.name":         "pod",
	"k8s.container.name":   "container",
	"service.name":         "workload_name",
	"sw.transaction":       "span_name",
	"otel.status_code":     "status_code",
}

// swGroupingsToQueryResult converts SWO measurement groupings into a QueryResult.
//
// Each grouping with at least one value becomes one Result series; a grouping with none
// is dropped, so a filter that matches nothing returns zero series. Sparse (nil) bucket
// values are dropped;
// their corresponding timestamps are also omitted so the two slices stay aligned.
// Timestamps are in Unix seconds.
//
// SWO OTel attribute keys (e.g. "k8s.node.name") are mapped to the same short canonical
// labels used by Prometheus/Datadog/NewRelic (e.g. "node") so the UI can render results
// uniformly. The internal "__name__" field is not emitted — it is not part of the
// canonical Result.Metric contract.
//
// When instant is true, each series is reduced to a single data point (the last non-nil
// bucket), matching the single-timestamp/value format returned by Prometheus instant queries.
// The caller drops still-filling buckets first (swDropPartialBuckets), so that point is the
// newest COMPLETE bucket.
func swGroupingsToQueryResult(groupings []swMeasurementGrouping, queryKey, metricName string, instant bool) QueryResult {
	qr := QueryResult{
		QueryKey: queryKey,
		Query:    metricName,
		Payload:  make([]Result, 0, len(groupings)),
	}

	for _, g := range groupings {
		if len(swLastMeasurement(g.Measurements)) == 0 {
			// No data at all. SWO answers a filter that matches nothing with one such
			// grouping; passing it on returned one empty {} series, which reads as a
			// matching series that happened to be idle.
			continue
		}
		metric := make(map[string]string, len(g.Attributes))
		for _, a := range g.Attributes {
			if a.Key == "" {
				continue
			}
			labelKey := a.Key
			if canonical, ok := swAttrToLabel[a.Key]; ok {
				labelKey = canonical
				// Deployment, statefulset and daemonset all map to workload, and a series
				// carries the two it is not as empty strings: the first non-empty wins.
				if metric[labelKey] != "" {
					continue
				}
			}
			metric[labelKey] = a.Value
		}

		measurements := g.Measurements
		if instant {
			measurements = swLastMeasurement(g.Measurements)
		}

		qr.Payload = append(qr.Payload, Result{
			Metric:     metric,
			Timestamps: swExtractTimestamps(measurements),
			Values:     swExtractValues(measurements),
		})
	}
	return qr
}

// swLastMeasurement returns a slice containing only the last non-nil bucket measurement.
// Used for instant queries to mirror the single [timestamp, value] format of Prometheus
// instant query results. Returns an empty slice when all buckets are nil.
func swLastMeasurement(measurements []swBucketMeasurement) []swBucketMeasurement {
	for i := len(measurements) - 1; i >= 0; i-- {
		if measurements[i].Value != nil {
			return measurements[i : i+1]
		}
	}
	return nil
}

// swExtractTimestamps returns Unix-second timestamps for all non-nil bucket measurements.
func swExtractTimestamps(measurements []swBucketMeasurement) []int64 {
	out := make([]int64, 0, len(measurements))
	for _, m := range measurements {
		if m.Value == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, m.Time)
		if err != nil {
			continue
		}
		out = append(out, t.Unix())
	}
	return out
}

// swExtractValues returns the float64 values for all non-nil bucket measurements,
// in the same order as swExtractTimestamps so the two slices stay aligned.
func swExtractValues(measurements []swBucketMeasurement) []float64 {
	out := make([]float64, 0, len(measurements))
	for _, m := range measurements {
		if m.Value != nil {
			out = append(out, *m.Value)
		}
	}
	return out
}

// swStringFromRequest extracts a string from the freeform Request map with a default.
func swStringFromRequest(r map[string]any, key, defaultVal string) string {
	if r == nil {
		return defaultVal
	}
	if v, ok := r[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}
