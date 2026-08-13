package gcp

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
)

// gcpFirewallSchema is the concrete per-specific_type property schema for the
// GCPFirewall specific_type. Names are the node.Properties keys written by
// createNodeFromResource + enrichFirewallRuleFromCLI. Indexed mirrors
// core.QueryablePropertiesMap[NodeTypeSecurityGroup] (vpc_id); direction/disabled
// are declared but NOT Indexed (governed by the existing map only).
var gcpFirewallSchema = core.SpecificTypeSchema{
	SpecificType: "GCPFirewall",
	NodeType:     core.NodeTypeSecurityGroup,
	Properties: []core.PropertyDef{
		{Name: "vpc_id", Indexed: true},
		{Name: "vpc_network_url"},
		{Name: "direction"},
		{Name: "priority"},
		{Name: "disabled"},
		// Internet-exposure signals (INGRESS rules open to 0.0.0.0/0). Set at CLI
		// enrichment (post-NewNode), so NOT Indexed / not in query_attributes.
		{Name: "open_to_internet"},
		{Name: "exposed_ports"},
		{Name: "ingress_cidrs"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "self_link"},
		{Name: "has_target_service_accounts"},
	},
}

func init() { core.RegisterSpecificTypeSchema(gcpFirewallSchema) }

// fetchFirewallRulesFromGCP fetches all firewall rules via gcloud CLI
func (s *GCPSource) fetchFirewallRulesFromGCP(reqCtx *security.RequestContext, accountID string) ([]GCPFirewallRule, error) {
	cmd := "gcloud compute firewall-rules list --format=json"

	s.logger.Info("fetching GCP firewall rules via CLI", "account_id", accountID)

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

	var rules []GCPFirewallRule
	if err := json.Unmarshal([]byte(output), &rules); err != nil {
		return nil, fmt.Errorf("failed to parse firewall rules response: %w", err)
	}

	return rules, nil
}

// enrichFirewallRuleFromCLI enriches a firewall rule node with VPC info from CLI data
func (s *GCPSource) enrichFirewallRuleFromCLI(node *core.DbNode, shortName string, cliData *gcpCLIData) {
	fr, ok := cliData.firewallRules[shortName]
	if !ok {
		return
	}

	if _, hasVPC := node.Properties["vpc_id"]; !hasVPC {
		if fr.Network != "" {
			node.Properties["vpc_id"] = extractGCPResourceNameFromURL(fr.Network)
			node.Properties["vpc_network_url"] = fr.Network
		}
	}
	if fr.Direction != "" {
		node.Properties["direction"] = fr.Direction
	}
	if fr.Priority != 0 {
		node.Properties["priority"] = fr.Priority
	}
	node.Properties["disabled"] = fr.Disabled

	// Internet-exposure signals for INGRESS rules (mirrors the AWS SG-exposure props):
	// open_to_internet when any source range is 0.0.0.0/0, plus the exposed ports and
	// source CIDRs. GCP ports are strings (single or "lo-hi" ranges), kept as-is.
	if fr.Direction == "INGRESS" && len(fr.SourceRanges) > 0 {
		node.Properties["ingress_cidrs"] = fr.SourceRanges
		openToInternet := false
		for _, cidr := range fr.SourceRanges {
			if cidr == "0.0.0.0/0" {
				openToInternet = true
			}
		}
		node.Properties["open_to_internet"] = openToInternet
		if openToInternet {
			ports := make([]string, 0)
			for _, a := range fr.Allowed {
				ports = append(ports, a.Ports...)
			}
			if len(ports) > 0 {
				node.Properties["exposed_ports"] = ports
			}
		}
	}
}

// createFirewallRuleEdges creates PROTECTS edges from Firewall Rules to their VPCs
func (s *GCPSource) createFirewallRuleEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	sgNodes, ok := lookup.ByNodeType[core.NodeTypeSecurityGroup]
	if !ok {
		return edges
	}

	for _, node := range sgNodes {
		resourceType, _ := node.Properties["type"].(string)
		if resourceType != "firewall-rule" {
			continue
		}

		vpcID, _ := node.Properties["vpc_id"].(string)
		if vpcID == "" {
			continue
		}

		if vpcNode := findNodeByNameAndType(lookup, core.NodeTypeVPC, vpcID); vpcNode != nil {
			direction, _ := node.Properties["direction"].(string)
			edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipProtects,
				map[string]interface{}{
					"connection_type": "firewall_vpc",
					"direction":       direction,
				}, req))
		}
	}

	s.logger.Info("created GCP firewall rule edges", "edge_count", len(edges))
	return edges
}
