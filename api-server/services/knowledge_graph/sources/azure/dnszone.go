package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// azureDNSZoneSchema is the concrete per-specific_type property schema for the
// AzureDNSZone specific_type. Names are the node.Properties keys written by
// extractDNSZoneMetadata; universal base keys are implicit.
var azureDNSZoneSchema = core.SpecificTypeSchema{
	SpecificType: "AzureDNSZone",
	NodeType:     core.NodeTypeDNSZone,
	Properties: []core.PropertyDef{
		{Name: "record_set_count", Indexed: true}, // ~ record_count
		{Name: "zone_type", Indexed: true},
		{Name: "resource_group", Indexed: true},
		{Name: "is_public_entry"},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureDNSZoneSchema) }

// extractDNSZoneMetadata handles Azure DNS Zone (subtype=dnszone, public) and Private DNS Zone (subtype=privatednszone).
// Note: A-records live as separate child resources (Microsoft.Network/dnszones/A); this extractor
// only captures zone-level metadata. Record extraction would require ingesting the child types.
func (s *AzureSource) extractDNSZoneMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if recordCount, ok := metaMap["numberOfRecordSets"]; ok {
		properties["record_set_count"] = recordCount
	}
	if zoneType, ok := metaMap["zoneType"].(string); ok && zoneType != "" {
		properties["zone_type"] = zoneType
	}
	subtype, _ := properties["subtype"].(string)
	properties["is_public_entry"] = !strings.HasPrefix(strings.ToLower(subtype), "privatednszone")
}
