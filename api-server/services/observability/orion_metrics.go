package observability

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/integrations"
	"nudgebee/services/integrations/orionclient"
	"nudgebee/services/security"
)

// OrionMetricSource reads a self-hosted SolarWinds Platform (Orion) server through
// SWIS, its SWQL endpoint.
//
// The query text carried through the utilisation layer is SWQL, not PromQL. Two
// column aliases give it the shape the layer expects:
//
//	nb_time   the sample's timestamp   (omitted for a current-value query)
//	nb_value  the sample's number
//
// every other column becomes a label on the series, which is what lets the fleet
// queries publish `instance`, `local_ipv4` and `hostname` the way node-agent's
// identity series do.
type OrionMetricSource struct{}

// orionMaxRows bounds one answer. Orion merges three retention tiers into one view,
// so a wide window over a busy node can return a great many rows; a query that would
// exceed this is reported rather than silently truncated.
const orionMaxRows = 50000

// FetchMetricsQuery runs each SWQL statement and converts its rows into series.
// A statement that fails carries its error on its own result rather than failing
// the whole request, so one broken key cannot blank a page of charts.
func (s *OrionMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	// Checked before anything is fetched, so a filter Orion cannot express fails
	// the request rather than coming back as an unfiltered answer.
	if err := orionRejectMatchers(req); err != nil {
		return OutputMetricQuery{}, err
	}

	cfg, err := integrations.GetOrionConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	start, end := orionWindow(req.StartTime, req.EndTime)
	params := map[string]any{
		"start": start.UTC().Format(orionTimeLayout),
		"end":   end.UTC().Format(orionTimeLayout),
	}

	results := make([]QueryResult, 0, len(req.Queries))
	for _, key := range sortedKeys(req.Queries) {
		swql := req.Queries[key]
		result := QueryResult{QueryKey: key, Query: swql, Payload: []Result{}}

		rows, qErr := cfg.Query(ctx.GetContext(), swql, orionParamsFor(swql, params))
		switch {
		case qErr != nil:
			msg := qErr.Error()
			result.Error = &msg
			ctx.GetLogger().Warn("observability: Orion query failed",
				"account_id", req.AccountId, "key", key, "error", qErr)
		case len(rows) > orionMaxRows:
			msg := fmt.Sprintf("Orion returned %d rows for this window, above the %d row limit; narrow the time range", len(rows), orionMaxRows)
			result.Error = &msg
		default:
			result.Payload = orionRowsToSeries(rows, end)
		}
		results = append(results, result)
	}
	return OutputMetricQuery{Results: results}, nil
}

// orionTimeLayout is what SWIS accepts and returns. Its DateTime columns come back
// without a zone and are UTC (verified against a live 2026.4.0 server).
const orionTimeLayout = "2006-01-02T15:04:05Z"

// orionRowsToSeries groups rows into series by their label columns. A row's
// nb_value is the sample; nb_time is its timestamp, and a row without one is a
// current value stamped at the end of the window, which is what the fleet gauges
// return.
func orionRowsToSeries(rows []orionclient.Row, at time.Time) []Result {
	type bucket struct {
		labels map[string]string
		times  []int64
		values []float64
	}
	order := make([]string, 0, len(rows))
	buckets := make(map[string]*bucket, len(rows))

	for _, row := range rows {
		labels := make(map[string]string, len(row))
		var (
			value    float64
			hasValue bool
			ts       = at
		)
		for col, raw := range row {
			switch col {
			case orionColValue:
				value, hasValue = orionFloat(raw)
			case orionColTime:
				if parsed, ok := orionTime(raw); ok {
					ts = parsed
				}
			default:
				if str := orionLabel(raw); str != "" {
					labels[col] = str
				}
			}
		}
		if !hasValue {
			// A row whose measurement is NULL is a gap, not a zero: Orion returns
			// null for a poll it never got. Dropping it keeps a chart's gap honest.
			continue
		}

		id := orionSeriesID(labels)
		b, seen := buckets[id]
		if !seen {
			b = &bucket{labels: labels}
			buckets[id] = b
			order = append(order, id)
		}
		b.times = append(b.times, ts.Unix())
		b.values = append(b.values, value)
	}

	series := make([]Result, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		series = append(series, Result{Metric: b.labels, Timestamps: b.times, Values: b.values})
	}
	return series
}

// orionSeriesID is the identity of a series: its label set.
func orionSeriesID(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte(',')
	}
	return b.String()
}

// orionFloat reads a SWIS numeric. JSON gives float64 for numbers; SWIS sends some
// 64-bit values as strings, and null for a poll that never arrived.
func orionFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case nil:
		return 0, false
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// orionTime parses a SWIS DateTime. They arrive without a zone, as UTC, and with a
// fractional part of varying length.
func orionTime(raw any) (time.Time, bool) {
	s, ok := raw.(string)
	if !ok {
		return time.Time{}, false
	}
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		"2006-01-02T15:04:05.9999999",
		"2006-01-02T15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			// A layout without a zone parses as UTC, which is what SWIS means;
			// one with a zone is converted. Either way the series carries UTC.
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// orionLabel renders a label column. Orion's integer ids arrive as numbers, and the
// label contract is strings.
func orionLabel(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return fmt.Sprintf("%v", raw)
}

// orionParamsFor passes only the placeholders a statement actually carries. SWIS
// rejects a parameter the query does not name.
func orionParamsFor(swql string, all map[string]any) map[string]any {
	out := make(map[string]any, len(all))
	for name, value := range all {
		if strings.Contains(swql, "@"+name) {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// orionWindow turns the request's millisecond bounds into times, defaulting to the
// last hour when a caller sends none.
func orionWindow(startMs, endMs int64) (time.Time, time.Time) {
	end := time.Now().UTC()
	if endMs > 0 {
		end = time.UnixMilli(endMs).UTC()
	}
	start := end.Add(-time.Hour)
	if startMs > 0 {
		start = time.UnixMilli(startMs).UTC()
	}
	if !start.Before(end) {
		start = end.Add(-time.Hour)
	}
	return start, end
}

// GetQuery returns the statement as written. SWQL is already the query text here,
// so there is nothing to render.
func (s *OrionMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	if err := orionRejectMatchers(req); err != nil {
		return "", err
	}
	for _, key := range sortedKeys(req.Queries) {
		return req.Queries[key], nil
	}
	return "", nil
}

// WrapAggregate implements MetricAggregateWrapper. Without it the layer would wrap
// SWQL in PromQL's `sum(...)`, which SWIS cannot run. Orion's statistics rows are
// already aggregates (Min/Avg/Max per observation), so an outer aggregate cannot be
// written into the statement and is refused rather than silently ignored.
//
// The utilisation path does honour an aggregate: it fetches the range and reduces
// it afterwards (orionCollapseOverTime). The difference is deliberate — a query
// this layer hands back as TEXT must be runnable as written, while a collapse
// computed from a fetched range need not be expressible in SWQL at all.
func (s *OrionMetricSource) WrapAggregate(expr string, aggregateOperator string) (string, error) {
	if aggregateOperator == "" {
		return expr, nil
	}
	return "", fmt.Errorf("aggregate_operator %q is not supported for SolarWinds Platform (Orion) metrics: its statistics tables store per-observation averages, and SWQL cannot re-aggregate them in the query the way PromQL does", aggregateOperator)
}

// FetchMetricList lists the entities a person can query, which is the closest thing
// Orion has to a metric catalogue.
func (s *OrionMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, err := integrations.GetOrionConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	rows, err := cfg.Query(ctx.GetContext(),
		"SELECT FullName FROM Metadata.Entity WHERE FullName LIKE 'Orion.%' ORDER BY FullName", nil)
	if err != nil {
		return nil, err
	}
	out := make([]OutputMetrics, 0, len(rows))
	for _, row := range rows {
		if name := orionLabel(row["FullName"]); name != "" {
			out = append(out, OutputMetrics{Metric: name})
		}
	}
	return out, nil
}

// FetchMetricsLabels lists an entity's columns, which are what a query can select
// and filter on.
func (s *OrionMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, err := integrations.GetOrionConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	entity := strings.TrimSpace(req.MetricName)
	if entity == "" {
		return []OutputMetricLabels{}, nil
	}
	rows, err := cfg.Query(ctx.GetContext(),
		"SELECT Name FROM Metadata.Property WHERE EntityName = @entity AND IsNavigable = false ORDER BY Name",
		map[string]any{"entity": entity})
	if err != nil {
		return nil, err
	}
	labels := make([]OutputMetricLabels, 0, len(rows))
	for _, row := range rows {
		if name := orionLabel(row["Name"]); name != "" {
			labels = append(labels, OutputMetricLabels{Label: name})
		}
	}
	return labels, nil
}

// FetchMetricLabelValues reads the distinct values of one column.
func (s *OrionMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, err := integrations.GetOrionConfig(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	entity, column := strings.TrimSpace(orionRequestString(req.Request, "metric")), strings.TrimSpace(req.Label)
	if entity == "" || column == "" {
		return []OutputMetricsLabelValues{}, nil
	}
	// The entity and column are identifiers, not values, so they cannot be bound as
	// parameters. They are checked against Orion's own schema first, which means a
	// caller can only name something that exists.
	known, err := s.FetchMetricsLabels(ctx, FetchMetricLabelsRequest{AccountId: req.AccountId, MetricName: entity})
	if err != nil {
		return nil, err
	}
	if !orionHasLabel(known, column) {
		return nil, fmt.Errorf("%q is not a column of %s", column, entity)
	}
	rows, err := cfg.Query(ctx.GetContext(),
		fmt.Sprintf("SELECT DISTINCT %s AS v FROM %s", column, entity), nil)
	if err != nil {
		return nil, err
	}
	values := make([]OutputMetricsLabelValues, 0, len(rows))
	for _, row := range rows {
		if v := orionLabel(row["v"]); v != "" {
			values = append(values, OutputMetricsLabelValues{Value: v})
		}
	}
	return values, nil
}

// orionRequestString reads one string out of a request's free-form Request map.
func orionRequestString(request map[string]any, key string) string {
	if request == nil {
		return ""
	}
	if v, ok := request[key].(string); ok {
		return v
	}
	return ""
}

func orionHasLabel(labels []OutputMetricLabels, name string) bool {
	for _, l := range labels {
		if strings.EqualFold(l.Label, name) {
			return true
		}
	}
	return false
}

// GetSupportedOperators returns an empty slice, because this source does not
// translate label matchers into SWQL yet. Advertising operators it cannot apply
// would let the builder offer chips that are silently dropped, and the caller
// would read an unfiltered answer as a filtered one. The UI falls back to its
// minimal built-in descriptor set when this list is empty, as it does for
// SolarWinds. Add the tokens here in the same change that teaches
// orionMatcherFilter to render them.
func (s *OrionMetricSource) GetSupportedOperators() []string {
	return []string{}
}

// orionRejectMatchers fails a request carrying a filter this source cannot apply.
// Dropping the filter and answering anyway is the worse failure: the numbers come
// back looking right for a narrower question than was asked.
func orionRejectMatchers(req FetchMetricsRequest) error {
	if len(req.LabelMatchers) == 0 && len(req.Labels) == 0 {
		return nil
	}
	return fmt.Errorf("label filters are not supported for SolarWinds Platform (Orion) metrics yet; " +
		"the filter would be ignored and the answer would look filtered when it is not")
}
