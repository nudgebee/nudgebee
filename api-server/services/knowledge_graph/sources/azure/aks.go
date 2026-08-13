package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// aksClusterSchema is the fixed concrete-property schema for the AKSCluster
// specific_type. Names are the node.Properties keys written by
// extractAKSMetadata + extractCommonNetworkIDs; universal base keys are implicit.
var aksClusterSchema = core.SpecificTypeSchema{
	SpecificType: "AKSCluster",
	NodeType:     core.NodeTypeManagedCluster,
	Properties: []core.PropertyDef{
		{Name: "kubernetes_version", Indexed: true}, // ~ version
		{Name: "dns_name", Indexed: true},           // ~ endpoint (fqdn)
		{Name: "node_pool_count", Indexed: true},
		{Name: "vnet_id", Indexed: true},
		{Name: "subnet_id", Indexed: true},
		{Name: "nsg_id"},
		{Name: "resource_group", Indexed: true},
		{Name: "vnet_name_hierarchy"},
		// Additional provider fields (not yet emitted by our extractor):
		// (fqdn is represented by the declared dns_name field)
		{Name: "provisioning_state"},
		{Name: "api_server_public_access"},
	},
}

func init() { core.RegisterSpecificTypeSchema(aksClusterSchema) }

func (s *AzureSource) extractAKSMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if kubeVersion, ok := metaMap["kubernetesVersion"].(string); ok && kubeVersion != "" {
		properties["kubernetes_version"] = kubeVersion
	}
	if fqdn, ok := metaMap["fqdn"].(string); ok && fqdn != "" {
		properties["dns_name"] = fqdn
	}
	if nodePoolCount, ok := metaMap["nodePoolCount"]; ok {
		properties["node_pool_count"] = nodePoolCount
	}
}

// createAKSEdges creates edges for AKS → VNet/Subnet
func (s *AzureSource) createAKSEdges(aksNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, aksNode := range aksNodes {
		// AKS → Subnet
		if subnetID, ok := aksNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.ByResourceID[strings.ToLower(subnetID)]; exists {
				edges = append(edges, s.createEdge(aksNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// AKS → VNet
		if vnetID, ok := aksNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.ByResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(aksNode, vnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vnet"}, req))
			}
		}
	}

	return edges
}
