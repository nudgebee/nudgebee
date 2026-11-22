package observability

import (
	"fmt"
	"math"
	"sort"
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
		switch UtilisationKey(k) {
		case KeyCPUUsage, KeyCPUUsageLine:
			queries[k] = "k8s.node.cpu.usage.seconds.rate"
		case KeyMemoryUsage, KeyMemoryUsageLine:
			queries[k] = "k8s.node.memory.working_set"
		case KeyCPUAllocatable:
			queries[k] = "k8s.node.cpu.allocatable"
		case KeyMemoryAllocatable:
			queries[k] = "k8s.node.memory.allocatable"
		case KeyDiskUsed:
			queries[k] = "k8s.node.fs.usage"
			// cpu_request, memory_request, cpu_limit, memory_limit:
			//   No per-node rollup metric exists; they are summed from the container
			//   specs instead (buildSolarWindsNodePlans).
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
		switch UtilisationKey(k) {
		// Pod-level OTel metrics are used for all workload kinds (deployment, statefulset, pod).
		// When filtered by k8s.deployment.name / k8s.statefulset.name and grouped by that key,
		// SWO aggregates across all pods belonging to the workload.
		case KeyCPUUsage, KeyCPUUsageLine:
			queries[k] = "k8s.pod.cpu.usage.seconds.rate"
		case KeyMemoryUsage, KeyMemoryUsageLine:
			queries[k] = "k8s.pod.memory.working_set"
		// Spec (request/limit) metrics use the container-level OTel variants which are generated
		// by Kubernetes State Metrics and do carry deployment hierarchy attributes in SWO.
		case KeyCPURequest:
			queries[k] = "k8s.container.spec.cpu.requests"
		case KeyMemoryRequest:
			queries[k] = "k8s.container.spec.memory.requests"
		case KeyCPULimit:
			queries[k] = "k8s.container.spec.cpu.limit"
		case KeyMemoryLimit:
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
		switch UtilisationKey(k) {
		case KeyP90CPU, KeyP50CPU, KeyMaxUsageCPU, KeyCPUUsageLine:
			queries[k] = "k8s.node.cpu.usage.seconds.rate"
		case KeyP90Mem, KeyP50Mem, KeyMaxUsageMem, KeyMemoryUsageLine:
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
		switch UtilisationKey(k) {
		case KeyCPURequest:
			queries[k] = "k8s.cluster.spec.cpu.requests"
		case KeyMemoryRequest:
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
		switch UtilisationKey(k) {
		case KeyCPUReal, KeyCPUUsageTrend:
			// Actual CPU usage summed across all cluster nodes. The trend key is the same
			// total, kept as a series for the utilisation sparkline.
			queries[k] = "k8s.node.cpu.usage.seconds.rate"
		case KeyMemReal, KeyMemUsageTrend:
			// Actual memory working-set summed across all cluster nodes.
			queries[k] = "k8s.node.memory.working_set"
		case KeyCPUTotal:
			// Allocatable CPU summed across all cluster nodes.
			queries[k] = "k8s.node.cpu.allocatable"
		case KeyMemTotal:
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
		} else if meta.Kind != "node" {
			// A workload named without a kind SolarWinds tags (or with none) must still be
			// scoped to that workload: dropping the clause read the whole namespace.
			name := swEscapeValue(meta.Name)
			parts = append(parts, fmt.Sprintf("(k8s.deployment.name: [%s] OR k8s.statefulset.name: [%s] OR k8s.daemonset.name: [%s])", name, name, name))
		}
	}
	if meta.PodNamesSupplied && meta.Kind != "node" {
		parts = append(parts, swPodNamesClause(meta.PodNames))
	}
	// SWO measurements API uses a single space between filter clauses (consistent with
	// buildSwTraceFilter in solarwinds_traces.go). " AND " is not a valid separator.
	return strings.Join(parts, " ")
}

// swNoPodsSentinel is a pod name Kubernetes cannot produce (RFC1123 forbids
// underscores) -- the same literal the Prometheus and Datadog builders use. Verified
// against the live API: the filter is accepted and matches nothing.
const swNoPodsSentinel = "__no_pods_resolved__"

// swPodNamesClause scopes a query to the caller's exact pod list.
//
// Unquoted values are exact on SWO metrics, and a bracket list is an OR. An empty
// list is an answer -- the caller resolved the pods and found none -- so it must
// match nothing rather than fall back to every pod of the workload. Pod names need
// no escaping: RFC1123 allows none of SWO's filter metacharacters.
func swPodNamesClause(pods []string) string {
	if len(pods) == 0 {
		return fmt.Sprintf("k8s.pod.name: [%s]", swNoPodsSentinel)
	}
	return fmt.Sprintf("k8s.pod.name: [%s]", strings.Join(pods, ","))
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
//     "attr:val*" term inside one "(… OR …)" group. Wildcards match nothing inside a
//     bracket list, and SWO reads terms joined by a space as AND, not OR.
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
		parts := make([]string, len(cleaned))
		for i, v := range cleaned {
			parts[i] = fmt.Sprintf("%s:%s", attribute, v)
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return "(" + strings.Join(parts, " OR ") + ")"
	}
	// All exact values: use multi-value bracket syntax.
	return fmt.Sprintf("%s: [%s]", attribute, strings.Join(cleaned, ","))
}

// swKindToAttrKey maps a Kubernetes resource kind to the SWO attribute key used in filters
// and groupBy expressions.
func swKindToAttrKey(kind string) string {
	// The UI sends Kubernetes kinds as written in manifests ("Deployment"); a case-
	// sensitive match dropped the workload filter and charted the whole namespace.
	switch strings.ToLower(strings.TrimSpace(kind)) {
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
//
// The workload attribute is grouped on only when a workload is named. A kind with no
// name scopes nothing, so grouping on it split a namespace-wide answer into one series
// per deployment: cpu_usage_line for a namespace on one node came back as 3 series, a
// "summed into one series" key fanning out under every payload[0] reader.
func buildSolarWindsGroupBy(meta RequestMetadata) string {
	if meta.Kind == "node" {
		return "k8s.node.name"
	}
	parts := []string{"k8s.namespace.name"}
	if attrKey := swKindToAttrKey(meta.Kind); attrKey != "" && meta.Name != "" {
		parts = append(parts, attrKey)
	}
	return strings.Join(parts, ",")
}

// buildSolarWindsRequestParams builds the freeform Request map passed as FetchMetricsRequest.Request
// when routing utilization queries through the SolarWinds MetricSource. groupBy and aggregateBy are
// passed as arguments: groupBy because cluster-level callers use two different groupBy values across
// two separate batch calls; aggregateBy because node queries use "AVG" (per-node average) while
// workload queries use "SUM" (total across all pods belonging to the workload).
//
// preGroupBy names the entity a SUM adds up. The source answers SUM + preGroupBy as a
// time-weighted total (see swTimeWeightedSum): plain SUM read 5x the true total at 6h and
// 28x at 24h. Empty for an AVG over one entity's own series, which needs no pre-grouping.
func buildSolarWindsRequestParams(meta RequestMetadata, groupBy, aggregateBy, preGroupBy string) map[string]any {
	r := map[string]any{"aggregateBy": aggregateBy}
	if filter := buildSolarWindsFilter(meta); filter != "" {
		r["filter"] = filter
	}
	if groupBy != "" {
		r["groupBy"] = groupBy
	}
	if preGroupBy != "" {
		r["preGroupBy"] = preGroupBy
		r["preGroupByMethod"] = "AVG"
	}
	return r
}

// The entities a utilisation SUM adds up.
const (
	// swEntityPod: pod usage metrics carry one series per pod.
	swEntityPod = "k8s.pod.name"
	// swEntityContainer: container spec metrics carry one series per container, so a
	// multi-container pod holds more samples per bucket than its pod name alone accounts
	// for: counted per pod, requests read 0.965 of the truth on a cluster with sidecars.
	swEntityContainer = "k8s.pod.name,k8s.container.name"
	// swEntityNode: node metrics carry one series per node.
	swEntityNode = "k8s.node.name"
)

// swWorkloadEntity is the entity a workload key's metric is reported per.
func swWorkloadEntity(metric string) string {
	if strings.HasPrefix(metric, "k8s.container.spec.") {
		return swEntityContainer
	}
	return swEntityPod
}

// swUtilisationBatch is one measurements call: the keys that share a request.
type swUtilisationBatch struct {
	queries map[string]string
	request map[string]any
}

// swWorkloadBatches splits the workload keys by the entity their SUM must pre-group
// by. SWO takes preGroupBy per request, not per metric, so usage and spec keys
// cannot share a call.
func swWorkloadBatches(meta RequestMetadata, queries map[string]string) []swUtilisationBatch {
	byEntity := map[string]map[string]string{}
	var order []string
	for key, metric := range queries {
		entity := swWorkloadEntity(metric)
		if byEntity[entity] == nil {
			byEntity[entity] = map[string]string{}
			order = append(order, entity)
		}
		byEntity[entity][key] = metric
	}
	batches := make([]swUtilisationBatch, 0, len(order))
	for _, entity := range order {
		batches = append(batches, swUtilisationBatch{
			queries: byEntity[entity],
			request: buildSolarWindsRequestParams(meta, buildSolarWindsGroupBy(meta), "SUM", entity),
		})
	}
	return batches
}

// fetchSolarWindsWorkloadUtilisation runs one measurements call per entity batch
// (see swWorkloadBatches) and merges the results.
func fetchSolarWindsWorkloadUtilisation(
	ctx *security.RequestContext,
	req GetUtilisationTrendRequest,
	metricsProvider, integrationSource string,
	meta RequestMetadata,
	queries map[string]string,
	instant bool,
) (OutputMetricQuery, error) {
	return fetchSolarWindsBatches(ctx, req, metricsProvider, integrationSource, swWorkloadBatches(meta, queries), instant)
}

// fetchSolarWindsBatches runs one measurements call per batch and merges the results.
func fetchSolarWindsBatches(
	ctx *security.RequestContext,
	req GetUtilisationTrendRequest,
	metricsProvider, integrationSource string,
	batches []swUtilisationBatch,
	instant bool,
) (OutputMetricQuery, error) {
	merged := OutputMetricQuery{Results: []QueryResult{}}
	for _, batch := range batches {
		out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:            req.AccountId,
			MetricProvider:       metricsProvider,
			MetricProviderSource: integrationSource,
			Queries:              batch.queries,
			StartTime:            req.StartTime,
			EndTime:              req.EndTime,
			StepInterval:         req.StepInterval,
			Instant:              instant,
			Request:              batch.request,
		})
		if err != nil {
			return out, err
		}
		merged.Results = append(merged.Results, out.Results...)
	}
	return merged, nil
}

// --- Keys that need a request shape of their own ---------------------------------
//
// The keys above share one call per scope: the same filter, one series for the
// workload (or node), SUM or AVG. The keys below each need something that call does not
// have -- a per-container or per-claim grouping, a pod-name or claim filter, MAX across
// pods, a phase filter -- so each carries its own request (swPlan), and keys whose
// requests are identical still share a call (swPlanBatches).
//
// Every metric here was confirmed against a live SWO org, units included. Keys SWO has
// no equivalent for are left out, and completeUtilisationResults answers them with a
// note; see the "Not answered" list at the end of this section.

// Groupings, in the canonical label order of each family.
const (
	// swGroupPerContainer is the rightsizing family's identity: {namespace, pod, container}.
	swGroupPerContainer = "k8s.namespace.name,k8s.pod.name,k8s.container.name"
	// swGroupPerPod is one series per pod, for the per-pod usage charts.
	swGroupPerPod = "k8s.namespace.name,k8s.pod.name"
	// swGroupPerClaim is one series per claim. A claim name is unique only within its
	// namespace, so both are the identity.
	swGroupPerClaim = "k8s.namespace.name,k8s.persistentvolumeclaim.name"
)

// swPlan is one key's measurements request.
type swPlan struct {
	metric  string
	request map[string]any
	// perContainer drops the series SWO could not attribute to a named container.
	// cAdvisor reports a series with an empty container name beside the real ones
	// (measured: 67 MB working set next to a 133 MB container, on a one-container pod);
	// the family is keyed on the container, so it cannot be reported under one.
	perContainer bool
	// negate reports the value with its sign flipped: transmit traffic is negative on
	// every provider, so receive and transmit chart as a mirror.
	negate bool
	// dropEndMarker drops the 0 SWO writes as a gauge's last reading when its pod goes
	// away. Measured over 6h of one namespace: 91 of 177 container-spec series and 25 of
	// 202 working-set series ended in exactly one 0, and none held a 0 anywhere else. A
	// request or a working set of 0 is not a reading, and it broke the units check and
	// pulled every min and avg toward zero. Rates are exempt: a CPU rate of 0 is real,
	// and SWO writes no such marker for them.
	dropEndMarker bool
}

// swPerContainerMetrics are the rightsizing keys: one series per container.
//
// Usage is SWO's per-container CPU rate, in cores (it reads exactly the pod rate on a
// one-container pod). Memory comes from the cAdvisor gauges SWO's collector ships
// (k8s.container_memory_*_bytes): SWO has no per-container working-set metric of its
// own, and these carry the full workload hierarchy. Requests and limits are the
// kube-state-metrics specs, in cores and bytes.
var swPerContainerMetrics = map[UtilisationKey]string{
	KeyContainerCPUUsageByPod:         "k8s.container.cpu.usage.seconds.rate",
	KeyContainerMemoryWorkingSetByPod: "k8s.container_memory_working_set_bytes",
	KeyContainerMemoryMaxUsageByPod:   "k8s.container_memory_max_usage_bytes",
	KeyContainerCPURequestByPod:       "k8s.container.spec.cpu.requests",
	KeyContainerCPULimitByPod:         "k8s.container.spec.cpu.limit",
	KeyContainerMemoryRequestByPod:    "k8s.container.spec.memory.requests",
	KeyContainerMemoryLimitByPod:      "k8s.container.spec.memory.limit",
}

// swReplicaMetrics names each controller's desired and ready replica counts, from
// kube-state-metrics. A DaemonSet has no replica count; its desired and ready scheduled
// pods are the same quantity, as on Prometheus.
var swReplicaMetrics = map[string][2]string{
	"k8s.deployment.name":  {"k8s.kube_deployment_spec_replicas", "k8s.kube_deployment_status_replicas_ready"},
	"k8s.statefulset.name": {"k8s.kube_statefulset_replicas", "k8s.kube_statefulset_status_replicas_ready"},
	"k8s.daemonset.name":   {"k8s.kube_daemonset_status_desired_number_scheduled", "k8s.kube_daemonset_status_number_ready"},
}

// swAvgPlan is one series per group: each group is one entity's own series, so AVG over
// the bucket is its value and nothing is added up.
func swAvgPlan(metric, filter, groupBy string) swPlan {
	r := map[string]any{"aggregateBy": "AVG", "groupBy": groupBy}
	if filter != "" {
		r["filter"] = filter
	}
	return swPlan{metric: metric, request: r}
}

// swSumPlan is a time-weighted total across the entities preGroupBy names.
func swSumPlan(metric, filter, groupBy, entity string) swPlan {
	r := map[string]any{"aggregateBy": "SUM", "preGroupBy": entity, "preGroupByMethod": "AVG"}
	if filter != "" {
		r["filter"] = filter
	}
	if groupBy != "" {
		r["groupBy"] = groupBy
	}
	return swPlan{metric: metric, request: r}
}

// swPerContainerPlan renders one rightsizing key for the scope filter, narrowed to one
// container when the caller names it (the shared call cannot be, since its pod-level
// metrics carry no container).
func swPerContainerPlan(metric, scopeFilter string, meta RequestMetadata) swPlan {
	filter := scopeFilter
	if meta.ContainerName != "" {
		filter = swJoinFilters(filter, fmt.Sprintf("k8s.container.name: [%s]", swEscapeValue(meta.ContainerName)))
	}
	p := swAvgPlan(metric, filter, swGroupPerContainer)
	p.perContainer = true
	p.dropEndMarker = !strings.HasSuffix(metric, ".rate")
	if strings.HasPrefix(metric, "k8s.container.spec.") {
		// A spec is constant per container, so the bucket's MAX is its value -- and,
		// unlike the AVG, it is not dragged down by the end marker sharing the pod's
		// last bucket: at 6h an AVG read a 0.254-core request as 0.169 there.
		p.request["aggregateBy"] = "MAX"
	}
	return p
}

// swPVCFilter scopes the claim keys as Prometheus does: the namespace, then one claim
// when named, else the claims whose names start with the workload's (a StatefulSet's
// claims are <template>-<name>-<ordinal>... and Prometheus matches "<name>.*").
func swPVCFilter(meta RequestMetadata) string {
	var parts []string
	if meta.Namespace != "" {
		parts = append(parts, fmt.Sprintf("k8s.namespace.name: [%s]", swEscapeValue(meta.Namespace)))
		switch {
		case meta.PVCName != "":
			parts = append(parts, fmt.Sprintf("k8s.persistentvolumeclaim.name: [%s]", swEscapeValue(meta.PVCName)))
		case meta.Name != "":
			parts = append(parts, "k8s.persistentvolumeclaim.name:"+swEscapeValue(meta.Name)+"*")
		}
	}
	return strings.Join(parts, " ")
}

// buildSolarWindsPVCPlans renders the claim keys, which belong to a namespace rather
// than a workload: volume rightsizing asks for every claim in the cluster, with no kind.
func buildSolarWindsPVCPlans(meta RequestMetadata, metrics []string) map[string]swPlan {
	plans := map[string]swPlan{}
	filter := swPVCFilter(meta)
	sumGroup := ""
	if meta.Namespace != "" {
		sumGroup = "k8s.namespace.name"
	}
	for _, k := range metrics {
		switch UtilisationKey(k) {
		case KeyPVCUsedByClaim:
			plans[k] = swAvgPlan("k8s.kubelet_volume_stats_used_bytes", filter, swGroupPerClaim)
		case KeyPVCCapacityByClaim:
			// The kubelet's capacity, not the claim's request: what the volume actually
			// has is what a resize decision compares usage against.
			plans[k] = swAvgPlan("k8s.kubelet_volume_stats_capacity_bytes", filter, swGroupPerClaim)
		case KeyPVCUsage:
			plans[k] = swSumPlan("k8s.kubelet_volume_stats_used_bytes", filter, sumGroup, swGroupPerClaim)
		case KeyPVCRequests:
			plans[k] = swSumPlan("k8s.kube_persistentvolumeclaim_resource_requests_storage_bytes", filter, sumGroup, swGroupPerClaim)
		}
	}
	return plans
}

// buildSolarWindsWorkloadPlans renders the workload-scope keys that need their own
// request. The shared call's filter scopes them, so pod_names narrows them too.
func buildSolarWindsWorkloadPlans(meta RequestMetadata, metrics []string) map[string]swPlan {
	plans := buildSolarWindsPVCPlans(meta, metrics)
	if meta.Namespace == "" && meta.Name == "" {
		return plans
	}
	filter := buildSolarWindsFilter(meta)
	group := buildSolarWindsGroupBy(meta)
	for _, k := range metrics {
		key := UtilisationKey(k)
		if metric, ok := swPerContainerMetrics[key]; ok {
			plans[k] = swPerContainerPlan(metric, filter, meta)
			continue
		}
		switch key {
		case KeyCPUUsageByPod:
			plans[k] = swAvgPlan("k8s.pod.cpu.usage.seconds.rate", filter, swGroupPerPod)
		case KeyMemoryUsageByPod:
			p := swAvgPlan("k8s.pod.memory.working_set", filter, swGroupPerPod)
			p.dropEndMarker = true
			plans[k] = p
		// The busiest pod: each pod's own average in the bucket, then the MAX across pods.
		case KeyCPUUsageMaxPod:
			plans[k] = swMaxPerPodPlan("k8s.pod.cpu.usage.seconds.rate", filter, group)
		case KeyMemoryUsageMaxPod:
			plans[k] = swMaxPerPodPlan("k8s.pod.memory.working_set", filter, group)
		// Running pods, time-weighted: kube-state-metrics reports 1 per pod in its
		// current phase, so a pod that ran for half a bucket counts one half.
		case KeyPodCount:
			plans[k] = swSumPlan("k8s.kube_pod_status_phase", swJoinFilters(filter, "phase:Running"), group, swEntityPod)
		// Byte rates, per second, summed across the workload's pods.
		case KeyDiskReadBytes:
			plans[k] = swSumPlan("k8s.pod.fs.reads.bytes.rate", filter, group, swEntityPod)
		case KeyDiskWriteBytes:
			plans[k] = swSumPlan("k8s.pod.fs.writes.bytes.rate", filter, group, swEntityPod)
		case KeyNetworkReceivePacket:
			plans[k] = swSumPlan("k8s.pod.network.bytes_received", filter, group, swEntityPod)
		case KeyNetworkTransmitPackets:
			p := swSumPlan("k8s.pod.network.bytes_transmitted", filter, group, swEntityPod)
			p.negate = true
			plans[k] = p
		case KeyReplicaDefined, KeyReplicaReady:
			if p, ok := swReplicaPlan(meta, key == KeyReplicaReady); ok {
				plans[k] = p
			}
		// The *_pod keys name ONE pod: the investigate card sends the event's pod as the
		// workload name, with no controller kind. Usage is that pod's total; request and
		// limit are per container. A controller kind says the name is not a pod, so the
		// keys are left unanswered there rather than answered empty.
		case KeyCPUUsagePod, KeyCPURequestPod, KeyCPULimitPod:
			if meta.Name == "" || swNamesController(meta.Kind) {
				continue
			}
			podFilter := swJoinFilters(fmt.Sprintf("k8s.namespace.name: [%s]", swEscapeValue(meta.Namespace)),
				fmt.Sprintf("k8s.pod.name: [%s]", swEscapeValue(meta.Name)))
			switch key {
			case KeyCPUUsagePod:
				plans[k] = swSumPlan("k8s.pod.cpu.usage.seconds.rate", podFilter, swGroupPerPod, swEntityPod)
			case KeyCPURequestPod:
				plans[k] = swPerContainerPlan("k8s.container.spec.cpu.requests", podFilter, RequestMetadata{})
			case KeyCPULimitPod:
				plans[k] = swPerContainerPlan("k8s.container.spec.cpu.limit", podFilter, RequestMetadata{})
			}
		}
	}
	return plans
}

// swNamesController reports whether kind names a workload controller, so the name is
// the controller's and not a pod's.
func swNamesController(kind string) bool {
	attr := swKindToAttrKey(kind)
	return attr != "" && attr != "k8s.pod.name"
}

// swMaxPerPodPlan is the busiest pod in each bucket.
func swMaxPerPodPlan(metric, filter, groupBy string) swPlan {
	r := map[string]any{"aggregateBy": "MAX", "preGroupBy": swEntityPod, "preGroupByMethod": "AVG", "groupBy": groupBy}
	if filter != "" {
		r["filter"] = filter
	}
	return swPlan{metric: metric, request: r}
}

// swReplicaPlan reads the controller's own replica counts. MAX, not SUM: the filter
// names one workload, so every series it matches carries the same number. A kind with
// no controller object (a pod, a node) has no replica count; any other kind SolarWinds
// does not tag is read as a Deployment, as Prometheus reads it.
func swReplicaPlan(meta RequestMetadata, ready bool) (swPlan, bool) {
	if meta.Name == "" || meta.Namespace == "" {
		return swPlan{}, false
	}
	attr := swKindToAttrKey(meta.Kind)
	switch attr {
	case "k8s.pod.name":
		return swPlan{}, false
	case "":
		if strings.EqualFold(strings.TrimSpace(meta.Kind), "node") {
			return swPlan{}, false
		}
		attr = "k8s.deployment.name"
	}
	// A kind with no replica metric here (e.g. one swKindToAttrKey learns later) is
	// unanswered, never a request with an empty metric name.
	pair, ok := swReplicaMetrics[attr]
	if !ok {
		return swPlan{}, false
	}
	metric := pair[0]
	if ready {
		metric = pair[1]
	}
	filter := fmt.Sprintf("k8s.namespace.name: [%s] %s: [%s]", swEscapeValue(meta.Namespace), attr, swEscapeValue(meta.Name))
	return swPlan{metric: metric, request: map[string]any{
		"aggregateBy": "MAX", "filter": filter, "groupBy": "k8s.namespace.name," + attr,
	}}, true
}

// buildSolarWindsNodePlans renders the node-scope keys that need their own request.
// Only active_nodes answers with no node named: the node trends page asks for it with
// kind "node" and nothing else, meaning every node.
func buildSolarWindsNodePlans(meta RequestMetadata, metrics []string) map[string]swPlan {
	plans := map[string]swPlan{}
	filter := swBuildMultiValueFilter("k8s.node.name", meta.NodeName)
	for _, k := range metrics {
		key := UtilisationKey(k)
		if key == KeyActiveNodes {
			// One series per node, value 1, as Prometheus's kube_node_info.
			plans[k] = swAvgPlan("k8s.kube_node_info", filter, swEntityNode)
			continue
		}
		if filter == "" {
			continue
		}
		if metric, ok := swPerContainerMetrics[key]; ok {
			plans[k] = swPerContainerPlan(metric, filter, meta)
			continue
		}
		switch key {
		// What the node's pods asked for, summed from their containers' specs, one
		// series per node like the node usage keys.
		case KeyCPURequest:
			plans[k] = swSumPlan("k8s.container.spec.cpu.requests", filter, swEntityNode, swEntityContainer)
		case KeyCPULimit:
			plans[k] = swSumPlan("k8s.container.spec.cpu.limit", filter, swEntityNode, swEntityContainer)
		case KeyMemoryRequest:
			plans[k] = swSumPlan("k8s.container.spec.memory.requests", filter, swEntityNode, swEntityContainer)
		case KeyMemoryLimit:
			plans[k] = swSumPlan("k8s.container.spec.memory.limit", filter, swEntityNode, swEntityContainer)
		}
	}
	return plans
}

// buildSolarWindsClusterPlans renders the cluster-scope keys that need their own request:
// the claim keys, which volume rightsizing asks for with no kind and no namespace.
func buildSolarWindsClusterPlans(metrics []string) map[string]swPlan {
	return buildSolarWindsPVCPlans(RequestMetadata{}, metrics)
}

// Not answered, each measured against a live SWO org:
//   - HTTP keys (http_*, workload_http_*): SWO keeps OBI's http.server.request.duration
//     as a cumulative histogram and the measurements API returns neither its count nor
//     its sum. composite.http.server.request.request_rate is count_over_time over that
//     histogram -- data points, not requests: it read exactly (series count)/60 in every
//     1-minute bucket whatever the traffic.
//   - cpu_throttling: k8s.container_cpu_cfs_throttled_seconds_total is a cumulative
//     counter and SWO offers no rate over it.
//   - node and cluster network (packets at node scope, *_bytes_increase): SWO's node
//     network metrics sum the node's pods; they read 20x the node's NIC (ens3).
//   - disk_total: no filesystem capacity metric.
//   - container_oomkilled_limit_by_pod: needs a join SWO cannot express.
//   - network_usage: its definition is the node agent's TCP connection time.
//   - no_of_pods and the other node-pool keys: Karpenter metrics.

// swPlanQueries is the key -> metric map of plans, for the rendered-keys bookkeeping.
func swPlanQueries(plans map[string]swPlan) map[string]string {
	out := make(map[string]string, len(plans))
	for k, p := range plans {
		out[k] = p.metric
	}
	return out
}

// swPlanBatches groups plans whose requests are identical into one call each, in a
// stable order.
func swPlanBatches(plans map[string]swPlan) []swUtilisationBatch {
	keys := make([]string, 0, len(plans))
	for k := range plans {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	index := map[string]int{}
	var batches []swUtilisationBatch
	for _, k := range keys {
		p := plans[k]
		sig := swRequestSignature(p.request)
		i, ok := index[sig]
		if !ok {
			i = len(batches)
			index[sig] = i
			batches = append(batches, swUtilisationBatch{queries: map[string]string{}, request: p.request})
		}
		batches[i].queries[k] = p.metric
	}
	return batches
}

// swRequestSignature is a request map rendered in key order.
func swRequestSignature(r map[string]any) string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v\x00", k, r[k])
	}
	return b.String()
}

// fetchSolarWindsPlans fetches the plans and applies their post-processing.
func fetchSolarWindsPlans(
	ctx *security.RequestContext,
	req GetUtilisationTrendRequest,
	metricsProvider, integrationSource string,
	plans map[string]swPlan,
	instant bool,
) (OutputMetricQuery, error) {
	if len(plans) == 0 {
		return OutputMetricQuery{Results: []QueryResult{}}, nil
	}
	out, err := fetchSolarWindsBatches(ctx, req, metricsProvider, integrationSource, swPlanBatches(plans), instant)
	if err != nil {
		return out, err
	}
	swApplyPlans(out.Results, plans)
	return out, nil
}

// swMergePlans fetches the plans and appends their results to out, recording each plan's
// metric in rendered so a key that came back with no entry is still known as rendered.
func swMergePlans(
	ctx *security.RequestContext,
	req GetUtilisationTrendRequest,
	metricsProvider, integrationSource string,
	out OutputMetricQuery,
	rendered map[string]string,
	plans map[string]swPlan,
	instant bool,
) (OutputMetricQuery, error) {
	planOut, err := fetchSolarWindsPlans(ctx, req, metricsProvider, integrationSource, plans, instant)
	if err != nil {
		return planOut, err
	}
	for k, metric := range swPlanQueries(plans) {
		rendered[k] = metric
	}
	out.Results = append(out.Results, planOut.Results...)
	return out, nil
}

// swApplyPlans applies each plan's post-processing to its key's result, in place.
func swApplyPlans(results []QueryResult, plans map[string]swPlan) {
	for i := range results {
		p, ok := plans[results[i].QueryKey]
		if !ok {
			continue
		}
		if p.perContainer {
			kept := results[i].Payload[:0]
			for _, s := range results[i].Payload {
				if c := s.Metric["container"]; c != "" && c != "POD" {
					kept = append(kept, s)
				}
			}
			results[i].Payload = kept
		}
		if p.dropEndMarker {
			kept := results[i].Payload[:0]
			for _, s := range results[i].Payload {
				if n := len(s.Values); n > 0 && s.Values[n-1] == 0 {
					s.Values, s.Timestamps = s.Values[:n-1], s.Timestamps[:min(n-1, len(s.Timestamps))]
				}
				// A series that held only the marker is a pod that was already gone.
				if len(s.Values) > 0 {
					kept = append(kept, s)
				}
			}
			results[i].Payload = kept
		}
		if p.negate {
			for _, s := range results[i].Payload {
				for j := range s.Values {
					s.Values[j] = -s.Values[j]
				}
			}
		}
	}
}

// solarwindsCollapseOverTime reduces each series to one point of the requested
// aggregate, over time.
//
// SWO's own whole-window answer (seriesType=SCALAR) cannot stand in: asked for MAX it
// returned the busiest single node, not the peak of the cluster total. So the range is
// fetched and reduced here, with the same arithmetic as Datadog and OpenObserve.
//
// `count` converts SWO buckets into the contract's unit -- Step-wide intervals with
// data (metrics_aggregate_count.go). Each reading stands for swReadingSeconds of
// observation.
func solarwindsCollapseOverTime(results []QueryResult, meta RequestMetadata) {
	if meta.Aggregate.Name == "" {
		return
	}
	step := aggregateStepSeconds(meta)
	for i := range results {
		width := swReadingSeconds(results[i])
		for j := range results[i].Payload {
			series := &results[i].Payload[j]
			if len(series.Timestamps) == 0 {
				continue
			}
			value, ok := reduceOverTime(series.Values, meta.Aggregate)
			switch {
			case !ok:
				// Every sample was a gap. Still one point, carrying NaN (null) rather
				// than a zero that would read as idle -- as Datadog does.
				value = math.NaN()
			case meta.Aggregate.Name == AggregateCount:
				value = stepEquivalentCount(value, width, step)
			}
			last := series.Timestamps[len(series.Timestamps)-1]
			series.Timestamps, series.Values = []int64{last}, []float64{value}
		}
	}
}

// swReadingSeconds is how much observation one reading in the result stands for: the
// bucket width SWO reported, or the typical (median) gap between consecutive readings
// when that is longer. The two differ for windows of 15 minutes or less, which SWO
// answers in 1-second buckets that each hold one scrape: there a reading covers the
// scrape interval, not one second. With no reported width and no two readings to
// compare, 0, which stepEquivalentCount reads as "do not convert".
func swReadingSeconds(result QueryResult) float64 {
	var gaps []int64
	for _, series := range result.Payload {
		for k := 1; k < len(series.Timestamps); k++ {
			if gap := series.Timestamps[k] - series.Timestamps[k-1]; gap > 0 {
				gaps = append(gaps, gap)
			}
		}
	}
	var typical int64
	if len(gaps) > 0 {
		sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
		typical = gaps[len(gaps)/2]
	}
	if result.bucketSeconds > typical {
		return float64(result.bucketSeconds)
	}
	return float64(typical)
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
//   - Sum call: node-level metrics summed across all nodes + no groupBy (SUM over nodes,
//     time-weighted by the source)
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
				switch UtilisationKey(qr.QueryKey) {
				case KeyP90CPU, KeyP90Mem:
					pctOut.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 0.90)}
				case KeyP50CPU, KeyP50Mem:
					pctOut.Results[i].Payload = []Result{computePercentileFromSeries(qr.Payload, 0.50)}
				case KeyMaxUsageCPU, KeyMaxUsageMem:
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
			Request:              buildSolarWindsRequestParams(RequestMetadata{}, "", "SUM", swEntityNode),
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			merged.Results = append(merged.Results, sumOut.Results...)
		}
	}

	// --- 4. Keys with a request of their own (per-claim volumes, active nodes) ---
	if plans := buildSolarWindsClusterPlans(meta.RequestedMetrics); len(plans) > 0 {
		planOut, err := fetchSolarWindsPlans(ctx, req, metricsProvider, integrationSource, plans, instant)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			merged.Results = append(merged.Results, planOut.Results...)
		}
	}

	// Return partial results when at least one call succeeded; only propagate error when
	// all calls failed and there is nothing to show.
	if len(merged.Results) == 0 && firstErr != nil {
		return OutputMetricQuery{}, firstErr
	}
	return merged, nil
}
