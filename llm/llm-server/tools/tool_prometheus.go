package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools/core"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/tmc/langchaingo/llms"
)

// metricsListCache caches metrics_list results by (accountId, keyword) for a short TTL.
// Repeated calls with the same keyword within a single agent session (or across
// child promql_query agents in the same prometheus run) hit the cache instead of
// the backend, saving ~8s per call.
type metricsListCacheEntry struct {
	response core.NBToolResponse
	expiry   time.Time
}

var metricsListCache sync.Map

// metricsListCacheTTL returns the configured TTL for the metrics list cache.
// Configurable via llm_server_agent_promql_metrics_cache_ttl_minutes (default 5m).
func metricsListCacheTTL() time.Duration {
	if v := config.Config.LlmServerAgentPromqlCacheTTLMinutes; v > 0 {
		return time.Duration(v) * time.Minute
	}
	return 5 * time.Minute
}

const ToolMetricsList = "metrics_list"
const ToolMetricsLabelsList = "metrics_labels_list"
const ToolQueryPrometheus = "prometheus_execute"
const ToolSearchMetrics = "search_metrics"

func init() {
	core.RegisterNBToolFactory(ToolQueryPrometheus, func(accountId string) (core.NBTool, error) {
		return PrometheusExecuteTool{}, nil
	})
	core.RegisterNBToolFactory(ToolMetricsList, func(accountId string) (core.NBTool, error) {
		return MetricsListTool{}, nil
	})
	core.RegisterNBToolFactory(ToolMetricsLabelsList, func(accountId string) (core.NBTool, error) {
		return ListMetricsLabelsTool{}, nil
	})
	core.RegisterNBToolFactory(ToolSearchMetrics, func(accountId string) (core.NBTool, error) {
		return SearchMetricsTool{}, nil
	})
}

type PrometheusExecuteTool struct {
}

func (m PrometheusExecuteTool) Name() string {
	return ToolQueryPrometheus
}

func (m PrometheusExecuteTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m PrometheusExecuteTool) Description() string {
	return `Executes a PromQL query against a Prometheus instance and retrieves the corresponding metric data.
		Usage:
		* Input: Provide a well-formatted PromQL query as input.
		* Output: The tool will return the time series data retrieved from Prometheus based on your query.

		Purpose: This tool enables you to access and analyze Kubernetes monitoring data, such as CPU usage, memory consumption, network traffic, and other key metrics, allowing you to provide informed assistance and insights to users.

		Important Notes:
		* Ensure the PromQL query is syntactically correct and adheres to the PromQL specification.
		* The output may contain a large volume of data points. You should process and summarize the data appropriately before presenting it to the user.
	`
}

func (m PrometheusExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "Prometheus Query to Execute",
			},
			"start_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "Start Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"end_time": {
				Type:        core.ToolSchemaTypeString,
				Description: "End Time for the query. Format: RFC3339 or Unix timestamp",
			},
			"range": {
				Type:        core.ToolSchemaTypeString,
				Description: "Time range for the query (e.g., '2d', '1w', '1h'). If provided, start_time is calculated relative to end_time.",
			},
		},
		Required: []string{"command"},
	}
}

// validatePromQLSyntax parses the query locally using the Prometheus parser.
// Returns a structured, LLM-actionable error string on failure, or empty string if valid.
// Common error types and suggested fixes are included so the LLM can self-correct
// without escalating to the promql_query sub-agent.
func validatePromQLSyntax(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return ""
	}
	if _, err := parser.ParseExpr(q); err != nil {
		msg := err.Error()
		// Classify the error and add a fix hint for the LLM
		hint := ""
		switch {
		case strings.Contains(msg, "unexpected ','") || strings.Contains(msg, "unexpected end"):
			hint = "Hint: Multiple queries must use ';' as separator, not ','. Example: query1; query2"
		case strings.Contains(msg, "unexpected identifier") || strings.Contains(msg, "unexpected character"):
			hint = "Hint: Check metric name spelling and label syntax. Labels must use '=', '!=', '=~', '!~' operators inside '{}'."
		case strings.Contains(msg, "parse error") && strings.Contains(msg, "["):
			hint = "Hint: Duration format is invalid. Use 's', 'm', 'h', 'd' suffixes (e.g., [5m], [1h])."
		case strings.Contains(msg, "unknown function"):
			hint = "Hint: Unknown PromQL function. Common functions: rate(), irate(), increase(), sum(), avg(), max(), min(), histogram_quantile()."
		default:
			hint = "Hint: Validate the query structure. Ensure metric names exist and label selectors are correctly formatted."
		}
		return fmt.Sprintf(`{"error_type":"syntax_error","message":%q,"suggestion":%q}`, msg, hint)
	}
	return ""
}

func (m PrometheusExecuteTool) cleanupQuery(query string) string {
	query = strings.TrimSpace(query)
	foundClusterMetrics := false
	for _, s := range []string{"node_", "kube_", "container_", "pod_"} {
		if strings.Contains(query, s) {
			foundClusterMetrics = true
			break
		}
	}
	if !foundClusterMetrics {
		query = strings.ReplaceAll(query, "__CLUSTER__", "")
	}
	return query
}

// extractPromQLFromCommand checks whether the command string is a JSON object
// containing a "query" or "command" key (common when the LLM wraps the PromQL
// inside a structured payload instead of providing a raw expression). If so it
// extracts the actual PromQL and moves any extra keys (range, start_time, …)
// into the arguments map so they are not lost.
func extractPromQLFromCommand(command string, arguments map[string]any) (string, map[string]any) {
	trimmed := strings.TrimSpace(command)
	// Note: valid PromQL label selectors also start with '{' but use '='
	// (not ':') as the key-value separator, so they are never valid JSON.
	if !strings.HasPrefix(trimmed, "{") {
		return command, arguments
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return command, arguments
	}

	// Try to find the actual PromQL string inside the JSON.
	promql := ""
	for _, key := range []string{"query", "command", "promql_query", "promql"} {
		if q, ok := parsed[key].(string); ok && q != "" {
			promql = q
			delete(parsed, key)
			break
		}
	}
	if promql == "" {
		return command, arguments
	}

	// Merge remaining keys into arguments so time params are preserved.
	if arguments == nil {
		arguments = map[string]any{}
	}
	for k, v := range parsed {
		if _, exists := arguments[k]; !exists {
			arguments[k] = v
		}
	}

	return promql, arguments
}

func (m PrometheusExecuteTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("prometheus: executing queryPrometheus tool call", "query", input.Command)

	// Guard: LLM may wrap PromQL inside a JSON object (double-serialization).
	origCmd := input.Command
	input.Command, input.Arguments = extractPromQLFromCommand(input.Command, input.Arguments)
	if input.Command != origCmd {
		nbRequestContext.Ctx.GetLogger().Warn("prometheus: detected JSON-wrapped PromQL (double-serialization), extracted query",
			"original", origCmd, "extracted", input.Command)
	}

	query := strings.ReplaceAll(input.Command, "\\", "")
	query = strings.ReplaceAll(query, "`", "")

	// split query by ;
	queries := strings.Split(query, ";")
	finalData := make(map[string]any)
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	// also check query in json format && start/end time on that. for future
	t1, t2, err := ExtractStartEndtimeFromLabels(nbRequestContext, input.Arguments)
	if err == nil {
		startTime = t1
		endTime = t2
	}

	for _, q := range queries {
		q = m.cleanupQuery(q)

		// Validate PromQL syntax locally before hitting the relay server.
		// This catches issues like comma-separated queries, missing brackets,
		// or invalid label matchers in ~0ms — no network round-trip needed.
		// Returns a structured, actionable error so the LLM can self-correct
		// without escalating to the promql_query sub-agent (saves ~565s/run).
		if parseErr := validatePromQLSyntax(q); parseErr != "" {
			nbRequestContext.Ctx.GetLogger().Warn("prometheus: PromQL syntax error detected before relay call", "query", q, "error", parseErr)
			return core.NBToolResponse{
				Data:   parseErr,
				Status: core.NBToolResponseStatusError,
			}, nil
		}

		apiDataAny, err := m.executePromQl(nbRequestContext, q, nbRequestContext.AccountId, map[string]any{
			"end_time":   endTime.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"start_time": startTime.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("unable to execute tool query", "error", err.Error())
			return core.NBToolResponse{
				Data:   "",
				Status: core.NBToolResponseStatusError,
			}, err
		}
		apiData := apiDataAny
		for _, s := range apiData {
			if ss, ok1 := s.(map[string]any); ok1 && ss["values"] != nil {
				valuesAny, ok := ss["values"].([]any)
				if !ok {
					nbRequestContext.Ctx.GetLogger().Warn("prometheus: 'values' field is not a slice of expected type", "series", ss)
					continue
				}
				if len(valuesAny) == 0 {
					continue
				}
				valuesFloat := make([]float64, len(valuesAny))
				sum := 0.0
				minIdx, maxIdx := 0, 0

				for i, v := range valuesAny {
					var f float64
					var e error
					switch val := v.(type) {
					case string:
						f, e = strconv.ParseFloat(val, 64)
					case float64:
						f = val
					case json.Number:
						f, e = val.Float64()
					default:
						f = 0
						e = errors.New("unsupported value type in prometheus response")
					}

					if e != nil {
						f = 0
					}
					if math.IsInf(f, 0) || math.IsNaN(f) {
						f = 0
					}
					valuesFloat[i] = f
					sum += f

					if f < valuesFloat[minIdx] {
						minIdx = i
					}
					if f > valuesFloat[maxIdx] {
						maxIdx = i
					}
				}

				avgValue := sum / float64(len(valuesFloat))

				// Get timestamps for min/max
				var minTimestamp, maxTimestamp any
				if tsAny, ok := ss["timestamps"].([]any); ok {
					if minIdx < len(tsAny) {
						minTimestamp = tsAny[minIdx]
					}
					if maxIdx < len(tsAny) {
						maxTimestamp = tsAny[maxIdx]
					}
				}

				// Sort a copy for p99
				sortedValues := make([]float64, len(valuesFloat))
				copy(sortedValues, valuesFloat)
				slices.Sort(sortedValues)

				p99Index := 0
				if len(sortedValues) > 1 {
					p99Index = int(float64(len(sortedValues))*.99) - 1
				}

				statsMap := map[string]any{
					"min":           sortedValues[0],
					"max":           sortedValues[len(sortedValues)-1],
					"avg":           avgValue,
					"p99":           sortedValues[p99Index],
					"min_timestamp": minTimestamp,
					"max_timestamp": maxTimestamp,
				}
				ss["stats"] = statsMap
				// Write sanitized values back to replace raw values (which may contain +Inf/NaN strings)
				ss["values"] = valuesFloat
			}
		}

		// Sanitize any remaining +Inf/NaN float64 values before marshaling
		sanitizeFloats(apiData)
		data, err := common.MarshalJson(apiData)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("prometheus: unable to marshal data", "error", err.Error())
			return core.NBToolResponse{
				Data:   err.Error(),
				Status: core.NBToolResponseStatusError,
			}, err
		}
		finalData[q] = string(data)
	}
	// Check if all queries returned empty results
	allEmpty := true
	for _, v := range finalData {
		if vStr, ok := v.(string); ok && vStr != "[]" && vStr != "null" {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		slog.Info("prometheus: all queries returned empty data", "query", input.Command, "parentAgentId", nbRequestContext.ParentAgentId)
		return core.NBToolResponse{
			Data:       fmt.Sprintf("No data found for query: %s. The metric may not exist, labels may be incorrect, or there is no data in the selected time range. Try verifying the metric name and labels using metrics_list or search_metrics tools.", input.Command),
			Status:     core.NBToolResponseStatusSuccess,
			References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query"}, "Query Prometheus", map[string]string{"tab": "4", "subtab": "2"}, input.Command)},
		}, nil
	}

	dataResponse, err := common.MarshalJson(finalData)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("prometheus: unable to marshal final data", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}
	return core.NBToolResponse{
		Data:       string(dataResponse),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query"}, "Query Prometheus", map[string]string{"tab": "4", "subtab": "2"}, input.Command)},
	}, nil
}

func (m PrometheusExecuteTool) executePromQl(nbRequestContext core.NbToolContext, query string, accountId string, configs map[string]any) ([]any, error) {
	endTimeStr := time.Now().UTC().Format(time.RFC3339)
	if configs["end_time"] != nil && configs["end_time"] != "" {
		endTimeStr = configs["end_time"].(string)
	}
	endTime, err := time.Parse(time.RFC3339, endTimeStr)
	if err != nil {
		return nil, err
	}
	startTime := endTime.Add(-24 * time.Hour)
	if configs["start_time"] != nil && configs["start_time"] != "" {
		startTimeStr := configs["start_time"].(string)
		startTime, err = time.Parse(time.RFC3339, startTimeStr)
		if err != nil {
			return nil, err
		}
	}

	endTime = endTime.UTC()
	startTime = startTime.UTC()

	// Calculate dynamic step based on duration, aiming for ~60 data points
	duration := endTime.Sub(startTime)
	step := duration / 60
	if step < time.Minute {
		step = time.Minute
	}
	// Round to nearest second for cleaner step string
	stepSeconds := int(step.Seconds())
	stepStr := strconv.Itoa(stepSeconds) + "s"

	// Format the time to the specified string format
	startTimeString := startTime.Format("2006-01-02 15:04:05 UTC")
	endTimeString := endTime.Format("2006-01-02 15:04:05 UTC")
	actionParam := relay.ActionExecuteBody{
		AccountID:  accountId,
		ActionName: "prometheus_enricher",
		ActionParams: map[string]any{
			"promql_query": query,
			"duration":     map[string]any{"starts_at": startTimeString, "ends_at": endTimeString},
			"step":         stepStr,
		},
	}
	slog.Debug("prometheus query", "query", query, "start_time", startTimeString, "end_time", endTimeString)
	response, err := relay.Execute(actionParam)
	if err != nil {
		return nil, err
	}
	dataFromEvidence, err := m.getDataFromRelayPrometheusResponse(response)
	if err != nil {
		slog.Error("error marshaling JSON at Prometheus API call:", "error", err)
		return nil, err
	}
	slog.Debug("prometheus response data", "data", dataFromEvidence)
	return dataFromEvidence, nil
}

func (m PrometheusExecuteTool) getDataFromRelayPrometheusResponse(relayResponse map[string]any) ([]any, error) {
	dataFromResponse, ok := relayResponse["data"].(map[string]any)
	if !ok || dataFromResponse == nil {
		slog.Info("prometheus relay response", "relayResponse", relayResponse)
		return nil, errors.New("data field not found or is nil from response")
	}
	findings, ok := dataFromResponse["findings"].([]any)
	if !ok || findings == nil {
		slog.Info("prometheus relay response", "data", dataFromResponse)
		return nil, errors.New("findings field not found or is nil from data")
	}
	if len(findings) == 0 {
		slog.Info("prometheus: query returned empty findings (no matching data)")
		return []any{}, nil
	}
	firstFinding, ok := findings[0].(map[string]any)
	if !ok || firstFinding == nil {
		slog.Info("prometheus relay response", "findings", findings)
		return nil, errors.New("findings field has no values from")
	}
	evidence, ok := firstFinding["evidence"].([]any)
	if !ok || evidence == nil {
		slog.Info("prometheus relay response", "firstFinding", firstFinding)
		return nil, errors.New("evidence field not found or is nil from findings")
	}
	if len(evidence) == 0 {
		slog.Info("prometheus relay response", "evidence", evidence)
		return nil, errors.New("evidence field is empty")
	}
	firstEvidence, ok := evidence[0].(map[string]any)
	if !ok || firstEvidence == nil {
		slog.Info("prometheus relay response", "evidence", evidence)
		return nil, errors.New("evidence field has no values")
	}
	evidenceData, ok := firstEvidence["data"].(string)
	if !ok {
		slog.Info("prometheus relay response", "firstEvidence", firstEvidence)
		return nil, errors.New("data field not found or is nil from evidence")
	}
	var dataList []map[string]any
	if err := common.UnmarshalJson([]byte(evidenceData), &dataList); err != nil {
		slog.Info("prometheus relay response, unable to unmarshal", "evidenceData", evidenceData)
		return nil, err
	}
	seriesData, err := m.getMappedValuesFromDataList(dataList)
	if err != nil {
		slog.Info("prometheus relay response, unable to unmarshal", "datalist", dataList)
		return nil, err
	}
	return seriesData, nil
}

func (m PrometheusExecuteTool) getMappedValuesFromDataList(dataList []map[string]any) ([]any, error) {
	for _, data := range dataList {
		if data == nil || data["data"] == nil {
			continue
		}

		// Handle both map and string (double-encoded JSON) formats for data["data"]
		var dataMap map[string]any
		switch v := data["data"].(type) {
		case map[string]any:
			dataMap = v
		case string:
			if err := common.UnmarshalJson([]byte(v), &dataMap); err != nil {
				slog.Warn("prometheus: unable to parse data string from relay response", "error", err)
				continue
			}
		default:
			continue
		}

		// Handle "query" wrapper (prometheus_queries_enricher format)
		if queryData, ok := dataMap["query"].(map[string]any); ok {
			dataMap = queryData
		}

		// Check result_type for error responses
		if resultType, _ := dataMap["result_type"].(string); resultType == "error" {
			errMsg := "prometheus query returned an error"
			if stringResult, ok := dataMap["string_result"].(string); ok && stringResult != "" {
				errMsg = stringResult
			}
			return nil, errors.New(errMsg)
		}

		// Try series_list_result (matrix/range queries)
		if seriesList, ok := dataMap["series_list_result"].([]any); ok && len(seriesList) > 0 {
			return seriesList, nil
		}

		// Try vector_result (instant queries)
		if vectorResult, ok := dataMap["vector_result"].([]any); ok && len(vectorResult) > 0 {
			return vectorResult, nil
		}

		// Try scalar_result (scalar aggregation queries).
		// Uses "value" (singular) intentionally — scalar results are a single [timestamp, value]
		// pair, not a time series, so stats computation (min/max/avg/p99) in Call() is skipped.
		if scalarResult, ok := dataMap["scalar_result"].([]any); ok && len(scalarResult) > 0 {
			return []any{map[string]any{"metric": map[string]any{}, "value": scalarResult}}, nil
		}

		// No non-empty result found in this data entry
		return []any{}, nil
	}
	return []any{}, nil
}

// sanitizeFloats recursively walks a data structure and replaces any +Inf/NaN
// float64 values with 0, preventing JSON marshal failures.
func sanitizeFloats(v any) any {
	switch val := v.(type) {
	case float64:
		if math.IsInf(val, 0) || math.IsNaN(val) {
			return float64(0)
		}
		return val
	case []any:
		for i, item := range val {
			val[i] = sanitizeFloats(item)
		}
	case map[string]any:
		for k, item := range val {
			val[k] = sanitizeFloats(item)
		}
	case []float64:
		for i, f := range val {
			if math.IsInf(f, 0) || math.IsNaN(f) {
				val[i] = 0
			}
		}
	}
	return v
}

type MetricsListTool struct {
	Provider string
}

func (m MetricsListTool) Name() string {
	return ToolMetricsList
}

func (m MetricsListTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m MetricsListTool) Description() string {
	return `Returns List of Available metrics.
		Usage:
		* Input: (optional) Provide a keyword to filter metrics.
		* Output: The tool will return the list of metrics.

		Purpose: Use this tool to search for available metrics.
		`
}

func (m MetricsListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "metrics name to search (required)",
			},
		},
		Required: []string{"command"},
	}
}

func (m MetricsListTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("metrics: executing metrics.metrics tool call", "query", input.Command)
	query := strings.ReplaceAll(input.Command, "\\", "")
	query = strings.ReplaceAll(query, "`", "")

	if query == "" {
		return core.NBToolResponse{
			Data:   "Error: 'command' (search term) is required. Please provide a keyword to search for metrics (e.g., 'cpu', 'memory').",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	provider := "prometheus"
	if m.Provider != "" {
		provider = m.Provider
	}

	// Check cache before hitting the backend (~8s saved per cache hit).
	cacheKey := nbRequestContext.AccountId + ":" + provider + ":" + query
	if cached, ok := metricsListCache.Load(cacheKey); ok {
		entry := cached.(metricsListCacheEntry)
		if time.Now().Before(entry.expiry) {
			nbRequestContext.Ctx.GetLogger().Info("metrics: cache hit for metrics_list", "query", query)
			return entry.response, nil
		}
		metricsListCache.Delete(cacheKey)
	}

	var metricsResponse core.ObservabilityMetricsSeriesResponse

	if strings.EqualFold(provider, "ES") {
		// ES doesn't support metrics_list; use metrics_list_label_values with label="name"
		// and the index pattern passed in a nested "request" object (matching the GraphQL schema).
		indexPattern := query
		labelValuesResp, err := services_server.ListMetricsSeriesLabelValues(
			*nbRequestContext.Ctx, nbRequestContext.AccountId, provider, "name",
			map[string]any{"request": map[string]any{"metric_name": indexPattern}},
		)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("metrics: unable to fetch ES metric names via label values", "error", err.Error())
			return core.NBToolResponse{
				Data:   err.Error(),
				Status: core.NBToolResponseStatusError,
			}, err
		}
		// Convert label values to MetricsSeries format for downstream processing.
		series := make([]core.ObservabilityMetricsSeries, 0, len(labelValuesResp.Values))
		for _, v := range labelValuesResp.Values {
			series = append(series, core.ObservabilityMetricsSeries{Metric: v.Value})
		}
		metricsResponse = core.ObservabilityMetricsSeriesResponse{Series: series}
	} else {
		var err error
		metricsResponse, err = services_server.ListMetricsSeries(*nbRequestContext.Ctx, nbRequestContext.AccountId, provider, query)
		if err != nil {
			nbRequestContext.Ctx.GetLogger().Error("metrics: unable to fetch metrics", "error", err.Error())
			return core.NBToolResponse{
				Data:   err.Error(),
				Status: core.NBToolResponseStatusError,
			}, err
		}
	}

	// For ES provider, query is an index pattern — skip keyword filtering.
	if query != "" && !strings.EqualFold(provider, "ES") {
		// Choose filtering method based on config
		if config.Config.EnableLLMMetricsFiltering {
			// Use LLM to intelligently filter metrics based on user question
			nbRequestContext.Ctx.GetLogger().Info("metrics: using LLM-based filtering")
			filteredMetrics, err := filterMetricsWithLLM(nbRequestContext, query, metricsResponse.Series)
			if err != nil {
				nbRequestContext.Ctx.GetLogger().Error("metrics: LLM filtering failed, falling back to fuzzy matching", "error", err.Error())
				// Fallback to fuzzy matching if LLM fails
				metricsResponse.Series = filterMetricsWithFuzzyMatch(query, metricsResponse.Series)
			} else {
				metricsResponse.Series = filteredMetrics
			}
		} else {
			// Use traditional fuzzy matching
			nbRequestContext.Ctx.GetLogger().Info("metrics: using fuzzy matching filtering")
			metricsResponse.Series = filterMetricsWithFuzzyMatch(query, metricsResponse.Series)
		}
	}

	// Deduplicate histogram families (_bucket, _count, _sum, _total, _created)
	// and cap results to prevent overwhelming LLM context
	familySuffixes := []string{"_bucket", "_count", "_sum", "_total", "_created"}
	seen := map[string]bool{}
	deduped := []core.ObservabilityMetricsSeries{}
	for _, m := range metricsResponse.Series {
		base := m.Metric
		for _, suffix := range familySuffixes {
			if strings.HasSuffix(base, suffix) {
				base = strings.TrimSuffix(base, suffix)
				break
			}
		}
		if !seen[base] {
			seen[base] = true
			deduped = append(deduped, m)
		}
	}
	totalFamilies := len(seen)
	const maxMetricsResults = 30
	if len(deduped) > maxMetricsResults {
		deduped = deduped[:maxMetricsResults]
	}
	metricsResponse.Series = deduped

	dataResponse, err := common.MarshalJson(metricsResponse)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("metrics: unable to marshal final data", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}

	responseData := string(dataResponse)
	if totalFamilies > maxMetricsResults {
		responseData = fmt.Sprintf("Note: Showing %d of %d metric families. Use a more specific keyword to narrow results.\n\n%s", maxMetricsResults, totalFamilies, responseData)
	}

	// Only add a reference if there are results to show
	var refs []core.NBToolResponseReference
	if len(deduped) > 0 {
		refLabel := "Query Prometheus"
		if strings.EqualFold(provider, "datadog") {
			refLabel = "Query Datadog Metrics"
		}
		refs = []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query"}, refLabel, map[string]string{"tab": "4", "subtab": "2"}, query)}
	}

	result := core.NBToolResponse{
		Data:       responseData,
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: refs,
	}
	metricsListCache.Store(cacheKey, metricsListCacheEntry{response: result, expiry: time.Now().Add(metricsListCacheTTL())})
	return result, nil
}

// filterMetricsWithLLM uses LLM to intelligently filter metrics based on user's question
func filterMetricsWithLLM(nbRequestContext core.NbToolContext, userQuery string, allMetrics []core.ObservabilityMetricsSeries) ([]core.ObservabilityMetricsSeries, error) {
	if len(allMetrics) == 0 {
		return allMetrics, nil
	}

	// Build list of metric names
	metricNames := make([]string, 0, len(allMetrics))
	for _, m := range allMetrics {
		metricNames = append(metricNames, m.Metric)
	}

	// Limit to first 500 metrics to avoid overwhelming the LLM
	maxMetrics := 500
	if len(metricNames) > maxMetrics {
		metricNames = metricNames[:maxMetrics]
	}

	// Build prompt for LLM
	prompt := buildMetricFilterPrompt(userQuery, metricNames)

	// Call LLM via the adapter
	llmClient := core.GetLLMClient()
	if llmClient == nil {
		return nil, errors.New("LLM client not initialized")
	}

	response, err := llmClient.GenerateContent(
		nbRequestContext.Ctx,
		nbRequestContext.UserId,
		nbRequestContext.AccountId,
		"",    // conversationId
		"",    // messageId
		"",    // agentId
		false, // trackContent
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeHuman, prompt),
		},
		true, // cleanupMarkdown
	)

	if err != nil {
		return nil, err
	}

	if len(response.Choices) == 0 || len(response.Choices[0].Content) == 0 {
		return nil, errors.New("empty LLM response")
	}

	// Parse LLM response to get filtered metric names
	selectedMetrics := parseMetricNamesFromLLMResponse(response.Choices[0].Content)

	// Filter original metrics based on LLM selection
	filteredMetrics := make([]core.ObservabilityMetricsSeries, 0)
	selectedSet := make(map[string]bool)
	for _, name := range selectedMetrics {
		selectedSet[name] = true
	}

	for _, metric := range allMetrics {
		if selectedSet[metric.Metric] {
			filteredMetrics = append(filteredMetrics, metric)
		}
	}

	// Limit to top 100 results
	if len(filteredMetrics) > 100 {
		filteredMetrics = filteredMetrics[:100]
	}

	return filteredMetrics, nil
}

// filterMetricsWithFuzzyMatch uses traditional fuzzy matching to filter metrics
func filterMetricsWithFuzzyMatch(query string, allMetrics []core.ObservabilityMetricsSeries) []core.ObservabilityMetricsSeries {
	// Score and rank metrics by relevance instead of simple substring match
	type ScoredMetric struct {
		Metric core.ObservabilityMetricsSeries
		Score  int
	}

	scoredMetrics := []ScoredMetric{}
	queryLower := strings.ToLower(query)

	for _, metric := range allMetrics {
		metricLower := strings.ToLower(metric.Metric)
		score := 0

		// Exact match (highest priority)
		if metricLower == queryLower {
			score = 100
		} else if strings.HasPrefix(metricLower, queryLower) {
			// Prefix match: "cpu" matches "cpu_usage_total"
			score = 90
		} else if strings.HasSuffix(metricLower, queryLower) {
			// Suffix match: "total" matches "cpu_usage_total"
			score = 80
		} else {
			// Word boundary match (split by _, ., -)
			words := strings.FieldsFunc(metricLower, func(r rune) bool {
				return r == '_' || r == '.' || r == '-'
			})
			for i, word := range words {
				if word == queryLower {
					// Exact word match, earlier position = higher score
					score = 70 - i*2
					break
				} else if strings.HasPrefix(word, queryLower) {
					// Word prefix match
					score = 60 - i*2
					break
				}
			}

			// Fallback to substring match (lowest priority)
			if score == 0 && strings.Contains(metricLower, queryLower) {
				// Lower score for matches further from start
				idx := strings.Index(metricLower, queryLower)
				score = 50 - (idx / 10)
			}
		}

		// Only include if matched
		if score > 0 {
			scoredMetrics = append(scoredMetrics, ScoredMetric{
				Metric: metric,
				Score:  score,
			})
		}
	}

	// Sort by score (descending), then by name for stable ordering
	sort.Slice(scoredMetrics, func(i, j int) bool {
		if scoredMetrics[i].Score != scoredMetrics[j].Score {
			return scoredMetrics[i].Score > scoredMetrics[j].Score
		}
		return scoredMetrics[i].Metric.Metric < scoredMetrics[j].Metric.Metric
	})

	// Limit to top 100 results
	maxResults := 100
	if len(scoredMetrics) > maxResults {
		scoredMetrics = scoredMetrics[:maxResults]
	}

	// Extract metrics
	filteredMetrics := make([]core.ObservabilityMetricsSeries, len(scoredMetrics))
	for i, sm := range scoredMetrics {
		filteredMetrics[i] = sm.Metric
		// Add match score as metadata for transparency
		if filteredMetrics[i].Attributes == nil {
			filteredMetrics[i].Attributes = make(map[string]any)
		}
		filteredMetrics[i].Attributes["_match_score"] = sm.Score
	}

	return filteredMetrics
}

// buildMetricFilterPrompt creates a prompt for the LLM to filter metrics
func buildMetricFilterPrompt(userQuery string, metricNames []string) string {
	metricsJSON, _ := json.Marshal(metricNames)

	return `You are a metrics filtering assistant. Given a user's question and a list of available metric names, select ONLY the metrics that are relevant to answering the user's question.

User Question: ` + userQuery + `

Available Metrics:
` + string(metricsJSON) + `

Instructions:
1. Analyze the user's question to understand what metrics they need
2. Select ONLY metrics that are directly relevant to answering the question
3. Return the selected metric names as a JSON array
4. If the question is about CPU, select CPU-related metrics
5. If the question is about memory, select memory-related metrics
6. If the question is about network, select network-related metrics
7. Be selective - only include metrics that are clearly relevant

Return ONLY a JSON array of metric names, nothing else. Example: ["metric1", "metric2", "metric3"]`
}

// parseMetricNamesFromLLMResponse extracts metric names from LLM response
func parseMetricNamesFromLLMResponse(content string) []string {
	// Try to parse as JSON array
	var metricNames []string

	// Clean up markdown code blocks if present
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	err := json.Unmarshal([]byte(content), &metricNames)
	if err == nil {
		return metricNames
	}

	// Fallback: try to extract metric names line by line
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, `"'`)
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "//") {
			metricNames = append(metricNames, line)
		}
	}

	return metricNames
}

type ListMetricsLabelsTool struct {
	Provider string
}

func (m ListMetricsLabelsTool) Name() string {
	return ToolMetricsLabelsList
}

func (m ListMetricsLabelsTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (m ListMetricsLabelsTool) Description() string {
	return `Returns List of Available labels for a given metric.
		Usage:
		* Input: (required) Metric Name.
		* Output: The tool will return available labels for the given metric.

		Purpose: Use this tool to search for available metrics.
		`
}

func (m ListMetricsLabelsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "metrics name to fetch labels",
			},
		},
		Required: []string{"command"},
	}
}

func (m ListMetricsLabelsTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("metrics: executing metrics.label tool call", "query", input.Command)
	query := strings.ReplaceAll(input.Command, "\\", "")
	query = strings.ReplaceAll(query, "`", "")

	if query == "" {
		return core.NBToolResponse{
			Data:   "metrics name is required to fetch labels",
			Status: core.NBToolResponseStatusError,
		}, errors.New("metrics name is required to fetch labels")
	}

	provider := "prometheus"
	if m.Provider != "" {
		provider = m.Provider
	}

	labelsResponse, err := services_server.ListMetricsSeriesLabels(*nbRequestContext.Ctx, nbRequestContext.AccountId, provider, query)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("unable to fetch metrics labels", "error", err.Error())
		return core.NBToolResponse{
			Data:   err.Error(),
			Status: core.NBToolResponseStatusError,
		}, err
	}

	dataResponse, err := common.MarshalJson(labelsResponse)
	if err != nil {
		nbRequestContext.Ctx.GetLogger().Error("metrics: unable to marshal final data", "error", err.Error())
		return core.NBToolResponse{
			Data:   "",
			Status: core.NBToolResponseStatusError,
		}, err
	}

	// Only add a reference if there are labels to show
	var labelRefs []core.NBToolResponseReference
	if len(labelsResponse.Labels) > 0 {
		refLabel := "Query Prometheus"
		if strings.EqualFold(provider, "datadog") {
			refLabel = "Query Datadog Metrics"
		}
		labelRefs = []core.NBToolResponseReference{core.GetNudgebeeUIReferenceForClusterDetails(nbRequestContext, []string{"monitoring", "query"}, refLabel, map[string]string{"tab": "4", "subtab": "2"}, query)}
	}

	return core.NBToolResponse{
		Data:       string(dataResponse),
		Type:       core.NBToolResponseTypeJson,
		Status:     core.NBToolResponseStatusSuccess,
		References: labelRefs,
	}, nil
}

// SearchMetricsTool provides semantic/natural language search over customer metrics via RAG.
// Unlike MetricsListTool (keyword substring match), this uses vector similarity search
// against the account-specific prometheus collection in Qdrant, returning the most
// semantically relevant metrics with example PromQL queries.
type SearchMetricsTool struct {
	Provider string
}

func (s SearchMetricsTool) Name() string {
	return ToolSearchMetrics
}

func (s SearchMetricsTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (s SearchMetricsTool) Description() string {
	return `Searches for available metrics using semantic/natural language search.
		Usage:
		* Input: (required) natural language description of the metric you're looking for (e.g. 'redis memory usage', 'kafka consumer lag', 'HTTP error rate').
		* Output: Top matching metrics with example PromQL queries.

		Purpose: Use this tool to discover custom/unknown metrics by describing what you need in natural language.
		`
}

func (s SearchMetricsTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"command": {
				Type:        core.ToolSchemaTypeString,
				Description: "natural language description of the metric (e.g. 'redis memory usage', 'kafka consumer lag', 'HTTP error rate')",
			},
		},
		Required: []string{"command"},
	}
}

func (s SearchMetricsTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	nbRequestContext.Ctx.GetLogger().Info("search_metrics: executing semantic metric search", "query", input.Command)
	query := strings.TrimSpace(input.Command)
	query = strings.ReplaceAll(query, "\\", "")
	query = strings.ReplaceAll(query, "`", "")

	if query == "" {
		return core.NBToolResponse{
			Data:   "Error: search query is required. Provide a natural language description of the metric (e.g., 'redis memory usage', 'kafka consumer lag').",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	results := core.QueryRAG(
		nbRequestContext.UserId, nbRequestContext.AccountId, query,
		"prometheus", 5,
		nbRequestContext.ConversationId, nbRequestContext.MessageId, nbRequestContext.ParentAgentId, true,
	)

	if len(results) == 0 {
		return core.NBToolResponse{
			Data:   "No matching metrics found for: " + query + ". Try different search terms or use metrics_list for keyword-based search.",
			Status: core.NBToolResponseStatusSuccess,
		}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d matching metrics:\n\n", len(results))
	for i, result := range results {
		var doc map[string]any
		if err := json.Unmarshal([]byte(result.Document), &doc); err != nil {
			continue
		}

		metricName := ""
		if metadata, ok := doc["metadata"].(map[string]any); ok {
			if m, ok := metadata["metric"].(string); ok {
				metricName = m
			}
		}
		if metricName == "" {
			if q, ok := doc["question"].(string); ok {
				metricName = q
			}
		}

		question, _ := doc["question"].(string)
		answer, _ := doc["answer"].(string)

		fmt.Fprintf(&sb, "%d. Metric: %s (similarity: %.2f)\n", i+1, metricName, result.SimilarityScore)
		if question != "" {
			fmt.Fprintf(&sb, "   Q: %s\n", question)
		}
		if answer != "" {
			fmt.Fprintf(&sb, "   PromQL: %s\n", answer)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Use the metric name and example PromQL above to construct your answer directly.")

	return core.NBToolResponse{
		Data:   sb.String(),
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}
