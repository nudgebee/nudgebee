package observability

// Canonical label names for the utilisation layer.
//
// Every provider names the same Kubernetes dimension differently — Prometheus says
// `pod`, Datadog `pod_name`, New Relic `podName`, Elasticsearch
// `kubernetes.pod.name` or `resource.attributes.k8s.pod.name` depending on which
// document layout the cluster writes. A consumer that wants "which pod is this
// series?" therefore has to learn every provider's vocabulary, which is the thing
// this layer exists to stop.
//
// The rule is narrow on purpose (see canonicaliseUtilisationLabels): only the
// identity of the object being queried is renamed, never the identity of something
// it talked to.
const (
	CanonicalPod       = "pod"
	CanonicalContainer = "container"
	CanonicalNamespace = "namespace"
	CanonicalNode      = "node"
	CanonicalWorkload  = "workload"
)

// canonicalMetricLabels maps a provider's own label name to the canonical one, per
// provider. A provider with no entry already speaks canonically:
//
//   - Prometheus, VictoriaMetrics, Chronosphere, CubeAPM and OpenObserve emit
//     `pod`/`container`/`namespace`/`node` directly.
//   - SolarWinds is translated at parse time by swAttrToLabel, which is where this
//     convention started.
//   - Elasticsearch synthesises its labels in esUtilSeriesLabels and already uses
//     these names.
//
// Only Datadog, New Relic and Dynatrace need a rename, and Dynatrace only for its
// workload dimension.
var canonicalMetricLabels = map[string]map[string]string{
	"datadog": {
		"pod_name":            CanonicalPod,
		"kube_container_name": CanonicalContainer,
		"kube_namespace":      CanonicalNamespace,
		"host":                CanonicalNode,
		"kube_node":           CanonicalNode,
		// Datadog names the workload tag after its kind; all of them answer the same
		// question. Spellings are taken from controllerFacets
		// (integrations/datadog_webhook.go:763), which is this repo's authoritative
		// list — note `kube_cronjob`, not `kube_cron_job`, which is what
		// ml-k8s-server's KIND_TO_TAG has wrong in two places today.
		"kube_deployment":             CanonicalWorkload,
		"kube_stateful_set":           CanonicalWorkload,
		"kube_daemon_set":             CanonicalWorkload,
		"kube_replica_set":            CanonicalWorkload,
		"kube_replication_controller": CanonicalWorkload,
		"kube_job":                    CanonicalWorkload,
		"kube_cronjob":                CanonicalWorkload,
	},
	// OpenObserve is fed by the OTel collector's kubeletstats receiver, which uses the
	// k8s semantic-convention attribute names rather than cAdvisor's. Without this the
	// values arrive correct under names nothing joins on -- the same silent empty join
	// that cost Elasticsearch its volume recommendations.
	"openobserve": {
		"k8s_pod_name":        CanonicalPod,
		"k8s_container_name":  CanonicalContainer,
		"k8s_namespace_name":  CanonicalNamespace,
		"k8s_node_name":       CanonicalNode,
		"k8s_deployment_name": CanonicalWorkload,
	},
	"newrelic": {
		"podName":        CanonicalPod,
		"containerName":  CanonicalContainer,
		"namespaceName":  CanonicalNamespace,
		"nodeName":       CanonicalNode,
		"deploymentName": CanonicalWorkload,
		"daemonsetName":  CanonicalWorkload,
	},
	"dynatrace": {
		// Grail already emits namespace/pod/container/node; only the workload
		// dimension arrives under a different name.
		"workload_name": CanonicalWorkload,
	},
	// OTel resource attributes, which survive HEC with their dots intact. Only the
	// per-container keys carry labels; the summed keys have none to rename.
	"splunk_enterprise": {
		"k8s.namespace.name": CanonicalNamespace,
		"k8s.pod.name":       CanonicalPod,
		"k8s.container.name": CanonicalContainer,
		"k8s.node.name":      CanonicalNode,
	},
	// SigNoz keeps the OTel resource attributes' dotted names as PromQL labels.
	"signoz": {
		"k8s.namespace.name":   CanonicalNamespace,
		"k8s.pod.name":         CanonicalPod,
		"k8s.container.name":   CanonicalContainer,
		"k8s.node.name":        CanonicalNode,
		"k8s.deployment.name":  CanonicalWorkload,
		"k8s.statefulset.name": CanonicalWorkload,
		"k8s.daemonset.name":   CanonicalWorkload,
		"k8s.job.name":         CanonicalWorkload,
		"k8s.cronjob.name":     CanonicalWorkload,
	},
}

// canonicalSourcePriority breaks a tie when a series carries two labels that map to
// the same canonical name with different values — a Datadog pod owned by a CronJob
// carries both `kube_cronjob` and `kube_job`, and Go's map iteration order would
// otherwise pick between them at random, giving the same workload a different
// `workload` value request to request.
//
// The order is controllerFacets' (integrations/datadog_webhook.go:763), which exists
// for this same question and prefers the outermost controller: Datadog resolves a
// pod's ReplicaSet up to its Deployment, so `kube_deployment` carries the clean name.
// Sources not listed here never collide, because each maps to its own canonical name.
var canonicalSourcePriority = map[string][]string{
	"datadog": {
		"kube_deployment", "kube_stateful_set", "kube_daemon_set",
		"kube_replica_set", "kube_replication_controller", "kube_job", "kube_cronjob",
	},
	"newrelic": {"deploymentName", "daemonsetName"},
	// Outermost controller first, as for Datadog: a CronJob's pod carries both.
	"signoz": {"k8s.deployment.name", "k8s.statefulset.name", "k8s.daemonset.name", "k8s.cronjob.name", "k8s.job.name"},
}

// canonicalSourceRank returns a sort key for a provider's source label: lower wins.
// An unlisted source ranks last, and ties among unlisted ones fall back to the label
// name so the outcome never depends on map order.
func canonicalSourceRank(provider, label string) int {
	for i, candidate := range canonicalSourcePriority[provider] {
		if candidate == label {
			return i
		}
	}
	return len(canonicalSourcePriority[provider]) + 1
}

// canonicalLabelExempt lists labels that must never be canonicalised even though
// they look like an identity dimension.
//
// `destination_workload_name` and its actual_/namespace_ siblings name the CALLEE of
// a request, not the object the query is about. Three screens read them by name —
// KubernetesApplicationGrouping renders one row per destination, HttpLatencyTable
// keys its rows on them, and the status chart reads the actual_ variant — so a
// rename blanks those screens. Folding them into `workload` would also give one
// label two meanings, because Elasticsearch already sets `workload` to the QUERIED
// workload.
func canonicalLabelExempt(label string) bool {
	switch label {
	case "destination_workload_name", "destination_workload_namespace",
		"actual_destination_workload_name", "actual_destination_workload_namespace",
		"src_workload_name", "src_workload_namespace":
		return true
	}
	return false
}
