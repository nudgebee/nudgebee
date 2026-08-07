package observability

import "fmt"

// Prometheus utilisation query builders. These render abstract metric keys into
// PromQL for node- and workload-scoped utilisation, and are dispatched from
// FetchMetricUtilisation (see service.go) for the prometheus, chronosphere and
// victoria_metrics providers. Kept in their own file mirroring the per-provider
// layout (datadog_queries.go, newrelic_queries.go, solarwinds_queries.go).

// --- PROMETHEUS HELPERS ---

func buildPrometheusNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	// Escape the node identity fields before they are interpolated into PromQL,
	// mirroring the safeMeta sanitisation in buildPrometheusWorkloadQueries. These
	// originate from request input, so an unescaped quote could otherwise break out
	// of the query string. meta is a value copy, so reassigning it is local.
	meta.InternalIP = escapePromQLString(meta.InternalIP)
	meta.NodeName = escapePromQLString(meta.NodeName)
	meta.NodeIP = escapePromQLString(meta.NodeIP)

	for _, metricKey := range metrics {
		switch metricKey {
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_cpu_seconds_total{mode!="idle", instance=~"%s.*"}[5m])) OR sum(irate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"%s.*"}[5m]))`, meta.InternalIP, meta.NodeName)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf(`sum(node_memory_Active_bytes{instance=~"%s.*"}) or sum(node_resources_memory_total_bytes{instance=~"%s.*"} - node_resources_memory_available_bytes{instance=~"%s.*"})`, meta.InternalIP, meta.NodeName, meta.NodeName)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{resource="cpu", node=~"%s.*"})`, meta.NodeName)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{resource="memory", node=~"%s.*"})`, meta.NodeName)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="cpu", node=~"%s.*"})`, meta.NodeName)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{resource="memory", node=~"%s.*"})`, meta.NodeName)
		case "disk_total":
			queries[metricKey] = fmt.Sprintf(`sum(node_filesystem_size_bytes{mountpoint="/", instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"})`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case "disk_used":
			queries[metricKey] = fmt.Sprintf(`(sum(node_filesystem_size_bytes{mountpoint="/", instance=~"%s.*"}) - sum(node_filesystem_free_bytes{mountpoint="/", instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{instance=~"%s.*"}))`, meta.InternalIP, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeIP, meta.NodeIP)
		case "cpu_usage_line":
			// node-agent labels node_resources_cpu_usage_seconds_total with `instance` (= node name),
			// not `node`; the last fallback must match on instance or it returns empty when
			// node-exporter (node_cpu_seconds_total) is absent and CPU renders as 0%.
			queries[metricKey] = fmt.Sprintf(`sum by (instance) (rate(node_cpu_seconds_total{mode!="idle", instance=~"%s|%s"}[5m])) or (sum by (node) (rate(node_cpu_seconds_total{mode!="idle", node=~"%s"}[5m]))) or (sum by (instance) (rate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"%s"}[5m])))`, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName)
		case "memory_usage_line":
			queries[metricKey] = fmt.Sprintf(`(avg(node_memory_MemTotal_bytes{instance=~"%s|%s"} - node_memory_MemAvailable_bytes{instance=~"%s|%s"}) by (instance)) or (avg(node_resources_memory_total_bytes{instance=~"%s"} - node_resources_memory_available_bytes{instance=~"%s"}) by (instance)) or (avg(node_memory_MemTotal_bytes{node=~"%s"} - node_memory_MemAvailable_bytes{node=~"%s"}) by (node)) or (avg(node_resources_memory_total_bytes{node=~"%s"} - node_resources_memory_available_bytes{node=~"%s"}) by (node))`, meta.InternalIP, meta.NodeName, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName)
		case "pvc_usage":
			queries[metricKey] = fmt.Sprintf(`((1 - node_filesystem_free_bytes{ __CLUSTER__ instance=~"%s.*", fstype !~"tmpfs"} / node_filesystem_size_bytes{ __CLUSTER__ instance=~"%s.*", fstype !~"tmpfs"}) * 100) or (kubelet_volume_stats_used_bytes{ __CLUSTER__ instance=~"%s.*"}  * 100/ kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"})`, meta.NodeIP, meta.NodeIP, meta.NodeName, meta.NodeName)
		case "node_az":
			queries[metricKey] = `count(karpenter_nodes_total_pod_requests{ __CLUSTER__ provisioner_name="",resource_type="pods"}) by (zone)`
		case "pod_az":
			queries[metricKey] = `sum(karpenter_pods_state{ __CLUSTER__ provisioner=""}) by (zone)`
		case "no_of_pods":
			queries[metricKey] = `sum(karpenter_pods_state{ __CLUSTER__ provisioner="", name=~".*-[0-9]+.*"})`
		case "node_pool_pod_trend":
			queries[metricKey] = `sum by (nodepool)(karpenter_pods_state{__CLUSTER__})`
		case "nodeclaims_disrupted":
			queries[metricKey] = `round(sum(increase(karpenter_nodeclaims_disrupted_total{__CLUSTER__}[1h])) by (nodepool, capacity_type, reason))`
		case "node_created_node_pool":
			queries[metricKey] = `round(sum(increase(karpenter_nodes_created_total{__CLUSTER__}[1h])) by (nodepool))`
		case "nodes_terminated_node_pool":
			queries[metricKey] = `round(sum(increase(karpenter_nodes_terminated_total{__CLUSTER__}[1h])) by (nodepool))`
		case "node_disruption_decisions_reason_decision":
			queries[metricKey] = `round(sum(increase(karpenter_voluntary_disruption_decisions_total{__CLUSTER__}[1h])) by (decision, reason))`
		case "nodes_eligible_disruption_reason":
			queries[metricKey] = `round(sum(increase(karpenter_voluntary_disruption_eligible_nodes{__CLUSTER__}[1h])) by (reason))`
		case "network_receive_packet":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case "network_transmit_packets":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		// Disk I/O throughput, from node-exporter. The device filter drops the
		// virtual block devices (loop/ram/dm) that would otherwise double-count the
		// physical disk they sit on. Same instance fallback chain as the network
		// metrics above: internal IP, then node name, then node IP.
		case "disk_read_bytes":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case "disk_write_bytes":
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		}
	}
	return queries
}

// promAggWindow derives the subquery range, step and inner-rate windows (as
// Prometheus/MetricsQL duration literals) for cluster utilisation aggregations from
// the picker's start/end (unix millis). The window follows the picker so the usage,
// P50/P90/Max and the usage-trend sparkline reflect the selected range instead of a
// hardcoded 24h. Falls back to 24h when the range is missing or invalid.
func promAggWindow(startMs, endMs int64) (rangeStr, stepStr, rateStr string) {
	rangeSec := (endMs - startMs) / 1000
	if rangeSec <= 0 {
		rangeSec = 24 * 3600
	}
	// ~300 sample points across the range, clamped so short ranges keep a 1m
	// resolution and long ranges don't explode the subquery point count.
	stepSec := rangeSec / 300
	if stepSec < 60 {
		stepSec = 60
	}
	if stepSec > 1800 {
		stepSec = 1800
	}
	// Inner rate window: at least 5m (a few scrape intervals) and never finer than
	// the step, so each sampled point covers its whole interval.
	rateSec := stepSec
	if rateSec < 300 {
		rateSec = 300
	}
	return fmt.Sprintf("%ds", rangeSec), fmt.Sprintf("%ds", stepSec), fmt.Sprintf("%ds", rateSec)
}

func buildPrometheusWorkloadQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	safeMeta := meta
	safeMeta.InternalIP = escapePromQLString(meta.InternalIP)
	safeMeta.Namespace = escapePromQLString(meta.Namespace)
	safeMeta.Name = escapePromQLString(meta.Name)
	safeMeta.ContainerName = escapePromQLString(meta.ContainerName)
	safeMeta.PVCName = escapePromQLString(meta.PVCName)

	// --- 1. Construct Filters ---
	var basePodFilter, containerFilter, containerIDFilter string

	if safeMeta.Namespace != "" {
		// Define the Pod Matcher based on Kind
		var podMatcher string
		if safeMeta.Name != "" {
			if safeMeta.Kind == "pod" {
				podMatcher = fmt.Sprintf(`pod="%s"`, safeMeta.Name)
			} else {
				// Regex match for deployments/statefulsets
				podMatcher = fmt.Sprintf(`pod=~"%s-.*"`, safeMeta.Name)
			}
			basePodFilter = fmt.Sprintf(` namespace="%s", %s`, safeMeta.Namespace, podMatcher)

			// Handle Container Filter Logic
			if safeMeta.ContainerName != "" {
				containerFilter = fmt.Sprintf(`%s, container="%s"`, basePodFilter, safeMeta.ContainerName)
				containerIDFilter = fmt.Sprintf(` container_id=~"/k8s/%s/%s/.*"`, safeMeta.Namespace, safeMeta.Name)
			} else {
				// --- FIX: This ELSE block was missing ---
				// If no container_name is provided, we still need a filter (usually excluding empty containers)
				containerFilter = fmt.Sprintf(`%s, container!=""`, basePodFilter)
				containerIDFilter = fmt.Sprintf(` container_id=~"/k8s/%s/%s/.*"`, safeMeta.Namespace, safeMeta.Name)
			}

		} else {
			// Namespace only case
			basePodFilter = fmt.Sprintf(` namespace="%s"`, safeMeta.Namespace)
			if safeMeta.ContainerName != "" {
				containerFilter = fmt.Sprintf(`%s, container="%s"`, basePodFilter, safeMeta.ContainerName)
			} else {
				containerFilter = basePodFilter // Direct assignment since basePodFilter is string
			}
			containerIDFilter = fmt.Sprintf(` container_id=~"/k8s/%s/.*"`, safeMeta.Namespace)
		}
	}

	// --- 2. Destination Filters ---
	var destFilter, actualDestFilter string
	if safeMeta.Namespace != "" && safeMeta.Name != "" {
		if safeMeta.Regex {
			destFilter = fmt.Sprintf(` destination_workload_namespace=~"%s", destination_workload_name=~"%s"`, safeMeta.Namespace, safeMeta.Name)
		} else {
			destFilter = fmt.Sprintf(` destination_workload_namespace="%s", destination_workload_name="%s"`, safeMeta.Namespace, safeMeta.Name)
		}
		actualDestFilter = fmt.Sprintf(` actual_destination_workload_namespace="%s", actual_destination_workload_name=~"%s.*"`, safeMeta.Namespace, safeMeta.Name)
	} else if safeMeta.Namespace != "" {
		if safeMeta.Regex {
			destFilter = fmt.Sprintf(` destination_workload_namespace=~"%s"`, safeMeta.Namespace)
		} else {
			destFilter = fmt.Sprintf(` destination_workload_namespace="%s"`, safeMeta.Namespace)
		}
		actualDestFilter = fmt.Sprintf(` actual_destination_workload_namespace="%s"`, safeMeta.Namespace)
	}

	// --- 3. PVC Filters ---
	var pvcFilter string
	if safeMeta.Namespace != "" {
		if safeMeta.PVCName != "" {
			pvcFilter = fmt.Sprintf(` namespace="%s", persistentvolumeclaim="%s"`, safeMeta.Namespace, safeMeta.PVCName)
		} else if safeMeta.Name != "" {
			pvcFilter = fmt.Sprintf(` namespace="%s", persistentvolumeclaim=~"%s.*"`, safeMeta.Namespace, safeMeta.Name)
		} else {
			pvcFilter = fmt.Sprintf(` namespace="%s"`, safeMeta.Namespace)
		}
	}

	// --- 4. Append Trailing Commas ---
	if basePodFilter != "" {
		basePodFilter += ","
	}
	if containerFilter != "" {
		containerFilter += ","
	}
	if containerIDFilter != "" {
		containerIDFilter += ","
	}
	if destFilter != "" {
		destFilter += ","
	}
	if actualDestFilter != "" {
		actualDestFilter += ","
	}
	if pvcFilter != "" {
		pvcFilter += ","
	}

	// Cluster-level aggregation windows, derived from the picker range (empty for
	// unit-tested metadata -> fall back to a 24h window / 5m resolution).
	rangeW := safeMeta.RangeWindow
	if rangeW == "" {
		rangeW = "24h"
	}
	stepW := safeMeta.Step
	if stepW == "" {
		stepW = "5m"
	}
	rateW := safeMeta.RateWindow
	if rateW == "" {
		rateW = "5m"
	}

	// --- 5. Build Queries ---
	for _, metricKey := range metrics {
		switch metricKey {
		// --- PVC Metrics ---
		case "pvc_usage":
			queries[metricKey] = fmt.Sprintf(`sum(kubelet_volume_stats_used_bytes{__CLUSTER__ %s})`, pvcFilter)
		case "pvc_requests":
			queries[metricKey] = fmt.Sprintf(`sum(kube_persistentvolumeclaim_resource_requests_storage_bytes{__CLUSTER__ %s})`, pvcFilter)

		// --- HTTP / Network ---
		case "http_status":
			queries[metricKey] = fmt.Sprintf(`sum by (actual_destination_workload_namespace, status) (rate(container_http_requests_total{__CLUSTER__ %sjob!=""}[5m]))`, actualDestFilter)
		case "http_max_response_time":
			queries[metricKey] = fmt.Sprintf(`max by (actual_destination_workload_namespace) (max_over_time(container_net_tcp_connection_time_seconds_total{__CLUSTER__ %sjob!=""}[5m]))`, actualDestFilter)
		case "http_throughput":
			queries[metricKey] = fmt.Sprintf(`sort_desc(sum by(method, path, destination_workload_name, destination_workload_namespace)(increase(container_http_requests_total{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case "http_latency_p95":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.95, sum by(le, path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case "http_latency_p99":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum by(le, path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case "http_latency_sum":
			queries[metricKey] = fmt.Sprintf(`sum by (path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_sum{__CLUSTER__ %sjob!=""}[1h]))`, destFilter)
		case "http_error_rate":
			queries[metricKey] = fmt.Sprintf(`(sum by(method, path, destination_workload_name, destination_workload_namespace)(increase(container_http_requests_total{__CLUSTER__ %sstatus=~"^[45]..$"}[1h])) / sum(increase(container_http_requests_total{__CLUSTER__ %sjob!=""}[1h]))) * 100`, destFilter, destFilter)

		// --- Network Packet Logic ---
		// Network metrics are pod-scoped: cAdvisor's container_network_*_bytes_total series carry an
		// empty `container` label, so the container!="" constraint in containerFilter filters them all
		// out. Use basePodFilter (namespace + pod matcher only) instead.
		case "network_receive_packet":
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_network_receive_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_received_total{__CLUSTER__ %sjob!=""}[5m])))`, basePodFilter, containerIDFilter)
		case "network_transmit_packets":
			queries[metricKey] = fmt.Sprintf(`-((sum(rate(container_network_transmit_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_sent_total{__CLUSTER__ %sjob!=""}[5m]))))`, basePodFilter, containerIDFilter)
		case "network_usage":
			queries[metricKey] = fmt.Sprintf(`sum(container_net_tcp_connection_time_seconds_total{__CLUSTER__ %scontainer!=""}) or sum(kube_network_rx_bytes{__CLUSTER__ %scontainer!=""})`, containerFilter, basePodFilter)

		// --- Disk I/O ---
		// cAdvisor filesystem counters. Preferred form is containerFilter (container!="")
		// so the per-container series are summed without also adding the pod-level
		// rollup. Some runtimes only publish the rollup, which carries an empty
		// `container` label and is therefore excluded by that filter -- the basePodFilter
		// fallback catches those. `or` only evaluates the right side when the left
		// returns no series, so the two forms can never be summed together.
		case "disk_read_bytes":
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_fs_reads_bytes_total{__CLUSTER__ %s}[5m]))) or (sum(rate(container_fs_reads_bytes_total{__CLUSTER__ %sjob!=""}[5m])))`, containerFilter, basePodFilter)
		case "disk_write_bytes":
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_fs_writes_bytes_total{__CLUSTER__ %s}[5m]))) or (sum(rate(container_fs_writes_bytes_total{__CLUSTER__ %sjob!=""}[5m])))`, containerFilter, basePodFilter)

		// --- Resources ---
		case "cpu_usage":
			queries[metricKey] = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{__CLUSTER__ %s}[5m]))`, containerFilter)
		case "cpu_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{__CLUSTER__ %sresource="cpu"})`, containerFilter)
		case "cpu_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{__CLUSTER__ %sresource="cpu"})`, containerFilter)
		case "memory_usage":
			queries[metricKey] = fmt.Sprintf(`sum(container_memory_working_set_bytes{__CLUSTER__ %s})`, containerFilter)
		case "memory_request":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_requests{__CLUSTER__ %sresource="memory"})`, containerFilter)
		case "memory_limit":
			queries[metricKey] = fmt.Sprintf(`sum(kube_pod_container_resource_limits{__CLUSTER__ %sresource="memory"})`, containerFilter)
		case "disk_total":
			queries[metricKey] = fmt.Sprintf(`sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"})`, safeMeta.InternalIP, safeMeta.NodeName, safeMeta.NodeIP)
		case "disk_used":
			queries[metricKey] = fmt.Sprintf(`(sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"}) - sum(node_filesystem_free_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"%s.*"}))`, safeMeta.InternalIP, safeMeta.InternalIP, safeMeta.NodeName, safeMeta.NodeName, safeMeta.NodeIP, safeMeta.NodeIP)

		// --- Node/Cluster Aggregations ---
		// Usage / percentiles / peak are windowed by the picker range (rangeW) instead of a
		// hardcoded 24h, so the time filter actually adjusts the numbers. The percentile/peak
		// queries sum across the per-(node,core,mode) series FIRST, then aggregate over time.
		// Summing each series' own time-percentile instead (the old form) added peaks that occur
		// at different instants and produced values above physical capacity (P50/P90/Max >100%).
		// Valid in both PromQL (prod) and VictoriaMetrics MetricsQL (dev).
		case "cpu_real":
			queries[metricKey] = fmt.Sprintf(`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))`, rangeW, rangeW)
		case "cpu_total":
			queries[metricKey] = `sum(machine_cpu_cores{__CLUSTER__}) or sum(node_resources_cpu_logical_cores{__CLUSTER__})`
		case "mem_real":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "mem_total":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__})`
		// cpu_usage_trend / mem_usage_trend feed the utilisation sparkline: fetched as a RANGE
		// query so the relay evaluates them at each step across the picker window. CPU uses a
		// short rate window (rateW) so spikes register instead of being averaged away.
		case "cpu_usage_trend":
			queries[metricKey] = fmt.Sprintf(`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))`, rateW, rateW)
		case "mem_usage_trend":
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p90_mem":
			queries[metricKey] = `quantile(0.9, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.9, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p90_cpu":
			queries[metricKey] = fmt.Sprintf(`quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case "p50_mem":
			queries[metricKey] = `quantile(0.5, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.5, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case "p50_cpu":
			queries[metricKey] = fmt.Sprintf(`quantile_over_time(0.50, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or quantile_over_time(0.50, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case "max_usage_mem":
			queries[metricKey] = fmt.Sprintf(`max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[%s:%s])`, rangeW, stepW)
		case "max_usage_cpu":
			queries[metricKey] = fmt.Sprintf(`max_over_time(sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or max_over_time(sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case "replica_defined":
			queries[metricKey] = fmt.Sprintf(`sum(kube_replicaset_spec_replicas{ __CLUSTER__ namespace="%s", replicaset=~"%s.*"})`, safeMeta.Namespace, safeMeta.Name)
		case "replica_ready":
			queries[metricKey] = fmt.Sprintf(`sum(kube_replicaset_status_ready_replicas{ __CLUSTER__ namespace="%s", replicaset=~"%s.*"})`, safeMeta.Namespace, safeMeta.Name)

		// others
		case "container_application_type_with_pod":
			queries[metricKey] = fmt.Sprintf(`container_application_type{ __CLUSTER__ container_id=~"/k8s/%s/%s.*"}`, safeMeta.Namespace, safeMeta.Name)
		case "container_application_type_with_workload":
			queries[metricKey] = fmt.Sprintf(`container_application_type{ __CLUSTER__ container_id=~"/k8s/%s/%s-.*"}`, safeMeta.Namespace, safeMeta.Name)
		case "jvm_memory_metric_count":
			queries[metricKey] = fmt.Sprintf(`count by (namespace, pod) ({ __CLUSTER__ __name__=~"process.runtime.jvm.memory.usage|process_runtime_jvm_memory_usage_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case "cpython_memory_metric_count":
			queries[metricKey] = fmt.Sprintf(`count by (pod, namespace) ({ __CLUSTER__ __name__=~"process.runtime.cpython.memory|process_runtime_cpython_memory_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case "go_heap_memory_metric_count":
			queries[metricKey] = fmt.Sprintf(`count by (pod, namespace) ({ __CLUSTER__ __name__=~"process.runtime.go.mem.heap_sys|process_runtime_go_mem_heap_sys_bytes|go.memory.used|go_memory_used_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case "service_info_by_cluster_ip":
			queries[metricKey] = fmt.Sprintf(`kube_service_info{ __CLUSTER__ cluster_ip="%s"}`, safeMeta.InternalIP)
		case "sensitive_log_messages":
			queries[metricKey] = "sum(increase(container_sensitive_log_messages_total{__CLUSTER__}[5m])) by (pattern, container_id, regex, name, pattern_hash)"
		case "container_error_log_count_with_pod":
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_log_messages_total{ __CLUSTER__ container_id=~"%s", level=~"critical|error|exception"}[5m])) by (container_id)`, safeMeta.Name)
		case "container_error_log_count_with_workload":
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_log_messages_total{ __CLUSTER__ container_id=~"%s", level=~"critical|error"}[5m])) by (container_id)`, safeMeta.Name)
		case "workload_http_error_rate":
			queries[metricKey] = fmt.Sprintf(`sum by(destination_workload_name, destination_workload_namespace)(rate(container_http_requests_total{ __CLUSTER__ status=~"5..|4..", destination_workload_name=~"%s", destination_workload_namespace=~"%s"}[1h])) / sum by(destination_workload_name, destination_workload_namespace)(rate(container_http_requests_total{ __CLUSTER__ destination_workload_name=~"%s", destination_workload_namespace=~"%s"}[1h]))`, safeMeta.Name, safeMeta.Namespace, safeMeta.Name, safeMeta.Namespace)
		case "container_http_latency_p90":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.90, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_p99":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_p95":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.95, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_p50":
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.50, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, safeMeta.ContainerName)
		case "container_http_latency_mean":
			queries[metricKey] = fmt.Sprintf(`sum(rate(container_http_requests_duration_seconds_total_sum{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) / sum(rate(container_http_requests_duration_seconds_total_count{ __CLUSTER__ container_id=~"%s", destination_workload_namespace!="external", destination_workload_namespace!=""}[1h]))`, safeMeta.ContainerName, safeMeta.ContainerName)
		case "container_http_request_count":
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_http_requests_total{ __CLUSTER__ container_id=~"%s"}[1h]))`, safeMeta.ContainerName)
		case "container_http_error_status_count":
			queries[metricKey] = fmt.Sprintf(`sum by(status) (increase(container_http_requests_total{ __CLUSTER__ status=~"4..|5..",container_id=~"%s"}[1h]))`, safeMeta.ContainerName)
		case "container_top_destination_services":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (rate(container_http_requests_total{ __CLUSTER__ container_id=~"%s"}[1h])))`, safeMeta.ContainerName)
		case "cpu_usage_pod":
			queries[metricKey] = fmt.Sprintf(`sum(irate(container_cpu_usage_seconds_total{namespace="%s", pod=~"%s"}[1m]))`, safeMeta.Namespace, safeMeta.Name)
		case "cpu_request_pod":
			queries[metricKey] = fmt.Sprintf(`kube_pod_container_resource_requests{resource = "cpu", namespace="%s", pod=~"%s"}`, safeMeta.Namespace, safeMeta.Name)
		case "cpu_limit_pod":
			queries[metricKey] = fmt.Sprintf(`kube_pod_container_resource_limits{resource = "cpu", namespace="%s", pod=~"%s"}`, safeMeta.Namespace, safeMeta.Name)
		case "container_top_http_requests":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (rate(container_http_requests_total{ __CLUSTER__ container_id=~"%s"}[1h])))`, safeMeta.ContainerName)
		case "container_top_cpu_usage":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (pod, namespace) (rate(container_cpu_usage_seconds_total{ __CLUSTER__ pod=~"%s", namespace=~"%s"}[1h])))`, safeMeta.Name, safeMeta.Namespace)
		case "container_top_memory_usage":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (pod, namespace) (rate(container_memory_working_set_bytes{ __CLUSTER__ pod=~"%s", namespace=~"%s"}[1h])))`, safeMeta.Name, safeMeta.Namespace)
		case "container_top_http_error_calls":
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (increase(container_http_requests_total{ __CLUSTER__ status=~"4..|5..",container_id=~"%s"}[1h])))`, safeMeta.ContainerName)
		}
	}
	return queries
}
