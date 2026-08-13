package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// elasticIPSchema — concrete schema for Elastic IPs (NodeTypePublicIP). The address /
// association are resolved during edge building (createPublicIPEdges), not at node creation.
var elasticIPSchema = core.SpecificTypeSchema{
	SpecificType: "ElasticIP",
	NodeType:     core.NodeTypePublicIP,
	Properties: []core.PropertyDef{
		{Name: "public_ip"},
		{Name: "private_ip"},
		{Name: "domain"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "instance_id"},
		{Name: "allocation_id"},
		{Name: "association_id"},
		{Name: "network_interface_id"},
		{Name: "network_interface_owner_id"},
		{Name: "private_ip_address"},
		{Name: "public_ipv4_pool"},
		{Name: "network_border_group"},
		{Name: "customer_owned_ip"},
		{Name: "customer_owned_ipv4_pool"},
		{Name: "carrier_ip"},
	},
}

func init() { core.RegisterSpecificTypeSchema(elasticIPSchema) }

// fetchAllPublicIPDataFromAWS fetches all Elastic IP data from the in-memory meta cache.
func (s *AWSSource) fetchAllPublicIPDataFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) (map[string]*PublicIPData, error) {
	rows := s.metaByType["elastic-ip"]
	eipByAllocationID := make(map[string]*PublicIPData, len(rows))
	for _, row := range rows {
		var addr PublicIPData
		if err := unmarshalMetaInto(row, &addr); err != nil {
			s.logger.Warn("Failed to parse Elastic IP meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		if addr.AllocationId != "" {
			eipByAllocationID[addr.AllocationId] = &addr
		}
	}
	s.logger.Info("Fetched Elastic IP data from DB cache", "eip_count", len(eipByAllocationID))
	return eipByAllocationID, nil
}

// createPublicIPEdges creates edges for Elastic IP (PublicIP) resources
// PublicIP → ComputeInstance (ASSOCIATED_WITH), PublicIP → NetworkInterface (ASSOCIATED_WITH)
func (s *AWSSource) createPublicIPEdges(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	if len(nodes) == 0 {
		return edges
	}

	// Batch fetch ALL Elastic IPs in one API call
	var eipDataMap map[string]*PublicIPData
	if req.CloudAccountID != "" {
		data, err := s.fetchAllPublicIPDataFromAWS(reqCtx, req, req.CloudAccountID)
		if err != nil {
			s.logger.Error("Failed to batch fetch Elastic IP data from AWS",
				"error", err)
		} else {
			eipDataMap = data
		}
	}

	// Build reverse index: resource_id → NetworkInterface node for O(1) fallback lookup
	// when lookup.ByResourceID misses NLB-created ENIs that aren't in the standard CLI output.
	eniByResourceID := make(map[string]*core.DbNode, len(lookup.ByNodeType[core.NodeTypeNetworkInterface]))
	for _, n := range lookup.ByNodeType[core.NodeTypeNetworkInterface] {
		if rid, _ := n.Properties["resource_id"].(string); rid != "" {
			eniByResourceID[rid] = n
		}
	}

	// Process nodes using the pre-fetched data
	for _, node := range nodes {
		// Get allocation ID
		allocationID, _ := node.Properties["resource_id"].(string)
		if allocationID == "" {
			if name, ok := node.Properties["name"].(string); ok {
				allocationID = name
			}
		}

		// Lookup from batch-fetched data instead of making API call
		var eipData *PublicIPData
		if eipDataMap != nil && allocationID != "" {
			if data, exists := eipDataMap[allocationID]; exists {
				eipData = data
				// Enrich node with fetched data
				node.Properties["public_ip"] = data.PublicIp
				node.Properties["private_ip"] = data.PrivateIpAddress
				node.Properties["domain"] = data.Domain
			}
		}

		if eipData == nil {
			continue
		}

		// PublicIP → ComputeInstance (ASSOCIATED_WITH)
		if eipData.InstanceId != "" {
			if ec2Node, exists := lookup.ByResourceID[eipData.InstanceId]; exists {
				edges = append(edges, s.createEdge(node, ec2Node, core.RelationshipAssociatedWith,
					map[string]interface{}{
						"association_id": eipData.AssociationId,
						"public_ip":      eipData.PublicIp,
						"private_ip":     eipData.PrivateIpAddress,
					}, req))
			} else {
				s.logger.Warn("PublicIP EC2 instance not found in lookup", "allocation_id", allocationID, "instance_id", eipData.InstanceId)
			}
		}

		// PublicIP → NetworkInterface (ASSOCIATED_WITH)
		if eipData.NetworkInterfaceId != "" {
			eniEdgeProps := map[string]interface{}{
				"association_id":       eipData.AssociationId,
				"public_ip":            eipData.PublicIp,
				"private_ip":           eipData.PrivateIpAddress,
				"network_interface_id": eipData.NetworkInterfaceId,
			}

			eniNode, exists := lookup.ByResourceID[eipData.NetworkInterfaceId]
			if !exists {
				// ENI may have been replaced by createENIEdges (NLB-created ENIs are often
				// dropped from the in-memory lookup because they don't show in standard ENI
				// CLI output). Fall back to the pre-built resource_id index.
				eniNode, exists = eniByResourceID[eipData.NetworkInterfaceId]
			}

			if exists {
				edges = append(edges, s.createEdge(node, eniNode, core.RelationshipAssociatedWith, eniEdgeProps, req))

				// If this ENI belongs to a LoadBalancer (NLB pattern: description = "ELB {lb-name}"),
				// also create a direct PublicIP → LoadBalancer edge so the EIP → LB hop is visible.
				desc, _ := eniNode.Properties["description"].(string)
				if strings.HasPrefix(desc, "ELB ") {
					lbName := strings.TrimPrefix(desc, "ELB ")
					for _, lbNode := range lookup.ByNodeType[core.NodeTypeLoadBalancer] {
						if n, _ := lbNode.Properties["name"].(string); n == lbName {
							edges = append(edges, s.createEdge(node, lbNode, core.RelationshipAssociatedWith,
								map[string]interface{}{
									"association_id":       eipData.AssociationId,
									"public_ip":            eipData.PublicIp,
									"network_interface_id": eipData.NetworkInterfaceId,
									"connection_type":      "nlb_eip",
								}, req))
							s.logger.Debug("Created direct PublicIP → LoadBalancer edge via NLB ENI",
								"public_ip", eipData.PublicIp,
								"lb_name", lbName)
							break
						}
					}
				}
			} else {
				s.logger.Warn("PublicIP ENI not found in lookup", "allocation_id", allocationID, "eni_id", eipData.NetworkInterfaceId)
			}
		}
	}

	return edges
}
