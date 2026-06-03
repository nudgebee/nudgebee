package observability

import (
	"fmt"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sort"
	"strings"
	"time"
)

// NewRelicMetricSource implements MetricSource interface for New Relic
type NewRelicMetricSource struct{}

func (s *NewRelicMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like", "_ilike", "_contains"}
}

func (s *NewRelicMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)
	stepInterval := s.calculateStepInterval(req.StepInterval, startTime, endTime, req.MetricProvider)
	for _, rawQuery := range req.Queries {
		if err := validateAPMScope(rawQuery, req.Labels); err != nil {
			return "", err
		}
		return s.buildNRQLMetricQuery(rawQuery, startTime, endTime, stepInterval, req.Instant, req.Labels, req.LabelMatchers)
	}
	return "", nil
}

// FetchMetricsQuery executes metric queries against New Relic
func (s *NewRelicMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicMetricSource.FetchMetricsQuery: failed to get configs", "error", err)
		return OutputMetricQuery{}, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	results := OutputMetricQuery{Results: []QueryResult{}}

	// Get time range in seconds
	startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)

	// Calculate step interval to ensure we don't exceed 366 buckets (New Relic TIMESERIES limit)
	stepInterval := s.calculateStepInterval(req.StepInterval, startTime, endTime, req.MetricProvider)

	// Execute each query
	for queryKey, rawQuery := range req.Queries {
		if err := validateAPMScope(rawQuery, req.Labels); err != nil {
			ctx.GetLogger().Warn("NewRelicMetricSource.FetchMetricsQuery: APM scope validation failed",
				"key", queryKey, "query", rawQuery, "error", err)
			errorMsg := err.Error()
			results.Results = append(results.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errorMsg,
			})
			continue
		}

		nrqlQuery, buildErr := s.buildNRQLMetricQuery(rawQuery, startTime, endTime, stepInterval, req.Instant, req.Labels, req.LabelMatchers)
		if buildErr != nil {
			ctx.GetLogger().Warn("NewRelicMetricSource.FetchMetricsQuery: query build failed",
				"key", queryKey, "error", buildErr)
			errorMsg := buildErr.Error()
			results.Results = append(results.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errorMsg,
			})
			continue
		}

		ctx.GetLogger().Info("NewRelic Metric Query", "key", queryKey, "query", nrqlQuery)

		nrResults, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
		if err != nil {
			ctx.GetLogger().Error("NewRelicMetricSource.FetchMetricsQuery: query failed",
				"key", queryKey, "query", nrqlQuery, "error", err)
			errorMsg := err.Error()
			results.Results = append(results.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errorMsg,
			})
			continue
		}

		// Convert to QueryResult format
		qr := s.convertNRMetricsToQueryResult(nrResults, queryKey, nrqlQuery)
		results.Results = append(results.Results, qr)
	}

	return results, nil
}

// FetchMetricList returns available metrics from New Relic
func (s *NewRelicMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicMetricSource.FetchMetricList: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Query unique metric names
	nrqlQuery := "SELECT uniques(metricName, 1000) FROM Metric SINCE 1 day ago"
	if req.Metric != "" {
		nrqlQuery = fmt.Sprintf("SELECT uniques(metricName, 1000) FROM Metric WHERE metricName LIKE '%%%s%%' SINCE 1 day ago", escapeNRQLValue(req.Metric))
	}

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metric list: %w", err)
	}

	var metrics []OutputMetrics
	for _, r := range results {
		if uniques, ok := r["uniques.metricName"].([]any); ok {
			for _, metric := range uniques {
				if metricName, ok := metric.(string); ok {
					metrics = append(metrics, OutputMetrics{
						Metric:     metricName,
						Attributes: map[string]any{},
					})
				}
			}
		}
	}

	return metrics, nil
}

// FetchMetricLabelValues returns values for a specific metric label
func (s *NewRelicMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicMetricSource.FetchMetricLabelValues: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Get metric name from request (optional)
	metricName, _ := req.Request["metric_name"].(string)

	// Escape the label name for NRQL
	escapedLabel := escapeNRQLField(req.Label)

	// Build the query dynamically based on provided filters
	nrqlQuery := fmt.Sprintf("SELECT uniques(%s, 100) FROM Metric", escapedLabel)

	// Add metric name filter if provided
	if metricName != "" {
		nrqlQuery += fmt.Sprintf(" WHERE metricName = '%s'", escapeNRQLValue(metricName))
	}

	// Add time range filter; default to 7 days if not provided (NR returns empty without SINCE)
	if req.StartTime > 0 && req.EndTime > 0 {
		startTime, endTime := s.getTimeRangeSeconds(req.StartTime, req.EndTime)
		nrqlQuery += fmt.Sprintf(" SINCE %d UNTIL %d", startTime, endTime)
	} else {
		nrqlQuery += " SINCE 7 days ago"
	}

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch label values: %w", err)
	}

	var values []OutputMetricsLabelValues
	for _, r := range results {
		// The key in the result depends on the label name
		uniquesKey := fmt.Sprintf("uniques.%s", req.Label)
		if uniques, ok := r[uniquesKey].([]any); ok {
			for _, val := range uniques {
				if valStr, ok := val.(string); ok {
					values = append(values, OutputMetricsLabelValues{
						Value:      valStr,
						Attributes: map[string]any{},
					})
				}
			}
		}
	}

	return values, nil
}

// FetchMetricsLabels returns available labels for a metric
func (s *NewRelicMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	apiKey, nrAccountId, region, err := integrations.GetNewRelicConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("NewRelicMetricSource.FetchMetricsLabels: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get New Relic configs: %w", err)
	}

	// Use keyset() to get available attributes for the metric
	nrqlQuery := fmt.Sprintf("SELECT keyset() FROM Metric WHERE metricName = '%s' SINCE 1 day ago LIMIT 1",
		escapeNRQLValue(req.MetricName))

	results, err := integrations.ExecuteNRQL(apiKey, nrAccountId, region, nrqlQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metric labels: %w", err)
	}

	var labels []OutputMetricLabels
	// New Relic keyset() returns an array of objects with "type" and "key" fields
	// e.g., [{"type": "string", "key": "service.name"}, {"type": "numeric", "key": "duration"}]
	for _, r := range results {
		if keyStr, ok := r["key"].(string); ok {
			// Skip internal fields
			if isInternalMetricField(keyStr) {
				continue
			}
			labels = append(labels, OutputMetricLabels{
				Label:      keyStr,
				Attributes: map[string]any{},
			})
		}
	}

	return labels, nil
}

// buildNRQLWhereFromLabels builds a WHERE clause fragment from label filters.
func buildNRQLWhereFromLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := sortedKeys(labels)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s='%s'", escapeNRQLField(k), escapeNRQLValue(labels[k])))
	}
	return strings.Join(parts, " AND ")
}

// buildNRQLWhereFromMatchers renders BUILDER LabelMatchers (with operator) into
// an NRQL WHERE clause fragment. Output is deterministic — matchers are sorted
// by (label, operator, value). _in / _not_in are advertised by
// GetSupportedOperators but rejected here until the UI grows a list-value
// editor and a value-shape contract — same convention as promqlMatcherOp.
func buildNRQLWhereFromMatchers(matchers []LabelMatcher) (string, error) {
	if len(matchers) == 0 {
		return "", nil
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
	parts := make([]string, 0, len(sorted))
	for _, m := range sorted {
		clause, err := nrqlMatcherClause(m)
		if err != nil {
			return "", err
		}
		parts = append(parts, clause)
	}
	return strings.Join(parts, " AND "), nil
}

// nrqlMatcherClause renders one LabelMatcher into an NRQL predicate.
func nrqlMatcherClause(m LabelMatcher) (string, error) {
	field := escapeNRQLField(m.Label)
	value := escapeNRQLValue(m.Value)
	switch m.Operator {
	case "_eq":
		return fmt.Sprintf("%s = '%s'", field, value), nil
	case "_neq":
		return fmt.Sprintf("%s != '%s'", field, value), nil
	case "_like", "_ilike":
		// _ilike collapses to LIKE here for parity with newrelic_logs.go's
		// buildNRQLOperatorClause, which also treats them identically.
		// Note: NRQL's LIKE is case-sensitive — true case-insensitive matching
		// would need LOWER(field) LIKE LOWER(...) and is not yet wired here
		// or in the logs path.
		return fmt.Sprintf("%s LIKE '%s'", field, value), nil
	case "_contains":
		return fmt.Sprintf("%s LIKE '%%%s%%'", field, value), nil
	case "_in", "_not_in":
		return "", fmt.Errorf("operator %q not yet supported in NRQL builder; pending list-value editor", m.Operator)
	default:
		return "", fmt.Errorf("unsupported operator %q for NRQL", m.Operator)
	}
}

// buildNRQLMetricQuery constructs an NRQL query for metrics. labels is the
// legacy eq-only map (used by internal callers); matchers carries the
// operator-aware BUILDER chips. Both clauses are ANDed together.
func (s *NewRelicMetricSource) buildNRQLMetricQuery(rawQuery string, startTime, endTime int64, stepInterval int, instant bool, labels map[string]string, matchers []LabelMatcher) (string, error) {
	whereClause := buildNRQLWhereFromLabels(labels)
	matcherClause, err := buildNRQLWhereFromMatchers(matchers)
	if err != nil {
		return "", err
	}
	switch {
	case whereClause != "" && matcherClause != "":
		whereClause = whereClause + " AND " + matcherClause
	case matcherClause != "":
		whereClause = matcherClause
	}

	// If the query already looks like NRQL, use it as-is with time range
	if isNRQLQuery(rawQuery) {
		if whereClause != "" {
			if strings.Contains(strings.ToUpper(rawQuery), " WHERE ") {
				rawQuery += " AND " + whereClause
			} else {
				rawQuery = injectNRQLWhere(rawQuery, whereClause)
			}
		}
		// Append time range if not already present
		if !strings.Contains(strings.ToUpper(rawQuery), "SINCE") {
			rawQuery = fmt.Sprintf("%s SINCE %d UNTIL %d", rawQuery, startTime, endTime)
		}
		// Add TIMESERIES only for non-instant queries
		if !instant && !strings.Contains(strings.ToUpper(rawQuery), "TIMESERIES") && stepInterval > 0 {
			rawQuery = fmt.Sprintf("%s TIMESERIES %d seconds", rawQuery, stepInterval)
		}
		return rawQuery, nil
	}

	// Otherwise, treat it as a metric name and build a simple query
	escapedMetric := escapeNRQLField(rawQuery)
	where := "FROM Metric"
	if whereClause != "" {
		where = fmt.Sprintf("FROM Metric WHERE %s", whereClause)
	}
	if instant {
		return fmt.Sprintf("SELECT average(%s) %s SINCE %d UNTIL %d",
			escapedMetric, where, startTime, endTime), nil
	}
	return fmt.Sprintf("SELECT average(%s) %s SINCE %d UNTIL %d TIMESERIES %d seconds",
		escapedMetric, where, startTime, endTime, stepInterval), nil
}

// injectNRQLWhere inserts a WHERE clause before SINCE/UNTIL/TIMESERIES/FACET/LIMIT keywords.
func injectNRQLWhere(query, whereClause string) string {
	upper := strings.ToUpper(query)
	for _, kw := range []string{" SINCE ", " UNTIL ", " TIMESERIES ", " FACET ", " LIMIT "} {
		if idx := strings.Index(upper, kw); idx != -1 {
			return query[:idx] + " WHERE " + whereClause + query[idx:]
		}
	}
	return query + " WHERE " + whereClause
}

// seriesAcc accumulates data points for a single time series.
type seriesAcc struct {
	labels     map[string]string
	timestamps []int64
	values     []float64
}

// convertNRMetricsToQueryResult converts flat NRQL metric rows into a QueryResult
// where each unique label set (FACET combination) becomes one payload entry with all
// its timestamps and values — matching the Datadog/Prometheus unified format.
func (s *NewRelicMetricSource) convertNRMetricsToQueryResult(results []map[string]any, queryKey string, nrqlQuery string) QueryResult {
	seriesMap := map[string]*seriesAcc{}
	seriesOrder := []string{} // preserves first-seen insertion order

	for _, r := range results {
		// Separate string labels (FACET dims) from numeric fields and timestamp.
		labels := map[string]string{}
		numericFields := map[string]float64{}
		var ts int64

		for k, v := range r {
			if isMetadataField(k) {
				continue
			}
			switch typed := v.(type) {
			case float64:
				numericFields[k] = typed
			case string:
				labels[k] = typed
			}
		}

		// Pick the metric value deterministically by sorting numeric field names and
		// selecting the first. This avoids flaky behavior when a row has multiple
		// aggregation results (e.g. average(cpu) and max(cpu)).
		metricName := ""
		var value *float64
		if len(numericFields) > 0 {
			metricKeys := make([]string, 0, len(numericFields))
			for k := range numericFields {
				metricKeys = append(metricKeys, k)
			}
			sort.Strings(metricKeys)
			metricName = metricKeys[0]
			f := numericFields[metricName]
			value = &f
		}

		// Extract timestamp from NRQL metadata fields.
		if beginTime, ok := r["beginTimeSeconds"].(float64); ok {
			ts = int64(beginTime)
		} else if t, ok := r["timestamp"].(float64); ok {
			if t > 1e12 {
				ts = int64(t / 1000)
			} else {
				ts = int64(t)
			}
		} else if value != nil {
			// Instant / aggregate query with no time bucket — use current time.
			ts = time.Now().Unix()
		}

		if value == nil || ts == 0 {
			continue
		}

		// Normalize NewRelic K8sNodeSample labels to Prometheus-compatible names expected by the UI.
		// The frontend checks r.metric.node for node name matching; NRQL returns nodeName.
		if nodeName, ok := labels["nodeName"]; ok {
			if _, hasNode := labels["node"]; !hasNode {
				labels["node"] = nodeName
			}
		}

		labels["__name__"] = metricName
		key := buildSeriesKey(labels)

		if _, exists := seriesMap[key]; !exists {
			seriesMap[key] = &seriesAcc{labels: labels}
			seriesOrder = append(seriesOrder, key)
		}
		seriesMap[key].timestamps = append(seriesMap[key].timestamps, ts)
		seriesMap[key].values = append(seriesMap[key].values, *value)
	}

	qr := QueryResult{
		QueryKey: queryKey,
		Query:    nrqlQuery,
		Payload:  []Result{},
	}
	for _, key := range seriesOrder {
		acc := seriesMap[key]
		qr.Payload = append(qr.Payload, Result{
			Metric:     acc.labels,
			Timestamps: acc.timestamps,
			Values:     acc.values,
		})
	}
	return qr
}

// buildSeriesKey produces a stable string key from a label map by sorting keys
// alphabetically and joining as "%q=%q,...". Keys and values are quoted to prevent
// collision when label values contain '=' or ',' characters.
func buildSeriesKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%q=%q", k, labels[k]))
	}
	return strings.Join(parts, ",")
}

// getTimeRangeSeconds converts timestamps to seconds
func (s *NewRelicMetricSource) getTimeRangeSeconds(startTime, endTime int64) (int64, int64) {
	// Convert from milliseconds to seconds if needed
	if startTime > 1e12 {
		startTime = startTime / 1000
	}
	if endTime > 1e12 {
		endTime = endTime / 1000
	}

	// Default to last hour if not specified
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).Unix()
	}
	if endTime == 0 {
		endTime = time.Now().Unix()
	}

	return startTime, endTime
}

// calculateStepInterval calculates the optimal step interval to ensure we don't exceed 366 buckets
// New Relic TIMESERIES supports a maximum of 366 buckets
func (s *NewRelicMetricSource) calculateStepInterval(requestedInterval int, startTime, endTime int64, integrationType string) int {
	const minStepInterval = 60 // Minimum 1 minute granularity
	const maxBuckets = 366     // New Relic TIMESERIES limit

	// Calculate time range in seconds
	timeRangeSeconds := endTime - startTime
	if timeRangeSeconds <= 0 {
		return minStepInterval
	}

	// Calculate minimum step interval needed to stay under 366 buckets
	// Add 1 to ensure we round up and stay safely under the limit
	minRequiredInterval := int((timeRangeSeconds / int64(maxBuckets)) + 1)

	// If user provided a step interval, use it if it's large enough
	if requestedInterval > 0 {
		if requestedInterval >= minRequiredInterval {
			// Always enforce minimum step interval of 60 seconds
			return max(requestedInterval, minStepInterval)
		}
		// User's interval is too small, use the minimum required (but at least 60s)
		return max(minRequiredInterval, minStepInterval)
	}

	// No user-provided interval, calculate optimal default based on time range
	var defaultInterval int
	switch {
	case timeRangeSeconds <= 3600: // <= 1 hour
		defaultInterval = 60 // 1 minute intervals
	case timeRangeSeconds <= 86400: // <= 1 day
		defaultInterval = 300 // 5 minute intervals
	case timeRangeSeconds <= 604800: // <= 1 week
		defaultInterval = 1800 // 30 minute intervals
	default: // > 1 week
		defaultInterval = 3600 // 1 hour intervals
	}

	// Use the larger of default interval or minimum required interval
	return max(defaultInterval, minRequiredInterval)
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// validateAPMScope returns an error when a bare apm.* metric is queried without an
// entity-scoping label. New Relic's NRQL rejects unscoped apm.* metrics with:
// "No application was matched (did you specify appId, appName or entity.guid?)".
// We catch this early so the UI surfaces a clear, actionable message instead of
// the raw New Relic response. Full user-authored NRQL queries are trusted as-is —
// if their WHERE clause is wrong, NR's error is already meaningful.
func validateAPMScope(rawQuery string, labels map[string]string) error {
	if isNRQLQuery(rawQuery) {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawQuery)), "apm.") {
		return nil
	}
	for k := range labels {
		switch strings.ToLower(k) {
		case "appid", "appname", "entity.guid", "entityname", "service.name":
			return nil
		}
	}
	return fmt.Errorf("metric %q requires an entity scope: provide appName, entity.guid, entityName, appId, or service.name as a label", rawQuery)
}

// isNRQLQuery checks if a string looks like a complete NRQL query
func isNRQLQuery(query string) bool {
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	return strings.HasPrefix(upperQuery, "SELECT") ||
		strings.HasPrefix(upperQuery, "FROM")
}

// isMetadataField checks if a field is NRQL metadata rather than data
func isMetadataField(field string) bool {
	metadataFields := map[string]bool{
		"beginTimeSeconds": true,
		"endTimeSeconds":   true,
		"inspectedCount":   true,
		"timestamp":        true,
		"eventType":        true,
	}
	return metadataFields[field]
}

// isInternalMetricField checks if a field is an internal New Relic field
func isInternalMetricField(field string) bool {
	internalFields := map[string]bool{
		"metricName":               true,
		"timestamp":                true,
		"newrelic.source":          true,
		"instrumentation.provider": true,
		"instrumentation.name":     true,
		"instrumentation.version":  true,
	}
	return internalFields[field]
}
