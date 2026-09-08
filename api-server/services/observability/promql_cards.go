package observability

import (
	"fmt"
	"nudgebee/services/security"
	"strconv"
	"time"
)

// Internal enrichers hand their PromQL to FetchMetricsQuery like any other
// caller of the metrics layer and read OutputMetricQuery back. What this file
// adds is the last mile for the two things a typed result cannot be used for
// directly: reading label sets in bulk, and rendering the evidence cards the UI
// stores and draws in the agent's wire shape.

// PromQLLabels returns the label set of every series FetchMetricsQuery returned
// under queryKey — what the discovery and knowledge-graph lookups read.
func PromQLLabels(out OutputMetricQuery, queryKey string) []map[string]string {
	for _, result := range out.Results {
		if result.QueryKey != queryKey {
			continue
		}
		labels := make([]map[string]string, 0, len(result.Payload))
		for _, series := range result.Payload {
			labels = append(labels, series.Metric)
		}
		return labels
	}
	return nil
}

// workloadMetricQueries are the per-workload series the anomaly and SLO
// evidence cards plot. __CLUSTER__ is expanded by the relay-server on the agent
// path and by the direct source on the user path.
var workloadMetricQueries = map[string]string{
	"cpu":            `sum(rate(container_cpu_usage_seconds_total{ __CLUSTER__ pod=~"%[1]s.*", namespace="%[2]s"}[5m])) by (pod,namespace)`,
	"memory":         `sum(container_memory_usage_bytes{ __CLUSTER__ pod=~"%[1]s.*", namespace="%[2]s"}) by (pod, namespace)`,
	"network":        `sum(rate(container_network_receive_bytes_total{ __CLUSTER__ pod=~"%[1]s.*", namespace="%[2]s"}[5m])) + sum(rate(container_network_transmit_bytes_total{ __CLUSTER__ pod=~"%[1]s.*", namespace="%[2]s"}[5m]))`,
	"latency":        `histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ actual_destination_workload_name=~"%[1]s.*", actual_destination_workload_namespace="%[2]s"}[5m])) by (le))`,
	"error_rate":     `sum(rate(container_http_requests_duration_seconds_total_count{ __CLUSTER__ actual_destination_workload_name=~"%[1]s.*", actual_destination_workload_namespace="%[2]s"}[5m]))`,
	"replicas":       `sum(kube_deployment_status_replicas{ __CLUSTER__ deployment=~"%[1]s.*", namespace="%[2]s"})`,
	"cpu_throttling": `sum(rate(container_resources_cpu_throttled_seconds_total{ __CLUSTER__ container_id=~".*%[2]s/%[1]s.*"}[5m])) by (container_id)`,
}

// WorkloadMetricEvidence runs one of the workload metric queries over the window
// and returns it as the evidence card the anomaly and SLO events persist — the
// same card the agent's prometheus_enricher produced, so the investigate page's
// Prometheus card keeps rendering it.
func WorkloadMetricEvidence(ctx *security.RequestContext, accountID, workload, namespace, resourceType, title string, start, end time.Time) (map[string]any, error) {
	template, ok := workloadMetricQueries[resourceType]
	if !ok {
		return nil, fmt.Errorf("observability: invalid workload metric %q", resourceType)
	}
	query := fmt.Sprintf(template, workload, namespace)
	out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:      accountID,
		MetricProvider: integrationPrometheus,
		Queries:        map[string]string{"A": query},
		StartTime:      start.UnixMilli(),
		EndTime:        end.UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if len(out.Results) == 0 {
		data = promQLMatrixEnvelope(nil)
	} else {
		data = promQLSingleEnvelope(out.Results[0], false)
	}
	return map[string]any{
		"type":    "prometheus",
		"data":    data,
		"version": 1.0,
		"metadata": map[string]any{
			"query-result-version": "1.0",
			"query":                query,
		},
		"additional_info": map[string]any{"title": title},
		"insight":         []map[string]any{},
	}, nil
}

// integrationPrometheus is the provider every internal PromQL request names:
// PromQL only runs on a Prometheus-compatible engine, and naming the provider
// leaves the metrics layer to pick the source — the connection the user
// configured, or the cluster agent's Prometheus.
const integrationPrometheus = "prometheus"

// Card encoders — the agent's wire shape for a PromQL result, which is what the
// persisted evidence cards and the playbook parsers read. Containers are []any
// and map[string]any throughout, exactly what decoding the agent's JSON yields,
// so a card is read the same way before and after it is persisted:
//
//	instant prometheus_queries_enricher → bare array [{metric, value:[ts,"v"]}]
//	instant prometheus_enricher         → {result_type:"vector", vector_result:[{metric, value:{timestamp,value}}]}
//	range                                → {result_type:"matrix", series_list_result:[{metric, timestamps:[float64], values:["str"]}]}
//	error                                → {result_type:"error", string_result}

func promQLQueriesResult(result QueryResult, instant bool) any {
	if result.Error != nil {
		return promQLErrorEnvelope(*result.Error)
	}
	if instant {
		entries := make([]any, 0, len(result.Payload))
		for _, series := range result.Payload {
			if len(series.Values) == 0 {
				continue
			}
			entries = append(entries, map[string]any{
				"metric": promQLMetricLabels(series.Metric),
				"value":  []any{promQLTimestamp(series, 0), promQLValue(series.Values[0])},
			})
		}
		return entries
	}
	return promQLMatrixEnvelope(result.Payload)
}

func promQLSingleEnvelope(result QueryResult, instant bool) map[string]any {
	if result.Error != nil {
		return promQLErrorEnvelope(*result.Error)
	}
	if !instant {
		return promQLMatrixEnvelope(result.Payload)
	}
	entries := make([]any, 0, len(result.Payload))
	for _, series := range result.Payload {
		if len(series.Values) == 0 {
			continue
		}
		entries = append(entries, map[string]any{
			"metric": promQLMetricLabels(series.Metric),
			"value":  map[string]any{"timestamp": promQLTimestamp(series, 0), "value": promQLValue(series.Values[0])},
		})
	}
	return map[string]any{
		"result_type":        "vector",
		"vector_result":      entries,
		"series_list_result": nil,
		"scalar_result":      nil,
		"string_result":      nil,
	}
}

func promQLMatrixEnvelope(payload []Result) map[string]any {
	return map[string]any{
		"result_type":        "matrix",
		"vector_result":      nil,
		"series_list_result": promQLSeriesList(payload),
		"scalar_result":      nil,
		"string_result":      nil,
	}
}

// promQLSeriesList is the matrix entries alone; series without samples are
// dropped, as the agent never emits one.
func promQLSeriesList(payload []Result) []any {
	entries := make([]any, 0, len(payload))
	for _, series := range payload {
		if len(series.Values) == 0 {
			continue
		}
		timestamps := make([]any, 0, len(series.Values))
		values := make([]any, 0, len(series.Values))
		for i, v := range series.Values {
			timestamps = append(timestamps, promQLTimestamp(series, i))
			values = append(values, promQLValue(v))
		}
		entries = append(entries, map[string]any{
			"metric":     promQLMetricLabels(series.Metric),
			"timestamps": timestamps,
			"values":     values,
		})
	}
	return entries
}

func promQLErrorEnvelope(msg string) map[string]any {
	return map[string]any{
		"result_type":        "error",
		"vector_result":      nil,
		"series_list_result": nil,
		"scalar_result":      nil,
		"string_result":      msg,
	}
}

func promQLMetricLabels(metric map[string]string) map[string]any {
	out := make(map[string]any, len(metric))
	for k, v := range metric {
		out[k] = v
	}
	return out
}

// promQLTimestamp is the sample's unix seconds as the float the agent emits.
func promQLTimestamp(series Result, i int) float64 {
	if i < len(series.Timestamps) {
		return float64(series.Timestamps[i])
	}
	return 0
}

func promQLValue(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
