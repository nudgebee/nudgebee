package flow_sources

import "nudgebee/services/knowledge_graph/core"

// EdgeSourcePriority defines the priority level for edge sources.
// Lower number = higher priority (wins in conflicts when same edge is created by multiple sources).
// Note: This is separate from FlowSourcePriority which defines execution order.
type EdgeSourcePriority int

const (
	EdgePriority1 EdgeSourcePriority = 1 // Highest priority (k8s)
	EdgePriority2 EdgeSourcePriority = 2 // aws
	EdgePriority3 EdgeSourcePriority = 3 // ebpf
	EdgePriority4 EdgeSourcePriority = 4 // traces
	EdgePriority5 EdgeSourcePriority = 5 // datadog_apm
	EdgePriority6 EdgeSourcePriority = 6 // Lowest priority (unknown sources)
)

// Aliases for readability
const (
	EdgePriorityHighest = EdgePriority1
	EdgePriorityLowest  = EdgePriority6
)

// EdgeTypePriorities defines source priority for each edge type.
// When multiple flow sources create the same edge (same source node, dest node, and edge type),
// the source with the highest priority (lowest number) becomes the primary source.
// Properties from lower priority sources are merged with source prefix (e.g., traces_latency_ms).
//
// Priority order: k8s > aws > ebpf > traces > datadog_apm
// Rationale: Infrastructure sources (k8s, aws) have most authoritative data about
// their own resources, followed by observability sources (ebpf, traces, datadog).
var EdgeTypePriorities = map[core.RelationshipType]map[string]EdgeSourcePriority{
	core.RelationshipCalls: {
		"k8s":         EdgePriority1, // K8s has authoritative service-to-service data
		"aws":         EdgePriority2, // AWS has authoritative cloud resource data
		"ebpf":        EdgePriority3, // eBPF has accurate network-level data
		"traces":      EdgePriority4, // Traces has rich application-level data
		"datadog-apm": EdgePriority5, // External APM source
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
		"k8s":         EdgePriority1,
		"aws":         EdgePriority2, // AWS SNS/SQS/Kinesis
		"ebpf":        EdgePriority3,
		"traces":      EdgePriority4,
		"datadog-apm": EdgePriority5,
	},
	core.RelationshipSubscribesTo: {
		"k8s":         EdgePriority1,
		"aws":         EdgePriority2, // AWS SQS/Kinesis consumers
		"ebpf":        EdgePriority3,
		"traces":      EdgePriority4,
		"datadog-apm": EdgePriority5,
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
var MetricsToMerge = []string{
	"latency_ms",
	"request_count",
	"failure_count",
	"bytes_sent",
	"bytes_received",
	"error_rate",
	"throughput",
	"response_time",
}
