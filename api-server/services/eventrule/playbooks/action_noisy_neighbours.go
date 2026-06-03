package playbooks

import (
	"errors"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/relay"
	"sort"
	"strconv"
	"time"
)

// noisy_neighbours_enricher composes Prometheus queries against the host
// node to identify the top memory-consuming co-tenant pods.
//
// Output shape:
//
//	{
//	  "name": "noisy_neighbours",
//	  "data": {
//	    "node_name":          "<node>",
//	    "memory_used":        <bytes>,
//	    "memory_allocatable": <bytes>,
//	    "total_pods":         N,
//	    "neighbours":         [{pod_name, namespace, memory_used}, ...]
//	  }
//	}
type noisyNeighboursAction struct{}

var noisyNeighboursAggKeys = map[string]bool{
	"pod_oom_killer_enricher": true,
	"report_crash_loop":       true,
}

func (a *noisyNeighboursAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if !noisyNeighboursAggKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	name, ns := subjectPodNamespace(ctx.GetEvent())
	if name == "" || ns == "" {
		return false
	}
	// Need the host node to filter peers — collector populates
	// events.subject_node from the kubewatch payload; we read it from
	// PlaybookEvent.SubjectNode without a relay call.
	return subjectNodeName(ctx.GetEvent()) != ""
}

func (a *noisyNeighboursAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	podName, namespace := subjectPodNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{
		"pod_name":  podName,
		"namespace": namespace,
		"node_name": subjectNodeName(ctx.GetEvent()),
	})
}

func (a *noisyNeighboursAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	podName, _ := rawParams["pod_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	nodeName, _ := rawParams["node_name"].(string)
	if nodeName == "" {
		nodeName = subjectNodeName(ctx.GetEvent())
	}
	if podName == "" || namespace == "" {
		return nil, errors.New("noisy_neighbours_enricher: pod_name + namespace required")
	}
	if nodeName == "" {
		return nil, errors.New("noisy_neighbours_enricher: no node_name on event (subject_node empty)")
	}

	// We assemble five instant queries against the host node so the
	// resulting `neighbours` shape matches what the legacy Robusta
	// playbook emitted (memory_analyzer.py:100 →
	// `{name, pod_name, namespace, memory_used, memory_requested,
	//   memory_limit}`). The UI's NoisyNeighbour card consumes those
	// fields verbatim; missing `name` or `memory_requested` renders as
	// "Container undefined does not have a memory requests".
	//
	// Label conventions are split between two metric families:
	//   - cAdvisor (container_memory_working_set_bytes): K8s node lives
	//     on `instance` (the scrape target); the `node` label is often
	//     relabelled to a node-pool category, so filtering by
	//     `node=<k8s-node>` returns zero series on vmsingle /
	//     kube-prometheus-stack.
	//   - kube-state-metrics (kube_*): K8s node lives on `node`.
	// Per-container topk keeps the `container` label intact so we can
	// join against the kube_pod_container_resource_{requests,limits}
	// series, which only carry `pod` / `namespace` / `container`.
	topPodsQuery := fmt.Sprintf(
		`topk(15, sum by (pod, namespace, container) (container_memory_working_set_bytes{__CLUSTER__ instance="%s", pod!="", container!="", container!="POD", image!=""}))`,
		nodeName,
	)
	nodeUsageQuery := fmt.Sprintf(
		`sum(container_memory_working_set_bytes{__CLUSTER__ instance="%s", pod!="", image!=""})`,
		nodeName,
	)
	nodeAllocatableQuery := fmt.Sprintf(
		`kube_node_status_allocatable{__CLUSTER__ resource="memory", node="%s"}`,
		nodeName,
	)
	memoryRequestsQuery := fmt.Sprintf(
		`kube_pod_container_resource_requests{__CLUSTER__ resource="memory", node="%s"}`,
		nodeName,
	)
	memoryLimitsQuery := fmt.Sprintf(
		`kube_pod_container_resource_limits{__CLUSTER__ resource="memory", node="%s"}`,
		nodeName,
	)

	results, err := promInstantQueries(ctx, []NamedQuery{
		{Key: "top_pods", Query: topPodsQuery},
		{Key: "node_used", Query: nodeUsageQuery},
		{Key: "node_alloc", Query: nodeAllocatableQuery},
		{Key: "mem_requests", Query: memoryRequestsQuery},
		{Key: "mem_limits", Query: memoryLimitsQuery},
	})
	if err != nil {
		return nil, fmt.Errorf("noisy_neighbours_enricher: prom: %w", err)
	}

	// Index requests / limits by (namespace, pod, container) for O(1)
	// lookup while iterating top_pods. kube-state-metrics emits one
	// series per (pod, container) per resource — no aggregation needed.
	memRequests := indexByPodContainer(results["mem_requests"])
	memLimits := indexByPodContainer(results["mem_limits"])
	totalRequested := 0.0
	for _, v := range memRequests {
		totalRequested += v
	}

	neighbours := []map[string]any{}
	if vec, ok := results["top_pods"]; ok {
		for _, s := range vectorResultEntries(vec) {
			pod, _ := s.metric["pod"].(string)
			ns, _ := s.metric["namespace"].(string)
			container, _ := s.metric["container"].(string)
			key := ns + "/" + pod + "/" + container
			entry := map[string]any{
				"name":             container,
				"pod_name":         pod,
				"namespace":        ns,
				"node_name":        nodeName,
				"memory_used":      s.value,
				"memory_requested": memRequests[key],
				"memory_limit":     memLimits[key],
			}
			neighbours = append(neighbours, entry)
		}
		sort.Slice(neighbours, func(i, j int) bool {
			vi, _ := neighbours[i]["memory_used"].(float64)
			vj, _ := neighbours[j]["memory_used"].(float64)
			return vi > vj
		})
	}

	nodeUsed := firstInstantValue(results["node_used"])
	nodeAlloc := firstInstantValue(results["node_alloc"])

	payload := map[string]any{
		"name": "noisy_neighbours",
		"data": map[string]any{
			"node_name":          nodeName,
			"memory_used":        nodeUsed,
			"memory_allocatable": nodeAlloc,
			"memory_requested":   totalRequested,
			"total_pods":         len(neighbours),
			"neighbours":         neighbours,
		},
	}

	additionalInfo := map[string]any{
		"title":              "Noisy Neighbours",
		"action_name":        "noisy_neighbours_enricher",
		"actual_action_name": "noisy_neighbours_enricher",
		"node_name":          nodeName,
		"pod_name":           podName,
		"namespace":          namespace,
	}
	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return NewPlaybookActionResponseJson(payload, additionalInfo, []PlaybookActionResponseInsight{}, metadata), nil
}

// promInstantQueries fires N named instant queries through the relay's
// prometheus_queries_enricher action and returns the per-key payload.
//
// Like promRangeQueries, the timestamp prefers the event's EndedAt /
// StartedAt over time.Now() so the snapshot reflects cluster state at the
// incident, not at investigation time.
func promInstantQueries(ctx PlaybookActionContext, queries []NamedQuery) (map[string]any, error) {
	end := time.Now().UTC()
	if t := ctx.GetEvent().EndedAt; t != nil && !t.IsZero() {
		end = t.UTC()
	} else if t := ctx.GetEvent().StartedAt; t != nil && !t.IsZero() {
		end = t.UTC()
	}
	start := end
	rel := relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  ctx.GetAccountId(),
			ActionName: "prometheus_queries_enricher",
			ActionParams: map[string]any{
				"duration": map[string]any{
					"starts_at": start.Format("2006-01-02 15:04:05 UTC"),
					"ends_at":   end.Format("2006-01-02 15:04:05 UTC"),
				},
				"instant":        true,
				"promql_queries": queries,
			},
			Origin: "services-server",
		},
		NoSinks: true,
		Cache:   false,
	}
	resp, _, err := relay.ExecuteAndExtractResponse(rel)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	switch d := resp["data"].(type) {
	case map[string]any:
		result = d
	case string:
		if err := common.UnmarshalJson([]byte(d), &result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// vectorEntry is a single (metric, value) pair from a Prometheus instant
// vector — we normalize the relay's two wire shapes (bare array vs
// wrapped envelope) into this local struct so callers don't deal with
// nested any types. See vectorResultEntries for the shape handling.
type vectorEntry struct {
	metric map[string]any
	value  float64
}

// indexByPodContainer builds a {namespace/pod/container → value} map
// from a kube-state-metrics vector result. Used to attach per-container
// memory_requested / memory_limit values to entries assembled from the
// cAdvisor top_pods query without an N×M lookup.
func indexByPodContainer(raw any) map[string]float64 {
	out := map[string]float64{}
	for _, e := range vectorResultEntries(raw) {
		ns, _ := e.metric["namespace"].(string)
		pod, _ := e.metric["pod"].(string)
		container, _ := e.metric["container"].(string)
		if pod == "" || container == "" {
			continue
		}
		out[ns+"/"+pod+"/"+container] = e.value
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
func vectorResultEntries(raw any) []vectorEntry {
	out := []vectorEntry{}
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
		out = append(out, vectorEntry{metric: metric, value: v})
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

func firstInstantValue(raw any) float64 {
	for _, e := range vectorResultEntries(raw) {
		return e.value
	}
	return 0
}

// seriesEntry is the range-query equivalent of vectorEntry — the relay's
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
