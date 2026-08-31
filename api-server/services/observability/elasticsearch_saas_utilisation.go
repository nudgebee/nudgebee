package observability

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"nudgebee/services/security"
)

// Utilisation metrics over Elasticsearch, across every schema a customer's cluster
// might be shipping.
//
// The abstract keys the product asks for (cpu_real, memory_limit, p90_cpu, …) are
// provider-neutral; what differs per schema is where the number lives. Three layouts
// are in the field:
//
//   - Elastic Agent Kubernetes integration -> metrics-kubernetes.{pod,container,
//     state_container,state_node}-* data streams, ECS field paths.
//   - Classic Metricbeat -> metricbeat-* indices, same ECS field paths.
//   - OTLP / Data Prepper -> {name, value, attributes.*} documents.
//
// In the first two the metric's identity IS its field path
// (`kubernetes.pod.cpu.usage.nanocores`); there is no name/value pair to filter on.
// The previous implementation only spoke OTLP, so an Elastic Agent tenant got an
// empty payload and no error for every key — the 0% gauges and "-" rows in the
// utilisation panel.
//
// Rather than detect a schema per account, each key expands into an ordered list of
// candidate sources and all of them are evaluated in one request. This mirrors the
// candidate expansion esCanonicalK8sFields already does for log labels, and for the
// same reason: one index pattern can hold documents from more than one collector, so
// a single detected schema would be wrong for part of the data.

// esFieldSet names the fields a schema uses for the scope filters. ECS spells them
// as a dotted path under `kubernetes`; OTLP nests them under attributes and needs the
// `.keyword` subfield, which is what the pre-existing OTLP queries filtered on.
type esFieldSet struct {
	Namespace string
	Pod       string
	Container string
	Node      string
	// Workload names the owning controller. kube-state-metrics datasets describe a
	// workload directly and carry no pod dimension at all, so they are filtered by
	// this rather than by a pod-name prefix.
	Workload string
	// PVC names the claim a volume is backed by. The kubelet volume metricset emits a
	// document per mounted volume and only sets this one for PVC-backed volumes, so it
	// doubles as the selector that separates them from configMap/projected/emptyDir
	// mounts — which report the NODE's filesystem size and would otherwise be summed
	// into the answer.
	PVC string
}

var (
	esECSFields = esFieldSet{
		Namespace: "kubernetes.namespace",
		Pod:       "kubernetes.pod.name",
		Container: "kubernetes.container.name",
		Node:      "kubernetes.node.name",
		Workload:  "kubernetes.deployment.name",
		PVC:       "kubernetes.persistentvolumeclaim.name",
	}
	esOTLPFields = esFieldSet{
		Namespace: "attributes.metric.attributes.namespace.keyword",
		Pod:       "attributes.metric.attributes.pod.keyword",
		Container: "attributes.metric.attributes.container.keyword",
		Node:      "attributes.resource.attributes.k8s@node@name.keyword",
	}
	// OTel-native indices (the Elasticsearch exporter's `mapping.mode: otel`) are a
	// third layout again: metrics are keyed by name under `metrics`, and dimensions
	// live under `resource.attributes` with dots rather than the Data Prepper `@`
	// escaping. parseESMetricsHitsWithStats has read this shape since the metrics
	// explorer shipped; the utilisation path had never been taught it, so a cluster
	// using the OTel Collector's ES exporter hit exactly the blank-dashboard bug this
	// file exists to fix.
	esOTelNativeFields = esFieldSet{
		Namespace: "resource.attributes.k8s.namespace.name",
		Pod:       "resource.attributes.k8s.pod.name",
		Container: "resource.attributes.k8s.container.name",
		Node:      "resource.attributes.k8s.node.name",
	}
)

// esOTelNativeSource builds a candidate for an OTel-native document. The value lives
// at `metrics.<otel metric name>`, and that path's presence is what selects the shape.
//
// These metric names come from the OTel Kubernetes semantic conventions
// (kubeletstats / k8scluster receivers) rather than from a live customer index — dev
// has no OTel-native metrics stream to check against. That is safe here in a way it
// would not be in a single-schema design: a name that no customer actually writes
// simply never matches its `exists` filter, costs one empty sub-aggregation, and the
// next candidate answers. It is worth listing on that basis; it is not worth
// asserting as verified.
func esOTelNativeSource(metric string, scale float64, agg string, groupBy []string) esMetricSource {
	return esMetricSource{
		Field: "metrics." + metric, Scale: scale, Agg: agg,
		GroupBy: groupBy, Fields: esOTelNativeFields,
	}
}

// esMetricSource is one place a quantity may live.
//
// Field doubles as the dataset selector: `kubernetes.pod.cpu.usage.nanocores` exists
// only in documents from the `pod` metricset, so an `exists` filter on it picks the
// right dataset without a term on `metricset.name`. That matters beyond brevity — a
// customer template that maps `metricset.name` as `text` would make such a term match
// nothing, silently, which is the failure mode this whole file exists to end.
type esMetricSource struct {
	// Name, when set, is an OTLP metric name matched against NameField; the value is
	// then read from Field ("value"). Empty for the ECS layouts.
	Name      string
	NameField string
	Field     string
	// Scale converts the stored unit to the product's unit (nanocores -> cores).
	Scale float64
	// Agg is how several samples of one series inside a bucket collapse: "avg" for
	// usage gauges, "max" for resource specs that should not be averaged across a
	// spec change.
	Agg string
	// GroupBy identifies one series within a bucket. Two levels (pod, container) are
	// nested terms rather than multi_terms, which OpenSearch only gained in 2.1.
	GroupBy []string
	// Counter marks a cumulative field that must be differentiated to a per-second
	// rate (Metricbeat network byte counters).
	Counter bool
	Fields  esFieldSet
	// Scoped fields the source does not carry. Container-level specs have no node
	// field, so a node filter must not be applied to them.
	NoNodeFilter bool
	// WorkloadScoped marks a source that describes the workload itself rather than
	// its pods — replica counts from kube-state-metrics. The name filter then matches
	// the controller exactly instead of prefix-matching pod names, which is both
	// cheaper and more accurate than the "{workload}-.*" trick the PromQL form needs.
	WorkloadScoped bool
	// PVCScoped marks a source that describes a persistent volume claim. Its series is
	// one claim, not one pod, so the pod/container/node filters do not apply — one
	// claim can be mounted by several pods, and filtering by pod would drop the rest.
	PVCScoped bool
}

// esUtilScope is the granularity a utilisation request is asking about. The previous
// code derived a boolean isNode, which sent cluster-level requests (no namespace, no
// name, no node — the Account Overview panel) down the container branch and asked for
// a container metric with no container in sight.
type esUtilScope string

const (
	esScopeCluster  esUtilScope = "cluster"
	esScopeNode     esUtilScope = "node"
	esScopeWorkload esUtilScope = "workload"
	esScopePod      esUtilScope = "pod"
)

func esUtilisationScope(meta RequestMetadata) esUtilScope {
	switch {
	case strings.EqualFold(meta.Kind, "node"):
		return esScopeNode
	case meta.Namespace == "" && meta.Name == "" && meta.NodeName != "":
		return esScopeNode
	case meta.Namespace == "" && meta.Name == "" && meta.NodeName == "":
		return esScopeCluster
	case strings.EqualFold(meta.Kind, "pod"):
		return esScopePod
	default:
		return esScopeWorkload
	}
}

// esUtilReduce is how a series collapses to the single number a gauge row shows.
//
// All reductions here are over TIME: p90 is the 90th percentile of the cluster (or
// workload) total across the picker window, and max is its peak. That matches the
// panel's own labels — "P90 Usage" next to "Max Usage" reads as typical-versus-peak
// over the selected range.
//
// It matches the Prometheus builder for CPU (`quantile_over_time`) but deliberately
// not for memory, where that builder uses `quantile(0.9, node_memory_… )` — a
// quantile ACROSS nodes at a single instant, which answers "how loaded is the 90th
// percentile node" rather than "how much memory does this cluster typically use".
// The two are not comparable, and only one of them is the question the row asks.
type esUtilReduce string

const (
	esReduceNone esUtilReduce = ""
	esReduceP50  esUtilReduce = "p50"
	esReduceP90  esUtilReduce = "p90"
	esReduceMax  esUtilReduce = "max"
)

// esUtilMetric is the resolved plan for one abstract key at one scope.
type esUtilMetric struct {
	Sources []esMetricSource
	Reduce  esUtilReduce
	// Breakdown returns one series PER GROUP instead of the summed total. The nodes
	// list needs a usage figure for each node row, not a cluster figure, and matches
	// series to rows on the `node` / `instance` label.
	Breakdown bool
}

const (
	nanocoresToCores = 1e-9
	esNoScale        = 1.0
)

// podCPUSources / podMemSources and friends are the candidate lists for workload and
// pod scope. Pod-level documents come first: they are one series per pod, already
// summed over the pod's containers. Container-level documents are the fallback, and
// the preferred source when the request names a container.
//
// `working_set` (pod metricset) versus `workingset` (container metricset) is not a
// typo — Metricbeat really does spell the same quantity both ways.
func esWorkloadCPUSources(containerNamed bool) []esMetricSource {
	pod := esMetricSource{
		Field: "kubernetes.pod.cpu.usage.nanocores", Scale: nanocoresToCores, Agg: "avg",
		GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
	}
	container := esMetricSource{
		Field: "kubernetes.container.cpu.usage.nanocores", Scale: nanocoresToCores, Agg: "avg",
		GroupBy: []string{esECSFields.Pod, esECSFields.Container}, Fields: esECSFields,
	}
	otlp := esMetricSource{
		Name: "container.cpu.usage", NameField: "name.keyword", Field: "value", Scale: esNoScale, Agg: "avg",
		GroupBy: []string{esOTLPFields.Pod}, Fields: esOTLPFields,
	}
	native := esOTelNativeSource("k8s.pod.cpu.usage", esNoScale, "avg", []string{esOTelNativeFields.Pod})
	if containerNamed {
		return []esMetricSource{container, pod, otlp, native}
	}
	return []esMetricSource{pod, container, otlp, native}
}

func esWorkloadMemSources(containerNamed bool) []esMetricSource {
	pod := esMetricSource{
		Field: "kubernetes.pod.memory.working_set.bytes", Scale: esNoScale, Agg: "avg",
		GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
	}
	container := esMetricSource{
		Field: "kubernetes.container.memory.workingset.bytes", Scale: esNoScale, Agg: "avg",
		GroupBy: []string{esECSFields.Pod, esECSFields.Container}, Fields: esECSFields,
	}
	otlp := esMetricSource{
		Name: "container.memory.working_set", NameField: "name.keyword", Field: "value", Scale: esNoScale, Agg: "avg",
		GroupBy: []string{esOTLPFields.Pod}, Fields: esOTLPFields,
	}
	native := esOTelNativeSource("k8s.pod.memory.working_set", esNoScale, "avg", []string{esOTelNativeFields.Pod})
	if containerNamed {
		return []esMetricSource{container, pod, otlp, native}
	}
	return []esMetricSource{pod, container, otlp, native}
}

// esSpecSource builds the source for a resource request/limit. These live in the
// state_container metricset (kube-state-metrics), keyed by pod and container, and
// carry no node field — a node filter would match nothing.
func esSpecSource(field string) esMetricSource {
	return esMetricSource{
		Field: field, Scale: esNoScale, Agg: "max",
		GroupBy: []string{esECSFields.Pod, esECSFields.Container},
		Fields:  esECSFields, NoNodeFilter: true,
	}
}

// esSpecSources pairs the ECS spec field with its OTel-native equivalent from the
// k8scluster receiver, so requests and limits resolve on either layout.
func esSpecSources(ecsField, otelMetric string) []esMetricSource {
	native := esOTelNativeSource(otelMetric, esNoScale, "max",
		[]string{esOTelNativeFields.Pod, esOTelNativeFields.Container})
	native.NoNodeFilter = true
	return []esMetricSource{esSpecSource(ecsField), native}
}

// esNodeCPUUsageSources / esNodeMemUsageSources answer cluster- and node-scope
// usage. The kubelet `node` metricset is the direct source, but it is NOT always
// enabled: the Elastic Agent Kubernetes integration lets each metricset be turned on
// separately, and a real customer's stream (gd-ehq, 2026-08-27) carries pod,
// container, state_pod, state_node and proxy — no `node`. Verified through the full
// RPC stack: every usage key came back "no series, searched 0 documents".
//
// Summing pod usage is the fallback. It is not identical — it excludes kubelet,
// container runtime and other system overhead outside pod cgroups, so it reads
// slightly LOW against node capacity — but "slightly low" is a usable utilisation
// gauge and an empty one is not. The direct node source stays first, so a cluster
// that does ship the `node` metricset is unaffected.
func esNodeCPUUsageSources() []esMetricSource {
	return []esMetricSource{
		{
			Field: "kubernetes.node.cpu.usage.nanocores", Scale: nanocoresToCores, Agg: "avg",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		},
		{
			Field: "kubernetes.pod.cpu.usage.nanocores", Scale: nanocoresToCores, Agg: "avg",
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
		},
		{
			Name: "system.cpu.utilization", NameField: "name.keyword", Field: "value", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esOTLPFields.Node}, Fields: esOTLPFields,
		},
		esOTelNativeSource("k8s.node.cpu.usage", esNoScale, "avg", []string{esOTelNativeFields.Node}),
		esOTelNativeSource("k8s.pod.cpu.usage", esNoScale, "avg", []string{esOTelNativeFields.Pod}),
	}
}

func esNodeMemUsageSources() []esMetricSource {
	return []esMetricSource{
		{
			Field: "kubernetes.node.memory.usage.bytes", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		},
		{
			Field: "kubernetes.pod.memory.working_set.bytes", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
		},
		{
			Name: "system.memory.usage", NameField: "name.keyword", Field: "value", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esOTLPFields.Node}, Fields: esOTLPFields,
		},
		esOTelNativeSource("k8s.node.memory.usage", esNoScale, "avg", []string{esOTelNativeFields.Node}),
		esOTelNativeSource("k8s.pod.memory.working_set", esNoScale, "avg", []string{esOTelNativeFields.Pod}),
	}
}

// esNodeOnlyUsageSources are the node-grouped usage candidates, without the
// pod-metricset fallback. Per-node breakdown needs series keyed by node; the pod
// fallback groups by pod and would hand back pod series wearing node labels.
func esNodeOnlyUsageSources(all []esMetricSource) []esMetricSource {
	out := make([]esMetricSource, 0, len(all))
	for _, src := range all {
		if len(src.GroupBy) == 1 && (src.GroupBy[0] == esECSFields.Node ||
			src.GroupBy[0] == esOTelNativeFields.Node || src.GroupBy[0] == esOTLPFields.Node) {
			out = append(out, src)
		}
	}
	return out
}

// esReplicaSources builds the candidates for a replica count: the deployment
// dataset first (it names the controller directly), then the replicaset dataset,
// which also carries kubernetes.deployment.name and so covers clusters shipping
// only state_replicaset. Both are "max" — a replica count is a level, not a rate,
// and averaging across a scale event would invent a fractional replica.
func esReplicaSources(deploymentField, replicasetField string) []esMetricSource {
	return []esMetricSource{
		{
			Field: deploymentField, Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Workload}, Fields: esECSFields,
			WorkloadScoped: true, NoNodeFilter: true,
		},
		{
			Field: replicasetField, Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Workload}, Fields: esECSFields,
			WorkloadScoped: true, NoNodeFilter: true,
		},
	}
}

// esCapacitySource builds a node capacity source (state_node metricset). Allocatable
// is preferred over capacity: it is what the scheduler can actually place work on,
// which is the number the utilisation gauge divides by.
func esCapacitySource(allocatable, capacity, otelAllocatable string) []esMetricSource {
	return []esMetricSource{
		{
			Field: allocatable, Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		},
		{
			Field: capacity, Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		},
		esOTelNativeSource(otelAllocatable, esNoScale, "max", []string{esOTelNativeFields.Node}),
	}
}

// esUtilisationMetric resolves an abstract metric key at a scope into the sources to
// try and the reduction to apply. Returns ok=false for keys with no equivalent in any
// Elasticsearch layout, so the caller can say which ones those were instead of
// returning an empty payload that reads as "no data".
func esUtilisationMetric(key string, scope esUtilScope, containerNamed bool) (esUtilMetric, bool) {
	nodeScope := scope == esScopeCluster || scope == esScopeNode

	cpuUsage := esWorkloadCPUSources(containerNamed)
	memUsage := esWorkloadMemSources(containerNamed)
	if nodeScope {
		cpuUsage = esNodeCPUUsageSources()
		memUsage = esNodeMemUsageSources()
	}

	switch key {
	case "cpu_usage", "cpu_usage_pod", "cpu_real", "cpu_usage_trend":
		return esUtilMetric{Sources: cpuUsage}, true
	case "memory_usage", "mem_real", "mem_usage_trend":
		return esUtilMetric{Sources: memUsage}, true

	// The nodes list asks for these and matches each returned series to a node row,
	// so they must come back broken down per node rather than summed. Unmapped, they
	// fell to the "no Elasticsearch equivalent" branch, which returns a note WITHOUT
	// issuing a search — which is why the nodes page showed 0% with no ES query in
	// the logs at all.
	// Node filesystem, from the kubelet `node` metricset. These are node-level in
	// the Prometheus builder too (node_filesystem_* selected by instance), so cluster
	// scope sums across nodes and node scope narrows to the one.
	case "disk_total":
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.node.fs.capacity.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		}}}, true
	case "disk_used":
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.node.fs.used.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		}}}, true

	// PVC usage and size come from the kubelet volume metricset, one document per
	// mounted volume per pod. The series is the claim, so both group by claim name.
	//
	// pvc_requests answers with the volume's FILESYSTEM capacity, where the PromQL
	// form reads the claim's requested size from kube-state-metrics. Elastic Agent
	// ships no equivalent of that field (kubernetes.persistentvolumeclaim.* carries
	// only the name), and filesystem capacity is a few percent under the request
	// because of filesystem overhead — a 100Gi claim measures 98.25 GiB here. It is
	// the honest number for "how much room does this volume actually have", which is
	// what the panel is asked, but it will not match the PromQL figure exactly.
	case "pvc_usage":
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.volume.fs.used.bytes", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esECSFields.PVC}, Fields: esECSFields, PVCScoped: true,
		}}}, true
	case "pvc_requests":
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.volume.fs.capacity.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.PVC}, Fields: esECSFields, PVCScoped: true,
		}}}, true

	// Replica counts describe the controller, so they come from kube-state-metrics
	// rather than from any pod-level metricset. Field names verified against the same
	// Elastic Kubernetes integration the customer runs, not inferred.
	case "replica_defined":
		return esUtilMetric{Sources: esReplicaSources(
			"kubernetes.deployment.replicas.desired", "kubernetes.replicaset.replicas.desired")}, true
	case "replica_ready":
		return esUtilMetric{Sources: esReplicaSources(
			"kubernetes.deployment.replicas.available", "kubernetes.replicaset.replicas.ready")}, true

	case "cpu_usage_line":
		return esUtilMetric{Sources: esNodeOnlyUsageSources(esNodeCPUUsageSources()), Breakdown: true}, true
	case "memory_usage_line":
		return esUtilMetric{Sources: esNodeOnlyUsageSources(esNodeMemUsageSources()), Breakdown: true}, true

	case "p50_cpu":
		return esUtilMetric{Sources: cpuUsage, Reduce: esReduceP50}, true
	case "p90_cpu":
		return esUtilMetric{Sources: cpuUsage, Reduce: esReduceP90}, true
	case "max_usage_cpu":
		return esUtilMetric{Sources: cpuUsage, Reduce: esReduceMax}, true
	case "p50_mem":
		return esUtilMetric{Sources: memUsage, Reduce: esReduceP50}, true
	case "p90_mem":
		return esUtilMetric{Sources: memUsage, Reduce: esReduceP90}, true
	case "max_usage_mem":
		return esUtilMetric{Sources: memUsage, Reduce: esReduceMax}, true

	case "cpu_request", "cpu_request_pod":
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.cpu.request.cores", "k8s.container.cpu_request")}, true
	case "cpu_limit", "cpu_limit_pod":
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.cpu.limit.cores", "k8s.container.cpu_limit")}, true
	case "memory_request":
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.memory.request.bytes", "k8s.container.memory_request")}, true
	case "memory_limit":
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.memory.limit.bytes", "k8s.container.memory_limit")}, true

	case "cpu_total":
		return esUtilMetric{Sources: esCapacitySource(
			"kubernetes.node.cpu.allocatable.cores", "kubernetes.node.cpu.capacity.cores",
			"k8s.node.allocatable_cpu")}, true
	case "mem_total":
		return esUtilMetric{Sources: esCapacitySource(
			"kubernetes.node.memory.allocatable.bytes", "kubernetes.node.memory.capacity.bytes",
			"k8s.node.allocatable_memory")}, true

	// Network counters feed the abandoned-workload scan, which averages the values
	// as rates. Metricbeat ships cumulative byte counters, so they are differentiated
	// per second before being summed across pods.
	case "network_receive_packet":
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.pod.network.rx.bytes", Scale: esNoScale, Agg: "max", Counter: true,
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
		}}}, true
	case "network_transmit_packets":
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.pod.network.tx.bytes", Scale: esNoScale, Agg: "max", Counter: true,
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
		}}}, true
	}

	return esUtilMetric{}, false
}

// esUtilStepSeconds picks the date_histogram interval. meta.Step is already derived
// from the picker range by promAggWindow ("300s"); parse it rather than deriving a
// second, differently-rounded window, so ES buckets line up with what the Prometheus
// path would have produced for the same picker range.
func esUtilStepSeconds(meta RequestMetadata, startMs, endMs int64) int64 {
	if s := strings.TrimSuffix(meta.Step, "s"); s != "" && s != meta.Step {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			return v
		}
	}
	rangeSec := (endMs - startMs) / 1000
	if rangeSec <= 0 {
		return 300
	}
	step := rangeSec / 300
	if step < 60 {
		step = 60
	}
	if step > 1800 {
		step = 1800
	}
	return step
}

// esUtilisationConcurrency bounds how many metric-key queries run at once against a
// customer's Elasticsearch. The utilisation panel asks for fourteen keys; six keeps
// that to a few waves without opening fourteen simultaneous searches on a cluster
// that may already be under load (the customer's has 292 shards).
const esUtilisationConcurrency = 6

// esUtilTermsSize caps the per-bucket series count.
//
// Sized for the group that actually gets large: cluster-scope requests and limits
// group by POD, so the cap has to clear a whole cluster's pod count, not a single
// workload's. At 1000 a cluster with more pods than that silently summed a subset —
// the number stayed plausible and simply read low. Exceeding this is now reported
// through esUtilOutcome.TruncatedDocs and surfaced on the result, not just logged.
const esUtilTermsSize = 10000

// esSourceScopeFilters renders the namespace/pod/node constraints for one source in
// its own field vocabulary.
func esSourceScopeFilters(src esMetricSource, meta RequestMetadata, scope esUtilScope) []any {
	var filters []any
	if meta.Namespace != "" && src.Fields.Namespace != "" {
		filters = append(filters, map[string]any{"term": map[string]any{src.Fields.Namespace: meta.Namespace}})
	}
	if src.PVCScoped {
		// Only PVC-backed volumes carry a claim name. Without this the sum also picks
		// up configMap/projected/emptyDir mounts, which report the node's filesystem
		// size — on this cluster that turns a 6 GiB claim into ~96 GiB.
		filters = append(filters, map[string]any{"exists": map[string]any{"field": src.Fields.PVC}})
		switch {
		case meta.PVCName != "":
			filters = append(filters, map[string]any{"term": map[string]any{src.Fields.PVC: meta.PVCName}})
		case meta.Name != "":
			// A StatefulSet's claims are {volumeClaimTemplate}-{workload}-{ordinal}, so
			// the workload name appears mid-string rather than at the front; the PromQL
			// form prefix-matches here and misses them the same way. Namespace scope is
			// still applied above, so an unmatched name degrades to the namespace total
			// rather than to the cluster's.
			//
			// Escaped: an unescaped "*" or "?" reaching a wildcard query widens the match
			// and can be made arbitrarily expensive to evaluate.
			filters = append(filters, map[string]any{"wildcard": map[string]any{src.Fields.PVC: map[string]any{"value": "*" + escapeESWildcard(meta.Name) + "*"}}})
		}
		return filters
	}
	if meta.Name != "" && src.WorkloadScoped {
		if src.Fields.Workload != "" {
			filters = append(filters, map[string]any{"term": map[string]any{src.Fields.Workload: meta.Name}})
		}
	} else if meta.Name != "" && src.Fields.Pod != "" {
		if scope == esScopePod {
			filters = append(filters, map[string]any{"term": map[string]any{src.Fields.Pod: meta.Name}})
		} else {
			// Pod names are {workload}-{rs-hash}-{pod-hash}; a prefix covers every pod
			// of the workload. `prefix` rather than `wildcard`: same match, and it does
			// not need the name escaped for wildcard metacharacters.
			filters = append(filters, map[string]any{"prefix": map[string]any{src.Fields.Pod: meta.Name + "-"}})
		}
	}
	if meta.ContainerName != "" && src.Fields.Container != "" && len(src.GroupBy) > 1 {
		filters = append(filters, map[string]any{"term": map[string]any{src.Fields.Container: meta.ContainerName}})
	}
	if meta.NodeName != "" && src.Fields.Node != "" && !src.NoNodeFilter {
		if clause := esNodeNameClause(src.Fields.Node, meta.NodeName); clause != nil {
			filters = append(filters, clause)
		}
	}
	// A source with no node field is narrowed by the pods on that node instead. This is
	// what lets a node chart show request and limit lines, which come from
	// state_container — a dataset with a pod name but no node.
	if scope == esScopeNode && src.NoNodeFilter && src.Fields.Pod != "" && len(meta.NodePods) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{src.Fields.Pod: meta.NodePods}})
	}
	return filters
}

// esNodeNameClause matches a node-name selector that may be a single literal or a
// PromQL regex alternation.
//
// The nodes list builds its selector for PromQL's =~ matcher — it joins every node as
// "<name>.*" with "|", e.g. "node-a.*|node-b.*". Handed to an Elasticsearch `term`
// that whole string matches nothing, so the table rendered 0% for every node while
// the single-node chart (which sends one literal name) worked fine.
//
// Each branch becomes a prefix or a term, combined in a should. Prefix rather than
// regexp: the ".*" suffix is exactly a prefix match, and prefix is far cheaper.
//
// Returns nil when the selector cannot narrow anything — it is empty, or some branch
// is a bare ".*" that matches every node. Callers must then append no clause at all.
// The tempting shortcut, emitting prefix:"" for a bare ".*", is a silent data bug:
// an empty prefix matches every document, so a node-scoped chart would answer with
// cluster-wide totals rather than erroring.
func esNodeNameClause(field, selector string) map[string]any {
	branches := strings.Split(selector, "|")
	shoulds := make([]map[string]any, 0, len(branches))
	for _, b := range branches {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		if trimmed := strings.TrimSuffix(b, ".*"); trimmed != b {
			if trimmed == "" {
				// Bare ".*" — this branch alone matches every node, so the whole
				// alternation does too. No filter is the honest translation.
				return nil
			}
			shoulds = append(shoulds, map[string]any{"prefix": map[string]any{field: trimmed}})
			continue
		}
		shoulds = append(shoulds, map[string]any{"term": map[string]any{field: b}})
	}
	switch len(shoulds) {
	case 0:
		return nil
	case 1:
		return shoulds[0]
	}
	return map[string]any{"bool": map[string]any{"should": shoulds, "minimum_should_match": 1}}
}

// esSourceUnusableAtScope reports whether a source cannot honour the requested
// scope, in which case it must not answer at all.
//
// state_container carries requests and limits but no node field, so a node-scoped
// question cannot be narrowed to that node. Answering anyway returns the CLUSTER
// total: the node detail chart drew "CPU Limit 97 cores" against a 4-core node.
// A missing line is honest; a confidently wrong one is not. Cluster scope is
// unaffected — there the un-narrowed sum is exactly the right answer.
func esSourceUnusableAtScope(src esMetricSource, meta RequestMetadata, scope esUtilScope) bool {
	if scope != esScopeNode || meta.NodeName == "" {
		return false
	}
	// A PVC source ignores the node filter by construction — a claim is not a
	// node-local thing — so a node-scoped ask would be answered with the cluster's
	// claims. There is no pod join that rescues it: a claim can outlive and outrank
	// any single pod.
	if src.PVCScoped {
		return true
	}
	// A node-less source is answerable once the pods on the node are known — see
	// esPodsOnNode. Without that list it must still be dropped, because answering
	// un-narrowed returns the CLUSTER total.
	return src.NoNodeFilter && (src.Fields.Pod == "" || len(meta.NodePods) == 0)
}

// esSourceAgg renders one candidate source as a named filter aggregation:
//
//	filter(exists(field) AND scope)
//	  ts: date_histogram
//	     g0: terms(<group 0>)          [ g1: terms(<group 1>) -> v, gsum ]
//	        v: avg|max(field)
//	     total: sum_bucket(g0>v)       # sum across series, matching Prometheus sum()
//	     rate:  derivative(total)      # counters only
//	  p50/p90/max: *_bucket(ts>total)  # reduced keys only
//
// Summing per-bucket per-series aggregates — rather than summing raw documents — is
// what keeps the number correct when a pod reports several samples inside one bucket.
func esSourceAgg(src esMetricSource, meta RequestMetadata, scope esUtilScope, stepSec int64) map[string]any {
	filters := []any{map[string]any{"exists": map[string]any{"field": src.Field}}}
	if src.Name != "" {
		filters = append(filters, map[string]any{"term": map[string]any{src.NameField: src.Name}})
	}
	filters = append(filters, esSourceScopeFilters(src, meta, scope)...)

	valueAgg := map[string]any{src.Agg: map[string]any{"field": src.Field}}

	// Innermost group carries the value; an outer group sums its inner groups so two
	// levels work with plain terms aggregations (multi_terms is ES 7.12+/OpenSearch
	// 2.1+ and buys nothing here).
	// Every source names one or two group-by fields: a utilisation number is always a
	// sum over pods or over nodes, never a bare aggregate over documents. Anything
	// else is a malformed source, which returns no aggregation so the caller drops it
	// — a missing candidate degrades to the next one, whereas grouping on something
	// arbitrary would answer confidently with the wrong number.
	// TestEverySourceGroupsByOneOrTwoFields holds the invariant over the source table.
	var groupAgg map[string]any
	var totalPath string
	switch len(src.GroupBy) {
	default:
		return nil
	case 1:
		groupAgg = map[string]any{
			"g0": map[string]any{
				"terms": map[string]any{"field": src.GroupBy[0], "size": esUtilTermsSize},
				"aggs":  map[string]any{"v": valueAgg},
			},
		}
		totalPath = "g0>v"
	case 2:
		groupAgg = map[string]any{
			"g0": map[string]any{
				"terms": map[string]any{"field": src.GroupBy[0], "size": esUtilTermsSize},
				"aggs": map[string]any{
					"g1": map[string]any{
						"terms": map[string]any{"field": src.GroupBy[1], "size": esUtilTermsSize},
						"aggs":  map[string]any{"v": valueAgg},
					},
					"gsum": map[string]any{"sum_bucket": map[string]any{"buckets_path": "g1>v"}},
				},
			},
		}
		totalPath = "g0>gsum"
	}

	tsAggs := map[string]any{}
	for k, v := range groupAgg {
		tsAggs[k] = v
	}
	tsAggs["total"] = map[string]any{"sum_bucket": map[string]any{"buckets_path": totalPath}}
	if src.Counter {
		tsAggs["rate"] = map[string]any{"derivative": map[string]any{"buckets_path": "total", "unit": "1s"}}
	}

	// Empty buckets are dropped so a gap reads as a gap rather than a zero — except
	// under a derivative, which Elasticsearch refuses on a histogram with
	// min_doc_count > 0 ("parent histogram of derivative aggregation must have
	// min_doc_count of 0"): it needs the empty buckets to keep the time axis even.
	// The parser skips the resulting null values.
	minDocCount := 1
	if src.Counter {
		minDocCount = 0
	}
	aggs := map[string]any{
		"ts": map[string]any{
			"date_histogram": map[string]any{
				"field":          "@timestamp",
				"fixed_interval": fmt.Sprintf("%ds", stepSec),
				"min_doc_count":  minDocCount,
			},
			"aggs": tsAggs,
		},
	}

	// Reductions read the summed series, so a percentile is over cluster totals at
	// each instant — not over individual pods' samples, which would mix series.
	// All three reductions are emitted on every query, not just the one this key
	// wants. They are pipeline aggregations over a histogram Elasticsearch has
	// already built, so the marginal cost is negligible — and it means cpu_real,
	// p50_cpu, p90_cpu and max_usage_cpu, which differ ONLY by reduction, can be
	// answered from ONE search instead of four. On a cluster the size of the
	// customer's that is the difference between 14 heavy aggregations per panel
	// load and 6.
	reducePath := "ts>total"
	if src.Counter {
		reducePath = "ts>rate"
	}
	aggs[esReducedAggName(esReduceP50)] = map[string]any{
		"percentiles_bucket": map[string]any{"buckets_path": reducePath, "percents": []any{50}}}
	aggs[esReducedAggName(esReduceP90)] = map[string]any{
		"percentiles_bucket": map[string]any{"buckets_path": reducePath, "percents": []any{90}}}
	aggs[esReducedAggName(esReduceMax)] = map[string]any{
		"max_bucket": map[string]any{"buckets_path": reducePath}}

	return map[string]any{
		"filter": map[string]any{"bool": map[string]any{"filter": filters}},
		"aggs":   aggs,
	}
}

// buildESUtilisationAggQuery renders every candidate source of one metric key into a
// single _search body. All candidates are evaluated server-side in one round trip; the
// parser takes the first that produced buckets. The alternative — probe the mapping,
// then query — costs an extra request and can still be wrong when an index pattern
// spans collectors.
func buildESUtilisationAggQuery(m esUtilMetric, meta RequestMetadata, scope esUtilScope, startMs, endMs, stepSec int64) map[string]any {
	aggs := make(map[string]any, len(m.Sources))
	for i, src := range m.Sources {
		if esSourceUnusableAtScope(src, meta, scope) {
			continue
		}
		agg := esSourceAgg(src, meta, scope, stepSec)
		if agg == nil {
			continue
		}
		aggs[esSourceAggName(i)] = agg
	}
	return map[string]any{
		"size": 0,
		"query": map[string]any{
			"bool": map[string]any{"filter": []any{esMetricsTimeRangeClause(startMs, endMs)}},
		},
		"aggs": aggs,
	}
}

func esSourceAggName(i int) string { return fmt.Sprintf("src_%d", i) }

// esUtilAggResponse is the aggregation envelope. Sources are decoded generically
// because their names are positional (src_0, src_1, …).
type esUtilAggResponse struct {
	Aggregations map[string]json.RawMessage `json:"aggregations"`
}

type esUtilSourceResult struct {
	DocCount int64 `json:"doc_count"`
	TS       struct {
		Buckets []esUtilBucket `json:"buckets"`
	} `json:"ts"`
	ReducedP50 json.RawMessage `json:"reduced_p50"`
	ReducedP90 json.RawMessage `json:"reduced_p90"`
	ReducedMax json.RawMessage `json:"reduced_max"`
}

type esUtilBucket struct {
	Key   int64 `json:"key"`
	Total struct {
		Value *float64 `json:"value"`
	} `json:"total"`
	Rate struct {
		NormalizedValue *float64 `json:"normalized_value"`
	} `json:"rate"`
	G0 struct {
		SumOtherDocCount int64            `json:"sum_other_doc_count"`
		Buckets          []esUtilG0Bucket `json:"buckets"`
	} `json:"g0"`
}

// esUtilG0Bucket is one group (a pod, or a node) inside a histogram bucket. Read
// only in breakdown mode, where callers want the per-series values the summed
// `total` throws away — "which pod on this node is eating the memory" cannot be
// answered by a cluster total.
type esUtilG0Bucket struct {
	Key string `json:"key"`
	V   struct {
		Value *float64 `json:"value"`
	} `json:"v"`
	GSum struct {
		Value *float64 `json:"value"`
	} `json:"gsum"`
	G1 struct {
		Buckets []struct {
			Key string `json:"key"`
			V   struct {
				Value *float64 `json:"value"`
			} `json:"v"`
		} `json:"buckets"`
	} `json:"g1"`
}

// esReducedAggName is the aggregation name a reduction is emitted under. Named per
// reduction rather than a single "reduced" so one search can carry all of them.
func esReducedAggName(r esUtilReduce) string { return "reduced_" + string(r) }

// reducedFor picks the reduction this metric key asked for out of the three every
// query now carries.
func (r *esUtilSourceResult) reducedFor(reduce esUtilReduce) json.RawMessage {
	switch reduce {
	case esReduceP50:
		return r.ReducedP50
	case esReduceP90:
		return r.ReducedP90
	case esReduceMax:
		return r.ReducedMax
	}
	return nil
}

// esUtilBucketValue is the number one histogram bucket contributes: the summed
// series, or its per-second derivative for cumulative counters. Counter resets and
// pod churn make a derivative negative; those are clamped to zero rather than
// subtracted from the average the caller computes.
func esUtilBucketValue(b esUtilBucket, counter bool) (float64, bool) {
	if counter {
		if b.Rate.NormalizedValue == nil {
			return 0, false
		}
		v := *b.Rate.NormalizedValue
		if v < 0 {
			v = 0
		}
		return v, true
	}
	if b.Total.Value == nil {
		return 0, false
	}
	return *b.Total.Value, true
}

// esUtilReducedValue reads the pipeline aggregation that collapses the series to one
// number. percentiles_bucket answers under "values" keyed by the percent as a float
// string; max_bucket answers with a plain "value".
func esUtilReducedValue(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var single struct {
		Value *float64 `json:"value"`
	}
	if err := json.Unmarshal(raw, &single); err == nil && single.Value != nil {
		return *single.Value, true
	}
	var pct struct {
		Values map[string]*float64 `json:"values"`
	}
	if err := json.Unmarshal(raw, &pct); err == nil {
		for _, v := range pct.Values {
			if v != nil {
				return *v, true
			}
		}
	}
	return 0, false
}

// esUtilSeriesLabels describes the series the way the rest of the pipeline expects:
// a __name__ plus whatever scope the request pinned. The field path is the name for
// the ECS layouts, which makes the source visible in the response — useful when two
// layouts coexist and the question is which one answered.
func esUtilSeriesLabels(src esMetricSource, meta RequestMetadata) map[string]string {
	name := src.Field
	if src.Name != "" {
		name = src.Name
	}
	labels := map[string]string{"__name__": name}
	if meta.Namespace != "" {
		labels["namespace"] = meta.Namespace
	}
	if src.PVCScoped {
		// The series is a claim, so meta.Name (a workload) must not be hung on it as
		// though it were one.
		if meta.PVCName != "" {
			labels["persistentvolumeclaim"] = meta.PVCName
		}
		return labels
	}
	if meta.Name != "" {
		// At pod scope meta.Name IS a pod name, so labelling it "workload" both
		// misnames it and drops the "pod" label the Prometheus path emits — which
		// consumers key on to tie a series to its pod (the app's dashboards read
		// metric['pod']). Grouped series get their real pod name in the breakdown
		// path; this is the single-series case.
		if esUtilisationScope(meta) == esScopePod {
			labels["pod"] = meta.Name
		} else {
			labels["workload"] = meta.Name
		}
	}
	if meta.ContainerName != "" {
		labels["container"] = meta.ContainerName
	}
	if meta.NodeName != "" {
		labels["node"] = meta.NodeName
	}
	return labels
}

// esUtilSourcesByCoverage orders candidate indexes by how much of the window each one
// actually covers, descending, with ties broken by the declared preference order.
//
// Coverage is counted as buckets carrying a usable value, not raw document count: a
// source can match thousands of documents and still yield nothing chartable (the
// shape did not fit), and that must not outrank a source that produced real points.
func esUtilSourcesByCoverage(m esUtilMetric, decoded []*esUtilSourceResult) []int {
	coverage := make([]int, len(m.Sources))
	for i, sr := range decoded {
		if sr == nil {
			continue
		}
		counter := m.Sources[i].Counter
		for _, b := range sr.TS.Buckets {
			if _, ok := esUtilBucketValue(b, counter); ok {
				coverage[i]++
			}
		}
	}
	order := make([]int, 0, len(m.Sources))
	for i := range m.Sources {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		return coverage[order[a]] > coverage[order[b]]
	})
	return order
}

// parseESUtilisationAggs returns the series from the candidate source with the best
// coverage of the window, along with the index of that source. A source that matched
// documents but yielded no bucket value is not "data" — it is a shape that did not
// fit — so it is skipped rather than returned as an empty series.
//
// "Best coverage", not "first that answered". Taking the first non-empty candidate
// looks equivalent and is not: when a customer enables a metricset partway through a
// window, the preferred source has a handful of points and the fallback has the whole
// range, and first-wins would chart the handful. Ties go to the earlier candidate, so
// when coverage is equal — the normal case, including a cluster dual-shipping two
// layouts — the preference order in esUtilisationMetric still decides.
func parseESUtilisationAggs(bodyBytes []byte, m esUtilMetric, meta RequestMetadata) (esUtilOutcome, error) {
	var resp esUtilAggResponse
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return esUtilOutcome{SourceIdx: -1}, err
	}
	return parseESUtilisationResponse(&resp, m, meta)
}

// parseESUtilisationResponse reads an already-decoded response. Grouped keys share one
// response and differ only in the reduction they read back, so the outer envelope is
// decoded once per search rather than once per key — four times over for the CPU keys.
func parseESUtilisationResponse(resp *esUtilAggResponse, m esUtilMetric, meta RequestMetadata) (esUtilOutcome, error) {
	// Decode every candidate first: the winner is chosen on coverage, which cannot be
	// judged before they have all been read.
	var matched int64
	decoded := make([]*esUtilSourceResult, len(m.Sources))
	for i := range m.Sources {
		raw, ok := resp.Aggregations[esSourceAggName(i)]
		if !ok {
			continue
		}
		var sr esUtilSourceResult
		if err := json.Unmarshal(raw, &sr); err != nil {
			continue
		}
		matched += sr.DocCount
		decoded[i] = &sr
	}

	order := esUtilSourcesByCoverage(m, decoded)

	for _, i := range order {
		src := m.Sources[i]
		sr := decoded[i]
		if sr == nil || len(sr.TS.Buckets) == 0 {
			continue
		}

		labels := esUtilSeriesLabels(src, meta)

		if m.Breakdown {
			if series := esUtilBreakdownSeries(sr, src, labels); len(series) > 0 {
				return esUtilOutcome{Series: series, SourceIdx: i, DocsMatched: matched,
					TruncatedDocs: esUtilTruncatedDocs(sr)}, nil
			}
			continue
		}

		if m.Reduce != esReduceNone {
			v, ok := esUtilReducedValue(sr.reducedFor(m.Reduce))
			if !ok {
				continue
			}
			last := sr.TS.Buckets[len(sr.TS.Buckets)-1]
			return esUtilOutcome{Series: []Result{{
				Metric:     labels,
				Timestamps: []int64{last.Key / 1000},
				Values:     []float64{v * src.Scale},
			}}, SourceIdx: i, DocsMatched: matched, TruncatedDocs: esUtilTruncatedDocs(sr)}, nil
		}

		var truncated int64
		timestamps := make([]int64, 0, len(sr.TS.Buckets))
		values := make([]float64, 0, len(sr.TS.Buckets))
		for _, b := range sr.TS.Buckets {
			truncated += b.G0.SumOtherDocCount
			v, ok := esUtilBucketValue(b, src.Counter)
			if !ok {
				continue
			}
			timestamps = append(timestamps, b.Key/1000)
			values = append(values, v*src.Scale)
		}
		if len(values) == 0 {
			continue
		}
		if truncated > 0 {
			// A capped terms aggregation drops whole series from the sum, which reads
			// as a genuine dip in utilisation. Never let that pass unsaid.
			slog.Warn("ES utilisation: series truncated by terms cap",
				"field", src.Field, "cap", esUtilTermsSize, "dropped_docs", truncated)
		}
		return esUtilOutcome{
			Series:    []Result{{Metric: labels, Timestamps: timestamps, Values: values}},
			SourceIdx: i, DocsMatched: matched, TruncatedDocs: truncated,
		}, nil
	}
	return esUtilOutcome{SourceIdx: -1, DocsMatched: matched}, nil
}

// esUtilTruncatedDocs totals the documents the terms cap excluded across all buckets.
func esUtilTruncatedDocs(sr *esUtilSourceResult) int64 {
	var n int64
	for _, b := range sr.TS.Buckets {
		n += b.G0.SumOtherDocCount
	}
	return n
}

// esUtilTruncatedNote states that a sum omitted series, so a low number is never
// presented as a complete one.
func esUtilTruncatedNote(field string, dropped int64) string {
	return fmt.Sprintf("This total is INCOMPLETE: the per-series cap of %d was exceeded for %s and "+
		"%d document(s) were excluded, so the value reads low. Narrow the query (namespace or workload) "+
		"for an exact figure.", esUtilTermsSize, field, dropped)
}

// esUtilOutcome is what one utilisation search yielded. Truncation travels with it
// because a truncated sum is WRONG, not merely incomplete, and the caller has to be
// able to say so rather than presenting a low number as fact.
type esUtilOutcome struct {
	Series        []Result
	SourceIdx     int
	DocsMatched   int64
	TruncatedDocs int64
}

// esUtilBreakdownSeries returns one single-point series per group (per node),
// carrying the group key on both `node` and `instance`.
//
// Both labels, because the nodes list matches a series to its row on either — and on
// `instance` it also accepts a node IP, splitting on ":" for the Prometheus
// "host:port" form. Emitting the node name under both covers every branch of that
// match without the caller needing to know which backend answered.
//
// A single point rather than the whole series: the consumer reads values[0], so the
// series must lead with the number meant for the gauge. That is the LATEST reading,
// which is what a "current usage" bar should show.
func esUtilBreakdownSeries(sr *esUtilSourceResult, src esMetricSource, base map[string]string) []Result {
	type latest struct {
		ts  int64
		val float64
	}
	byGroup := map[string]latest{}
	var order []string
	for _, b := range sr.TS.Buckets {
		for _, g := range b.G0.Buckets {
			v := g.V.Value
			if v == nil {
				v = g.GSum.Value
			}
			if v == nil || g.Key == "" {
				continue
			}
			if _, seen := byGroup[g.Key]; !seen {
				order = append(order, g.Key)
			}
			// Buckets arrive oldest-first, so the last write per group is the latest.
			byGroup[g.Key] = latest{ts: b.Key / 1000, val: *v * src.Scale}
		}
	}
	results := make([]Result, 0, len(order))
	for _, key := range order {
		l := byGroup[key]
		labels := make(map[string]string, len(base)+2)
		for k, v := range base {
			labels[k] = v
		}
		labels["node"] = key
		labels["instance"] = key
		results = append(results, Result{Metric: labels, Timestamps: []int64{l.ts}, Values: []float64{l.val}})
	}
	return results
}

// esUtilNoDataNote names the fields that were searched. An empty payload with no
// explanation is the defect this file replaces: the caller could not tell "this
// cluster does not ship that metric" from "we asked the wrong question".
func esUtilNoDataNote(m esUtilMetric, matched int64) string {
	fields := make([]string, 0, len(m.Sources))
	for _, s := range m.Sources {
		if s.Name != "" {
			fields = append(fields, s.Name)
			continue
		}
		fields = append(fields, s.Field)
	}
	return fmt.Sprintf("No series found. Searched %d document(s) for: %s. "+
		"An Elastic Agent / Metricbeat cluster reports these only when the matching metricset is enabled "+
		"(pod, container and state_container for workloads; node and state_node for capacity).",
		matched, strings.Join(fields, ", "))
}

// esUtilUnsupportedNote explains a key with no Elasticsearch equivalent at all, so it
// is distinguishable from a key that has one and found nothing.
func esUtilUnsupportedNote(key string, scope esUtilScope) string {
	return fmt.Sprintf("Metric %q has no Elasticsearch equivalent at %s scope.", key, scope)
}

// esUtilQueryGroup is one distinct search and every metric key that shares it. The
// rendered body doubles as the grouping key, so two keys are grouped exactly when the
// bytes they would send are identical — see the comment at the grouping loop.
type esUtilQueryGroup struct {
	body     map[string]any
	rendered string
	members  []int
}

// esUtilAssignError records one failed search against every metric key that shared it.
func esUtilAssignError(results []QueryResult, keys []string, members []int, query, errStr string) {
	// One copy per member rather than a shared address. A string header is 16 bytes
	// and the copy does not duplicate the backing bytes, so the cost is nil — and it
	// keeps each result's Error independently addressable, rather than having a
	// consumer that writes through one pointer silently rewrite every other key's
	// error message.
	for _, m := range members {
		err := errStr
		results[m] = QueryResult{QueryKey: keys[m], Query: query, Error: &err}
	}
}

// esPodsOnNode lists the pods running on a node, so metricsets that carry no node
// field can still answer a node-scoped question.
//
// kube-state-metrics datasets (state_container, which holds requests and limits) have
// no node dimension at all, but they DO carry the pod name — and the kubelet usage
// metricsets carry both pod and node. So the node dimension is recoverable by joining
// through pods: resolve the pods on the node here, then filter the node-less source by
// that list.
//
// Before this, such sources were simply dropped at node scope (see
// esSourceUnusableAtScope), because answering without the node filter returns the
// CLUSTER total — the node chart once drew "CPU Limit 97 cores" against a 4-core node.
// Dropping them was honest but left the request and limit lines blank; the join makes
// them answerable instead.
//
// Returns the pods and whether the list was truncated. A truncated list silently
// under-counts, so callers must surface it rather than present a short answer as whole.
func esPodsOnNode(ctx *security.RequestContext, cfg *ElasticsearchConfig, index string, meta RequestMetadata, startMs, endMs int64) ([]string, bool, error) {
	nodeClause := esNodeNameClause(esECSFields.Node, meta.NodeName)
	if nodeClause == nil {
		return nil, false, nil
	}
	filters := []any{
		map[string]any{"exists": map[string]any{"field": esECSFields.Pod}},
		esMetricsTimeRangeClause(startMs, endMs),
		nodeClause,
	}
	body := map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
		"aggs": map[string]any{
			"pods": map[string]any{
				"terms": map[string]any{"field": esECSFields.Pod, "size": esUtilTermsSize},
			},
		},
	}
	var resp struct {
		Aggregations struct {
			Pods struct {
				SumOtherDocCount int64 `json:"sum_other_doc_count"`
				Buckets          []struct {
					Key string `json:"key"`
				} `json:"buckets"`
			} `json:"pods"`
		} `json:"aggregations"`
	}
	if err := esSearchInto(ctx, cfg, index, body, &resp); err != nil {
		return nil, false, err
	}
	pods := make([]string, 0, len(resp.Aggregations.Pods.Buckets))
	for _, b := range resp.Aggregations.Pods.Buckets {
		if b.Key != "" {
			pods = append(pods, b.Key)
		}
	}
	return pods, resp.Aggregations.Pods.SumOtherDocCount > 0, nil
}

// esScopeNeedsPodJoin reports whether any candidate for these plans is a node-less
// source being asked a node-scoped question, i.e. whether the join is worth a round trip.
func esScopeNeedsPodJoin(plans []esUtilMetric, meta RequestMetadata, scope esUtilScope) bool {
	if scope != esScopeNode || meta.NodeName == "" {
		return false
	}
	for _, plan := range plans {
		for _, src := range plan.Sources {
			if src.NoNodeFilter && src.Fields.Pod != "" {
				return true
			}
		}
	}
	return false
}
