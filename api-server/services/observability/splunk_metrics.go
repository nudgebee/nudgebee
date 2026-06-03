package observability

import (
	"fmt"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"sort"
	"strings"
)

// SplunkMetricSource implements MetricSource for Splunk Observability Cloud.
// Uses the SignalFlow API (stream.<realm>.signalfx.com) for time-series metric queries
// and the Metric/Dimension catalog APIs for listing.
type SplunkMetricSource struct{}

const (
	splunkDimensionFmt = "dimension('%s', '%s')"
	splunkFilterPrefix = ".filter("
)

func (s *SplunkMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_like"}
}

func (s *SplunkMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, rawQuery := range req.Queries {
		return s.buildSignalFlowProgram(rawQuery, req.Labels, req.LabelMatchers)
	}
	return "", nil
}

// FetchMetricsQuery executes metric queries via SignalFlow.
func (s *SplunkMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SplunkMetricSource.FetchMetricsQuery: failed to get configs", "error", err)
		return OutputMetricQuery{}, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	startMs, endMs := normalizeTimeRangeMs(req.StartTime, req.EndTime)
	results := OutputMetricQuery{Results: []QueryResult{}}

	for queryKey, rawQuery := range req.Queries {
		program, buildErr := s.buildSignalFlowProgram(rawQuery, req.Labels, req.LabelMatchers)
		if buildErr != nil {
			ctx.GetLogger().Warn("SplunkMetricSource.FetchMetricsQuery: query build failed",
				"key", queryKey, "error", buildErr)
			errMsg := buildErr.Error()
			results.Results = append(results.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errMsg,
			})
			continue
		}
		resolutionMs := int64(req.StepInterval) * 1000
		if req.Instant {
			resolutionMs = 0
		}

		ctx.GetLogger().Info("Splunk O11y SignalFlow Query", "key", queryKey, "program", program)

		points, queryErr := integrations.ExecuteSignalFlow(cfg, program, startMs, endMs, resolutionMs)
		if queryErr != nil {
			ctx.GetLogger().Error("SplunkMetricSource.FetchMetricsQuery: SignalFlow failed",
				"key", queryKey, "program", program, "error", queryErr)
			errMsg := queryErr.Error()
			results.Results = append(results.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errMsg,
			})
			continue
		}

		qr := s.convertSignalFlowToQueryResult(points, queryKey)
		results.Results = append(results.Results, qr)
	}

	return results, nil
}

// FetchMetricList returns available metric names from the Splunk O11y catalog.
func (s *SplunkMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("SplunkMetricSource.FetchMetricList: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	names, err := integrations.FetchO11yMetricList(cfg, "*", 200)
	if err != nil {
		return nil, fmt.Errorf("failed to list Splunk O11y metrics: %w", err)
	}

	metrics := make([]OutputMetrics, 0, len(names))
	for _, name := range names {
		metrics = append(metrics, OutputMetrics{
			Metric:     name,
			Attributes: map[string]any{},
		})
	}
	return metrics, nil
}

// FetchMetricLabelValues returns distinct values for a dimension in Splunk O11y.
func (s *SplunkMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	if req.Label == "" {
		return nil, fmt.Errorf("label name is required")
	}

	// Query dimension values for the specific label key
	query := fmt.Sprintf("key:%s", integrations.EscapeO11yQueryString(req.Label))
	dims, err := integrations.FetchO11yDimensions(cfg, query, 100)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Splunk O11y dimension values: %w", err)
	}

	var values []OutputMetricsLabelValues
	for _, d := range dims {
		if val, ok := d["value"].(string); ok && val != "" {
			values = append(values, OutputMetricsLabelValues{
				Value:      val,
				Attributes: map[string]any{},
			})
		}
	}
	return values, nil
}

// FetchMetricsLabels returns dimension (label) names available in Splunk O11y.
func (s *SplunkMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	cfg, err := integrations.GetSplunkO11yConfigs(ctx, req.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Splunk O11y configs: %w", err)
	}

	dims, err := integrations.FetchO11yDimensions(cfg, "*", 100)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Splunk O11y dimension labels: %w", err)
	}

	seen := make(map[string]bool)
	var labels []OutputMetricLabels
	for _, d := range dims {
		if key, ok := d["key"].(string); ok && key != "" && !seen[key] {
			seen[key] = true
			labels = append(labels, OutputMetricLabels{
				Label:      key,
				Attributes: map[string]any{},
			})
		}
	}
	return labels, nil
}

// buildSplunkFilter renders eq-only label key-value pairs as a comma-joined
// list of SignalFlow filter arguments. The caller is responsible for wrapping
// the result in `.filter(...)` — separating the wrap lets us merge label
// filters with matcher filters into a single .filter() call.
func buildSplunkFilter(labels map[string]string) string {
	keys := sortedKeys(labels)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		ek := strings.ReplaceAll(k, "'", "\\'")
		v := strings.ReplaceAll(labels[k], "'", "\\'")
		parts = append(parts, fmt.Sprintf(splunkDimensionFmt, ek, v))
	}
	return strings.Join(parts, ", ")
}

// splunkMatcherClause renders one LabelMatcher into a SignalFlow filter
// expression fragment. _ilike, _in, _not_in are advertised by
// GetSupportedOperators but rejected here until the corresponding SignalFlow
// syntax (and a list-value editor for _in/_not_in) lands — same convention as
// promqlMatcherOp.
func splunkMatcherClause(m LabelMatcher) (string, error) {
	field := strings.ReplaceAll(m.Label, "'", "\\'")
	value := strings.ReplaceAll(m.Value, "'", "\\'")
	switch m.Operator {
	case "_eq":
		return fmt.Sprintf(splunkDimensionFmt, field, value), nil
	case "_neq":
		return "not " + fmt.Sprintf(splunkDimensionFmt, field, value), nil
	case "_like":
		// SignalFlow filter() values accept glob wildcards directly.
		return fmt.Sprintf(splunkDimensionFmt, field, value), nil
	case "_in", "_not_in":
		return "", fmt.Errorf("operator %q not yet supported in Splunk O11y builder; pending list-value editor", m.Operator)
	default:
		return "", fmt.Errorf("unsupported operator %q for SignalFlow", m.Operator)
	}
}

// buildSplunkFilterFromMatchers renders LabelMatchers into a SignalFlow
// filter() argument list.
func buildSplunkFilterFromMatchers(matchers []LabelMatcher) (string, error) {
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
		clause, err := splunkMatcherClause(m)
		if err != nil {
			return "", err
		}
		parts = append(parts, clause)
	}
	return strings.Join(parts, ", "), nil
}

// buildSignalFlowProgram constructs a SignalFlow program from a metric name or raw program string.
func (s *SplunkMetricSource) buildSignalFlowProgram(rawQuery string, labels map[string]string, matchers []LabelMatcher) (string, error) {
	// If the raw query is already a complete SignalFlow program, pass it through.
	trimmed := strings.TrimSpace(rawQuery)
	if strings.Contains(trimmed, ".publish()") || strings.HasPrefix(trimmed, "data(") {
		return rawQuery, nil
	}

	// Build a simple mean aggregation over the metric.
	safeMetric := strings.ReplaceAll(rawQuery, "'", "\\'")
	program := fmt.Sprintf("data('%s')", safeMetric)

	matcherClause, err := buildSplunkFilterFromMatchers(matchers)
	if err != nil {
		return "", err
	}
	labelClause := buildSplunkFilter(labels)
	switch {
	case labelClause != "" && matcherClause != "":
		program += splunkFilterPrefix + labelClause + ", " + matcherClause + ")"
	case labelClause != "":
		program += splunkFilterPrefix + labelClause + ")"
	case matcherClause != "":
		program += splunkFilterPrefix + matcherClause + ")"
	}
	return program + ".mean().publish()", nil
}

// convertSignalFlowToQueryResult converts SignalFlow data points to QueryResult format.
func (s *SplunkMetricSource) convertSignalFlowToQueryResult(points []integrations.SignalFlowDataPoint, queryKey string) QueryResult {
	qr := QueryResult{
		QueryKey: queryKey,
		Payload:  []Result{},
	}

	if len(points) == 0 {
		return qr
	}

	// Group points by their label set (metric + dimensions) into separate series.
	type seriesData struct {
		labels     map[string]string
		metricName string
		timestamps []int64
		values     []float64
	}

	seriesMap := make(map[string]*seriesData)

	for _, p := range points {
		// Build a stable key for this label combination — sort parts to avoid
		// non-deterministic map iteration order producing duplicate series.
		labelParts := make([]string, 0, len(p.Labels))
		for k, v := range p.Labels {
			labelParts = append(labelParts, k+"="+v)
		}
		sort.Strings(labelParts)
		key := p.MetricName + "|" + strings.Join(labelParts, "|")

		if _, exists := seriesMap[key]; !exists {
			seriesMap[key] = &seriesData{
				labels:     p.Labels,
				metricName: p.MetricName,
			}
		}
		seriesMap[key].timestamps = append(seriesMap[key].timestamps, p.TimestampMs)
		seriesMap[key].values = append(seriesMap[key].values, p.Value)
	}

	for _, sd := range seriesMap {
		metric := make(map[string]string, len(sd.labels)+1)
		metric["__name__"] = sd.metricName
		for k, v := range sd.labels {
			metric[k] = v
		}
		qr.Payload = append(qr.Payload, Result{
			Metric:     metric,
			Timestamps: sd.timestamps,
			Values:     sd.values,
		})
	}

	return qr
}
