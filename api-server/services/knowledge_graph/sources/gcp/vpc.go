package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// gcpVpcSchema is the concrete per-specific_type property schema for the GCPVpc
// specific_type. Names are the node.Properties keys written by ensureGCPVPCNodes
// (CLI). GCP VPCs are global
// and expose no CIDR/state (core.QueryablePropertiesMap[NodeTypeVPC] fields), so
// no field is Indexed; the CLI-only descriptors are declared for documentation.
var gcpVpcSchema = core.SpecificTypeSchema{
	SpecificType: "GCPVpc",
	NodeType:     core.NodeTypeVPC,
	Properties: []core.PropertyDef{
		{Name: "vpc_id"},
		{Name: "self_link"},
		{Name: "auto_create_subnetworks"},
		{Name: "routing_mode"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "partial_uri"},
		{Name: "routing_config_routing_mode"},
		{Name: "description"},
	},
}

// gcpSubnetSchema is the concrete per-specific_type property schema for the
// GCPSubnet specific_type. Names are the node.Properties keys written by
// extractGCPSubnetMetadata +
// ensureGCPSubnetNodes. Indexed mirrors core.QueryablePropertiesMap[NodeTypeSubnet]
// (subnet_id/vpc_id) plus ip_cidr_range, the GCP-native CIDR key the ontology reads.
var gcpSubnetSchema = core.SpecificTypeSchema{
	SpecificType: "GCPSubnet",
	NodeType:     core.NodeTypeSubnet,
	Properties: []core.PropertyDef{
		{Name: "subnet_id", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		{Name: "ip_cidr_range", Indexed: true},
		{Name: "vpc_network_url"},
		{Name: "gateway_address"},
		{Name: "self_link"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "partial_uri"},
		{Name: "project_id"},
		{Name: "private_ip_google_access"},
		{Name: "flow_logs_enabled"},
		{Name: "flow_logs_aggregation_interval"},
		{Name: "flow_logs_sampling"},
		{Name: "flow_logs_metadata"},
		{Name: "flow_logs_filter_expr"},
		{Name: "purpose"},
		{Name: "vpc_partial_uri"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(gcpVpcSchema)
	core.RegisterSpecificTypeSchema(gcpSubnetSchema)
}

// extractGCPSubnetMetadata derives the parent VPC (vpc_id) and CIDR info from a
// subnet's collected metadata. The collector persists the subnet's network
// self-link in meta.network (see cloud-collector listSubnets); without it the
// resource row carries no recoverable VPC reference and the subnet is orphaned.
func (s *GCPSource) extractGCPSubnetMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if network, ok := metaMap["network"].(string); ok && network != "" {
		properties["vpc_id"] = extractGCPResourceNameFromURL(network)
		properties["vpc_network_url"] = network
	}
	if cidr, ok := metaMap["ip_cidr_range"].(string); ok && cidr != "" {
		properties["ip_cidr_range"] = cidr
	}
	if gateway, ok := metaMap["gateway_address"].(string); ok && gateway != "" {
		properties["gateway_address"] = gateway
	}
}

// fetchVPCNetworksFromGCP fetches all VPC networks via gcloud CLI
func (s *GCPSource) fetchVPCNetworksFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPVPCNetwork, error) {
	cmd := "gcloud compute networks list --format=json"

	s.logger.Info("fetching GCP VPC networks via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var networks []GCPVPCNetwork
	if err := json.Unmarshal([]byte(output), &networks); err != nil {
		return nil, fmt.Errorf("failed to parse VPC networks response: %w", err)
	}

	return networks, nil
}

// fetchSubnetsFromGCP fetches all subnets via gcloud CLI
func (s *GCPSource) fetchSubnetsFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPSubnetData, error) {
	cmd := "gcloud compute networks subnets list --format=json"

	s.logger.Info("fetching GCP subnets via CLI", "account_id", accountID)

	resp, err := cloud.ExecuteCliWithRetry(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: accountID,
		Command:   cmd,
	}, 3)
	if err != nil {
		return nil, fmt.Errorf("failed to execute gcloud CLI: %w", err)
	}

	output, err := parseGCloudCLIResponse(resp)
	if err != nil {
		return nil, err
	}

	var subnets []GCPSubnetData
	if err := json.Unmarshal([]byte(output), &subnets); err != nil {
		return nil, fmt.Errorf("failed to parse subnets response: %w", err)
	}

	return subnets, nil
}

// ensureGCPVPCNodes ensures VPC nodes exist for all VPC networks from CLI data
func (s *GCPSource) ensureGCPVPCNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for name, vpc := range cliData.vpcNetworks {
		// Check if a VPC node already exists with this name
		found := false
		if vpcNodes, ok := lookup.ByNodeType[core.NodeTypeVPC]; ok {
			for _, existing := range vpcNodes {
				existingName := extractGCPShortName(getNodeName(existing))
				if existingName == name {
					// Enrich existing node
					if vpc.SelfLink != "" {
						existing.Properties["self_link"] = vpc.SelfLink
					}
					existing.Properties["auto_create_subnetworks"] = vpc.AutoCreateSubnetworks
					existing.Properties["routing_mode"] = vpc.RoutingConfig.RoutingMode
					found = true
					break
				}
			}
		}

		if !found {
			// Create new VPC node from CLI data
			properties := map[string]interface{}{
				"name":                    name,
				"type":                    "vpc",
				"subtype":                 "vpc",
				"service_name":            "Networking",
				"cloud_provider":          "GCP",
				"inferred":                false,
				"self_link":               vpc.SelfLink,
				"auto_create_subnetworks": vpc.AutoCreateSubnetworks,
				"routing_mode":            vpc.RoutingConfig.RoutingMode,
				"resource_id":             name,
				"vpc_id":                  name,
			}

			tempNode := &core.DbNode{
				NodeType:       core.NodeTypeVPC,
				Properties:     properties,
				CloudAccountID: req.CloudAccountID,
			}
			uniqueKey := s.GenerateUniqueKey(tempNode)

			node := core.NewNode(core.NodeTypeVPC, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP VPC node from CLI", "name", name)
		}
	}

	return nodes
}

// ensureGCPSubnetNodes ensures Subnet nodes exist for all subnets from CLI data
func (s *GCPSource) ensureGCPSubnetNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, cliData *gcpCLIData, req *core.SourceBuildRequest) []*core.DbNode {
	for _, subnet := range cliData.subnets {
		// Skip duplicates (we index by both selfLink and name)
		if subnet.SelfLink == "" {
			continue
		}

		// Check if exists
		found := false
		if subnetNodes, ok := lookup.ByNodeType[core.NodeTypeSubnet]; ok {
			for _, existing := range subnetNodes {
				existingName := extractGCPShortName(getNodeName(existing))
				if existingName == subnet.Name {
					// Enrich existing
					existing.Properties["ip_cidr_range"] = subnet.IpCidrRange
					existing.Properties["vpc_id"] = extractGCPResourceNameFromURL(subnet.Network)
					existing.Properties["self_link"] = subnet.SelfLink
					found = true
					break
				}
			}
		}

		if !found {
			vpcName := extractGCPResourceNameFromURL(subnet.Network)
			properties := map[string]interface{}{
				"name":            subnet.Name,
				"type":            "subnet",
				"subtype":         "subnet",
				"service_name":    "Networking",
				"cloud_provider":  "GCP",
				"region":          subnet.Region,
				"inferred":        false,
				"ip_cidr_range":   subnet.IpCidrRange,
				"gateway_address": subnet.GatewayAddress,
				"self_link":       subnet.SelfLink,
				"vpc_id":          vpcName,
				"resource_id":     subnet.Name,
				"subnet_id":       subnet.Name,
			}

			tempNode := &core.DbNode{
				NodeType:       core.NodeTypeSubnet,
				Properties:     properties,
				CloudAccountID: req.CloudAccountID,
			}
			uniqueKey := s.GenerateUniqueKey(tempNode)

			node := core.NewNode(core.NodeTypeSubnet, uniqueKey, properties, req.TenantID, req.CloudAccountID, "gcp")
			nodes = append(nodes, node)
			s.logger.Debug("created GCP subnet node from CLI", "name", subnet.Name, "vpc", vpcName)
		}
	}

	return nodes
}

// createSubnetToVPCEdges creates edges from subnets to their parent VPCs
func (s *GCPSource) createSubnetToVPCEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	subnetNodes, ok := lookup.ByNodeType[core.NodeTypeSubnet]
	if !ok {
		return edges
	}

	// Per-account build invariant: BuildGraph runs once per cloud account and
	// fetchGCPResources filters by CloudAccountID, so the lookup only ever holds
	// this account's VPC nodes. A subnet always belongs to a VPC in its own account,
	// so when the account has exactly one VPC (the common GCP auto-mode "default"
	// network) any subnet whose parent network wasn't captured at collection time
	// unambiguously belongs to it. vpc_id is only populated by CLI enrichment
	// (ensureGCPSubnetNodes); subnets ingested via the resource-table/API path carry
	// no network self-link, which is why most subnets would otherwise be orphaned.
	var soleVPC *core.DbNode
	if vpcNodes := lookup.ByNodeType[core.NodeTypeVPC]; len(vpcNodes) == 1 {
		soleVPC = vpcNodes[0]
	}

	for _, node := range subnetNodes {
		if vpcID, _ := node.Properties["vpc_id"].(string); vpcID != "" {
			// vpc_id is authoritative: link to the named VPC when it's present, but
			// never fall back to soleVPC here — the named VPC may simply be absent from
			// this account's lookup (e.g. a Shared VPC host project), and inferring a
			// different VPC would be a false relationship.
			if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
			continue
		}

		// Fallback: no resolvable vpc_id, but the account has a single VPC. Link to it
		// and tag the edge "vpc_inferred" so the inference is auditable. Accounts with
		// multiple VPCs are left as-is (no guessing) to avoid mislinking shared/multi-VPC
		// topologies — those keep the pre-existing behaviour.
		if soleVPC != nil {
			edges = append(edges, s.createEdge(node, soleVPC, core.RelationshipBelongsTo,
				map[string]interface{}{"connection_type": "vpc_inferred"}, req))
		}
	}

	s.logger.Info("created GCP subnet → VPC edges", "edge_count", len(edges))
	return edges
}
