package core

import (
	"log/slog"
	"regexp"
	"strings"
)

// cloudCollectorWorkloadName is the workload Nudgebee's own cloud-collector
// runs under in every connected cluster. It calls AWS's regional
// control-plane API directly to poll inventory; those endpoints are not a
// customer dependency.
const cloudCollectorWorkloadName = "cloud-collector-server"

// awsRegionalControlPlaneHost matches a bare AWS regional API endpoint —
// <service>.<region>.amazonaws.com, e.g. "elasticache.sa-east-1.amazonaws.com"
// or "kafka.ap-northeast-3.amazonaws.com". The region group allows 2-4
// hyphenated segments so GovCloud/ISO regions match too ("us-gov-west-1",
// "us-iso-east-1"), not just the standard 3-segment form. Resource-specific
// endpoints (a particular RDS instance, ElastiCache cluster, API Gateway,
// ...) carry extra host labels and don't match this; those are handled by
// cloud_enrichment's Route53/cloud_resources matching + CollapseEnrichedExternalServices instead.
var awsRegionalControlPlaneHost = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.[a-z]{2,4}(-[a-z]+)+-\d+\.amazonaws\.com$`)

// DropCollectorOnlyAWSEndpoints removes ExternalService nodes that represent
// AWS's own regional control-plane API (no single cloud_resources row maps to
// "elasticache.sa-east-1.amazonaws.com", so cloud_enrichment never matches and
// collapses them) when every observed caller is Nudgebee's own
// cloud-collector-server workload. A node any customer workload also calls is
// left alone.
func DropCollectorOnlyAWSEndpoints(
	nodes []*DbNode,
	edges []*DbEdge,
	logger *slog.Logger,
) (prunedNodes []*DbNode, prunedEdges []*DbEdge, droppedCount int) {
	if logger == nil {
		logger = slog.Default()
	}

	nodeByID := make(map[string]*DbNode, len(nodes))
	for _, n := range nodes {
		if n != nil {
			nodeByID[n.ID] = n
		}
	}

	candidates := make(map[string]bool)
	for _, n := range nodes {
		if n == nil || n.NodeType != NodeTypeExternalService || n.Source == ManualNodeSource {
			continue
		}
		name, _ := n.Properties["name"].(string)
		if awsRegionalControlPlaneHost.MatchString(strings.ToLower(name)) {
			candidates[n.ID] = true
		}
	}
	if len(candidates) == 0 {
		return nodes, edges, 0
	}

	hasAnyCaller := make(map[string]bool)
	hasNonCollectorCaller := make(map[string]bool)
	for _, e := range edges {
		if e == nil || e.RelationshipType != RelationshipCalls || !candidates[e.DestinationNodeID] {
			continue
		}
		hasAnyCaller[e.DestinationNodeID] = true
		if !isCloudCollectorWorkload(nodeByID[e.SourceNodeID]) {
			hasNonCollectorCaller[e.DestinationNodeID] = true
		}
	}

	toDrop := make(map[string]bool)
	for esID := range candidates {
		if hasAnyCaller[esID] && !hasNonCollectorCaller[esID] {
			toDrop[esID] = true
		}
	}
	if len(toDrop) == 0 {
		return nodes, edges, 0
	}

	prunedNodes = make([]*DbNode, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && toDrop[n.ID] {
			continue
		}
		prunedNodes = append(prunedNodes, n)
	}

	prunedEdges = make([]*DbEdge, 0, len(edges))
	for _, e := range edges {
		if e == nil || toDrop[e.SourceNodeID] || toDrop[e.DestinationNodeID] {
			continue
		}
		prunedEdges = append(prunedEdges, e)
	}

	logger.Info("dropped collector-only AWS control-plane endpoints",
		"dropped_count", len(toDrop),
		"input_nodes", len(nodes),
		"output_nodes", len(prunedNodes),
		"input_edges", len(edges),
		"output_edges", len(prunedEdges))

	return prunedNodes, prunedEdges, len(toDrop)
}

// isCloudCollectorWorkload reports whether n is the cloud-collector-server
// workload, identified by name alone (not NodeType) since a caller may be
// matched as either a Workload or a Service node depending on which source
// resolved it first.
func isCloudCollectorWorkload(n *DbNode) bool {
	if n == nil {
		return false
	}
	name, _ := n.Properties["name"].(string)
	return strings.EqualFold(name, cloudCollectorWorkloadName)
}
