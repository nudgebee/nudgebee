package observability

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"strconv"
	"time"
)

type ChronosphereMetricSaasSource struct{}
type ChronosphereTraceSaasSource struct{}

func (s *ChronosphereTraceSaasSource) QueryTracesHeatmap(ctx *security.RequestContext, fetchHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	traceIDQueryRequest := TracesQueryBuilderRequest{
		Where: query.QueryWhereClause{
			Binary: query.BinaryWhereClause{
				"trace_id": map[query.BinaryWhereClauseType]any{
					query.Eq: fetchHeatMapRequest.TraceId,
				},
			},
		},
	}
	queryRequest := getQueryRequest(ctx, traceIDQueryRequest, query.TableDefinition{}, "traces_v2")
	chronosphereParams := query.ExtractChronosphereParams(queryRequest)
	rows, err := s.handleChronosphereBatching(ctx, chronosphereParams, fetchHeatMapRequest.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to call heat map API: %w", err)
	}
	heatmapEntries := make([]common.OpenTelemetryTraceHeatMap, 0, len(rows))
	for i := range rows {
		heatmapEntry := common.OpenTelemetryTraceHeatMap{
			TraceID:            rows[i].TraceID,
			Timestamp:          rows[i].Timestamp,
			ResourceAttributes: rows[i].ResourceAttributes,
			SpanName:           rows[i].SpanName,
			StatusCode:         rows[i].StatusCode,
			DurationNs:         rows[i].DurationNs,
			SpanAttributes:     rows[i].SpanAttributes,
			SpanID:             rows[i].SpanID,
			ServiceName:        rows[i].ServiceName,
			EventsAttributes:   rows[i].EventsAttributes,
			EventsName:         rows[i].EventsName,
		}
		heatmapEntries = append(heatmapEntries, heatmapEntry)
	}
	return heatmapEntries, nil
}

func (s *ChronosphereMetricSaasSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	chronosphereUrl, chronosphereToken, err := GetChronosphereAuth(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(fmt.Sprintf("%s/api/v1/labels", chronosphereUrl))
	if err != nil {
		return nil, fmt.Errorf("failed to parse url: %w", err)
	}

	q := u.Query()
	if req.StartTime > 0 {
		start := time.Unix(req.StartTime/1000, 0).UTC().Format(time.RFC3339Nano)
		q.Set("start", start)
	}
	if req.EndTime > 0 {
		end := time.Unix(req.EndTime/1000, 0).UTC().Format(time.RFC3339Nano)
		q.Set("end", end)
	}
	if req.MetricName != "" {
		selector := fmt.Sprintf("{__name__=\"%s\"}", req.MetricName)
		q.Add("match[]", selector)
	}
	if limit, ok := req.Request["limit"].(int); ok && limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	u.RawQuery = q.Encode()
	resp, err := common.HttpGet(u.String(), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
		"Content-Type":  "application/json",
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to call metric API: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Failed to get chronosphere metrics list", "resp", resp)
		return nil, fmt.Errorf("failed to get chronosphere metrics list, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data3 map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	rawData, ok := data3["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected format for 'data'")
	}

	var labels []OutputMetricLabels
	for _, v := range rawData {
		if valueStr, ok := v.(string); ok {
			labels = append(labels, OutputMetricLabels{
				Label:      valueStr,
				Attributes: make(map[string]interface{}),
			})
		}
	}
	return labels, nil
}

const (
	ChronosphereUrl   = "chronosphere_url"
	ChronosphereToken = "chronosphere_token"
)

func GetChronosphereAuth(ctx *security.RequestContext, accountId string) (string, string, error) {
	integrationDto, err := core.ListIntegrationConfigs(ctx, accountId, "chronosphere")
	if err != nil {
		return "", "", fmt.Errorf("failed to get chronosphere integration: %w", err)
	}
	if len(integrationDto) == 0 {
		return "", "", errors.New("no chronosphere integrations found")
	}

	integration := integrationDto[0]

	var chronosphereUrl, chronosphereToken string
	for _, config := range integration.Configs {
		switch config.Name {
		case ChronosphereUrl:
			chronosphereUrl = config.Value
		case ChronosphereToken:
			chronosphereToken = config.Value
		}
	}
	if chronosphereUrl == "" || chronosphereToken == "" {
		return "", "", fmt.Errorf("missing required chronosphere configuration values")
	}

	return chronosphereUrl, chronosphereToken, nil
}

func (s *ChronosphereMetricSaasSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	chronosphereUrl, chronosphereToken, err := GetChronosphereAuth(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(fmt.Sprintf("%s/api/v1/label/%s/values", chronosphereUrl, req.Label))
	if err != nil {
		return nil, fmt.Errorf("failed to parse url: %w", err)
	}

	q := u.Query()
	if req.StartTime > 0 {
		start := time.Unix(req.StartTime/1000, 0).UTC().Format(time.RFC3339Nano)
		q.Set("start", start)
	}
	if req.EndTime > 0 {
		end := time.Unix(req.EndTime/1000, 0).UTC().Format(time.RFC3339Nano)
		q.Set("end", end)
	}
	if match, ok := req.Request["query"].(string); ok && match != "" {
		q.Add("match[]", match)
	}
	if limit, ok := req.Request["limit"].(int); ok && limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	u.RawQuery = q.Encode()
	resp, err := common.HttpGet(u.String(), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
		"Content-Type":  "application/json",
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to call label values API: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Failed to get chronosphere metrics list", "resp", resp)
		return nil, fmt.Errorf("failed to get chronosphere metrics list, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data3 map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	rawData, ok := data3["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected format for 'data'")
	}

	var labelValues []OutputMetricsLabelValues
	for _, v := range rawData {
		if valueStr, ok := v.(string); ok {
			labelValues = append(labelValues, OutputMetricsLabelValues{
				Value:      valueStr,
				Attributes: make(map[string]interface{}),
			})
		}
	}
	return labelValues, nil
}

func (s *ChronosphereMetricSaasSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	chronosphereUrl, chronosphereToken, err := GetChronosphereAuth(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}

	resp, err := common.HttpGet(fmt.Sprintf("%s/api/v1/label/__name__/values", chronosphereUrl), common.HttpWithHeaders(map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
		"Content-Type":  "application/json",
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to execute chronosphere metrics list: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			ctx.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		ctx.GetLogger().Error("Failed to get chronosphere metrics list", "resp", resp)
		return nil, fmt.Errorf("failed to get chronosphere metrics list, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data3 map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	rawData, ok := data3["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected format for 'data'")
	}

	var metrics []OutputMetrics
	for _, v := range rawData {
		if metricStr, ok := v.(string); ok {
			metrics = append(metrics, OutputMetrics{
				Metric:     metricStr,
				Attributes: make(map[string]interface{}),
			})
		}
	}
	return metrics, nil
}

func (s *ChronosphereMetricSaasSource) FetchMetricsQuery(
	ctx *security.RequestContext,
	req FetchMetricsRequest,
) (OutputMetricQuery, error) {
	chronosphereUrl, chronosphereToken, err := GetChronosphereAuth(ctx, req.AccountId)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	var queryResults []QueryResult

	start := time.Unix(req.StartTime/1000, 0).UTC().Format(time.RFC3339Nano)
	end := time.Unix(req.EndTime/1000, 0).UTC().Format(time.RFC3339Nano)

	for queryKey, rawExpr := range req.Queries {
		queryExpr, err := injectPromQLMatchers(rawExpr, nil, req.Labels)
		if err != nil {
			return OutputMetricQuery{}, err
		}
		u, err := url.Parse(chronosphereUrl)
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("invalid chronosphere base URL: %w", err)
		}

		var endpoint string
		q := u.Query()

		if req.Instant { // instant query
			endpoint = "/api/v1/query"
			q.Set("query", queryExpr)
			q.Set("time", start)
		} else { // range query
			endpoint = "/api/v1/query_range"
			q.Set("query", queryExpr)
			q.Set("start", start)
			q.Set("end", end)
			q.Set("step", strconv.Itoa(req.StepInterval))
		}

		u.Path = endpoint
		u.RawQuery = q.Encode()
		urlStr := u.String()

		resp, err := common.HttpGet(urlStr, common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
			"Content-Type":  "application/json",
		}))
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to execute chronosphere query %q: %w", queryExpr, err)
		}
		jsonResponseBody := resp.Body
		defer func() {
			err := jsonResponseBody.Close()
			if err != nil {
				ctx.GetLogger().Error("Error closing response body", "error", err)
			}
		}()

		bodyBytes, err := io.ReadAll(jsonResponseBody)
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to read response body: %w", err)
		}

		if resp.StatusCode != 200 {

			return OutputMetricQuery{}, fmt.Errorf(
				"failed to get chronosphere metrics, query=%q, status code: %d, body: %s",
				queryExpr,
				resp.StatusCode,
				string(bodyBytes),
			)

		}
		var data3 map[string]any
		if err := common.UnmarshalJson(bodyBytes, &data3); err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to unmarshal response body: %w", err)
		}

		data, ok := data3["data"].(map[string]any)
		if !ok {
			return OutputMetricQuery{}, fmt.Errorf("missing 'data' field")
		}

		results, ok := data["result"].([]any)
		if !ok {
			return OutputMetricQuery{}, fmt.Errorf("missing 'result' field")
		}

		var payload []Result
		for _, r := range results {
			rMap, ok := r.(map[string]any)
			if !ok {
				continue
			}

			metricMap := make(map[string]string)
			if m, ok := rMap["metric"].(map[string]any); ok {
				for k, v := range m {
					if s, ok := v.(string); ok {
						metricMap[k] = s
					}
				}
			}

			var timestamps []int64
			var values []float64

			// instant → "value": [timestamp, "val"]
			if v, ok := rMap["value"].([]any); ok && len(v) == 2 {
				if ts, ok := v[0].(float64); ok {
					timestamps = append(timestamps, int64(ts))
				}
				if valStr, ok := v[1].(string); ok {
					if valFloat, err := strconv.ParseFloat(valStr, 64); err == nil {
						values = append(values, valFloat)
					}
				}
			}

			// range → "values": [[ts, "val"], [ts, "val"], ...]
			if vv, ok := rMap["values"].([]any); ok {
				for _, pair := range vv {
					if arr, ok := pair.([]any); ok && len(arr) == 2 {
						if ts, ok := arr[0].(float64); ok {
							timestamps = append(timestamps, int64(ts))
						}
						if valStr, ok := arr[1].(string); ok {
							if valFloat, err := strconv.ParseFloat(valStr, 64); err == nil {
								values = append(values, valFloat)
							}
						}
					}
				}
			}

			payload = append(payload, Result{
				Metric:     metricMap,
				Timestamps: timestamps,
				Values:     values,
			})
		}

		queryResults = append(queryResults, QueryResult{
			QueryKey: queryKey,
			Payload:  payload,
		})
	}

	return OutputMetricQuery{Results: queryResults}, nil
}
func (s *ChronosphereTraceSaasSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *ChronosphereTraceSaasSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_regex"}
}

func (c *ChronosphereTraceSaasSource) CountTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

func (c *ChronosphereTraceSaasSource) GetLabelValues(ctx *security.RequestContext, fetchTraceRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("Chronosphere.OpenTelemetryTraceLabelValues unimplemented")
}

func (s *ChronosphereTraceSaasSource) QueryGroupedTracesCount(sc *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{Count: -1}, nil
}

func (s *ChronosphereTraceSaasSource) GetQuery(sc *security.RequestContext, tracesRequest TracesV3Request) (string, error) {
	return "", fmt.Errorf("Chronosphere.GetQuery unimplemented")
}
func (m ChronosphereTraceSaasSource) normalizeTraceIDResponse(id string) string {
	if id == "" {
		return id
	}

	// Assume all IDs from Chronosphere are Base64 encoded and convert to hex
	if decoded, err := base64.StdEncoding.DecodeString(id); err == nil {
		return hex.EncodeToString(decoded)
	}

	// If Base64 decoding fails, return as-is
	return id
}

func (m ChronosphereTraceSaasSource) extractTagsAsJSON(span map[string]any) map[string]string {
	tags := make(map[string]string)

	// Extract from attributes
	if attributesRaw, hasAttrs := span["attributes"]; hasAttrs {
		if attributes, ok := attributesRaw.([]any); ok {
			for _, attrData := range attributes {
				if attr, ok := attrData.(map[string]any); ok {
					if key, hasKey := attr["key"].(string); hasKey {
						if valueRaw, hasValue := attr["value"]; hasValue {
							if value, ok := valueRaw.(map[string]any); ok {
								// Try different value types
								if stringVal, hasString := value["string_value"].(string); hasString {
									tags[key] = stringVal
								} else if intVal, hasInt := value["int_value"]; hasInt {
									tags[key] = fmt.Sprintf("%v", intVal)
								} else if doubleVal, hasDouble := value["double_value"]; hasDouble {
									tags[key] = fmt.Sprintf("%v", doubleVal)
								} else if boolVal, hasBool := value["bool_value"]; hasBool {
									tags[key] = fmt.Sprintf("%v", boolVal)
								}
							}
						}
					}
				}
			}
		}
	}

	// // Convert to JSON string
	// if len(tags) > 0 {
	// 	if jsonBytes, err := common.MarshalJson(tags); err == nil {
	// 		return string(jsonBytes)
	// 	}
	// }

	return tags
}

func (m ChronosphereTraceSaasSource) extractAttributeValue(span map[string]any, targetKeys []string, defaultValue string) string {
	return m.findAttributeStringValue(span, targetKeys, defaultValue)
}

func (m ChronosphereTraceSaasSource) extractStatusCode(span map[string]any) string {
	// First check for OpenTelemetry status
	if statusRaw, hasStatus := span["status"]; hasStatus {
		// Handle status as object (proper OTel format)
		if status, ok := statusRaw.(map[string]any); ok {
			// Check for status code - handle both int and float64 (JSON unmarshaling)
			if codeRaw, hasCode := status["code"]; hasCode {
				// Handle float64 (JSON numbers) and int
				var codeInt int
				switch code := codeRaw.(type) {
				case float64:
					codeInt = int(code)
				case int:
					codeInt = code
				case int64:
					codeInt = int(code)
				default:
					// Invalid code type, fall through to HTTP status check
					break
				}

				// Map according to OpenTelemetry status codes
				switch codeInt {
				case 0:
					return "STATUS_CODE_UNSET"
				case 1:
					return "STATUS_CODE_OK"
				case 2:
					return "STATUS_CODE_ERROR"
				default:
					// Unknown code, return formatted version
					return fmt.Sprintf("STATUS_CODE_%d", codeInt)
				}
			}
		} else if statusStr, ok := statusRaw.(string); ok {
			// Handle status as string (some APIs might return this format)
			switch statusStr {
			case "OK":
				return "STATUS_CODE_OK"
			case "ERROR":
				return "STATUS_CODE_ERROR"
			case "UNSET":
				return "STATUS_CODE_UNSET"
			}
		}
	}

	// Fallback: check HTTP status code from attributes to derive OTel status
	httpStatusCode := m.findAttributeStringValue(span, []string{"http.status_code"}, "")
	if httpStatusCode != "" {
		if statusInt, err := strconv.Atoi(httpStatusCode); err == nil {
			if statusInt >= 400 {
				return "STATUS_CODE_ERROR"
			} else if statusInt >= 100 && statusInt < 400 {
				return "STATUS_CODE_OK"
			}
		}
	}

	// Default to UNSET if no status field (per OpenTelemetry specification)
	return "STATUS_CODE_UNSET"
}

func (m ChronosphereTraceSaasSource) convertNanoToTime(nanoTimestamp string) (time.Time, error) {
	nanoTime, err := strconv.ParseInt(nanoTimestamp, 10, 64)
	if err != nil {
		return time.Time{}, err
	}

	// Convert nanoseconds to time.Time
	return time.Unix(0, nanoTime), nil
}

func (m ChronosphereTraceSaasSource) extractServiceNamespaceFromAttributes(span map[string]any) string {
	return m.findAttributeStringValue(span, []string{"service.namespace", "k8s.namespace.name"}, "")
}

func (m ChronosphereTraceSaasSource) extractServiceNameFromAttributes(span map[string]any) string {
	// First try service.name
	serviceName := m.findAttributeStringValue(span, []string{"service.name"}, "")
	if serviceName != "" {
		return serviceName
	}

	// Fallback to k8s.pod.name
	podName := m.findAttributeStringValue(span, []string{"k8s.pod.name"}, "")
	if podName != "" {
		return podName
	}

	return "unknown-service"
}

func (m ChronosphereTraceSaasSource) findAttributeStringValue(span map[string]any, targetKeys []string, defaultValue string) string {
	// Helper function to search through attributes array
	searchAttributes := func(attributesRaw any) string {
		if attributes, ok := attributesRaw.([]any); ok {
			for _, attrData := range attributes {
				if attr, ok := attrData.(map[string]any); ok {
					if key, hasKey := attr["key"].(string); hasKey {
						// Check if this key matches any of our target keys
						for _, targetKey := range targetKeys {
							if key == targetKey {
								if valueRaw, hasValue := attr["value"]; hasValue {
									if value, ok := valueRaw.(map[string]any); ok {
										// Try different value types
										if stringVal, hasString := value["string_value"].(string); hasString {
											return stringVal
										} else if intVal, hasInt := value["int_value"]; hasInt {
											return fmt.Sprintf("%v", intVal)
										} else if doubleVal, hasDouble := value["double_value"]; hasDouble {
											return fmt.Sprintf("%.0f", doubleVal)
										} else if boolVal, hasBool := value["bool_value"]; hasBool {
											return fmt.Sprintf("%v", boolVal)
										}
									}
								}
							}
						}
					}
				}
			}
		}
		return ""
	}

	// Look in span attributes first
	if attributesRaw, hasAttrs := span["attributes"]; hasAttrs {
		if result := searchAttributes(attributesRaw); result != "" {
			return result
		}
	}

	// Fallback: look for resource attributes
	if resourceRaw, hasResource := span["resource"]; hasResource {
		if resource, ok := resourceRaw.(map[string]any); ok {
			if attributesRaw, hasAttrs := resource["attributes"]; hasAttrs {
				if result := searchAttributes(attributesRaw); result != "" {
					return result
				}
			}
		}
	}

	return defaultValue
}

func (m ChronosphereTraceSaasSource) getStringField(data map[string]any, key, defaultValue string) string {
	if val, exists := data[key]; exists {
		switch v := val.(type) {
		case string:
			return v
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			// For large numbers (like nanosecond timestamps), ensure we don't get scientific notation
			// JSON numbers are decoded as float64, so we need to convert them properly
			if v == float64(int64(v)) {
				// If it's a whole number, convert to int64 first to avoid scientific notation
				return strconv.FormatInt(int64(v), 10)
			}
			// For actual floating point numbers, use standard formatting
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		default:
			// Last resort for other types
			return fmt.Sprintf("%v", v)
		}
	}
	return defaultValue
}

// convertNanoToTimeString converts nanosecond timestamp string to RFC3339 string for TraceSpan compatibility
func (s *ChronosphereTraceSaasSource) convertNanoToTimeString(nanoTimestamp string) string {
	if nanoTimestamp == "" {
		return ""
	}

	nanoTime, err := strconv.ParseInt(nanoTimestamp, 10, 64)
	if err != nil {
		return nanoTimestamp // Return original string if parsing fails
	}

	// Convert nanoseconds to time.Time, then to RFC3339 string
	return time.Unix(0, nanoTime).Format(time.RFC3339)
}

func (s *ChronosphereTraceSaasSource) calculateDurationAsFloat(startTime, endTime string) float64 {
	if startTime != "" && endTime != "" {
		startNano, startErr := strconv.ParseInt(startTime, 10, 64)
		endNano, endErr := strconv.ParseInt(endTime, 10, 64)

		if startErr == nil && endErr == nil {
			durationNano := endNano - startNano
			if durationNano >= 0 {
				return float64(durationNano)
			}
		}
	}
	return 0.0
}

func (s *ChronosphereTraceSaasSource) newChronosphereRows(sc *security.RequestContext, data map[string]any) ([]common.OpenTelemetryTrace, error) {
	// Chronosphere returns trace data in OpenTelemetry format
	// We need to extract and flatten the spans into a tabular format

	// Expected structure: data contains "traces" array with spans
	traces, ok := data["traces"].([]any)
	if !ok {
		// Fallback: look for "spans" directly
		if spans, hasSpans := data["spans"].([]any); hasSpans {
			// Create the expected nested structure for the fallback case
			traces = []any{map[string]any{
				"resource_spans": []any{
					map[string]any{
						"scope_spans": []any{
							map[string]any{
								"spans": spans,
							},
						},
					},
				},
			}}
		} else {
			return nil, errors.New("unable to find traces or spans in Chronosphere response")
		}
	}
	rows := []common.OpenTelemetryTrace{}

	// Process each trace
	for _, traceData := range traces {
		trace, ok := traceData.(map[string]any)
		if !ok {
			// Skip invalid trace data
			continue
		}
		resourceSpans, hasResourceSpans := trace["resource_spans"].([]any)
		if !hasResourceSpans {
			continue
		}

		// Process each resource span
		for _, resourceSpanData := range resourceSpans {
			resourceSpan, ok := resourceSpanData.(map[string]any)
			if !ok {
				continue
			}

			scopeSpans, hasScopeSpans := resourceSpan["scope_spans"].([]any)
			if !hasScopeSpans {
				continue
			}

			// Process each scope span
			for _, scopeSpanData := range scopeSpans {
				scopeSpan, ok := scopeSpanData.(map[string]any)
				if !ok {
					continue
				}

				spans, hasSpans := scopeSpan["spans"].([]any)
				if !hasSpans {
					continue
				}

				// Process each span
				for _, spanData := range spans {
					span, ok := spanData.(map[string]any)
					if !ok {
						// Skip invalid span data
						continue
					}

					// Extract basic span fields
					parentSpanID := s.normalizeTraceIDResponse(s.getStringField(span, "parent_span_id", ""))
					startTimeNano := s.getStringField(span, "start_time_unix_nano", "")
					endTimeNano := s.getStringField(span, "end_time_unix_nano", "")

					// Convert nanosecond timestamps to RFC3339 string for TraceSpan compatibility
					startTime := s.convertNanoToTimeString(startTimeNano)
					endTime := s.convertNanoToTimeString(endTimeNano)

					// Extract service name and namespace from attributes
					workloadName := s.extractServiceNameFromAttributes(span)
					workloadNamespace := s.extractServiceNamespaceFromAttributes(span)

					// Calculate duration as float64 for TraceSpan compatibility
					duration := s.calculateDurationAsFloat(startTimeNano, endTimeNano)

					// Extract status - OpenTelemetry status has different structure
					statusCode := s.extractStatusCode(span)

					// Extract additional traces_v2 fields from attributes
					resource := s.extractAttributeValue(span, []string{"db.statement", "http.url", "http.target"}, "")
					destinationWorkloadName := s.extractAttributeValue(span, []string{"destination.workload_name", "messaging.destination", "db.host", "net.peer.name"}, "")
					destinationName := s.extractAttributeValue(span, []string{"messaging.destination.name", "messaging.destination", "destination.name", "db.host", "net.peer.name"}, "")
					httpResponse := s.extractAttributeValue(span, []string{"http.response"}, "")

					// Determine trace source based on span attributes
					traceSource := "otel" // default
					if scopeName := s.extractAttributeValue(span, []string{"otel.scope.name"}, ""); scopeName == "nudgebee-node-agent" {
						traceSource = "ebpf"
					}
					resAttribute := make(map[string]string)
					resAttribute["service.name"] = s.extractServiceNameFromAttributes(span)
					resAttribute["host.id"] = resource
					resAttribute["host.name"] = resource
					timeStampTemp, err := s.convertNanoToTime(startTimeNano)
					if err != nil {
						return nil, fmt.Errorf("invalid start_time_unix_nano: %w", err)
					}
					timestamp := timeStampTemp.Format("2006-01-02T15:04:05.0000000Z")

					spanAttributes := s.extractTagsAsJSON(span)
					// if err != nil {
					// 	return nil, fmt.Errorf("failed marshalling spanAttributes from span: %w", err)
					// }
					// Create row matching traces_v2 format
					row := common.OpenTelemetryTrace{}
					row.TraceID = s.normalizeTraceIDResponse(s.getStringField(span, "trace_id", ""))
					row.SpanID = s.normalizeTraceIDResponse(s.getStringField(span, "span_id", ""))
					row.ParentSpanID = parentSpanID
					row.SpanName = s.getStringField(span, "name", "")
					row.ResourceAttributes = resAttribute
					row.SpanAttributes = spanAttributes
					row.DurationNs = int64(duration)
					row.StatusCode = statusCode
					row.WorkloadName = workloadName
					row.WorkloadNamespace = workloadNamespace
					row.Resource = resource
					row.DestinationName = destinationName
					row.DestinationWorkload = destinationWorkloadName
					row.DestinationNamespace = s.extractAttributeValue(span, []string{"destination.workload_namespace", "destination.namespace"}, "")
					row.Headers = s.extractAttributeValue(span, []string{"http.headers"}, "")
					row.HTTPStatusCode = s.extractAttributeValue(span, []string{"http.status_code"}, "")
					row.RequestPayload = s.extractAttributeValue(span, []string{"http.request_payload"}, "")
					row.HTTPResponse = httpResponse
					row.StartTime = startTime
					row.EndTime = endTime
					row.TraceSource = traceSource
					row.Timestamp = timestamp
					rows = append(rows, row)
				}
			}
		}
	}
	return rows, nil
}
func (c *ChronosphereTraceSaasSource) getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (c *ChronosphereTraceSaasSource) handleTraceIdBatching(sc *security.RequestContext, chronosphereParams map[string]any, traceIds any, maxTraceIDs int, accountId string) ([]common.OpenTelemetryTrace, error) {
	var traceIdsList []string

	slog.Info("Starting trace ID batching analysis",
		"max_trace_ids", maxTraceIDs,
		"trace_ids_type", fmt.Sprintf("%T", traceIds))

	// Convert trace IDs to string slice
	switch v := traceIds.(type) {
	case []any:
		slog.Debug("Converting []any trace IDs to string slice", "count", len(v))
		for i, id := range v {
			if idStr, ok := id.(string); ok {
				traceIdsList = append(traceIdsList, idStr)
			} else {
				slog.Warn("Non-string trace ID encountered",
					"index", i,
					"type", fmt.Sprintf("%T", id),
					"value", id)
			}
		}
	case []string:
		slog.Debug("Using []string trace IDs directly", "count", len(v))
		traceIdsList = v
	default:
		slog.Warn("Invalid trace IDs format, skipping batching",
			"type", fmt.Sprintf("%T", traceIds),
			"value", traceIds)
		return []common.OpenTelemetryTrace{}, nil
	}

	slog.Info("Trace ID batching assessment",
		"total_trace_ids", len(traceIdsList),
		"max_allowed", maxTraceIDs,
		"batching_needed", len(traceIdsList) > maxTraceIDs)

	// If trace IDs count is within limit, no batching needed
	if len(traceIdsList) <= maxTraceIDs {
		responseData, err := c.executeChronosphereChunk(sc, chronosphereParams, accountId)
		if err != nil {
			return nil, err
		}
		return c.newChronosphereRows(sc, responseData)
	}

	// Batching required for trace IDs
	numChunks := (len(traceIdsList) + maxTraceIDs - 1) / maxTraceIDs // Ceiling division

	slog.Info("Starting batched Chronosphere trace ID queries",
		"total_trace_ids", len(traceIdsList),
		"max_per_batch", maxTraceIDs,
		"calculated_chunks", numChunks)

	allResponsesData := make([]map[string]any, 0)
	successfulChunks := 0

	for i := 0; i < numChunks; i++ {
		start := i * maxTraceIDs
		end := start + maxTraceIDs
		if end > len(traceIdsList) {
			end = len(traceIdsList)
		}

		chunkTraceIds := traceIdsList[start:end]

		slog.Info("Processing Chronosphere trace ID chunk",
			"chunk", i+1,
			"total_chunks", numChunks,
			"trace_ids_in_chunk", len(chunkTraceIds),
			"start_index", start,
			"end_index", end)

		// Create parameters for this chunk
		chunkParams := make(map[string]any)
		for k, v := range chronosphereParams {
			chunkParams[k] = v
		}
		chunkParams["trace_ids"] = chunkTraceIds

		slog.Debug("Trace ID chunk parameters",
			"chunk", i+1,
			"chunk_trace_ids", chunkTraceIds)

		// Execute query for this chunk
		chunkResponseData, err := c.executeChronosphereChunk(sc, chunkParams, accountId)
		if err != nil {
			slog.Error("Chronosphere trace ID chunk query failed",
				"chunk", i+1,
				"total_chunks", numChunks,
				"trace_ids_in_chunk", len(chunkTraceIds),
				"error", err)
			continue
		}

		// Log chunk response data stats
		if chunkResponseData != nil {
			if traces, ok := chunkResponseData["traces"].([]any); ok {
				slog.Info("Trace ID chunk query successful",
					"chunk", i+1,
					"traces_count", len(traces),
					"trace_ids_queried", len(chunkTraceIds))
			} else {
				slog.Warn("Trace ID chunk response missing traces data",
					"chunk", i+1,
					"response_keys", c.getMapKeys(chunkResponseData))
			}
		} else {
			slog.Warn("Trace ID chunk returned nil response data",
				"chunk", i+1)
		}

		allResponsesData = append(allResponsesData, chunkResponseData)
		successfulChunks++
	}

	if successfulChunks == 0 {
		slog.Error("All trace ID chunks failed",
			"total_chunks", numChunks,
			"total_trace_ids", len(traceIdsList))
		return []common.OpenTelemetryTrace{}, fmt.Errorf("all %d trace ID chunks failed", numChunks)
	}

	// Combine all chunk responses into a single response
	slog.Info("Starting trace ID response combination",
		"response_chunks_to_combine", len(allResponsesData))

	combinedResponse := c.combineChronosphereResponses(allResponsesData)

	// Log final combined result stats
	if combinedTraces, ok := combinedResponse["traces"].([]any); ok {
		slog.Info("Chronosphere trace ID batched query completed successfully",
			"total_chunks", numChunks,
			"successful_chunks", successfulChunks,
			"total_trace_ids_queried", len(traceIdsList),
			"combined_traces_count", len(combinedTraces))
	} else {
		slog.Warn("Combined trace ID response missing traces data",
			"total_chunks", numChunks,
			"successful_chunks", successfulChunks,
			"combined_response_keys", c.getMapKeys(combinedResponse))
	}

	return c.newChronosphereRows(sc, combinedResponse)
}

func (c *ChronosphereTraceSaasSource) handleChronosphereBatching(sc *security.RequestContext, chronosphereParams map[string]any, accountId string) ([]common.OpenTelemetryTrace, error) {
	const (
		maxChunkDuration = 5 * time.Minute // Reduced chunk size for better granularity
		maxTotalDuration = 2 * time.Hour   // Maximum we support
		maxTraceIDs      = 10              // Chronosphere trace ID limitation
	)

	slog.Debug("Chronosphere batching check initiated")

	// Check for trace ID batching first
	if traceIds, hasTraceIds := chronosphereParams["trace_ids"]; hasTraceIds {
		slog.Info("Starting trace ID batching",
			"trace_ids_type", fmt.Sprintf("%T", traceIds),
			"max_trace_ids", maxTraceIDs)
		return c.handleTraceIdBatching(sc, chronosphereParams, traceIds, maxTraceIDs, accountId)
	}

	// Extract start and end times for time-based batching
	startTimeStr, hasStart := chronosphereParams["start_time"].(string)
	endTimeStr, hasEnd := chronosphereParams["end_time"].(string)

	slog.Debug("Time-based batching check")

	if !hasStart || !hasEnd {
		slog.Info("No time range specified, skipping batching")
		return nil, nil
	}

	startTime, err := time.Parse(time.RFC3339, startTimeStr)
	if err != nil {
		slog.Error("Failed to parse start_time",
			"start_time_str", startTimeStr,
			"error", err)
		return nil, fmt.Errorf("invalid start_time format: %v", err)
	}

	endTime, err := time.Parse(time.RFC3339, endTimeStr)
	if err != nil {
		slog.Error("Failed to parse end_time",
			"end_time_str", endTimeStr,
			"error", err)
		return nil, fmt.Errorf("invalid end_time format: %v", err)
	}

	totalDuration := endTime.Sub(startTime)

	slog.Debug("Parsed time range", "duration_hours", totalDuration.Hours())

	// Check if duration exceeds maximum supported
	if totalDuration > maxTotalDuration {
		return nil, fmt.Errorf("requested duration %.1f minutes exceeds maximum supported duration %.1f minutes",
			totalDuration.Minutes(), maxTotalDuration.Minutes())
	}

	// If duration is within single chunk limit, no batching needed
	if totalDuration <= maxChunkDuration {
		slog.Info("Duration within single chunk limit, no batching needed",
			"duration_minutes", totalDuration.Minutes(),
			"max_chunk_minutes", maxChunkDuration.Minutes())
		responseData, err := c.executeChronosphereChunk(sc, chronosphereParams, accountId)
		if err != nil {
			return nil, err
		}
		return c.newChronosphereRows(sc, responseData)
	}

	// Batching required
	slog.Info("Starting batched Chronosphere queries",
		"total_duration_minutes", totalDuration.Minutes(),
		"max_chunk_minutes", maxChunkDuration.Minutes(),
		"service_name", chronosphereParams["service"],
		"query_type", chronosphereParams["query_type"])

	numChunks := int((totalDuration + maxChunkDuration - 1) / maxChunkDuration) // Ceiling division
	successfulChunks := 0

	slog.Info("Calculated chunking strategy",
		"total_chunks", numChunks,
		"chunk_duration_minutes", maxChunkDuration.Minutes(),
		"parallel_execution", true,
		"max_concurrent", 2,
		"max_retries", 1)

	// Execute chunks in parallel with controlled concurrency
	allResponsesData, successfulChunks := c.executeChunksInParallel(sc, chronosphereParams, startTime, endTime, maxChunkDuration, numChunks, accountId)

	if successfulChunks == 0 {
		return nil, fmt.Errorf("all %d Chronosphere chunks failed", numChunks)
	}

	// Combine all chunk responses into a single response
	slog.Info("Starting response combination",
		"response_chunks_to_combine", len(allResponsesData))

	combinedResponse := c.combineChronosphereResponses(allResponsesData)

	// Log final combined result stats
	if combinedTraces, ok := combinedResponse["traces"].([]any); ok {
		slog.Info("Chronosphere batched query completed successfully",
			"total_chunks", numChunks,
			"successful_chunks", successfulChunks,
			"total_response_chunks", len(allResponsesData),
			"combined_traces_count", len(combinedTraces))
	} else {
		slog.Warn("Combined response missing traces data",
			"total_chunks", numChunks,
			"successful_chunks", successfulChunks,
			"combined_response_keys", c.getMapKeys(combinedResponse))
	}

	return c.newChronosphereRows(sc, combinedResponse)
}
func (c *ChronosphereTraceSaasSource) mapToChronosphereAPI(params map[string]any) map[string]any {
	mappedParams := make(map[string]any)
	// Set required parameters with defaults
	queryType := "SERVICE_OPERATION"
	if qt, exists := params["query_type"]; exists {
		queryType = fmt.Sprintf("%v", qt)
	}
	// Determine query_type based on available parameters
	if traceIds, hasTraceIds := params["trace_ids"]; hasTraceIds {
		if traceIdsList, ok := traceIds.([]any); ok && len(traceIdsList) > 0 {
			queryType = "TRACE_IDS"
			mappedParams["trace_ids"] = traceIds
			// Remove time range parameters when trace IDs are specified
			// as Chronosphere API doesn't allow both
		} else if traceIdsStr, ok := traceIds.([]string); ok && len(traceIdsStr) > 0 {
			queryType = "TRACE_IDS"
			mappedParams["trace_ids"] = traceIds
		}
	}

	mappedParams["query_type"] = queryType

	// Always include time range unless trace_ids is specified
	if queryType != "TRACE_IDS" {
		if startTime, exists := params["start_time"]; exists {
			mappedParams["start_time"] = startTime
		}
		if endTime, exists := params["end_time"]; exists {
			mappedParams["end_time"] = endTime
		}
	}
	// Map service parameter for SERVICE_OPERATION queries
	if queryType == "SERVICE_OPERATION" {
		if service, exists := params["service"]; exists {
			mappedParams["service"] = service
		}
	}
	// Map additional filtering parameters based on Chronosphere API specification
	if operation, exists := params["operation"]; exists {
		mappedParams["operation"] = operation
	}
	// Handle tag_filters in the correct Chronosphere API format
	if tagFilters, exists := params["tag_filters"]; exists {
		mappedParams["tag_filters"] = tagFilters
	}

	// Handle label_filters for span attribute filtering
	if labelFilters, exists := params["label_filters"]; exists {
		if filters, ok := labelFilters.(map[string]string); ok && len(filters) > 0 {
			// Convert label filters to Chronosphere tag_filters format
			chronosphereFilters := make([]map[string]string, 0, len(filters))
			for key, value := range filters {
				chronosphereFilters = append(chronosphereFilters, map[string]string{
					"key":   key,
					"value": value,
				})
			}
			mappedParams["tag_filters"] = chronosphereFilters

			slog.Info("Added label filters to Chronosphere query",
				"label_filters", filters,
				"chronosphere_tag_filters", chronosphereFilters)
		}
	}

	slog.Debug("Chronosphere API parameters",
		"query_type", mappedParams["query_type"],
		"has_time_range", mappedParams["start_time"] != nil && mappedParams["end_time"] != nil,
		"all_params", mappedParams)

	// Note: resource_filter, min_duration, max_duration, status_code are not supported
	// in the Chronosphere API based on the documentation provided

	return mappedParams
}

func (c *ChronosphereTraceSaasSource) executeChronosphereChunk(sc *security.RequestContext, chunkParams map[string]any, accountID string) (map[string]any, error) {
	// Create request data for this chunk
	chronosphereUrl, chronosphereToken, err := GetChronosphereAuth(sc, accountID)
	if err != nil {
		sc.GetLogger().Error("executeChronosphereChunk: failed to get chronophere auth", "error", err)
		return map[string]any{}, err
	}
	mappedParams := c.mapToChronosphereAPI(chunkParams)
	param := common.HttpWithJsonBody(mappedParams)

	resp, err := common.HttpPost(fmt.Sprintf("%s/api/v1/data/traces", chronosphereUrl), common.HttpWithHeaders(map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": fmt.Sprintf("Bearer %s", chronosphereToken),
	}), param)
	if err != nil {
		return nil, fmt.Errorf("failed to call label values API: %w", err)
	}
	jsonResponseBody := resp.Body
	defer func() {
		err := jsonResponseBody.Close()
		if err != nil {
			sc.GetLogger().Error("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != 200 {
		sc.GetLogger().Error("Failed to get chronosphere metrics list", "resp", resp)
		return nil, fmt.Errorf("failed to get chronosphere metrics list, status code: %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(jsonResponseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	var data map[string]any
	if err := common.UnmarshalJson(bodyBytes, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	// Check for pagination token in response (multiple possible field names)
	var nextToken string
	possibleTokenFields := []string{"next_token", "nextToken", "page_token", "pageToken", "continuation_token"}

	for _, field := range possibleTokenFields {
		if token, exists := data[field].(string); exists && token != "" {
			nextToken = token
			slog.Info("Pagination token found",
				"field_name", field,
				"token_preview", token[:min(len(token), 20)],
				"traces_in_page", func() int {
					if traces, ok := data["traces"].([]any); ok {
						return len(traces)
					}
					return 0
				}())
			break
		}
	}

	// Check if there are more results indicators
	if hasMore, exists := data["has_more"].(bool); exists && hasMore {
		slog.Warn("Response indicates more results available but no pagination token found",
			"has_more", hasMore)
	}
	// Log the final data structure
	if traces, ok := data["traces"].([]any); ok {
		slog.Info("Chunk response data extracted",
			"traces_count", len(traces),
			"data_keys", c.getMapKeys(data))
	}

	// Log complete response structure for debugging
	slog.Info("Complete Chronosphere response analysis",
		"response_keys", c.getMapKeys(data),
		"traces_count", func() int {
			if traces, ok := data["traces"].([]any); ok {
				return len(traces)
			}
			return 0
		}())

	// Log if we're potentially missing data
	if nextToken != "" {
		slog.Warn("Pagination detected - data may be incomplete",
			"next_token_present", true,
			"suggestion", "implement full pagination for complete data")
	}

	// rows, nil := c.newChronosphereRows(sc, data)
	return data, nil

}

func (c *ChronosphereTraceSaasSource) executeChunksInParallel(sc *security.RequestContext, chronosphereParams map[string]any, startTime, endTime time.Time, maxChunkDuration time.Duration, numChunks int, accountId string) ([]map[string]any, int) {
	const (
		maxConcurrent = 2
		maxRetries    = 1
	)

	type chunkJob struct {
		index      int
		start      time.Time
		end        time.Time
		params     map[string]any
		retryCount int
	}

	type chunkResult struct {
		index int
		data  map[string]any
		err   error
	}

	// Create job channel with buffered capacity
	jobs := make(chan chunkJob, numChunks)
	results := make(chan chunkResult, numChunks)

	// Start worker goroutines
	for w := 0; w < maxConcurrent; w++ {
		go func(workerID int) {
			for job := range jobs {
				slog.Debug("Worker processing chunk",
					"worker_id", workerID,
					"chunk", job.index+1,
					"retry_attempt", job.retryCount)

				data, err := c.executeChronosphereChunk(sc, job.params, accountId)
				results <- chunkResult{
					index: job.index,
					data:  data,
					err:   err,
				}
			}
		}(w)
	}

	// Send initial jobs
	for i := 0; i < numChunks; i++ {
		chunkStart := startTime.Add(time.Duration(i) * maxChunkDuration)
		chunkEnd := chunkStart.Add(maxChunkDuration)
		if chunkEnd.After(endTime) {
			chunkEnd = endTime
		}

		// Create parameters for this chunk
		chunkParams := make(map[string]any)
		for k, v := range chronosphereParams {
			chunkParams[k] = v
		}
		chunkParams["start_time"] = chunkStart.Format(time.RFC3339)
		chunkParams["end_time"] = chunkEnd.Format(time.RFC3339)

		jobs <- chunkJob{
			index:      i,
			start:      chunkStart,
			end:        chunkEnd,
			params:     chunkParams,
			retryCount: 0,
		}
	}

	// Collect results and handle retries
	allResponsesData := make([]map[string]any, 0, numChunks)
	successfulChunks := 0
	completedChunks := 0
	failedJobs := make(map[int]chunkJob)

	for completedChunks < numChunks {
		result := <-results
		completedChunks++

		if result.err != nil {
			originalJob := failedJobs[result.index]
			if originalJob.retryCount == 0 {
				// Find the original job info
				chunkStart := startTime.Add(time.Duration(result.index) * maxChunkDuration)
				chunkEnd := chunkStart.Add(maxChunkDuration)
				if chunkEnd.After(endTime) {
					chunkEnd = endTime
				}

				chunkParams := make(map[string]any)
				for k, v := range chronosphereParams {
					chunkParams[k] = v
				}
				chunkParams["start_time"] = chunkStart.Format(time.RFC3339)
				chunkParams["end_time"] = chunkEnd.Format(time.RFC3339)

				originalJob = chunkJob{
					index:  result.index,
					start:  chunkStart,
					end:    chunkEnd,
					params: chunkParams,
				}
			}

			if originalJob.retryCount < maxRetries {
				// Retry the failed chunk
				slog.Info("Retrying failed chunk",
					"chunk", result.index+1,
					"retry_attempt", originalJob.retryCount+1,
					"error", result.err)

				retryJob := originalJob
				retryJob.retryCount++
				failedJobs[result.index] = retryJob
				jobs <- retryJob
				completedChunks-- // Don't count this as completed yet
			} else {
				slog.Error("Chunk failed after retries",
					"chunk", result.index+1,
					"total_retries", maxRetries,
					"final_error", result.err)
			}
		} else {
			// Successful chunk
			if result.data != nil {
				if traces, ok := result.data["traces"].([]any); ok {
					slog.Debug("Chunk completed successfully",
						"chunk", result.index+1,
						"traces", len(traces))
				}
				allResponsesData = append(allResponsesData, result.data)
				successfulChunks++
			}
		}
	}

	close(jobs)
	close(results)

	slog.Info("Parallel chunk execution completed",
		"total_chunks", numChunks,
		"successful_chunks", successfulChunks,
		"failed_chunks", numChunks-successfulChunks)

	return allResponsesData, successfulChunks
}

func (c *ChronosphereTraceSaasSource) combineChronosphereResponses(responses []map[string]any) map[string]any {
	slog.Debug("Combining Chronosphere responses",
		"response_count", len(responses))

	if len(responses) == 0 {
		slog.Warn("No responses to combine, returning empty traces")
		return map[string]any{"traces": []any{}}
	}

	if len(responses) == 1 {
		slog.Debug("Single response, returning as-is")
		if traces, ok := responses[0]["traces"].([]any); ok {
			slog.Debug("Single response traces count", "traces", len(traces))
		}
		return responses[0]
	}

	// Combine all traces from all responses
	allTraces := make([]any, 0)
	totalTracesBeforeCombine := 0

	for i, response := range responses {
		if response == nil {
			slog.Warn("Nil response in combination", "response_index", i)
			continue
		}

		if traces, ok := response["traces"].([]any); ok {
			tracesCount := len(traces)
			allTraces = append(allTraces, traces...)
			totalTracesBeforeCombine += tracesCount
			slog.Debug("Combined traces from response",
				"response_index", i,
				"traces_in_response", tracesCount,
				"total_traces_so_far", len(allTraces))
		} else {
			slog.Warn("Response missing traces data",
				"response_index", i,
				"response_keys", c.getMapKeys(response))
		}
	}

	slog.Info("Response combination completed",
		"input_responses", len(responses),
		"total_traces_before_combine", totalTracesBeforeCombine,
		"final_traces_count", len(allTraces))

	// Return combined response in the same format as single response
	return map[string]any{
		"traces": allTraces,
	}
}

func (c *ChronosphereTraceSaasSource) QueryTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	if fetchTraceRequest.Query != "" {
		return nil, fmt.Errorf("custom query not supported for Chronosphere trace source")
	}
	queryRequest := getQueryRequest(ctx, fetchTraceRequest.QueryRequest, query.TableDefinition{}, "traces_v2")
	chronosphereParams := query.ExtractChronosphereParams(queryRequest)
	if fetchTraceRequest.StartTime != 0 {
		chronosphereParams["start_time"] = time.UnixMilli(fetchTraceRequest.StartTime).UTC().Format(time.RFC3339)
	}
	if fetchTraceRequest.EndTime != 0 {
		chronosphereParams["end_time"] = time.UnixMilli(fetchTraceRequest.EndTime).UTC().Format(time.RFC3339)
	}
	rows, err := c.handleChronosphereBatching(ctx, chronosphereParams, fetchTraceRequest.AccountId)
	if err != nil {
		return nil, fmt.Errorf("failed to call query traces API: %w", err)
	}
	return rows, nil
}

func (c *ChronosphereTraceSaasSource) QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("grouped traces not supported for Chronosphere trace source")
}

func (s *ChronosphereMetricSaasSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_regex"}
}

func (s *ChronosphereMetricSaasSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return injectPromQLMatchers(q, req.LabelMatchers, req.Labels)
	}
	return "", nil
}
