package observability

import (
	"fmt"
	"strings"

	"nudgebee/services/security"
)

// buildSolarWindsNodeQueries returns a metricKey→SWO metric name map for node-level utilization.
// Values are SWO REST API metric names; the filter and groupBy are built separately via
// buildSolarWindsRequestParams and passed as FetchMetricsRequest.Request in the caller.
// Returns an empty map when nodeName is empty (no node scoped data can be fetched).
func buildSolarWindsNodeQueries(nodeName string, metrics []string) map[string]string {
	queries := make(map[string]string)
	if nodeName == "" {
		return queries
	}
	for _, k := range metrics {
		switch k {
		case "cpu_usage", "cpu_usage_line":
			queries[k] = "k8s.node.cpu.usage.seconds.rate"
		case "memory_usage", "memory_usage_line":
			queries[k] = "k8s.node.memory.working_set"
		case "cpu_allocatable":
			queries[k] = "k8s.node.cpu.allocatable"
		case "memory_allocatable":
			queries[k] = "k8s.node.memory.allocatable"
		case "disk_used":
			queries[k] = "k8s.node.fs.usage"
			// cpu_request, memory_request, cpu_limit, memory_limit:
			//   No per-node pod-request rollup metric exists in SWO — skip.
			// disk_total:
			//   SWO exposes no filesystem capacity metric (only k8s.node.fs.usage) — skip.
		}
	}
	return queries
}

// buildSolarWindsWorkloadQueries returns a metricKey→SWO metric name map for workload and pod
// utilization. Pod-level OTel metrics are used for all kinds because they are enriched with the
// full Kubernetes hierarchy (namespace, deployment, statefulset, pod) in SWO's entity model,
// making them filterable by deployment name. cAdvisor-style container metrics (e.g.
// k8s.container_memory_working_set_bytes) use underscore naming and do NOT carry deployment
// attributes, so filtering by k8s.deployment.name on them returns empty results.
// Returns an empty map when both Namespace and Name are empty.
func buildSolarWindsWorkloadQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	if meta.Namespace == "" && meta.Name == "" {
		return queries
	}
	for _, k := range metrics {
		switch k {
		// Pod-level OTel metrics are used for all workload kinds (deployment, statefulset, pod).
		// When filtered by k8s.deployment.name / k8s.statefulset.name and grouped by that key,
		// SWO aggregates across all pods belonging to the workload.
		case "cpu_usage", "cpu_usage_line":
			queries[k] = "k8s.pod.cpu.usage.seconds.rate"
		case "memory_usage", "memory_usage_line":
			queries[k] = "k8s.pod.memory.working_set"
		// Spec (request/limit) metrics use the container-level OTel variants which are generated
		// by Kubernetes State Metrics and do carry deployment hierarchy attributes in SWO.
		case "cpu_request":
			queries[k] = "k8s.container.spec.cpu.requests"
		case "memory_request":
			queries[k] = "k8s.container.spec.memory.requests"
		case "cpu_limit":
			queries[k] = "k8s.container.spec.cpu.limit"
		case "memory_limit":
			queries[k] = "k8s.container.spec.memory.limit"
		}
	}
	return queries
}

// buildSolarWindsClusterPercentileQueries returns node-level metric names for cluster-wide
// percentile, max, and per-node line metrics. These are batched with groupBy=k8s.node.name.
//
//   - p90_*/p50_*/max_* entries: collapsed by fetchSolarWindsClusterUtilisation via
//     computePercentileFromSeries into a single cluster-wide series.
//   - cpu_usage_line / memory_usage_line: NOT in the percentile switch — they are returned
//     as-is (one series per node) so the UI can render a multi-line chart per node.
func buildSolarWindsClusterPercentileQueries(metrics []string) map[string]string {
	queries := make(map[string]string)
	for _, k := range metrics {
		switch k {
		case "p90_cpu", "p50_cpu", "max_usage_cpu", "cpu_usage_line":
			queries[k] = "k8s.node.cpu.usage.seconds.rate"
		case "p90_mem", "p50_mem", "max_usage_mem", "memory_usage_line":
			queries[k] = "k8s.node.memory.working_set"
		}
	}
	return queries
}

// buildSolarWindsClusterAggregateQueries returns cluster-level metric names for aggregate
// cluster-wide metrics (AVG aggregation, no groupBy → single cluster value per metric).
func buildSolarWindsClusterAggregateQueries(metrics []string) map[string]string {
	queries := make(map[string]string)
	for _, k := range metrics {
		switch k {
		case "cpu_request":
			queries[k] = "k8s.cluster.spec.cpu.requests"
		case "memory_request":
			queries[k] = "k8s.cluster.spec.memory.requests"
		}
	}
	return queries
}

// buildSolarWindsClusterSumQueries returns node-level metric names for cluster-wide
// aggregate usage and capacity metrics (SUM aggregation, no groupBy → single summed cluster value).
// These cannot share the AVG aggregate call because SUM is required to total across nodes.
//
// cpu_total/mem_total use allocatable (not raw capacity) so that numerator and denominator
// both reflect node-level SUM aggregation, preventing >100% utilisation from AVG vs SUM mismatch
// when k8s.cluster.* metrics return per-node values instead of a pre-summed cluster total.
func buildSolarWindsClusterSumQueries(metrics []string) map[string]string {
	queries := make(map[string]string)
	for _, k := range metrics {
		switch k {
		case "cpu_real":
			// Actual CPU usage summed across all cluster nodes.
			queries[k] = "k8s.node.cpu.usage.seconds.rate"
		case "mem_real":
			// Actual memory working-set summed across all cluster nodes.
			queries[k] = "k8s.node.memory.working_set"
		case "cpu_total":
			// Allocatable CPU summed across all cluster nodes.
			queries[k] = "k8s.node.cpu.allocatable"
		case "mem_total":
			// Allocatable memory summed across all cluster nodes.
			queries[k] = "k8s.node.memory.allocatable"
		}
	}
	return queries
}

// buildSolarWindsFilter constructs a SWO filter expression string from RequestMetadata.
// Returns "" when no identifying fields are set (cluster-level queries need no filter).
//
// ContainerName is intentionally excluded: utilization queries mix pod-level usage metrics
// (k8s.pod.cpu.usage.seconds.rate, k8s.pod.memory.working_set) and container-level spec
// metrics in a single batch. Applying a k8s.container.name filter globally would produce
// empty results for the pod-level metrics since they carry no container dimension.
func buildSolarWindsFilter(meta RequestMetadata) string {
	var parts []string
	if meta.NodeName != "" {
		// NodeName may be a pipe-separated list of Prometheus-style regex patterns
		// (e.g. "node-a.*|node-b.*"). swBuildMultiValueFilter splits on "|", converts
		// ".*" regex wildcards to glob "*", and emits a single SWO "[val1,val2]" clause.
		if f := swBuildMultiValueFilter("k8s.node.name", meta.NodeName); f != "" {
			parts = append(parts, f)
		}
	}
	if meta.Namespace != "" {
		parts = append(parts, fmt.Sprintf("k8s.namespace.name: [%s]", swEscapeValue(meta.Namespace)))
	}
	if meta.Name != "" {
		if attrKey := swKindToAttrKey(meta.Kind); attrKey != "" {
			parts = append(parts, fmt.Sprintf("%s: [%s]", attrKey, swEscapeValue(meta.Name)))
		}
	}
	// SWO measurements API uses a single space between filter clauses (consistent with
	// buildSwTraceFilter in solarwinds_traces.go). " AND " is not a valid separator.
	return strings.Join(parts, " ")
}

// swBuildMultiValueFilter builds a SWO measurements filter clause for an attribute whose
// value may be a pipe-separated list of Prometheus-style regex patterns (e.g. from the
// node_name field: "gke-cluster-node1.*|gke-cluster-node2.*").
//
// Transformation rules applied to each pipe-separated token:
//  1. Whitespace is trimmed and empty tokens are dropped.
//  2. Trailing ".*" is stripped to recover the exact attribute value. GKE node names are
//     passed as "node-name.*" where ".*" is a Prometheus regex suffix meaning "any suffix".
//     The SWO k8s.node.name attribute stores the bare node name without any such suffix.
//
// Output format:
//   - When all cleaned values are exact (no "*" remaining):
//     attr: [val1,val2]   — SWO multi-value exact-match bracket syntax.
//   - When any cleaned value still contains a glob wildcard "*" (e.g. caller passed
//     "prefix*" without the leading dot), each value is emitted as a separate
//     "attr:val*" clause joined by spaces (implicit OR in SWO filter syntax).
//
// Returns "" when pipeJoinedValues is empty or all tokens are blank.
func swBuildMultiValueFilter(attribute, pipeJoinedValues string) string {
	tokens := strings.Split(pipeJoinedValues, "|")
	cleaned := make([]string, 0, len(tokens))
	hasWildcard := false
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// Strip Prometheus-style trailing ".*" to recover the exact SWO attribute value.
		t = strings.TrimSuffix(t, ".*")
		if strings.Contains(t, "*") {
			hasWildcard = true
		}
		cleaned = append(cleaned, t)
	}
	if len(cleaned) == 0 {
		return ""
	}
	if hasWildcard {
		// SWO measurements filter supports glob wildcards only outside brackets.
		// Space-join individual "attr:val*" clauses (implicit OR in SWO filter syntax).
		parts := make([]string, len(cleaned))
		for i, v := range cleaned {
			parts[i] = fmt.Sprintf("%s:%s", attribute, v)
		}
		return strings.Join(parts, " ")
	}
	// All exact values: use multi-value bracket syntax.
	return fmt.Sprintf("%s: [%s]", attribute, strings.Join(cleaned, ","))
}

// swKindToAttrKey maps a Kubernetes resource kind to the SWO attribute key used in filters
// and groupBy expressions.
func swKindToAttrKey(kind string) string {
	switch kind {
	case "deployment":
		return "k8s.deployment.name"
	case "statefulset":
		return "k8s.statefulset.name"
	case "daemonset":
		return "k8s.daemonset.name"
	case "pod":
		return "k8s.pod.name"
	default:
		return ""
	}
}

// buildSolarWindsGroupBy returns a comma-separated list of SWO attribute keys for the groupBy
// query parameter. groupBy ensures attributes are populated in the measurement response.
func buildSolarWindsGroupBy(meta RequestMetadata) string {
	if meta.Kind == "node" {
		return "k8s.node.name"
	}
	parts := []string{"k8s.namespace.name"}
	if attrKey := swKindToAttrKey(meta.Kind); attrKey != "" {
		parts = append(parts, attrKey)
	}
	return strings.Join(parts, ",")
}

// buildSolarWindsRequestParams builds the freeform Request map passed as FetchMetricsRequest.Request
// when routing utilization queries through the SolarWinds MetricSource. groupBy and aggregateBy are
// passed as arguments: groupBy because cluster-level callers use two different groupBy values across
// two separate batch calls; aggregateBy because node queries use "AVG" (per-node average) while
// workload queries use "SUM" (total across all pods belonging to the workload).
func buildSolarWindsRequestParams(meta RequestMetadata, groupBy, aggregateBy string) map[string]any {
	r := map[string]any{"aggregateBy": aggregateBy}
	if filter := buildSolarWindsFilter(meta); filter != "" {
		r["filter"] = filter
	}
	if groupBy != "" {
		r["groupBy"] = groupBy
	}
	return r
}

// fetchSolarWindsClusterUtilisation handles cluster-level utilization queries for SolarWinds.
//
// SWO's groupBy is a global parameter shared across all metrics in one batch call, so cluster
// queries cannot mix per-node (percentile) and cluster-aggregate metrics in a single request:
//
//   - Percentile call: node-level metrics + groupBy=k8s.node.name
//     → post-processes p90/p50/max via computePercentileFromSeries
//   - Aggregate call: cluster-level request metrics + no groupBy (AVG)
//     → cpu_request, memory_request
//   - Sum call: node-level metrics summed across all nodes + no groupBy (SUM)
//     → cpu_real, mem_real, cpu_total, mem_total
//
// Results from all calls are merged. If only one call succeeds, partial results are returned.
func fetchSolarWindsClusterUtilisation(
	ctx *security.RequestContext,
	req GetUtilisationTrendRequest,
	metricsProvider, integrationSource string,
	meta RequestMetadata,
	instant bool,
) (OutputMetricQuery, error) {
	merged := OutputMetricQuery{Results: []QueryResult{}}
	var firstErr error

	// --- 1. Percentile call (p90/p50/max, grouped per node) ---
	pctQueries := buildSolarWindsClusterPercentileQueries(meta.RequestedMetrics)
	if len(pctQueries) > 0 {
		pctOut, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:            req.AccountId,
			MetricProvider:       metricsProvider,
			MetricProviderSource: integrationSource,
			Queries:              pctQueries,
			StartTime:            req.StartTime,
			EndTime:              req.EndTime,
			Instant:              instant,
			Request:              map[string]any{"aggregateBy": "AVG", "groupBy": "k8s.node.name"},
		})
		if err != nil {
			firstErr = err
		} else {
			// Collapse per-node series into a single cluster-wide percentile series.
			for i, qr := range pctOut.Results {
				if len(qr.Payload) == 0 {
					continue
				}
				switch qr.QueryKey {
				case "p90_cpu", "p90_mem":
					pctOut.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 0.90)}
				case "p50_cpu", "p50_mem":
					pctOut.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 0.50)}
				case "max_usage_cpu", "max_usage_mem":
					pctOut.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 1.0)}
				}
			}
			merged.Results = append(merged.Results, pctOut.Results...)
		}
	}

	// --- 2. Aggregate call (cpu_request, memory_request — cluster-wide, no groupBy) ---
	aggQueries := buildSolarWindsClusterAggregateQueries(meta.RequestedMetrics)
	if len(aggQueries) > 0 {
		aggOut, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:            req.AccountId,
			MetricProvider:       metricsProvider,
			MetricProviderSource: integrationSource,
			Queries:              aggQueries,
			StartTime:            req.StartTime,
			EndTime:              req.EndTime,
			Instant:              instant,
			Request:              map[string]any{"aggregateBy": "AVG"},
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			merged.Results = append(merged.Results, aggOut.Results...)
		}
	}

	// --- 3. Sum call (cpu_real, mem_real, cpu_total, mem_total — node-level metrics summed across all cluster nodes) ---
	sumQueries := buildSolarWindsClusterSumQueries(meta.RequestedMetrics)
	if len(sumQueries) > 0 {
		sumOut, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:            req.AccountId,
			MetricProvider:       metricsProvider,
			MetricProviderSource: integrationSource,
			Queries:              sumQueries,
			StartTime:            req.StartTime,
			EndTime:              req.EndTime,
			Instant:              instant,
			Request:              map[string]any{"aggregateBy": "SUM"},
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			merged.Results = append(merged.Results, sumOut.Results...)
		}
	}

	// Return partial results when at least one call succeeded; only propagate error when
	// all calls failed and there is nothing to show.
	if len(merged.Results) == 0 && firstErr != nil {
		return OutputMetricQuery{}, firstErr
	}
	return merged, nil
}
