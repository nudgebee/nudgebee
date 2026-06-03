package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"strings"
)

const DatadogMetricsQueryAgentName = "datadog_metrics_query"

var availableDatadogMetrics = []string{
	"cert_manager.certificate.expiration_timestamp",
	"cert_manager.certificate.ready_status",
	"cert_manager.clock_time",
	"cert_manager.controller.sync_call.count",
	"cert_manager.http_acme_client.request.count",
	"cert_manager.http_acme_client.request.duration.count",
	"cert_manager.http_acme_client.request.duration.sum",
	"container.cpu.limit",
	"container.cpu.system",
	"container.cpu.throttled",
	"container.cpu.throttled.periods",
	"container.cpu.usage",
	"container.cpu.user",
	"container.io.read",
	"container.io.read.operations",
	"container.io.write",
	"container.io.write.operations",
	"container.memory.cache",
	"container.memory.kernel",
	"container.memory.limit",
	"container.memory.major_page_faults",
	"container.memory.oom_events",
	"container.memory.page_faults",
	"container.memory.rss",
	"container.memory.swap",
	"container.memory.usage",
	"container.memory.usage.peak",
	"container.memory.working_set",
	"container.net.rcvd",
	"container.net.rcvd.packets",
	"container.net.sent",
	"container.net.sent.packets",
	"container.pid.open_files",
	"container.pid.thread_count",
	"container.uptime",
	"kubernetes.apiserver.certificate.expiration.count",
	"kubernetes.apiserver.certificate.expiration.sum",
	"kubernetes.containers.last_state.terminated",
	"kubernetes.containers.restarts",
	"kubernetes.containers.running",
	"kubernetes.containers.state.waiting",
	"kubernetes.cpu.cfs.periods",
	"kubernetes.cpu.cfs.throttled.periods",
	"kubernetes.cpu.cfs.throttled.seconds",
	"kubernetes.cpu.limits",
	"kubernetes.cpu.load.10s.avg",
	"kubernetes.cpu.requests",
	"kubernetes.cpu.system.total",
	"kubernetes.cpu.usage.total",
	"kubernetes.cpu.user.total",
	"kubernetes.ephemeral_storage.limits",
	"kubernetes.ephemeral_storage.requests",
	"kubernetes.ephemeral_storage.usage",
	"kubernetes.go_goroutines",
	"kubernetes.go_threads",
	"kubernetes.io.read_bytes",
	"kubernetes.io.write_bytes",
	"kubernetes.kubelet.container.log_filesystem.used_bytes",
	"kubernetes.kubelet.cpu.usage",
	"kubernetes.kubelet.cpu_manager.pinning_errors_total",
	"kubernetes.kubelet.cpu_manager.pinning_requests_total",
	"kubernetes.kubelet.evictions",
	"kubernetes.kubelet.memory.rss",
	"kubernetes.kubelet.memory.usage",
	"kubernetes.kubelet.pleg.discard_events",
	"kubernetes.kubelet.pleg.last_seen",
	"kubernetes.kubelet.pleg.relist_duration.count",
	"kubernetes.kubelet.pleg.relist_duration.sum",
	"kubernetes.kubelet.pleg.relist_interval.count",
	"kubernetes.kubelet.pleg.relist_interval.sum",
	"kubernetes.kubelet.pod.start.duration.count",
	"kubernetes.kubelet.pod.start.duration.sum",
	"kubernetes.kubelet.pod.worker.duration.count",
	"kubernetes.kubelet.pod.worker.duration.sum",
	"kubernetes.kubelet.pod.worker.start.duration.count",
	"kubernetes.kubelet.pod.worker.start.duration.sum",
	"kubernetes.kubelet.runtime.errors",
	"kubernetes.kubelet.runtime.operations",
	"kubernetes.kubelet.runtime.operations.duration.count",
	"kubernetes.kubelet.runtime.operations.duration.sum",
	"kubernetes.kubelet.volume.stats.available_bytes",
	"kubernetes.kubelet.volume.stats.capacity_bytes",
	"kubernetes.kubelet.volume.stats.inodes",
	"kubernetes.kubelet.volume.stats.inodes_free",
	"kubernetes.kubelet.volume.stats.inodes_used",
	"kubernetes.kubelet.volume.stats.used_bytes",
	"kubernetes.liveness_probe.failure.total",
	"kubernetes.liveness_probe.success.total",
	"kubernetes.memory.cache",
	"kubernetes.memory.limits",
	"kubernetes.memory.requests",
	"kubernetes.memory.rss",
	"kubernetes.memory.sw_limit",
	"kubernetes.memory.swap",
	"kubernetes.memory.usage",
	"kubernetes.memory.usage_pct",
	"kubernetes.memory.working_set",
	"kubernetes.network.rx_bytes",
	"kubernetes.network.rx_dropped",
	"kubernetes.network.rx_errors",
	"kubernetes.network.tx_bytes",
	"kubernetes.network.tx_dropped",
	"kubernetes.network.tx_errors",
	"kubernetes.node.filesystem.usage",
	"kubernetes.node.filesystem.usage_pct",
	"kubernetes.node.image.filesystem.usage",
	"kubernetes.node.image.filesystem.usage_pct",
	"kubernetes.pods.expired",
	"kubernetes.pods.running",
	"kubernetes.readiness_probe.failure.total",
	"kubernetes.readiness_probe.success.total",
	"kubernetes.rest.client.latency.count",
	"kubernetes.rest.client.latency.sum",
	"kubernetes.rest.client.requests",
	"kubernetes.slis.kubernetes_healthcheck",
	"kubernetes.slis.kubernetes_healthchecks_total",
	"kubernetes.startup_probe.failure.total",
	"kubernetes.startup_probe.success.total",
	"kubernetes_state.apiservice.condition",
	"kubernetes_state.apiservice.count",
	"kubernetes_state.configmap.count",
	"kubernetes_state.container.cpu_limit",
	"kubernetes_state.container.cpu_limit.total",
	"kubernetes_state.container.cpu_requested",
	"kubernetes_state.container.cpu_requested.total",
	"kubernetes_state.container.memory_limit",
	"kubernetes_state.container.memory_limit.total",
	"kubernetes_state.container.memory_requested",
	"kubernetes_state.container.memory_requested.total",
	"kubernetes_state.container.ready",
	"kubernetes_state.container.restarts",
	"kubernetes_state.container.running",
	"kubernetes_state.container.status_report.count.waiting",
	"kubernetes_state.container.terminated",
	"kubernetes_state.container.waiting",
	"kubernetes_state.crd.condition",
	"kubernetes_state.crd.count",
	"kubernetes_state.daemonset.count",
	"kubernetes_state.daemonset.daemons_available",
	"kubernetes_state.daemonset.daemons_unavailable",
	"kubernetes_state.daemonset.desired",
	"kubernetes_state.daemonset.misscheduled",
	"kubernetes_state.daemonset.ready",
	"kubernetes_state.daemonset.scheduled",
	"kubernetes_state.daemonset.updated",
	"kubernetes_state.deployment.condition",
	"kubernetes_state.deployment.count",
	"kubernetes_state.deployment.paused",
	"kubernetes_state.deployment.replicas",
	"kubernetes_state.deployment.replicas_available",
	"kubernetes_state.deployment.replicas_desired",
	"kubernetes_state.deployment.replicas_ready",
	"kubernetes_state.deployment.replicas_unavailable",
	"kubernetes_state.deployment.replicas_updated",
	"kubernetes_state.deployment.rollingupdate.max_surge",
	"kubernetes_state.deployment.rollingupdate.max_unavailable",
	"kubernetes_state.endpoint.address_available",
	"kubernetes_state.endpoint.address_not_ready",
	"kubernetes_state.endpoint.count",
	"kubernetes_state.ingress.count",
	"kubernetes_state.ingress.path",
	"kubernetes_state.ingress.tls",
	"kubernetes_state.initcontainer.restarts",
	"kubernetes_state.initcontainer.waiting",
	"kubernetes_state.job.completion.failed",
	"kubernetes_state.job.completion.succeeded",
	"kubernetes_state.job.count",
	"kubernetes_state.job.duration",
	"kubernetes_state.job.failed",
	"kubernetes_state.job.succeeded",
	"kubernetes_state.namespace.count",
	"kubernetes_state.node.age",
	"kubernetes_state.node.by_condition",
	"kubernetes_state.node.count",
	"kubernetes_state.node.cpu_allocatable",
	"kubernetes_state.node.cpu_allocatable.total",
	"kubernetes_state.node.cpu_capacity",
	"kubernetes_state.node.cpu_capacity.total",
	"kubernetes_state.node.ephemeral_storage_allocatable",
	"kubernetes_state.node.ephemeral_storage_capacity",
	"kubernetes_state.node.memory_allocatable",
	"kubernetes_state.node.memory_allocatable.total",
	"kubernetes_state.node.memory_capacity",
	"kubernetes_state.node.memory_capacity.total",
	"kubernetes_state.node.pods_allocatable",
	"kubernetes_state.node.pods_capacity",
	"kubernetes_state.node.status",
	"kubernetes_state.pdb.disruptions_allowed",
	"kubernetes_state.pdb.pods_desired",
	"kubernetes_state.pdb.pods_healthy",
	"kubernetes_state.pdb.pods_total",
	"kubernetes_state.persistentvolume.by_phase",
	"kubernetes_state.persistentvolume.capacity",
	"kubernetes_state.persistentvolumeclaim.access_mode",
	"kubernetes_state.persistentvolumeclaim.request_storage",
	"kubernetes_state.persistentvolumeclaim.status",
	"kubernetes_state.persistentvolumes.by_phase",
	"kubernetes_state.pod.age",
	"kubernetes_state.pod.count",
	"kubernetes_state.pod.ready",
	"kubernetes_state.pod.scheduled",
	"kubernetes_state.pod.status_phase",
	"kubernetes_state.pod.tolerations",
	"kubernetes_state.pod.uptime",
	"kubernetes_state.pod.volumes.persistentvolumeclaims_readonly",
	"kubernetes_state.replicaset.count",
	"kubernetes_state.replicaset.fully_labeled_replicas",
	"kubernetes_state.replicaset.replicas",
	"kubernetes_state.replicaset.replicas_desired",
	"kubernetes_state.replicaset.replicas_ready",
	"kubernetes_state.secret.count",
	"kubernetes_state.secret.type",
	"kubernetes_state.service.count",
	"kubernetes_state.service.type",
	"kubernetes_state.statefulset.count",
	"kubernetes_state.statefulset.replicas",
	"kubernetes_state.statefulset.replicas_current",
	"kubernetes_state.statefulset.replicas_desired",
	"kubernetes_state.statefulset.replicas_ready",
	"kubernetes_state.statefulset.replicas_updated",
	"ntp.offset",
	"runtime.node.cpu.system",
	"runtime.node.cpu.total",
	"runtime.node.cpu.user",
	"runtime.node.event_loop.delay.95percentile",
	"runtime.node.event_loop.delay.avg",
	"runtime.node.event_loop.delay.count",
	"runtime.node.event_loop.delay.max",
	"runtime.node.event_loop.delay.median",
	"runtime.node.event_loop.delay.min",
	"runtime.node.event_loop.delay.sum",
	"runtime.node.event_loop.delay.total",
	"runtime.node.event_loop.utilization",
	"runtime.node.gc.pause.95percentile",
	"runtime.node.gc.pause.avg",
	"runtime.node.gc.pause.by.type.95percentile",
	"runtime.node.gc.pause.by.type.avg",
	"runtime.node.gc.pause.by.type.count",
	"runtime.node.gc.pause.by.type.max",
	"runtime.node.gc.pause.by.type.median",
	"runtime.node.gc.pause.by.type.min",
	"runtime.node.gc.pause.by.type.sum",
	"runtime.node.gc.pause.by.type.total",
	"runtime.node.gc.pause.count",
	"runtime.node.gc.pause.max",
	"runtime.node.gc.pause.median",
	"runtime.node.gc.pause.min",
	"runtime.node.gc.pause.sum",
	"runtime.node.gc.pause.total",
	"runtime.node.heap.available_size.by.space",
	"runtime.node.heap.heap_size_limit",
	"runtime.node.heap.malloced_memory",
	"runtime.node.heap.peak_malloced_memory",
	"runtime.node.heap.physical_size.by.space",
	"runtime.node.heap.size.by.space",
	"runtime.node.heap.total_available_size",
	"runtime.node.heap.total_heap_size",
	"runtime.node.heap.total_heap_size_executable",
	"runtime.node.heap.total_physical_size",
	"runtime.node.heap.used_size.by.space",
	"runtime.node.mem.external",
	"runtime.node.mem.free",
	"runtime.node.mem.heap_total",
	"runtime.node.mem.heap_used",
	"runtime.node.mem.rss",
	"runtime.node.mem.total",
	"runtime.node.process.uptime",
	"runtime.python.cpu.ctx_switch.involuntary",
	"runtime.python.cpu.ctx_switch.voluntary",
	"runtime.python.cpu.percent",
	"runtime.python.cpu.time.sys",
	"runtime.python.cpu.time.user",
	"runtime.python.gc.count.gen0",
	"runtime.python.gc.count.gen1",
	"runtime.python.gc.count.gen2",
	"runtime.python.mem.rss",
	"runtime.python.thread_count",
	"system.cpu.context_switches",
	"system.cpu.guest",
	"system.cpu.guest.total",
	"system.cpu.guestnice.total",
	"system.cpu.idle",
	"system.cpu.idle.total",
	"system.cpu.interrupt",
	"system.cpu.iowait",
	"system.cpu.iowait.total",
	"system.cpu.irq.total",
	"system.cpu.nice.total",
	"system.cpu.num_cores",
	"system.cpu.softirq.total",
	"system.cpu.steal.total",
	"system.cpu.stolen",
	"system.cpu.system",
	"system.cpu.system.total",
	"system.cpu.user",
	"system.cpu.user.total",
	"system.disk.free",
	"system.disk.in_use",
	"system.disk.read_time",
	"system.disk.read_time_pct",
	"system.disk.total",
	"system.disk.used",
	"system.disk.utilized",
	"system.disk.write_time",
	"system.disk.write_time_pct",
	"system.fs.file_handles.allocated",
	"system.fs.file_handles.allocated_unused",
	"system.fs.file_handles.in_use",
	"system.fs.file_handles.max",
	"system.fs.file_handles.used",
	"system.fs.inodes.free",
	"system.fs.inodes.in_use",
	"system.fs.inodes.total",
	"system.fs.inodes.used",
	"system.fs.inodes.utilized",
	"system.io.avg_q_sz",
	"system.io.avg_rq_sz",
	"system.io.await",
	"system.io.block_in",
	"system.io.block_out",
	"system.io.r_await",
	"system.io.r_s",
	"system.io.rkb_s",
	"system.io.rrqm_s",
	"system.io.svctm",
	"system.io.util",
	"system.io.w_await",
	"system.io.w_s",
	"system.io.wkb_s",
	"system.io.wrqm_s",
	"system.load.1",
	"system.load.15",
	"system.load.5",
	"system.load.norm.1",
	"system.load.norm.15",
	"system.load.norm.5",
	"system.mem.buffered",
	"system.mem.cached",
	"system.mem.commit_limit",
	"system.mem.committed_as",
	"system.mem.free",
	"system.mem.page_tables",
	"system.mem.pct_usable",
	"system.mem.shared",
	"system.mem.slab",
	"system.mem.slab_reclaimable",
	"system.mem.total",
	"system.mem.usable",
	"system.mem.used",
	"system.net.bytes_rcvd",
	"system.net.bytes_sent",
	"system.net.conntrack.count",
	"system.net.conntrack.expect_max",
	"system.net.conntrack.max",
	"system.net.conntrack.tcp_max_retrans",
	"system.net.conntrack.tcp_timeout_max_retrans",
	"system.net.iface.mtu",
	"system.net.iface.num_rx_queues",
	"system.net.iface.num_tx_queues",
	"system.net.iface.tx_queue_len",
	"system.net.ip.forwarded_datagrams",
	"system.net.ip.fragmentation_creates",
	"system.net.ip.fragmentation_fails",
	"system.net.ip.fragmentation_oks",
	"system.net.ip.in_addr_errors",
	"system.net.ip.in_csum_errors",
	"system.net.ip.in_delivers",
	"system.net.ip.in_discards",
	"system.net.ip.in_header_errors",
	"system.net.ip.in_no_routes",
	"system.net.ip.in_receives",
	"system.net.ip.in_truncated_pkts",
	"system.net.ip.in_unknown_protos",
	"system.net.ip.out_discards",
	"system.net.ip.out_no_routes",
	"system.net.ip.out_requests",
	"system.net.ip.reassembly_fails",
	"system.net.ip.reassembly_oks",
	"system.net.ip.reassembly_overlaps",
	"system.net.ip.reassembly_requests",
	"system.net.ip.reassembly_timeouts",
	"system.net.ip.reverse_path_filter",
	"system.net.packets_in.count",
	"system.net.packets_in.drop",
	"system.net.packets_in.error",
	"system.net.packets_out.count",
	"system.net.packets_out.drop",
	"system.net.packets_out.error",
	"system.net.tcp.abort_on_timeout",
	"system.net.tcp.active_opens",
	"system.net.tcp.attempt_fails",
	"system.net.tcp.backlog_drops",
	"system.net.tcp.current_established",
	"system.net.tcp.established_resets",
	"system.net.tcp.failed_retransmits",
	"system.net.tcp.from_zero_window",
	"system.net.tcp.in_csum_errors",
	"system.net.tcp.in_errors",
	"system.net.tcp.in_segs",
	"system.net.tcp.listen_drops",
	"system.net.tcp.listen_overflows",
	"system.net.tcp.out_resets",
	"system.net.tcp.out_segs",
	"system.net.tcp.passive_opens",
	"system.net.tcp.paws_connection_drops",
	"system.net.tcp.paws_established_drops",
	"system.net.tcp.prune_called",
	"system.net.tcp.prune_ofo_called",
	"system.net.tcp.prune_rcv_drops",
	"system.net.tcp.retrans_segs",
	"system.net.tcp.syn_cookies_failed",
	"system.net.tcp.syn_cookies_recv",
	"system.net.tcp.syn_cookies_sent",
	"system.net.tcp.syn_retrans",
	"system.net.tcp.to_zero_window",
	"system.net.tcp.tw_reused",
	"system.net.udp.in_csum_errors",
	"system.net.udp.in_datagrams",
	"system.net.udp.in_errors",
	"system.net.udp.no_ports",
	"system.net.udp.out_datagrams",
	"system.net.udp.rcv_buf_errors",
	"system.net.udp.snd_buf_errors",
	"system.swap.cached",
	"system.swap.free",
	"system.swap.pct_free",
	"system.swap.swap_in",
	"system.swap.swap_out",
	"system.swap.total",
	"system.swap.used",
	"system.uptime",
}

func init() {
	toolDescription := `Generates a Datadog metrics query string from a natural language question. Input should be a natural language request for metrics.`
	toolInput := "Provide metrics question in natural language"
	toolOutput := "The tool will return the Datadog metrics query retrieved from your question"

	core.RegisterNBAgentFactoryAsTool(DatadogMetricsQueryAgentName, func(accountId string) (core.NBAgent, error) {
		return DatadogMetricsQueryAgent{}, nil
	}, toolDescription, toolInput, toolOutput)
}

type DatadogMetricsQueryAgent struct{}

func (d DatadogMetricsQueryAgent) GetName() string { return DatadogMetricsQueryAgentName }

func (d DatadogMetricsQueryAgent) GetNameAliases() []string { return []string{"Datadog Metrics Query"} }

func (d DatadogMetricsQueryAgent) GetDescription() string {
	return `Generate Datadog metrics query based on natural language question.`
}

func (d DatadogMetricsQueryAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	// Build the metrics string from the package variable
	availableMetrices := "\n\t" + strings.Join(availableDatadogMetrics, "\n\t") + "\n\t"

	availableLabelsForK8s := `container_id, container_name:otel-collector, image_name, image_tag, kube_container_name, kube_deployment, kube_daemon_set, kube_namespace, kube_node
kube_ownerref_kind, kube_ownerref_name, kube_qos, kube_service, pod_name, pod_phase, service, short_image, source`

	instructions := []string{
		"**Analyze User Request:** Carefully analyze the user's request to understand the specific metrics they need.",
		"**Generate Datadog Query:** Construct a valid Datadog metrics query based on the user's request.",
		"**Time Range:** Unless specified, limit the query to the last 1 hour.",
		"**Output:** Return only the Datadog metrics query string with no additional text, formatting, or JSON wrappers.",
		"**LABELS FOR KUBERNETES:**" + availableLabelsForK8s,
	}
	constraints := []string{
		"Always return a valid Datadog metrics query string only.",
		"Do not wrap the query in JSON objects or add any formatting.",
		"Do not ask for any clarification from the user, try to resolve using the available tools.",
		"CRITICAL: NEVER mix comma operators (,) with functional operators (AND, OR, IN, NOT IN) in the same query.",
		"For simple tag filtering, use commas: {tag1:value1,tag2:value2}.",
		"For multiple values of the same tag, use functional syntax: {tag1:value1 AND tag2 IN (val1,val2,val3)}.",
		"For wildcard matching, use asterisk: tag:value* or status_code:5*.",
		"Use 'by {tag}' for grouping, not '.by(tag)' or '.by {tag}'.",
		"Do not use array syntax: tag:[val1,val2]. Use: tag IN (val1,val2).",
		"Do not use Prometheus functions: rate(), increase(), etc.",
		"AVAILABLE METRICS  - " + availableMetrices,
	}
	examples := []core.NBAgentPromptExample{
		{
			Question:    "Show me the CPU usage for pod 'my-app-pod-xyz' in namespace 'default'.",
			Answer:      "avg:kubernetes.cpu.usage.total{pod_name:my-app-pod-xyz,kube_namespace:default} by {pod_name}",
			Explanation: "Uses comma operator for simple AND logic between two tags.",
		},
		{
			Question:    "Show me the CPU usage for pods of 'my-app' in namespace 'default'.",
			Answer:      "avg:kubernetes.cpu.usage.total{pod_name:my-app*,kube_namespace:default} by {pod_name}",
			Explanation: "Uses wildcard matching with comma operator for simple filtering.",
		},
		{
			Question:    "What is the memory usage for service 'my-service'?",
			Answer:      "avg:kubernetes.memory.usage{service:my-service} by {service}",
			Explanation: "Simple single-tag filter.",
		},
		{
			Question:    "What is the memory usage for deployment 'my-service'?",
			Answer:      "avg:kubernetes.memory.usage{kube_deployment:my-service} by {kube_deployment}",
			Explanation: "Simple single-tag filter by deployment.",
		},
		{
			Question:    "Show me memory usage for services api-server, worker-service, and cache-service in production namespace.",
			Answer:      "avg:kubernetes.memory.usage{kube_namespace:production AND service IN (api-server,worker-service,cache-service)} by {service}",
			Explanation: "Uses functional operators (AND with IN) - no commas mixed with functional operators.",
		},
		{
			Question:    "Show me container restarts for deployments web-app, api-gateway, and auth-service.",
			Answer:      "sum:kubernetes.containers.restarts{kube_deployment IN (web-app,api-gateway,auth-service)} by {kube_deployment}",
			Explanation: "Uses IN operator for multiple values - single tag filter so no mixing issue.",
		},
		{
			Question:    "How many network bytes are received by host 'my-host'?",
			Answer:      "sum:system.net.bytes_rcvd{host:my-host} by {host}",
			Explanation: "Simple single-tag filter.",
		},
		{
			Question:    "Show me disk utilization for filesystem '/var/log' on host 'my-host'.",
			Answer:      "avg:system.disk.utilized{host:my-host,device:/var/log} by {host,device}",
			Explanation: "Uses comma operator for simple AND logic between two tags.",
		},
	}
	toolUsage := map[string][]string{
		tools.ToolMetricsList: {
			"Returns List of Metrics Available. Use it to search/list for metrics.",
			"Input: (optional) keyword to filter metrics.",
			"Output: List of Metrics & Details.",
		},
		tools.ToolMetricsLabelsList: {
			"Returns List of Labels Available for given metric.",
			"Input: (required) metric name.",
			"Output: List of Metric Labels.",
		},
	}
	return core.NBAgentPrompt{
		Role:         "an SRE expert in Datadog metrics",
		Instructions: instructions,
		Constraints:  constraints,
		Examples:     examples,
		ToolUsage:    toolUsage,
	}
}

func (d DatadogMetricsQueryAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{
		tools.MetricsListTool{
			Provider: "datadog",
		},
		tools.ListMetricsLabelsTool{
			Provider: "datadog",
		},
	}
}

func (l DatadogMetricsQueryAgent) CritiqueEnabled() bool {
	return false
}

func (d DatadogMetricsQueryAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

func (d DatadogMetricsQueryAgent) UpdateExecutorLlmResponse(actions []core.NBAgentPlannerToolAction, finished *core.NBAgentPlannerFinishAction, err error) ([]core.NBAgentPlannerToolAction, *core.NBAgentPlannerFinishAction, error) {
	if finished != nil && finished.Data != "" {
		query := finished.Data

		// Validate all metrics in the query (handles complex queries with arithmetic)
		invalidMetric := validateDatadogQuery(query)

		if invalidMetric != "" {
			if invalidMetric == "no_metrics_found" {
				// Query doesn't contain any recognizable metrics
				return actions, nil, fmt.Errorf("query does not contain valid metric format (expected: aggregation:metric_name). Please use the metrics_list tool to find valid metrics")
			}
			// Return error to force retry with valid metric
			return actions, nil, fmt.Errorf("invalid metric '%s' used in query. This metric is not in the available metrics list. Please use the metrics_list tool to find a valid metric", invalidMetric)
		}
	}
	return actions, finished, err
}

// extractAllMetricNames extracts all metric names from a Datadog query
// Handles complex queries with multiple metrics like: avg:cpu / avg:memory
func extractAllMetricNames(query string) []string {
	query = strings.TrimSpace(query)

	// Pattern to match ONLY known aggregation prefixes followed by metric names
	// This avoids matching tag key:value pairs like pod_name:my.app.v1.2
	// Matches: avg:metric, sum:metric, min:metric, max:metric, count:metric, etc.
	re := regexp.MustCompile(`(avg|sum|min|max|count|top|percentile|last|stddev|p50|p75|p90|p95|p99):([a-zA-Z0-9_\.]+)`)
	matches := re.FindAllStringSubmatch(query, -1)

	// Extract all unique metric names
	// match[0] = full match (e.g., "avg:kubernetes.cpu.usage.total")
	// match[1] = aggregation prefix (e.g., "avg")
	// match[2] = metric name (e.g., "kubernetes.cpu.usage.total")
	metricNames := []string{}
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 2 {
			metricName := match[2]
			if !seen[metricName] {
				metricNames = append(metricNames, metricName)
				seen[metricName] = true
			}
		}
	}

	return metricNames
}

// isValidDatadogMetric checks if the metric name is in the available metrics list
func isValidDatadogMetric(metricName string) bool {
	for _, metric := range availableDatadogMetrics {
		if metric == metricName {
			return true
		}
	}

	return false
}

// validateDatadogQuery validates that all metrics in a query are valid
// Returns invalid metric name if validation fails, empty string if all valid
func validateDatadogQuery(query string) string {
	metricNames := extractAllMetricNames(query)

	// If no metrics found, might be a malformed query
	if len(metricNames) == 0 {
		return "no_metrics_found"
	}

	// Check each metric
	for _, metricName := range metricNames {
		if !isValidDatadogMetric(metricName) {
			return metricName // Return first invalid metric
		}
	}

	return "" // All metrics are valid
}
