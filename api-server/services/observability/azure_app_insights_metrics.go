package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
)

// AzureAppInsightsMetricSource implements MetricSource for Azure Application
// Insights. Metric data is queried with KQL against the Application Insights
// Analytics endpoint (the same endpoint the trace source uses). By default it
// targets the `customMetrics` table — the canonical home for named metrics with
// a numeric `value` column and a `customDimensions` bag — but a caller may pass
// a full KQL statement in Queries for advanced cases (e.g. performanceCounters).
//
// getConfigs/execQuery are function-var seams so the KQL builders and the
// columnar-response parser can be unit-tested without a live Azure app.
type AzureAppInsightsMetricSource struct {
	getConfigs func(*security.RequestContext, string) (integrations.AzureAppInsightsConfig, error)
	execQuery  func(*security.RequestContext, integrations.AzureAppInsightsConfig, string) (AzureResponse, error)
}

// azureMetricsTable is the default KQL table for named custom metrics.
const azureMetricsTable = "customMetrics"

func (s *AzureAppInsightsMetricSource) configs(ctx *security.RequestContext, accountId string) (integrations.AzureAppInsightsConfig, error) {
	if s.getConfigs != nil {
		return s.getConfigs(ctx, accountId)
	}
	return integrations.GetAzureAppInsightConfigs(ctx, accountId)
}

// exec runs a KQL query against the Application Insights Analytics endpoint and
// returns the decoded columnar response.
func (s *AzureAppInsightsMetricSource) exec(ctx *security.RequestContext, conf integrations.AzureAppInsightsConfig, kql string) (AzureResponse, error) {
	if s.execQuery != nil {
		return s.execQuery(ctx, conf, kql)
	}

	azureInsightsObj := integrations.AzureAppInsights{}
	url := "https://api.applicationinsights.io/v1/apps/" + conf.AzureAppInsightsAppID + "/query"
	headers, err := azureInsightsObj.GetHeader(conf, map[string]string{"Content-Type": "application/json"}, integrations.AzureApplicationInsightURL)
	if err != nil {
		return AzureResponse{}, fmt.Errorf("failed to get azure headers: %w", err)
	}

	resp, err := common.HttpPost(url, common.HttpWithHeaders(headers), common.HttpWithJsonBody(map[string]string{"query": kql}))
	if err != nil {
		return AzureResponse{}, fmt.Errorf("failed to call azure metrics api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return AzureResponse{}, fmt.Errorf("failed to read azure metrics body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return AzureResponse{}, fmt.Errorf("azure metric query failed: status=%d body=%s", resp.StatusCode, string(bodyBytes))
	}

	var out AzureResponse
	if err := common.UnmarshalJson(bodyBytes, &out); err != nil {
		return AzureResponse{}, fmt.Errorf("failed to unmarshal azure metrics: %w", err)
	}
	return out, nil
}

func (s *AzureAppInsightsMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_contains", "_like"}
}

func (s *AzureAppInsightsMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	startMs, endMs := azureMetricsTimeRangeMs(req.StartTime, req.EndTime)
	step := azureMetricsStepMinutes(req.StepInterval, startMs, endMs)
	for _, rawQuery := range req.Queries {
		return buildAzureMetricKql(rawQuery, startMs, endMs, step, req.Labels, req.LabelMatchers)
	}
	return "", nil
}

// FetchMetricsQuery executes each metric query and returns unified time series.
func (s *AzureAppInsightsMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	conf, err := s.configs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("AzureAppInsightsMetricSource.FetchMetricsQuery: failed to get configs", "error", err)
		return OutputMetricQuery{}, fmt.Errorf("failed to get azure app insights configs: %w", err)
	}

	startMs, endMs := azureMetricsTimeRangeMs(req.StartTime, req.EndTime)
	step := azureMetricsStepMinutes(req.StepInterval, startMs, endMs)

	results := OutputMetricQuery{Results: []QueryResult{}}
	for queryKey, rawQuery := range req.Queries {
		kql, buildErr := buildAzureMetricKql(rawQuery, startMs, endMs, step, req.Labels, req.LabelMatchers)
		if buildErr != nil {
			msg := buildErr.Error()
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &msg})
			continue
		}
		ctx.GetLogger().Info("Azure App Insights Metric Query", "key", queryKey, "query", kql)

		resp, execErr := s.exec(ctx, conf, kql)
		if execErr != nil {
			ctx.GetLogger().Error("AzureAppInsightsMetricSource.FetchMetricsQuery: query failed", "key", queryKey, "error", execErr)
			msg := execErr.Error()
			results.Results = append(results.Results, QueryResult{QueryKey: queryKey, Error: &msg})
			continue
		}
		results.Results = append(results.Results, azureMetricResponseToQueryResult(resp, queryKey, kql))
	}
	return results, nil
}

// FetchMetricList returns distinct metric names from the customMetrics table.
func (s *AzureAppInsightsMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	conf, err := s.configs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get azure app insights configs: %w", err)
	}

	kql := fmt.Sprintf("%s\n| where timestamp > ago(1d)", azureMetricsTable)
	if req.Metric != "" {
		kql += fmt.Sprintf("\n| where name contains '%s'", escapeKqlValue(req.Metric))
	}
	kql += "\n| distinct name\n| limit 1000"

	resp, err := s.exec(ctx, conf, kql)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch azure metric list: %w", err)
	}
	out := []OutputMetrics{}
	for _, v := range azureLabelValuesFromResponse(resp) {
		out = append(out, OutputMetrics{Metric: v, Attributes: map[string]any{}})
	}
	return out, nil
}

// FetchMetricsLabels returns the dimension keys available for a metric.
func (s *AzureAppInsightsMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	conf, err := s.configs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get azure app insights configs: %w", err)
	}

	kql := fmt.Sprintf("%s\n| where timestamp > ago(1d)", azureMetricsTable)
	if req.MetricName != "" {
		kql += fmt.Sprintf("\n| where name == '%s'", escapeKqlValue(req.MetricName))
	}
	kql += "\n| extend nb_dim_key = bag_keys(customDimensions)\n| mv-expand nb_dim_key to typeof(string)\n| distinct nb_dim_key\n| limit 200"

	resp, err := s.exec(ctx, conf, kql)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch azure metric labels: %w", err)
	}
	out := []OutputMetricLabels{}
	for _, v := range azureLabelValuesFromResponse(resp) {
		out = append(out, OutputMetricLabels{Label: v, Attributes: map[string]any{}})
	}
	return out, nil
}

// FetchMetricLabelValues returns distinct values for a given dimension.
func (s *AzureAppInsightsMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	conf, err := s.configs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get azure app insights configs: %w", err)
	}

	metricName := ""
	if req.Request != nil {
		if v, ok := req.Request["metric_name"].(string); ok {
			metricName = v
		}
	}
	kql := fmt.Sprintf("%s\n| where timestamp > ago(7d)", azureMetricsTable)
	if metricName != "" {
		kql += fmt.Sprintf("\n| where name == '%s'", escapeKqlValue(metricName))
	}
	// Project the dimension value into an explicitly named column rather than
	// relying on KQL's auto-generated `Column1`, which would break the
	// isnotempty() filter if the engine's naming changed.
	kql += fmt.Sprintf("\n| project nb_dim_val = tostring(customDimensions['%s'])\n| distinct nb_dim_val\n| where isnotempty(nb_dim_val)\n| limit 200", escapeKqlValue(req.Label))

	resp, err := s.exec(ctx, conf, kql)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch azure metric label values: %w", err)
	}
	out := []OutputMetricsLabelValues{}
	for _, v := range azureLabelValuesFromResponse(resp) {
		out = append(out, OutputMetricsLabelValues{Value: v, Attributes: map[string]any{}})
	}
	return out, nil
}

// --- KQL builders + response parsing (pure; unit-tested) ---

// buildAzureMetricKql builds a time-series KQL query. A rawQuery that already
// looks like KQL (contains a pipe or starts with a known verb) is passed
// through with only a time-range guard; otherwise it is treated as a metric
// name against customMetrics with avg(value) bucketed by bin(timestamp, step).
func buildAzureMetricKql(rawQuery string, startMs, endMs int64, stepMinutes int, labels map[string]string, matchers []LabelMatcher) (string, error) {
	timeFilter := fmt.Sprintf("| where timestamp between (datetime('%s') .. datetime('%s'))",
		azureMsToRFC3339(startMs), azureMsToRFC3339(endMs))

	if isAzureRawKql(rawQuery) {
		if strings.Contains(strings.ToLower(rawQuery), "where timestamp") {
			return rawQuery, nil
		}
		return injectAzureKqlAfterTable(rawQuery, timeFilter), nil
	}

	whereClauses, err := azureWhereClauses(labels, matchers)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(azureMetricsTable)
	b.WriteString("\n")
	b.WriteString(timeFilter)
	fmt.Fprintf(&b, "\n| where name == '%s'", escapeKqlValue(rawQuery))
	for _, c := range whereClauses {
		b.WriteString("\n| where ")
		b.WriteString(c)
	}
	fmt.Fprintf(&b, "\n| summarize value = avg(value) by bin(timestamp, %dm)\n| order by timestamp asc", stepMinutes)
	return b.String(), nil
}

// azureWhereClauses renders eq-only labels and operator-aware matchers into KQL
// predicates over customDimensions, sorted for deterministic output.
func azureWhereClauses(labels map[string]string, matchers []LabelMatcher) ([]string, error) {
	clauses := []string{}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		clauses = append(clauses, fmt.Sprintf("tostring(customDimensions['%s']) == '%s'", escapeKqlValue(k), escapeKqlValue(labels[k])))
	}

	sorted := make([]LabelMatcher, len(matchers))
	copy(sorted, matchers)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Label != sorted[j].Label {
			return sorted[i].Label < sorted[j].Label
		}
		if sorted[i].Operator != sorted[j].Operator {
			return sorted[i].Operator < sorted[j].Operator
		}
		return sorted[i].Value < sorted[j].Value
	})
	for _, m := range sorted {
		field := fmt.Sprintf("tostring(customDimensions['%s'])", escapeKqlValue(m.Label))
		val := escapeKqlValue(m.Value)
		switch m.Operator {
		case "_eq":
			clauses = append(clauses, fmt.Sprintf("%s == '%s'", field, val))
		case "_neq":
			clauses = append(clauses, fmt.Sprintf("%s != '%s'", field, val))
		case "_contains", "_like":
			clauses = append(clauses, fmt.Sprintf("%s contains '%s'", field, val))
		default:
			return nil, fmt.Errorf("unsupported operator %q for azure app insights metrics", m.Operator)
		}
	}
	return clauses, nil
}

// azureMetricResponseToQueryResult converts the columnar Azure response into a
// QueryResult. The timestamp column becomes the series time axis, the numeric
// `value` column the values, and any remaining string columns are dimension
// labels that split the rows into separate series.
func azureMetricResponseToQueryResult(resp AzureResponse, queryKey, query string) QueryResult {
	qr := QueryResult{QueryKey: queryKey, Query: query, Payload: []Result{}}
	if len(resp.Tables) == 0 {
		return qr
	}
	table := resp.Tables[0]

	tsIdx, valIdx := -1, -1
	labelIdx := map[string]int{}
	for i, col := range table.Columns {
		switch {
		case strings.EqualFold(col.Name, "timestamp"):
			tsIdx = i
		case strings.EqualFold(col.Name, "value"):
			valIdx = i
		default:
			labelIdx[col.Name] = i
		}
	}
	if tsIdx == -1 || valIdx == -1 {
		return qr
	}

	type acc struct {
		labels     map[string]string
		timestamps []int64
		values     []float64
	}
	seriesMap := map[string]*acc{}
	order := []string{}

	for _, row := range table.Rows {
		if tsIdx >= len(row) || valIdx >= len(row) {
			continue
		}
		tsMs, ok := azureParseTimestampMs(row[tsIdx])
		if !ok {
			continue
		}
		val, ok := azureToFloat(row[valIdx])
		if !ok {
			continue
		}
		labels := map[string]string{}
		lkeys := make([]string, 0, len(labelIdx))
		for name := range labelIdx {
			lkeys = append(lkeys, name)
		}
		sort.Strings(lkeys)
		for _, name := range lkeys {
			idx := labelIdx[name]
			if idx < len(row) && row[idx] != nil {
				labels[name] = fmt.Sprintf("%v", row[idx])
			}
		}
		key := buildSeriesKey(labels)
		if _, exists := seriesMap[key]; !exists {
			seriesMap[key] = &acc{labels: labels}
			order = append(order, key)
		}
		seriesMap[key].timestamps = append(seriesMap[key].timestamps, tsMs)
		seriesMap[key].values = append(seriesMap[key].values, val)
	}

	for _, key := range order {
		a := seriesMap[key]
		qr.Payload = append(qr.Payload, Result{Metric: a.labels, Timestamps: a.timestamps, Values: a.values})
	}
	return qr
}

// --- small pure helpers ---

func isAzureRawKql(q string) bool {
	t := strings.TrimSpace(q)
	return strings.Contains(t, "|") || strings.HasPrefix(strings.ToLower(t), "let ")
}

// injectAzureKqlAfterTable inserts a clause right after the leading table
// expression of a raw KQL query, before its first pipe operator. It scans
// line-by-line and skips blank lines, `//` comments and `let` statements so a
// pipe inside a string literal or let binding doesn't cause a misplaced insert.
func injectAzureKqlAfterTable(query, clause string) string {
	lines := strings.Split(query, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(strings.ToLower(t), "let ") {
			continue
		}
		if idx := strings.Index(line, "|"); idx != -1 {
			lines[i] = strings.TrimRight(line[:idx], " ") + "\n" + clause + "\n" + strings.TrimLeft(line[idx:], " ")
			return strings.Join(lines, "\n")
		}
		// First meaningful line has no pipe: append the clause after it.
		lines[i] = strings.TrimRight(line, " ") + "\n" + clause
		return strings.Join(lines, "\n")
	}
	return strings.TrimRight(query, "\n") + "\n" + clause
}

// escapeKqlValue makes a value safe inside a single-quoted KQL string literal.
func escapeKqlValue(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// azureMetricsTimeRangeMs normalizes to epoch ms, defaulting to the last hour.
func azureMetricsTimeRangeMs(startTime, endTime int64) (int64, int64) {
	toMs := func(t int64) int64 {
		if t > 0 && t < 1e12 { // seconds → ms
			return t * 1000
		}
		return t
	}
	start, end := toMs(startTime), toMs(endTime)
	if end <= 0 {
		end = time.Now().UnixMilli()
	}
	if start <= 0 {
		start = end - int64(time.Hour/time.Millisecond)
	}
	return start, end
}

// azureMetricsStepMinutes picks a bin width in minutes, honoring a requested
// step but keeping the bucket count bounded.
func azureMetricsStepMinutes(requestedSeconds int, startMs, endMs int64) int {
	const maxBuckets = 1000
	rangeMin := (endMs - startMs) / 60000
	if rangeMin <= 0 {
		return 1
	}
	minStep := int(rangeMin/maxBuckets) + 1
	step := requestedSeconds / 60
	if step < minStep {
		step = minStep
	}
	if step < 1 {
		step = 1
	}
	return step
}

func azureMsToRFC3339(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// azureParseTimestampMs accepts the App Insights timestamp column, which is an
// ISO8601 string in JSON responses.
func azureParseTimestampMs(v any) (int64, bool) {
	switch t := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999Z"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed.UTC().UnixMilli(), true
			}
		}
	case float64:
		if t > 1e12 {
			return int64(t), true
		}
		return int64(t) * 1000, true
	}
	return 0, false
}

func azureToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}
