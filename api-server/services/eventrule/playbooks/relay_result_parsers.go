package playbooks

import (
	"strconv"
)

// Relay wire-format parsers for prometheus_queries_enricher results.
//
// These live here, not beside the noisy-neighbours action that used to hold them:
// action_k8s_pod_metrics and action_impacted_services parse the same shapes, and the
// action itself moved to the observability package (which owns the metrics-provider
// lookup and cannot be imported from here). Three of them are exported for that caller.

// LatestValueEntries reduces a relay prometheus_queries_enricher result to
// {metric, latest-value} pairs. noisy_neighbours runs RANGE queries (see the
// const block above), so the common case is the matrix wire shape
// (`series_list_result`) where we take the last sample of each series; we
// still fall back to the instant shapes (bare array / `vector_result`) via
// vectorResultEntries so the parser is robust to either response.
func LatestValueEntries(raw any) []VectorEntry {
	if m, ok := raw.(map[string]any); ok {
		if _, hasSeries := m["series_list_result"]; hasSeries {
			out := []VectorEntry{}
			for _, e := range seriesListEntries(raw) {
				out = append(out, VectorEntry{Metric: e.metric, Value: e.lastValue})
			}
			return out
		}
	}
	return vectorResultEntries(raw)
}

// FirstLatestValue returns the latest sample of the first series in a relay
// result (range matrix or instant vector). Used for scalar-ish queries
// (node_used / node_alloc) that resolve to a single series.
func FirstLatestValue(raw any) float64 {
	for _, e := range LatestValueEntries(raw) {
		return e.Value
	}
	return 0
}

// VectorEntry is a single (metric, value) pair from a Prometheus instant
// vector — we normalize the relay's two wire shapes (bare array vs
// wrapped envelope) into this local struct so callers don't deal with
// nested any types. See vectorResultEntries for the shape handling.
type VectorEntry struct {
	Metric map[string]any
	Value  float64
}

// IndexByPodContainer builds a {namespace/pod/container → latest value} map
// from a kube-state-metrics result. Used to attach per-container
// memory_requested / memory_limit values to entries assembled from the
// cAdvisor top_pods query without an N×M lookup.
func IndexByPodContainer(raw any) map[string]float64 {
	out := map[string]float64{}
	for _, e := range LatestValueEntries(raw) {
		ns, _ := e.Metric["namespace"].(string)
		pod, _ := e.Metric["pod"].(string)
		container, _ := e.Metric["container"].(string)
		if pod == "" || container == "" {
			continue
		}
		out[ns+"/"+pod+"/"+container] = e.Value
	}
	return out
}

// vectorResultEntries normalizes the two wire shapes the relay's
// prometheus_queries_enricher emits for an instant query (per
// nudgebee-agent/pkg/enrichers/prometheus.go:114-118):
//
//   - instant + success → bare Prometheus result array
//     `[{metric, value}, ...]` (Go-agent / forager path)
//   - range + success or any error → wrapped envelope
//     `{result_type, vector_result, series_list_result, ...}` (the
//     vector_result branch is what older Python Robusta returned even
//     for instant queries — we keep accepting it for backward compat)
//
// Without the bare-array branch every instant-query caller (the noisy
// neighbours card, pod_metric_enricher's requests/limits join) silently
// rendered as "no data" against the Go agent.
func vectorResultEntries(raw any) []VectorEntry {
	out := []VectorEntry{}
	var arr []any
	switch v := raw.(type) {
	case []any:
		arr = v
	case map[string]any:
		var ok bool
		arr, ok = v["vector_result"].([]any)
		if !ok {
			return out
		}
	default:
		return out
	}
	for _, item := range arr {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric, _ := im["metric"].(map[string]any)
		v, ok := parseInstantValue(im["value"])
		if !ok {
			continue
		}
		out = append(out, VectorEntry{Metric: metric, Value: v})
	}
	return out
}

// parseInstantValue accepts both wire shapes the relay's
// prometheus_queries_enricher returns for an instant-vector `value`:
//
//   - Robusta-coerced object: {"timestamp": <float>, "value": "<str>"}
//     (emitted by the Go-agent forager and the Python Robusta sink)
//   - Standard Prometheus tuple: [<ts>, "<str>"]
//
// Returns the numeric sample (ok=false if the value cannot be parsed).
func parseInstantValue(raw any) (float64, bool) {
	switch v := raw.(type) {
	case map[string]any:
		s, ok := v["value"].(string)
		if !ok {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	case []any:
		if len(v) < 2 {
			return 0, false
		}
		s, ok := v[1].(string)
		if !ok {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// seriesEntry is the range-query equivalent of VectorEntry — the relay's
// series_list_result items have parallel timestamps/values arrays (values are
// value-strings, NOT [ts,val] pairs — see ml-k8s-server PR #30322 for the
// same wire shape over there).
type seriesEntry struct {
	metric    map[string]any
	lastValue float64
}

func seriesListEntries(raw any) []seriesEntry {
	out := []seriesEntry{}
	m, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	arr, ok := m["series_list_result"].([]any)
	if !ok {
		return out
	}
	for _, item := range arr {
		im, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metric, _ := im["metric"].(map[string]any)
		values, _ := im["values"].([]any)
		if len(values) == 0 {
			continue
		}
		lastStr, _ := values[len(values)-1].(string)
		v, err := strconv.ParseFloat(lastStr, 64)
		if err != nil {
			continue
		}
		out = append(out, seriesEntry{metric: metric, lastValue: v})
	}
	return out
}
