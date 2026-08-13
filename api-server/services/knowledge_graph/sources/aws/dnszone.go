package aws

import "nudgebee/services/knowledge_graph/core"

// route53HostedZoneSchema — concrete schema for Route53 hosted zones. Fields written by
// extractDNSZoneMetadata.
var route53HostedZoneSchema = core.SpecificTypeSchema{
	SpecificType: "Route53HostedZone",
	NodeType:     core.NodeTypeDNSZone,
	Properties: []core.PropertyDef{
		{Name: "zone_name"},
		{Name: "private_zone"},
		{Name: "alias_target_dns"},
		{Name: "alias_target_zone_id"},
		{Name: "is_public_entry"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "comment"},
	},
}

func init() { core.RegisterSpecificTypeSchema(route53HostedZoneSchema) }

// extractDNSZoneMetadata extracts essential fields for Route53 hosted zones
func (s *AWSSource) extractDNSZoneMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Zone name (critical identifier)
	if zoneName, ok := metaMap["Name"].(string); ok && zoneName != "" {
		properties["zone_name"] = zoneName
	}

	// Private zone flag (important for access control)
	if privateZone, ok := metaMap["PrivateZone"].(bool); ok {
		properties["private_zone"] = privateZone
		// A non-private hosted zone is reachable via the public DNS hierarchy.
		properties["is_public_entry"] = !privateZone
	}

	// Extract alias target DNS name (needed for Route53->LoadBalancer relationship)
	if aliasTarget, ok := metaMap["AliasTarget"].(map[string]interface{}); ok {
		if dnsName, ok := aliasTarget["DNSName"].(string); ok && dnsName != "" {
			properties["alias_target_dns"] = dnsName
		}
		if hostedZoneId, ok := aliasTarget["HostedZoneId"].(string); ok && hostedZoneId != "" {
			properties["alias_target_zone_id"] = hostedZoneId
		}
	}
}
