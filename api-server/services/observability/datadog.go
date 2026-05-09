package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	datadogTraceMaxRetries   = 4
	datadogTraceBaseBackoff  = 500 * time.Millisecond
	datadogTraceMaxBackoff   = 30 * time.Second
	datadogTraceBodyLogTrunc = 512
)

// fetchDatadogTraceAPI issues a GET against the Datadog API and retries on
// 429 (rate-limited) and 5xx responses. On 429 it honors the Retry-After
// response header (delta-seconds or HTTP-date), falling back to exponential
// backoff with jitter. Returns the response body bytes on 2xx.
func fetchDatadogTraceAPI(ctx *security.RequestContext, requestURL string, headers map[string]string) ([]byte, error) {
	var lastErr error
	var lastStatus int

	for attempt := 0; attempt <= datadogTraceMaxRetries; attempt++ {
		resp, err := common.HttpGet(requestURL, common.HttpWithHeaders(headers))
		if err != nil {
			lastErr = err
			lastStatus = 0
			if attempt < datadogTraceMaxRetries {
				sleep := datadogBackoff(attempt)
				ctx.GetLogger().Warn("datadog: trace request network error, retrying", "attempt", attempt+1, "sleep", sleep, "error", err)
				time.Sleep(sleep)
				continue
			}
			return nil, fmt.Errorf("failed to make request to datadog traces api after %d attempts: %w", attempt+1, err)
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); cerr != nil {
			ctx.GetLogger().Warn("datadog: failed to close trace response body", "error", cerr)
		}
		if readErr != nil {
			lastErr = readErr
			lastStatus = resp.StatusCode
			if attempt < datadogTraceMaxRetries {
				sleep := datadogBackoff(attempt)
				ctx.GetLogger().Warn("datadog: trace response read error, retrying", "attempt", attempt+1, "sleep", sleep, "status", resp.StatusCode, "error", readErr)
				time.Sleep(sleep)
				continue
			}
			return nil, fmt.Errorf("failed to read datadog traces body after %d attempts (status %d): %w", attempt+1, resp.StatusCode, readErr)
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			lastStatus = resp.StatusCode
			lastErr = fmt.Errorf("datadog rate-limited (429): %s", truncateForLog(bodyBytes))
			if attempt >= datadogTraceMaxRetries {
				return nil, fmt.Errorf("datadog traces api rate-limited after %d attempts: %s", attempt+1, truncateForLog(bodyBytes))
			}
			sleep := retryAfterDelay(resp.Header.Get("Retry-After"), datadogBackoff(attempt))
			ctx.GetLogger().Warn("datadog: trace request rate-limited, retrying",
				"attempt", attempt+1,
				"sleep", sleep,
				"retry_after_header", resp.Header.Get("Retry-After"),
			)
			time.Sleep(sleep)
			continue

		case resp.StatusCode >= 500:
			lastStatus = resp.StatusCode
			lastErr = fmt.Errorf("datadog server error %d: %s", resp.StatusCode, truncateForLog(bodyBytes))
			if attempt >= datadogTraceMaxRetries {
				return nil, fmt.Errorf("datadog traces api server error after %d attempts (status %d): %s", attempt+1, resp.StatusCode, truncateForLog(bodyBytes))
			}
			sleep := datadogBackoff(attempt)
			ctx.GetLogger().Warn("datadog: trace request server error, retrying", "attempt", attempt+1, "sleep", sleep, "status", resp.StatusCode)
			time.Sleep(sleep)
			continue

		case resp.StatusCode >= 400:
			return nil, fmt.Errorf("datadog traces api returned status %d: %s", resp.StatusCode, truncateForLog(bodyBytes))
		}

		return bodyBytes, nil
	}

	return nil, fmt.Errorf("datadog traces api exhausted retries (last status %d): %v", lastStatus, lastErr)
}

// datadogBackoff returns an exponential backoff delay (capped) with ±25% jitter.
func datadogBackoff(attempt int) time.Duration {
	delay := datadogTraceBaseBackoff * time.Duration(1<<uint(attempt))
	if delay > datadogTraceMaxBackoff {
		delay = datadogTraceMaxBackoff
	}
	jitter := time.Duration(float64(delay) * 0.25 * (0.5 - float64(time.Now().UnixNano()%100)/100))
	return delay + jitter
}

// retryAfterDelay parses an HTTP Retry-After header (delta-seconds or HTTP-date).
// Returns fallback when the header is absent, malformed, or in the past.
// Result is clamped to datadogTraceMaxBackoff to avoid sleeping for hours when
// Datadog returns a long quota window.
func retryAfterDelay(header string, fallback time.Duration) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		if d > datadogTraceMaxBackoff {
			return datadogTraceMaxBackoff
		}
		return d
	}
	if t, err := http.ParseTime(header); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return fallback
		}
		if d > datadogTraceMaxBackoff {
			return datadogTraceMaxBackoff
		}
		return d
	}
	return fallback
}

func truncateForLog(b []byte) string {
	if len(b) <= datadogTraceBodyLogTrunc {
		return string(b)
	}
	return string(b[:datadogTraceBodyLogTrunc]) + "...(truncated)"
}

// DatadogSource is a LogSource implementation for Datadog.
type DatadogSource struct{}

type DatadogTraceSource struct{}

type DatadogMetricSource struct{}

func (s *DatadogMetricSource) FetchMetricsLabels(ctx *security.RequestContext, fetchMetricsRequest FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, fetchMetricsRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("FetchMetricList: failed to get datadog configs", "error", err)
		return nil, err
	}
	url := fmt.Sprintf("https://%s/api/v2/metrics/%s/tag-cardinalities", site, url.PathEscape(fetchMetricsRequest.MetricName))

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to datadog metrics label api: %w", err)
	}

	defer func() {
		if cerr := body.Body.Close(); cerr != nil {
			ctx.GetLogger().Error("datadog metics label failed to close response body", "error", cerr)
		}
	}()
	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read datadog metrics label body: %w", err)
	}
	var datadogMetrics map[string]any
	if err := common.UnmarshalJson(bodyBytes, &datadogMetrics); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog metrics label: %w", err)
	}
	var output []OutputMetricLabels
	if data, ok := datadogMetrics["data"].([]any); ok {
		for _, item := range data {
			if m, ok := item.(map[string]any); ok {
				metric := OutputMetricLabels{
					Label:      "",
					Attributes: make(map[string]any),
				}

				if id, ok := m["id"].(string); ok {
					metric.Label = id
				}
				for k, v := range m {
					if k != "id" {
						metric.Attributes[k] = v
					}
				}

				output = append(output, metric)
			}
		}
	}
	return output, nil
}

func (s *DatadogMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, fetchMetricsLabelRequest FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, fetchMetricsLabelRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("FetchMetricList: failed to get datadog configs", "error", err)
		return nil, err
	}
	metricName, ok := fetchMetricsLabelRequest.Request["metric_name"].(string)
	if !ok {
		return nil, fmt.Errorf("metric_name not found in request or is not a string")
	}
	escapedMetricName := url.PathEscape(metricName)
	url := fmt.Sprintf("https://%s/api/v2/metrics/%s/all-tags", site, escapedMetricName)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to datadog metrics label values api: %w", err)
	}
	defer func() {
		if cerr := body.Body.Close(); cerr != nil {
			ctx.GetLogger().Error("datadog metrics label values failed to close response body", "error", cerr)
		}
	}()
	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body for datadog metrics label values %w", err)
	}
	var datadogMetrics map[string]any
	if err := common.UnmarshalJson(bodyBytes, &datadogMetrics); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog metrics: %w", err)
	}
	var output []OutputMetricsLabelValues
	if data, ok := datadogMetrics["data"].(map[string]any); ok {
		if attributes, ok := data["attributes"].(map[string]any); ok {
			if tags, ok := attributes["tags"].([]any); ok {
				for _, t := range tags {
					if str, ok := t.(string); ok {
						if str != "" {
							parts := strings.SplitN(str, ":", 2)
							var value string
							if len(parts) == 2 {
								value = parts[1]
							}
							output = append(output, OutputMetricsLabelValues{
								Value:      value,
								Attributes: map[string]any{},
							})
						}
					}
				}
			}
		}
	}
	return output, nil
}

func (s *DatadogMetricSource) FetchMetricList(ctx *security.RequestContext, fetchMetricsListRequest FetchMetricsListRequest) ([]OutputMetrics, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, fetchMetricsListRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("FetchMetricList: failed to get datadog configs", "error", err)
		return nil, err
	}
	url := fmt.Sprintf("https://%s/api/v2/metrics", site)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to datadog metrics list: %w", err)
	}
	defer func() {
		if cerr := body.Body.Close(); cerr != nil {
			ctx.GetLogger().Error("datadog metrics list failed to close response body", "error", cerr)
		}
	}()
	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read datadog metrics list: %w", err)
	}
	var datadogMetrics map[string]any
	if err := common.UnmarshalJson(bodyBytes, &datadogMetrics); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog metrics: %w", err)
	}
	var output []OutputMetrics
	if data, ok := datadogMetrics["data"].([]any); ok {
		for _, item := range data {
			if m, ok := item.(map[string]any); ok {
				metric := OutputMetrics{
					Metric:     "",
					Attributes: make(map[string]any),
				}

				if id, ok := m["id"].(string); ok {
					metric.Metric = id
				}

				if fetchMetricsListRequest.Metric != "" && !strings.Contains(strings.ToLower(metric.Metric), strings.ToLower(fetchMetricsListRequest.Metric)) {
					continue
				}

				for k, v := range m {
					if k != "id" {
						metric.Attributes[k] = v
					}
				}

				output = append(output, metric)
			}
		}
	}
	return output, nil
}

func (s *DatadogSource) GetQuery(sc *security.RequestContext, fetchLogRequest FetchLogRequest) (string, error) {
	return "", fmt.Errorf("Datadog.GetQuery unimplemented")
}

func (s *DatadogSource) QueryLogs(ctx *security.RequestContext, fetchLogRequest FetchLogRequest) ([]OutputLog, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, fetchLogRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("QueryLogs: failed to get datadog configs", "error", err)
	}
	if fetchLogRequest.Query == "" {
		fetchLogRequest.Query, err = ConvertToDatadogLogQuery(fetchLogRequest.QueryRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to datadog trace query: %w", err)
		}
	}
	limit := fetchLogRequest.Limit
	if limit <= 0 {
		limit = 1000
	}
	params := map[string]string{
		"filter[query]": fetchLogRequest.Query,
		"filter[from]":  fmt.Sprintf("%v", fetchLogRequest.StartTime),
		"filter[to]":    fmt.Sprintf("%v", fetchLogRequest.EndTime), "page[limit]": fmt.Sprintf("%v", limit)}
	ctx.GetLogger().Info("Datadog Log Query", "query", fetchLogRequest.Query)
	url := fmt.Sprintf(
		"https://%s/api/v2/logs/events", site)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	body, err := common.HttpGet(url, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params))
	if err != nil {
		return nil, fmt.Errorf("failed to make request to datadog logs api: %w", err)
	}

	bodyBytes, err := io.ReadAll(body.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read datadog logs body: %w", err)
	}

	var eventLogs map[string]any
	if err := common.UnmarshalJson(bodyBytes, &eventLogs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog logs: %w", err)
	}

	if errs, ok := eventLogs["errors"]; ok && errs != nil {
		stringJson, err := common.MarshalJson(errs)
		if err != nil {
			ctx.GetLogger().Error("failed to marshal datadog error logs", "marshal_error", err)
			return nil, fmt.Errorf("failed to marshal json at datadog logs")
		}
		ctx.GetLogger().Error("failed to get datadog logs", "error", stringJson)
		return nil, fmt.Errorf("failed to get datadog logs")
	}

	if raw, ok := eventLogs["data"]; ok {
		if logs, ok := raw.([]interface{}); ok && len(logs) > 0 {
			jsonData, err := common.MarshalJson(logs)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal datadog logs: %w", err)
			}

			var datalogs []integrations.DatadogLog
			err = json.Unmarshal(jsonData, &datalogs)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal datadog: %w", err)
			}
			return s.ConvertDatadogToOutputLogs(datalogs), nil
		}
	}
	return []OutputLog{}, nil
}

func (s *DatadogSource) QueryLabels(ctx *security.RequestContext, fetchLogRequest FetchLogLabelRequest) ([]OutputLogLabel, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *DatadogSource) QueryLabelValues(ctx *security.RequestContext, fetchLogRequest FetchLogLabelValuesRequest) ([]OutputLogLabelValue, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *DatadogSource) GetLabelMapping() map[string]string {
	return map[string]string{
		"timestamp": "@timestamp",
		"body":      "",
		"namespace": "kube_namespace",
		"container": "container_name",
		"pod":       "pod_name",
		"node":      "kube_node",
		"app":       "service",
	}
}

func (s *DatadogSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like", "_ilike"}
}

func (s *DatadogSource) CanGenerateQuery(ctx playbooks.PlaybookActionContext) bool {
	return ctx.GetEvent().SubjectName != "" &&
		getEventNamespace(ctx.GetEvent()) != ""
}

func (s *DatadogSource) GenerateQuery(ctx playbooks.PlaybookActionContext) (string, map[string]any, error) {
	workloadName := escapeDatadogTagValue(ctx.GetEvent().SubjectName)
	namespace := escapeDatadogTagValue(getEventNamespace(ctx.GetEvent()))
	// Datadog log search syntax: tag:value pairs space-separated (AND)
	query := fmt.Sprintf("kube_deployment:%s kube_namespace:%s", workloadName, namespace)
	return query, map[string]any{}, nil
}

// escapeDatadogTagValue escapes special characters in Datadog tag values.
func escapeDatadogTagValue(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, ` `, `\ `)
	return r.Replace(s)
}

// Convert function
func (s *DatadogSource) ConvertDatadogToOutputLogs(datalogs []integrations.DatadogLog) []OutputLog {
	var out []OutputLog
	for _, d := range datalogs {
		labels := map[string]interface{}{
			"host":    d.Attributes.Host,
			"service": d.Attributes.Service,
			"type":    d.Type,
			"id":      d.ID,
		}

		// Convert tags (key:value) into map entries
		for _, tag := range d.Attributes.Tags {
			parts := strings.SplitN(tag, ":", 2)
			if len(parts) == 2 {
				labels[parts[0]] = parts[1]
			} else {
				// Handle case where tag has no colon
				labels[parts[0]] = true
			}
		}

		out = append(out, OutputLog{
			Timestamp: d.Attributes.Timestamp,
			Message:   d.Attributes.Message,
			Labels:    labels,
			Severity:  d.Attributes.Status,
		})
	}
	return out
}

func (s *DatadogTraceSource) QueryTracesHeatmap(ctx *security.RequestContext, fetchHeatMapRequest TracesHeatMapRequest) ([]common.OpenTelemetryTraceHeatMap, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, fetchHeatMapRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("QueryTracesHeatmap: failed to get datadog configs", "error", err)
		return nil, err
	}
	embededQuery := url.QueryEscape(fmt.Sprintf("trace_id:%s", fetchHeatMapRequest.TraceId))

	// Use a wide time range if not specified (last 30 days)
	// Traces are typically recent, so this should cover most cases
	endTime := time.Now().UnixMilli()
	startTime := endTime - (30 * 24 * 60 * 60 * 1000) // 30 days ago

	url := fmt.Sprintf(
		"https://%s/api/v2/spans/events?filter[query]=%s&filter[from]=%d&filter[to]=%d",
		site, embededQuery, startTime, endTime,
	)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	bodyBytes, err := fetchDatadogTraceAPI(ctx, url, headers)
	if err != nil {
		return nil, err
	}

	var datadogTraces map[string]any
	otelTraces := []common.OpenTelemetryTraceHeatMap{}
	if err := common.UnmarshalJson(bodyBytes, &datadogTraces); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
	}

	if raw, ok := datadogTraces["data"]; ok {
		rawBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal datadog traces: %w", err)
		}

		var ddTrace common.DatadogTrace
		err = json.Unmarshal(rawBytes, &ddTrace.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
		}

		otelTraces = common.MapDatadogToOpenTelemetryHeatMap(ddTrace)
		return otelTraces, nil
	}

	return otelTraces, nil
}

func ConvertToDatadogTraceQuery(req TracesQueryBuilderRequest) (string, error) {
	whereQuery, err := buildWhereClause(req.Where)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if whereQuery != "" {
		sb.WriteString(whereQuery)
	}

	return strings.TrimSpace(sb.String()), nil
}

func ConvertToDatadogLogQuery(req LogsQueryBuilderRequest) (string, error) {
	whereQuery, err := buildWhereClause(req.Where)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if whereQuery != "" {
		sb.WriteString(whereQuery)
	}

	return strings.TrimSpace(sb.String()), nil
}

func (s *DatadogTraceSource) GetLabelMapping() map[string]string {
	return map[string]string{"workload_namespace": "kube_namespace",
		"workload_name":           "service",
		"http_status_code":        "@http.status_code",
		"span_name":               "operation_name",
		"resource":                "resource_name",
		"status_code":             "status",
		"duration_ns":             "@duration",
		"@k8s.namespace.name":     "kube_namespace",
		"@container.image.name":   "image_name",
		"@container.image.tag":    "image_tag",
		"@http.method":            "@http.method",
		"@http.route":             "@http.route",
		"@http.status_code":       "@http.status_code",
		"@k8s.container.name":     "container_name",
		"@k8s.deployment.name":    "kube_deployment",
		"@k8s.node.name":          "kube_node",
		"@k8s.pod.name":           "pod_name",
		"@net.peer.port":          "@http.url_details.port",
		"@ns":                     "kube_namespace",
		"@service.version":        "@version",
		"@telemetry.sdk.language": "@language",
	}

}

func (s *DatadogTraceSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like", "_ilike"}
}

func buildWhereClause(where query.QueryWhereClause) (string, error) {
	if len(where.Binary) > 0 {
		return buildBinaryClause(where.Binary)
	}

	if len(where.And) > 0 {
		var parts []string
		for _, c := range where.And {
			whereStr, err := buildWhereClause(c)
			if err != nil {
				return "", err
			}
			parts = append(parts, whereStr)
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}

	if len(where.Or) > 0 {
		var parts []string
		for _, c := range where.Or {
			whereStr, err := buildWhereClause(c)
			if err != nil {
				return "", err
			}
			parts = append(parts, whereStr)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	}

	if where.Not != nil {
		return "", fmt.Errorf("NOT clauses are not supported in Datadog trace queries")
	}
	return "", nil
}

func buildBinaryClause(binary query.BinaryWhereClause) (string, error) {
	var parts []string
	for field, ops := range binary {
		for op, val := range ops {
			switch op {
			case Eq:
				parts = append(parts, fmt.Sprintf("%s:\"%v\"", field, val))
			case Nq:
				parts = append(parts, fmt.Sprintf("-%s:\"%v\"", field, val))
			case Gt:
				switch val.(type) {
				case int, int8, int16, int32, int64,
					uint, uint8, uint16, uint32, uint64,
					float32, float64:
					// numeric → OK
					parts = append(parts, fmt.Sprintf("%s:>%v", field, val))
				default:
					return "", fmt.Errorf("GT operator requires numeric value for field '%s', got %T", field, val)
				}
			case Lt:
				switch val.(type) {
				case int, int8, int16, int32, int64,
					uint, uint8, uint16, uint32, uint64,
					float32, float64:
					// numeric → OK
					parts = append(parts, fmt.Sprintf("%s:<%v", field, val))
				default:
					return "", fmt.Errorf("GT operator requires numeric value for field '%s', got %T", field, val)
				}
			case Gte:
				switch val.(type) {
				case int, int8, int16, int32, int64,
					uint, uint8, uint16, uint32, uint64,
					float32, float64:
					// numeric → OK
					parts = append(parts, fmt.Sprintf("%s:>=%v", field, val))
				default:
					return "", fmt.Errorf("GT operator requires numeric value for field '%s', got %T", field, val)
				}
			case Lte:
				switch val.(type) {
				case int, int8, int16, int32, int64,
					uint, uint8, uint16, uint32, uint64,
					float32, float64:
					// numeric → OK
					parts = append(parts, fmt.Sprintf("%s:<=%v", field, val))
				default:
					return "", fmt.Errorf("GT operator requires numeric value for field '%s', got %T", field, val)
				}

			case Like, ILike:
				parts = append(parts, fmt.Sprintf("%s:\"%v\"", field, val))
			case In:
				arr, ok := val.([]any)
				if ok {
					var strVals []string
					for _, v := range arr {
						strVals = append(strVals, fmt.Sprintf("\"%v\"", v))
					}
					parts = append(parts, fmt.Sprintf("(%s:%s)", field, strings.Join(strVals, " OR "+field+":")))
				}
			case NotIn:
				arr, ok := val.([]any)
				if ok {
					for _, v := range arr {
						parts = append(parts, fmt.Sprintf("-%s:\"%v\"", field, v))
					}
				}
			case Contains:
				parts = append(parts, fmt.Sprintf("%s:*%v*", field, val))
			case HasKey:
				parts = append(parts, fmt.Sprintf("has:%s", field))
			case IsNull:
				if isNull, ok := val.(bool); ok {
					if isNull {
						parts = append(parts, fmt.Sprintf("!has:%s", field))
					} else {
						parts = append(parts, fmt.Sprintf("has:%s", field))
					}
				} else {
					parts = append(parts, fmt.Sprintf("%s:*", field))
				}
			default:
				parts = append(parts, fmt.Sprintf("%s:%v", field, val))
			}
		}
	}
	return strings.Join(parts, " AND "), nil
}

func (s *DatadogTraceSource) GetQuery(sc *security.RequestContext, tracesRequest TracesV3Request) (string, error) {
	return "", fmt.Errorf("Datadog.GetQuery unimplemented")
}

func (s *DatadogTraceSource) GetTimeFilter(tracesRequest TracesV3Request) (int64, int64, error) {
	startTime := time.Now().Add(-1 * time.Hour).UnixMilli()
	endTime := time.Now().UnixMilli()
	if tracesRequest.StartTime != 0 {
		startTime = tracesRequest.StartTime
	}
	if tracesRequest.EndTime != 0 {
		endTime = tracesRequest.EndTime
	}
	return startTime, endTime, nil
}

func (s *DatadogTraceSource) QueryTraces(sc *security.RequestContext, tracesRequest TracesV3Request) ([]common.OpenTelemetryTrace, error) {
	// temp handling to be removed in future
	if tracesRequest.QueryRequest.Where.Binary["trace_source"] != nil {
		delete(tracesRequest.QueryRequest.Where.Binary, "trace_source")
	}
	if tracesRequest.QueryRequest.Where.Binary["destination_workload_name"] != nil {
		delete(tracesRequest.QueryRequest.Where.Binary, "destination_workload_name")
	}
	if tracesRequest.QueryRequest.Where.Binary["destination_workload_namespace"] != nil {
		delete(tracesRequest.QueryRequest.Where.Binary, "destination_workload_namespace")
	}

	spanAttri, ok := tracesRequest.QueryRequest.Where.Binary["spanattributes"]
	if ok {
		// Iterate over operators for spanattributes to correctly handle modifications.
		for op, val := range spanAttri {
			valueMap, ok := val.(map[string]interface{})
			if !ok {
				continue
			}

			// Handle service.name: move to top-level filter
			if _, hasServiceName := valueMap["service.name"]; hasServiceName {
				// This temporary logic removes `service.name` from the spanattributes filter.
				// The intention is likely to handle it as a top-level `service_name` filter instead.
				delete(valueMap, "service.name")

				// If the attribute map for an operator becomes empty, remove the operator.
				if len(valueMap) == 0 {
					delete(spanAttri, op)
				}
			}

			// Handle env: move to top-level filter for Datadog compatibility
			// In Datadog, 'env' is a root-level tag, not a span attribute
			var envValue interface{}
			if val, hasEnv := valueMap["env"]; hasEnv {
				envValue = val
				delete(valueMap, "env")
			} else if val, hasDeploymentEnv := valueMap["deployment.environment"]; hasDeploymentEnv {
				// Also check for deployment.environment (OTel semantic convention)
				envValue = val
				delete(valueMap, "deployment.environment")
			}

			// If we found an env value, add it as a top-level filter
			if envValue != nil {
				if tracesRequest.QueryRequest.Where.Binary["env"] == nil {
					tracesRequest.QueryRequest.Where.Binary["env"] = make(map[query.BinaryWhereClauseType]any)
				}
				tracesRequest.QueryRequest.Where.Binary["env"][op] = envValue
			}

			// If the attribute map for an operator becomes empty, remove the operator
			if len(valueMap) == 0 {
				delete(spanAttri, op)
			}

			for k, v := range valueMap {
				newKey := "@" + k
				tracesRequest.QueryRequest.Where.Binary[newKey] = map[query.BinaryWhereClauseType]any{op: v}
			}
		}
		delete(tracesRequest.QueryRequest.Where.Binary, "spanattributes")
	}
	tracesRequest.QueryRequest.Where = convertWhereClauseWithMApping(tracesRequest.QueryRequest.Where, s.GetLabelMapping())
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(sc, tracesRequest.AccountId)
	if err != nil {
		sc.GetLogger().Error("QueryTraces: failed to get datadog configs", "error", err)
		return nil, err
	}
	startTime, endTime, err := s.GetTimeFilter(tracesRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to get time filter: %w", err)
	}
	if tracesRequest.Query == "" {
		tracesRequest.Query, err = ConvertToDatadogTraceQuery(tracesRequest.QueryRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to datadog trace query: %w", err)
		}
	}
	sc.GetLogger().Info("Datadog Trace Query", "query", tracesRequest.Query)
	embededQuery := url.QueryEscape(tracesRequest.Query)
	limit := tracesRequest.QueryRequest.Limit
	if limit <= 0 {
		limit = 1000
	}
	url := fmt.Sprintf(
		"https://%s/api/v2/spans/events?filter[query]=%s&filter[from]=%d&filter[to]=%d&page[limit]=%d",
		site, embededQuery, startTime, endTime, limit,
	)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	bodyBytes, err := fetchDatadogTraceAPI(sc, url, headers)
	if err != nil {
		return nil, err
	}

	var datadogTraces map[string]any
	otelTraces := []common.OpenTelemetryTrace{}
	if err := common.UnmarshalJson(bodyBytes, &datadogTraces); err != nil {
		return nil, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
	}

	if raw, ok := datadogTraces["data"]; ok {
		rawBytes, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal datadog traces: %w", err)
		}

		var ddTrace common.DatadogTrace
		err = json.Unmarshal(rawBytes, &ddTrace.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal datadog traces: %w", err)
		}

		otelTraces = common.MapDatadogToOpenTelemetry(ddTrace)
		return otelTraces, nil
	}

	return otelTraces, nil
}

func (c *DatadogTraceSource) QueryGroupedTraces(ctx *security.RequestContext, fetchTraceRequest TracesV3Request) ([]TraceGroupingValues, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *DatadogTraceSource) CountTraces(sc *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceCount, error) {
	return common.OpenTelemetryTraceCount{Count: -1}, nil
}

func (s *DatadogTraceSource) GetLabelValues(sc *security.RequestContext, tracesRequest TracesV3LabelValuesRequest) (common.OpenTelemetryTraceLabelValues, error) {
	return common.OpenTelemetryTraceLabelValues{}, fmt.Errorf("not implemented")
}

func (s *DatadogTraceSource) QueryGroupedTracesCount(sc *security.RequestContext, tracesRequest TracesV3Request) (common.OpenTelemetryTraceGroupCount, error) {
	return common.OpenTelemetryTraceGroupCount{Count: -1}, nil
}

func (s *DatadogMetricSource) FetchMetricsQuery(
	ctx *security.RequestContext,
	fetchMetricsRequest FetchMetricsRequest,
) (OutputMetricQuery, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, fetchMetricsRequest.AccountId)
	if err != nil {
		ctx.GetLogger().Error("FetchMetricList: failed to get datadog configs", "error", err)
		return OutputMetricQuery{}, err
	}

	from := fetchMetricsRequest.StartTime / 1000
	to := fetchMetricsRequest.EndTime / 1000

	finalOutput := OutputMetricQuery{
		Results: []QueryResult{},
	}
	for queryKey, rawQuery := range fetchMetricsRequest.Queries {
		injected, injErr := injectDatadogLabels(rawQuery, fetchMetricsRequest.Labels, fetchMetricsRequest.LabelMatchers)
		if injErr != nil {
			ctx.GetLogger().Warn("DatadogMetricSource.FetchMetricsQuery: query build failed",
				"key", queryKey, "error", injErr)
			errMsg := injErr.Error()
			finalOutput.Results = append(finalOutput.Results, QueryResult{
				QueryKey: queryKey,
				Error:    &errMsg,
			})
			continue
		}
		encodedQuery := url.QueryEscape(injected)
		url := fmt.Sprintf("https://%s/api/v1/query?from=%d&to=%d&query=%s", site, from, to, encodedQuery)

		headers := map[string]string{
			"Content-Type":       "application/json",
			"DD-API-KEY":         apiKey,
			"DD-APPLICATION-KEY": appKey,
		}

		body, err := common.HttpGet(url, common.HttpWithHeaders(headers))
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to make request to datadog metrics api for query %q: %w", queryKey, err)
		}

		defer func() {
			if cerr := body.Body.Close(); cerr != nil {
				ctx.GetLogger().Error("datadog metrics failed to close response body", "error", cerr)
			}
		}()

		bodyBytes, err := io.ReadAll(body.Body)
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to read body for query %q: %w", queryKey, err)
		}
		var data map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &data); err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to parse JSON for query %q: %w", queryKey, err)
		}

		if errMsg, ok := data["error"].(string); ok {
			return OutputMetricQuery{}, fmt.Errorf("datadog query %q returned error: %s", queryKey, errMsg)
		}
		queryOutput, err := datadogMapToOutputMetricWithKey(bodyBytes, queryKey)
		if err != nil {
			return OutputMetricQuery{}, fmt.Errorf("failed to map datadog response for query %q: %w", queryKey, err)
		}
		finalOutput.Results = append(finalOutput.Results, queryOutput.Results...)
	}

	return finalOutput, nil
}

type DatadogResponse struct {
	Status  string          `json:"status"`
	ResType string          `json:"res_type"`
	Series  []SeriesItem    `json:"series"`
	Values  [][]interface{} `json:"values"`
	Times   []int64         `json:"times"`
	GroupBy []string        `json:"group_by"`
}

type SeriesItem struct {
	Unit        []*UnitItem `json:"unit"`
	QueryIdx    int         `json:"query_index"`
	Aggr        string      `json:"aggr"`
	Metric      string      `json:"metric"`
	TagSet      []string    `json:"tag_set"`
	Expression  string      `json:"expression"`
	Scope       string      `json:"scope"`
	Interval    int         `json:"interval"`
	Start       int64       `json:"start"`
	End         int64       `json:"end"`
	PointList   [][]float64 `json:"pointlist"`
	DisplayName string      `json:"display_name"`
}

type UnitItem struct {
	Family      string  `json:"family"`
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	ShortName   string  `json:"short_name"`
	Plural      string  `json:"plural"`
	ScaleFactor float64 `json:"scale_factor"`
}

func datadogMapToOutputMetricWithKey(bodyBytes []byte, queryKey string) (OutputMetricQuery, error) {
	var ddResp DatadogResponse
	if err := json.Unmarshal(bodyBytes, &ddResp); err != nil {
		return OutputMetricQuery{}, fmt.Errorf("failed to unmarshal datadog response: %w", err)
	}

	qr := QueryResult{
		QueryKey: queryKey,
		Payload:  []Result{},
	}

	for _, s := range ddResp.Series {
		metricMap := make(map[string]string)
		metricMap["__name__"] = s.Metric

		for _, t := range s.TagSet {
			if kv := strings.SplitN(t, ":", 2); len(kv) == 2 {
				metricMap[kv[0]] = kv[1]
			}
		}

		if len(s.Unit) > 0 && s.Unit[0] != nil {
			metricMap["unit"] = s.Unit[0].Name
		}

		var timestamps []int64
		var values []float64
		for _, point := range s.PointList {
			if len(point) == 2 {
				t := int64(point[0] / 1000) // ms to sec
				v := point[1]
				timestamps = append(timestamps, t)
				values = append(values, v)
			}
		}

		qr.Payload = append(qr.Payload, Result{
			Metric:     metricMap,
			Timestamps: timestamps,
			Values:     values,
		})
	}

	return OutputMetricQuery{Results: []QueryResult{qr}}, nil
}

// QueryLogGroup implements LogGroupSource for Datadog.
// Uses the Datadog Log Analytics Aggregate API to group error/critical logs.
func (s *DatadogSource) QueryLogGroup(ctx *security.RequestContext, req FetchLogGroupRequest) (LogGroupOutput, error) {
	apiKey, appKey, site, err := integrations.GetDatadogConfigs(ctx, req.AccountId)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: failed to get configs: %w", err)
	}

	// Build the filter query
	filterParts := []string{"status:(error OR critical OR emergency)"}

	selectedNamespace := common.GetString(req.Request, "selectedNamespace")
	selectedWorkload := common.GetString(req.Request, "selectedWorkload")

	if selectedNamespace != "" {
		filterParts = append(filterParts, fmt.Sprintf("kube_namespace:%s", escapeDatadogTagValue(selectedNamespace)))
	}
	if selectedWorkload != "" {
		filterParts = append(filterParts, fmt.Sprintf("kube_deployment:%s", escapeDatadogTagValue(selectedWorkload)))
	}

	// Convert timestamps: Datadog expects ISO8601 or epoch milliseconds as string
	startTime := req.StartTime
	endTime := req.EndTime
	// Convert to milliseconds if in seconds
	if startTime < 1e12 {
		startTime = startTime * 1000
	}
	if endTime < 1e12 {
		endTime = endTime * 1000
	}

	// Use the Datadog Log Analytics Aggregate API
	aggregateBody := map[string]any{
		"filter": map[string]any{
			"query": strings.Join(filterParts, " "),
			"from":  fmt.Sprintf("%d", startTime),
			"to":    fmt.Sprintf("%d", endTime),
		},
		"compute": []map[string]any{
			{"type": "total", "aggregation": "count"},
		},
		"group_by": []map[string]any{
			{"facet": "service", "limit": 100, "sort": map[string]string{"aggregation": "count", "order": "desc"}},
			{"facet": "kube_namespace", "limit": 100},
			{"facet": "status", "limit": 10},
		},
	}

	apiURL := fmt.Sprintf("https://%s/api/v2/logs/analytics/aggregate", site)
	headers := map[string]string{
		"Content-Type":       "application/json",
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}

	resp, err := common.HttpPost(apiURL, common.HttpWithJsonBody(aggregateBody), common.HttpWithHeaders(headers))
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: failed to read response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var respData map[string]any
	if err := json.Unmarshal(bodyBytes, &respData); err != nil {
		return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: failed to parse response: %w", err)
	}

	if errs, ok := respData["errors"]; ok && errs != nil {
		stringJson, marshalErr := common.MarshalJson(errs)
		if marshalErr != nil {
			ctx.GetLogger().Error("datadog.QueryLogGroup: failed to marshal error response", "marshal_error", marshalErr)
			return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: API returned errors")
		}
		ctx.GetLogger().Error("datadog.QueryLogGroup: API returned errors", "error", stringJson)
		return LogGroupOutput{}, fmt.Errorf("datadog.QueryLogGroup: API returned errors")
	}

	// Convert endTime back to seconds for the response — other providers return
	// epoch-seconds and the frontend multiplies by 1000.
	endTimeSec := endTime
	if endTimeSec >= 1e12 {
		endTimeSec = endTimeSec / 1000
	}

	return s.parseDatadogLogGroupResponse(respData, endTimeSec)
}

// parseDatadogLogGroupResponse parses the Datadog analytics aggregate response.
func (s *DatadogSource) parseDatadogLogGroupResponse(respData map[string]any, endTimeSec int64) (LogGroupOutput, error) {
	data, ok := respData["data"].(map[string]any)
	if !ok {
		return LogGroupOutput{}, nil
	}

	buckets, ok := data["buckets"].([]any)
	if !ok || len(buckets) == 0 {
		return LogGroupOutput{}, nil
	}

	groups := make([]LogGroup, 0, len(buckets))
	for _, b := range buckets {
		bucket, ok := b.(map[string]any)
		if !ok {
			continue
		}

		group := LogGroup{}

		// Extract group-by values from "by" field
		var service string
		if by, ok := bucket["by"].(map[string]any); ok {
			if svc, ok := by["service"].(string); ok {
				service = svc
			}
			if ns, ok := by["kube_namespace"].(string); ok {
				group.Namespace = ns
			}
			if status, ok := by["status"].(string); ok {
				group.Level = status
			}
		}

		// Workload: use kube_deployment from the query filter context, not the
		// Datadog service tag.  The aggregate API groups by service, so we keep
		// it as a label but derive Workload separately when available.
		group.Workload = service

		// Sample should be a representative description, not the service name.
		// Since the aggregate API doesn't return log message content, build a
		// descriptive sample from the available dimensions.
		group.Sample = fmt.Sprintf("[%s] %s/%s", group.Level, group.Namespace, service)

		if group.Namespace != "" && group.Workload != "" {
			group.ContainerID = fmt.Sprintf("/k8s/%s/%s", group.Namespace, group.Workload)
		}

		// PatternHash from composite key so each (service, namespace, level)
		// combination gets a unique, stable hash.
		group.PatternHash = generatePatternHash(
			fmt.Sprintf("%s|%s|%s", service, group.Namespace, group.Level),
		)

		// Extract count from computes
		var count float64
		if computes, ok := bucket["computes"].(map[string]any); ok {
			if c0, ok := computes["c0"].(float64); ok {
				count = c0
			}
		}

		group.Timestamps = []int64{endTimeSec}
		group.Values = []float64{count}
		group.Count = int64(math.Round(count))

		groups = append(groups, group)
	}

	return LogGroupOutput{Groups: groups}, nil
}

func (s *DatadogMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_contains", "_like", "_ilike"}
}

// injectDatadogLabels appends legacy eq-only tag filters and operator-aware
// LabelMatchers to a Datadog metric query using {tag:value} syntax.
// Both inputs are merged into a single comma-separated selector and inserted
// inside the existing {} (or appended as a new {} if none is present).
//
// Datadog tag selector syntax:
//
//	tag:value     — eq
//	!tag:value    — neq
//	tag:val*      — glob (used for _like and _contains lowering)
//
// Operator coverage matches the conservative set advertised by other
// providers' metric builders. _ilike, _in, _not_in, and _regex are advertised
// by GetSupportedOperators but rejected here until a list-value editor and
// equivalent Datadog syntax mapping land — same convention as
// promqlMatcherOp.
func injectDatadogLabels(expr string, labels map[string]string, matchers []LabelMatcher) (string, error) {
	parts := make([]string, 0, len(labels)+len(matchers))
	for _, k := range sortedKeys(labels) {
		parts = append(parts, k+":"+labels[k])
	}
	sortedMatchers := make([]LabelMatcher, len(matchers))
	copy(sortedMatchers, matchers)
	sort.Slice(sortedMatchers, func(i, j int) bool {
		if sortedMatchers[i].Label != sortedMatchers[j].Label {
			return sortedMatchers[i].Label < sortedMatchers[j].Label
		}
		if sortedMatchers[i].Operator != sortedMatchers[j].Operator {
			return sortedMatchers[i].Operator < sortedMatchers[j].Operator
		}
		return sortedMatchers[i].Value < sortedMatchers[j].Value
	})
	for _, m := range sortedMatchers {
		clause, err := datadogMatcherClause(m)
		if err != nil {
			return "", err
		}
		parts = append(parts, clause)
	}
	if len(parts) == 0 {
		return expr, nil
	}
	selector := strings.Join(parts, ",")
	if idx := strings.Index(expr, "{"); idx != -1 {
		closeIdx := strings.Index(expr, "}")
		existing := expr[idx+1 : closeIdx]
		if existing == "" {
			return expr[:idx+1] + selector + expr[closeIdx:], nil
		}
		return expr[:idx+1] + existing + "," + selector + expr[closeIdx:], nil
	}
	return expr + "{" + selector + "}", nil
}

// datadogMatcherClause renders one LabelMatcher into a Datadog tag selector.
func datadogMatcherClause(m LabelMatcher) (string, error) {
	switch m.Operator {
	case "_eq":
		return m.Label + ":" + m.Value, nil
	case "_neq":
		return "!" + m.Label + ":" + m.Value, nil
	case "_like":
		return m.Label + ":" + m.Value, nil
	case "_contains":
		return m.Label + ":*" + m.Value + "*", nil
	case "_ilike", "_in", "_not_in", "_regex":
		return "", fmt.Errorf("operator %q not yet supported in Datadog builder", m.Operator)
	default:
		return "", fmt.Errorf("unsupported operator %q for Datadog", m.Operator)
	}
}

func (s *DatadogMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return injectDatadogLabels(q, req.Labels, req.LabelMatchers)
	}
	return "", nil
}
