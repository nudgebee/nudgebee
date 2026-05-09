package observability

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

type ChronosphereMetricSource struct{}
type ChronosphereTraceSource struct{}

func (s *ChronosphereMetricSource) FetchMetricsLabels(ctx *security.RequestContext, req FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	params := map[string]any{}
	if req.StartTime > 0 {
		start := time.Unix(req.StartTime/1000, 0).UTC().Format(time.RFC3339Nano)
		params = map[string]any{"start_time": start}
	}
	if req.EndTime > 0 {
		end := time.Unix(req.EndTime/1000, 0).UTC().Format(time.RFC3339Nano)
		params["end_time"] = end

	}
	if req.MetricName != "" {
		selector := fmt.Sprintf("{__name__=\"%s\"}", req.MetricName)
		params["selector"] = selector
	}
	if limit, ok := req.Request["limit"].(int); ok && limit > 0 {
		params["limit"] = limit
	}
	chronosphereTraceRequest := relay.ActionExecuteBody{
		AccountID:    req.AccountId,
		ActionName:   "prometheus_labels",
		ActionParams: params,
		NoSinks:      true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    chronosphereTraceRequest,
	})
	if err != nil {
		return []OutputMetricLabels{}, err
	}
	if respData["status_code"].(float64) != 200 {
		return []OutputMetricLabels{}, fmt.Errorf("chronosphere returned status code %v", respData["status_code"])
	}

	if _, ok := respData["data"]; !ok {
		return []OutputMetricLabels{}, nil
	}
	var labels []string
	respDataMap, ok := respData["data"].(map[string]any)
	if !ok {
		return []OutputMetricLabels{}, nil
	}

	findingsAny, ok := respDataMap["findings"]
	if !ok {
		return []OutputMetricLabels{}, nil
	}

	findings, ok := findingsAny.([]any)
	if !ok || len(findings) == 0 {
		return []OutputMetricLabels{}, nil
	}

	finding, ok := findings[0].(map[string]any)
	if !ok {
		return []OutputMetricLabels{}, nil
	}

	evidenceAny, ok := finding["evidence"]
	if !ok {
		return []OutputMetricLabels{}, nil
	}

	evidenceList, ok := evidenceAny.([]any)
	if !ok || len(evidenceList) == 0 {
		return []OutputMetricLabels{}, nil
	}

	evidence, ok := evidenceList[0].(map[string]any)
	if !ok {
		return []OutputMetricLabels{}, nil
	}

	evidenceDataRaw, ok := evidence["data"].(string)
	if !ok {
		return []OutputMetricLabels{}, nil
	}

	var data []map[string]any
	if err := common.UnmarshalJson([]byte(evidenceDataRaw), &data); err != nil {
		return []OutputMetricLabels{}, err
	}

	if len(data) > 0 {
		if dataInside, ok := data[0]["data"].(string); ok {
			var resultData struct {
				Data []string `json:"data"`
			}
			if err := json.Unmarshal([]byte(dataInside), &resultData); err != nil {
				return []OutputMetricLabels{}, err
			}
			labels = resultData.Data
		}
	}
	var finalLabels []OutputMetricLabels
	for v := range labels {
		finalLabels = append(finalLabels, OutputMetricLabels{
			Label:      labels[v],
			Attributes: make(map[string]interface{}),
		})
	}
	return finalLabels, nil
}

func (s *ChronosphereMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	resp, err := relayRequest(ctx, req.AccountId, "prometheus_labels", map[string]any{"label_name": req.Label})
	if err != nil {
		return nil, err
	}

	result, err := parseRelayData(resp)
	if err != nil {
		return nil, err
	}

	values, raw, err := decodeInnerData(result)
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetricsLabelValues, len(values))
	for i, v := range values {
		output[i] = OutputMetricsLabelValues{Value: v, Attributes: raw}
	}
	return output, nil
}

func (s *ChronosphereMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
	if req.Metric != "" {
		sanitizedMetric := strings.ReplaceAll(req.Metric, "\"", "\\\"")
		match := fmt.Sprintf("{__name__=~\".*%s.*\"}", sanitizedMetric)
		path := fmt.Sprintf("prometheus-v2/api/v1/label/__name__/values?match[]=%s", url.QueryEscape(match))

		resp, err := relayProxyRequest(ctx, req.AccountId, nil, path)
		if err != nil {
			return nil, err
		}

		data, ok := resp["data"]
		if !ok {
			return nil, fmt.Errorf("missing data field in response")
		}

		metricsList, ok := data.([]interface{})
		if !ok {
			return nil, fmt.Errorf("data field is not a list")
		}

		output := make([]OutputMetrics, 0, len(metricsList))
		for _, v := range metricsList {
			m, ok := v.(string)
			if !ok {
				continue
			}
			output = append(output, OutputMetrics{Metric: m, Attributes: map[string]interface{}{}})
		}
		return output, nil
	}

	resp, err := relayRequest(ctx, req.AccountId, "prometheus_labels", map[string]any{"label_name": "__name__"})
	if err != nil {
		return nil, err
	}

	result, err := parseRelayData(resp)
	if err != nil {
		return nil, err
	}

	metrics, _, err := decodeInnerData(result)
	if err != nil {
		return nil, err
	}

	output := make([]OutputMetrics, len(metrics))
	for i, m := range metrics {
		output[i] = OutputMetrics{Metric: m, Attributes: map[string]interface{}{}}
	}
	return output, nil
}

func (s *ChronosphereMetricSource) FetchMetricsQuery(
	ctx *security.RequestContext,
	req FetchMetricsRequest,
) (OutputMetricQuery, error) {
	instant := false
	if v, ok := req.Request["instant"].(bool); ok {
		instant = v
	}

	filteredQueries := make(map[string]string, len(req.Queries))
	for k, v := range req.Queries {
		injected, err := injectPromQLMatchers(v, nil, req.Labels)
		if err != nil {
			return OutputMetricQuery{}, err
		}
		filteredQueries[k] = injected
	}

	externalAppMap, err := relay.ExecutePrometheus(
		req.AccountId,
		time.Unix(req.StartTime/1000, 0).UTC(),
		time.Unix(req.EndTime/1000, 0).UTC(),
		filteredQueries,
		instant,
	)
	if err != nil {
		return OutputMetricQuery{}, err
	}

	output := OutputMetricQuery{Results: make([]QueryResult, 0, len(externalAppMap))}
	for queryKey, query := range externalAppMap {
		switch q := query.(type) {
		case map[string]any: // Range query
			if seriesListResult, ok := q["series_list_result"].([]any); ok {
				results := make([]Result, 0, len(seriesListResult))
				for _, seriesAny := range seriesListResult {
					seriesMap, ok := seriesAny.(map[string]any)
					if !ok {
						continue
					}
					metricData, metricOK := seriesMap["metric"].(map[string]any)
					timestampsData, timestampsOK := seriesMap["timestamps"].([]any)
					valuesData, valuesOK := seriesMap["values"].([]any)

					if !metricOK || !timestampsOK || !valuesOK {
						continue
					}

					results = append(results, Result{
						Metric:     toStringMap(metricData),
						Timestamps: toInt64Slice(timestampsData),
						Values:     toFloat64Slice(valuesData),
					})
				}
				output.Results = append(output.Results, QueryResult{
					QueryKey: queryKey,
					Payload:  results,
				})
			}
		case []any: // Instant query
			results := make([]Result, 0, len(q))
			for _, vecAny := range q {
				vecMap := vecAny.(map[string]any)
				metric := toStringMap(vecMap["metric"].(map[string]any))

				var ts []int64
				var vals []float64
				if val, ok := vecMap["value"].([]any); ok && len(val) == 2 {
					t, tOK := val[0].(float64)
					s, sOK := val[1].(string)
					if tOK && sOK {
						if f, err := strconv.ParseFloat(s, 64); err == nil {
							ts = []int64{int64(t)}
							vals = []float64{f}
						}
					}
				}
				results = append(results, Result{
					Metric:     metric,
					Timestamps: ts,
					Values:     vals,
				})
			}
			output.Results = append(output.Results, QueryResult{
				QueryKey: queryKey,
				Payload:  results,
			})
		}
	}
	return output, nil
}

func (s *ChronosphereTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{}
}

func (s *ChronosphereTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_regex"}
}

func (c *ChronosphereTraceSource) CountTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

func (s *ChronosphereTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, fetchHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
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
		return nil, fmt.Errorf("failed to call label values API: %w", err)
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

func (s *ChronosphereTraceSource) QueryGroupedTracesCount(sc *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{Count: -1}, nil
}

func (c *ChronosphereTraceSource) GetLabelValues(ctx *security.RequestContext, fetchTraceRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("Chronosphere.OpenTelemetryTraceLabelValues unimplemented")
}

func (s *ChronosphereTraceSource) GetQuery(sc *security.RequestContext, tracesRequest TracesV3Request) (string, error) {
	return "", fmt.Errorf("Chronosphere.GetQuery unimplemented")
}
func (m ChronosphereTraceSource) normalizeTraceIDResponse(id string) string {
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

func (m ChronosphereTraceSource) extractTagsAsJSON(span map[string]any) map[string]string {
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

func (m ChronosphereTraceSource) extractAttributeValue(span map[string]any, targetKeys []string, defaultValue string) string {
	return m.findAttributeStringValue(span, targetKeys, defaultValue)
}

func (m ChronosphereTraceSource) extractStatusCode(span map[string]any) string {
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

func (m ChronosphereTraceSource) convertNanoToTime(nanoTimestamp string) (time.Time, error) {
	nanoTime, err := strconv.ParseInt(nanoTimestamp, 10, 64)
	if err != nil {
		return time.Time{}, err
	}

	// Convert nanoseconds to time.Time
	return time.Unix(0, nanoTime), nil
}

func (m ChronosphereTraceSource) extractServiceNamespaceFromAttributes(span map[string]any) string {
	return m.findAttributeStringValue(span, []string{"service.namespace", "k8s.namespace.name"}, "")
}

func (m ChronosphereTraceSource) extractServiceNameFromAttributes(span map[string]any) string {
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

func (m ChronosphereTraceSource) findAttributeStringValue(span map[string]any, targetKeys []string, defaultValue string) string {
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

func (m ChronosphereTraceSource) getStringField(data map[string]any, key, defaultValue string) string {
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
func (s *ChronosphereTraceSource) convertNanoToTimeString(nanoTimestamp string) string {
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

func (s *ChronosphereTraceSource) calculateDurationAsFloat(startTime, endTime string) float64 {
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

func (s *ChronosphereTraceSource) newChronosphereRows(sc *security.RequestContext, data map[string]any) ([]common.OpenTelemetryTrace, error) {
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
func (c *ChronosphereTraceSource) getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (c *ChronosphereTraceSource) handleTraceIdBatching(sc *security.RequestContext, chronosphereParams map[string]any, traceIds any, maxTraceIDs int, accountId string) ([]common.OpenTelemetryTrace, error) {
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
			"type", fmt.Sprintf("%T", traceIds))
		return []common.OpenTelemetryTrace{}, nil
	}

	slog.Info("Trace ID batching assessment",
		"total_trace_ids", len(traceIdsList),
		"max_allowed", maxTraceIDs,
		"batching_needed", len(traceIdsList) > maxTraceIDs)

	// If trace IDs count is within limit, no batching needed
	if len(traceIdsList) <= maxTraceIDs {
		slog.Info("Trace IDs within limit, no batching needed")
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
			"chunk_trace_ids_count", len(chunkTraceIds),
			"first_trace_id", func() string {
				if len(chunkTraceIds) > 0 {
					return chunkTraceIds[0]
				}
				return ""
			}())

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

func (c *ChronosphereTraceSource) handleChronosphereBatching(sc *security.RequestContext, chronosphereParams map[string]any, accountId string) ([]common.OpenTelemetryTrace, error) {
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
func (c *ChronosphereTraceSource) mapToChronosphereAPI(params map[string]any) map[string]any {
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
		"param_keys", c.getMapKeys(mappedParams))

	// Note: resource_filter, min_duration, max_duration, status_code are not supported
	// in the Chronosphere API based on the documentation provided

	return mappedParams
}

func (c *ChronosphereTraceSource) executeChronosphereChunk(sc *security.RequestContext, chunkParams map[string]any, accountID string) (map[string]any, error) {
	// Create request data for this chunk
	mappedParams := c.mapToChronosphereAPI(chunkParams)

	chronosphereTraceRequest := relay.ActionExecuteBody{
		AccountID:    accountID,
		ActionName:   "chronosphere_query_traces",
		ActionParams: mappedParams,
		NoSinks:      true,
	}

	respData, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body:    chronosphereTraceRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute signoz query: %w", err)
	}

	if code, ok := respData["status_code"].(float64); !ok || int(code) != 200 {
		sc.GetLogger().Error("Failed to get chronosphere list", "resp", respData)
		return nil, fmt.Errorf("failed to get chronosphere list, status code: %v", respData["status_code"])
	}
	// Check for pagination token in response (multiple possible field names)
	var nextToken string
	possibleTokenFields := []string{"next_token", "nextToken", "page_token", "pageToken", "continuation_token"}

	for _, field := range possibleTokenFields {
		if token, exists := respData[field].(string); exists && token != "" {
			nextToken = token
			slog.Info("Pagination token found",
				"field_name", field,
				"token_preview", token[:min(len(token), 20)],
				"traces_in_page", func() int {
					if traces, ok := respData["traces"].([]any); ok {
						return len(traces)
					}
					return 0
				}())
			break
		}
	}

	// Check if there are more results indicators
	if hasMore, exists := respData["has_more"].(bool); exists && hasMore {
		slog.Warn("Response indicates more results available but no pagination token found",
			"has_more", hasMore)
	}
	// Log the final data structure
	if traces, ok := respData["traces"].([]any); ok {
		slog.Info("Chunk response data extracted",
			"traces_count", len(traces),
			"data_keys", c.getMapKeys(respData))
	} else if dataWrapper, ok := respData["data"].(map[string]any); ok {
		// Handle nested response format: action=response with data.data containing traces
		if nestedData, ok := dataWrapper["data"].([]any); ok {
			slog.Info("Chunk response data extracted from nested structure",
				"traces_count", len(nestedData),
				"data_keys", c.getMapKeys(respData),
				"nested_data_keys", c.getMapKeys(dataWrapper))
			// Move nested data to top-level traces for consistent processing
			respData["traces"] = nestedData
		} else {
			slog.Warn("Nested data.data is not an array",
				"data_wrapper_keys", c.getMapKeys(dataWrapper))
		}
	} else {
		slog.Warn("Final data missing traces",
			"final_data_keys", c.getMapKeys(respData))
	}

	// Log complete response structure for debugging
	slog.Info("Complete Chronosphere response analysis",
		"response_keys", c.getMapKeys(respData),
		"traces_count", func() int {
			if traces, ok := respData["traces"].([]any); ok {
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
	return respData, nil

}

func (c *ChronosphereTraceSource) executeChunksInParallel(sc *security.RequestContext, chronosphereParams map[string]any, startTime, endTime time.Time, maxChunkDuration time.Duration, numChunks int, accountId string) ([]map[string]any, int) {
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

func (c *ChronosphereTraceSource) combineChronosphereResponses(responses []map[string]any) map[string]any {
	slog.Debug("Combining Chronosphere responses",
		"response_count", len(responses))

	if len(responses) == 0 {
		slog.Warn("No responses to combine, returning empty traces")
		return map[string]any{"traces": []any{}}
	}

	if len(responses) == 1 {
		slog.Debug("Single response, returning as-is")
		agentData, ok := responses[0]["data"].(map[string]any)
		if !ok {
			slog.Debug("Single response traces count")
		}

		data, ok := agentData["data"].(map[string]any)
		if !ok {
			slog.Warn("Unexpected 'data' field in response, expected 'traces'",
				"data_type", fmt.Sprintf("%T", agentData))
		}

		data, ok = data["data"].(map[string]any)
		if !ok {
			slog.Warn("Unexpected 'data' field in response, expected 'traces'",
				"data_type", fmt.Sprintf("%T", data))
		}

		return data
	}

	// Combine all traces from all responses
	allTraces := make([]any, 0)
	totalTracesBeforeCombine := 0

	for i, response := range responses {
		if response == nil {
			slog.Warn("Nil response in combination", "response_index", i)
			continue
		}

		agentData, ok := response["data"]
		if !ok {
			slog.Warn("Unexpected 'data' field in response, expected 'traces'",
				"response_index", i,
				"data_type", fmt.Sprintf("%T", agentData))
		}

		data, ok := agentData.(map[string]any)["data"].(map[string]any)
		if !ok {
			slog.Warn("Unexpected 'data' field in response, expected 'traces'",
				"response_index", i,
				"data_type", fmt.Sprintf("%T", data))
		}

		if traces, ok := data["traces"].([]any); ok {
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

func (c *ChronosphereTraceSource) QueryTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
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
		return nil, fmt.Errorf("failed to call label values API: %w", err)
	}
	return rows, nil
}

func (c *ChronosphereTraceSource) QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("grouped traces not supported for Chronosphere trace source")
}

func (s *ChronosphereMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_regex"}
}

func (s *ChronosphereMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return injectPromQLMatchers(q, req.LabelMatchers, req.Labels)
	}
	return "", nil
}
