package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- dynatraceEscapeForDQL ---

func TestDynatraceEscapeForDQL_Plain(t *testing.T) {
	assert.Equal(t, "simple", dynatraceEscapeForDQL("simple"))
}

func TestDynatraceEscapeForDQL_Quote(t *testing.T) {
	assert.Equal(t, `say \"hello\"`, dynatraceEscapeForDQL(`say "hello"`))
}

func TestDynatraceEscapeForDQL_Backslash(t *testing.T) {
	assert.Equal(t, `path\\to\\file`, dynatraceEscapeForDQL(`path\to\file`))
}

func TestDynatraceEscapeForDQL_BackslashThenQuote(t *testing.T) {
	// Input: \"  →  Output: \\\"
	assert.Equal(t, `\\\"`, dynatraceEscapeForDQL(`\"`))
}

// --- buildDynatraceNodeFilterCondition ---

func TestBuildDynatraceNodeFilterCondition_SingleExact(t *testing.T) {
	cond := buildDynatraceNodeFilterCondition("my-node")
	assert.Equal(t, `node == "my-node"`, cond)
}

func TestBuildDynatraceNodeFilterCondition_SingleWithWildcard(t *testing.T) {
	cond := buildDynatraceNodeFilterCondition("gke-pool-abc.*")
	assert.Equal(t, `node == "gke-pool-abc"`, cond, ".* suffix must be stripped")
}

func TestBuildDynatraceNodeFilterCondition_MultiNode(t *testing.T) {
	cond := buildDynatraceNodeFilterCondition("gke-pool-abc.*|gke-pool-xyz.*|gke-pool-qrs.*")
	assert.Contains(t, cond, "in(node,")
	assert.Contains(t, cond, `"gke-pool-abc"`)
	assert.Contains(t, cond, `"gke-pool-xyz"`)
	assert.Contains(t, cond, `"gke-pool-qrs"`)
	assert.NotContains(t, cond, ".*", "wildcards must be stripped")
}

// --- buildDynatraceNodeQueries ---

func TestBuildDynatraceNodeQueries_EmptyNodeName(t *testing.T) {
	meta := RequestMetadata{NodeName: ""}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_allocatable", "memory_allocatable"})
	assert.Empty(t, queries, "empty NodeName should produce no queries")
}

// cpu_usage/memory_usage use cAdvisor federation; disk and request/limit still unsupported.
func TestBuildDynatraceNodeQueries_UsageViacAdvisor(t *testing.T) {
	meta := RequestMetadata{NodeName: "my-node"}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_usage", "cpu_usage_line", "memory_usage", "memory_usage_line"})

	assert.Contains(t, queries["cpu_usage"], cadvisorCPU)
	assert.Contains(t, queries["cpu_usage_line"], cadvisorCPU)
	assert.Contains(t, queries["memory_usage"], cadvisorMemory)
	assert.Contains(t, queries["memory_usage_line"], cadvisorMemory)
}

func TestBuildDynatraceNodeQueries_MultiNodePipeSeparated(t *testing.T) {
	nodeName := "gke-pool-58cl.*|gke-pool-xkz9.*|gke-pool-xpkl.*"
	meta := RequestMetadata{NodeName: nodeName}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_usage_line", "memory_usage_line"})

	assert.Contains(t, queries["cpu_usage_line"], "in(node,")
	assert.Contains(t, queries["cpu_usage_line"], `"gke-pool-58cl"`)
	assert.NotContains(t, queries["cpu_usage_line"], ".*", "wildcards must not appear in DQL filter")

	assert.Contains(t, queries["memory_usage_line"], "in(node,")
	assert.Contains(t, queries["memory_usage_line"], `"gke-pool-xkz9"`)
}

func TestBuildDynatraceNodeQueries_DiskNotSupported(t *testing.T) {
	meta := RequestMetadata{NodeName: "my-node"}
	unsupported := []string{"disk_used", "disk_avail"}
	queries := buildDynatraceNodeQueries(meta, unsupported)
	assert.Empty(t, queries, "disk metrics are not available in Grail DQL (no filesystem scrape)")
}

func TestBuildDynatraceNodeQueries_RequestsAndLimits(t *testing.T) {
	meta := RequestMetadata{NodeName: "ip-10-0-1-5.ec2.internal"}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"})

	assert.Len(t, queries, 4)
	// cpu_request: sum of ksmPodRequests filtered by resource=cpu and node
	assert.Contains(t, queries["cpu_request"], ksmPodRequests)
	assert.Contains(t, queries["cpu_request"], `resource == "cpu"`)
	assert.Contains(t, queries["cpu_request"], "ip-10-0-1-5.ec2.internal")
	assert.Contains(t, queries["cpu_request"], "timeseries val = sum(")
	// cpu_limit: sum of ksmPodLimits
	assert.Contains(t, queries["cpu_limit"], ksmPodLimits)
	assert.Contains(t, queries["cpu_limit"], `resource == "cpu"`)
	// memory_request: sum of ksmPodRequests filtered by resource=memory
	assert.Contains(t, queries["memory_request"], ksmPodRequests)
	assert.Contains(t, queries["memory_request"], `resource == "memory"`)
	// memory_limit: sum of ksmPodLimits filtered by resource=memory
	assert.Contains(t, queries["memory_limit"], ksmPodLimits)
	assert.Contains(t, queries["memory_limit"], `resource == "memory"`)
}

func TestBuildDynatraceNodeQueries_Allocatable(t *testing.T) {
	meta := RequestMetadata{NodeName: "ip-10-0-1-5.ec2.internal"}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_allocatable", "memory_allocatable"})

	cpuQ, ok := queries["cpu_allocatable"]
	assert.True(t, ok)
	assert.Contains(t, cpuQ, ksmNodeAllocatable)
	assert.Contains(t, cpuQ, `resource == "cpu"`)
	assert.Contains(t, cpuQ, "ip-10-0-1-5.ec2.internal")
	assert.Contains(t, cpuQ, dtFromPlaceholder)
	assert.Contains(t, cpuQ, dtToPlaceholder)
	assert.Contains(t, cpuQ, dtIntervalPlaceholder)

	memQ, ok := queries["memory_allocatable"]
	assert.True(t, ok)
	assert.Contains(t, memQ, ksmNodeAllocatable)
	assert.Contains(t, memQ, `resource == "memory"`)
}

func TestBuildDynatraceNodeQueries_NodeNameEscaped(t *testing.T) {
	meta := RequestMetadata{NodeName: `node"with"quotes`}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_allocatable"})
	q := queries["cpu_allocatable"]
	assert.NotContains(t, q, `"node"with"quotes"`, "unescaped quotes would break DQL parse")
	assert.Contains(t, q, `node\"with\"quotes`)
}

func TestBuildDynatraceNodeQueries_IsDQLTemplate(t *testing.T) {
	meta := RequestMetadata{NodeName: "my-node"}
	queries := buildDynatraceNodeQueries(meta, []string{"cpu_allocatable"})
	q := queries["cpu_allocatable"]
	assert.True(t, len(q) > 10 && q[:10] == "timeseries", "query must start with 'timeseries'")
}

func TestBuildDynatraceNodeQueries_UnknownMetricSkipped(t *testing.T) {
	meta := RequestMetadata{NodeName: "my-node"}
	queries := buildDynatraceNodeQueries(meta, []string{"nonexistent_metric"})
	assert.Empty(t, queries, "unknown metric keys should be skipped")
}

// --- buildDynatraceClusterQueries ---

func TestBuildDynatraceClusterQueries_CpuReal(t *testing.T) {
	queries := buildDynatraceClusterQueries([]string{"cpu_real"})
	q, ok := queries["cpu_real"]
	assert.True(t, ok)
	assert.Contains(t, q, cadvisorCPU)
	assert.True(t, len(q) > 10 && q[:10] == "timeseries")
}

func TestBuildDynatraceClusterQueries_MemReal(t *testing.T) {
	queries := buildDynatraceClusterQueries([]string{"mem_real"})
	q, ok := queries["mem_real"]
	assert.True(t, ok)
	assert.Contains(t, q, cadvisorMemory)
}

func TestBuildDynatraceClusterQueries_CpuTotal(t *testing.T) {
	queries := buildDynatraceClusterQueries([]string{"cpu_total"})
	q, ok := queries["cpu_total"]
	assert.True(t, ok)
	assert.Contains(t, q, ksmNodeAllocatable)
	assert.Contains(t, q, `resource == "cpu"`)
	assert.NotContains(t, q, "by: {node}", "cpu_total must be a single aggregate (no by:)")
}

func TestBuildDynatraceClusterQueries_CpuRequestLimit(t *testing.T) {
	queries := buildDynatraceClusterQueries([]string{"cpu_request", "cpu_limit", "mem_request", "mem_limit"})
	assert.Contains(t, queries["cpu_request"], ksmPodRequests)
	assert.Contains(t, queries["cpu_request"], `resource == "cpu"`)
	assert.Contains(t, queries["cpu_limit"], ksmPodLimits)
	assert.Contains(t, queries["mem_request"], ksmPodRequests)
	assert.Contains(t, queries["mem_request"], `resource == "memory"`)
	assert.Contains(t, queries["mem_limit"], ksmPodLimits)
}

// The frontend sends "memory_limit" and "memory_request" (not "mem_limit"/"mem_request").
// These aliases must be handled so the cluster UI populates correctly.
func TestBuildDynatraceClusterQueries_MemoryAliases(t *testing.T) {
	queries := buildDynatraceClusterQueries([]string{"memory_request", "memory_limit"})
	assert.Contains(t, queries["memory_request"], ksmPodRequests)
	assert.Contains(t, queries["memory_request"], `resource == "memory"`)
	assert.Contains(t, queries["memory_limit"], ksmPodLimits)
}

func TestBuildDynatraceClusterQueries_Percentiles(t *testing.T) {
	// CPU p90/p50/max use actual cAdvisor usage per node.
	queries := buildDynatraceClusterQueries([]string{"p90_cpu", "p50_cpu", "max_usage_cpu"})
	for _, key := range []string{"p90_cpu", "p50_cpu", "max_usage_cpu"} {
		q, ok := queries[key]
		assert.True(t, ok, "expected %s in queries", key)
		assert.Contains(t, q, cadvisorCPU, "CPU percentiles must use cAdvisor usage, not KSM allocatable")
		assert.Contains(t, q, "by: {node}")
	}
	// Memory p90/p50/max fall back to KSM allocatable (no reliable node label on working_set).
	memQueries := buildDynatraceClusterQueries([]string{"p90_mem", "p50_mem", "max_usage_mem"})
	for _, key := range []string{"p90_mem", "p50_mem", "max_usage_mem"} {
		q, ok := memQueries[key]
		assert.True(t, ok, "expected %s in queries", key)
		assert.Contains(t, q, ksmNodeAllocatable)
		assert.Contains(t, q, "by: {node}")
	}
}

// --- buildDynatraceWorkloadQueries ---

func TestBuildDynatraceWorkloadQueries_ClusterWide_CPU(t *testing.T) {
	meta := RequestMetadata{} // no ns/name
	queries := buildDynatraceWorkloadQueries(meta, []string{"p90_cpu", "p50_cpu", "max_usage_cpu"})
	for _, key := range []string{"p90_cpu", "p50_cpu", "max_usage_cpu"} {
		q, ok := queries[key]
		assert.True(t, ok, "expected %s in queries", key)
		assert.Contains(t, q, cadvisorCPU, "CPU percentiles must use cAdvisor usage, not KSM allocatable")
		assert.Contains(t, q, "by: {node}")
	}
}

func TestBuildDynatraceWorkloadQueries_ClusterWide_Mem(t *testing.T) {
	meta := RequestMetadata{} // no ns/name
	queries := buildDynatraceWorkloadQueries(meta, []string{"p90_mem", "p50_mem", "max_usage_mem"})
	for _, key := range []string{"p90_mem", "p50_mem", "max_usage_mem"} {
		q, ok := queries[key]
		assert.True(t, ok, "expected %s in queries", key)
		assert.Contains(t, q, ksmNodeAllocatable)
		assert.Contains(t, q, `resource == "memory"`)
	}
}

// cpu_usage and memory_usage for non-pod workloads use startsWith(pod, "name-") pod prefix matching.
func TestBuildDynatraceWorkloadQueries_UsageNotSupported(t *testing.T) {
	meta := RequestMetadata{Namespace: "production", Name: "my-app", Kind: "deployment"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_usage", "memory_usage"})
	assert.Contains(t, queries["cpu_usage"], cadvisorCPU)
	assert.Contains(t, queries["cpu_usage"], `startsWith(pod, "my-app-")`)
	assert.Contains(t, queries["memory_usage"], cadvisorMemory)
	assert.Contains(t, queries["memory_usage"], `startsWith(pod, "my-app-")`)
}

func TestBuildDynatraceWorkloadQueriesPodUsageCAdvisor(t *testing.T) {
	meta := RequestMetadata{Namespace: "default", Name: "my-pod-xyz", Kind: "pod"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_usage", "memory_usage"})

	assert.Contains(t, queries["cpu_usage"], cadvisorCPU)
	assert.Contains(t, queries["cpu_usage"], "default")
	assert.Contains(t, queries["cpu_usage"], "my-pod-xyz")
	assert.Contains(t, queries["memory_usage"], cadvisorMemory)
	assert.Contains(t, queries["memory_usage"], "default")
}

// Non-pod workload request/limit uses startsWith(pod, "name-") to aggregate across pods.
func TestBuildDynatraceWorkloadQueries_WorkloadResourcesNotSupported(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "deployment"}
	metrics := []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"}
	queries := buildDynatraceWorkloadQueries(meta, metrics)
	for _, key := range metrics {
		q, ok := queries[key]
		assert.True(t, ok, "expected %s in queries for deployment kind", key)
		assert.Contains(t, q, `startsWith(pod, "svc-")`, "deployment must use pod prefix filter for %s", key)
	}
}

// Pod request/limit: confirmed working via kube_pod_container_resource_requests/limits.
func TestBuildDynatraceWorkloadQueries_PodResourceMetrics(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "my-pod", Kind: "pod"}
	metrics := []string{"cpu_request", "cpu_limit", "memory_request", "memory_limit"}
	queries := buildDynatraceWorkloadQueries(meta, metrics)

	assert.Contains(t, queries["cpu_request"], ksmPodRequests)
	assert.Contains(t, queries["cpu_request"], `resource == "cpu"`)
	assert.Contains(t, queries["cpu_request"], "ns")
	assert.Contains(t, queries["cpu_request"], "my-pod")

	assert.Contains(t, queries["cpu_limit"], ksmPodLimits)
	assert.Contains(t, queries["memory_request"], ksmPodRequests)
	assert.Contains(t, queries["memory_request"], `resource == "memory"`)
	assert.Contains(t, queries["memory_limit"], ksmPodLimits)
}

func TestBuildDynatraceWorkloadQueries_PodMissingNamespace(t *testing.T) {
	meta := RequestMetadata{Namespace: "", Name: "my-pod", Kind: "pod"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_request"})
	assert.Empty(t, queries, "pod metrics require non-empty namespace")
}

func TestBuildDynatraceWorkloadQueries_PodMissingName(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "", Kind: "pod"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_request"})
	assert.Empty(t, queries, "pod metrics require non-empty name")
}

func TestBuildDynatraceWorkloadQueries_PodNameEscaped(t *testing.T) {
	meta := RequestMetadata{Namespace: `ns"bad`, Name: `pod"name`, Kind: "pod"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_request"})
	q := queries["cpu_request"]
	assert.Contains(t, q, `ns\"bad`)
	assert.Contains(t, q, `pod\"name`)
	assert.NotContains(t, q, `"ns"bad"`)
}

func TestBuildDynatraceWorkloadQueries_PodIsDQLTemplate(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "my-pod", Kind: "pod"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_request"})
	q := queries["cpu_request"]
	assert.True(t, len(q) > 10 && q[:10] == "timeseries", "query must start with 'timeseries'")
}

func TestBuildDynatraceWorkloadQueries_PodContainerNamePropagated(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "my-pod", Kind: "pod", ContainerName: "app"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"memory_usage", "cpu_request", "memory_request"})

	assert.Contains(t, queries["memory_usage"], "app", "containerName must appear in memory_usage filter")
	assert.Contains(t, queries["cpu_request"], "app", "containerName must appear in cpu_request filter")
	assert.Contains(t, queries["memory_request"], "app", "containerName must appear in memory_request filter")
}

func TestBuildDynatraceWorkloadQueries_UnknownMetricSkipped(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "deployment"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"nonexistent_metric"})
	assert.Empty(t, queries, "unknown metric keys should be skipped")
}

// --- HTTP workload metrics ---

func TestBuildDynatraceWorkloadQueries_HTTPThroughput(t *testing.T) {
	meta := RequestMetadata{Namespace: "nudgebee", Name: "api-server", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_throughput"})

	q, ok := queries["http_throughput"]
	assert.True(t, ok, "http_throughput must be present for workload kind")
	assert.True(t, len(q) > 10 && q[:10] == "timeseries", "query must start with 'timeseries'")
	assert.Contains(t, q, istioHTTPRequests)
	assert.Contains(t, q, `"nudgebee"`)
	assert.Contains(t, q, `"api-server"`)
	assert.Contains(t, q, "destination_workload_namespace")
	assert.Contains(t, q, "destination_workload_name")
}

func TestBuildDynatraceWorkloadQueries_HTTPThroughput_MissingNamespace(t *testing.T) {
	meta := RequestMetadata{Namespace: "", Name: "api-server", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_throughput"})
	assert.Empty(t, queries, "http_throughput requires non-empty namespace and name")
}

func TestBuildDynatraceWorkloadQueries_HTTPThroughput_MissingName(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_throughput"})
	assert.Empty(t, queries, "http_throughput requires non-empty namespace and name")
}

func TestBuildDynatraceWorkloadQueries_HTTPErrorRate(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_error_rate"})

	q, ok := queries["http_error_rate"]
	assert.True(t, ok)
	assert.Contains(t, q, istioHTTPRequests)
	assert.Contains(t, q, `"5xx"`)
	assert.Contains(t, q, "response_code_class")
}

func TestBuildDynatraceWorkloadQueries_HTTPLatencyPercentiles(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_latency_p95", "http_latency_p99"})

	q95, ok95 := queries["http_latency_p95"]
	assert.True(t, ok95, "http_latency_p95 must be present")
	assert.Contains(t, q95, istioHTTPDurationSeconds)
	assert.Contains(t, q95, "percentile")
	assert.Contains(t, q95, "95")

	q99, ok99 := queries["http_latency_p99"]
	assert.True(t, ok99, "http_latency_p99 must be present")
	assert.Contains(t, q99, "99")
}

func TestBuildDynatraceWorkloadQueries_HTTPMetricsNameEscaped(t *testing.T) {
	meta := RequestMetadata{Namespace: `my"ns`, Name: `my"svc`, Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_throughput", "http_error_rate"})

	assert.Contains(t, queries["http_throughput"], `my\"ns`)
	assert.Contains(t, queries["http_throughput"], `my\"svc`)
	assert.Contains(t, queries["http_error_rate"], `my\"ns`)
	assert.Contains(t, queries["http_error_rate"], `my\"svc`)
}

func TestBuildDynatraceWorkloadQueries_HTTPStatus(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_status"})
	q := queries["http_status"]
	assert.Contains(t, q, istioHTTPRequests)
	assert.Contains(t, q, "response_code")
	assert.Contains(t, q, `"ns"`)
	assert.Contains(t, q, `"svc"`)
}

func TestBuildDynatraceWorkloadQueries_HTTPMaxResponseTime(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"http_max_response_time"})
	q := queries["http_max_response_time"]
	assert.Contains(t, q, istioHTTPDurationSeconds)
	assert.Contains(t, q, "max")
}

func TestBuildDynatraceWorkloadQueries_NetworkMetrics(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "svc", Kind: "workload"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"network_receive_packet", "network_transmit_packets"})
	assert.Contains(t, queries["network_receive_packet"], cAdvisorNetworkReceive)
	assert.Contains(t, queries["network_receive_packet"], `startsWith(pod, "svc-")`)
	assert.Contains(t, queries["network_transmit_packets"], cAdvisorNetworkTransmit)
}

func TestBuildDynatraceWorkloadQueries_DeploymentCPUMemory(t *testing.T) {
	meta := RequestMetadata{Namespace: "nudgebee", Name: "services-server", Kind: "Deployment"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_usage", "memory_usage", "cpu_request", "memory_limit"})

	// Must use startsWith() for non-pod kind
	assert.Contains(t, queries["cpu_usage"], `startsWith(pod, "services-server-")`)
	assert.Contains(t, queries["cpu_usage"], cadvisorCPU)
	assert.Contains(t, queries["memory_usage"], `startsWith(pod, "services-server-")`)
	assert.Contains(t, queries["memory_usage"], cadvisorMemory)
	assert.Contains(t, queries["cpu_request"], `startsWith(pod, "services-server-")`)
	assert.Contains(t, queries["memory_limit"], `startsWith(pod, "services-server-")`)
}

func TestBuildDynatraceWorkloadQueries_PodCPU_ExactMatch(t *testing.T) {
	meta := RequestMetadata{Namespace: "ns", Name: "my-pod-abc", Kind: "pod"}
	queries := buildDynatraceWorkloadQueries(meta, []string{"cpu_usage"})
	q := queries["cpu_usage"]
	// Pod kind must use exact equality, not startsWith
	assert.Contains(t, q, `pod == "my-pod-abc"`)
	assert.NotContains(t, q, "startsWith")
}

func TestBuildDynatraceWorkloadPodFilter_Pod(t *testing.T) {
	f := buildDynatraceWorkloadPodFilter("my-pod", "pod")
	assert.Equal(t, `pod == "my-pod"`, f)
}

func TestBuildDynatraceWorkloadPodFilter_Deployment(t *testing.T) {
	f := buildDynatraceWorkloadPodFilter("my-app", "Deployment")
	assert.Equal(t, `startsWith(pod, "my-app-")`, f)
}
