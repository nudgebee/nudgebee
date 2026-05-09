package observability

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

// errDtForbidden is returned when a Dynatrace API call returns HTTP 403.
// Callers use errors.Is to degrade gracefully instead of surfacing an error.
var errDtForbidden = errors.New("forbidden")

// DynatraceMetricSource implements MetricSource interface for Dynatrace Grail
type DynatraceMetricSource struct {
	// getAllConfigs overrides integrations.GetDynatraceConfigs in tests.
	// When nil, integrations.GetDynatraceConfigs is called.
	getAllConfigs func(*security.RequestContext, string) (string, string, error)
}

func (s *DynatraceMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_gt", "_lt", "_gte", "_lte", "_in", "_not_in", "_like", "_nlike", "_ilike", "_icontains", "_contains", "_regex", "_nregex"}
}

func (s *DynatraceMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	from, to := s.getTimeRange(req.StartTime, req.EndTime)
	resolution := s.buildResolution(req.StepInterval)
	for _, rawQuery := range req.Queries {
		return s.buildMetricDQL(rawQuery, from, to, resolution, req.Labels), nil
	}
	return "", nil
}

func (s *DynatraceMetricSource) dynatraceAllConfigs(ctx *security.RequestContext, accountId string) (string, string, error) {
	if s.getAllConfigs != nil {
		return s.getAllConfigs(ctx, accountId)
	}
	return integrations.GetDynatraceConfigs(ctx, accountId)
}

// grailTimeseriesAlias is the fixed DQL alias used for the timeseries value array.
// Keeping it constant lets convertToQueryResult always know which record field holds values.
const grailTimeseriesAlias = "val"

// FetchMetricsQuery queries time-series metrics from Dynatrace Grail using DQL timeseries.
// If rawQuery already starts with "timeseries" it is used as-is; otherwise it is wrapped
// as: timeseries val = avg(<rawQuery>), from:..., to:..., interval:...
func (s *DynatraceMetricSource) FetchMetricsQuery(ctx *security.RequestContext, req FetchMetricsRequest) (OutputMetricQuery, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("DynatraceMetricSource.FetchMetricsQuery: failed to get configs", "error", err)
		return OutputMetricQuery{}, fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	from, to := s.getTimeRange(req.StartTime, req.EndTime)
	resolution := s.buildResolution(req.StepInterval)

	results := OutputMetricQuery{Results: []QueryResult{}}
	for queryKey, rawQuery := range req.Queries {
		dql := s.buildMetricDQL(rawQuery, from, to, resolution, req.Labels)
		ctx.GetLogger().Info("DynatraceMetricSource.FetchMetricsQuery", "key", queryKey, "dql", dql)

		grailRes, fetchErr := executeDQLQuery(baseURL, apiToken, dql)
		if fetchErr != nil {
			ctx.GetLogger().Error("DynatraceMetricSource.FetchMetricsQuery: query failed",
				"key", queryKey, "error", fetchErr)
			errMsg := fetchErr.Error()
			results.Results = append(results.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errMsg,
			})
			continue
		}

		results.Results = append(results.Results, s.convertToQueryResult(grailRes, queryKey, rawQuery))
	}

	return results, nil
}

// DQL time-range placeholders used in builder-generated timeseries templates.
// buildMetricDQL substitutes these so the request's actual start/end/interval are respected.
const (
	dtFromPlaceholder     = "{DTFROM}"
	dtToPlaceholder       = "{DTTO}"
	dtIntervalPlaceholder = "{DTINTERVAL}"
)

// buildMetricDQL constructs the DQL timeseries statement for a metric query.
//
// Two input forms are supported:
//  1. A raw metric selector (not starting with "timeseries"): wrapped as
//     `timeseries val = avg(`selector`), from: "FROM", to: "TO", interval: RES`
//     The metric ID is backtick-quoted because bare colons (e.g. "builtin:host.cpu.usage")
//     are not valid identifiers in DQL expressions.
//  2. A DQL timeseries template (starts with "timeseries"): used verbatim after
//     substituting {DTFROM}, {DTTO}, {DTINTERVAL} with the actual time range.
//     Builder functions use this form to include filter: and by: clauses.
//
// buildDQLFilter constructs a DQL filter expression from label key-value pairs.
func buildDQLFilter(labels map[string]string) string {
	keys := sortedKeys(labels)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		ek := strings.ReplaceAll(k, "`", "\\`")
		v := strings.ReplaceAll(labels[k], `"`, `\"`)
		parts = append(parts, fmt.Sprintf("`%s`==\"%s\"", ek, v))
	}
	return strings.Join(parts, " AND ")
}

func (s *DynatraceMetricSource) buildMetricDQL(rawQuery, from, to, resolution string, labels map[string]string) string {
	if strings.HasPrefix(strings.TrimSpace(rawQuery), "timeseries") {
		q := strings.ReplaceAll(rawQuery, dtFromPlaceholder, from)
		q = strings.ReplaceAll(q, dtToPlaceholder, to)
		q = strings.ReplaceAll(q, dtIntervalPlaceholder, resolution)
		return q
	}
	// Backtick-quote the metric ID: colons in "builtin:..." are not valid bare identifiers in DQL.
	dql := fmt.Sprintf("timeseries %s = avg(`%s`), from: \"%s\", to: \"%s\", interval: %s",
		grailTimeseriesAlias, rawQuery, from, to, resolution)
	if len(labels) > 0 {
		dql += ", filter: " + buildDQLFilter(labels)
	}
	return dql
}

// convertToQueryResult normalizes a Grail timeseries result to NudgeBee's OutputMetricQuery format.
func (s *DynatraceMetricSource) convertToQueryResult(gr *grailResult, queryKey, query string) QueryResult {
	qr := QueryResult{
		QueryKey: queryKey,
		Query:    query,
		Payload:  []Result{},
	}
	// Metadata timestamps are shared (object form from mocks/docs); compute once.
	metadataTimestamps := s.extractMetadataTimestamps(gr)
	for _, record := range gr.Records {
		// Prefer per-record computed timestamps (real API); fall back to metadata.
		timestamps := s.computeTimestampsFromRecord(record)
		if len(timestamps) == 0 {
			timestamps = metadataTimestamps
		}
		qr.Payload = append(qr.Payload, Result{
			Metric:     s.extractDimensions(record, query),
			Timestamps: timestamps,
			Values:     s.extractValues(record),
		})
	}
	return qr
}

// extractMetadataTimestamps parses ISO 8601 timestamp strings from Grail metadata (object form).
// Returns nil when no metadata timestamps are present (real API case).
func (s *DynatraceMetricSource) extractMetadataTimestamps(gr *grailResult) []int64 {
	if gr.Metadata == nil {
		return nil
	}
	meta, ok := gr.Metadata.Metrics[grailTimeseriesAlias]
	if !ok || meta == nil || len(meta.Timestamps) == 0 {
		return nil
	}
	timestamps := make([]int64, 0, len(meta.Timestamps))
	for _, tsStr := range meta.Timestamps {
		if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			timestamps = append(timestamps, t.Unix())
		} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
			timestamps = append(timestamps, t.Unix())
		}
	}
	return timestamps
}

// extractTimestamps returns epoch-second timestamps for a timeseries result.
// Prefers per-record computation (real API), falls back to metadata timestamps (mocks/docs).
func (s *DynatraceMetricSource) extractTimestamps(gr *grailResult) []int64 {
	if gr == nil {
		return nil
	}
	if len(gr.Records) > 0 {
		if ts := s.computeTimestampsFromRecord(gr.Records[0]); len(ts) > 0 {
			return ts
		}
	}
	return s.extractMetadataTimestamps(gr)
}

// computeTimestampsFromRecord derives epoch-second timestamps from the timeframe and interval
// fields that the Dynatrace Grail API embeds directly in each timeseries record.
// interval is a nanosecond duration string (e.g. "300000000000" = 5 minutes).
func (s *DynatraceMetricSource) computeTimestampsFromRecord(record map[string]any) []int64 {
	rawVals, ok := record[grailTimeseriesAlias]
	if !ok {
		return nil
	}
	// Scalar float64: produced by | fields val = arrayLast(val).
	// Use timeframe.end as the single representative timestamp.
	if _, ok := rawVals.(float64); ok {
		if tf, ok := record["timeframe"].(map[string]any); ok {
			if endStr, ok := tf["end"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, endStr); err == nil {
					return []int64{t.Unix()}
				} else if t, err := time.Parse(time.RFC3339, endStr); err == nil {
					return []int64{t.Unix()}
				}
			}
		}
		return nil
	}
	arr, ok := rawVals.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	timeframeMap, ok := record["timeframe"].(map[string]any)
	if !ok {
		return nil
	}
	startStr, ok := timeframeMap["start"].(string)
	if !ok {
		return nil
	}
	var startTime time.Time
	if t, err := time.Parse(time.RFC3339Nano, startStr); err == nil {
		startTime = t
	} else if t, err := time.Parse(time.RFC3339, startStr); err == nil {
		startTime = t
	} else {
		return nil
	}

	intervalStr, ok := record["interval"].(string)
	if !ok {
		return nil
	}
	intervalNs, err := strconv.ParseInt(intervalStr, 10, 64)
	if err != nil || intervalNs <= 0 {
		return nil
	}
	step := time.Duration(intervalNs)

	timestamps := make([]int64, len(arr))
	for i := range arr {
		timestamps[i] = startTime.Add(time.Duration(i) * step).Unix()
	}
	return timestamps
}

// grailRecordSystemFields lists Grail record fields that are structural metadata,
// not dimension labels. These are excluded from the metric label map.
var grailRecordSystemFields = map[string]bool{
	grailTimeseriesAlias: true,
	"timeframe":          true,
	"interval":           true,
}

// extractDimensions builds the metric label map from string dimension fields in a record.
// Structural fields (val, timeframe, interval) are excluded.
func (s *DynatraceMetricSource) extractDimensions(record map[string]any, query string) map[string]string {
	metricMap := map[string]string{"__name__": query}
	for k, v := range record {
		if grailRecordSystemFields[k] {
			continue
		}
		if str, ok := v.(string); ok {
			metricMap[k] = str
		}
	}
	return metricMap
}

// extractValues extracts the float64 values array from a Grail timeseries record.
// Null entries remain as zero (the zero value of float64).
func (s *DynatraceMetricSource) extractValues(record map[string]any) []float64 {
	rawVals, ok := record[grailTimeseriesAlias]
	if !ok {
		return nil
	}
	if f, ok := rawVals.(float64); ok {
		return []float64{f}
	}
	arr, ok := rawVals.([]any)
	if !ok {
		return nil
	}
	values := make([]float64, len(arr))
	for i, v := range arr {
		if v == nil {
			continue
		}
		switch n := v.(type) {
		case float64:
			values[i] = n
		case int64:
			values[i] = float64(n)
		}
	}
	return values
}

// dtAutocompleteRequest is the request body for the DQL autocomplete endpoint.
type dtAutocompleteRequest struct {
	Query    string `json:"query"`
	Position int    `json:"position"`
}

// dtAutocompletePart is a single token fragment within a suggestion.
type dtAutocompletePart struct {
	Type       string `json:"type"`
	Suggestion string `json:"suggestion"`
}

// dtAutocompleteResponse is the top-level response from the DQL autocomplete endpoint.
type dtAutocompleteResponse struct {
	Suggestions []struct {
		Suggestion string               `json:"suggestion"`
		Parts      []dtAutocompletePart `json:"parts"`
	} `json:"suggestions"`
}

// fetchDynatraceMetricListViaAutocomplete enumerates available metric keys by querying
// the DQL autocomplete endpoint with the platform Bearer token — no classic API token needed.
// An empty filter returns all ~330 available metrics in a single call.
// A non-empty filter returns only metrics whose keys contain the filter string.
func fetchDynatraceMetricListViaAutocomplete(baseURL, apiToken, filter string) ([]OutputMetrics, error) {
	autocompleteURL := strings.TrimRight(baseURL, "/") + "/platform/storage/query/v1/query:autocomplete"
	headers := map[string]string{
		"Authorization": "Bearer " + apiToken,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}

	queryStr := "timeseries avg(" + filter
	body := dtAutocompleteRequest{
		Query:    queryStr,
		Position: len(queryStr),
	}

	res, err := common.HttpPost(autocompleteURL,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to call Dynatrace autocomplete endpoint: %w", err)
	}
	bodyBytes, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("failed to read Dynatrace autocomplete response: %w", readErr)
	}
	if res.StatusCode == http.StatusForbidden {
		return nil, errDtForbidden
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dynatrace autocomplete returned status %d: %s", res.StatusCode, string(bodyBytes))
	}

	var resp dtAutocompleteResponse
	if err := common.UnmarshalJson(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Dynatrace autocomplete response: %w", err)
	}

	output := make([]OutputMetrics, 0, len(resp.Suggestions))
	for _, s := range resp.Suggestions {
		// Skip empty suggestions and DQL syntax tokens (rollup:*, ")", ", ")
		if s.Suggestion == "" || strings.HasPrefix(s.Suggestion, "rollup:") ||
			s.Suggestion == ")" || strings.TrimSpace(s.Suggestion) == "," {
			continue
		}
		output = append(output, OutputMetrics{Metric: s.Suggestion, Attributes: map[string]any{}})
	}
	return output, nil
}

// FetchMetricList returns available Dynatrace metrics using the DQL autocomplete endpoint.
func (s *DynatraceMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	apiToken, baseURL, err := s.dynatraceAllConfigs(ctx, req.AccountId)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Error("DynatraceMetricSource.FetchMetricList: failed to get configs", "error", err)
		}
		return nil, fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	output, autocompleteErr := fetchDynatraceMetricListViaAutocomplete(baseURL, apiToken, req.Metric)
	if autocompleteErr == nil {
		return output, nil
	}
	if ctx != nil {
		ctx.GetLogger().Warn("DynatraceMetricSource.FetchMetricList: autocomplete failed", "error", autocompleteErr)
	}
	return []OutputMetrics{}, nil
}

// grailDimensionAutocompleteBlocklist contains DQL context-level identifiers returned
// by the by-clause autocomplete that are not useful metric dimension labels.
var grailDimensionAutocompleteBlocklist = map[string]bool{
	"metric.key":       true, // the metric ID itself, not a groupable dimension
	"dt.system.bucket": true, // Grail storage routing field, not a metric dimension
}

// fetchDTMetricDimensionsViaAutocomplete returns available dimension keys for a metric
// using the DQL autocomplete endpoint — works with platform tokens (no classic API needed).
// It sends "timeseries avg({metricID}), from: now()-1h, by: {" to the autocomplete endpoint
// and parses the SIMPLE_IDENTIFIER suggestions Dynatrace returns as valid dimension keys.
func fetchDTMetricDimensionsViaAutocomplete(baseURL, apiToken, metricID string) ([]OutputMetricLabels, error) {
	queryStr := fmt.Sprintf("timeseries avg(%s), from: now()-1h, by: {", metricID)
	body := dtAutocompleteRequest{
		Query:    queryStr,
		Position: len(queryStr),
	}
	autocompleteURL := strings.TrimRight(baseURL, "/") + "/platform/storage/query/v1/query:autocomplete"
	headers := map[string]string{
		"Authorization": "Bearer " + apiToken,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}
	res, err := common.HttpPost(autocompleteURL,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to call Dynatrace dimension autocomplete: %w", err)
	}
	bodyBytes, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("failed to read Dynatrace dimension autocomplete response: %w", readErr)
	}
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusUnauthorized {
		return nil, errDtForbidden
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dynatrace dimension autocomplete returned status %d: %s", res.StatusCode, string(bodyBytes))
	}
	var resp dtAutocompleteResponse
	if unmarshalErr := common.UnmarshalJson(bodyBytes, &resp); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to unmarshal Dynatrace dimension autocomplete: %w", unmarshalErr)
	}
	var output []OutputMetricLabels
	for _, s := range resp.Suggestions {
		if s.Suggestion == "" || grailDimensionAutocompleteBlocklist[s.Suggestion] {
			continue
		}
		isIdentifier := false
		for _, part := range s.Parts {
			if part.Type == "SIMPLE_IDENTIFIER" {
				isIdentifier = true
				break
			}
		}
		if !isIdentifier {
			continue
		}
		output = append(output, OutputMetricLabels{
			Label:      s.Suggestion,
			Attributes: map[string]any{},
		})
	}
	return output, nil
}

// extractBaseMetricID strips Dynatrace metric selector transformation suffixes so
// the bare metric ID can be passed to the Metrics v2 REST API.
// Dynatrace metric IDs always have exactly one colon (e.g. "builtin:host.cpu.usage");
// any second colon begins a transformation (e.g. ":filter(", ":splitBy(", ":fold(").
func extractBaseMetricID(metricSelector string) string {
	firstColon := strings.Index(metricSelector, ":")
	if firstColon == -1 {
		return metricSelector
	}
	secondColon := strings.Index(metricSelector[firstColon+1:], ":")
	if secondColon == -1 {
		return metricSelector
	}
	return metricSelector[:firstColon+1+secondColon]
}

// fetchDTMetricLabelValues executes a DQL timeseries query grouped by the requested
// dimension key and returns distinct non-empty string values from the result records.
// Using "by: {labelKey}" forces Dynatrace to expand one record per unique dimension value
// instead of collapsing everything into an aggregated row.
// Extracted as a standalone function so unit tests can inject a mock server URL directly.
func fetchDTMetricLabelValues(baseURL, apiToken, metricSelector, labelKey, from, to string) ([]OutputMetricsLabelValues, error) {
	baseSelector := extractBaseMetricID(metricSelector)
	dql := fmt.Sprintf("timeseries %s = avg(`%s`), from: \"%s\", to: \"%s\", interval: 1h, by: {%s}",
		grailTimeseriesAlias, baseSelector, from, to, labelKey)

	result, err := executeDQLQuery(baseURL, apiToken, dql)
	if err != nil {
		return nil, fmt.Errorf("dynatrace metric label values query failed: %w", err)
	}

	var output []OutputMetricsLabelValues
	if grailRecordSystemFields[labelKey] {
		return output, nil
	}
	seen := make(map[string]bool)
	for _, record := range result.Records {
		val, ok := record[labelKey].(string)
		if !ok || val == "" || seen[val] {
			continue
		}
		seen[val] = true
		output = append(output, OutputMetricsLabelValues{
			Value:      val,
			Attributes: map[string]any{},
		})
	}
	return output, nil
}

// FetchMetricsLabels returns the dimension definitions for a specific Dynatrace metric.
// It calls the Metrics v2 API via the Dynatrace platform bridge so that platform tokens work.
// Degrades gracefully (returns empty) if the token lacks access.
func (s *DynatraceMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	apiToken, baseURL, err := s.dynatraceAllConfigs(ctx, req.AccountId)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Error("DynatraceMetricSource.FetchMetricsLabels: failed to get configs", "error", err)
		}
		return nil, fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	metricID := extractBaseMetricID(req.MetricName)
	if metricID == "" {
		return []OutputMetricLabels{}, nil
	}

	output, err := fetchDTMetricDimensionsViaAutocomplete(baseURL, apiToken, metricID)
	if err != nil {
		if errors.Is(err, errDtForbidden) {
			if ctx != nil {
				ctx.GetLogger().Warn("DynatraceMetricSource.FetchMetricsLabels: token lacks dimension access, returning empty",
					"metricId", metricID)
			}
			return []OutputMetricLabels{}, nil
		}
		if ctx != nil {
			ctx.GetLogger().Error("DynatraceMetricSource.FetchMetricsLabels: failed to fetch dimensions",
				"metricId", metricID, "error", err)
		}
		return nil, err
	}
	return output, nil
}

// FetchMetricLabelValues returns distinct values for a specific metric dimension key.
// It runs a DQL timeseries query for recent data and extracts unique string values
// from the records. Requires 'metric_name' in req.Request.
func (s *DynatraceMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	apiToken, baseURL, err := integrations.GetDynatraceConfigs(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Error("DynatraceMetricSource.FetchMetricLabelValues: failed to get configs", "error", err)
		return nil, fmt.Errorf("failed to get Dynatrace configs: %w", err)
	}

	metricName, _ := req.Request["metric_name"].(string)
	if metricName == "" {
		ctx.GetLogger().Warn("DynatraceMetricSource.FetchMetricLabelValues: metric_name missing from request")
		return []OutputMetricsLabelValues{}, nil
	}

	from, to := s.getTimeRange(req.StartTime, req.EndTime)
	ctx.GetLogger().Info("DynatraceMetricSource.FetchMetricLabelValues",
		"metric", metricName, "label", req.Label, "from", from, "to", to)
	return fetchDTMetricLabelValues(baseURL, apiToken, metricName, req.Label, from, to)
}

// getTimeRange converts epoch ms timestamps to ISO 8601 strings for the Dynatrace Grail API.
func (s *DynatraceMetricSource) getTimeRange(startTime, endTime int64) (string, string) {
	if startTime == 0 {
		startTime = time.Now().Add(-1 * time.Hour).UnixMilli()
	}
	if endTime == 0 {
		endTime = time.Now().UnixMilli()
	}
	if startTime < 1e12 {
		startTime *= 1000
	}
	if endTime < 1e12 {
		endTime *= 1000
	}
	return time.UnixMilli(startTime).UTC().Format(time.RFC3339),
		time.UnixMilli(endTime).UTC().Format(time.RFC3339)
}

// buildResolution converts a step interval in seconds to a Dynatrace DQL interval string.
func (s *DynatraceMetricSource) buildResolution(stepInterval int) string {
	if stepInterval <= 0 {
		return "1m"
	}
	minutes := stepInterval / 60
	if minutes < 1 {
		return "1m"
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh", minutes/60)
}
