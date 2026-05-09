package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strconv"
	"strings"
	"time"
)

type PrometheusMetricSource struct{}

func (s *PrometheusMetricSource) GetSupportedOperators() []string {
	return []string{"_eq", "_neq", "_in", "_not_in", "_regex"}
}

func (s *PrometheusMetricSource) GetQuery(_ *security.RequestContext, req FetchMetricsRequest) (string, error) {
	for _, q := range req.Queries {
		return injectPromQLMatchers(q, req.LabelMatchers, req.Labels)
	}
	return "", nil
}

type PrometheusLogGroupSource struct{}

// QueryLogGroup implements LogGroupSource.
func (p *PrometheusLogGroupSource) QueryLogGroup(ctx *security.RequestContext, fetchLogGroupRequest FetchLogGroupRequest) (LogGroupOutput, error) {
	promQL := `increase(container_log_messages_total{ __CLUSTER__ level=~"critical|error", container_id!~".*(prometheus|grafana|kube-system|nudgebee-agent|containerd|kubelet).*"}[1h]) > 0`

	selectedNamespace := common.GetString(fetchLogGroupRequest.Request, "selectedNamespace")
	selectedWorkload := common.GetString(fetchLogGroupRequest.Request, "selectedWorkload")
	if selectedNamespace != "" && selectedWorkload != "" {
		promQL = fmt.Sprintf(
			`increase(container_log_messages_total{ __CLUSTER__ level=~"critical|error", container_id!~".*(prometheus|grafana|kube-system|nudgebee-agent|containerd|kubelet).*", container_id=~"/k8s/%s/%s.*"}[1h]) > 0`,
			selectedNamespace,
			selectedWorkload,
		)
	} else if selectedNamespace != "" {
		promQL = fmt.Sprintf(
			`increase(container_log_messages_total{ __CLUSTER__ level=~"critical|error", container_id!~".*(prometheus|grafana|kube-system|nudgebee-agent|containerd|kubelet).*", container_id=~"/k8s/%s/.*"}[1h]) > 0`,
			selectedNamespace,
		)
	} else if selectedWorkload != "" {
		promQL = fmt.Sprintf(
			`increase(container_log_messages_total{ __CLUSTER__ level=~"critical|error", container_id!~".*(prometheus|grafana|kube-system|nudgebee-agent|containerd|kubelet).*", container_id=~"/k8s/.*/%s.*"}[1h]) > 0`,
			selectedWorkload,
		)
	}
	response, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId: fetchLogGroupRequest.AccountId,
		Queries: map[string]string{
			"log_group": promQL,
		},
		StartTime: fetchLogGroupRequest.StartTime,
		EndTime:   fetchLogGroupRequest.EndTime,
		Instant:   false,
	})
	if err != nil {
		return LogGroupOutput{}, err
	}
	deduped := deduplicateLogGroups(response)
	for _, qr := range deduped.Results {
		if qr.Error != nil && *qr.Error != "" {
			return LogGroupOutput{}, fmt.Errorf("prometheus.QueryLogGroup: query %q failed: %s", qr.QueryKey, *qr.Error)
		}
	}
	return metricQueryToLogGroupOutput(deduped), nil
}

// metricQueryToLogGroupOutput converts the legacy OutputMetricQuery from Prometheus
// into the new LogGroupOutput format.
func metricQueryToLogGroupOutput(output OutputMetricQuery) LogGroupOutput {
	var groups []LogGroup
	for _, qr := range output.Results {
		for _, r := range qr.Payload {
			var total int64
			for _, v := range r.Values {
				total += int64(math.Round(v))
			}
			groups = append(groups, LogGroup{
				Sample:      r.Metric["sample"],
				Namespace:   r.Metric["namespace"],
				Workload:    r.Metric["workload"],
				Container:   r.Metric["container"],
				ContainerID: r.Metric["container_id"],
				PatternHash: r.Metric["pattern_hash"],
				Level:       r.Metric["level"],
				Count:       total,
				Timestamps:  r.Timestamps,
				Values:      r.Values,
			})
		}
	}
	return LogGroupOutput{Groups: groups}
}

// deduplicateLogGroups merges log group results that share the same
// app_id + pattern_hash + level but come from different pods. Values are
// summed element-wise across matching timestamps; the first sample text
// encountered is kept.
func deduplicateLogGroups(output OutputMetricQuery) OutputMetricQuery {
	for i, qr := range output.Results {
		type dedupKey struct{ appID, hash, level string }
		type entry struct {
			result Result
			tsIdx  map[int64]int // timestamp -> index in Timestamps/Values
		}

		merged := make(map[dedupKey]*entry)
		var order []dedupKey

		for _, r := range qr.Payload {
			key := dedupKey{
				appID: r.Metric["app_id"],
				hash:  r.Metric["pattern_hash"],
				level: r.Metric["level"],
			}

			existing, ok := merged[key]
			if !ok {
				metricCopy := make(map[string]string, len(r.Metric))
				for k, v := range r.Metric {
					if k == "container_id" || k == "pod" || k == "instance" {
						continue
					}
					metricCopy[k] = v
				}
				if appID := r.Metric["app_id"]; appID != "" {
					metricCopy["container_id"] = appID
				}
				tsIdx := make(map[int64]int, len(r.Timestamps))
				for j, ts := range r.Timestamps {
					tsIdx[ts] = j
				}
				tsCopy := make([]int64, len(r.Timestamps))
				copy(tsCopy, r.Timestamps)
				valsCopy := make([]float64, len(r.Values))
				copy(valsCopy, r.Values)
				merged[key] = &entry{
					result: Result{Metric: metricCopy, Timestamps: tsCopy, Values: valsCopy},
					tsIdx:  tsIdx,
				}
				order = append(order, key)
				continue
			}

			for j, ts := range r.Timestamps {
				if idx, found := existing.tsIdx[ts]; found {
					existing.result.Values[idx] += r.Values[j]
				} else {
					idx = len(existing.result.Timestamps)
					existing.result.Timestamps = append(existing.result.Timestamps, ts)
					existing.result.Values = append(existing.result.Values, r.Values[j])
					existing.tsIdx[ts] = idx
				}
			}
		}

		deduped := make([]Result, 0, len(order))
		for _, key := range order {
			deduped = append(deduped, merged[key].result)
		}
		output.Results[i].Payload = deduped
	}
	return output
}

func (s *PrometheusMetricSource) FetchMetricsLabels(ctx *security.RequestContext, fetchMetricsRequest FetchMetricLabelsRequest) ([]OutputMetricLabels, error) {
	resp, err := relayProxyRequest(ctx, fetchMetricsRequest.AccountId, nil, fmt.Sprintf(
		"prometheus-v2/api/v1/labels?&match[]=%s",
		url.QueryEscape(fmt.Sprintf("{__name__=\"%s\"}", fetchMetricsRequest.MetricName)),
	))
	if err != nil {
		return nil, err
	}
	rawData, ok := resp["data"].([]interface{})
	if !ok {
		ctx.GetLogger().Error("prometheus.FetchMetricsLabels unexpected type for data in response", "response_data", resp)
		return nil, fmt.Errorf("unexpected type for data in response")
	}

	output := make([]OutputMetricLabels, len(rawData))
	for i, v := range rawData {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected label type: %T", v)
		}
		output[i] = OutputMetricLabels{Label: s, Attributes: map[string]interface{}{}}
	}
	return output, nil
}

func relayRequest(ctx *security.RequestContext, accountID, actionName string, params map[string]any) (map[string]any, error) {
	resp, err := relay.Execute(relay.RelayExecuteRequest{
		NoSinks: true,
		Cache:   false,
		Body: relay.ActionExecuteBody{
			AccountID:    accountID,
			ActionName:   actionName,
			ActionParams: params,
			NoSinks:      true,
		},
	})
	if err != nil {
		return nil, err
	}

	switch resp["status_code"] {
	case 500:
		ctx.GetLogger().Error("Relay task failed", "error", resp["response"], "accountId", accountID)
		return nil, fmt.Errorf("relay execution failed (500): %s (accountId %s)", resp["response"], accountID)
	case 400:
		ctx.GetLogger().Error("Relay task failed", "error", resp["response"], "accountId", accountID)
		return nil, fmt.Errorf("relay execution failed (400): %s", resp["response"])
	}

	return resp, nil
}

func relayProxyRequest(ctx *security.RequestContext, accountID string, params map[string]any, apiPath string) (map[string]any, error) {
	resp, err := relay.ExecuteRelayProxyApi(accountID, params, apiPath)
	if err != nil {
		return nil, err
	}

	switch resp["status_code"] {
	case 500:
		ctx.GetLogger().Error("Relay task failed", "error", resp["response"], "accountId", accountID)
		return nil, fmt.Errorf("relay execution failed (500): %s (accountId %s)", resp["response"], accountID)
	case 400:
		ctx.GetLogger().Error("Relay task failed", "error", resp["response"], "accountId", accountID)
		return nil, fmt.Errorf("relay execution failed (400): %s", resp["response"])
	}

	return resp, nil
}

func parseRelayData(resp map[string]any) ([]map[string]any, error) {
	findingsData, ok := resp["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing 'data' field in response")
	}

	findings, ok := findingsData["findings"].([]any)
	if !ok || len(findings) == 0 {
		return nil, fmt.Errorf("empty or invalid 'findings'")
	}

	firstFinding, ok := findings[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid findings structure")
	}

	evidence, ok := firstFinding["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		return nil, fmt.Errorf("empty or invalid 'evidence'")
	}

	firstEvidence, ok := evidence[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid evidence structure")
	}

	rawData, ok := firstEvidence["data"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid evidence data")
	}

	var result []map[string]any
	if err := common.UnmarshalJson([]byte(rawData), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal evidence data: %v", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("parsed evidence data is empty")
	}

	return result, nil
}

func decodeInnerData(result []map[string]any) ([]string, map[string]any, error) {
	marshaledData, err := common.MarshalJson(result[0]["data"])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal first result item: %v", err)
	}

	var inner string
	if err := json.Unmarshal(marshaledData, &inner); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal outer layer: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(inner), &raw); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal inner JSON: %v", err)
	}

	dataAny, ok := raw["data"]
	if !ok {
		return nil, nil, fmt.Errorf("missing 'data' field")
	}

	dataSlice, ok := dataAny.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("'data' is not an array")
	}

	strs := make([]string, 0, len(dataSlice))
	for _, item := range dataSlice {
		if s, ok := item.(string); ok {
			strs = append(strs, s)
		} else {
			return nil, nil, fmt.Errorf("data item is not a string: %v", item)
		}
	}

	return strs, raw, nil
}

func (s *PrometheusMetricSource) FetchMetricLabelValues(ctx *security.RequestContext, req FetchMetricsLabelValueRequest) ([]OutputMetricsLabelValues, error) {
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

func (s *PrometheusMetricSource) FetchMetricList(ctx *security.RequestContext, req FetchMetricsListRequest) ([]OutputMetrics, error) {
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

func toStringMap(m map[string]any) map[string]string {
	res := make(map[string]string, len(m))
	for k, v := range m {
		if str, ok := v.(string); ok {
			res[k] = str
		}
	}
	return res
}

func toInt64Slice(arr []any) []int64 {
	res := make([]int64, 0, len(arr))
	for _, v := range arr {
		switch vv := v.(type) {
		case float64:
			res = append(res, int64(vv))
		case int64:
			res = append(res, vv)
		}
	}
	return res
}

func toFloat64Slice(arr []any) []float64 {
	res := make([]float64, 0, len(arr))
	for _, v := range arr {
		switch vv := v.(type) {
		case string:
			if f, err := strconv.ParseFloat(vv, 64); err == nil {
				res = append(res, f)
			}
		case float64:
			res = append(res, vv)
		}
	}
	return res
}

func (s *PrometheusMetricSource) FetchMetricsQuery(
	ctx *security.RequestContext,
	req FetchMetricsRequest,
) (OutputMetricQuery, error) {
	instant := req.Instant
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
		case map[string]any: // Range query or error
			// Check if this is an error response
			if resultType, ok := q["result_type"].(string); ok && resultType == "error" {
				errorMsg := ""
				if stringResult, ok := q["string_result"].(string); ok {
					errorMsg = stringResult
				}
				ctx.GetLogger().Error("Prometheus query error",
					"query_key", queryKey,
					"error", errorMsg,
					"account_id", req.AccountId)
				output.Results = append(output.Results, QueryResult{
					QueryKey: queryKey,
					Payload:  []Result{},
					Error:    &errorMsg,
					Query:    req.Queries[queryKey],
				})
			} else if seriesListResult, ok := q["series_list_result"].([]any); ok {
				results := make([]Result, 0, len(seriesListResult))
				for _, seriesAny := range seriesListResult {
					seriesMap := seriesAny.(map[string]any)
					results = append(results, Result{
						Metric:     toStringMap(seriesMap["metric"].(map[string]any)),
						Timestamps: toInt64Slice(seriesMap["timestamps"].([]any)),
						Values:     toFloat64Slice(seriesMap["values"].([]any)),
					})
				}
				output.Results = append(output.Results, QueryResult{
					QueryKey: queryKey,
					Payload:  results,
					Query:    req.Queries[queryKey],
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
					if t, ok := val[0].(float64); ok {
						ts = []int64{int64(t)}
					}
					if s, ok := val[1].(string); ok {
						if f, err := strconv.ParseFloat(s, 64); err == nil {
							if math.IsNaN(f) {
								f = 0
							}
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
				Query:    req.Queries[queryKey],
			})
		}
	}
	return output, nil
}
