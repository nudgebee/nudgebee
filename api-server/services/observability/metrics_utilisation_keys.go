package observability

// The utilisation key vocabulary. Every provider's key switches and key tables are typed on
// it, so a misspelled key constant does not compile. A raw string literal still compiles
// (Go converts it to UtilisationKey); TestUtilisationKeys_* catch those, except in a new
// switch whose cases are all undeclared literals or a new map[string] key table. Callers
// send keys as strings over JSON, so isKnownUtilisationKey is the check at that boundary.

// UtilisationKey names one quantity a utilisation caller can ask for. The key also fixes
// the shape of the answer — one summed series, or one series per pod — so a key never
// changes shape with the request.
type UtilisationKey string

const (
	// Workload and node usage, requests and limits, summed into one series.
	KeyCPUUsage          UtilisationKey = "cpu_usage"
	KeyCPURequest        UtilisationKey = "cpu_request"
	KeyCPULimit          UtilisationKey = "cpu_limit"
	KeyMemoryUsage       UtilisationKey = "memory_usage"
	KeyMemoryRequest     UtilisationKey = "memory_request"
	KeyMemoryLimit       UtilisationKey = "memory_limit"
	KeyCPUUsageLine      UtilisationKey = "cpu_usage_line"
	KeyMemoryUsageLine   UtilisationKey = "memory_usage_line"
	KeyCPUUsageTrend     UtilisationKey = "cpu_usage_trend"
	KeyMemUsageTrend     UtilisationKey = "mem_usage_trend"
	KeyCPUThrottling     UtilisationKey = "cpu_throttling"
	KeyCPUAllocatable    UtilisationKey = "cpu_allocatable"
	KeyMemoryAllocatable UtilisationKey = "memory_allocatable"

	// Totals and percentiles that collapse a series to a headline number.
	KeyCPUReal     UtilisationKey = "cpu_real"
	KeyCPUTotal    UtilisationKey = "cpu_total"
	KeyMemReal     UtilisationKey = "mem_real"
	KeyMemTotal    UtilisationKey = "mem_total"
	KeyP50CPU      UtilisationKey = "p50_cpu"
	KeyP50Mem      UtilisationKey = "p50_mem"
	KeyP90CPU      UtilisationKey = "p90_cpu"
	KeyP90Mem      UtilisationKey = "p90_mem"
	KeyMaxUsageCPU UtilisationKey = "max_usage_cpu"
	KeyMaxUsageMem UtilisationKey = "max_usage_mem"

	// One series per pod, for charts that draw a line per pod.
	KeyCPUUsagePod       UtilisationKey = "cpu_usage_pod"
	KeyCPURequestPod     UtilisationKey = "cpu_request_pod"
	KeyCPULimitPod       UtilisationKey = "cpu_limit_pod"
	KeyCPUUsageByPod     UtilisationKey = "cpu_usage_by_pod"
	KeyMemoryUsageByPod  UtilisationKey = "memory_usage_by_pod"
	KeyCPUUsageMaxPod    UtilisationKey = "cpu_usage_max_pod"
	KeyMemoryUsageMaxPod UtilisationKey = "memory_usage_max_pod"
	KeyPodCount          UtilisationKey = "pod_count"

	// One series per pod and container, the rightsizing inputs.
	KeyContainerCPUUsageByPod         UtilisationKey = "container_cpu_usage_by_pod"
	KeyContainerMemoryWorkingSetByPod UtilisationKey = "container_memory_working_set_by_pod"
	KeyContainerMemoryMaxUsageByPod   UtilisationKey = "container_memory_max_usage_by_pod"
	KeyContainerCPURequestByPod       UtilisationKey = "container_cpu_request_by_pod"
	KeyContainerCPULimitByPod         UtilisationKey = "container_cpu_limit_by_pod"
	KeyContainerMemoryRequestByPod    UtilisationKey = "container_memory_request_by_pod"
	KeyContainerMemoryLimitByPod      UtilisationKey = "container_memory_limit_by_pod"
	KeyContainerOOMKilledLimitByPod   UtilisationKey = "container_oomkilled_limit_by_pod"

	// Replica counts for the workload's controller.
	KeyReplicaDefined UtilisationKey = "replica_defined"
	KeyReplicaReady   UtilisationKey = "replica_ready"

	// Storage: volumes, claims and node disks.
	KeyPVCUsage           UtilisationKey = "pvc_usage"
	KeyPVCRequests        UtilisationKey = "pvc_requests"
	KeyPVCUsedByClaim     UtilisationKey = "pvc_used_by_claim"
	KeyPVCCapacityByClaim UtilisationKey = "pvc_capacity_by_claim"
	KeyDiskTotal          UtilisationKey = "disk_total"
	KeyDiskUsed           UtilisationKey = "disk_used"
	KeyDiskReadBytes      UtilisationKey = "disk_read_bytes"
	KeyDiskWriteBytes     UtilisationKey = "disk_write_bytes"

	// Network.
	KeyNetworkReceivePacket         UtilisationKey = "network_receive_packet"
	KeyNetworkTransmitPackets       UtilisationKey = "network_transmit_packets"
	KeyNetworkUsage                 UtilisationKey = "network_usage"
	KeyNetworkReceiveBytesIncrease  UtilisationKey = "network_receive_bytes_increase"
	KeyNetworkTransmitBytesIncrease UtilisationKey = "network_transmit_bytes_increase"

	// HTTP traffic for a workload.
	KeyHTTPStatus               UtilisationKey = "http_status"
	KeyHTTPThroughput           UtilisationKey = "http_throughput"
	KeyHTTPLatencyP95           UtilisationKey = "http_latency_p95"
	KeyHTTPLatencyP99           UtilisationKey = "http_latency_p99"
	KeyHTTPLatencySum           UtilisationKey = "http_latency_sum"
	KeyHTTPMaxResponseTime      UtilisationKey = "http_max_response_time"
	KeyHTTPErrorRate            UtilisationKey = "http_error_rate"
	KeyWorkloadHTTPRequestRate  UtilisationKey = "workload_http_request_rate"
	KeyWorkloadHTTPRequestCount UtilisationKey = "workload_http_request_count"
	KeyWorkloadHTTPErrorCount   UtilisationKey = "workload_http_error_count"
	KeyWorkloadHTTPErrorRate    UtilisationKey = "workload_http_error_rate"
	KeyWorkloadHTTPLatencyMean  UtilisationKey = "workload_http_latency_mean"
	KeyWorkloadHTTPLatencyP99   UtilisationKey = "workload_http_latency_p99"

	// Read from the node agent's own counters (eBPF, runtime and log detection). No
	// third-party provider carries them, so they answer on the agent's Prometheus only.
	KeyContainerHTTPLatencyP50              UtilisationKey = "container_http_latency_p50"
	KeyContainerHTTPLatencyP90              UtilisationKey = "container_http_latency_p90"
	KeyContainerHTTPLatencyP95              UtilisationKey = "container_http_latency_p95"
	KeyContainerHTTPLatencyP99              UtilisationKey = "container_http_latency_p99"
	KeyContainerHTTPLatencyMean             UtilisationKey = "container_http_latency_mean"
	KeyContainerHTTPRequestCount            UtilisationKey = "container_http_request_count"
	KeyContainerHTTPErrorStatusCount        UtilisationKey = "container_http_error_status_count"
	KeyContainerTopCPUUsage                 UtilisationKey = "container_top_cpu_usage"
	KeyContainerTopMemoryUsage              UtilisationKey = "container_top_memory_usage"
	KeyContainerTopHTTPRequests             UtilisationKey = "container_top_http_requests"
	KeyContainerTopHTTPErrorCalls           UtilisationKey = "container_top_http_error_calls"
	KeyContainerTopDestinationServices      UtilisationKey = "container_top_destination_services"
	KeyContainerApplicationTypeWithPod      UtilisationKey = "container_application_type_with_pod"
	KeyContainerApplicationTypeWithWorkload UtilisationKey = "container_application_type_with_workload"
	KeyContainerErrorLogCountWithPod        UtilisationKey = "container_error_log_count_with_pod"
	KeyContainerErrorLogCountWithWorkload   UtilisationKey = "container_error_log_count_with_workload"
	KeyJVMMemoryMetricCount                 UtilisationKey = "jvm_memory_metric_count"
	KeyCPythonMemoryMetricCount             UtilisationKey = "cpython_memory_metric_count"
	KeyGoHeapMemoryMetricCount              UtilisationKey = "go_heap_memory_metric_count"
	KeySensitiveLogMessages                 UtilisationKey = "sensitive_log_messages"
	KeyServiceInfoByClusterIP               UtilisationKey = "service_info_by_cluster_ip"

	// Nodes, node pools and Karpenter.
	KeyActiveNodes                           UtilisationKey = "active_nodes"
	KeyNoOfPods                              UtilisationKey = "no_of_pods"
	KeyNodeAZ                                UtilisationKey = "node_az"
	KeyPodAZ                                 UtilisationKey = "pod_az"
	KeyNodePoolPodTrend                      UtilisationKey = "node_pool_pod_trend"
	KeyNodeclaimsDisrupted                   UtilisationKey = "nodeclaims_disrupted"
	KeyNodeCreatedNodePool                   UtilisationKey = "node_created_node_pool"
	KeyNodesTerminatedNodePool               UtilisationKey = "nodes_terminated_node_pool"
	KeyNodeDisruptionDecisionsReasonDecision UtilisationKey = "node_disruption_decisions_reason_decision"
	KeyNodesEligibleDisruptionReason         UtilisationKey = "nodes_eligible_disruption_reason"
)

// utilisationKeys is every declared key.
var utilisationKeys = func() map[UtilisationKey]bool {
	keys := []UtilisationKey{
		KeyCPUUsage,
		KeyCPURequest,
		KeyCPULimit,
		KeyMemoryUsage,
		KeyMemoryRequest,
		KeyMemoryLimit,
		KeyCPUUsageLine,
		KeyMemoryUsageLine,
		KeyCPUUsageTrend,
		KeyMemUsageTrend,
		KeyCPUThrottling,
		KeyCPUAllocatable,
		KeyMemoryAllocatable,
		KeyCPUReal,
		KeyCPUTotal,
		KeyMemReal,
		KeyMemTotal,
		KeyP50CPU,
		KeyP50Mem,
		KeyP90CPU,
		KeyP90Mem,
		KeyMaxUsageCPU,
		KeyMaxUsageMem,
		KeyCPUUsagePod,
		KeyCPURequestPod,
		KeyCPULimitPod,
		KeyCPUUsageByPod,
		KeyMemoryUsageByPod,
		KeyCPUUsageMaxPod,
		KeyMemoryUsageMaxPod,
		KeyPodCount,
		KeyContainerCPUUsageByPod,
		KeyContainerMemoryWorkingSetByPod,
		KeyContainerMemoryMaxUsageByPod,
		KeyContainerCPURequestByPod,
		KeyContainerCPULimitByPod,
		KeyContainerMemoryRequestByPod,
		KeyContainerMemoryLimitByPod,
		KeyContainerOOMKilledLimitByPod,
		KeyReplicaDefined,
		KeyReplicaReady,
		KeyPVCUsage,
		KeyPVCRequests,
		KeyPVCUsedByClaim,
		KeyPVCCapacityByClaim,
		KeyDiskTotal,
		KeyDiskUsed,
		KeyDiskReadBytes,
		KeyDiskWriteBytes,
		KeyNetworkReceivePacket,
		KeyNetworkTransmitPackets,
		KeyNetworkUsage,
		KeyNetworkReceiveBytesIncrease,
		KeyNetworkTransmitBytesIncrease,
		KeyHTTPStatus,
		KeyHTTPThroughput,
		KeyHTTPLatencyP95,
		KeyHTTPLatencyP99,
		KeyHTTPLatencySum,
		KeyHTTPMaxResponseTime,
		KeyHTTPErrorRate,
		KeyWorkloadHTTPRequestRate,
		KeyWorkloadHTTPRequestCount,
		KeyWorkloadHTTPErrorCount,
		KeyWorkloadHTTPErrorRate,
		KeyWorkloadHTTPLatencyMean,
		KeyWorkloadHTTPLatencyP99,
		KeyContainerHTTPLatencyP50,
		KeyContainerHTTPLatencyP90,
		KeyContainerHTTPLatencyP95,
		KeyContainerHTTPLatencyP99,
		KeyContainerHTTPLatencyMean,
		KeyContainerHTTPRequestCount,
		KeyContainerHTTPErrorStatusCount,
		KeyContainerTopCPUUsage,
		KeyContainerTopMemoryUsage,
		KeyContainerTopHTTPRequests,
		KeyContainerTopHTTPErrorCalls,
		KeyContainerTopDestinationServices,
		KeyContainerApplicationTypeWithPod,
		KeyContainerApplicationTypeWithWorkload,
		KeyContainerErrorLogCountWithPod,
		KeyContainerErrorLogCountWithWorkload,
		KeyJVMMemoryMetricCount,
		KeyCPythonMemoryMetricCount,
		KeyGoHeapMemoryMetricCount,
		KeySensitiveLogMessages,
		KeyServiceInfoByClusterIP,
		KeyActiveNodes,
		KeyNoOfPods,
		KeyNodeAZ,
		KeyPodAZ,
		KeyNodePoolPodTrend,
		KeyNodeclaimsDisrupted,
		KeyNodeCreatedNodePool,
		KeyNodesTerminatedNodePool,
		KeyNodeDisruptionDecisionsReasonDecision,
		KeyNodesEligibleDisruptionReason,
	}
	set := make(map[UtilisationKey]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}()

// isKnownUtilisationKey reports whether key is declared.
func isKnownUtilisationKey(key string) bool {
	return utilisationKeys[UtilisationKey(key)]
}

// unknownUtilisationKeys returns the requested keys that are not declared, in request
// order and without repeats.
func unknownUtilisationKeys(requested []string) []string {
	var unknown []string
	seen := make(map[string]bool, len(requested))
	for _, key := range requested {
		if key == "" || seen[key] || isKnownUtilisationKey(key) {
			continue
		}
		seen[key] = true
		unknown = append(unknown, key)
	}
	return unknown
}
