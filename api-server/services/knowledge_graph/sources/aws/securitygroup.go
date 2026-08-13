package aws

import (
	"sort"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// ec2SecurityGroupSchema — concrete schema for security groups. vpc_id is lifted by the
// common extractor; group_id is hoisted via the per-NodeType QueryablePropertiesMap fallback.
var ec2SecurityGroupSchema = core.SpecificTypeSchema{
	SpecificType: "EC2SecurityGroup",
	NodeType:     core.NodeTypeSecurityGroup,
	Properties: []core.PropertyDef{
		{Name: "vpc_id", Indexed: true},
		// Internet-exposure signals derived from ingress rules (extractSecurityGroupMetadata).
		{Name: "open_to_internet", Indexed: true},
		{Name: "exposed_ports", Indexed: true},
		{Name: "ingress_cidrs"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "group_name"},
		{Name: "description"},
	},
}

func init() { core.RegisterSpecificTypeSchema(ec2SecurityGroupSchema) }

// extractSecurityGroupMetadata derives internet-exposure signals from a security
// group's ingress rules (IpPermissions) and surfaces them as queryable node
// properties: open_to_internet (any 0.0.0.0/0 or ::/0 ingress), exposed_ports (the
// ports opened to the internet), and ingress_cidrs (all distinct source CIDRs). This
// answers "which security groups — and therefore which instances/databases — are
// exposed to the internet, and on what ports". Only runs when the SG has ingress
// rules, so groups without rules stay unflagged.
func (s *AWSSource) extractSecurityGroupMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	ipPermissions, ok := metaMap["IpPermissions"].([]interface{})
	if !ok {
		return
	}

	openToInternet := false
	exposedPorts := map[int]struct{}{}
	cidrs := map[string]struct{}{}

	for _, permission := range ipPermissions {
		permMap, ok := permission.(map[string]interface{})
		if !ok {
			continue
		}
		fromPort, hasPort := permMap["FromPort"].(float64)

		ruleOpen := false
		if ipRanges, ok := permMap["IpRanges"].([]interface{}); ok {
			for _, r := range ipRanges {
				if rm, ok := r.(map[string]interface{}); ok {
					if cidr, ok := rm["CidrIp"].(string); ok && cidr != "" {
						cidrs[cidr] = struct{}{}
						if cidr == "0.0.0.0/0" {
							ruleOpen = true
						}
					}
				}
			}
		}
		if ipv6Ranges, ok := permMap["Ipv6Ranges"].([]interface{}); ok {
			for _, r := range ipv6Ranges {
				if rm, ok := r.(map[string]interface{}); ok {
					if cidr, ok := rm["CidrIpv6"].(string); ok && cidr != "" {
						cidrs[cidr] = struct{}{}
						if cidr == "::/0" {
							ruleOpen = true
						}
					}
				}
			}
		}
		if ruleOpen {
			openToInternet = true
			if hasPort {
				exposedPorts[int(fromPort)] = struct{}{}
			}
		}
	}

	properties["open_to_internet"] = openToInternet
	if len(exposedPorts) > 0 {
		ports := make([]int, 0, len(exposedPorts))
		for p := range exposedPorts {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		properties["exposed_ports"] = ports
	}
	if len(cidrs) > 0 {
		list := make([]string, 0, len(cidrs))
		for c := range cidrs {
			list = append(list, c)
		}
		sort.Strings(list)
		properties["ingress_cidrs"] = list
	}
}

// createSecurityGroupEdges creates edges for Security Groups
func (s *AWSSource) createSecurityGroupEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		meta, hasMeta := getMetadataMap(node)
		if !hasMeta {
			// No metadata, try basic VPC connection
			if vpcID, ok := getStringProperty(node, "vpc_id"); ok {
				if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
					edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "vpc"}, req))
				}
			}
			continue
		}

		// 1. SecurityGroup → VPC relationship
		if vpcID, ok := meta["VpcId"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{
						"connection_type": "vpc",
					}, req))
			}
		}

		// 2. SecurityGroup → Other SecurityGroup relationships (from ingress rules)
		if ipPermissions, ok := meta["IpPermissions"].([]interface{}); ok {
			for _, permission := range ipPermissions {
				if permMap, ok := permission.(map[string]interface{}); ok {
					// Extract port and protocol info for edge properties
					protocol, _ := permMap["IpProtocol"].(string)
					fromPort, _ := permMap["FromPort"].(float64)
					toPort, _ := permMap["ToPort"].(float64)

					// Parse UserIdGroupPairs for security group references
					if userIdGroupPairs, ok := permMap["UserIdGroupPairs"].([]interface{}); ok {
						for _, pair := range userIdGroupPairs {
							if pairMap, ok := pair.(map[string]interface{}); ok {
								if referencedSGID, ok := pairMap["GroupId"].(string); ok && referencedSGID != "" {
									if referencedSGNode, exists := lookup.ByResourceID[referencedSGID]; exists {
										edges = append(edges, s.createEdge(node, referencedSGNode, core.RelationshipHostedOn,
											map[string]interface{}{
												"connection_type": "security_group_rule",
												"rule_type":       "ingress",
												"protocol":        protocol,
												"from_port":       int(fromPort),
												"to_port":         int(toPort),
											}, req))
									}
								}
							}
						}
					}
				}
			}
		}

		// 3. SecurityGroup → Other SecurityGroup relationships (from egress rules)
		if ipPermissionsEgress, ok := meta["IpPermissionsEgress"].([]interface{}); ok {
			for _, permission := range ipPermissionsEgress {
				if permMap, ok := permission.(map[string]interface{}); ok {
					// Extract port and protocol info for edge properties
					protocol, _ := permMap["IpProtocol"].(string)
					fromPort, _ := permMap["FromPort"].(float64)
					toPort, _ := permMap["ToPort"].(float64)

					// Parse UserIdGroupPairs for security group references
					if userIdGroupPairs, ok := permMap["UserIdGroupPairs"].([]interface{}); ok {
						for _, pair := range userIdGroupPairs {
							if pairMap, ok := pair.(map[string]interface{}); ok {
								if referencedSGID, ok := pairMap["GroupId"].(string); ok && referencedSGID != "" {
									if referencedSGNode, exists := lookup.ByResourceID[referencedSGID]; exists {
										edges = append(edges, s.createEdge(node, referencedSGNode, core.RelationshipHostedOn,
											map[string]interface{}{
												"connection_type": "security_group_rule",
												"rule_type":       "egress",
												"protocol":        protocol,
												"from_port":       int(fromPort),
												"to_port":         int(toPort),
											}, req))
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return edges
}
