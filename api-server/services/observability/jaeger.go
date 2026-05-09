package observability

import (
	"fmt"
	"log/slog"
	"math"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// JaegerTraceSource - Agent mode (relay to K8s agent)
type JaegerTraceSource struct{}

// JaegerSaasTraceSource - SaaS mode (direct API call)
type JaegerSaasTraceSource struct{}

// jaegerMetricKey identifies a unique (service, operation) combination from Jaeger metrics
type jaegerMetricKey struct {
	ServiceName string
	Operation   string
}

// jaegerMetricValues holds the RED metric values for a (service, operation) pair
type jaegerMetricValues struct {
	Calls      float64
	Errors     float64
	P95Latency float64 // milliseconds from Jaeger
	P99Latency float64 // milliseconds from Jaeger
}

// GetLabelMapping returns the mapping of frontend column names to Jaeger field names
func (j *JaegerTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (j *JaegerTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in"}
}

// GetLabelMapping returns the mapping for SaaS mode
func (j *JaegerSaasTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (j *JaegerSaasTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in"}
}

// extractFirstValueFromBinaryFilter extracts the first string value from a WHERE clause binary filter
// Supports both _eq (single value) and _in (array) operators
func extractFirstValueFromBinaryFilter(binary query.BinaryWhereClause, fields ...string) string {
	if binary == nil {
		return ""
	}
	for _, field := range fields {
		filter, exists := binary[field]
		if !exists || filter == nil {
			continue
		}
		// Check for _eq (single value)
		if eqVal, ok := filter[query.Eq]; ok {
			if strVal, ok := eqVal.(string); ok && strVal != "" {
				return strVal
			}
		}
		// Check for _in (array of values OR single string) - use the first one
		if inVal, ok := filter[query.In]; ok {
			switch v := inVal.(type) {
			case string:
				// Handle case where _in receives a single string instead of array
				if v != "" {
					return v
				}
			case []string:
				if len(v) > 0 {
					return v[0]
				}
			case []any:
				if len(v) > 0 {
					if strVal, ok := v[0].(string); ok {
						return strVal
					}
				}
			}
		}
	}
	return ""
}

// CountTraces returns -1 as Jaeger doesn't support efficient counting
func (j *JaegerTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

// CountTraces returns -1 for SaaS mode
func (j *JaegerSaasTraceSource) CountTraces(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

// GetLabelValues returns label values for filter dropdowns
// Enhanced to support more labels with static values where Jaeger API doesn't provide them
func (j *JaegerTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	// Check account access
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
		return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}, fmt.Errorf("access denied for account: %s", req.AccountId)
	}

	switch req.Label {
	case "workload_name", "service_name":
		// Fetch actual services from Jaeger
		services, err := j.getServices(ctx, req.AccountId)
		if err != nil {
			ctx.GetLogger().Warn("Failed to get services from Jaeger", "error", err)
			return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}, nil
		}
		return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: services}, nil

	case "span_name":
		// Fetch operations for selected service (if available in query)
		service := j.extractServiceFromLabelRequest(req)
		if service != "" {
			operations, err := j.getOperations(ctx, req.AccountId, service)
			if err != nil {
				ctx.GetLogger().Warn("Failed to get operations from Jaeger", "service", service, "error", err)
			} else if len(operations) > 0 {
				return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: operations}, nil
			}
		}
		// Fallback: Jaeger requires service to list operations
		return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}, nil

	case "http_status_code":
		// Return common HTTP status codes
		return common.OpenTelemetryTraceLabelValues{
			Label:  req.Label,
			Values: []string{"200", "201", "204", "301", "302", "400", "401", "403", "404", "500", "502", "503", "504"},
		}, nil

	case "status_code":
		// Return OTEL status codes
		return common.OpenTelemetryTraceLabelValues{
			Label:  req.Label,
			Values: []string{"STATUS_CODE_OK", "STATUS_CODE_ERROR", "STATUS_CODE_UNSET"},
		}, nil

	default:
		// Other labels not supported by Jaeger API
		return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}, nil
	}
}

// extractServiceFromLabelRequest extracts service name from the label values request query
func (j *JaegerTraceSource) extractServiceFromLabelRequest(req TracesV3LabelValuesRequest) string {
	return extractFirstValueFromBinaryFilter(req.QueryRequest.Where.Binary, "workload_name", "service_name")
}

// getServices fetches the list of services from Jaeger via relay
func (j *JaegerTraceSource) getServices(ctx *security.RequestContext, accountId string) ([]string, error) {
	jaegerRequest := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "jaeger_query_services",
		ActionParams: map[string]any{},
		NoSinks:      true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    jaegerRequest,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute jaeger_query_services via relay: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		ctx.GetLogger().Error("Failed to get Jaeger services", "resp", respData)
		return nil, fmt.Errorf("failed to get Jaeger services, status code: %v", respData["status_code"])
	}

	return j.parseStringArrayFromRelayResponse(respData)
}

// parseStringArrayFromRelayResponse extracts string array from Jaeger API response
// Jaeger returns {"data": ["item1", "item2", ...]} structure
func (j *JaegerTraceSource) parseStringArrayFromRelayResponse(respData map[string]any) ([]string, error) {
	var result []string

	data, ok := respData["data"].(map[string]any)
	if !ok {
		data = respData
	}

	var itemsData []any
	if d, ok := data["data"].([]any); ok {
		itemsData = d
	} else if innerData, ok := data["data"].(map[string]any); ok {
		if d, ok := innerData["data"].([]any); ok {
			itemsData = d
		}
	}

	if itemsData == nil {
		return result, nil
	}

	for _, item := range itemsData {
		if itemStr, ok := item.(string); ok {
			result = append(result, itemStr)
		}
	}

	return result, nil
}

// getOperations fetches the list of operations for a service from Jaeger via relay
func (j *JaegerTraceSource) getOperations(ctx *security.RequestContext, accountId, service string) ([]string, error) {
	jaegerRequest := relay.ActionExecuteBody{
		AccountID:  accountId,
		ActionName: "jaeger_query_operations",
		ActionParams: map[string]any{
			"service": service,
		},
		NoSinks: true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    jaegerRequest,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute jaeger_query_operations via relay: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		ctx.GetLogger().Warn("Failed to get Jaeger operations", "resp", respData)
		return nil, fmt.Errorf("failed to get Jaeger operations, status code: %v", respData["status_code"])
	}

	return j.parseStringArrayFromRelayResponse(respData)
}

// GetLabelValues returns label values for SaaS mode
func (j *JaegerSaasTraceSource) GetLabelValues(ctx *security.RequestContext, req TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	// SaaS mode not yet implemented - return empty values
	return common.OpenTelemetryTraceLabelValues{Label: req.Label, Values: []string{}}, nil
}

// GetQuery returns the query string representation
func (j *JaegerTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	return "", fmt.Errorf("Jaeger.GetQuery not implemented")
}

// GetQuery returns query string for SaaS mode
func (j *JaegerSaasTraceSource) GetQuery(ctx *security.RequestContext, req TracesV3Request) (string, error) {
	return "", fmt.Errorf("JaegerSaas.GetQuery not implemented")
}

// QueryGroupedTraces returns RED metrics grouped by (service, operation) via Jaeger Metrics API (SPM)
func (j *JaegerTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("access denied for account: %s", req.AccountId)
	}

	// Extract services from filter or fetch all
	services := j.extractServicesFromQuery(req)
	if len(services) == 0 {
		fetchedServices, err := j.getServices(ctx, req.AccountId)
		if err != nil {
			return nil, fmt.Errorf("failed to get services for metrics query: %w", err)
		}
		services = fetchedServices
	}
	if len(services) == 0 {
		return []TraceGroupingValues{}, nil
	}

	respData, err := j.queryMetrics(ctx, req.AccountId, services, req.StartTime, req.EndTime)
	if err != nil {
		return nil, err
	}

	parsed, err := j.parseMetricsResponse(respData)
	if err != nil {
		return nil, err
	}

	results := j.metricsToGroupingValues(parsed)

	// Sort
	j.sortGroupedTraces(results, req.QueryRequest.OrderBy)

	// Paginate
	offset := req.QueryRequest.Offset
	limit := req.QueryRequest.Limit
	if limit <= 0 {
		limit = 100
	}
	if offset > len(results) {
		return []TraceGroupingValues{}, nil
	}
	end := offset + limit
	if end > len(results) {
		end = len(results)
	}

	return results[offset:end], nil
}

// QueryGroupedTraces for SaaS mode
func (j *JaegerSaasTraceSource) QueryGroupedTraces(ctx *security.RequestContext, req TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("grouped traces not yet available for Jaeger SaaS mode")
}

// QueryGroupedTracesCount returns count of grouped traces via Jaeger Metrics API (SPM)
func (j *JaegerTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
		return common.OpenTelemetryTraceGroupCount{Count: -1}, fmt.Errorf("access denied for account: %s", req.AccountId)
	}

	services := j.extractServicesFromQuery(req)
	if len(services) == 0 {
		fetchedServices, err := j.getServices(ctx, req.AccountId)
		if err != nil {
			return common.OpenTelemetryTraceGroupCount{Count: -1}, fmt.Errorf("failed to get services for metrics count: %w", err)
		}
		services = fetchedServices
	}
	if len(services) == 0 {
		return common.OpenTelemetryTraceGroupCount{Count: 0}, nil
	}

	respData, err := j.queryMetrics(ctx, req.AccountId, services, req.StartTime, req.EndTime)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{Count: -1}, err
	}

	parsed, err := j.parseMetricsResponse(respData)
	if err != nil {
		return common.OpenTelemetryTraceGroupCount{Count: -1}, err
	}

	return common.OpenTelemetryTraceGroupCount{Count: len(parsed)}, nil
}

// QueryGroupedTracesCount for SaaS mode
func (j *JaegerSaasTraceSource) QueryGroupedTracesCount(ctx *security.RequestContext, req TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{Count: -1}, fmt.Errorf("grouped traces not yet available for Jaeger SaaS mode")
}

// extractServicesFromQuery extracts all service names from the query WHERE clause
func (j *JaegerTraceSource) extractServicesFromQuery(req TracesV3Request) []string {
	return extractAllValuesFromBinaryFilter(req.QueryRequest.Where.Binary, "workload_name", "service_name")
}

// extractAllValuesFromBinaryFilter extracts all string values from a WHERE clause binary filter
func extractAllValuesFromBinaryFilter(binary query.BinaryWhereClause, fields ...string) []string {
	if binary == nil {
		return nil
	}
	var results []string
	for _, field := range fields {
		filter, exists := binary[field]
		if !exists || filter == nil {
			continue
		}
		if eqVal, ok := filter[query.Eq]; ok {
			if strVal, ok := eqVal.(string); ok && strVal != "" {
				results = append(results, strVal)
			}
		}
		if inVal, ok := filter[query.In]; ok {
			switch v := inVal.(type) {
			case string:
				if v != "" {
					results = append(results, v)
				}
			case []string:
				results = append(results, v...)
			case []any:
				for _, item := range v {
					if strVal, ok := item.(string); ok && strVal != "" {
						results = append(results, strVal)
					}
				}
			}
		}
		if len(results) > 0 {
			return results
		}
	}
	return results
}

// queryMetrics calls the agent relay to fetch Jaeger Metrics API data
func (j *JaegerTraceSource) queryMetrics(ctx *security.RequestContext, accountId string,
	services []string, startTime, endTime int64) (map[string]any, error) {

	// startTime/endTime are in milliseconds
	lookback := endTime - startTime
	if lookback <= 0 {
		lookback = 3600000 // default 1 hour
	}
	step := lookback
	ratePer := lookback

	jaegerRequest := relay.ActionExecuteBody{
		AccountID:  accountId,
		ActionName: "jaeger_query_metrics",
		ActionParams: map[string]any{
			"services":         services,
			"groupByOperation": true,
			"endTs":            endTime,
			"lookback":         lookback,
			"step":             step,
			"ratePer":          ratePer,
			"spanKinds":        []string{"server"},
		},
		NoSinks: true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    jaegerRequest,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute jaeger_query_metrics via relay: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		// Check if the error message indicates SPM not configured
		if data, ok := respData["data"].(map[string]any); ok {
			if errMsg, ok := data["error_message"].(string); ok && strings.Contains(errMsg, "SPM") {
				return nil, fmt.Errorf("%s", errMsg)
			}
		}
		return nil, fmt.Errorf("jaeger Metrics API (SPM) not available, ensure Jaeger is configured with METRICS_STORAGE_TYPE=prometheus")
	}

	return respData, nil
}

// parseMetricsResponse parses the combined metrics response from the relay into a map of metric values
func (j *JaegerTraceSource) parseMetricsResponse(respData map[string]any) (map[jaegerMetricKey]*jaegerMetricValues, error) {
	result := make(map[jaegerMetricKey]*jaegerMetricValues)

	data, ok := respData["data"].(map[string]any)
	if !ok {
		data = respData
	}

	// The relay wraps the response: {data: {calls: ..., errors: ..., latencies_p95: ..., latencies_p99: ...}}
	metricsData, ok := data["data"].(map[string]any)
	if !ok {
		// Try one more level of nesting
		if innerData, ok := data["data"].(map[string]any); ok {
			if md, ok := innerData["data"].(map[string]any); ok {
				metricsData = md
			}
		}
		if metricsData == nil {
			return result, nil
		}
	}

	metricTypes := map[string]string{
		"calls":         "calls",
		"errors":        "errors",
		"latencies_p95": "p95",
		"latencies_p99": "p99",
	}

	for metricName, metricType := range metricTypes {
		metricsFamily, ok := metricsData[metricName].(map[string]any)
		if !ok {
			continue
		}

		metrics, ok := metricsFamily["metrics"].([]any)
		if !ok {
			continue
		}

		for _, metricEntry := range metrics {
			entry, ok := metricEntry.(map[string]any)
			if !ok {
				continue
			}

			// Extract labels
			serviceName := ""
			operation := ""
			if labels, ok := entry["labels"].([]any); ok {
				for _, label := range labels {
					labelMap, ok := label.(map[string]any)
					if !ok {
						continue
					}
					name, _ := labelMap["name"].(string)
					value, _ := labelMap["value"].(string)
					switch name {
					case "service_name":
						serviceName = value
					case "operation":
						operation = value
					}
				}
			}

			if serviceName == "" {
				continue
			}

			// Extract latest metric point value
			value := 0.0
			if metricPoints, ok := entry["metricPoints"].([]any); ok && len(metricPoints) > 0 {
				// Use last point (most recent)
				lastPoint, ok := metricPoints[len(metricPoints)-1].(map[string]any)
				if ok {
					if gaugeValue, ok := lastPoint["gaugeValue"].(map[string]any); ok {
						if dv, ok := gaugeValue["doubleValue"].(float64); ok {
							value = dv
						}
					}
				}
			}

			key := jaegerMetricKey{ServiceName: serviceName, Operation: operation}
			if _, exists := result[key]; !exists {
				result[key] = &jaegerMetricValues{}
			}

			switch metricType {
			case "calls":
				result[key].Calls = value
			case "errors":
				result[key].Errors = value
			case "p95":
				result[key].P95Latency = value
			case "p99":
				result[key].P99Latency = value
			}
		}
	}

	return result, nil
}

// metricsToGroupingValues converts parsed metrics to TraceGroupingValues slice
func (j *JaegerTraceSource) metricsToGroupingValues(metrics map[jaegerMetricKey]*jaegerMetricValues) []TraceGroupingValues {
	results := make([]TraceGroupingValues, 0, len(metrics))
	for key, values := range metrics {
		results = append(results, TraceGroupingValues{
			Count:                        int(math.Round(values.Calls)),
			ErrorCount:                   int(math.Round(values.Errors)),
			P95Latency:                   int64(values.P95Latency * 1_000_000), // ms → ns
			P99Latency:                   int64(values.P99Latency * 1_000_000), // ms → ns
			MaxLatency:                   int64(values.P99Latency * 1_000_000), // best approximation
			WorkloadName:                 key.ServiceName,
			SpanName:                     key.Operation,
			WorkloadNamespace:            "",
			DestinationWorkloadName:      "",
			DestinationWorkloadNamespace: "",
			Resource:                     "",
			HTTPStatusCode:               "",
		})
	}
	return results
}

// sortGroupedTraces sorts results by the first OrderBy column
func (j *JaegerTraceSource) sortGroupedTraces(results []TraceGroupingValues, orderBy []query.QueryOrderBy) {
	if len(orderBy) == 0 || len(results) == 0 {
		return
	}

	ob := orderBy[0]
	ascending := ob.Order == query.Asc || ob.Order == query.AscNullsFirst || ob.Order == query.AscNullsLast
	sort.Slice(results, func(i, j int) bool {
		var less bool
		switch ob.Column {
		case "count":
			less = results[i].Count < results[j].Count
		case "error_count":
			less = results[i].ErrorCount < results[j].ErrorCount
		case "p95_latency":
			less = results[i].P95Latency < results[j].P95Latency
		case "p99_latency":
			less = results[i].P99Latency < results[j].P99Latency
		case "max_latency":
			less = results[i].MaxLatency < results[j].MaxLatency
		default:
			less = results[i].Count < results[j].Count
		}
		if ascending {
			return less
		}
		return !less
	})
}

// sortTraces sorts individual traces, defaulting to timestamp descending (most recent first)
func (j *JaegerTraceSource) sortTraces(traces []common.OpenTelemetryTrace, orderBy []query.QueryOrderBy) {
	if len(traces) == 0 {
		return
	}

	// Default: sort by timestamp descending (most recent first)
	column := "timestamp"
	ascending := false

	if len(orderBy) > 0 {
		column = orderBy[0].Column
		ascending = orderBy[0].Order == query.Asc || orderBy[0].Order == query.AscNullsFirst || orderBy[0].Order == query.AscNullsLast
	}

	sort.Slice(traces, func(i, k int) bool {
		var less bool
		switch column {
		case "duration_ns":
			less = traces[i].DurationNs < traces[k].DurationNs
		default:
			// Sort by timestamp (RFC3339Nano string comparison works for chronological ordering)
			less = traces[i].Timestamp < traces[k].Timestamp
		}
		if ascending {
			return less
		}
		return !less
	})
}

// QueryTraces fetches traces from Jaeger via the K8s agent relay
func (j *JaegerTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	// Check account access
	if !ctx.GetSecurityContext().HasAccountAccess(req.AccountId, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("access denied for account: %s", req.AccountId)
	}

	if req.Query != "" {
		return nil, fmt.Errorf("custom query not supported for Jaeger trace source")
	}

	// Check for trace_id filter - use dedicated endpoint for better performance
	traceId := j.extractTraceIdFromQuery(req)
	if traceId != "" {
		ctx.GetLogger().Info("Querying Jaeger by trace_id", "trace_id", traceId)
		return j.queryTraceById(ctx, req.AccountId, traceId)
	}

	service, _ := req.Request["service"].(string)

	// If no service in request, check workload_name in query WHERE clause
	if service == "" {
		service = j.extractServiceFromQuery(req)
	}

	// If service found, set it in the request for buildJaegerParams to use
	if service != "" {
		reqCopy := req
		newRequestMap := make(map[string]any, len(req.Request)+1)
		if req.Request != nil {
			for k, v := range req.Request {
				newRequestMap[k] = v
			}
		}
		newRequestMap["service"] = service
		reqCopy.Request = newRequestMap
		return j.queryTracesForService(ctx, reqCopy)
	}

	// If no service specified, fetch traces for first available service
	return j.queryTracesForFirstService(ctx, req)
}

// extractTraceIdFromQuery extracts trace_id from the query WHERE clause
func (j *JaegerTraceSource) extractTraceIdFromQuery(req TracesV3Request) string {
	return extractFirstValueFromBinaryFilter(req.QueryRequest.Where.Binary, "trace_id")
}

// queryTraceById fetches a specific trace by ID using dedicated Jaeger endpoint
func (j *JaegerTraceSource) queryTraceById(ctx *security.RequestContext, accountId, traceId string) ([]common.OpenTelemetryTrace, error) {
	jaegerRequest := relay.ActionExecuteBody{
		AccountID:  accountId,
		ActionName: "jaeger_query_trace_by_id",
		ActionParams: map[string]any{
			"trace_id": traceId,
		},
		NoSinks: true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    jaegerRequest,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute jaeger_query_trace_by_id: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		ctx.GetLogger().Error("Failed to get Jaeger trace by ID", "resp", respData)
		return nil, fmt.Errorf("failed to get Jaeger trace by ID, status: %v", respData["status_code"])
	}

	return j.transformJaegerResponse(ctx, respData)
}

// extractServiceFromQuery extracts service name from workload_name, destination_workload_name,
// or service_name in the query WHERE clause. Also checks OR/AND sub-clauses for
// workload-based queries built by autoExecuteByWorkload.
func (j *JaegerTraceSource) extractServiceFromQuery(req TracesV3Request) string {
	fields := []string{"workload_name", "destination_workload_name", "service_name"}
	// Check top-level Binary first
	if v := extractFirstValueFromBinaryFilter(req.QueryRequest.Where.Binary, fields...); v != "" {
		return v
	}
	// Check OR sub-clauses (autoExecuteByWorkload builds OR conditions)
	for _, orClause := range req.QueryRequest.Where.Or {
		if v := extractFirstValueFromBinaryFilter(orClause.Binary, fields...); v != "" {
			return v
		}
	}
	// Check AND sub-clauses
	for _, andClause := range req.QueryRequest.Where.And {
		if v := extractFirstValueFromBinaryFilter(andClause.Binary, fields...); v != "" {
			return v
		}
	}
	return ""
}

// queryTracesForAServices fetches services first, then queries traces for each
func (j *JaegerTraceSource) queryTracesForFirstService(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	services, err := j.getServices(ctx, req.AccountId)
	if err != nil {
		ctx.GetLogger().Warn("Failed to get services from Jaeger, returning empty traces", "error", err)
		return []common.OpenTelemetryTrace{}, nil
	}

	if len(services) == 0 {
		ctx.GetLogger().Info("No services found in Jaeger")
		return []common.OpenTelemetryTrace{}, nil
	}

	// Save original offset/limit for pagination slicing
	originalOffset := req.QueryRequest.Offset
	originalLimit := req.QueryRequest.Limit
	if originalLimit <= 0 {
		originalLimit = 100
	}

	// Override offset/limit so queryTracesForService fetches enough results
	req.QueryRequest.Offset = 0
	req.QueryRequest.Limit = originalOffset + originalLimit

	// Limit to first N services to prevent overload
	maxServices := 1
	if len(services) < maxServices {
		maxServices = len(services)
	}

	var allTraces []common.OpenTelemetryTrace
	for i := 0; i < maxServices; i++ {
		reqCopy := req
		if reqCopy.Request == nil {
			reqCopy.Request = make(map[string]any)
		}
		reqCopy.Request["service"] = services[i]

		traces, err := j.queryTracesForService(ctx, reqCopy)
		if err != nil {
			ctx.GetLogger().Warn("Failed to query traces for service", "service", services[i], "error", err)
			continue
		}
		allTraces = append(allTraces, traces...)
	}

	// Sort combined traces by timestamp descending (most recent first)
	j.sortTraces(allTraces, req.QueryRequest.OrderBy)

	// Apply pagination: slice by [originalOffset : originalOffset+originalLimit]
	if originalOffset > len(allTraces) {
		return []common.OpenTelemetryTrace{}, nil
	}
	end := originalOffset + originalLimit
	if end > len(allTraces) {
		end = len(allTraces)
	}
	allTraces = allTraces[originalOffset:end]

	return allTraces, nil
}

// queryTracesForService queries traces for a specific service
func (j *JaegerTraceSource) queryTracesForService(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	queryRequest := getQueryRequest(ctx, req.QueryRequest, query.TableDefinition{}, "traces_v2")

	// Save original offset/limit for pagination slicing after post-filtering
	originalOffset := queryRequest.Offset
	originalLimit := queryRequest.Limit
	if originalLimit <= 0 {
		originalLimit = 100
	}

	// Jaeger API doesn't support offset, so fetch enough results to cover the requested page
	queryRequest.Offset = 0
	queryRequest.Limit = originalOffset + originalLimit

	jaegerParams := j.buildJaegerParams(ctx, queryRequest, req)
	ctx.GetLogger().Info("Built Jaeger params", "params", jaegerParams)
	jaegerRequest := relay.ActionExecuteBody{
		AccountID:    req.AccountId,
		ActionName:   "jaeger_query_traces",
		ActionParams: jaegerParams,
		NoSinks:      true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    jaegerRequest,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute Jaeger query via relay: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		ctx.GetLogger().Error("Failed to get Jaeger traces", "resp", respData)
		return nil, fmt.Errorf("failed to get Jaeger traces, status code: %v", respData["status_code"])
	}

	traces, err := j.transformJaegerResponse(ctx, respData)
	if err != nil {
		return nil, err
	}

	// Apply post-filters for conditions Jaeger can't handle natively
	traces = j.applyPostFilters(ctx, traces, req)

	// Sort by timestamp descending (most recent first) by default
	j.sortTraces(traces, req.QueryRequest.OrderBy)

	// Apply pagination: slice by [originalOffset : originalOffset+originalLimit]
	if originalOffset > len(traces) {
		return []common.OpenTelemetryTrace{}, nil
	}
	end := originalOffset + originalLimit
	if end > len(traces) {
		end = len(traces)
	}
	traces = traces[originalOffset:end]

	return traces, nil
}

// applyPostFilters filters traces for conditions that Jaeger API can't handle natively
// This includes: status_code non-error, resource LIKE patterns, http_status_code _in arrays
func (j *JaegerTraceSource) applyPostFilters(ctx *security.RequestContext, traces []common.OpenTelemetryTrace, req TracesV3Request) []common.OpenTelemetryTrace {
	if req.QueryRequest.Where.Binary == nil && len(req.QueryRequest.Where.And) == 0 {
		return traces
	}

	filtered := make([]common.OpenTelemetryTrace, 0, len(traces))
	for _, trace := range traces {
		if j.traceMatchesFilters(trace, req.QueryRequest.Where.Binary, req.QueryRequest.Where.And) {
			filtered = append(filtered, trace)
		}
	}

	if len(filtered) != len(traces) {
		ctx.GetLogger().Debug("Post-filtered Jaeger traces", "before", len(traces), "after", len(filtered))
	}

	return filtered
}

// traceMatchesFilters checks if a trace matches all the filter conditions
func (j *JaegerTraceSource) traceMatchesFilters(trace common.OpenTelemetryTrace, binary query.BinaryWhereClause, andClauses []query.QueryWhereClause) bool {
	// Check binary filters
	if binary != nil {
		if !j.traceMatchesBinaryFilters(trace, binary) {
			return false
		}
	}

	// Check AND clause filters
	for _, andClause := range andClauses {
		if andClause.Binary != nil {
			if !j.traceMatchesBinaryFilters(trace, andClause.Binary) {
				return false
			}
		}
	}

	return true
}

// traceMatchesBinaryFilters checks if a trace matches binary filter conditions
func (j *JaegerTraceSource) traceMatchesBinaryFilters(trace common.OpenTelemetryTrace, binary query.BinaryWhereClause) bool {
	// Check status_code filter (non-error status can't be filtered by Jaeger)
	if statusFilter, exists := binary["status_code"]; exists {
		if eqVal, ok := statusFilter[query.Eq]; ok {
			expectedStatus := fmt.Sprintf("%v", eqVal)
			if trace.StatusCode != expectedStatus {
				return false
			}
		}
	}

	// Check resource LIKE filter (Jaeger tags are exact match, we need pattern matching)
	if resourceFilter, exists := binary["resource"]; exists {
		if likeVal, ok := resourceFilter[query.Like]; ok {
			pattern := strings.Trim(fmt.Sprintf("%v", likeVal), "%")
			if pattern != "" && !strings.Contains(trace.Resource, pattern) {
				return false
			}
		}
	}

	// Check http_status_code _in filter (Jaeger only supports single tag value)
	if httpFilter, exists := binary["http_status_code"]; exists {
		if inVal, ok := httpFilter[query.In]; ok {
			found := false
			hasValues := false
			switch v := inVal.(type) {
			case string:
				hasValues = v != ""
				if v == trace.HTTPStatusCode {
					found = true
				}
			case []string:
				hasValues = len(v) > 0
				for _, val := range v {
					if val == trace.HTTPStatusCode {
						found = true
						break
					}
				}
			case []any:
				hasValues = len(v) > 0
				for _, val := range v {
					if fmt.Sprintf("%v", val) == trace.HTTPStatusCode {
						found = true
						break
					}
				}
			}
			if !found && hasValues {
				return false
			}
		}
	}

	// Check span_name filter
	if spanFilter, exists := binary["span_name"]; exists {
		if eqVal, ok := spanFilter[query.Eq]; ok {
			expectedSpan := fmt.Sprintf("%v", eqVal)
			if trace.SpanName != expectedSpan {
				return false
			}
		}
	}

	// Check destination_workload_name filter
	if destFilter, exists := binary["destination_workload_name"]; exists {
		if !matchStringFilter(trace.DestinationWorkload, destFilter) {
			return false
		}
	}

	// Check destination_workload_namespace filter
	if destNsFilter, exists := binary["destination_workload_namespace"]; exists {
		if !matchStringFilter(trace.DestinationNamespace, destNsFilter) {
			return false
		}
	}

	// Check destination_name filter
	if destNameFilter, exists := binary["destination_name"]; exists {
		if !matchStringFilter(trace.DestinationName, destNameFilter) {
			return false
		}
	}

	return true
}

// matchStringFilter checks if a value matches _eq or _in operators from a filter clause.
func matchStringFilter(value string, filter map[query.BinaryWhereClauseType]any) bool {
	if eqVal, ok := filter[query.Eq]; ok {
		if fmt.Sprintf("%v", eqVal) != value {
			return false
		}
	}
	if inVal, ok := filter[query.In]; ok {
		found := false
		hasValues := false
		switch v := inVal.(type) {
		case string:
			hasValues = v != ""
			found = v == value
		case []string:
			hasValues = len(v) > 0
			for _, s := range v {
				if s == value {
					found = true
					break
				}
			}
		case []any:
			hasValues = len(v) > 0
			for _, s := range v {
				if fmt.Sprintf("%v", s) == value {
					found = true
					break
				}
			}
		}
		if !found && hasValues {
			return false
		}
	}
	return true
}

// QueryTraces for SaaS mode - direct API call to Jaeger
func (j *JaegerSaasTraceSource) QueryTraces(ctx *security.RequestContext, req TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	if req.Query != "" {
		return nil, fmt.Errorf("custom query not supported for Jaeger SaaS trace source")
	}
	return nil, fmt.Errorf("JaegerSaas.QueryTraces not implemented yet - use agent mode")
}

// QueryTracesHeatmap fetches trace heatmap data for a specific trace
func (j *JaegerTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	jaegerParams := map[string]any{
		"trace_id": req.TraceId,
	}

	jaegerRequest := relay.ActionExecuteBody{
		AccountID:    req.AccountId,
		ActionName:   "jaeger_query_trace_by_id",
		ActionParams: jaegerParams,
		NoSinks:      true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    jaegerRequest,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to execute Jaeger trace query via relay: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		ctx.GetLogger().Error("Failed to get Jaeger trace heatmap", "resp", respData)
		return nil, fmt.Errorf("failed to get Jaeger trace heatmap, status code: %v", respData["status_code"])
	}

	traces, err := j.transformJaegerResponse(ctx, respData)
	if err != nil {
		return nil, err
	}

	heatmapEntries := make([]common.OpenTelemetryTraceHeatMap, 0, len(traces))
	for _, trace := range traces {
		heatmapEntries = append(heatmapEntries, common.OpenTelemetryTraceHeatMap{
			TraceID:            trace.TraceID,
			Timestamp:          trace.Timestamp,
			ResourceAttributes: trace.ResourceAttributes,
			SpanName:           trace.SpanName,
			StatusCode:         trace.StatusCode,
			DurationNs:         trace.DurationNs,
			SpanAttributes:     trace.SpanAttributes,
			SpanID:             trace.SpanID,
			ServiceName:        trace.ServiceName,
			EventsAttributes:   trace.EventsAttributes,
			EventsName:         trace.EventsName,
		})
	}

	return heatmapEntries, nil
}

// QueryTracesHeatmap for SaaS mode
func (j *JaegerSaasTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, req TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	return nil, fmt.Errorf("JaegerSaas.QueryTracesHeatmap not implemented yet")
}

// buildJaegerParams converts the query request to Jaeger API parameters
// Enhanced to extract filters from QueryRequest.Where.Binary
func (j *JaegerTraceSource) buildJaegerParams(ctx *security.RequestContext, queryRequest query.QueryRequest, req TracesV3Request) map[string]any {
	params := make(map[string]any)
	tags := make(map[string]string)

	// Time range
	if req.StartTime > 0 {
		params["start"] = req.StartTime * 1000 // ms → µs
	}
	if req.EndTime > 0 {
		params["end"] = req.EndTime * 1000 // ms → µs
	}

	// Default limit from queryRequest or fallback to 100
	if queryRequest.Limit > 0 {
		params["limit"] = queryRequest.Limit
	} else {
		params["limit"] = 100
	}

	// Extract service from Request map (legacy)
	if service, ok := req.Request["service"].(string); ok && service != "" {
		params["service"] = service
	}

	// Process WHERE clause Binary filters - this is where frontend filters come from
	if queryRequest.Where.Binary != nil {
		j.extractFiltersFromBinary(ctx, queryRequest.Where.Binary, params, tags)
	}

	// Process AND clauses for nested conditions
	for _, andClause := range queryRequest.Where.And {
		if andClause.Binary != nil {
			j.extractFiltersFromBinary(ctx, andClause.Binary, params, tags)
		}
	}

	// Process OR clauses (e.g., workload-based queries from autoExecuteByWorkload)
	for _, orClause := range queryRequest.Where.Or {
		if orClause.Binary != nil {
			j.extractFiltersFromBinary(ctx, orClause.Binary, params, tags)
		}
	}

	// Merge legacy Request params for backward compatibility
	if operation, ok := req.Request["operation"].(string); ok && operation != "" {
		params["operation"] = operation
	}
	if minDuration, ok := req.Request["min_duration"].(string); ok && minDuration != "" {
		params["minDuration"] = minDuration
	}
	if maxDuration, ok := req.Request["max_duration"].(string); ok && maxDuration != "" {
		params["maxDuration"] = maxDuration
	}
	// Merge legacy tags from Request (these take precedence)
	if existingTags, ok := req.Request["tags"].(map[string]string); ok {
		for k, v := range existingTags {
			tags[k] = v
		}
	}

	// Set tags only once at the end if any were collected
	if len(tags) > 0 {
		params["tags"] = tags
	}

	ctx.GetLogger().Debug("Built Jaeger query params", "params", params)

	return params
}

// extractFiltersFromBinary extracts filters from WHERE clause Binary and converts to Jaeger params
func (j *JaegerTraceSource) extractFiltersFromBinary(ctx *security.RequestContext, binary query.BinaryWhereClause, params map[string]any, tags map[string]string) {
	// Handle span_name -> operation
	if spanFilter, exists := binary["span_name"]; exists {
		if eqVal, ok := spanFilter[query.Eq]; ok {
			if strVal, ok := eqVal.(string); ok && strVal != "" {
				params["operation"] = strVal
			}
		}
	}

	// Handle workload_name/service_name -> service (if not already set)
	for _, field := range []string{"workload_name", "service_name"} {
		if serviceFilter, exists := binary[field]; exists {
			if _, hasService := params["service"]; !hasService {
				if eqVal, ok := serviceFilter[query.Eq]; ok {
					if strVal, ok := eqVal.(string); ok && strVal != "" {
						params["service"] = strVal
					}
				}
				if inVal, ok := serviceFilter[query.In]; ok {
					switch v := inVal.(type) {
					case string:
						if v != "" {
							params["service"] = v
						}
					case []string:
						if len(v) > 0 {
							params["service"] = v[0]
						}
					case []any:
						if len(v) > 0 {
							if strVal, ok := v[0].(string); ok {
								params["service"] = strVal
							}
						}
					}
				}
			}
		}
	}

	// Handle duration_ns -> minDuration/maxDuration
	if durationFilter, exists := binary["duration_ns"]; exists {
		if gteVal, ok := durationFilter[query.Gte]; ok {
			if dur := j.convertNsToJaegerDuration(gteVal); dur != "" {
				params["minDuration"] = dur
			}
		}
		if lteVal, ok := durationFilter[query.Lte]; ok {
			if dur := j.convertNsToJaegerDuration(lteVal); dur != "" {
				params["maxDuration"] = dur
			}
		}
		if gtVal, ok := durationFilter[query.Gt]; ok {
			if dur := j.convertNsToJaegerDuration(gtVal); dur != "" {
				params["minDuration"] = dur
			}
		}
		if ltVal, ok := durationFilter[query.Lt]; ok {
			if dur := j.convertNsToJaegerDuration(ltVal); dur != "" {
				params["maxDuration"] = dur
			}
		}
	}

	// Handle http_status_code -> tags["http.status_code"]
	if httpStatusFilter, exists := binary["http_status_code"]; exists {
		if eqVal, ok := httpStatusFilter[query.Eq]; ok {
			tags["http.status_code"] = fmt.Sprintf("%v", eqVal)
		}
		// For _in, use first value (Jaeger tags don't support IN)
		if inVal, ok := httpStatusFilter[query.In]; ok {
			switch v := inVal.(type) {
			case string:
				if v != "" {
					tags["http.status_code"] = v
				}
			case []string:
				if len(v) > 0 {
					tags["http.status_code"] = v[0]
				}
			case []any:
				if len(v) > 0 {
					tags["http.status_code"] = fmt.Sprintf("%v", v[0])
				}
			}
		}
	}

	// Handle status_code -> tags["error"] for error filtering
	if statusFilter, exists := binary["status_code"]; exists {
		if eqVal, ok := statusFilter[query.Eq]; ok {
			strVal := fmt.Sprintf("%v", eqVal)
			if strVal == "STATUS_CODE_ERROR" {
				tags["error"] = "true"
			}
			// STATUS_CODE_OK and STATUS_CODE_UNSET can't be filtered by Jaeger tags
			// They will be handled in post-filtering
		}
	}

	// Handle resource -> tags["http.url"] or tags["http.target"]
	if resourceFilter, exists := binary["resource"]; exists {
		if likeVal, ok := resourceFilter[query.Like]; ok {
			// Jaeger doesn't support LIKE, extract core value
			strVal := strings.Trim(fmt.Sprintf("%v", likeVal), "%")
			if strVal != "" {
				tags["http.url"] = strVal
			}
		}
		if eqVal, ok := resourceFilter[query.Eq]; ok {
			tags["http.url"] = fmt.Sprintf("%v", eqVal)
		}
	}

	// Skip timestamp - handled separately via start/end params
	// Skip trace_source - Jaeger-specific, not applicable
}

// convertNsToJaegerDuration converts nanoseconds to Jaeger duration string (e.g., "5ms", "1s")
func (j *JaegerTraceSource) convertNsToJaegerDuration(val any) string {
	var ns int64
	switch v := val.(type) {
	case float64:
		ns = int64(v)
	case int64:
		ns = v
	case int:
		ns = int64(v)
	default:
		return ""
	}

	if ns <= 0 {
		return ""
	}

	// Convert to appropriate unit
	if ns >= 1_000_000_000 { // 1 second or more
		return fmt.Sprintf("%ds", ns/1_000_000_000)
	} else if ns >= 1_000_000 { // 1 millisecond or more
		return fmt.Sprintf("%dms", ns/1_000_000)
	} else if ns >= 1_000 { // 1 microsecond or more
		return fmt.Sprintf("%dus", ns/1_000)
	}
	return fmt.Sprintf("%dns", ns)
}

// transformJaegerResponse converts Jaeger API response to OpenTelemetryTrace format
func (j *JaegerTraceSource) transformJaegerResponse(ctx *security.RequestContext, respData map[string]any) ([]common.OpenTelemetryTrace, error) {
	traces := []common.OpenTelemetryTrace{}

	data, ok := respData["data"].(map[string]any)
	if !ok {
		data = respData
	}
	ctx.GetLogger().Info("JaegerSaas.transformJaegerResponse", "data", respData)
	var tracesData []any
	if d, ok := data["data"].([]any); ok {
		tracesData = d
	} else if innerData, ok := data["data"].(map[string]any); ok {
		if d, ok := innerData["data"].([]any); ok {
			tracesData = d
		}
	}

	if tracesData == nil {
		slog.Warn("No traces found in Jaeger response", "response_keys", getMapKeys(data))
		return traces, nil
	}

	for _, traceData := range tracesData {
		trace, ok := traceData.(map[string]any)
		if !ok {
			continue
		}

		processes := make(map[string]map[string]any)
		if processesData, ok := trace["processes"].(map[string]any); ok {
			for pid, pdata := range processesData {
				if processMap, ok := pdata.(map[string]any); ok {
					processes[pid] = processMap
				}
			}
		}

		spans, ok := trace["spans"].([]any)
		if !ok {
			continue
		}

		for _, spanData := range spans {
			span, ok := spanData.(map[string]any)
			if !ok {
				continue
			}

			otelTrace := j.jaegerSpanToOTEL(span, processes)
			traces = append(traces, otelTrace)
		}
	}

	slog.Info("Transformed Jaeger response", "trace_count", len(traces))

	return traces, nil
}

// jaegerSpanToOTEL converts a Jaeger span to OpenTelemetryTrace format
func (j *JaegerTraceSource) jaegerSpanToOTEL(span map[string]any, processes map[string]map[string]any) common.OpenTelemetryTrace {
	traceID := getStringField(span, "traceID", "")
	spanID := getStringField(span, "spanID", "")
	operationName := getStringField(span, "operationName", "")
	processID := getStringField(span, "processID", "")

	serviceName := ""
	processTags := make(map[string]any)
	if process, ok := processes[processID]; ok {
		serviceName = getStringField(process, "serviceName", "")
		if tags, ok := process["tags"].([]any); ok {
			processTags = j.tagsToMap(tags)
		}
	}

	spanTags := make(map[string]any)
	if tags, ok := span["tags"].([]any); ok {
		spanTags = j.tagsToMap(tags)
	}

	parentSpanID := ""
	if refs, ok := span["references"].([]any); ok {
		for _, ref := range refs {
			if refMap, ok := ref.(map[string]any); ok {
				if refType := getStringField(refMap, "refType", ""); refType == "CHILD_OF" {
					parentSpanID = getStringField(refMap, "spanID", "")
					break
				}
			}
		}
	}

	startTime := getInt64Field(span, "startTime", 0) // microseconds
	duration := getInt64Field(span, "duration", 0)   // microseconds
	durationNs := duration * 1000                    // convert to nanoseconds
	timestamp := time.UnixMicro(startTime).Format(time.RFC3339Nano)

	workloadNamespace := j.extractFirstNonEmpty(spanTags,
		"Namespace",
		"namespace",
		"k8s.namespace.name",
	)
	if workloadNamespace == "" {
		workloadNamespace = j.extractFirstNonEmpty(processTags,
			"Namespace",
			"namespace",
			"k8s.namespace.name",
		)
	}

	// Extract workload name - check multiple sources in order of preference
	// Support both custom fields and OTEL conventions
	workloadName := j.extractFirstNonEmpty(spanTags,
		"Deployment",
		"deployment",
		"k8s.deployment.name",
	)
	if workloadName == "" {
		workloadName = j.extractFirstNonEmpty(processTags,
			"Deployment",
			"deployment",
			"k8s.deployment.name",
		)
	}
	if workloadName == "" {
		// Try to extract from host.name (pod name) in process tags
		// Pod names follow format: {workload}-{replicaset-hash}-{pod-hash}
		hostName := j.extractTagValue(processTags, "host.name")
		if hostName != "" {
			extracted := extractWorkloadFromPodName(hostName)
			// Only use if extraction was successful (different from input)
			if extracted != hostName {
				workloadName = extracted
			}
		}
	}
	if workloadName == "" {
		workloadName = j.extractFirstNonEmpty(spanTags, "k8s.pod.name", "pod.name", "Pod")
	}
	if workloadName == "" {
		workloadName = j.extractFirstNonEmpty(processTags, "k8s.pod.name", "pod.name", "Pod")
	}
	if workloadName == "" {
		workloadName = serviceName
	}

	httpStatusCode := j.extractTagValue(spanTags, "http.status_code")
	httpURL := j.extractTagValue(spanTags, "http.url")
	if httpURL == "" {
		httpURL = j.extractTagValue(spanTags, "http.target")
	}

	statusCode := "STATUS_CODE_UNSET"
	if errorTag := j.extractTagValue(spanTags, "error"); errorTag == "true" {
		statusCode = "STATUS_CODE_ERROR"
	} else if httpStatusCode != "" {
		if statusInt, err := strconv.Atoi(httpStatusCode); err == nil {
			if statusInt >= 400 {
				statusCode = "STATUS_CODE_ERROR"
			} else if statusInt >= 100 && statusInt < 400 {
				statusCode = "STATUS_CODE_OK"
			}
		}
	}

	resourceAttrs := make(map[string]string)
	resourceAttrs["service.name"] = serviceName
	for k, v := range processTags {
		resourceAttrs[k] = fmt.Sprintf("%v", v)
	}

	spanAttrs := make(map[string]string)
	for k, v := range spanTags {
		spanAttrs[k] = fmt.Sprintf("%v", v)
	}

	destWorkload := j.extractFirstNonEmpty(spanTags,
		"destination.workload_name",
		"peer.service",
		"server.address",
		"net.peer.name",
		"db.host",
		"rpc.service",
		"messaging.destination",
	)

	destName := j.extractFirstNonEmpty(spanTags,
		"destination.name",
		"messaging.destination.name",
		"db.name",
	)

	if destName == "" && destWorkload != "" {
		destName = destWorkload
	}

	destNamespace := j.extractFirstNonEmpty(spanTags,
		"destination.workload_namespace",
		"server.namespace",
	)

	return common.OpenTelemetryTrace{
		TraceID:              traceID,
		SpanID:               spanID,
		ParentSpanID:         parentSpanID,
		SpanName:             operationName,
		ServiceName:          serviceName,
		WorkloadName:         workloadName,
		WorkloadNamespace:    workloadNamespace,
		DurationNs:           durationNs,
		Timestamp:            timestamp,
		SpanAttributes:       spanAttrs,
		ResourceAttributes:   resourceAttrs,
		HTTPStatusCode:       httpStatusCode,
		StatusCode:           statusCode,
		Resource:             httpURL,
		DestinationName:      destName,
		DestinationWorkload:  destWorkload,
		DestinationNamespace: destNamespace,
		TraceSource:          "jaeger",
	}
}

// tagsToMap converts Jaeger tags array to a map
func (j *JaegerTraceSource) tagsToMap(tags []any) map[string]any {
	result := make(map[string]any)
	for _, tag := range tags {
		tagMap, ok := tag.(map[string]any)
		if !ok {
			continue
		}
		key := getStringField(tagMap, "key", "")
		if key == "" {
			continue
		}
		if val, ok := tagMap["value"]; ok {
			result[key] = val
		}
	}
	return result
}

// extractTagValue extracts a string value from tags map
func (j *JaegerTraceSource) extractTagValue(tags map[string]any, key string) string {
	if val, ok := tags[key]; ok {
		return fmt.Sprintf("%v", val)
	}
	return ""
}

// extractFirstNonEmpty returns the first non-empty value from a list of tag keys
func (j *JaegerTraceSource) extractFirstNonEmpty(tags map[string]any, keys ...string) string {
	for _, key := range keys {
		if val := j.extractTagValue(tags, key); val != "" {
			return val
		}
	}
	return ""
}

func getStringField(data map[string]any, key, defaultValue string) string {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int64:
			return strconv.FormatInt(v, 10)
		case int:
			return strconv.Itoa(v)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return defaultValue
}

func getInt64Field(data map[string]any, key string, defaultValue int64) int64 {
	if val, ok := data[key]; ok {
		switch v := val.(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		}
	}
	return defaultValue
}

func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// extractWorkloadFromPodName extracts the workload name from a Kubernetes pod name.
func extractWorkloadFromPodName(podName string) string {
	if podName == "" {
		return ""
	}

	// Split by '-', dropping empty segments, in a single O(n) pass.
	parts := strings.FieldsFunc(podName, func(c rune) bool { return c == '-' })
	if len(parts) < 3 {
		return podName
	}

	lastPart := parts[len(parts)-1]
	secondLastPart := parts[len(parts)-2]

	if looksLikeK8sSuffix(lastPart, 5, 5) && looksLikeK8sSuffix(secondLastPart, 5, 10) {
		return strings.Join(parts[:len(parts)-2], "-")
	}

	return podName
}

// looksLikeK8sSuffix checks if a string looks like a K8s-generated suffix
func looksLikeK8sSuffix(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
