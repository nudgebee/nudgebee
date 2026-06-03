package flow_sources

import "nudgebee/services/knowledge_graph/core"

// EdgeSourcePriority defines the priority level for edge sources.
// Lower number = higher priority (wins in conflicts when same edge is created by multiple sources).
// Note: This is separate from FlowSourcePriority which defines execution order.
type EdgeSourcePriority int

// TODO: collapse the duplication between this table and the canonical one at
// core/helpers.go:edgeTypePriorities. core.DeduplicateEdgesWithPriority uses
// the core/helpers.go table; this one only stamps the source_priority property
// on edges via BaseFlowSource.CreateEdge. Both must stay in sync until merged.
const (
	EdgePriority1 EdgeSourcePriority = 1 // Highest priority (k8s)
	EdgePriority2 EdgeSourcePriority = 2 // manual (user-declared dependencies)
	EdgePriority3 EdgeSourcePriority = 3 // aws
	EdgePriority4 EdgeSourcePriority = 4 // ebpf
	EdgePriority5 EdgeSourcePriority = 5 // traces
	EdgePriority6 EdgeSourcePriority = 6 // datadog-apm
	EdgePriority7 EdgeSourcePriority = 7 // newrelic-apm
	EdgePriority8 EdgeSourcePriority = 8 // Lowest priority (unknown sources)
)

// Aliases for readability
const (
	EdgePriorityHighest = EdgePriority1
	EdgePriorityLowest  = EdgePriority8
)

// EdgeTypePriorities defines source priority for each edge type.
// When multiple flow sources create the same edge (same source node, dest node, and edge type),
// the source with the highest priority (lowest number) becomes the primary source.
// Properties from lower priority sources are merged with source prefix (e.g., traces_latency_ms).
//
// Priority order: k8s > manual > aws > ebpf > traces > datadog-apm > newrelic-apm
// Rationale: Infrastructure sources (k8s) are most authoritative for their own
// resources. Human-declared edges (manual) reflect intent and override
// inferred observability sources, but never structural k8s topology. AWS
// (structural cloud resources) follows. Observability sources (ebpf, traces,
// APM) come last — they capture observed behaviour, which manual overrides
// only when the operator explicitly declares it.
//
// Edges from the manual flow source apply only to CALLS, PUBLISHES_TO, and
// SUBSCRIBES_TO. ResolvesTo / RoutesTo / RoutesThrough are structural and not
// user-declarable in Phase 1.
var EdgeTypePriorities = map[core.RelationshipType]map[string]EdgeSourcePriority{
	core.RelationshipCalls: {
		"k8s":          EdgePriority1, // K8s has authoritative service-to-service data
		"manual":       EdgePriority2, // User-declared dependency (intent)
		"aws":          EdgePriority3, // AWS has authoritative cloud resource data
		"ebpf":         EdgePriority4, // eBPF has accurate network-level data
		"traces":       EdgePriority5, // Traces has rich application-level data
		"datadog-apm":  EdgePriority6, // External APM source (instrumentation-derived)
		"newrelic-apm": EdgePriority7, // External APM source (NRQL Span aggregation)
	},
	core.RelationshipResolvesTo: {
		"k8s":              EdgePriority1, // K8s DNS resolution
		"aws":              EdgePriority2, // AWS Route53/DNS
		"dns_resolver":     EdgePriority3, // DNS resolution
		"cloud_enrichment": EdgePriority4, // Cloud API-based resolution
		"ip_mapper":        EdgePriority5, // IP-based resolution
	},
	core.RelationshipRoutesTo: {
		"k8s":              EdgePriority1, // K8s ingress/service routing
		"aws":              EdgePriority2, // AWS ALB/NLB routing
		"cloud_enrichment": EdgePriority3, // Cloud API routing data
		"dns_resolver":     EdgePriority4, // DNS-based discovery
	},
	core.RelationshipRoutesToBackend: {
		"k8s":              EdgePriority1,
		"aws":              EdgePriority2,
		"cloud_enrichment": EdgePriority3,
		"dns_resolver":     EdgePriority4,
	},
	core.RelationshipRoutesToService: {
		"k8s":              EdgePriority1,
		"aws":              EdgePriority2,
		"cloud_enrichment": EdgePriority3,
		"dns_resolver":     EdgePriority4,
	},
	core.RelationshipRoutesThrough: {
		"k8s":              EdgePriority1,
		"aws":              EdgePriority2,
		"cloud_enrichment": EdgePriority3,
	},
	core.RelationshipPublishesTo: {
		"k8s":          EdgePriority1,
		"manual":       EdgePriority2, // User-declared pub/sub
		"aws":          EdgePriority3, // AWS SNS/SQS/Kinesis
		"ebpf":         EdgePriority4,
		"traces":       EdgePriority5,
		"datadog-apm":  EdgePriority6,
		"newrelic-apm": EdgePriority7,
	},
	core.RelationshipSubscribesTo: {
		"k8s":          EdgePriority1,
		"manual":       EdgePriority2, // User-declared pub/sub
		"aws":          EdgePriority3, // AWS SQS/Kinesis consumers
		"ebpf":         EdgePriority4,
		"traces":       EdgePriority5,
		"datadog-apm":  EdgePriority6,
		"newrelic-apm": EdgePriority7,
	},
}

// GetEdgeSourcePriority returns the priority for a source creating a specific edge type.
// If the source or edge type is not in the priority map, returns EdgePriorityLowest.
func GetEdgeSourcePriority(source string, edgeType core.RelationshipType) EdgeSourcePriority {
	if priorities, ok := EdgeTypePriorities[edgeType]; ok {
		if priority, ok := priorities[source]; ok {
			return priority
		}
	}
	return EdgePriorityLowest // Unknown sources get lowest priority
}

// IsHigherPriority returns true if priority1 is higher than priority2.
// Remember: lower number = higher priority.
func IsHigherPriority(priority1, priority2 EdgeSourcePriority) bool {
	return priority1 < priority2
}

// MetricsToMerge defines which edge properties should be merged with source prefix
// when edges from multiple sources are deduplicated.
// These are typically metrics that may have different values from different sources.
// manual_declared / manual_dependency_id / declared_by_user_id are intentionally
// here as well: when k8s wins the dedup over a manual edge, we still want the
// merged property bag to retain "this edge is also declared by a user" so the
// UI diff view can render the corroboration without re-querying.
var MetricsToMerge = []string{
	"latency_ms",
	"request_count",
	"failure_count",
	"bytes_sent",
	"bytes_received",
	"error_rate",
	"throughput",
	"response_time",
	"manual_declared",
	"manual_dependency_id",
	"declared_by_user_id",
}
