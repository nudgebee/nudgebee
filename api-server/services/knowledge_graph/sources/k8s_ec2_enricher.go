package sources

import (
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	RegisterCrossSourceEnricherFactory("k8s_ec2", func(logger *slog.Logger) core.CrossSourceEnricherInterface {
		return NewK8sEC2Enricher(logger)
	}, "Links K8s Node nodes to their underlying AWS EC2 ComputeInstance via providerID")
}

// K8sEC2Enricher creates RUNS_ON edges from K8s Node → AWS ComputeInstance (EC2)
// by parsing the providerID field stored on each K8s Node node.
// providerID format: "aws:///availability-zone/i-xxxxxxxxx"
type K8sEC2Enricher struct {
	logger *slog.Logger
}

func NewK8sEC2Enricher(logger *slog.Logger) *K8sEC2Enricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &K8sEC2Enricher{logger: logger}
}

func (e *K8sEC2Enricher) GetName() string {
	return "k8s_ec2"
}

// EnrichCrossSources scans all K8s Node nodes, extracts the EC2 instance ID from
// providerID, and creates a RUNS_ON edge to the matching ComputeInstance node.
func (e *K8sEC2Enricher) EnrichCrossSources(
	_ *security.RequestContext,
	allNodes []*core.DbNode,
	allEdges []*core.DbEdge,
	tenantID string,
) ([]*core.DbNode, []*core.DbEdge, error) {

	// Index ComputeInstance nodes by resource_id for O(1) lookup
	ec2ByInstanceID := make(map[string]*core.DbNode)
	for _, n := range allNodes {
		if n.NodeType == core.NodeTypeComputeInstance {
			if rid, _ := n.Properties["resource_id"].(string); rid != "" {
				ec2ByInstanceID[rid] = n
			}
		}
	}

	if len(ec2ByInstanceID) == 0 {
		e.logger.Debug("k8s_ec2_enricher: no ComputeInstance nodes found, skipping")
		return allNodes, allEdges, nil
	}

	newEdges := make([]*core.DbEdge, 0)

	for _, n := range allNodes {
		if n.NodeType != core.NodeTypeNode {
			continue
		}

		providerID, _ := n.Properties["providerID"].(string)
		if providerID == "" {
			continue
		}

		instanceID := parseEC2InstanceIDFromProviderID(providerID)
		if instanceID == "" {
			e.logger.Debug("k8s_ec2_enricher: could not parse instance ID from providerID",
				"node_name", n.Properties["name"],
				"provider_id", providerID)
			continue
		}

		ec2Node, found := ec2ByInstanceID[instanceID]
		if !found {
			e.logger.Debug("k8s_ec2_enricher: EC2 node not found for K8s node",
				"node_name", n.Properties["name"],
				"instance_id", instanceID)
			continue
		}

		edge := core.NewEdge(
			n.ID,
			ec2Node.ID,
			core.RelationshipRunsOn,
			map[string]interface{}{
				"connection_type": "ec2_provider_id",
				"instance_id":     instanceID,
				"provider_id":     providerID,
			},
			tenantID,
			n.CloudAccountID,
			"k8s_ec2_enricher",
		)
		newEdges = append(newEdges, edge)

		e.logger.Debug("k8s_ec2_enricher: linked K8s Node to EC2",
			"k8s_node", n.Properties["name"],
			"instance_id", instanceID)
	}

	e.logger.Info("k8s_ec2_enricher: completed",
		"k8s_node_edges_created", len(newEdges))

	allEdges = append(allEdges, newEdges...)
	return allNodes, allEdges, nil
}

// parseEC2InstanceIDFromProviderID extracts the EC2 instance ID from a K8s node providerID.
// Supported formats:
//   - aws:///us-east-1a/i-0abc123def456  → i-0abc123def456
//   - aws://us-east-1/us-east-1a/i-0abc  → i-0abc
//   - i-0abc123def456                     → i-0abc123def456 (bare ID)
func parseEC2InstanceIDFromProviderID(providerID string) string {
	// Strip "aws://" prefix (handles both aws:/// and aws://region/)
	trimmed := strings.TrimPrefix(providerID, "aws://")
	// After stripping, format is: [region/]zone/i-xxx  or  /zone/i-xxx
	// Split by "/" and take the last segment
	parts := strings.Split(trimmed, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasPrefix(parts[i], "i-") {
			return parts[i]
		}
	}
	// Bare instance ID (no prefix)
	if strings.HasPrefix(providerID, "i-") {
		return providerID
	}
	return ""
}
