package tools

import (
	"fmt"
	"math"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/tools/core"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/nudgebee/logparser"
)

const (
	defaultPaginationLimit  = 50
	bytesPerMB              = 1024 * 1024
	slowSpanThresholdNs     = 1_000_000_000
	topLogErrorPatternCount = 5
	logFilterContextLines   = 2
)

func init() {
	core.RegisterNBToolFactory(ToolGetEventEvidence, func(accountId string) (core.NBTool, error) {
		return GetEventEvidenceTool{}, nil
	})
}

const ToolGetEventEvidence = "get_event_evidence"

type GetEventEvidenceTool struct {
}

func (t GetEventEvidenceTool) Name() string {
	return ToolGetEventEvidence
}

func (t GetEventEvidenceTool) GetType() core.NBToolType {
	return core.NBToolTypeTool
}

func (t GetEventEvidenceTool) Description() string {
	return `Fetches evidence data for a specific event. Supports query modes:
- raw (default): returns full evidence data
- summary: aggregated stats — metrics: min/max/avg per series; traces: span count, errors, slow spans; logs: line count, top error patterns
- filter: for logs, returns lines matching a pattern with context lines. For array types, applies offset/limit pagination.
Use offset/limit for pagination on large evidence sets.
Evidence types: logs, pod_metrics, node_metrics, container_metrics, traces, deployment, pod_events, node_events, pod_data, alert_labels, noisy_neighbours, related_events, api_failures, metrics_data, job_information, job_events, user_actions, rdbms_query_response, alert_data, markdowns, all`
}

func (t GetEventEvidenceTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"event_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "The event ID to fetch evidence for",
			},
			"evidence_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Optional evidence type filter: logs, pod_metrics, node_metrics, container_metrics, traces, deployment, pod_events, node_events, pod_data, alert_labels, noisy_neighbours, related_events, api_failures, metrics_data, job_information, job_events, user_actions, rdbms_query_response, alert_data, markdowns, all. If omitted, returns all evidence.",
			},
			"query_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "How to process evidence. 'raw' (default): full data. 'summary': aggregated stats for metrics (min/max/avg per series), span summary for traces (count, errors, slow spans), line count + error patterns for logs. 'filter': for logs, returns lines matching pattern with context. For other types, applies offset/limit pagination.",
			},
			"pattern": {
				Type:        core.ToolSchemaTypeString,
				Description: "For logs with query_mode=filter: case-insensitive substring pattern to match log lines. Returns matching lines with ±2 lines of context.",
			},
			"offset": {
				Type:        core.ToolSchemaTypeNumber,
				Description: "Skip first N entries for pagination. Default 0.",
			},
			"limit": {
				Type:        core.ToolSchemaTypeNumber,
				Description: "Max entries to return. Default 50 for logs, 100 for array types.",
			},
		},
		Required: []string{"event_id"},
	}
}

func (t GetEventEvidenceTool) Call(nbRequestContext core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	eventId := input.Command
	if eventId == "" {
		if id, ok := input.Arguments["event_id"].(string); ok {
			eventId = id
		}
	}
	eventId = strings.TrimSpace(eventId)
	if eventId == "" {
		return core.NBToolResponse{
			Data:   "Error: event_id is required",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// Validate UUID format before querying to fail fast on stale or malformed IDs
	if _, err := uuid.Parse(eventId); err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error: invalid event_id format '%s' — expected a valid UUID. Try searching for recent events instead of using this ID.", eventId),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	evidenceType := ""
	if et, ok := input.Arguments["evidence_type"].(string); ok {
		evidenceType = et
	}

	// Fetch the event using the same pattern as EvidenceInsightsTool.getEventData()
	eventTool := EventsExecuteTool{}
	toolCtx := core.NewNbToolContext(
		nbRequestContext.Ctx, eventTool,
		nbRequestContext.AccountId, nbRequestContext.UserId,
		nbRequestContext.ConversationId, nbRequestContext.MessageId,
		nbRequestContext.ParentAgentId, eventId,
		nil, "", core.NBQueryConfig{}, "",
	)

	query := fmt.Sprintf(`select * from events where id = '%s'`, eventId)
	resp, err := eventTool.Call(toolCtx, core.NBToolCallRequest{
		Command: query,
	})
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error fetching event: %v", err),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	if resp.Data == "[]" || resp.Data == "" {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Event not found with id: %s. The event may have expired or been deleted. Try searching for recent events using the events tool instead.", eventId),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	// If no specific evidence type requested, return everything
	if evidenceType == "" || evidenceType == "all" {
		return core.NBToolResponse{
			Data: resp.Data,
			Type: core.NBToolResponseTypeJson,
		}, nil
	}

	// Parse the event to extract specific evidence type
	var eventList []events.Event
	if err := common.UnmarshalJson([]byte(resp.Data), &eventList); err != nil {
		// If we can't parse as events, return raw data
		return core.NBToolResponse{
			Data: resp.Data,
			Type: core.NBToolResponseTypeJson,
		}, nil
	}

	if len(eventList) == 0 {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Event not found with id: %s", eventId),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	event := eventList[0]
	evidence := extractEvidenceByType(event.Evidences, evidenceType)
	if evidence == nil {
		return core.NBToolResponse{
			Data: fmt.Sprintf("No '%s' evidence found for event %s", evidenceType, eventId),
		}, nil
	}

	// Check for query mode
	queryMode := ""
	if qm, ok := input.Arguments["query_mode"].(string); ok {
		queryMode = qm
	}
	if queryMode != "" && queryMode != "raw" {
		offset := 0
		if o, ok := input.Arguments["offset"].(float64); ok && int(o) > 0 {
			offset = int(o)
		}
		limit := defaultPaginationLimit
		if l, ok := input.Arguments["limit"].(float64); ok && int(l) > 0 {
			limit = int(l)
		}
		pattern := ""
		if p, ok := input.Arguments["pattern"].(string); ok {
			pattern = p
		}

		switch queryMode {
		case "summary":
			return t.summarizeEvidence(evidence, evidenceType)
		case "filter":
			return t.filterEvidence(evidence, evidenceType, pattern, offset, limit)
		}
	}

	result, err := common.MarshalJson(evidence)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error marshaling evidence: %v", err),
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	return core.NBToolResponse{
		Data: string(result),
		Type: core.NBToolResponseTypeJson,
	}, nil
}

// extractEvidenceByType extracts a specific type of evidence from InvestigateData.
func extractEvidenceByType(data events.InvestigateData, evidenceType string) any {
	switch evidenceType {
	case "logs":
		result := map[string]any{}
		if data.LogData != "" {
			result["log_data"] = data.LogData
		}
		if len(data.ErrorLogData) > 0 {
			result["error_log_data"] = data.ErrorLogData
		}
		if len(result) == 0 {
			return nil
		}
		return result
	case "pod_metrics":
		if len(data.PodMetrics) == 0 {
			return nil
		}
		return data.PodMetrics
	case "node_metrics":
		if len(data.NodeMetrics) == 0 {
			return nil
		}
		return data.NodeMetrics
	case "container_metrics":
		if data.ContainerMetrics.Data == nil {
			return nil
		}
		return data.ContainerMetrics
	case "traces":
		if data.Traces.Data == nil {
			return nil
		}
		return data.Traces
	case "deployment":
		if data.Deployment.Data == nil {
			return nil
		}
		return data.Deployment
	case "pod_events":
		if len(data.PodEvents) == 0 {
			return nil
		}
		return data.PodEvents
	case "node_events":
		if len(data.NodeEvents) == 0 {
			return nil
		}
		return data.NodeEvents
	case "pod_data":
		if data.PodData.Data == nil {
			return nil
		}
		return data.PodData
	case "alert_labels":
		if data.AlertLabels.Data == nil {
			return nil
		}
		return data.AlertLabels
	case "noisy_neighbours":
		if len(data.NoisyNeighbours) == 0 {
			return nil
		}
		return data.NoisyNeighbours
	case "related_events":
		if data.RelatedEvents.Data == nil {
			return nil
		}
		return data.RelatedEvents
	case "api_failures":
		if len(data.ApiFailures) == 0 {
			return nil
		}
		return data.ApiFailures
	case "metrics_data":
		if len(data.MetricsData) == 0 {
			return nil
		}
		return data.MetricsData
	case "job_information":
		if data.JobInformation.Data == nil {
			return nil
		}
		return data.JobInformation
	case "job_events":
		if data.JobEvents.Data == nil {
			return nil
		}
		return data.JobEvents
	case "user_actions":
		if len(data.UserActions) == 0 {
			return nil
		}
		return data.UserActions
	case "rdbms_query_response":
		if len(data.RDBMSQueryData) == 0 {
			return nil
		}
		return data.RDBMSQueryData
	case "alert_data":
		if data.AlertData.Data == nil {
			return nil
		}
		return data.AlertData
	case "markdowns":
		if len(data.Markdowns) == 0 {
			return nil
		}
		return data.Markdowns
	default:
		return nil
	}
}

// summarizeEvidence routes to type-specific summary implementations.
func (t GetEventEvidenceTool) summarizeEvidence(evidence any, evidenceType string) (core.NBToolResponse, error) {
	switch evidenceType {
	case "pod_metrics", "node_metrics":
		return t.summarizePodNodeMetrics(evidence)
	case "container_metrics":
		return t.summarizeContainerMetrics(evidence)
	case "metrics_data":
		return t.summarizePrometheusMetrics(evidence)
	case "traces":
		return t.summarizeTraces(evidence)
	case "logs":
		return t.summarizeLogs(evidence)
	default:
		// For types without summary support, return as-is
		result, err := common.MarshalJson(evidence)
		if err != nil {
			return core.NBToolResponse{Data: "Error marshaling evidence", Status: core.NBToolResponseStatusError}, nil
		}
		return core.NBToolResponse{Data: string(result), Type: core.NBToolResponseTypeJson}, nil
	}
}

// summarizePodNodeMetrics summarizes []InvestigateDataInsight for pod_metrics or node_metrics.
// Each insight's .Data is map[string]any: {"name":"pod_metric", "data": [...], "resource_type": "Memory"}
// Each data entry: {"metric": {..., "requests": {}, "limits": {}}, "timestamps": [...], "values": [...]}
func (t GetEventEvidenceTool) summarizePodNodeMetrics(evidence any) (core.NBToolResponse, error) {
	insights, ok := evidence.([]events.InvestigateDataInsight)
	if !ok {
		return t.marshalResponse(evidence)
	}

	var summaries []map[string]any
	for _, insight := range insights {
		dataMap := t.toMap(insight.Data)
		if dataMap == nil {
			continue
		}

		resourceType, _ := dataMap["resource_type"].(string)
		dataEntries, _ := dataMap["data"].([]any)

		for _, entry := range dataEntries {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			timestamps, _ := entryMap["timestamps"].([]any)
			values, _ := entryMap["values"].([]any)
			metricMap, _ := entryMap["metric"].(map[string]any)

			if len(values) == 0 {
				continue
			}

			floatValues := make([]float64, 0, len(values))
			for _, v := range values {
				if fv := toFloat64(v); !math.IsNaN(fv) {
					floatValues = append(floatValues, fv)
				}
			}

			if len(floatValues) == 0 {
				continue
			}

			summary := map[string]any{
				"resource_type": resourceType,
				"data_points":   len(floatValues),
				"min":           minFloat(floatValues),
				"max":           maxFloat(floatValues),
				"avg":           avgFloat(floatValues),
			}

			// Convert bytes to MB for memory
			if strings.EqualFold(resourceType, "memory") {
				summary["unit"] = "MB"
				summary["min"] = math.Round(minFloat(floatValues)/bytesPerMB*100) / 100
				summary["max"] = math.Round(maxFloat(floatValues)/bytesPerMB*100) / 100
				summary["avg"] = math.Round(avgFloat(floatValues)/bytesPerMB*100) / 100
			}

			if len(timestamps) > 0 {
				summary["first_timestamp"] = timestamps[0]
				summary["last_timestamp"] = timestamps[len(timestamps)-1]
			}

			if metricMap != nil {
				if pod, ok := metricMap["pod"].(string); ok {
					summary["pod"] = pod
				}
				if container, ok := metricMap["container"].(string); ok {
					summary["container"] = container
				}
				if requests, ok := metricMap["requests"].(map[string]any); ok {
					summary["requests"] = requests
				}
				if limits, ok := metricMap["limits"].(map[string]any); ok {
					summary["limits"] = limits
				}
			}

			summaries = append(summaries, summary)
		}
	}

	return t.marshalResponse(summaries)
}

// summarizeContainerMetrics summarizes a single InvestigateDataInsight for container_metrics.
// Same data shape as pod_metrics but a single insight instead of a slice.
func (t GetEventEvidenceTool) summarizeContainerMetrics(evidence any) (core.NBToolResponse, error) {
	insight, ok := evidence.(events.InvestigateDataInsight)
	if !ok {
		return t.marshalResponse(evidence)
	}
	// Wrap in slice and reuse pod/node metrics summary
	return t.summarizePodNodeMetrics([]events.InvestigateDataInsight{insight})
}

// summarizePrometheusMetrics summarizes []InvestigateDataInsight for metrics_data.
// Each insight's .Data is map[string]any: {"result_type":"matrix", "series_list_result": [...]}
// Each series: {"metric": {"name":..., labels...}, "values": [...], "timestamps": [...]}
func (t GetEventEvidenceTool) summarizePrometheusMetrics(evidence any) (core.NBToolResponse, error) {
	insights, ok := evidence.([]events.InvestigateDataInsight)
	if !ok {
		return t.marshalResponse(evidence)
	}

	var summaries []map[string]any
	for _, insight := range insights {
		dataMap := t.toMap(insight.Data)
		if dataMap == nil {
			continue
		}

		seriesList, _ := dataMap["series_list_result"].([]any)
		for _, series := range seriesList {
			seriesMap, ok := series.(map[string]any)
			if !ok {
				continue
			}

			metricLabels, _ := seriesMap["metric"].(map[string]any)
			values, _ := seriesMap["values"].([]any)
			timestamps, _ := seriesMap["timestamps"].([]any)

			floatValues := make([]float64, 0, len(values))
			for _, v := range values {
				if fv := toFloat64(v); !math.IsNaN(fv) {
					floatValues = append(floatValues, fv)
				}
			}

			summary := map[string]any{
				"metric":      metricLabels,
				"data_points": len(floatValues),
			}

			if len(floatValues) > 0 {
				summary["min"] = minFloat(floatValues)
				summary["max"] = maxFloat(floatValues)
				summary["avg"] = avgFloat(floatValues)
				summary["latest"] = floatValues[len(floatValues)-1]
			}

			if len(timestamps) > 0 {
				summary["first_timestamp"] = timestamps[0]
				summary["last_timestamp"] = timestamps[len(timestamps)-1]
			}

			summaries = append(summaries, summary)
		}
	}

	return t.marshalResponse(summaries)
}

// summarizeTraces summarizes InvestigateDataInsight for traces.
// .Data is map[string]any: {"name":..., "data": [spans...]}
// Each span: {"Timestamp":"RFC3339", "SpanName":"query", "ServiceName":..., "Duration": float64 (ns), "StatusCode":..., "SpanAttributes":{...}}
func (t GetEventEvidenceTool) summarizeTraces(evidence any) (core.NBToolResponse, error) {
	insight, ok := evidence.(events.InvestigateDataInsight)
	if !ok {
		return t.marshalResponse(evidence)
	}

	dataMap := t.toMap(insight.Data)
	if dataMap == nil {
		return t.marshalResponse(evidence)
	}

	spans, _ := dataMap["data"].([]any)
	if len(spans) == 0 {
		return t.marshalResponse(map[string]any{"total_spans": 0})
	}

	serviceNames := map[string]bool{}
	var errorSpans []map[string]any
	var slowSpans []map[string]any
	var durations []float64

	for _, s := range spans {
		span, ok := s.(map[string]any)
		if !ok {
			continue
		}

		if svc, ok := span["ServiceName"].(string); ok {
			serviceNames[svc] = true
		}

		duration, _ := span["Duration"].(float64)
		durations = append(durations, duration)

		statusCode, _ := span["StatusCode"].(string)
		if strings.Contains(statusCode, "ERROR") {
			errorSpans = append(errorSpans, map[string]any{
				"span_name":    span["SpanName"],
				"service_name": span["ServiceName"],
				"duration_ms":  math.Round(duration/1_000_000*100) / 100,
				"status_code":  statusCode,
			})
		}

		// Slow spans: > 1 second (1_000_000_000 ns)
		if duration > slowSpanThresholdNs {
			slowSpans = append(slowSpans, map[string]any{
				"span_name":    span["SpanName"],
				"service_name": span["ServiceName"],
				"duration_ms":  math.Round(duration/1_000_000*100) / 100,
			})
		}
	}

	services := make([]string, 0, len(serviceNames))
	for svc := range serviceNames {
		services = append(services, svc)
	}

	summary := map[string]any{
		"total_spans": len(spans),
		"services":    services,
		"error_spans": len(errorSpans),
		"slow_spans":  len(slowSpans),
		"duration_stats": map[string]any{
			"min_ms": math.Round(minFloat(durations)/1_000_000*100) / 100,
			"max_ms": math.Round(maxFloat(durations)/1_000_000*100) / 100,
			"avg_ms": math.Round(avgFloat(durations)/1_000_000*100) / 100,
			"p95_ms": math.Round(p95Float(durations)/1_000_000*100) / 100,
		},
	}
	if len(errorSpans) > 0 {
		summary["error_span_details"] = errorSpans
	}
	if len(slowSpans) > 0 {
		summary["slow_span_details"] = slowSpans
	}

	return t.marshalResponse(summary)
}

// summarizeLogs summarizes map[string]any{"log_data": string, "error_log_data": []string}.
// Uses logparser.ExtractPatterns (Drain3 clustering) for pattern detection and
// logparser.GuessLevel for log level breakdown.
func (t GetEventEvidenceTool) summarizeLogs(evidence any) (core.NBToolResponse, error) {
	logMap, ok := evidence.(map[string]any)
	if !ok {
		return t.marshalResponse(evidence)
	}

	var allLines []string
	if logData, ok := logMap["log_data"].(string); ok && logData != "" {
		allLines = strings.Split(logData, "\n")
	}

	var errorLines []string
	if errData, ok := logMap["error_log_data"].([]string); ok {
		errorLines = errData
	} else if errDataAny, ok := logMap["error_log_data"].([]any); ok {
		for _, e := range errDataAny {
			if s, ok := e.(string); ok {
				errorLines = append(errorLines, s)
			}
		}
	}

	// If no log_data but we have error lines, use error lines for level breakdown and total count
	linesToAnalyze := allLines
	if len(linesToAnalyze) == 0 {
		linesToAnalyze = errorLines
	}

	// Level breakdown using logparser
	levelCounts := map[string]int{}
	for _, line := range linesToAnalyze {
		level := logparser.GuessLevel(line)
		if level != logparser.LevelUnknown {
			levelCounts[level.String()]++
		}
	}

	// Extract top error patterns using Drain3 clustering
	var topPatterns []map[string]any
	if len(errorLines) > 0 {
		extracted := logparser.ExtractPatterns(errorLines, topLogErrorPatternCount)
		for _, p := range extracted {
			topPatterns = append(topPatterns, map[string]any{
				"template":   p.Template,
				"count":      p.Count,
				"percentage": p.Percentage,
				"example":    p.Example,
			})
		}
	}

	summary := map[string]any{
		"total_lines":        len(linesToAnalyze),
		"error_lines":        len(errorLines),
		"top_error_patterns": topPatterns,
	}
	if len(allLines) == 0 && len(errorLines) > 0 {
		summary["source"] = "error_log_data"
	}
	if len(levelCounts) > 0 {
		summary["level_breakdown"] = levelCounts
	}

	return t.marshalResponse(summary)
}

// filterEvidence routes to type-specific filter implementations.
func (t GetEventEvidenceTool) filterEvidence(evidence any, evidenceType, pattern string, offset, limit int) (core.NBToolResponse, error) {
	switch evidenceType {
	case "logs":
		return t.filterLogs(evidence, pattern, offset, limit)
	default:
		return t.paginateArray(evidence, offset, limit)
	}
}

// filterLogs filters log lines by pattern with context, or paginates all lines.
func (t GetEventEvidenceTool) filterLogs(evidence any, pattern string, offset, limit int) (core.NBToolResponse, error) {
	logMap, ok := evidence.(map[string]any)
	if !ok {
		return t.marshalResponse(evidence)
	}

	var lines []string
	if logData, ok := logMap["log_data"].(string); ok && logData != "" {
		lines = strings.Split(logData, "\n")
	}
	// Fall back to error_log_data if log_data is empty
	if len(lines) == 0 {
		if errData, ok := logMap["error_log_data"].([]string); ok {
			lines = errData
		} else if errDataAny, ok := logMap["error_log_data"].([]any); ok {
			for _, e := range errDataAny {
				if s, ok := e.(string); ok {
					lines = append(lines, s)
				}
			}
		}
	}

	if pattern != "" {
		// Case-insensitive substring match with ±2 context lines
		lowerPattern := strings.ToLower(pattern)
		matchIndices := []int{}
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), lowerPattern) {
				matchIndices = append(matchIndices, i)
			}
		}

		// Collect matched lines with context, dedup overlapping ranges
		included := map[int]bool{}
		for _, idx := range matchIndices {
			for i := max(0, idx-logFilterContextLines); i <= min(len(lines)-1, idx+logFilterContextLines); i++ {
				included[i] = true
			}
		}

		var resultLines []string
		sortedIndices := make([]int, 0, len(included))
		for i := range included {
			sortedIndices = append(sortedIndices, i)
		}
		sort.Ints(sortedIndices)

		// Apply offset/limit on matched results
		totalContextLines := len(sortedIndices)
		if offset > totalContextLines {
			offset = totalContextLines
		}
		sortedIndices = sortedIndices[offset:]
		if limit > 0 && limit < len(sortedIndices) {
			sortedIndices = sortedIndices[:limit]
		}

		for _, i := range sortedIndices {
			resultLines = append(resultLines, lines[i])
		}

		return t.marshalResponse(map[string]any{
			"lines":         resultLines,
			"total_matches": len(matchIndices),
			"total_lines":   len(lines),
			"offset":        offset,
			"has_more":      offset+len(resultLines) < totalContextLines,
		})
	}

	// No pattern — paginate all lines
	totalLines := len(lines)
	if offset > totalLines {
		offset = totalLines
	}
	end := offset + limit
	if end > totalLines {
		end = totalLines
	}
	pageLines := lines[offset:end]

	return t.marshalResponse(map[string]any{
		"lines":       pageLines,
		"total_lines": totalLines,
		"offset":      offset,
		"has_more":    end < totalLines,
	})
}

// paginateArray applies offset/limit to array-type evidence.
func (t GetEventEvidenceTool) paginateArray(evidence any, offset, limit int) (core.NBToolResponse, error) {
	// Handle slice types ([]InvestigateDataInsight)
	if insights, ok := evidence.([]events.InvestigateDataInsight); ok {
		total := len(insights)
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		return t.marshalResponse(map[string]any{
			"data":     insights[offset:end],
			"total":    total,
			"offset":   offset,
			"has_more": end < total,
		})
	}

	// Handle single InvestigateDataInsight with nested data array
	if insight, ok := evidence.(events.InvestigateDataInsight); ok {
		dataMap := t.toMap(insight.Data)
		if dataMap != nil {
			if dataArr, ok := dataMap["data"].([]any); ok {
				total := len(dataArr)
				if offset > total {
					offset = total
				}
				end := offset + limit
				if end > total {
					end = total
				}
				resultMap := map[string]any{
					"data":     dataArr[offset:end],
					"total":    total,
					"offset":   offset,
					"has_more": end < total,
				}
				// Preserve other fields from dataMap
				for k, v := range dataMap {
					if k != "data" {
						resultMap[k] = v
					}
				}
				return t.marshalResponse(resultMap)
			}
		}
	}

	// Fallback: return as-is
	return t.marshalResponse(evidence)
}

// toMap converts Data field to map[string]any, handling both direct map and JSON string cases.
func (t GetEventEvidenceTool) toMap(data any) map[string]any {
	if m, ok := data.(map[string]any); ok {
		return m
	}
	if s, ok := data.(string); ok {
		var m map[string]any
		if err := common.UnmarshalJson([]byte(s), &m); err == nil {
			return m
		}
	}
	return nil
}

// marshalResponse is a helper to marshal any value into a JSON tool response.
func (t GetEventEvidenceTool) marshalResponse(v any) (core.NBToolResponse, error) {
	result, err := common.MarshalJson(v)
	if err != nil {
		return core.NBToolResponse{
			Data:   fmt.Sprintf("Error marshaling response: %v", err),
			Status: core.NBToolResponseStatusError,
		}, nil
	}
	return core.NBToolResponse{
		Data: string(result),
		Type: core.NBToolResponseTypeJson,
	}, nil
}

func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	default:
		return math.NaN()
	}
}

func minFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func avgFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return math.Round(sum/float64(len(vals))*100) / 100
}

func p95Float(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
