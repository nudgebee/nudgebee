package observability

import (
	"fmt"
	"regexp"
	"strings"
)

// Prometheus utilisation query builders. These render abstract metric keys into
// PromQL for node- and workload-scoped utilisation, and are dispatched from
// FetchMetricUtilisation (see service.go) for the prometheus, chronosphere and
// victoria_metrics providers. Kept in their own file mirroring the per-provider
// layout (datadog_queries.go, newrelic_queries.go, solarwinds_queries.go).

// --- PROMETHEUS HELPERS ---

// Node-exporter identifies a machine by address, not by Kubernetes name: its only
// identity labels are `instance` ("10.128.0.50:9100") and the DaemonSet pod it runs in.
// So a caller holding just a node name had nothing to filter on, and the interpolated
// `instance=~"%s.*"` collapsed to `instance=~".*"` — matching EVERY node. Measured on
// dev: 42.96 GB reported for a node with 12.06 GB allocatable, silently, because a
// regex that matches everything is not an error.
//
// Three forms, in order of preference:
//
//	IP known    `{instance=~"<ip>.*"}` — cheapest, and what the nodes page already gets
//	name only   joined to kube_pod_info through the node-exporter pod: `pod` and
//	            `namespace` match exactly on both sides (no port to strip, unlike
//	            `instance`) and kube_pod_info carries the node name. Verified on dev:
//	            both forms return 3.259 GB for the same node.
//	neither     a selector that matches NOTHING, so a caller with no scope sees a gap
//	            instead of the whole cluster.
//
// The join assumes node-exporter runs one pod per node, which is how a DaemonSet
// deploys it. A cluster scraping it some other way falls through to no series — still
// better than a wrong number, and the `node_resources_*` arm beside each caller covers
// clusters running our own node-agent instead.
func promNodeExporterScope(meta RequestMetadata) (selector, join string) {
	switch {
	case meta.InternalIP != "":
		return fmt.Sprintf(`instance=~"%s.*"`, meta.InternalIP), ""
	case meta.NodeName != "":
		return "", fmt.Sprintf(` * on(pod, namespace) group_left(node) kube_pod_info{node="%s"}`, meta.NodeName)
	}
	return `instance="` + promNoNodeScope + `"`, ""
}

// promNodeAgentScope is the same question for the node-agent series, which DO carry the
// node name in `instance`. Only the no-scope case needed fixing: it matched everything.
func promNodeAgentScope(meta RequestMetadata) string {
	if meta.NodeName != "" {
		return fmt.Sprintf(`instance=~"%s.*"`, meta.NodeName)
	}
	return `instance="` + promNoNodeScope + `"`
}

// promNoNodeScope is a name no instance can have, so the selector matches nothing.
// Deliberately visible in the query text: a reader seeing it in a log knows the caller
// supplied no node, rather than wondering why a node looks idle.
const promNoNodeScope = "__no_node_scope__"

// promPrefixComma joins a filter onto a selector that already has content.
func promPrefixComma(filter string) string {
	if filter == "" {
		return ""
	}
	return ", " + filter
}

func buildPrometheusNodeQueries(meta RequestMetadata, metrics []string) map[string]string {
	queries := make(map[string]string)

	// Escape the node identity fields before they are interpolated into PromQL,
	// mirroring the safeMeta sanitisation in buildPrometheusWorkloadQueries. These
	// originate from request input, so an unescaped quote could otherwise break out
	// of the query string. meta is a value copy, so reassigning it is local.
	meta.InternalIP = escapePromQLString(meta.InternalIP)
	meta.NodeName = escapePromQLString(meta.NodeName)
	meta.NodeIP = escapePromQLString(meta.NodeIP)

	// The node identity as a selector. A cluster can label cAdvisor series with
	// either `node` or `instance` depending on how it scrapes, so every per-container
	// key below asks both ways — see promPerContainerOnNode.
	node := meta.NodeName

	// Subquery window for an aggregated request, same defaults as the workload
	// builder so a caller that omits them still gets a parseable [range:step].
	rangeW := meta.RangeWindow
	if rangeW == "" {
		rangeW = "24h"
	}
	stepW := meta.Step
	if stepW == "" {
		stepW = "5m"
	}

	for _, metricKey := range metrics {
		switch UtilisationKey(metricKey) {
		// --- Per-container metrics, narrowed to one node ---
		//
		// Same key names and the same {namespace, pod, container} series shape as the
		// workload builder produces; only the narrowing differs. That is the point of
		// the family: a caller asks the same question and says where to look.
		case KeyContainerCPUUsageByPod:
			queries[metricKey] = promPerContainerOnNode(
				promRateSeries("container_cpu_usage_seconds_total", meta.RateWindow), node,
				`pod!=""`, `container!=""`, `container!="POD"`, `image!=""`)
		case KeyContainerMemoryWorkingSetByPod:
			queries[metricKey] = promPerContainerOnNode(
				promGaugeSeries("container_memory_working_set_bytes"), node,
				`pod!=""`, `container!=""`, `container!="POD"`, `image!=""`)
		case KeyContainerMemoryMaxUsageByPod:
			queries[metricKey] = promPerContainerOnNode(
				promGaugeSeries("container_memory_max_usage_bytes"), node,
				`pod!=""`, `container!=""`, `container!="POD"`, `image!=""`)

		// Spec keys come from kube-state-metrics, which labels by `node` only — no
		// `instance` variant to fall back to, so these do not need the two-armed form.
		case KeyContainerCPURequestByPod:
			queries[metricKey] = promSpecOnNode("kube_pod_container_resource_requests", node, "cpu")
		case KeyContainerCPULimitByPod:
			queries[metricKey] = promSpecOnNode("kube_pod_container_resource_limits", node, "cpu")
		case KeyContainerMemoryRequestByPod:
			queries[metricKey] = promSpecOnNode("kube_pod_container_resource_requests", node, "memory")
		case KeyContainerMemoryLimitByPod:
			queries[metricKey] = promSpecOnNode("kube_pod_container_resource_limits", node, "memory")

		// What the node can give out, as opposed to what it is using. The workload
		// builder answers these cluster-wide; at node scope they are the denominator
		// a caller needs to turn per-container usage into a percentage.
		case KeyCPUTotal:
			queries[metricKey] = promNodeAllocatable(node, "cpu")
		case KeyMemTotal:
			queries[metricKey] = promNodeAllocatable(node, "memory")

		case KeyCPUUsage:
			sel, join := promNodeExporterScope(meta)
			agent := promNodeAgentScope(meta)
			queries[metricKey] = fmt.Sprintf(
				`sum(irate(node_cpu_seconds_total{mode!="idle"%s}[5m])%s) OR sum(irate(node_resources_cpu_usage_seconds_total{mode!="idle", %s}[5m]))`,
				promPrefixComma(sel), join, agent)
		case KeyMemoryUsage:
			sel, join := promNodeExporterScope(meta)
			agent := promNodeAgentScope(meta)
			queries[metricKey] = fmt.Sprintf(
				`sum(node_memory_Active_bytes{%s}%s) or sum(node_resources_memory_total_bytes{%s} - node_resources_memory_available_bytes{%s})`,
				sel, join, agent, agent)
		case KeyCPURequest:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_requests", fmt.Sprintf(`resource="cpu", node=~"%s.*"`, meta.NodeName))
		case KeyMemoryRequest:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_requests", fmt.Sprintf(`resource="memory", node=~"%s.*"`, meta.NodeName))
		case KeyCPULimit:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_limits", fmt.Sprintf(`resource="cpu", node=~"%s.*"`, meta.NodeName))
		case KeyMemoryLimit:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_limits", fmt.Sprintf(`resource="memory", node=~"%s.*"`, meta.NodeName))
		case KeyDiskTotal:
			queries[metricKey] = fmt.Sprintf(`sum(node_filesystem_size_bytes{mountpoint="/", instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"})`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case KeyDiskUsed:
			queries[metricKey] = fmt.Sprintf(`(sum(node_filesystem_size_bytes{mountpoint="/", instance=~"%s.*"}) - sum(node_filesystem_free_bytes{mountpoint="/", instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{instance=~"%s.*"}))`, meta.InternalIP, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeIP, meta.NodeIP)
		case KeyCPUUsageLine:
			// node-agent labels node_resources_cpu_usage_seconds_total with `instance` (= node name),
			// not `node`; the last fallback must match on instance or it returns empty when
			// node-exporter (node_cpu_seconds_total) is absent and CPU renders as 0%.
			queries[metricKey] = fmt.Sprintf(`sum by (instance) (rate(node_cpu_seconds_total{mode!="idle", instance=~"%s|%s"}[5m])) or (sum by (node) (rate(node_cpu_seconds_total{mode!="idle", node=~"%s"}[5m]))) or (sum by (instance) (rate(node_resources_cpu_usage_seconds_total{mode!="idle", instance=~"%s"}[5m])))`, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName)
		case KeyMemoryUsageLine:
			queries[metricKey] = fmt.Sprintf(`(avg(node_memory_MemTotal_bytes{instance=~"%s|%s"} - node_memory_MemAvailable_bytes{instance=~"%s|%s"}) by (instance)) or (avg(node_resources_memory_total_bytes{instance=~"%s"} - node_resources_memory_available_bytes{instance=~"%s"}) by (instance)) or (avg(node_memory_MemTotal_bytes{node=~"%s"} - node_memory_MemAvailable_bytes{node=~"%s"}) by (node)) or (avg(node_resources_memory_total_bytes{node=~"%s"} - node_resources_memory_available_bytes{node=~"%s"}) by (node))`, meta.InternalIP, meta.NodeName, meta.InternalIP, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName, meta.NodeName)
		case KeyPVCUsage:
			queries[metricKey] = fmt.Sprintf(`((1 - node_filesystem_free_bytes{ __CLUSTER__ instance=~"%s.*", fstype !~"tmpfs"} / node_filesystem_size_bytes{ __CLUSTER__ instance=~"%s.*", fstype !~"tmpfs"}) * 100) or (kubelet_volume_stats_used_bytes{ __CLUSTER__ instance=~"%s.*"}  * 100/ kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"})`, meta.NodeIP, meta.NodeIP, meta.NodeName, meta.NodeName)
		case KeyNodeAZ:
			queries[metricKey] = `count(karpenter_nodes_total_pod_requests{ __CLUSTER__ provisioner_name="",resource_type="pods"}) by (zone)`
		case KeyPodAZ:
			queries[metricKey] = `sum(karpenter_pods_state{ __CLUSTER__ provisioner=""}) by (zone)`
		case KeyNoOfPods:
			queries[metricKey] = `sum(karpenter_pods_state{ __CLUSTER__ provisioner="", name=~".*-[0-9]+.*"})`
		case KeyNodePoolPodTrend:
			queries[metricKey] = `sum by (nodepool)(karpenter_pods_state{__CLUSTER__})`
		case KeyNodeclaimsDisrupted:
			queries[metricKey] = `round(sum(increase(karpenter_nodeclaims_disrupted_total{__CLUSTER__}[1h])) by (nodepool, capacity_type, reason))`
		case KeyNodeCreatedNodePool:
			queries[metricKey] = `round(sum(increase(karpenter_nodes_created_total{__CLUSTER__}[1h])) by (nodepool))`
		case KeyNodesTerminatedNodePool:
			queries[metricKey] = `round(sum(increase(karpenter_nodes_terminated_total{__CLUSTER__}[1h])) by (nodepool))`
		case KeyNodeDisruptionDecisionsReasonDecision:
			queries[metricKey] = `round(sum(increase(karpenter_voluntary_disruption_decisions_total{__CLUSTER__}[1h])) by (decision, reason))`
		case KeyNodesEligibleDisruptionReason:
			queries[metricKey] = `round(sum(increase(karpenter_voluntary_disruption_eligible_nodes{__CLUSTER__}[1h])) by (reason))`
		case KeyActiveNodes:
			queries[metricKey] = `kube_node_info{__CLUSTER__}`
		case KeyNetworkReceivePacket:
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_receive_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case KeyNetworkTransmitPackets:
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m])) or sum(irate(node_network_transmit_packets_total{__CLUSTER__ instance=~"%s.*", device!~"lo|veth.*|docker.*|flannel.*|cali.*|cbr.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		// Disk I/O throughput, from node-exporter. The device filter drops the
		// virtual block devices (loop/ram/dm) that would otherwise double-count the
		// physical disk they sit on. Same instance fallback chain as the network
		// metrics above: internal IP, then node name, then node IP.
		case KeyDiskReadBytes:
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_read_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		case KeyDiskWriteBytes:
			queries[metricKey] = fmt.Sprintf(`sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m])) or sum(irate(node_disk_written_bytes_total{__CLUSTER__ instance=~"%s.*", device!~"loop.*|ram.*|dm-.*"}[5m]))`, meta.InternalIP, meta.NodeName, meta.NodeIP)
		}
	}
	promApplyAggregate(queries, meta.Aggregate, rangeW, stepW)
	return queries
}

// promAggWindow derives the subquery range, step and inner-rate windows (as
// Prometheus/MetricsQL duration literals) for cluster utilisation aggregations from
// the picker's start/end (unix millis). The window follows the picker so the usage,
// P50/P90/Max and the usage-trend sparkline reflect the selected range instead of a
// hardcoded 24h. Falls back to 24h when the range is missing or invalid.
// promMaxRateWindowSeconds caps both the derived step and any rate window taken from
// an explicit step. Thirty minutes is long enough to smooth a scrape gap and short
// enough that rate() is not extrapolating across pods and nodes that did not live for
// the whole window.
const promMaxRateWindowSeconds = 1800

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
	if stepSec > promMaxRateWindowSeconds {
		stepSec = promMaxRateWindowSeconds
	}
	// Inner rate window: at least 5m (a few scrape intervals) and never finer than
	// the step, so each sampled point covers its whole interval.
	rateSec := stepSec
	if rateSec < 300 {
		rateSec = 300
	}
	return fmt.Sprintf("%ds", rangeSec), fmt.Sprintf("%ds", stepSec), fmt.Sprintf("%ds", rateSec)
}

// resolveAggWindows derives the range, step and rate windows for one request.
//
// An explicit step is the caller's sampling interval and outranks the one derived
// from the range: the rightsizing strategy samples at 75s, and a percentile taken
// over 5-minute averages is a different number.
//
// The rate window moves WITH it. A counter has no instantaneous value -- a usage
// figure is the rise between two readings divided by the gap between them, so that
// gap is half the definition of the number, not a performance knob. Honouring the
// step while leaving the rate window at promAggWindow's 300s floor answers a
// different question at the requested resolution: measured on dev, p99 CPU came back
// 26% low across 88 pods, because a 15-second burst was diluted by five minutes of
// idle either side of it, and rightsizing sizes containers on exactly that peak.
// With both set, the per-pod keys reproduce the PromQL they replace to the decimal.
//
// No floor is applied to an explicit step. The caller's step IS its statement of how
// finely it samples; the PromQL these keys replace has always used it unfloored; and
// any floor chosen here would be a guess about a cluster's scrape interval. A step
// finer than that interval yields no samples -- exactly what the old query does.
func resolveAggWindows(startMs, endMs int64, explicitStep int) (rangeW, stepW, rateW string) {
	rangeW, stepW, rateW = promAggWindow(startMs, endMs)
	if explicitStep > 0 {
		stepW = fmt.Sprintf("%ds", explicitStep)
		// The rate window follows the step, but only up to the ceiling the derived
		// path already uses. A step is a sampling interval; a rate window is the span
		// a counter's rise is divided by, and the two stop agreeing once the step
		// grows large. A caller sizing a bytes-per-day bucket sends a 1-day step,
		// which made cpu_usage_trend `rate(node_cpu_seconds_total[1d])`: over a day on
		// an autoscaling cluster that extrapolates across every node that came and
		// went, and the sum exceeds the machine core count. Rightsizing's 75s step is
		// far below the ceiling, so it keeps the exact window it asks for.
		rateSec := explicitStep
		if rateSec > promMaxRateWindowSeconds {
			rateSec = promMaxRateWindowSeconds
		}
		rateW = fmt.Sprintf("%ds", rateSec)
	}
	return rangeW, stepW, rateW
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
		// An exact pod list is scope in its own right, so it must not depend on a
		// workload name also being present: pod_names with no workload_name would
		// otherwise drop the matcher entirely and answer for the whole namespace.
		if safeMeta.Name != "" || safeMeta.PodNamesSupplied {
			switch {
			case safeMeta.PodNamesSupplied && len(safeMeta.PodNames) == 0:
				// The caller resolved the pod list and it is empty. That is a real
				// answer, so the selector must match nothing — falling back to the
				// prefix would size this workload on its neighbours' pods.
				//
				// An exact match on a name Kubernetes cannot produce, rather than a
				// clever regex: RFC1123 forbids underscores, and `pod=~"$^"` is NOT
				// empty-matching-nothing — Prometheus anchors matchers to ^(?:…)$ and
				// `$^` still matches the EMPTY string, which cAdvisor emits for
				// machine-level cgroups.
				podMatcher = `pod="__no_pods_resolved__"`
			case safeMeta.PodNamesSupplied:
				// An exact list beats the prefix, which is wrong twice over: it misses
				// pods that no longer exist, and "services-server" also matches
				// "services-server-worker", so one workload gets sized on another's.
				podMatcher = fmt.Sprintf(`pod=~"%s"`, promPodNameAlternation(safeMeta.PodNames))
			case safeMeta.Kind == "pod":
				podMatcher = fmt.Sprintf(`pod="%s"`, safeMeta.Name)
			default:
				// Regex match for deployments/statefulsets
				podMatcher = fmt.Sprintf(`pod=~"%s-.*"`, safeMeta.Name)
			}
			basePodFilter = fmt.Sprintf(` namespace="%s", %s`, safeMeta.Namespace, podMatcher)

			// Handle Container Filter Logic
			if safeMeta.ContainerName != "" {
				containerFilter = fmt.Sprintf(`%s, container="%s"`, basePodFilter, safeMeta.ContainerName)
				containerIDFilter = " " + promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, "")
			} else {
				// --- FIX: This ELSE block was missing ---
				// If no container_name is provided, we still need a filter (usually excluding empty containers)
				containerFilter = fmt.Sprintf(`%s, container!=""`, basePodFilter)
				containerIDFilter = " " + promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, "")
			}

		} else {
			// Namespace only case
			basePodFilter = fmt.Sprintf(` namespace="%s"`, safeMeta.Namespace)
			if safeMeta.ContainerName != "" {
				containerFilter = fmt.Sprintf(`%s, container="%s"`, basePodFilter, safeMeta.ContainerName)
			} else {
				// container!="" for the same reason the workload branch above sets it:
				// cAdvisor emits a pod-level cgroup rollup series with an empty
				// container label alongside the per-container ones. Summing both
				// counts every container twice — a namespace row read ~2x the truth.
				containerFilter = fmt.Sprintf(`%s, container!=""`, basePodFilter)
			}
			containerIDFilter = " " + promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, "", "")
		}
	}

	// --- 2. Destination Filters ---
	var destFilter, actualDestFilter string

	// httpWorkloadScope is the one selector every eBPF HTTP key scopes with, so a
	// workload's identity is decided once here instead of being re-invented per key.
	// It is built unconditionally because the workload_http_* keys inline it and
	// answer namespace-wide when no workload was named, where actualDestFilter is
	// left empty instead.
	httpWorkloadScope := fmt.Sprintf(`actual_destination_workload_namespace="%s"`, safeMeta.Namespace)
	if safeMeta.Name != "" {
		httpWorkloadScope = promWorkloadHTTPMatcher(safeMeta.Kind, safeMeta.Name) + ", " + httpWorkloadScope
	}

	if safeMeta.Namespace != "" && safeMeta.Name != "" {
		if safeMeta.Regex {
			destFilter = fmt.Sprintf(` destination_workload_namespace=~"%s", destination_workload_name=~"%s"`, safeMeta.Namespace, safeMeta.Name)
		} else {
			destFilter = fmt.Sprintf(` destination_workload_namespace="%s", destination_workload_name="%s"`, safeMeta.Namespace, safeMeta.Name)
		}
		actualDestFilter = " " + httpWorkloadScope
	} else if safeMeta.Namespace != "" {
		if safeMeta.Regex {
			destFilter = fmt.Sprintf(` destination_workload_namespace=~"%s"`, safeMeta.Namespace)
		} else {
			destFilter = fmt.Sprintf(` destination_workload_namespace="%s"`, safeMeta.Namespace)
		}
		actualDestFilter = " " + httpWorkloadScope
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
		switch UtilisationKey(metricKey) {
		// --- Per-pod container usage, for rightsizing -----------------------------
		//
		// One series per pod and container, unlike cpu_usage/memory_usage which sum
		// the workload into one. The shape is in the key name so it cannot vary with
		// the request: ~30 UI call sites read payload[0], and a key that sometimes
		// fans out would render one pod as the workload total, with no error.
		//
		// meta.Aggregate collapses each series to one point when the caller asks for
		// it (rightsizing asks for p92/p95/p97/p99, and count for its data floor);
		// empty leaves a time series, so these keys are chartable too.
		case KeyContainerCPUUsageByPod:
			expr := promKubeletPreferred(
				`max by (namespace, container, pod) (rate(container_cpu_usage_seconds_total{__CLUSTER__ %s}[`+rateW+`]))`,
				containerFilter)
			queries[metricKey] = expr
		case KeyContainerMemoryWorkingSetByPod:
			expr := promKubeletPreferred(
				`max by (namespace, container, pod) (container_memory_working_set_bytes{__CLUSTER__ %s})`,
				containerFilter)
			queries[metricKey] = expr
		case KeyContainerMemoryMaxUsageByPod:
			// The cgroup high-water mark, the floor for a memory LIMIT recommendation.
			// No other provider carries it, so they return nothing and say so; the
			// strategy already treats it as optional.
			expr := promKubeletPreferred(
				`max by (namespace, container, pod) (container_memory_max_usage_bytes{__CLUSTER__ %s})`,
				containerFilter)
			queries[metricKey] = expr

		case KeyContainerOOMKilledLimitByPod:
			// The memory LIMIT a container was holding when the kubelet OOM-killed it.
			//
			// Not a usage series: it is a spec value, gated on an event having
			// happened. kube-state-metrics publishes the two halves separately, so the
			// only way to ask "what was the limit at the kill" is to join them — the
			// limit series multiplied by the terminated-reason series, which is 1 only
			// where the reason is OOMKilled and absent everywhere else, so the product
			// exists only for containers that were actually killed.
			//
			// `job` is in the join keys because both halves come from kube-state-metrics
			// and carry it; dropping it would join a container's limit against another
			// scraper's kill record. `namespace` is in them too, which the PromQL this
			// replaces left out — two namespaces running a same-named pod would
			// otherwise cross-join, and the limit reported for one could come from the
			// other's kill.
			//
			// No promKubeletPreferred: neither half is a cAdvisor series, so there is
			// no per-scraper duplication to collapse and no `instance` arm to prefer.
			oomFilter := promSelector(containerFilter, `resource="memory"`)
			killFilter := promSelector(containerFilter, `reason="OOMKilled"`)
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, container, pod) (`+
					`max by (namespace, container, pod, job) (kube_pod_container_resource_limits{__CLUSTER__ %s})`+
					` * on (namespace, container, pod, job) group_left(reason) `+
					`max by (namespace, container, pod, job, reason) (kube_pod_container_status_last_terminated_reason{__CLUSTER__ %s})`+
					`)`,
				oomFilter, killFilter)

		// Requests and limits for the SAME series shape as the usage keys above, so a
		// consumer can join usage against its spec on (namespace, pod, container)
		// without a second request shape to reason about.
		//
		// No promKubeletPreferred: these come from kube-state-metrics, which is a
		// single series per container, not a per-scraper cAdvisor series.
		case KeyContainerCPURequestByPod:
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, container, pod) (kube_pod_container_resource_requests{__CLUSTER__ %s})`,
				promSelector(containerFilter, `resource="cpu"`))
		case KeyContainerCPULimitByPod:
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, container, pod) (kube_pod_container_resource_limits{__CLUSTER__ %s})`,
				promSelector(containerFilter, `resource="cpu"`))
		case KeyContainerMemoryRequestByPod:
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, container, pod) (kube_pod_container_resource_requests{__CLUSTER__ %s})`,
				promSelector(containerFilter, `resource="memory"`))
		case KeyContainerMemoryLimitByPod:
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, container, pod) (kube_pod_container_resource_limits{__CLUSTER__ %s})`,
				promSelector(containerFilter, `resource="memory"`))

		// --- PVC Metrics ---
		//
		// pvc_usage and pvc_requests sum every claim in scope into one number, which
		// is what a panel showing "this workload's storage" wants. The _by_claim pair
		// below answers the other question — one series per claim — because volume
		// rightsizing sizes each PVC separately and a sum tells it nothing.
		//
		// Both labels are on the series deliberately: a claim name is unique only
		// within its namespace, and the consumer keys on the pair.
		case KeyPVCUsedByClaim:
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, persistentvolumeclaim) (kubelet_volume_stats_used_bytes{__CLUSTER__ %s})`,
				pvcFilter)
		case KeyPVCCapacityByClaim:
			// kubelet_volume_stats_capacity_bytes, not the kube-state request: the
			// request is what was ASKED for and the capacity is what the volume
			// actually has, which is what a resize decision has to compare usage
			// against. pvc_requests above keeps answering the other one.
			queries[metricKey] = fmt.Sprintf(
				`max by (namespace, persistentvolumeclaim) (kubelet_volume_stats_capacity_bytes{__CLUSTER__ %s})`,
				pvcFilter)
		case KeyPVCUsage:
			queries[metricKey] = fmt.Sprintf(`sum(kubelet_volume_stats_used_bytes{__CLUSTER__ %s})`, pvcFilter)
		case KeyPVCRequests:
			queries[metricKey] = fmt.Sprintf(`sum(kube_persistentvolumeclaim_resource_requests_storage_bytes{__CLUSTER__ %s})`, pvcFilter)

		// --- HTTP / Network ---
		case KeyHTTPStatus:
			queries[metricKey] = fmt.Sprintf(`sum by (actual_destination_workload_namespace, status) (rate(container_http_requests_total{__CLUSTER__ %sjob!=""}[5m]))`, actualDestFilter)
		case KeyHTTPMaxResponseTime:
			queries[metricKey] = fmt.Sprintf(`max by (actual_destination_workload_namespace) (max_over_time(container_net_tcp_connection_time_seconds_total{__CLUSTER__ %sjob!=""}[5m]))`, actualDestFilter)
		case KeyHTTPThroughput:
			queries[metricKey] = fmt.Sprintf(`sort_desc(sum by(method, path, destination_workload_name, destination_workload_namespace)(increase(container_http_requests_total{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case KeyHTTPLatencyP95:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.95, sum by(le, path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case KeyHTTPLatencyP99:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum by(le, path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %sjob!=""}[1h])))`, destFilter)
		case KeyHTTPLatencySum:
			queries[metricKey] = fmt.Sprintf(`sum by (path, method, destination_workload_name, destination_workload_namespace) (increase(container_http_requests_duration_seconds_total_sum{__CLUSTER__ %sjob!=""}[1h]))`, destFilter)
		case KeyHTTPErrorRate:
			queries[metricKey] = fmt.Sprintf(`(sum by(method, path, destination_workload_name, destination_workload_namespace)(increase(container_http_requests_total{__CLUSTER__ %sstatus=~"^[45]..$"}[1h])) / sum(increase(container_http_requests_total{__CLUSTER__ %sjob!=""}[1h]))) * 100`, destFilter, destFilter)

		// --- Network Packet Logic ---
		// Network metrics are pod-scoped: cAdvisor's container_network_*_bytes_total series carry an
		// empty `container` label, so the container!="" constraint in containerFilter filters them all
		// out. Use basePodFilter (namespace + pod matcher only) instead.
		case KeyNetworkReceivePacket:
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_network_receive_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_received_total{__CLUSTER__ %sjob!=""}[5m])))`, basePodFilter, containerIDFilter)
		case KeyNetworkTransmitPackets:
			queries[metricKey] = fmt.Sprintf(`-((sum(rate(container_network_transmit_bytes_total{__CLUSTER__ %sjob!=""}[5m]))) or (sum(rate(container_net_tcp_bytes_sent_total{__CLUSTER__ %sjob!=""}[5m]))))`, basePodFilter, containerIDFilter)
		case KeyNetworkUsage:
			queries[metricKey] = fmt.Sprintf(`sum(container_net_tcp_connection_time_seconds_total{__CLUSTER__ %scontainer!=""}) or sum(kube_network_rx_bytes{__CLUSTER__ %scontainer!=""})`, containerFilter, basePodFilter)

		// --- Disk I/O ---
		// cAdvisor filesystem counters. Preferred form is containerFilter (container!="")
		// so the per-container series are summed without also adding the pod-level
		// rollup. Some runtimes only publish the rollup, which carries an empty
		// `container` label and is therefore excluded by that filter -- the basePodFilter
		// fallback catches those. `or` only evaluates the right side when the left
		// returns no series, so the two forms can never be summed together.
		case KeyDiskReadBytes:
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_fs_reads_bytes_total{__CLUSTER__ %s}[5m]))) or (sum(rate(container_fs_reads_bytes_total{__CLUSTER__ %sjob!=""}[5m])))`, containerFilter, basePodFilter)
		case KeyDiskWriteBytes:
			queries[metricKey] = fmt.Sprintf(`(sum(rate(container_fs_writes_bytes_total{__CLUSTER__ %s}[5m]))) or (sum(rate(container_fs_writes_bytes_total{__CLUSTER__ %sjob!=""}[5m])))`, containerFilter, basePodFilter)

		// --- Resources ---
		case KeyCPUUsage:
			queries[metricKey] = fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{__CLUSTER__ %s}[5m]))`, containerFilter)
		// Per-pod forms of the two usage keys, for charts that draw one line per pod
		// (the anomaly and SLO evidence cards). Same containerFilter as the totals
		// above, so container!="" keeps the pod-level cgroup series out: summing it
		// alongside the per-container series counts every pod twice.
		case KeyCPUUsageByPod:
			queries[metricKey] = fmt.Sprintf(`sum by (pod, namespace) (rate(container_cpu_usage_seconds_total{__CLUSTER__ %s}[5m]))`, containerFilter)
		case KeyMemoryUsageByPod:
			queries[metricKey] = fmt.Sprintf(`sum by (pod, namespace) (container_memory_working_set_bytes{__CLUSTER__ %s})`, containerFilter)
		// One series for the whole workload. http_latency_p99 above is per path and
		// method over an hour, shaped for the latency table; an evidence chart needs a
		// single p99 line at the chart's own resolution. Both this and cpu_throttling
		// read the node-agent's eBPF metrics, so they answer only where that agent runs.
		case KeyWorkloadHTTPLatencyP99:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum by (le) (rate(container_http_requests_duration_seconds_total_bucket{__CLUSTER__ %s}[5m])))`, httpWorkloadScope)
		// Request rate and mean latency for the whole workload, probes excluded.
		//
		// Siblings of workload_http_latency_p99 above: one series, no path or method
		// dimension, at the request's own resolution. The per-path http_throughput /
		// http_latency_sum keys answer a different question — they shape the latency
		// TABLE, where the path is the point.
		//
		// The probe exclusion is part of the key's meaning, not a caller's filter; see
		// metrics_health_endpoints.go for the measurement that forced it.
		case KeyWorkloadHTTPRequestRate:
			queries[metricKey] = fmt.Sprintf(
				`sum(rate(container_http_requests_total{__CLUSTER__ %s, path!~"%s"}[`+rateW+`]))`,
				httpWorkloadScope, healthCheckPathsRegex())
		case KeyWorkloadHTTPLatencyMean:
			// sum/count, not a quantile: the mean is what a capacity model wants, and
			// it is the one statistic a histogram gives exactly rather than by
			// interpolation. Division is guarded by `> 0` on the denominator so an
			// idle window is absent rather than +Inf.
			queries[metricKey] = fmt.Sprintf(
				`sum(rate(container_http_requests_duration_seconds_total_sum{__CLUSTER__ %s, path!~"%s"}[`+rateW+`]))`+
					` / (sum(rate(container_http_requests_duration_seconds_total_count{__CLUSTER__ %s, path!~"%s"}[`+rateW+`])) > 0)`,
				httpWorkloadScope, healthCheckPathsRegex(),
				httpWorkloadScope, healthCheckPathsRegex())
		case KeyCPUThrottling:
			queries[metricKey] = fmt.Sprintf(`sum by (container_id) (rate(container_resources_cpu_throttled_seconds_total{__CLUSTER__ container_id=~".*%s/%s.*"}[5m]))`, safeMeta.Namespace, safeMeta.Name)
		case KeyCPURequest:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_requests", fmt.Sprintf(`__CLUSTER__ %sresource="cpu"`, containerFilter))
		case KeyCPULimit:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_limits", fmt.Sprintf(`__CLUSTER__ %sresource="cpu"`, containerFilter))
		case KeyMemoryUsage:
			queries[metricKey] = fmt.Sprintf(`sum(container_memory_working_set_bytes{__CLUSTER__ %s})`, containerFilter)
		case KeyMemoryRequest:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_requests", fmt.Sprintf(`__CLUSTER__ %sresource="memory"`, containerFilter))
		case KeyMemoryLimit:
			queries[metricKey] = promSpecTotal("kube_pod_container_resource_limits", fmt.Sprintf(`__CLUSTER__ %sresource="memory"`, containerFilter))
		// Per-pod companions to the summed resource metrics: the running pod count
		// turns a workload total into an average per pod, and the busiest pod shows
		// what the average hides. Same filter as the totals so they divide cleanly.
		case KeyPodCount:
			queries[metricKey] = fmt.Sprintf(`count(count by (pod) (container_memory_working_set_bytes{__CLUSTER__ %s}))`, containerFilter)
		case KeyCPUUsageMaxPod:
			queries[metricKey] = fmt.Sprintf(`max(sum by (pod) (rate(container_cpu_usage_seconds_total{__CLUSTER__ %s}[5m])))`, containerFilter)
		case KeyMemoryUsageMaxPod:
			queries[metricKey] = fmt.Sprintf(`max(sum by (pod) (container_memory_working_set_bytes{__CLUSTER__ %s}))`, containerFilter)
		case KeyDiskTotal:
			queries[metricKey] = fmt.Sprintf(`sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"})`, safeMeta.InternalIP, safeMeta.NodeName, safeMeta.NodeIP)
		case KeyDiskUsed:
			queries[metricKey] = fmt.Sprintf(`(sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"}) - sum(node_filesystem_free_bytes{ __CLUSTER__ mountpoint="/", instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"%s.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"%s.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"%s.*"}))`, safeMeta.InternalIP, safeMeta.InternalIP, safeMeta.NodeName, safeMeta.NodeName, safeMeta.NodeIP, safeMeta.NodeIP)

		// --- Node/Cluster Aggregations ---
		// Usage / percentiles / peak are windowed by the picker range (rangeW) instead of a
		// hardcoded 24h, so the time filter actually adjusts the numbers. The percentile/peak
		// queries sum across the per-(node,core,mode) series FIRST, then aggregate over time.
		// Summing each series' own time-percentile instead (the old form) added peaks that occur
		// at different instants and produced values above physical capacity (P50/P90/Max >100%).
		// Valid in both PromQL (prod) and VictoriaMetrics MetricsQL (dev).
		case KeyCPUReal:
			queries[metricKey] = fmt.Sprintf(`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))`, rangeW, rangeW)
		case KeyCPUTotal:
			queries[metricKey] = `sum(machine_cpu_cores{__CLUSTER__}) or sum(node_resources_cpu_logical_cores{__CLUSTER__})`
		case KeyMemReal:
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case KeyMemTotal:
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__})`
		// Cluster network, as BYTES PER BUCKET rather than a rate.
		//
		// The existing network_receive_packet / network_transmit_packets keys are
		// per-second rates, and the transmit one is deliberately sign-negated so the
		// two render as a mirrored chart. The cluster tiles want neither: they ask
		// "how many bytes crossed the wire in this day / week / month", so the window
		// is the caller's own bucket and the answer is unsigned.
		//
		// meta.Step is that bucket, which a caller sets explicitly; the default
		// derived from the range would make each point cover a different span than
		// the one being charted.
		case KeyNetworkReceiveBytesIncrease:
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_network_receive_bytes_total{__CLUSTER__}[%s]))`, stepW)
		case KeyNetworkTransmitBytesIncrease:
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_network_transmit_bytes_total{__CLUSTER__}[%s]))`, stepW)

		// cpu_usage_trend / mem_usage_trend feed the utilisation sparkline: fetched as a RANGE
		// query so the relay evaluates them at each step across the picker window. CPU uses a
		// short rate window (rateW) so spikes register instead of being averaged away.
		case KeyCPUUsageTrend:
			queries[metricKey] = fmt.Sprintf(`sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s])) or sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))`, rateW, rateW)
		case KeyMemUsageTrend:
			queries[metricKey] = `sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or sum(node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case KeyP90Mem:
			queries[metricKey] = `quantile(0.9, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.9, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case KeyP90CPU:
			queries[metricKey] = fmt.Sprintf(`quantile_over_time(0.90, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or quantile_over_time(0.90, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case KeyP50Mem:
			queries[metricKey] = `quantile(0.5, node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemAvailable_bytes{__CLUSTER__}) or quantile(0.5, node_resources_memory_total_bytes{__CLUSTER__} - node_resources_memory_available_bytes{__CLUSTER__})`
		case KeyP50CPU:
			queries[metricKey] = fmt.Sprintf(`quantile_over_time(0.50, sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or quantile_over_time(0.50, sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case KeyMaxUsageMem:
			queries[metricKey] = fmt.Sprintf(`max_over_time(sum(node_memory_MemTotal_bytes{__CLUSTER__} - node_memory_MemFree_bytes{__CLUSTER__} - node_memory_Buffers_bytes{__CLUSTER__} - node_memory_Cached_bytes{__CLUSTER__})[%s:%s])`, rangeW, stepW)
		case KeyMaxUsageCPU:
			queries[metricKey] = fmt.Sprintf(`max_over_time(sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s]) or max_over_time(sum(rate(node_resources_cpu_usage_seconds_total{__CLUSTER__ mode!="idle"}[%s]))[%s:%s])`, rateW, rangeW, stepW, rateW, rangeW, stepW)
		case KeyReplicaDefined:
			queries[metricKey] = promWorkloadReplicaQuery(safeMeta, false)
		case KeyReplicaReady:
			queries[metricKey] = promWorkloadReplicaQuery(safeMeta, true)

		// others
		case KeyContainerApplicationTypeWithPod:
			queries[metricKey] = fmt.Sprintf(`container_application_type{ __CLUSTER__ container_id=~"/k8s/%s/%s.*"}`, safeMeta.Namespace, safeMeta.Name)
		case KeyContainerApplicationTypeWithWorkload:
			queries[metricKey] = fmt.Sprintf(`container_application_type{ __CLUSTER__ container_id=~"/k8s/%s/%s-.*"}`, safeMeta.Namespace, safeMeta.Name)
		case KeyJVMMemoryMetricCount:
			queries[metricKey] = fmt.Sprintf(`count by (namespace, pod) ({ __CLUSTER__ __name__=~"process.runtime.jvm.memory.usage|process_runtime_jvm_memory_usage_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case KeyCPythonMemoryMetricCount:
			queries[metricKey] = fmt.Sprintf(`count by (pod, namespace) ({ __CLUSTER__ __name__=~"process.runtime.cpython.memory|process_runtime_cpython_memory_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case KeyGoHeapMemoryMetricCount:
			queries[metricKey] = fmt.Sprintf(`count by (pod, namespace) ({ __CLUSTER__ __name__=~"process.runtime.go.mem.heap_sys|process_runtime_go_mem_heap_sys_bytes|go.memory.used|go_memory_used_bytes", namespace=~"%s"})`, safeMeta.Namespace)
		case KeyServiceInfoByClusterIP:
			queries[metricKey] = fmt.Sprintf(`kube_service_info{ __CLUSTER__ cluster_ip="%s"}`, safeMeta.InternalIP)
		case KeySensitiveLogMessages:
			queries[metricKey] = "sum(increase(container_sensitive_log_messages_total{__CLUSTER__}[5m])) by (pattern, container_id, regex, name, pattern_hash)"
		case KeyContainerErrorLogCountWithPod:
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_log_messages_total{ __CLUSTER__ %s, level=~"critical|error|exception"}[5m])) by (container_id)`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, ""))
		case KeyContainerErrorLogCountWithWorkload:
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_log_messages_total{ __CLUSTER__ %s, level=~"critical|error"}[5m])) by (container_id)`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, ""))
		// Error and request counts as separate series, so a consumer can apply its own
		// smoothing to the ratio instead of inheriting whatever this layer chose. The
		// anomaly detector needs exactly that: it divides by (requests + K) to stop a
		// 1-error-in-1-request moment reading as a 100% error rate.
		//
		// Both are an increase over the REQUESTED STEP, not a fixed window, so the value
		// scales with the caller's resolution — a consumer deriving a constant from these
		// (such as that K) must pin its step. Scoped on destination_workload_name, which
		// carries the workload name and is matched exactly; the actual_destination_*
		// labels carry pod names and would need a prefix, which over-matches siblings.
		case KeyWorkloadHTTPRequestCount:
			queries[metricKey] = fmt.Sprintf(
				`sum(increase(container_http_requests_total{ __CLUSTER__ destination_workload_name="%s", destination_workload_namespace="%s"}[%s]))`,
				safeMeta.Name, safeMeta.Namespace, stepW)
		case KeyWorkloadHTTPErrorCount:
			queries[metricKey] = fmt.Sprintf(
				`sum(increase(container_http_requests_total{ __CLUSTER__ destination_workload_name="%s", destination_workload_namespace="%s", status=~"^[45][0-9][0-9]$"}[%s]))`,
				safeMeta.Name, safeMeta.Namespace, stepW)
		case KeyWorkloadHTTPErrorRate:
			queries[metricKey] = fmt.Sprintf(`sum by(destination_workload_name, destination_workload_namespace)(rate(container_http_requests_total{ __CLUSTER__ status=~"5..|4..", destination_workload_name=~"%s", destination_workload_namespace=~"%s"}[1h])) / sum by(destination_workload_name, destination_workload_namespace)(rate(container_http_requests_total{ __CLUSTER__ destination_workload_name=~"%s", destination_workload_namespace=~"%s"}[1h]))`, safeMeta.Name, safeMeta.Namespace, safeMeta.Name, safeMeta.Namespace)
		case KeyContainerHTTPLatencyP90:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.90, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ %s, destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerHTTPLatencyP99:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.99, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ %s, destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerHTTPLatencyP95:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.95, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ %s, destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerHTTPLatencyP50:
			queries[metricKey] = fmt.Sprintf(`histogram_quantile(0.50, sum(rate(container_http_requests_duration_seconds_total_bucket{ __CLUSTER__ %s, destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) by (le))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerHTTPLatencyMean:
			// Divide only where requests were counted: with none, the count's
			// rate is 0 while a stored float _sum can wobble by an ulp, and
			// the ratio comes out +Inf instead of empty.
			queries[metricKey] = fmt.Sprintf(`sum(rate(container_http_requests_duration_seconds_total_sum{ __CLUSTER__ %s, destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) / (sum(rate(container_http_requests_duration_seconds_total_count{ __CLUSTER__ %s, destination_workload_namespace!="external", destination_workload_namespace!=""}[1h])) > 0)`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName), promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerHTTPRequestCount:
			queries[metricKey] = fmt.Sprintf(`sum(increase(container_http_requests_total{ __CLUSTER__ %s}[1h]))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerHTTPErrorStatusCount:
			queries[metricKey] = fmt.Sprintf(`sum by(status) (increase(container_http_requests_total{ __CLUSTER__ status=~"4..|5..",%s}[1h]))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerTopDestinationServices:
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (rate(container_http_requests_total{ __CLUSTER__ %s}[1h])))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyCPUUsagePod:
			queries[metricKey] = fmt.Sprintf(`sum(irate(container_cpu_usage_seconds_total{namespace="%s", pod=~"%s"}[1m]))`, safeMeta.Namespace, safeMeta.Name)
		case KeyCPURequestPod:
			queries[metricKey] = fmt.Sprintf(`%s (kube_pod_container_resource_requests{resource = "cpu", namespace="%s", pod=~"%s"})`, promSpecPerContainer, safeMeta.Namespace, safeMeta.Name)
		case KeyCPULimitPod:
			queries[metricKey] = fmt.Sprintf(`%s (kube_pod_container_resource_limits{resource = "cpu", namespace="%s", pod=~"%s"})`, promSpecPerContainer, safeMeta.Namespace, safeMeta.Name)
		case KeyContainerTopHTTPRequests:
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (rate(container_http_requests_total{ __CLUSTER__ %s}[1h])))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		case KeyContainerTopCPUUsage:
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (pod, namespace) (rate(container_cpu_usage_seconds_total{ __CLUSTER__ pod=~"%s", namespace=~"%s"}[1h])))`, safeMeta.Name, safeMeta.Namespace)
		case KeyContainerTopMemoryUsage:
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (pod, namespace) (rate(container_memory_working_set_bytes{ __CLUSTER__ pod=~"%s", namespace=~"%s"}[1h])))`, safeMeta.Name, safeMeta.Namespace)
		case KeyContainerTopHTTPErrorCalls:
			queries[metricKey] = fmt.Sprintf(`topk(5, sum by (destination_workload_name, destination_workload_namespace) (increase(container_http_requests_total{ __CLUSTER__ status=~"4..|5..",%s}[1h])))`, promContainerIDPath(safeMeta.Kind, safeMeta.Namespace, safeMeta.Name, safeMeta.ContainerName))
		}
	}
	promApplyAggregate(queries, safeMeta.Aggregate, rangeW, stepW)
	return queries
}

// promWorkloadHTTPMatcher selects one workload's eBPF HTTP series by identity.
//
// These series name the workload that was ADDRESSED and the pod that actually SERVED
// under separate labels: destination_workload_name carries the controller (its
// destination_workload_kind reads Deployment / StatefulSet / DaemonSet), while
// actual_destination_workload_name carries the pod -- actual_destination_workload_kind
// says "pod" on every one of them. A workload-scoped question has to read the workload
// label. Reaching a workload's pods through the POD label needs a `<name>.*` prefix,
// and that prefix also matches every pod of a workload whose name merely STARTS with
// this one: measured on a dev cluster, `services-server` answered over 41 pods, 21 of
// them `services-server-worker`'s, so the request rate was the two deployments' traffic
// added together (0.19 = 0.17 + 0.02 req/s) and the mean latency sat 10.5% low because
// the sibling's much faster calls diluted it.
//
// kind == "pod" is the one case that must NOT switch labels. The name is then a POD
// name, which only actual_destination_workload_name carries -- it matches there
// exactly and matches destination_workload_name not at all. The pod details page's
// "Request & Latency" chart asks for http_status this way, so switching it would blank
// that chart rather than narrow it.
//
// Known limitation, unchanged by this function and shared with the destination_*-scoped
// keys that already existed (workload_http_error_rate, http_throughput): where two
// workloads sit behind ONE Service, the agent stamps a single destination_workload_name
// on that Service's traffic, so a canary is not separable from its stable deployment.
func promWorkloadHTTPMatcher(kind, name string) string {
	if kind == "pod" {
		return fmt.Sprintf(`actual_destination_workload_name="%s"`, name)
	}
	return fmt.Sprintf(`destination_workload_name="%s"`, name)
}

// promPodNameAlternation renders an exact pod list as a PromQL alternation. The empty
// case is handled by the caller, which emits a match-nothing selector instead.
//
// Names are escaped because they reach a regex, even though Kubernetes names cannot
// contain regex metacharacters today — the matcher should not be the thing that
// depends on that staying true.
func promPodNameAlternation(pods []string) string {
	escaped := make([]string, 0, len(pods))
	for _, pod := range pods {
		// QuoteMeta FIRST, then the string escape. The other order leaves the
		// backslashes QuoteMeta adds unescaped for the PromQL literal, and a pod name
		// containing a "." (RFC1123 allows it) renders as pod=~"my\.pod", which the
		// parser rejects outright: `unknown escape sequence U+002E`. One unparseable
		// matcher fails the whole query, so this is not a cosmetic ordering.
		escaped = append(escaped, escapePromQLString(regexp.QuoteMeta(pod)))
	}
	return strings.Join(escaped, "|")
}

// promKubeletPreferred collapses the duplicate series a cluster with two cAdvisor
// scrapers produces.
//
// kube-state-metrics and the kubelet can both report the same container, under
// different `job` labels, so summing or maxing across them double counts — the 2x
// overcount this work has already chased once. The rightsizing loaders solve it by
// grouping on (container, pod, job) and keeping the kubelet copy; the same preference
// is expressed here in the query, where it belongs, because it is a property of a
// Prometheus deployment and not of the contract. The fallback arm answers clusters
// whose kubelet series are labelled with some other job name.
// promGaugeSeries / promRateSeries name a cAdvisor series given a selector body.
// Split out so promPerContainerOnNode can build the same series twice, once per node
// label convention, without knowing whether it is a gauge or a counter.
func promGaugeSeries(metric string) func(string) string {
	return func(selector string) string {
		return fmt.Sprintf(`%s{__CLUSTER__ %s}`, metric, selector)
	}
}

func promRateSeries(metric, rateWindow string) func(string) string {
	return func(selector string) string {
		return fmt.Sprintf(`rate(%s{__CLUSTER__ %s}[%s])`, metric, selector, rateWindow)
	}
}

// promPerContainerOnNode returns one series per {namespace, pod, container} on a node.
//
// A cluster labels cAdvisor series with `node` or with `instance` depending on how it
// scrapes, and some carry both, so the query asks both ways and unions the results.
// Aggregating BEFORE the `or` is deliberate: `or` is a set union on label sets, so
// once both arms group to the same {namespace, pod, container} the left arm wins per
// series and a container present in both is counted once, not twice.
//
// `max by`, not `sum by`, for the same reason the workload keys use it: a cluster with
// two cAdvisor scrapers emits the same container twice, and summing would double every
// number silently.
func promPerContainerOnNode(series func(string) string, nodeName string, extraFilters ...string) string {
	const groupBy = `max by (namespace, pod, container)`
	return fmt.Sprintf(`%s (%s) or %s (%s)`,
		groupBy, series(promSelector(fmt.Sprintf(`node="%s"`, nodeName), extraFilters...)),
		groupBy, series(promSelector(fmt.Sprintf(`instance="%s"`, nodeName), extraFilters...)))
}

// promNodeAllocatable is what one node can hand out, as one series.
//
// `max by (node)` rather than a bare selector for two reasons, both observed on the
// dev cluster: a cluster running more than one kube-state-metrics emits the series
// once per instance, and each copy carries that instance's OWN namespace/pod labels.
// Without the grouping a caller gets two identical series labelled by whichever
// namespace kube-state-metrics happens to run in — and anything that sums them reads
// double the node's real capacity.
func promNodeAllocatable(nodeName, resource string) string {
	return fmt.Sprintf(`max by (node) (kube_node_status_allocatable{__CLUSTER__ %s})`,
		promSelector("", fmt.Sprintf(`resource="%s"`, resource), fmt.Sprintf(`node="%s"`, nodeName)))
}

// promSpecPerContainer collapses request/limit series to one per container.
//
// kube-state-metrics is not the only exporter of these series: OpenCost re-exports
// kube_pod_container_resource_requests (but not _limits) and kube_node_status_allocatable,
// and a cluster may run more than one kube-state-metrics. Each copy carries its own
// job/instance labels, so anything that adds series up must collapse them first — on
// the dev cluster a bare sum reported every request at twice its real value, and the
// resize screen then proposed a request above the container's own limit.
const promSpecPerContainer = `max by (namespace, pod, container)`

// promSpecOnNode is the kube-state-metrics request/limit series for one node.
func promSpecOnNode(metric, nodeName, resource string) string {
	return fmt.Sprintf(`%s (%s{__CLUSTER__ %s})`,
		promSpecPerContainer, metric, promSelector("", fmt.Sprintf(`resource="%s"`, resource), fmt.Sprintf(`node="%s"`, nodeName)))
}

// promSpecTotal is a request/limit metric summed over the containers the selector
// matches, each container counted once whichever exporters publish it.
func promSpecTotal(metric, selector string) string {
	return fmt.Sprintf(`sum(%s (%s{%s}))`, promSpecPerContainer, metric, selector)
}

// promContainerIDPath matches the eBPF metrics' container_id, which is a PATH and not a
// name: `/k8s/<namespace>/<pod>/<container>`.
//
// Every caller of it was filtering `container_id=~"<name>"`, and Prometheus anchors its
// regexes, so that required container_id to EQUAL the name. It never can. Measured on
// dev: the workload form returned 0 series where this one returns 696, and the same held
// for container_log_messages_total (0 vs 50) and container_net_tcp_bytes_received_total
// (0 vs 21). The affected keys had been answering nothing on every provider.
//
// The pod segment needs the `-` : a workload's pods are `<workload>-<hash>-<id>`, so
// matching `<workload>/` alone lands on a pod called exactly the workload name, which
// does not exist. That is the same prefix-vs-exact trap the pod selectors carry, and the
// reason it is one helper rather than a format string at each call site.
// `kind` decides whether the pod segment is a prefix or an exact name. At pod scope the
// caller's Name IS the pod (see the `safeMeta.Kind == "pod"` arm of the pod matcher), so
// appending `-.*` there asks for a pod whose name extends a full pod name -- which no pod
// has. Passing the kind rather than a bare bool keeps that decision legible at the 17 call
// sites, where `true` would read as an unexplained flag.
func promContainerIDPath(kind, namespace, workload, container string) string {
	// QuoteMeta before the string escape, the same order and for the same reason as
	// promPodNameAlternation: these are REGEX segments, so a name containing "." (RFC1123
	// allows it) would otherwise match any character there, and the reverse order leaves
	// QuoteMeta's backslashes unescaped for the PromQL literal.
	quote := func(v string) string { return escapePromQLString(regexp.QuoteMeta(v)) }
	pod := ".*"
	if workload != "" {
		pod = quote(workload) + "-.*"
		// "" is treated as pod too: an unspecified kind reaches the pod-scoped keys with
		// a pod in Name, the same reading the rest of this package takes.
		if kind == "pod" || kind == "" {
			pod = quote(workload)
		}
	}
	c := ".*"
	if container != "" {
		c = quote(container)
	}
	ns := ".*"
	if namespace != "" {
		ns = quote(namespace)
	}
	return fmt.Sprintf(`container_id=~"/k8s/%s/%s/%s"`, ns, pod, c)
}

// promSelector joins a scope filter with extra matchers into one selector body.
//
// The scope filters are built with a leading space and sometimes a trailing comma so
// callers can concatenate them; at CLUSTER scope the filter is empty entirely. Either
// edge produces `{__CLUSTER__ , resource="cpu"}` or `{__CLUSTER__ ,}` if the parts are
// pasted together naively, and Prometheus rejects both. Joining explicitly means a key
// renders the same whether it was asked at workload, namespace or cluster scope.
func promSelector(filter string, extra ...string) string {
	parts := make([]string, 0, len(extra)+1)
	if f := strings.Trim(strings.TrimSpace(filter), ","); f != "" {
		parts = append(parts, f)
	}
	parts = append(parts, extra...)
	return strings.Join(parts, ", ")
}

func promKubeletPreferred(expr, filter string) string {
	// The shared filters are built with a trailing comma so callers can append to
	// them; both arms here append, so trim it first or the selector gets `,,` and
	// Prometheus rejects the whole query. The filter is also EMPTY for a request
	// that carries no namespace — a cluster-wide shape this same builder already
	// serves for p90_cpu and friends — and a leading comma there is just as invalid.
	filter = strings.TrimRight(filter, ", ")
	kubeletFilter := `job="kubelet"`
	if filter != "" {
		kubeletFilter = filter + `, ` + kubeletFilter
	}
	return fmt.Sprintf(`%s or %s`,
		fmt.Sprintf(expr, kubeletFilter),
		fmt.Sprintf(expr, filter))
}

// promWorkloadReplicaQuery renders replica_defined / replica_ready for the workload
// named in meta.
//
// The replica keys used to read the ReplicaSet family through
// `replicaset=~"<name>.*"`, which is wrong twice over. The regex is a prefix, so
// "services-server" also matches every ReplicaSet of "services-server-worker"; and a
// Deployment keeps one ReplicaSet per revision, so even the right workload contributes
// as many series as it has revisions. Measured on dev: the key answered 3 for a
// Deployment with 1 replica, summing 22 ReplicaSets across two Deployments.
//
// Reading the controller's own object instead gives one series per workload, and the
// controller is the thing the caller asked about. The kind decides the family; anything
// else (including an absent kind) is treated as a Deployment, which is what every
// caller sends today.
func promWorkloadReplicaQuery(meta RequestMetadata, ready bool) string {
	var metric, label string
	switch strings.ToLower(meta.Kind) {
	case "statefulset":
		label, metric = "statefulset", "kube_statefulset_replicas"
		if ready {
			metric = "kube_statefulset_status_replicas_ready"
		}
	case "daemonset":
		// A DaemonSet has no replica count; the scheduler's desired/ready pod counts
		// are the same quantity under a different name.
		label, metric = "daemonset", "kube_daemonset_status_desired_number_scheduled"
		if ready {
			metric = "kube_daemonset_status_number_ready"
		}
	case "replicaset":
		label, metric = "replicaset", "kube_replicaset_spec_replicas"
		if ready {
			metric = "kube_replicaset_status_ready_replicas"
		}
	default:
		label, metric = "deployment", "kube_deployment_spec_replicas"
		if ready {
			metric = "kube_deployment_status_replicas_ready"
		}
	}
	// max, not sum: the selector now identifies exactly one workload, so every series
	// it matches carries the same number. Summing is only ever equal or wrong — it
	// reads N× wherever two kube-state-metrics replicas report the same object, which
	// is the failure this whole key just had in a different form.
	return fmt.Sprintf(`max(%s{ __CLUSTER__ namespace="%s", %s="%s"})`,
		metric, meta.Namespace, label, meta.Name)
}
