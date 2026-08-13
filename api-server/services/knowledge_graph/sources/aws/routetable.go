package aws

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"strings"
)

// createRouteTableEdges creates edges for Route Tables
// Handles VPC association, subnet associations, and route destinations (NAT GW, VPC Endpoints)
func (s *AWSSource) createRouteTableEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			continue
		}

		// 1. RouteTable → VPC (BELONGS_TO)
		if vpcID, ok := meta["VpcId"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. RouteTable → Subnet associations (ASSOCIATED_WITH)
		if associations, ok := meta["Associations"].([]interface{}); ok {
			for _, assoc := range associations {
				if assocMap, ok := assoc.(map[string]interface{}); ok {
					// Skip main route table associations (no subnet ID)
					if subnetID, ok := assocMap["SubnetId"].(string); ok && subnetID != "" {
						if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
							isMain, _ := assocMap["Main"].(bool)
							edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipAssociatedWith,
								map[string]interface{}{
									"connection_type": "subnet_association",
									"is_main":         isMain,
								}, req))
						}
					}
				}
			}
		}

		// 3. Routes to gateway destinations (ROUTES_THROUGH)
		if routes, ok := meta["Routes"].([]interface{}); ok {
			for _, route := range routes {
				if routeMap, ok := route.(map[string]interface{}); ok {
					destCidr, _ := routeMap["DestinationCidrBlock"].(string)
					if destCidr == "" {
						// Try IPv6 destination
						destCidr, _ = routeMap["DestinationIpv6CidrBlock"].(string)
					}

					// NAT Gateway route
					if natGwID, ok := routeMap["NatGatewayId"].(string); ok && natGwID != "" {
						if natNode, exists := lookup.ByResourceID[natGwID]; exists {
							edges = append(edges, s.createEdge(node, natNode, core.RelationshipRoutesThrough,
								map[string]interface{}{
									"destination_cidr": destCidr,
									"route_type":       "nat_gateway",
								}, req))
						}
					}

					// Gateway ID can be: Internet Gateway (igw-*), VPC Endpoint (vpce-*), Virtual Private Gateway (vgw-*)
					if gwID, ok := routeMap["GatewayId"].(string); ok && gwID != "" {
						// VPC Endpoint (Gateway type)
						if strings.HasPrefix(gwID, "vpce-") {
							if vpcEndpointNode, exists := lookup.ByResourceID[gwID]; exists {
								edges = append(edges, s.createEdge(node, vpcEndpointNode, core.RelationshipRoutesThrough,
									map[string]interface{}{
										"destination_cidr": destCidr,
										"route_type":       "vpc_endpoint",
									}, req))
							}
						}
						// Internet Gateway: aws_vpc.go now collects these and aws_source.go
						// emits them as NodeTypeNetworkGateway, so wire ROUTES_THROUGH here.
						if strings.HasPrefix(gwID, "igw-") {
							if igwNode, exists := lookup.ByResourceID[gwID]; exists {
								edges = append(edges, s.createEdge(node, igwNode, core.RelationshipRoutesThrough,
									map[string]interface{}{
										"destination_cidr": destCidr,
										"route_type":       "internet_gateway",
									}, req))
							}
						}
						// Virtual Private Gateway (vgw-*) for VPN connections - future enhancement
					}

					// Transit Gateway route
					if tgwID, ok := routeMap["TransitGatewayId"].(string); ok && tgwID != "" {
						s.logger.Debug("Route to Transit Gateway found",
							"route_table", node.Properties["name"],
							"tgw_id", tgwID,
							"destination", destCidr)
					}

					// VPC Peering connection route
					if peeringID, ok := routeMap["VpcPeeringConnectionId"].(string); ok && peeringID != "" {
						s.logger.Debug("Route to VPC Peering found",
							"route_table", node.Properties["name"],
							"peering_id", peeringID,
							"destination", destCidr)
					}
				}
			}
		}
	}

	return edges
}

// fetchRouteTablesFromAWS fetches all route tables from the in-memory meta cache.
func (s *AWSSource) fetchRouteTablesFromAWS(_ *security.RequestContext, _ *core.SourceBuildRequest, _ string) ([]RouteTableData, error) {
	rows := s.metaByType["route-table"]
	routeTables := make([]RouteTableData, 0, len(rows))
	for _, row := range rows {
		var rt RouteTableData
		if err := unmarshalMetaInto(row, &rt); err != nil {
			s.logger.Warn("Failed to parse route table meta, skipping", "resource_id", row.ResourceID, "error", err)
			continue
		}
		routeTables = append(routeTables, rt)
	}
	s.logger.Info("Fetched route tables from DB cache", "count", len(routeTables))
	return routeTables, nil
}

// ensureRouteTableNodes fetches route tables via AWS CLI and creates nodes for them
func (s *AWSSource) ensureRouteTableNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, edges []*core.DbEdge, lookup *sources.NodeLookup, req *core.SourceBuildRequest) ([]*core.DbNode, []*core.DbEdge) {
	// Fetch route tables from AWS
	routeTables, err := s.fetchRouteTablesFromAWS(reqCtx, req, req.CloudAccountID)
	if err != nil {
		s.logger.Warn("Failed to fetch route tables from AWS, skipping route table nodes",
			"error", err,
			"account_id", req.CloudAccountID)
		return nodes, edges
	}

	// Create RouteTable nodes
	for _, rt := range routeTables {
		// Get name from tags
		name := rt.RouteTableId
		for _, tag := range rt.Tags {
			if tag.Key == "Name" && tag.Value != "" {
				name = tag.Value
				break
			}
		}

		// Check if main route table
		isMain := false
		for _, assoc := range rt.Associations {
			if assoc.Main {
				isMain = true
				break
			}
		}

		// Build labels map from tags
		labels := make(map[string]interface{})
		for _, tag := range rt.Tags {
			labels[tag.Key] = tag.Value
		}

		// Build metadata map for the route table. Roundtrip through JSON so
		// the typed Associations/Routes slices become []interface{} of
		// map[string]interface{} — matching the shape edge builders expect
		// (and the shape cloud_resourses.meta JSONB unmarshal produces).
		// Without this, createRouteTableEdges's `meta["Associations"].([]interface{})`
		// type assertion silently misses, emitting zero subnet / route edges.
		rawMeta := map[string]interface{}{
			"RouteTableId": rt.RouteTableId,
			"VpcId":        rt.VpcId,
			"OwnerId":      rt.OwnerId,
			"Associations": rt.Associations,
			"Routes":       rt.Routes,
		}
		metaMap := rawMeta
		if metaBytes, err := json.Marshal(rawMeta); err == nil {
			var normalized map[string]interface{}
			if json.Unmarshal(metaBytes, &normalized) == nil {
				metaMap = normalized
			}
		}

		// Build properties
		properties := map[string]interface{}{
			"name":           name,
			"resource_id":    rt.RouteTableId,
			"route_table_id": rt.RouteTableId,
			"vpc_id":         rt.VpcId,
			"is_main":        isMain,
			"account_number": rt.OwnerId, // AWS account number from route table data
			"region":         req.Region,
			// Stored under "meta" so getMetadataMap can find it; createRouteTableEdges
			// then emits BELONGS_TO/ASSOCIATED_WITH/ROUTES_THROUGH edges off this.
			"meta":   metaMap,
			"labels": labels,
		}

		// Create temp node for generating unique key
		tempNode := &core.DbNode{
			NodeType:       core.NodeTypeRouteTable,
			Properties:     properties,
			CloudAccountID: req.CloudAccountID,
		}
		uniqueKey := s.GenerateUniqueKey(tempNode)

		// Create the node using core.NewNode
		routeTableNode := core.NewNode(core.NodeTypeRouteTable, uniqueKey, properties, req.TenantID, req.CloudAccountID, "aws")

		// Add to nodes and lookup
		nodes = append(nodes, routeTableNode)
		lookup.ByResourceID[rt.RouteTableId] = routeTableNode
		lookup.ByNodeType[core.NodeTypeRouteTable] = append(lookup.ByNodeType[core.NodeTypeRouteTable], routeTableNode)

		s.logger.Debug("Created route table node",
			"route_table_id", rt.RouteTableId,
			"name", name,
			"vpc_id", rt.VpcId,
			"is_main", isMain)
	}

	s.logger.Info("Created route table nodes from AWS CLI",
		"count", len(routeTables))

	return nodes, edges
}
