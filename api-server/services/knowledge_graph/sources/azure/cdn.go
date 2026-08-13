package azure

import "nudgebee/services/knowledge_graph/core"

// azureCDNProfileSchema is the concrete per-specific_type property schema for the
// AzureCDNProfile specific_type (Azure Front Door / CDN Profile). Names are the
// node.Properties keys written by extractCDNMetadata; universal base keys are
// implicit. The hostname/backend lists are declared for documentation but not
// indexed (arrays are not useful SQL filter keys).
var azureCDNProfileSchema = core.SpecificTypeSchema{
	SpecificType: "AzureCDNProfile",
	NodeType:     core.NodeTypeCDN,
	Properties: []core.PropertyDef{
		{Name: "cdn_sku", Indexed: true},
		{Name: "resource_group", Indexed: true},
		{Name: "frontend_hostnames"}, // ~ domain_name (list)
		{Name: "backend_addresses"},
		{Name: "is_public_entry"},
	},
}

func init() { core.RegisterSpecificTypeSchema(azureCDNProfileSchema) }

// extractCDNMetadata handles Azure Front Door (subtype=frontdoor) and CDN Profile (subtype=cdnprofile).
// Front Door exposes frontendEndpoints + backendPools inline; CDN Profile only exposes SKU
// (origin endpoints are separate child resources Microsoft.Cdn/profiles/endpoints).
// Both Front Door and CDN Profile are always internet-facing.
func (s *AzureSource) extractCDNMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	properties["is_public_entry"] = true
	s.extractAzureFrontDoorFrontends(properties, metaMap)
	s.extractAzureFrontDoorBackends(properties, metaMap)
	if sku, ok := metaMap["sku"].(map[string]interface{}); ok {
		if name, ok := sku["name"].(string); ok && name != "" {
			properties["cdn_sku"] = name
		}
	}
}

// extractAzureFrontDoorFrontends reads frontendEndpoints[].properties.hostName.
func (s *AzureSource) extractAzureFrontDoorFrontends(properties map[string]interface{}, metaMap map[string]interface{}) {
	endpoints, ok := metaMap["frontendEndpoints"].([]interface{})
	if !ok {
		return
	}
	var hostnames []interface{}
	for _, e := range endpoints {
		eProps := nestedProps(e)
		if eProps == nil {
			continue
		}
		if h, ok := eProps["hostName"].(string); ok && h != "" {
			hostnames = append(hostnames, h)
		}
	}
	if len(hostnames) > 0 {
		properties["frontend_hostnames"] = hostnames
	}
}

// extractAzureFrontDoorBackends reads backendPools[].properties.backends[].address.
func (s *AzureSource) extractAzureFrontDoorBackends(properties map[string]interface{}, metaMap map[string]interface{}) {
	pools, ok := metaMap["backendPools"].([]interface{})
	if !ok {
		return
	}
	var backendAddrs []interface{}
	for _, p := range pools {
		pProps := nestedProps(p)
		if pProps == nil {
			continue
		}
		if backends, ok := pProps["backends"].([]interface{}); ok {
			for _, b := range backends {
				bMap, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				if v, ok := bMap["address"].(string); ok && v != "" {
					backendAddrs = append(backendAddrs, v)
				}
			}
		}
	}
	if len(backendAddrs) > 0 {
		properties["backend_addresses"] = backendAddrs
	}
}
