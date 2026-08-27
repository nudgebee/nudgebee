package observability

import (
	"fmt"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SplunkEnterpriseMetricSource queries a Splunk Enterprise / Splunk Cloud metrics index
// with SPL's mstats command.
//
// Not to be confused with SplunkMetricSource, which targets Splunk Observability Cloud
// over SignalFlow. The two products store metrics in entirely different systems.
type SplunkEnterpriseMetricSource struct{}

// splunkEnterpriseMetricLabelMapping maps canonical label names to the dimension names
// the Splunk Distribution of the OpenTelemetry Collector writes. It mirrors
// splunkEnterpriseLogLabelMapping: resource attributes survive HEC with their dots
// intact, so it is k8s.pod.name rather than the flattened spellings other backends use.
var splunkEnterpriseMetricLabelMapping = map[string]string{
	"namespace": "k8s.namespace.name",
	"pod":       "k8s.pod.name",
	"container": "k8s.container.name",
	"node":      "k8s.node.name",
	"workload":  "k8s.deployment.name",
	"app":       "k8s.deployment.name",
	"cluster":   "k8s.cluster.name",
	"host":      "host.name",
	"service":   "service.name",
}

// splunkEnterpriseMetricGeneratingCommands are the only commands a metric query may
// begin with. Metric queries MUST start with a leading pipe — mstats and mcatalog are
// generating commands and there is no other way to read a metrics index — so the
// blanket "no leading pipe" rule that guards log queries cannot apply here. Restricting
// the opening command to these two keeps the exception as narrow as the requirement.
var splunkEnterpriseMetricGeneratingCommands = map[string]bool{
	"mstats":   true,
	"mcatalog": true,
}

// splunkEnterpriseMetricSearchTimeout bounds one metric search. Shorter than the log
// equivalent: mstats reads a purpose-built metrics index and is far cheaper than a
// full-text event search, so a slow one usually means a bad query rather than a big one.
const splunkEnterpriseMetricSearchTimeout = 30 * time.Second

// splunkEnterpriseMetricMaxRows caps rows returned by a single metric search. A time
// series is one row per (span bucket x dimension combination), so a wide BY clause over
// a long window multiplies quickly.
const splunkEnterpriseMetricMaxRows = 20000

// splunkEnterpriseCatalogLimit bounds catalog listings (metric names, dimension values).
const splunkEnterpriseCatalogLimit = 500

// splunkEnterpriseDefaultSpanSeconds is the bucket width used when the caller supplies no
// step interval. One minute matches the default scrape interval of the OTel collector.
const splunkEnterpriseDefaultSpanSeconds = 60

func (s *SplunkEnterpriseMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like"}
}

// splunkEnterpriseMetricIndex resolves the configured metrics index, failing with an
// actionable message when it is unset. Empty is a legitimate configuration — a Splunk
// holding only logs — so this must read as "not configured", never as a broken query.
func splunkEnterpriseMetricIndex(cfg integrations.SplunkEnterpriseConfig) (string, error) {
	if cfg.MetricIndex == "" {
		return "", fmt.Errorf(
			"splunk enterprise metrics are not configured for this account: set %s to a metrics index (datatype=metric)",
			integrations.SplunkEnterpriseConfigMetricIndex)
	}
	if !integrations.IsSafeSplunkIndexName(cfg.MetricIndex) {
		return "", fmt.Errorf("invalid or unsafe metrics index name: %q", cfg.MetricIndex)
	}
	return cfg.MetricIndex, nil
}

// validateSplunkEnterpriseMetricQuery is the metric counterpart of
// validateSplunkEnterpriseQuery. It relaxes exactly one rule and tightens another.
//
// Relaxed: a leading pipe is required rather than forbidden, because mstats and mcatalog
// are generating commands.
//
// Tightened: because that opening command carries its own WHERE clause, the query gets
// to choose its own index — so the configured index must actually appear in it. Without
// that check a caller-supplied metric query could read any index on the server, which is
// precisely the escape the log guard's no-leading-pipe rule exists to prevent.
func validateSplunkEnterpriseMetricQuery(spl, index string) error {
	trimmed := strings.TrimSpace(spl)
	if trimmed == "" {
		return fmt.Errorf("empty splunk metric query")
	}
	if !strings.HasPrefix(trimmed, "|") {
		return fmt.Errorf("splunk metric query must start with a generating command (%s)",
			strings.Join(sortedSplunkMetricCommands(), " or "))
	}

	segments := splunkEnterpriseSplitPipeline(trimmed)
	for i, segment := range segments {
		fields := strings.Fields(strings.TrimSpace(segment))
		if len(fields) == 0 {
			continue
		}
		command := strings.ToLower(strings.TrimPrefix(fields[0], "|"))
		if i == 0 && !splunkEnterpriseMetricGeneratingCommands[command] {
			return fmt.Errorf("splunk metric query may only begin with %s, got %q",
				strings.Join(sortedSplunkMetricCommands(), " or "), command)
		}
		if splunkEnterpriseDisallowedCommands[command] {
			return fmt.Errorf("splunk command %q is not permitted: this integration may only read", command)
		}
	}

	// Matched as a quoted literal so a query naming `otel_metrics_secret` cannot pass as
	// `otel_metrics`.
	if !strings.Contains(trimmed, fmt.Sprintf(`index="%s"`, index)) {
		return fmt.Errorf(`splunk metric query must be scoped to the configured metrics index (expected index="%s")`, index)
	}
	return nil
}

func sortedSplunkMetricCommands() []string {
	out := make([]string, 0, len(splunkEnterpriseMetricGeneratingCommands))
	for c := range splunkEnterpriseMetricGeneratingCommands {
		out = append(out, "`| "+c+"`")
	}
	sort.Strings(out)
	return out
}

// escapeSplunkMetricName guards a metric name interpolated into an mstats aggregation.
// Metric names routinely carry dots (k8s.pod.cpu.usage); anything outside this set could
// close the aggregation and append a command.
func isSafeSplunkMetricName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			r != '_' && r != '.' && r != ':' && r != '-' && r != '*' {
			return false
		}
	}
	return true
}

// splunkEnterpriseAggregation maps NudgeBee's aggregate operator vocabulary onto mstats
// aggregation functions. mstats has no stddev/stdvar over metrics, and `group` has no
// meaning without a value, so both fall back to avg rather than emitting invalid SPL.
func splunkEnterpriseAggregation(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "sum":
		return "sum"
	case "min":
		return "min"
	case "max":
		return "max"
	case "count":
		return "count"
	case "avg", "":
		return "avg"
	default:
		return "avg"
	}
}

// buildSplunkMetricMatcher renders one label matcher as an mstats WHERE predicate.
func buildSplunkMetricMatcher(m LabelMatcher) (string, error) {
	label := m.Label
	if mapped, ok := splunkEnterpriseMetricLabelMapping[label]; ok {
		label = mapped
	}
	if !isSafeSplunkFieldName(label) {
		return "", fmt.Errorf("invalid or unsafe dimension name: %q", m.Label)
	}
	value := escapeSplunkString(m.Value)

	switch m.Operator {
	case "", "_eq", "=", "==":
		return fmt.Sprintf(`%s="%s"`, label, value), nil
	case "_neq", "!=":
		return fmt.Sprintf(`%s!="%s"`, label, value), nil
	case "_prefix":
		// Deliberately appends an UNESCAPED trailing wildcard. escapeSplunkString turns a
		// literal `*` into `\*`, so a caller that embedded the wildcard in Value would get
		// an escaped asterisk and match nothing. Used for pod-name prefixes, where the
		// replica-hash suffix is unknown.
		return fmt.Sprintf(`%s="%s*"`, label, value), nil
	case "_like", "_contains", "=~":
		// mstats WHERE has no regex operator; wildcard match is the closest equivalent
		// and is what the log source's _contains maps to as well.
		return fmt.Sprintf(`%s="*%s*"`, label, value), nil
	default:
		return "", fmt.Errorf("unsupported metric label operator for Splunk: %s", m.Operator)
	}
}

// buildSplunkMStatsQuery renders one QueryItem as an mstats search.
//
// The value column is always aliased to a fixed name so the result decoder does not have
// to guess which column holds the number; mstats would otherwise name it after the
// aggregation and metric (`avg(k8s.pod.cpu.usage)`).
const splunkEnterpriseValueColumn = "nb_value"

func buildSplunkMStatsQuery(item QueryItem, labels map[string]string, index string, spanSeconds int, instant bool) (string, error) {
	if !isSafeSplunkMetricName(item.Metric) {
		return "", fmt.Errorf("invalid or unsafe metric name: %q", item.Metric)
	}
	if !integrations.IsSafeSplunkIndexName(index) {
		return "", fmt.Errorf("invalid or unsafe metrics index name: %q", index)
	}

	agg := splunkEnterpriseAggregation(item.AggregateOperator)

	var where []string
	where = append(where, fmt.Sprintf(`index="%s"`, index))

	// Sorted so the generated SPL is stable and assertable.
	for _, key := range sortedKeys(labels) {
		matcher, err := buildSplunkMetricMatcher(LabelMatcher{Label: key, Operator: "_eq", Value: labels[key]})
		if err != nil {
			return "", err
		}
		where = append(where, matcher)
	}
	for _, m := range item.LabelMatchers {
		matcher, err := buildSplunkMetricMatcher(m)
		if err != nil {
			return "", err
		}
		where = append(where, matcher)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `| mstats %s(%s) AS %s WHERE %s`, agg, item.Metric, splunkEnterpriseValueColumn, strings.Join(where, " AND "))

	if !instant {
		if spanSeconds <= 0 {
			spanSeconds = splunkEnterpriseDefaultSpanSeconds
		}
		fmt.Fprintf(&b, " span=%ds", spanSeconds)
	}

	// Group by every dimension the caller filtered on, so a query that narrows to one
	// namespace still returns a labelled series rather than an anonymous one. Without
	// this the frontend has no way to tell two series apart in a multi-series chart.
	groupBy := splunkEnterpriseGroupByDimensions(item, labels)
	if len(groupBy) > 0 {
		fmt.Fprintf(&b, " BY %s", strings.Join(groupBy, ", "))
	}

	return b.String(), nil
}

// splunkEnterpriseGroupByDimensions collects the distinct, mapped dimension names the
// query filters on, in stable order.
func splunkEnterpriseGroupByDimensions(item QueryItem, labels map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		label := raw
		if mapped, ok := splunkEnterpriseMetricLabelMapping[label]; ok {
			label = mapped
		}
		if !isSafeSplunkFieldName(label) || seen[label] {
			return
		}
		seen[label] = true
		out = append(out, label)
	}
	for _, key := range sortedKeys(labels) {
		add(key)
	}
	for _, m := range item.LabelMatchers {
		add(m.Label)
	}
	return out
}

// splunkEnterpriseQueryItems normalises a FetchMetricsRequest into query items.
//
// The BUILDER path sends QueryItems keyed by query name. The raw/code path sends
// Queries, whose values are already complete SPL and are passed through untouched (still
// validated). Callers that send neither get an explicit error rather than an empty chart.
func splunkEnterpriseQueryItems(req FetchMetricsRequest) (items map[string]QueryItem, raw map[string]string) {
	if len(req.QueryItems) > 0 {
		return req.QueryItems, nil
	}
	return nil, req.Queries
}

// GetQuery renders the SPL a request would run, without executing it.
func (s *SplunkEnterpriseMetricSource) GetQuery(ctx *security.RequestContext, req FetchMetricsRequest) (string, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, req.AccountId)
	if err != nil {
		return "", fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return "", err
	}

	items, raw := splunkEnterpriseQueryItems(req)
	for _, key := range sortedKeys(rawKeys(items, raw)) {
		if items != nil {
			return buildSplunkMStatsQuery(items[key], req.Labels, index, req.StepInterval, req.Instant)
		}
		return raw[key], nil
	}
	return "", nil
}

// rawKeys returns a set-like map of the keys present in whichever of the two shapes is
// populated, so callers can iterate them in sorted order.
func rawKeys(items map[string]QueryItem, raw map[string]string) map[string]string {
	out := map[string]string{}
	for k := range items {
		out[k] = ""
	}
	for k := range raw {
		out[k] = ""
	}
	return out
}

// FetchMetricsQuery runs each query and converts the rows into series.
//
// A failure on one query is recorded on that query's own result rather than failing the
// batch: a dashboard with six panels should render the five that work.
func (s *SplunkEnterpriseMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)
	startTime, endTime := splunkEnterpriseTimeRangeSeconds(startMs, endMs, time.Now())

	items, raw := splunkEnterpriseQueryItems(req)
	results := OutputMetricQuery{Results: []QueryResult{}}

	for _, queryKey := range sortedKeys(rawKeys(items, raw)) {
		var spl string
		var buildErr error
		if items != nil {
			spl, buildErr = buildSplunkMStatsQuery(items[queryKey], req.Labels, index, req.StepInterval, req.Instant)
		} else {
			spl = raw[queryKey]
		}
		if buildErr != nil {
			results.Results = append(results.Results, splunkMetricErrorResult(queryKey, spl, buildErr))
			continue
		}
		if vErr := validateSplunkEnterpriseMetricQuery(spl, index); vErr != nil {
			results.Results = append(results.Results, splunkMetricErrorResult(queryKey, spl, vErr))
			continue
		}

		ctx.GetLogger().Info("splunk enterprise mstats query", "key", queryKey, "spl", spl)

		rows, runErr := execSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
			splunkEnterpriseMetricMaxRows, splunkEnterpriseMetricSearchTimeout)
		if runErr != nil {
			results.Results = append(results.Results, splunkMetricErrorResult(queryKey, spl, runErr))
			continue
		}

		results.Results = append(results.Results, convertSplunkMetricRows(queryKey, spl, rows))
	}

	return results, nil
}

func splunkMetricErrorResult(queryKey, spl string, err error) QueryResult {
	msg := err.Error()
	return QueryResult{QueryKey: queryKey, Query: spl, Error: &msg}
}

// convertSplunkMetricRows folds mstats rows into series.
//
// mstats returns one flat row per (time bucket x dimension combination). Rows are
// grouped by their dimension set — every column except _time and the aliased value — so
// each distinct combination becomes one series with aligned timestamp/value slices.
func convertSplunkMetricRows(queryKey, spl string, rows []map[string]any) QueryResult {
	type series struct {
		metric     map[string]string
		timestamps []int64
		values     []float64
	}
	order := []string{}
	bySeries := map[string]*series{}

	for _, row := range rows {
		valueRaw, ok := row[splunkEnterpriseValueColumn]
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(formatSplunkValue(valueRaw)), 64)
		if err != nil {
			// A non-numeric value column means the row is not a data point (mstats can
			// emit a null for a bucket with no samples). Skipping keeps the series
			// contiguous instead of injecting a zero that reads as a real measurement.
			continue
		}

		labels := map[string]string{}
		for k, v := range row {
			if k == splunkEnterpriseValueColumn || k == "_time" || strings.HasPrefix(k, "_") {
				continue
			}
			labels[k] = formatSplunkValue(v)
		}

		key := splunkMetricSeriesKey(labels)
		s, exists := bySeries[key]
		if !exists {
			s = &series{metric: labels}
			bySeries[key] = s
			order = append(order, key)
		}

		s.timestamps = append(s.timestamps, splunkEnterpriseRowTimestampMs(row))
		s.values = append(s.values, value)
	}

	payload := make([]Result, 0, len(order))
	for _, key := range order {
		s := bySeries[key]
		payload = append(payload, Result{
			Metric:     s.metric,
			Timestamps: s.timestamps,
			Values:     s.values,
		})
	}

	result := QueryResult{QueryKey: queryKey, Query: spl, Payload: payload}
	if len(payload) == 0 {
		matched := int64(len(rows))
		result.DocsMatched = &matched
		if len(rows) > 0 {
			result.Note = "mstats returned rows but none carried a numeric value column"
		}
	}
	return result
}

// splunkMetricSeriesKey builds a stable identity for a dimension set.
func splunkMetricSeriesKey(labels map[string]string) string {
	var b strings.Builder
	for _, k := range sortedKeys(labels) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte('\x1f')
	}
	return b.String()
}

// splunkEnterpriseRowTimestampMs reads _time from an mstats row.
//
// An instant query (no span) carries no _time at all, so "now" is the only honest answer
// for those — the value describes the window that was just queried.
func splunkEnterpriseRowTimestampMs(row map[string]any) int64 {
	raw, ok := row["_time"]
	if !ok {
		return time.Now().UnixMilli()
	}
	text := strings.TrimSpace(formatSplunkValue(raw))
	if text == "" {
		return time.Now().UnixMilli()
	}
	// Splunk emits ISO-8601 with a numeric offset for search results, and bare epoch
	// seconds for some export paths; accept both.
	if t, err := time.Parse("2006-01-02T15:04:05.000-07:00", text); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t.UnixMilli()
	}
	if secs, err := strconv.ParseFloat(text, 64); err == nil {
		return int64(secs * 1000)
	}
	return time.Now().UnixMilli()
}

// FetchMetricList lists metric names in the configured metrics index.
//
// Uses mcatalog rather than /services/catalog/metricstore/metrics deliberately: that
// REST endpoint returns an empty list unless an index filter is supplied (verified on
// Splunk 10.4.2 — unfiltered returned 0 entries while the same call with
// filter=index=<name> returned every metric), which makes it easy to ship a silently
// empty picker. mcatalog also reuses the search plumbing already built here rather than
// adding a second HTTP path with its own auth and TLS handling.
func (s *SplunkEnterpriseMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, index, err := splunkEnterpriseMetricContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	spl := fmt.Sprintf(`| mcatalog values(metric_name) WHERE index="%s"`, index)
	rows, err := s.runMetricCatalog(ctx, cfg, index, spl, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	names := splunkEnterpriseCatalogValues(rows, "values(metric_name)")
	filter := strings.TrimSpace(req.Metric)
	metrics := make([]OutputMetrics, 0, len(names))
	for _, name := range names {
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		metrics = append(metrics, OutputMetrics{Metric: name, Attributes: map[string]any{}})
	}
	return metrics, nil
}

// FetchMetricsLabels lists the dimension names present in the metrics index.
//
// `values(_dims)` is preferred over the REST dimensions endpoint because that endpoint
// requires a metric_name argument (verified: omitting it returns "The following required
// arguments are missing: metric_name") and includes Splunk's own host/source/sourcetype
// alongside real dimensions. mcatalog returns just the dimensions.
func (s *SplunkEnterpriseMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, index, err := splunkEnterpriseMetricContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	where := fmt.Sprintf(`index="%s"`, index)
	if name := strings.TrimSpace(req.MetricName); name != "" {
		if !isSafeSplunkMetricName(name) {
			return nil, fmt.Errorf("invalid or unsafe metric name: %q", name)
		}
		where += fmt.Sprintf(` AND metric_name="%s"`, name)
	}
	spl := fmt.Sprintf(`| mcatalog values(_dims) WHERE %s`, where)

	rows, err := s.runMetricCatalog(ctx, cfg, index, spl, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	dims := splunkEnterpriseCatalogValues(rows, "values(_dims)")
	labels := make([]OutputMetricLabels, 0, len(dims))
	for _, d := range dims {
		labels = append(labels, OutputMetricLabels{Label: d, Attributes: map[string]any{}})
	}
	return labels, nil
}

// FetchMetricLabelValues lists the distinct values of one dimension.
func (s *SplunkEnterpriseMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, index, err := splunkEnterpriseMetricContext(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	label := strings.TrimSpace(req.Label)
	if mapped, ok := splunkEnterpriseMetricLabelMapping[label]; ok {
		label = mapped
	}
	if !isSafeSplunkFieldName(label) {
		return nil, fmt.Errorf("invalid or unsafe dimension name: %q", req.Label)
	}

	spl := fmt.Sprintf(`| mcatalog values(%s) WHERE index="%s"`, label, index)
	rows, err := s.runMetricCatalog(ctx, cfg, index, spl, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	values := splunkEnterpriseCatalogValues(rows, fmt.Sprintf("values(%s)", label))
	out := make([]OutputMetricsLabelValues, 0, len(values))
	for _, v := range values {
		out = append(out, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return out, nil
}

// splunkEnterpriseMetricContext resolves config + metrics index in one step, since every
// catalog call needs both and fails identically without them.
func splunkEnterpriseMetricContext(ctx *security.RequestContext, accountId string) (integrations.SplunkEnterpriseConfig, string, error) {
	cfg, err := integrations.GetSplunkEnterpriseConfig(ctx, accountId)
	if err != nil {
		return cfg, "", fmt.Errorf("failed to get Splunk Enterprise config: %w", err)
	}
	index, err := splunkEnterpriseMetricIndex(cfg)
	if err != nil {
		return cfg, "", err
	}
	return cfg, index, nil
}

// runMetricCatalog validates and executes a catalog query.
func (s *SplunkEnterpriseMetricSource) runMetricCatalog(
	ctx *security.RequestContext,
	cfg integrations.SplunkEnterpriseConfig,
	index, spl string,
	startMs, endMs int64,
) ([]map[string]any, error) {
	if err := validateSplunkEnterpriseMetricQuery(spl, index); err != nil {
		return nil, err
	}
	startTime, endTime := splunkEnterpriseTimeRangeSeconds(startMs, endMs, time.Now())
	ctx.GetLogger().Debug("splunk enterprise mcatalog query", "spl", spl)
	return execSplunkEnterpriseSearch(cfg, spl, startTime, endTime,
		splunkEnterpriseCatalogLimit, splunkEnterpriseMetricSearchTimeout)
}

// splunkEnterpriseCatalogValues pulls the multivalue column out of an mcatalog result.
//
// mcatalog returns a single row whose one column is an array. formatSplunkValue joins
// arrays with commas for display, so the raw value is read here instead to keep each
// entry separate — a dimension value containing a comma would otherwise be split in two.
func splunkEnterpriseCatalogValues(rows []map[string]any, column string) []string {
	var out []string
	seen := map[string]bool{}
	for _, row := range rows {
		raw, ok := row[column]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case []any:
			for _, item := range v {
				add := strings.TrimSpace(formatSplunkValue(item))
				if add != "" && !seen[add] {
					seen[add] = true
					out = append(out, add)
				}
			}
		default:
			add := strings.TrimSpace(formatSplunkValue(raw))
			if add != "" && !seen[add] {
				seen[add] = true
				out = append(out, add)
			}
		}
	}
	sort.Strings(out)
	return out
}
