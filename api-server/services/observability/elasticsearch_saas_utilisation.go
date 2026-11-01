package observability

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// Fallback marks a source searched only when the plan's other sources match no
	// documents. For one that answers the same question far more expensively — a
	// cluster sum over pods behind the sum over nodes — so it is not computed, and
	// does not count against search.max_buckets, on clusters that have the cheap one.
	Fallback bool
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
	// Series picks WHICH number a histogram bucket contributes: the sum across the
	// pods of the workload (the default, and what every usage metric wants), the
	// single busiest pod, or how many pods reported at all. All three are read off
	// one stats_bucket the query always carries, so asking for the busiest pod costs
	// no extra search — it shares the usage key's body and therefore its response.
	Series esUtilSeriesRead
	// PerPod returns one series per pod and container rather than the workload
	// total. With an Aggregate it collapses to one statistic per pod, computed by
	// Elasticsearch over the raw samples; without one it is the per-pod time series.
	PerPod bool
}

// esUtilSeriesRead names the per-bucket reading. The zero value is the summed total,
// so every existing metric keeps its behaviour without naming it.
type esUtilSeriesRead string

const (
	esSeriesTotal esUtilSeriesRead = ""
	esSeriesMax   esUtilSeriesRead = "max"
	esSeriesCount esUtilSeriesRead = "count"
)

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

// esGroupedByNamespace returns the sources with a namespace level above their
// existing grouping, when the scope needs one.
//
// A per-container key's answer is one series per container, and the thing that makes
// two containers distinct is (namespace, pod, container). At workload and pod scope a
// namespace filter supplies the first part, so grouping by the other two is enough —
// and leaving those sources untouched keeps their rendered bodies byte-identical. At
// node and cluster scope nothing filters by namespace, so it has to be grouped.
func esGroupedByNamespace(sources []esMetricSource, needed bool) []esMetricSource {
	if !needed {
		return sources
	}
	out := make([]esMetricSource, 0, len(sources))
	for _, src := range sources {
		if src.Fields.Namespace != "" && len(src.GroupBy) < esPerPodMaxGroupLevels {
			src.GroupBy = append([]string{src.Fields.Namespace}, src.GroupBy...)
		}
		out = append(out, src)
	}
	return out
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
//
// The pod sums are Fallback: searched only when no node source matches anything.
// Sent alongside the node sums they cost time buckets x pods against
// search.max_buckets — on the dev cluster that alone refused every window longer than
// an hour — for an answer that loses to the node sum on coverage whenever both have
// data. The one case that changes: a node stream with gaps no longer loses to a
// fuller pod sum; the node series is drawn with its gaps.
func esNodeCPUUsageSources() []esMetricSource {
	return []esMetricSource{
		{
			Field: "kubernetes.node.cpu.usage.nanocores", Scale: nanocoresToCores, Agg: "avg",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		},
		{
			Field: "kubernetes.pod.cpu.usage.nanocores", Scale: nanocoresToCores, Agg: "avg",
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields, Fallback: true,
		},
		{
			Name: "system.cpu.utilization", NameField: "name.keyword", Field: "value", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esOTLPFields.Node}, Fields: esOTLPFields,
		},
		esOTelNativeSource("k8s.node.cpu.usage", esNoScale, "avg", []string{esOTelNativeFields.Node}),
		esUtilFallback(esOTelNativeSource("k8s.pod.cpu.usage", esNoScale, "avg", []string{esOTelNativeFields.Pod})),
	}
}

// esNodeMemUsageSources mirrors esNodeCPUUsageSources, pod fallbacks included.
func esNodeMemUsageSources() []esMetricSource {
	return []esMetricSource{
		{
			Field: "kubernetes.node.memory.usage.bytes", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		},
		{
			Field: "kubernetes.pod.memory.working_set.bytes", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields, Fallback: true,
		},
		{
			Name: "system.memory.usage", NameField: "name.keyword", Field: "value", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esOTLPFields.Node}, Fields: esOTLPFields,
		},
		esOTelNativeSource("k8s.node.memory.usage", esNoScale, "avg", []string{esOTelNativeFields.Node}),
		esUtilFallback(esOTelNativeSource("k8s.pod.memory.working_set", esNoScale, "avg", []string{esOTelNativeFields.Pod})),
	}
}

func esUtilFallback(src esMetricSource) esMetricSource {
	src.Fallback = true
	return src
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
	// At node and cluster scope nothing filters by namespace, so the grouping has to
	// carry it: two namespaces each running a pod called `worker-0` would otherwise
	// land in the same terms bucket and be reported as one series.
	perContainerCPU := esGroupedByNamespace(cpuUsage, nodeScope)
	perContainerMem := esGroupedByNamespace(memUsage, nodeScope)
	perContainerSpec := func(ecsField, otelMetric string) []esMetricSource {
		return esGroupedByNamespace(esSpecSources(ecsField, otelMetric), nodeScope)
	}
	if nodeScope {
		cpuUsage = esNodeCPUUsageSources()
		memUsage = esNodeMemUsageSources()
	}

	switch UtilisationKey(key) {
	case KeyCPUUsage, KeyCPUUsagePod, KeyCPUReal, KeyCPUUsageTrend:
		return esUtilMetric{Sources: cpuUsage}, true
	case KeyMemoryUsage, KeyMemReal, KeyMemUsageTrend:
		return esUtilMetric{Sources: memUsage}, true

	// The nodes list asks for these and matches each returned series to a node row,
	// so they must come back broken down per node rather than summed. Unmapped, they
	// fell to the "no Elasticsearch equivalent" branch, which returns a note WITHOUT
	// issuing a search — which is why the nodes page showed 0% with no ES query in
	// the logs at all.
	// Node filesystem, from the kubelet `node` metricset. These are node-level in
	// the Prometheus builder too (node_filesystem_* selected by instance), so cluster
	// scope sums across nodes and node scope narrows to the one.
	case KeyDiskTotal:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.node.fs.capacity.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Node}, Fields: esECSFields,
		}}}, true
	case KeyDiskUsed:
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
	// One series per claim rather than the sum below. The namespace level goes ABOVE
	// the claim for the same reason it does for pods: a claim name is unique only
	// within its namespace, and volume rightsizing keys on the pair.
	case KeyPVCUsedByClaim:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.volume.fs.used.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Namespace, esECSFields.PVC}, Fields: esECSFields, PVCScoped: true,
		}}, PerPod: true}, true
	case KeyPVCCapacityByClaim:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.volume.fs.capacity.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.Namespace, esECSFields.PVC}, Fields: esECSFields, PVCScoped: true,
		}}, PerPod: true}, true
	case KeyPVCUsage:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.volume.fs.used.bytes", Scale: esNoScale, Agg: "avg",
			GroupBy: []string{esECSFields.PVC}, Fields: esECSFields, PVCScoped: true,
		}}}, true
	case KeyPVCRequests:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.volume.fs.capacity.bytes", Scale: esNoScale, Agg: "max",
			GroupBy: []string{esECSFields.PVC}, Fields: esECSFields, PVCScoped: true,
		}}}, true

	// Replica counts describe the controller, so they come from kube-state-metrics
	// rather than from any pod-level metricset. Field names verified against the same
	// Elastic Kubernetes integration the customer runs, not inferred.
	case KeyReplicaDefined:
		return esUtilMetric{Sources: esReplicaSources(
			"kubernetes.deployment.replicas.desired", "kubernetes.replicaset.replicas.desired")}, true
	case KeyReplicaReady:
		return esUtilMetric{Sources: esReplicaSources(
			"kubernetes.deployment.replicas.available", "kubernetes.replicaset.replicas.ready")}, true

	case KeyCPUUsageLine:
		return esUtilMetric{Sources: esNodeOnlyUsageSources(esNodeCPUUsageSources()), Breakdown: true}, true
	case KeyMemoryUsageLine:
		return esUtilMetric{Sources: esNodeOnlyUsageSources(esNodeMemUsageSources()), Breakdown: true}, true

	case KeyP50CPU:
		return esUtilMetric{Sources: cpuUsage, Reduce: esReduceP50}, true
	case KeyP90CPU:
		return esUtilMetric{Sources: cpuUsage, Reduce: esReduceP90}, true
	case KeyMaxUsageCPU:
		return esUtilMetric{Sources: cpuUsage, Reduce: esReduceMax}, true
	case KeyP50Mem:
		return esUtilMetric{Sources: memUsage, Reduce: esReduceP50}, true
	case KeyP90Mem:
		return esUtilMetric{Sources: memUsage, Reduce: esReduceP90}, true
	case KeyMaxUsageMem:
		return esUtilMetric{Sources: memUsage, Reduce: esReduceMax}, true

	// Per-pod companions to the summed usage metrics, mirroring the Prometheus
	// builder's pod_count / *_max_pod keys. The evidence chart divides a workload
	// total by the pod count to put usage on the same axis as a per-pod
	// recommendation, and draws the busiest pod to show what that average hides.
	//
	// Workload and pod scope only. These read GroupBy[0] of the usage sources, which
	// is the pod there but the NODE at cluster and node scope — answering at those
	// scopes would silently count nodes and call them pods.
	case KeyPodCount:
		if nodeScope {
			return esUtilMetric{}, false
		}
		return esUtilMetric{Sources: memUsage, Series: esSeriesCount}, true
	// The rightsizing keys: one number per pod and container, not one per workload.
	// Same scope rule as the companions above, since GroupBy[0] is the pod only at
	// workload and pod scope.
	//
	// container_memory_max_usage_by_pod is deliberately absent: the cgroup
	// high-water mark has no field in any Elasticsearch layout we read, so it falls
	// through to esUtilUnsupportedNote rather than being answered with the working
	// set, which is a different number and would size a limit too low.
	//
	// container_oomkilled_limit_by_pod is absent for the same reason and falls the
	// same way: it needs a per-container terminated reason to join a limit against,
	// and no layout here carries one.
	// Per-container keys, at every scope.
	//
	// At workload and pod scope a namespace term filter already pins every document
	// to one namespace, so grouping by pod and container is enough. At node and
	// cluster scope there is no such filter, and esGroupedByNamespace adds a
	// namespace level above the pod — without it, two namespaces each running a pod
	// called `worker-0` would land in the same terms bucket and be reported as one
	// series, with no error.
	//
	// The extra level only exists in the collapsed (aggregate) path. The histogram
	// path groups at most twice, so a node-scoped request with no aggregate finds no
	// usable source and comes back empty with a note rather than a merged answer.
	case KeyContainerCPUUsageByPod:
		return esUtilMetric{Sources: perContainerCPU, PerPod: true}, true
	case KeyContainerMemoryWorkingSetByPod:
		return esUtilMetric{Sources: perContainerMem, PerPod: true}, true

	// Requests and limits in the same per-container shape as the usage keys, so a
	// consumer can join the two on (pod, container) within a namespace.
	case KeyContainerCPURequestByPod:
		return esUtilMetric{Sources: perContainerSpec("kubernetes.container.cpu.request.cores", "k8s.container.cpu_request"), PerPod: true}, true
	case KeyContainerCPULimitByPod:
		return esUtilMetric{Sources: perContainerSpec("kubernetes.container.cpu.limit.cores", "k8s.container.cpu_limit"), PerPod: true}, true
	case KeyContainerMemoryRequestByPod:
		return esUtilMetric{Sources: perContainerSpec("kubernetes.container.memory.request.bytes", "k8s.container.memory_request"), PerPod: true}, true
	case KeyContainerMemoryLimitByPod:
		return esUtilMetric{Sources: perContainerSpec("kubernetes.container.memory.limit.bytes", "k8s.container.memory_limit"), PerPod: true}, true

	case KeyCPUUsageMaxPod:
		if nodeScope {
			return esUtilMetric{}, false
		}
		return esUtilMetric{Sources: cpuUsage, Series: esSeriesMax}, true
	case KeyMemoryUsageMaxPod:
		if nodeScope {
			return esUtilMetric{}, false
		}
		return esUtilMetric{Sources: memUsage, Series: esSeriesMax}, true

	case KeyCPURequest, KeyCPURequestPod:
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.cpu.request.cores", "k8s.container.cpu_request")}, true
	case KeyCPULimit, KeyCPULimitPod:
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.cpu.limit.cores", "k8s.container.cpu_limit")}, true
	case KeyMemoryRequest:
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.memory.request.bytes", "k8s.container.memory_request")}, true
	case KeyMemoryLimit:
		return esUtilMetric{Sources: esSpecSources("kubernetes.container.memory.limit.bytes", "k8s.container.memory_limit")}, true

	case KeyCPUTotal:
		return esUtilMetric{Sources: esCapacitySource(
			"kubernetes.node.cpu.allocatable.cores", "kubernetes.node.cpu.capacity.cores",
			"k8s.node.allocatable_cpu")}, true
	case KeyMemTotal:
		return esUtilMetric{Sources: esCapacitySource(
			"kubernetes.node.memory.allocatable.bytes", "kubernetes.node.memory.capacity.bytes",
			"k8s.node.allocatable_memory")}, true

	// Network counters feed the abandoned-workload scan, which averages the values
	// as rates. Metricbeat ships cumulative byte counters, so they are differentiated
	// per second before being summed across pods.
	case KeyNetworkReceivePacket:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.pod.network.rx.bytes", Scale: esNoScale, Agg: "max", Counter: true,
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
		}}}, true
	case KeyNetworkTransmitPackets:
		return esUtilMetric{Sources: []esMetricSource{{
			Field: "kubernetes.pod.network.tx.bytes", Scale: esNoScale, Agg: "max", Counter: true,
			GroupBy: []string{esECSFields.Pod}, Fields: esECSFields,
		}}}, true
	}

	return esUtilMetric{}, false
}

// esUtilHistogramBucketBudget caps how many date_histogram buckets one search may
// ask for.
//
// Elasticsearch refuses a search that would build more than `search.max_buckets`
// (65,536 by default), and the buckets it counts are time buckets MULTIPLIED by the
// groups inside each one — so a request that is fine for one key is not for another.
// What makes it worth capping rather than catching is how long the refusal takes.
// Measured against the dev cluster on a per-claim key:
//
//	1 day  at 75s   1,152 buckets     7.5s   answered
//	3 days at 75s   3,456 buckets    42.2s   refused
//	7 days at 75s   8,064 buckets   187.6s   refused
//
// The HTTP client gives up after 30s (elasticsearch_saas.go), so api-server never
// receives the refusal — it reports a timeout, and Elasticsearch goes on building
// buckets for up to three minutes for a caller that has already hung up. Detecting
// too_many_buckets_exception in the response cannot work, because the response never
// arrives. The only place left to act is before the query is sent.
//
// 1,000 sits under the largest interval measured to answer. It is not a guarantee:
// the limit is buckets x groups and the group count is not known until the results
// come back, so a key with thousands of groups can still exceed it. It bounds the
// time-bucket half, which is the half a caller controls. The groups half is handled
// by esUtilSearchSpec.searchTier.
const esUtilHistogramBucketBudget = 1000

// esUtilStepSeconds picks the date_histogram interval. meta.Step is already derived
// from the picker range by promAggWindow ("300s"); parse it rather than deriving a
// second, differently-rounded window, so ES buckets line up with what the Prometheus
// path would have produced for the same picker range.
//
// An EXPLICIT step is widened until the window fits the bucket budget; the derived
// form is not touched. The derived form already spreads ~300 points across the range
// and its searches are what every chart runs today, so budgeting it too would change
// the resolution of queries that were never the problem. An explicit step has no such
// ceiling: a caller asking for 75s sampling over two weeks asks for 16,128 buckets.
func esUtilStepSeconds(meta RequestMetadata, startMs, endMs int64) int64 {
	rangeSec := (endMs - startMs) / 1000

	if s := strings.TrimSuffix(meta.Step, "s"); s != "" && s != meta.Step {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			return esUtilFitBuckets(v, rangeSec)
		}
	}
	if rangeSec <= 0 {
		return 300
	}
	// The derived form is left exactly as it was, budget and all. It already spreads
	// ~300 points across the range and stops at 1800s, and the searches it produces
	// are the ones every chart runs today. Applying the budget here as well widened
	// a 30-day window past that ceiling, which is a resolution change to queries that
	// were never the problem.
	step := rangeSec / 300
	if step < 60 {
		step = 60
	}
	if step > 1800 {
		step = 1800
	}
	return step
}

// esUtilFitBuckets widens a step until the window fits esUtilHistogramBucketBudget.
//
// Widening rather than refusing: a coarser answer is one a chart can still draw and a
// consumer can still read, where the alternative is a 30-second wait and nothing. The
// step only ever grows, so a request that already fits is returned untouched and no
// existing query text moves.
func esUtilFitBuckets(step, rangeSec int64) int64 {
	if step <= 0 || rangeSec <= 0 {
		return step
	}
	if buckets := rangeSec / step; buckets > esUtilHistogramBucketBudget {
		step = (rangeSec + esUtilHistogramBucketBudget - 1) / esUtilHistogramBucketBudget
	}
	return step
}

// esDefaultMaxBuckets is Elasticsearch's default `search.max_buckets`, used when a
// refusal does not say which limit it hit.
const esDefaultMaxBuckets = 65536

// esUtilProbeBuckets is how many time buckets the group-count probe splits the window
// into. One bucket over the whole window counts every pod that existed at any point in
// it, and pods churn: on the dev cluster a 7-day window holds ~6,200 pod names against
// ~530 reporting at once, which refitted the step to 28h. Eight buckets bring the
// count close to what one step really holds while keeping the probe itself far below
// the limit.
const esUtilProbeBuckets = 8

// esUtilBucketHeadroom is the share of `search.max_buckets` a refitted search aims
// for, rather than the limit itself: the histogram can add a partial bucket at each
// edge of the window, and the group count a probe returns is an estimate.
const esUtilBucketHeadroom = 0.75

// esUtilSearchSpec is everything about one utilisation search except its step and
// which sources it carries, which esUtilSearch varies while trying to get an answer.
type esUtilSearchSpec struct {
	cfg     *ElasticsearchConfig
	esURL   string
	index   string
	plan    esUtilMetric
	meta    RequestMetadata
	scope   esUtilScope
	startMs int64
	endMs   int64
}

func (s esUtilSearchSpec) render(stepSec int64, primaryOnly bool) (map[string]any, string, error) {
	body := buildESUtilisationAggQueryTier(s.plan, s.meta, s.scope, s.startMs, s.endMs, stepSec, primaryOnly)
	rendered, err := json.Marshal(body)
	return body, string(rendered), err
}

// esUtilSearch runs one utilisation search and returns the response body and the
// query that produced it.
//
// Two things keep it under Elasticsearch's `search.max_buckets`, whose count is time
// buckets x groups. On the dev cluster (~530 pods) a cluster-scope usage key was
// refused for every window longer than an hour: 300 time buckets x ~530 pods.
//
//  1. Fallback sources go out only when the primary ones find nothing. Cluster usage
//     is a sum over nodes, with a sum over pods behind it for clusters that do not
//     ship the node metricset. Sent together, the pod fallback is computed on every
//     search — and is what breaches the limit — even though the node sum wins
//     whenever it has data. See esUtilSearchSpec.withFallbackLast.
//  2. The step is widened to fit the limit. See searchTier.
func (s esUtilSearchSpec) search(stepSec int64) ([]byte, string, error) {
	if !esUtilHasFallback(s.plan) {
		return s.searchTier(stepSec, false)
	}
	out, sent, err := s.searchTier(stepSec, true)
	if err != nil || esUtilMatchedDocs(out) > 0 {
		return out, sent, err
	}
	// The primary sources matched nothing: this cluster does not ship them, so the
	// full search — fallbacks included — is the only one that can answer. Exactly
	// the search that was sent before primaries were tried first.
	return s.searchTier(stepSec, false)
}

// searchTier runs one tier of the search, at a step that fits search.max_buckets.
//
// The limit is time buckets x groups, and the group count is the half nothing knows
// in advance. Which step fits is learned in one of two ways, then remembered in
// esUtilSteps so the next load of the same window skips straight to it:
//
//   - Searches grouped by pod at cluster or node scope are measured BEFORE they are
//     sent. These are the ones a cluster's pod count can push over the limit, and
//     their refusals are too slow to wait for: a 6h cluster-scope cpu_request on the
//     dev cluster (~950 pod and container series per time bucket) took Elasticsearch
//     65s to refuse, past the 30s client timeout, while the probe took 0.2s.
//   - Everything else is sent as before and refitted only if refused. Those refusals
//     come back quickly (2-4s on the dev cluster, out to 7 days), so a search that
//     fits pays nothing extra.
func (s esUtilSearchSpec) searchTier(stepSec int64, primaryOnly bool) ([]byte, string, error) {
	key, keyErr := s.stepMemoryKey(stepSec, primaryOnly)
	remembered, known := int64(0), false
	if keyErr == nil {
		remembered, known = esUtilSteps.get(key, time.Now())
	}
	switch {
	case known:
		if remembered > stepSec {
			stepSec = remembered
		}
	case s.groupsByPodAtWideScope(primaryOnly):
		fitted, groups, err := s.measure(esDefaultMaxBuckets, primaryOnly)
		_, probeRefused := esUtilTooManyBuckets(err)
		switch {
		case probeRefused:
			// Even the eight-bucket probe is over the limit: no step can answer.
			return nil, "", err
		case err != nil:
			// The probe failed — a transient error, most likely. Send the search as
			// before rather than failing a chart on the probe's account.
			slog.Warn("ES utilisation: bucket probe failed; sending at the derived step", "err", err)
		case groups == 0:
			// The probe matched nothing, so there is nothing to size: send the search as
			// before, and let it answer "no data" the way it always has.
		case fitted == 0:
			return nil, "", fmt.Errorf("utilisation query would exceed Elasticsearch's search.max_buckets: "+
				"~%d series per time bucket in this window; narrow the scope", groups)
		default:
			if fitted > stepSec {
				slog.Info("ES utilisation: step widened to fit search.max_buckets",
					"groups_per_bucket", groups, "step_sec", stepSec, "fitted_step_sec", fitted)
				stepSec = fitted
			}
			if keyErr == nil {
				esUtilSteps.put(key, stepSec, time.Now())
			}
		}
	}

	body, rendered, err := s.render(stepSec, primaryOnly)
	if err != nil {
		return nil, "", err
	}
	out, err := esUtilPost(s.cfg, s.esURL, body)
	maxBuckets, refused := esUtilTooManyBuckets(err)
	if !refused {
		return out, rendered, err
	}

	wider, groups, measureErr := s.measure(maxBuckets, primaryOnly)
	if measureErr != nil || wider <= stepSec {
		// Not wider means too many groups for even two time buckets: no step answers
		// this, and the refusal says why better than an empty series would.
		return nil, rendered, err
	}
	retryBody, retryRendered, renderErr := s.render(wider, primaryOnly)
	if renderErr != nil {
		return nil, rendered, err
	}
	slog.Warn("ES utilisation: search refused for search.max_buckets; retrying at a wider step",
		"max_buckets", maxBuckets, "groups_per_bucket", groups, "step_sec", stepSec, "retry_step_sec", wider)
	out, err = esUtilPost(s.cfg, s.esURL, retryBody)
	if err == nil && keyErr == nil {
		esUtilSteps.put(key, wider, time.Now())
	}
	return out, retryRendered, err
}

// measure counts the groups one time bucket of this search holds, and returns the
// step at which the window fits under maxBuckets — 0 when not even two time buckets
// do.
//
// The count comes from a coarse probe of the same search: a group appears in a probe
// bucket if it reported at any point inside it, so the busiest probe bucket bounds
// what one step can hold. A search with no histogram (the per-pod collapsed form)
// renders the same body again; measured after a refusal it is refused again, which
// ends the attempt.
func (s esUtilSearchSpec) measure(maxBuckets int64, primaryOnly bool) (fitted, groups int64, err error) {
	rangeSec := (s.endMs - s.startMs) / 1000
	if rangeSec <= 0 {
		return 0, 0, errors.New("empty window")
	}
	probeStep := (rangeSec + esUtilProbeBuckets - 1) / esUtilProbeBuckets
	probeBody, _, err := s.render(probeStep, primaryOnly)
	if err != nil {
		return 0, 0, err
	}
	probe, err := esUtilPost(s.cfg, s.esURL, probeBody)
	if err != nil {
		return 0, 0, err
	}
	groups, err = esUtilGroupsPerBucket(probe)
	if err != nil {
		return 0, 0, err
	}
	return esUtilStepForBucketLimit(rangeSec, groups, maxBuckets), groups, nil
}

// groupsByPodAtWideScope reports whether this tier of the search sums series per pod
// across a whole cluster or node — the searches whose bucket count a cluster's size
// can push past the limit, and so the ones measured before they are sent. Workload
// and pod scopes are bounded by one workload's pods, and the per-pod collapsed form
// has no histogram to size.
func (s esUtilSearchSpec) groupsByPodAtWideScope(primaryOnly bool) bool {
	if s.scope != esScopeCluster && s.scope != esScopeNode {
		return false
	}
	if s.plan.PerPod && s.meta.Aggregate.Name != "" {
		return false
	}
	for _, src := range s.plan.Sources {
		if esSourceUnusableAtScope(src, s.meta, s.scope) || (primaryOnly && src.Fallback) {
			continue
		}
		for _, g := range src.GroupBy {
			if g != "" && g == src.Fields.Pod {
				return true
			}
		}
	}
	return false
}

// stepMemoryKey identifies a search for esUtilSteps independently of when it runs: the
// connection and index it reads, plus the same search rendered over [0, window)
// instead of [start, end). Keyed on the rendered body for the reason the query groups
// are — a hand-listed signature goes stale the first time a filter is added.
//
// The connection is the integration rather than its URL: two integrations on one URL
// can authenticate as users who see different documents, and so different pod
// counts. The index stays in the key because one integration resolves a different
// index per account, and a request may name its own. A config built without an
// integration falls back to the URL, which carries the index.
func (s esUtilSearchSpec) stepMemoryKey(stepSec int64, primaryOnly bool) (string, error) {
	normalised := s
	normalised.startMs, normalised.endMs = 0, s.endMs-s.startMs
	_, rendered, err := normalised.render(stepSec, primaryOnly)
	if err != nil {
		return "", err
	}
	conn := "url:" + s.esURL
	if s.cfg != nil && s.cfg.IntegrationID != "" {
		conn = "integration:" + s.cfg.IntegrationID + "\nindex:" + s.index
	}
	return hashPayload(conn + "\n" + rendered), nil
}

// esUtilStepMemoryTTL is how long a refitted step is reused before the search is tried
// at its own step again. Long enough that a panel reloaded or revisited does not pay
// for the refusal and the probe each time; short enough that a cluster which shrank
// gets its resolution back, and one which grew is refitted again on its next refusal.
const esUtilStepMemoryTTL = 10 * time.Minute

// esUtilStepMemoryMax bounds the memory. Keys are per search and window length, so a
// busy api-server holds a few hundred at most; past this, expired entries are swept
// and, if that frees nothing, the memory starts over rather than growing.
const esUtilStepMemoryMax = 4096

type esUtilStepMemory struct {
	mu    sync.Mutex
	steps map[string]esUtilRememberedStep
}

type esUtilRememberedStep struct {
	step    int64
	expires time.Time
}

// esUtilSteps remembers, per search, the step a refusal was refitted to. Per
// api-server pod: each pod pays for one refusal per search and window length per TTL.
var esUtilSteps = &esUtilStepMemory{steps: map[string]esUtilRememberedStep{}}

func (m *esUtilStepMemory) get(key string, now time.Time) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.steps[key]
	if !ok || now.After(r.expires) {
		return 0, false
	}
	return r.step, true
}

func (m *esUtilStepMemory) put(key string, step int64, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.steps) >= esUtilStepMemoryMax {
		for k, r := range m.steps {
			if now.After(r.expires) {
				delete(m.steps, k)
			}
		}
		if len(m.steps) >= esUtilStepMemoryMax {
			m.steps = map[string]esUtilRememberedStep{}
		}
	}
	m.steps[key] = esUtilRememberedStep{step: step, expires: now.Add(esUtilStepMemoryTTL)}
}

// esUtilHasFallback reports whether a plan has sources that should only be searched
// when its primary sources find nothing.
func esUtilHasFallback(m esUtilMetric) bool {
	for _, src := range m.Sources {
		if src.Fallback {
			return true
		}
	}
	return false
}

// esUtilMatchedDocs is how many documents the candidate sources of a response matched
// in total. Zero means none of them is shipped by this cluster.
func esUtilMatchedDocs(body []byte) int64 {
	var resp esUtilAggResponse
	if json.Unmarshal(body, &resp) != nil {
		return 0
	}
	var matched int64
	for name, raw := range resp.Aggregations {
		if !strings.HasPrefix(name, "src_") {
			continue
		}
		var sr struct {
			DocCount int64 `json:"doc_count"`
		}
		if json.Unmarshal(raw, &sr) == nil {
			matched += sr.DocCount
		}
	}
	return matched
}

func esUtilPost(cfg *ElasticsearchConfig, esURL string, body map[string]any) ([]byte, error) {
	resp, err := esRequestJSON("POST", esURL, body, cfg) //nolint:bodyclose // readResponse closes it
	if err != nil {
		return nil, err
	}
	return readResponse(resp, "utilisation query")
}

// esErrorCause is the part of an Elasticsearch error body that says what failed. The
// bucket refusal arrives nested — as `caused_by` of a search_phase_execution_exception
// with an empty root_cause — so every level is searched.
type esErrorCause struct {
	Type       string         `json:"type"`
	MaxBuckets int64          `json:"max_buckets"`
	CausedBy   *esErrorCause  `json:"caused_by"`
	RootCause  []esErrorCause `json:"root_cause"`
}

func (c *esErrorCause) tooManyBuckets() (int64, bool) {
	if c == nil {
		return 0, false
	}
	if c.Type == "too_many_buckets_exception" {
		if c.MaxBuckets > 0 {
			return c.MaxBuckets, true
		}
		return esDefaultMaxBuckets, true
	}
	for i := range c.RootCause {
		if limit, ok := c.RootCause[i].tooManyBuckets(); ok {
			return limit, true
		}
	}
	return c.CausedBy.tooManyBuckets()
}

// esUtilTooManyBuckets reports whether err is Elasticsearch refusing a search for
// `search.max_buckets`, and the limit that cluster enforces.
func esUtilTooManyBuckets(err error) (int64, bool) {
	var statusErr *esStatusError
	if !errors.As(err, &statusErr) {
		return 0, false
	}
	var envelope struct {
		Error esErrorCause `json:"error"`
	}
	if json.Unmarshal(statusErr.body, &envelope) != nil {
		return 0, false
	}
	return envelope.Error.tooManyBuckets()
}

// esUtilGroupsPerBucket reads a probe response and returns how many buckets one time
// bucket costs: the time bucket itself, its groups, and their subgroups, taking the
// largest time bucket per source. Sources are summed because Elasticsearch counts
// every aggregation in the search against the same limit.
func esUtilGroupsPerBucket(body []byte) (int64, error) {
	var resp esUtilAggResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	var total int64
	for name, raw := range resp.Aggregations {
		if !strings.HasPrefix(name, "src_") {
			continue
		}
		var sr esUtilSourceResult
		if err := json.Unmarshal(raw, &sr); err != nil {
			return 0, err
		}
		var largest int64
		for _, b := range sr.TS.Buckets {
			n := int64(1 + len(b.G0.Buckets))
			for _, g := range b.G0.Buckets {
				n += int64(len(g.G1.Buckets))
			}
			if n > largest {
				largest = n
			}
		}
		total += largest
	}
	return total, nil
}

// esUtilStepForBucketLimit returns the smallest step at which a window of rangeSec,
// costing groupsPerBucket buckets per time bucket, stays under maxBuckets with
// headroom. Returns 0 when not even two time buckets fit, since a single point is
// not a series — and when there is no window or no groups to size, which callers
// must tell apart from that by checking groupsPerBucket themselves.
func esUtilStepForBucketLimit(rangeSec, groupsPerBucket, maxBuckets int64) int64 {
	if rangeSec <= 0 || groupsPerBucket <= 0 {
		return 0
	}
	timeBuckets := int64(float64(maxBuckets)*esUtilBucketHeadroom) / groupsPerBucket
	if timeBuckets < 2 {
		return 0
	}
	// A histogram over the window at step s holds up to rangeSec/s + 1 buckets, the
	// extra one being the partial bucket where the window does not align to s.
	return (rangeSec + timeBuckets - 2) / (timeBuckets - 1)
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
	} else if meta.PodNamesSupplied && src.Fields.Pod != "" && !src.WorkloadScoped {
		// An exact list beats the prefix, which is wrong twice over: it misses pods
		// that no longer exist, and "services-server-" also prefixes
		// "services-server-worker-...", so one workload gets sized on another's pods.
		//
		// An empty list is the caller saying it resolved the pods and found none,
		// which is an answer rather than a missing parameter, so the filter matches
		// nothing.
		//
		// Workload-scoped sources are excluded: replica counts come from the
		// deployment/replicaset dataset, whose documents carry no pod field, so a
		// pod filter there matches nothing and would zero the count rather than
		// leave it unfiltered.
		filters = append(filters, map[string]any{"terms": map[string]any{src.Fields.Pod: esPodNameValues(meta.PodNames)}})
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
	// One stats_bucket over the same path gives the busiest pod and the pod count for
	// free. Emitted on every query rather than only when a key asks for it: the
	// rendered body is the dedup key, so keeping it identical is what lets pod_count
	// ride memory_usage's search and cpu_usage_max_pod ride cpu_usage's, instead of
	// forking two more round trips per chart.
	tsAggs["series"] = map[string]any{"stats_bucket": map[string]any{"buckets_path": totalPath}}
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
	return buildESUtilisationAggQueryTier(m, meta, scope, startMs, endMs, stepSec, false)
}

// buildESUtilisationAggQueryTier is buildESUtilisationAggQuery, leaving out Fallback
// sources when primaryOnly is set. Omitted sources keep their positional names, so the
// response parses against the full plan.
func buildESUtilisationAggQueryTier(m esUtilMetric, meta RequestMetadata, scope esUtilScope, startMs, endMs, stepSec int64, primaryOnly bool) map[string]any {
	aggs := make(map[string]any, len(m.Sources))
	for i, src := range m.Sources {
		if esSourceUnusableAtScope(src, meta, scope) || (primaryOnly && src.Fallback) {
			continue
		}
		var agg map[string]any
		if m.PerPod && meta.Aggregate.Name != "" {
			// One bucket per pod and no histogram: the statistic over the pod's raw
			// samples is cheaper than, and more exact than, one over step averages.
			agg = esPerPodCollapsedAgg(src, meta, scope)
		} else {
			agg = esSourceAgg(src, meta, scope, stepSec)
		}
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
	// stats_bucket over the same per-pod path `total` sums. `count` is how many pods
	// contributed, `max` is the busiest one.
	Series struct {
		Count *float64 `json:"count"`
		Max   *float64 `json:"max"`
	} `json:"series"`
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
//
// `read` selects the busiest pod or the pod count instead of the sum. Neither is ever
// asked of a cumulative counter, so both are answered ahead of the counter branch
// rather than having to compose with a derivative.
func esUtilBucketValue(b esUtilBucket, counter bool, read esUtilSeriesRead) (float64, bool) {
	switch read {
	case esSeriesMax:
		if b.Series.Max == nil {
			return 0, false
		}
		return *b.Series.Max, true
	case esSeriesCount:
		// A bucket with no pods reporting is a gap, not a workload of zero pods:
		// dividing a usage total by it would be the bug this metric exists to fix.
		if b.Series.Count == nil || *b.Series.Count <= 0 {
			return 0, false
		}
		return *b.Series.Count, true
	}
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
			if _, ok := esUtilBucketValue(b, counter, m.Series); ok {
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
	if m.PerPod && meta.Aggregate.Name != "" {
		return parseESPerPodResponse(resp, m, meta)
	}

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

		if m.PerPod {
			// No Aggregate, so no collapse - but the grouping still holds. Falling
			// through to the summed path below would return one workload line under
			// a key whose name promises one series per pod.
			if series := esPerPodHistogramSeries(sr, src, esPerPodScale(src, meta), labels); len(series) > 0 {
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
			v, ok := esUtilBucketValue(b, src.Counter, m.Series)
			if !ok {
				continue
			}
			timestamps = append(timestamps, b.Key/1000)
			// A pod count is a count, never a quantity in the source's units: scaling
			// it by nanocoresToCores would return 3e-9 pods.
			if m.Series == esSeriesCount {
				values = append(values, v)
			} else {
				values = append(values, v*src.Scale)
			}
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

// esSearchInto posts one _search and decodes it, so the two queries above do not each
// repeat the request/read/unmarshal dance.
func esSearchInto(ctx *security.RequestContext, cfg *ElasticsearchConfig, index string, body map[string]any, into any) error {
	url := fmt.Sprintf("%s/%s/_search", cfg.Url, index)
	resp, err := esRequestJSON("POST", url, body, cfg) //nolint:bodyclose
	if err != nil {
		return err
	}
	raw, err := readResponse(resp, "noisy neighbours query")
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}
