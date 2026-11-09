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

// workloadEvidenceKeys maps an evidence card to the utilisation keys that can answer
// it, most specific first. The per-pod keys draw one line per pod; where a provider
// has no such key the workload total is used and the chart is a single line.
//
// These are utilisation keys rather than PromQL so that the account's default metrics
// provider answers, whichever it is. A provider with no equivalent for a key returns
// nothing for it and the card carries no series, which the investigate page hides.
var workloadEvidenceKeys = map[string][]string{
	"cpu":            {"cpu_usage_by_pod", "cpu_usage"},
	"memory":         {"memory_usage_by_pod", "memory_usage"},
	"latency":        {"workload_http_latency_p99"},
	"cpu_throttling": {"cpu_throttling"},
	"error_rate":     {"workload_http_error_rate"},
	"replicas":       {"replica_ready"},
}

// WorkloadMetricEvidence fetches one workload metric over the window from the
// account's metrics provider and returns it as the evidence card the anomaly and SLO
// events persist — the same card shape the agent's prometheus_enricher produced, so
// the investigate page's Prometheus card keeps rendering it. "prometheus" in that
// shape names the card's format, not where the numbers came from; metadata.query
// carries the provider's own query for that.
//
// kind is the workload's Kubernetes kind. Providers that filter by a kind-specific
// tag (Datadog's kube_deployment vs kube_stateful_set) need it; empty means deployment.
func WorkloadMetricEvidence(ctx *security.RequestContext, accountID, workload, namespace, kind, resourceType, title string, start, end time.Time) (map[string]any, error) {
	keys, ok := workloadEvidenceKeys[resourceType]
	if !ok {
		return nil, fmt.Errorf("observability: invalid workload metric %q", resourceType)
	}
	out, err := FetchMetricUtilisation(ctx, GetUtilisationTrendRequest{
		AccountId: accountID,
		StartTime: start.UnixMilli(),
		EndTime:   end.UnixMilli(),
		Request: map[string]any{
			"workload_namespace": namespace,
			"workload_name":      workload,
			"kind":               workloadKindForUtilisation(kind),
			"metrics":            keys,
		},
	})
	if err != nil {
		return nil, err
	}

	// The metrics layer reports a failed query per key rather than as an error, so
	// without this a backend failure is indistinguishable from "no data".
	for _, r := range out.Results {
		if r.Error != nil {
			ctx.GetLogger().Warn("workload evidence: metrics query failed",
				"account_id", accountID, "workload", workload, "namespace", namespace,
				"metric", r.QueryKey, "error", *r.Error)
		}
	}

	result, found := firstResultWithSeries(out, keys)
	data := promQLMatrixEnvelope(nil)
	query := ""
	if found {
		data = promQLSingleEnvelope(result, false)
		query = result.Query
		if query == "" {
			// A source that does not report its query still has to say what was asked.
			query = result.QueryKey
		}
	} else {
		// An empty card still says what was asked. Without it the investigation saw
		// "no data" with no query, took the key's name for a series name and spent
		// minutes searching for a metric that does not exist.
		query = firstQueryAsked(out, keys)
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

// firstResultWithSeries returns the result for the first of keys, in order, that came
// back with at least one non-empty series. A key the provider has no query for is
// simply absent from the output (or present and empty), so preference order is all
// that is needed to fall from a per-pod key to the workload total.
func firstResultWithSeries(out OutputMetricQuery, keys []string) (QueryResult, bool) {
	byKey := make(map[string]QueryResult, len(out.Results))
	for _, r := range out.Results {
		byKey[r.QueryKey] = r
	}
	for _, key := range keys {
		r, ok := byKey[key]
		if !ok {
			continue
		}
		for _, series := range r.Payload {
			if len(series.Values) > 0 {
				return r, true
			}
		}
	}
	return QueryResult{}, false
}

// firstQueryAsked returns the query of the first of keys, in order, that the provider
// ran, or "" when it ran none of them.
func firstQueryAsked(out OutputMetricQuery, keys []string) string {
	byKey := make(map[string]string, len(out.Results))
	for _, r := range out.Results {
		if _, seen := byKey[r.QueryKey]; !seen && r.Query != "" {
			byKey[r.QueryKey] = r.Query
		}
	}
	for _, key := range keys {
		if q, ok := byKey[key]; ok {
			return q
		}
	}
	return ""
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
