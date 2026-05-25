package observability

import (
	"fmt"
	"strings"
)

// --- DYNATRACE DQL QUERY BUILDERS ---
//
// Each builder returns a map[string]string where every value is a DQL timeseries
// template string that:
//   - Starts with "timeseries" (so buildMetricDQL treats it as a template, not a bare selector)
//   - Contains {DTFROM}, {DTTO}, {DTINTERVAL} placeholders that buildMetricDQL substitutes
//     with the actual ISO-8601 time range from the request
//   - Backtick-quotes the metric ID (colons in "builtin:..." are not valid bare DQL identifiers)
//   - Uses DQL filter: and by: clauses for entity scoping
//
// Confirmed-valid DQL form (verified against msz37882.apps.dynatrace.com):
//
//	timeseries val = avg(`kube_node_status_allocatable`),
//	  from: "2024-01-01T00:00:00Z", to: "2024-01-01T01:00:00Z", interval: 5m,
//	  filter: {resource == "cpu"}, by: {node}
//
// NOTE: builtin:kubernetes.* and builtin:host.* metrics live in Classic storage (OneAgent)
// and are NOT available via Grail DQL when using a platform token (dt0s16.*).
//
// Metrics available in Grail DQL (forwarded via OTel collector):
//   - KSM: kube_node_status_allocatable, kube_pod_container_resource_requests/limits
//   - cAdvisor usage: container_cpu_usage_seconds_total, container_memory_working_set_bytes
//     (requires victoria-cadvisor-federation scrape in dynakube-ksm-otel.yaml)

// kube-state-metrics forwarded via the OTel collector — requests, limits, allocatable.
// Prometheus labels become OTel attributes verbatim: node, namespace, pod, container, resource.
const (
	ksmNodeAllocatable = "kube_node_status_allocatable"
	ksmPodRequests     = "kube_pod_container_resource_requests"
	ksmPodLimits       = "kube_pod_container_resource_limits"
)

// Usage metrics federated from Victoria Metrics into Grail via the OTel collector.
// cadvisorCPU is a recording rule (sum_irate) already in cores/second — use avg() in DQL directly.
// NOTE: Dynatrace OTLP ingestion converts colons in Prometheus metric names to underscores,
// so the Prometheus name "node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate"
// becomes "node_namespace_pod_container_container_cpu_usage_seconds_total_sum_irate" in Grail DQL.
// cadvisorMemory is a gauge (bytes); pause containers dropped by metric_relabel_configs.
// Labels on cadvisorCPU: namespace, pod, node (already summed across containers).
// Labels on cadvisorMemory: namespace, pod, container (filter container!="POD" applied at scrape).
const (
	cadvisorCPU    = "node_namespace_pod_container_container_cpu_usage_seconds_total_sum_irate"
	cadvisorMemory = "container_memory_working_set_bytes"
)

// Shared DQL format prefixes used by multiple builders.
const (
	// tsAvgFmt is the opening clause for avg-aggregated timeseries (gauges, pre-computed rates).
	tsAvgFmt = "timeseries val = avg(`%s`), from: \"%s\", to: \"%s\", interval: %s, "
	// tsSumFmt is the opening clause for sum-aggregated timeseries (pod/node totals).
	tsSumFmt = "timeseries val = sum(`%s`), from: \"%s\", to: \"%s\", interval: %s, "
)

// dynatraceEscapeForDQL escapes a string value for use inside a DQL filter string literal.
// DQL strings are double-quoted; backslash and double-quote must be backslash-escaped.
func dynatraceEscapeForDQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// buildDynatraceNodeFilterCondition returns the inner DQL filter condition for node matching.
// If nodeName contains "|" (pipe-separated list of Prometheus-style regex patterns), it splits
// by "|", strips trailing ".*" or "*" from each part, and returns an in() condition.
// Otherwise it strips any trailing ".*" and returns a simple equality condition.
// The returned string is meant to be embedded inside filter: { ... }.
func buildDynatraceNodeFilterCondition(nodeName string) string {
	if !strings.Contains(nodeName, "|") {
		// Single node: strip .* Prometheus wildcard suffix, use equality.
		name := strings.TrimSuffix(nodeName, ".*")
		name = strings.TrimSuffix(name, "*")
		return fmt.Sprintf(`node == "%s"`, dynatraceEscapeForDQL(name))
	}
	// Multi-node: split by pipe, strip .* suffix, build in() condition.
	parts := strings.Split(nodeName, "|")
	quotedParts := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSuffix(p, ".*")
		p = strings.TrimSuffix(p, "*")
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		quotedParts = append(quotedParts, fmt.Sprintf(`"%s"`, dynatraceEscapeForDQL(p)))
	}
	if len(quotedParts) == 0 {
		return `node == ""`
	}
	if len(quotedParts) == 1 {
		return fmt.Sprintf(`node == %s`, quotedParts[0])
	}
	return fmt.Sprintf("in(node, %s)", strings.Join(quotedParts, ", "))
}

// dqlCAdvisorPodCPU builds a DQL timeseries for actual CPU usage of a pod.
// cadvisorCPU is a pre-computed irate recording rule (cores/s gauge) — use avg() directly.
func dqlCAdvisorPodCPU(namespace, podName string) string {
	return fmt.Sprintf(
		tsAvgFmt+"filter: {namespace == \"%s\" and pod == \"%s\"}, by: {namespace, pod}",
		cadvisorCPU, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(podName),
	)
}

// HTTP metric names forwarded from Istio/Envoy via OTel collector into Grail.
// These mirror the Prometheus metric names used by buildPrometheusWorkloadQueries.
// Both metrics return records=[] (no error) when queried against Grail accounts that
// have the OTel collector running but no Istio data — that is expected behaviour.
const (
	istioHTTPRequests        = "container_http_requests_total"
	istioHTTPDurationSeconds = "container_http_request_duration_seconds"
)

// Network metric names from cAdvisor forwarded via OTel into Grail.
// These may return records=[] when the OTel collector does not scrape network cAdvisor metrics.
const (
	cAdvisorNetworkReceive  = "container_network_receive_bytes_total"
	cAdvisorNetworkTransmit = "container_network_transmit_bytes_total"
)

// dqlHTTPThroughput builds a DQL timeseries for HTTP request throughput of a workload.
// Uses container_http_requests_total (Istio/Envoy metric forwarded via OTel).
// Label names follow Istio destination conventions: destination_workload_namespace /
// destination_workload_name.
func dqlHTTPThroughput(namespace, workloadName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {destination_workload_namespace == "%s" and destination_workload_name == "%s"}, `+
			"by: {destination_workload_name, destination_workload_namespace}",
		istioHTTPRequests,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlHTTPErrorRate builds a DQL timeseries for HTTP 5xx error count of a workload.
// Filters to response_code_class == "5xx" (Istio attribute forwarded via OTel).
func dqlHTTPErrorRate(namespace, workloadName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {destination_workload_namespace == "%s" and destination_workload_name == "%s" and response_code_class == "5xx"}, `+
			"by: {destination_workload_name, destination_workload_namespace}",
		istioHTTPRequests,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlHTTPLatencyPercentile builds a DQL timeseries for HTTP latency at a given percentile.
// Uses container_http_request_duration_seconds histogram forwarded via OTel.
func dqlHTTPLatencyPercentile(namespace, workloadName string, percentile int) string {
	return fmt.Sprintf(
		"timeseries val = percentile(`%s`, %d), from: \"%s\", to: \"%s\", interval: %s, "+
			`filter: {destination_workload_namespace == "%s" and destination_workload_name == "%s"}, `+
			"by: {destination_workload_name, destination_workload_namespace}",
		istioHTTPDurationSeconds, percentile,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlHTTPStatus builds a DQL timeseries for HTTP request count grouped by response code.
// Uses container_http_requests_total (Istio/Envoy) grouped by response_code dimension.
func dqlHTTPStatus(namespace, workloadName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {destination_workload_namespace == "%s" and destination_workload_name == "%s"}, `+
			"by: {destination_workload_name, response_code}",
		istioHTTPRequests,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlHTTPLatencySum builds a DQL timeseries for total (sum) HTTP request duration.
// Uses container_http_request_duration_seconds histogram forwarded via OTel.
// Provides parity with the Prometheus/Datadog/NewRelic http_latency_sum metric.
func dqlHTTPLatencySum(namespace, workloadName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {destination_workload_namespace == "%s" and destination_workload_name == "%s"}, `+
			"by: {destination_workload_name, destination_workload_namespace}",
		istioHTTPDurationSeconds,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlHTTPMaxResponseTime builds a DQL timeseries for maximum HTTP response time.
// Uses container_http_request_duration_seconds (Istio/Envoy histogram forwarded via OTel).
func dqlHTTPMaxResponseTime(namespace, workloadName string) string {
	return fmt.Sprintf(
		"timeseries val = max(`%s`), from: \"%s\", to: \"%s\", interval: %s, "+
			`filter: {destination_workload_namespace == "%s" and destination_workload_name == "%s"}, `+
			"by: {destination_workload_name}",
		istioHTTPDurationSeconds,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlNetworkReceive builds a DQL timeseries for network receive bytes.
// podFilter is a pre-built DQL filter expression for the pod dimension (equality or startsWith).
func dqlNetworkReceive(namespace, podFilter string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {namespace == "%s" and %s}, by: {pod}`,
		cAdvisorNetworkReceive,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), podFilter,
	)
}

// dqlNetworkTransmit builds a DQL timeseries for network transmit bytes.
// podFilter is a pre-built DQL filter expression for the pod dimension.
func dqlNetworkTransmit(namespace, podFilter string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {namespace == "%s" and %s}, by: {pod}`,
		cAdvisorNetworkTransmit,
		dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), podFilter,
	)
}

// dqlCAdvisorWorkloadCPU builds a DQL timeseries for CPU usage of a non-pod workload.
// Uses startsWith(pod, "workload-name-") to match pods owned by the workload (same
// convention as Prometheus pod=~"workload-name-.*").
// Verified working in Grail: startsWith() is supported in timeseries filter: clauses.
func dqlCAdvisorWorkloadCPU(namespace, workloadName string) string {
	return fmt.Sprintf(
		tsAvgFmt+`filter: {namespace == "%s" and startsWith(pod, "%s-")}, by: {namespace, pod}`,
		cadvisorCPU, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlCAdvisorWorkloadMemory builds a DQL timeseries for memory usage of a non-pod workload.
// Excludes the infra "POD" container (pause container) to match cAdvisor metric_relabel behaviour.
func dqlCAdvisorWorkloadMemory(namespace, workloadName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {namespace == "%s" and startsWith(pod, "%s-") and container != "POD"}, by: {namespace, pod}`,
		cadvisorMemory, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlKSMWorkloadTimeseries builds a DQL timeseries for a KSM metric scoped to all pods
// belonging to a workload, using startsWith(pod, "workload-name-") pod matching.
func dqlKSMWorkloadTimeseries(metricID, resource, namespace, workloadName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {resource == "%s" and namespace == "%s" and startsWith(pod, "%s-")}, by: {namespace, pod}`,
		metricID, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		resource, dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(workloadName),
	)
}

// dqlCAdvisorPodMemory builds a DQL timeseries for actual memory usage of a pod.
// working_set_bytes is a gauge; sum across containers within the pod.
// If containerName is non-empty, the filter is narrowed to that single container.
func dqlCAdvisorPodMemory(namespace, podName, containerName string) string {
	filter := fmt.Sprintf("namespace == \"%s\" and pod == \"%s\"",
		dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(podName))
	if containerName != "" {
		filter += fmt.Sprintf(" and container == \"%s\"", dynatraceEscapeForDQL(containerName))
	}
	return fmt.Sprintf(
		tsSumFmt+"filter: {%s}, by: {namespace, pod}",
		cadvisorMemory, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder, filter,
	)
}

// dqlCAdvisorNodeCPU builds a DQL timeseries for total CPU usage across all containers on a node.
// Filters by the "node" label added by the kubernetes_sd relabeling in the OTel collector.
// nodeName may be a single name or a pipe-separated list of Prometheus-style patterns (with .* suffix).
func dqlCAdvisorNodeCPU(nodeName string) string {
	return fmt.Sprintf(
		tsAvgFmt+"filter: {%s}, by: {node} | fields val = arrayLast(val), timeframe, node",
		cadvisorCPU, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		buildDynatraceNodeFilterCondition(nodeName),
	)
}

// dqlCAdvisorNodeMemory builds a DQL timeseries for total memory usage across all containers on a node.
// nodeName may be a single name or a pipe-separated list of Prometheus-style patterns (with .* suffix).
func dqlCAdvisorNodeMemory(nodeName string) string {
	return fmt.Sprintf(
		tsSumFmt+"filter: {%s}, by: {node} | fields val = arrayLast(val), timeframe, node",
		cadvisorMemory, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		buildDynatraceNodeFilterCondition(nodeName),
	)
}

// dqlKSMAllNodesTimeseries builds a DQL timeseries template for a kube-state-metrics metric
// scoped by resource (e.g. "cpu" or "memory") across all nodes. The "node" attribute is the
// prometheus label forwarded verbatim by the OTel collector — not k8s.node.name.
// Used for cluster-wide aggregate metrics where Go-side post-processing computes percentiles.
func dqlKSMAllNodesTimeseries(metricID, resource string) string {
	return fmt.Sprintf(
		tsAvgFmt+"filter: {resource == \"%s\"}, by: {node} | fields val = arrayLast(val), timeframe, node",
		metricID, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		resource,
	)
}

// dqlKSMClusterTotal builds a DQL timeseries for a cluster-wide total of a KSM resource metric.
// No by: clause — sums across all dimensions to produce a single aggregate time series.
// Used for cpu_total (allocatable), cpu_request, cpu_limit, mem_total, mem_request, mem_limit.
func dqlKSMClusterTotal(metricID, resource string) string {
	return fmt.Sprintf(
		tsSumFmt+"filter: {resource == \"%s\"} | fields val = arrayLast(val), timeframe",
		metricID, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		resource,
	)
}

// dqlCAdvisorClusterCPU builds a cluster-wide DQL timeseries for total CPU usage across all pods.
// Sums the pre-computed sum_irate recording rule (already cores/s) without a by: clause.
func dqlCAdvisorClusterCPU() string {
	return fmt.Sprintf(
		"timeseries val = sum(`%s`), from: \"%s\", to: \"%s\", interval: %s  | fields val = arrayLast(val), timeframe",
		cadvisorCPU, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
	)
}

// dqlCAdvisorClusterMem builds a cluster-wide DQL timeseries for total memory usage across all pods.
func dqlCAdvisorClusterMem() string {
	return fmt.Sprintf(
		"timeseries val = sum(`%s`), from: \"%s\", to: \"%s\", interval: %s | fields val = arrayLast(val), timeframe",
		cadvisorMemory, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
	)
}

// dqlCAdvisorAllNodesCPU builds a per-node DQL timeseries for actual CPU usage.
// Sums the pre-computed sum_irate recording rule (cores/s) by {node} so Go-side
// post-processing can compute percentiles (p90/p50/max) across nodes.
// NOTE: container_memory_working_set_bytes has no reliable node label (the "node"
// attribute is a pool name, not a hostname), so memory percentiles still use
// ksmNodeAllocatable which gives allocatable capacity per node.
func dqlCAdvisorAllNodesCPU() string {
	return fmt.Sprintf(
		tsSumFmt+"by: {node} | fields val = arrayLast(val), timeframe, node",
		cadvisorCPU, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
	)
}

// dqlKSMNodeTimeseries builds a DQL timeseries template for a kube-state-metrics metric
// scoped to a single node (or pipe-separated list) by name and resource type.
// Uses avg() — suitable for capacity gauges (e.g. kube_node_status_allocatable).
func dqlKSMNodeTimeseries(metricID, resource, nodeName string) string {
	return fmt.Sprintf(
		tsAvgFmt+`filter: {resource == "%s" and %s}, by: {node} | fields val = arrayLast(val), timeframe, node`,
		metricID, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		resource, buildDynatraceNodeFilterCondition(nodeName),
	)
}

// dqlKSMNodeSum builds a DQL timeseries for a kube-state-metrics metric scoped to a single node
// (or pipe-separated list), aggregated with sum() across all matching containers/pods on that node.
// Uses sum() — suitable for request/limit metrics (kube_pod_container_resource_requests/limits)
// where KSM >= 2.x exposes a "node" label for the pod's scheduled node.
func dqlKSMNodeSum(metricID, resource, nodeName string) string {
	return fmt.Sprintf(
		tsSumFmt+`filter: {resource == "%s" and %s}, by: {node} | fields val = arrayLast(val), timeframe, node`,
		metricID, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder,
		resource, buildDynatraceNodeFilterCondition(nodeName),
	)
}

// dqlKSMPodTimeseries builds a DQL timeseries template for a kube-state-metrics metric
// scoped to a specific pod by namespace, pod name, and resource type.
// Uses sum() to aggregate across containers within the pod.
// If containerName is non-empty, the filter is narrowed to that single container.
func dqlKSMPodTimeseries(metricID, resource, namespace, podName, containerName string) string {
	filter := fmt.Sprintf("resource == \"%s\" and namespace == \"%s\" and pod == \"%s\"",
		resource, dynatraceEscapeForDQL(namespace), dynatraceEscapeForDQL(podName))
	if containerName != "" {
		filter += fmt.Sprintf(" and container == \"%s\"", dynatraceEscapeForDQL(containerName))
	}
	return fmt.Sprintf(
		tsSumFmt+"filter: {%s}, by: {namespace, pod}",
		metricID, dtFromPlaceholder, dtToPlaceholder, dtIntervalPlaceholder, filter,
	)
}

// buildDynatraceNodeQueries returns DQL timeseries templates for Kubernetes node metrics.
// Returns empty map if meta.NodeName is empty.
//
// Available metrics:
//   - cpu_usage / cpu_usage_line → container_cpu_usage_seconds_total (cAdvisor, avg all containers)
//   - memory_usage / memory_usage_line → container_memory_working_set_bytes (cAdvisor, sum)
//   - cpu_allocatable → kube_node_status_allocatable{resource="cpu"}  (KSM, avg)
//   - memory_allocatable → kube_node_status_allocatable{resource="memory"} (KSM, avg)
//   - cpu_request / memory_request → kube_pod_container_resource_requests per node (KSM, sum)
//   - cpu_limit / memory_limit → kube_pod_container_resource_limits per node (KSM, sum)
//
// Note: disk_total / disk_used are not available — no filesystem metric is scraped into Grail.
// Requires victoria-cadvisor-federation scrape in dynakube-ksm-otel.yaml for usage metrics.
// Requires KSM >= 2.x for the "node" label on kube_pod_container_resource_requests/limits.
func buildDynatraceNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	if meta.NodeName == "" {
		return queries
	}

	node := meta.NodeName

	for _, metricKey := range metrics {
		switch metricKey {
		case "cpu_usage", "cpu_usage_line":
			queries[metricKey] = dqlCAdvisorNodeCPU(node)
		case "memory_usage", "memory_usage_line":
			queries[metricKey] = dqlCAdvisorNodeMemory(node)
		case "cpu_allocatable":
			queries[metricKey] = dqlKSMNodeTimeseries(ksmNodeAllocatable, "cpu", node)
		case "memory_allocatable":
			queries[metricKey] = dqlKSMNodeTimeseries(ksmNodeAllocatable, "memory", node)
		case "cpu_request":
			queries[metricKey] = dqlKSMNodeSum(ksmPodRequests, "cpu", node)
		case "cpu_limit":
			queries[metricKey] = dqlKSMNodeSum(ksmPodLimits, "cpu", node)
		case "memory_request":
			queries[metricKey] = dqlKSMNodeSum(ksmPodRequests, "memory", node)
		case "memory_limit":
			queries[metricKey] = dqlKSMNodeSum(ksmPodLimits, "memory", node)
		}
	}
	return queries
}

// buildDynatraceClusterQueries returns DQL timeseries templates for cluster-wide aggregate metrics.
// Called when no namespace, name, or node name is present in the request.
//
// Available metrics:
//   - p90_cpu / p50_cpu / max_usage_cpu: actual CPU usage per node (cAdvisor sum_irate by {node});
//     Go-side post-processing computes percentile across nodes.
//   - p90_mem / p50_mem / max_usage_mem: per-node KSM allocatable memory; Go-side percentile.
//     NOTE: container_memory_working_set_bytes has no reliable node label (pool name, not hostname),
//     so memory percentiles use allocatable capacity rather than actual consumption.
//   - cpu_real / mem_real: total actual usage across all pods (cAdvisor sum_irate / working_set).
//   - cpu_total / mem_total: total allocatable capacity across all nodes (KSM, single series).
//   - cpu_request / cpu_limit / mem_request / mem_limit: cluster-wide totals (KSM, single series).
func buildDynatraceClusterQueries(metrics []string) map[string]string {
	queries := make(map[string]string)
	for _, metricKey := range metrics {
		switch metricKey {
		case "p90_cpu", "p50_cpu", "max_usage_cpu":
			queries[metricKey] = dqlCAdvisorAllNodesCPU()
		case "p90_mem", "p50_mem", "max_usage_mem":
			queries[metricKey] = dqlKSMAllNodesTimeseries(ksmNodeAllocatable, "memory")
		case "cpu_real":
			queries[metricKey] = dqlCAdvisorClusterCPU()
		case "mem_real":
			queries[metricKey] = dqlCAdvisorClusterMem()
		case "cpu_total":
			queries[metricKey] = dqlKSMClusterTotal(ksmNodeAllocatable, "cpu")
		case "mem_total":
			queries[metricKey] = dqlKSMClusterTotal(ksmNodeAllocatable, "memory")
		case "cpu_request":
			queries[metricKey] = dqlKSMClusterTotal(ksmPodRequests, "cpu")
		case "cpu_limit":
			queries[metricKey] = dqlKSMClusterTotal(ksmPodLimits, "cpu")
		case "mem_request", "memory_request":
			queries[metricKey] = dqlKSMClusterTotal(ksmPodRequests, "memory")
		case "mem_limit", "memory_limit":
			queries[metricKey] = dqlKSMClusterTotal(ksmPodLimits, "memory")
		}
	}
	return queries
}

// buildDynatraceWorkloadQueries returns DQL timeseries templates for Kubernetes workload/pod metrics.
//
// Available metrics:
//   - Cluster-wide (p90_cpu, p50_cpu, max_usage_cpu, p90_mem, p50_mem, max_usage_mem):
//     kube_node_status_allocatable across all nodes; Go-side percentile post-processing.
//   - Pod-scoped usage (kind == "pod"): cpu_usage → cAdvisor rate(); memory_usage → cAdvisor gauge.
//   - Pod-scoped requests/limits (kind == "pod"): via kube_pod_container_resource_requests/limits.
//
// Not available: workload-level (non-pod) usage/requests — KSM has no workload dimension and
// builtin:kubernetes.workload.* is in Classic storage (not queryable via platform token).
func buildDynatraceWorkloadQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)
	ns, name := meta.Namespace, meta.Name

	for _, metricKey := range metrics {
		switch metricKey {
		case "p90_cpu", "p50_cpu", "max_usage_cpu":
			queries[metricKey] = dqlCAdvisorAllNodesCPU()
		case "p90_mem", "p50_mem", "max_usage_mem":
			queries[metricKey] = dqlKSMAllNodesTimeseries(ksmNodeAllocatable, "memory")
		case "http_throughput":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPThroughput(ns, name)
			}
		case "http_error_rate":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPErrorRate(ns, name)
			}
		case "http_latency_p95":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPLatencyPercentile(ns, name, 95)
			}
		case "http_latency_p99":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPLatencyPercentile(ns, name, 99)
			}
		case "http_status":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPStatus(ns, name)
			}
		case "http_max_response_time":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPMaxResponseTime(ns, name)
			}
		case "http_latency_sum":
			if ns != "" && name != "" {
				queries[metricKey] = dqlHTTPLatencySum(ns, name)
			}
		case "network_receive_packet":
			if ns != "" && name != "" {
				pf := buildDynatraceWorkloadPodFilter(name, meta.Kind)
				queries[metricKey] = dqlNetworkReceive(ns, pf)
			}
		case "network_transmit_packets":
			if ns != "" && name != "" {
				pf := buildDynatraceWorkloadPodFilter(name, meta.Kind)
				queries[metricKey] = dqlNetworkTransmit(ns, pf)
			}
		default:
			if ns == "" || name == "" {
				continue
			}
			if meta.Kind == "pod" {
				addDynatracePodQuery(queries, metricKey, ns, name, meta.ContainerName)
			} else {
				addDynatraceWorkloadQuery(queries, metricKey, ns, name)
			}
		}
	}
	return queries
}

// addDynatracePodQuery populates a single pod-scoped DQL query into the queries map.
// containerName is optional; when non-empty it narrows the filter to a single container.
func addDynatracePodQuery(queries map[string]string, metricKey, ns, name, containerName string) {
	switch metricKey {
	case "cpu_usage":
		queries[metricKey] = dqlCAdvisorPodCPU(ns, name)
	case "memory_usage":
		queries[metricKey] = dqlCAdvisorPodMemory(ns, name, containerName)
	case "cpu_request":
		queries[metricKey] = dqlKSMPodTimeseries(ksmPodRequests, "cpu", ns, name, containerName)
	case "cpu_limit":
		queries[metricKey] = dqlKSMPodTimeseries(ksmPodLimits, "cpu", ns, name, containerName)
	case "memory_request":
		queries[metricKey] = dqlKSMPodTimeseries(ksmPodRequests, "memory", ns, name, containerName)
	case "memory_limit":
		queries[metricKey] = dqlKSMPodTimeseries(ksmPodLimits, "memory", ns, name, containerName)
	}
}

// buildDynatraceWorkloadPodFilter returns the DQL pod dimension filter expression for a workload.
// For pod kind: exact equality (pod == "name").
// For all other kinds (Deployment, StatefulSet, DaemonSet, …): prefix match
// (startsWith(pod, "name-")), mirroring Prometheus pod=~"name-.*" convention.
func buildDynatraceWorkloadPodFilter(name, kind string) string {
	if kind == "pod" {
		return fmt.Sprintf(`pod == "%s"`, dynatraceEscapeForDQL(name))
	}
	return fmt.Sprintf(`startsWith(pod, "%s-")`, dynatraceEscapeForDQL(name))
}

// addDynatraceWorkloadQuery populates resource/network DQL queries for non-pod workload kinds.
// Uses startsWith(pod, "name-") to match all pods owned by the workload.
func addDynatraceWorkloadQuery(queries map[string]string, metricKey, ns, name string) {
	switch metricKey {
	case "cpu_usage":
		queries[metricKey] = dqlCAdvisorWorkloadCPU(ns, name)
	case "memory_usage":
		queries[metricKey] = dqlCAdvisorWorkloadMemory(ns, name)
	case "cpu_request":
		queries[metricKey] = dqlKSMWorkloadTimeseries(ksmPodRequests, "cpu", ns, name)
	case "cpu_limit":
		queries[metricKey] = dqlKSMWorkloadTimeseries(ksmPodLimits, "cpu", ns, name)
	case "memory_request":
		queries[metricKey] = dqlKSMWorkloadTimeseries(ksmPodRequests, "memory", ns, name)
	case "memory_limit":
		queries[metricKey] = dqlKSMWorkloadTimeseries(ksmPodLimits, "memory", ns, name)
	}
}
