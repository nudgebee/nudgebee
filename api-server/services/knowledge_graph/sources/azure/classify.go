package azure

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// shouldSuppressAzureResource drops rows that are cost/billing constructs rather
// than deployed infrastructure. It is deliberately narrow: Azure billing-export
// rows (meta.nb_source="billing") include *real* resources that have no ARM
// counterpart (e.g. Private Endpoints), so we must NOT blanket-suppress by source.
// Reservation orders (Microsoft.Capacity/reservationOrders, type "reservationorders")
// are purchase commitments — they were surfacing as orphaned CloudResource phantoms.
func shouldSuppressAzureResource(r *sources.CloudResourceRow) bool {
	if r == nil {
		return true
	}
	if strings.EqualFold(r.Type, "reservationorders") {
		return true
	}
	if strings.Contains(strings.ToLower(r.ServiceName), "reservationorders") ||
		strings.Contains(strings.ToLower(r.ServiceName), "microsoft.capacity") {
		return true
	}
	return false
}

// createNodeFromResource creates a knowledge graph node from an Azure cloud resource
func (s *AzureSource) createNodeFromResource(resource *sources.CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	source := "azure"
	nodeType := s.determineNodeType(resource.Type, resource.ServiceName)

	properties := make(map[string]interface{})
	properties["name"] = resource.Name
	properties["type"] = resource.Type
	properties["status"] = resource.Status
	properties["cloud_provider"] = resource.CloudProvider
	properties["region"] = resource.Region
	properties["arn"] = resource.ARN // Azure Resource ID stored in arn column
	properties["resource_id"] = resource.ResourceID
	properties["service_name"] = resource.ServiceName
	properties["is_active"] = resource.IsActive
	properties["external_resource_id"] = resource.ExternalResourceID
	properties["labels"] = resource.Tags

	// Store identifiers
	properties["nb_resource_id"] = resource.ID
	properties["nb_account_id"] = resource.Account
	properties["account_number"] = resource.AccountNumber // Azure subscription ID

	// Add subtype
	properties["subtype"] = resource.Type

	// Declare the concrete cloud label (specific_type). NewNode lifts this out of
	// properties into the dedicated column.
	properties["specific_type"] = s.determineSpecificType(nodeType, resource.ServiceName, resource.Type)

	// Parse metadata
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil {
			s.extractEssentialMetadataByNodeType(properties, metaMap, nodeType, resource.ServiceName)
		}
		// Store raw meta for VNet nodes so ensureSubnetNodes can extract embedded subnets
		if nodeType == core.NodeTypeVPC {
			properties["_raw_meta"] = resource.Meta
		}
	}

	// Parse tags
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			properties["labels"] = tagsMap
		}
	}

	// Build unique key
	tempNode := &core.DbNode{
		NodeType:       nodeType,
		Properties:     properties,
		CloudAccountID: req.CloudAccountID,
	}
	uniqueKey := s.GenerateUniqueKey(tempNode)

	return core.NewNode(nodeType, uniqueKey, properties, req.TenantID, req.CloudAccountID, source)
}

// extractServicePrefix extracts the provider prefix from a full service_name path.
// e.g., "microsoft.compute/virtualmachines" → "microsoft.compute"
func extractServicePrefix(serviceName string) string {
	if idx := strings.Index(serviceName, "/"); idx > 0 {
		return serviceName[:idx]
	}
	return serviceName
}

// determineNodeType maps Azure resource types to knowledge graph node types.
// It handles both formats:
//   - Original format: type="VirtualMachine", service_name="Microsoft.Compute"
//   - DB format:       type="virtualmachines", service_name="microsoft.compute/virtualmachines"
func (s *AzureSource) determineNodeType(resourceType, serviceName string) core.NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	// Step 1: Direct type lookup (handles both singular and plural lowercase)
	if nodeType, exists := azureTypeToNodeType[resourceTypeLower]; exists {
		return nodeType
	}

	// Step 2: Some collectors store the full provider-qualified form
	// (e.g. "Microsoft.Network/routeTables") instead of the bare type segment.
	// Try the last "/"-delimited segment as a fallback.
	if idx := strings.LastIndex(resourceTypeLower, "/"); idx >= 0 && idx < len(resourceTypeLower)-1 {
		if nodeType, exists := azureTypeToNodeType[resourceTypeLower[idx+1:]]; exists {
			return nodeType
		}
	}

	// Step 3: Service prefix fallback
	servicePrefix := strings.ToLower(extractServicePrefix(serviceName))
	if nodeType, exists := azureServicePrefixToNodeType[servicePrefix]; exists {
		return nodeType
	}

	return core.NodeTypeCloudResource
}

// extractEssentialMetadataByNodeType extracts Azure-specific metadata fields based on node type.
// Azure Resource Graph returns meta in a nested format:
//
//	{ "id": "...", "name": "...", "properties": { ...actual resource data... } }
//
// This method normalizes by checking both top-level and nested "properties" keys.
func (s *AzureSource) extractEssentialMetadataByNodeType(properties map[string]interface{}, metaMap map[string]interface{}, nodeType core.NodeType, serviceName string) {
	// Azure Resource Graph nests actual data under "properties" key
	propsMap := metaMap
	if nested, ok := metaMap["properties"].(map[string]interface{}); ok {
		propsMap = nested
	}

	// Extract resource group from top-level meta (Azure Resource Graph puts it at top level)
	if rg, ok := metaMap["resourceGroup"].(string); ok && rg != "" {
		properties["resource_group"] = rg
	}

	// Extract location from top-level meta
	if loc, ok := metaMap["location"].(string); ok && loc != "" {
		// Override region with the location from meta if present (more accurate)
		properties["region"] = loc
	}

	// Extract VNet/Subnet IDs common to many resource types (check both levels)
	s.extractCommonNetworkIDs(properties, metaMap, propsMap)

	switch nodeType {
	case core.NodeTypeComputeInstance:
		s.extractVMMetadata(properties, propsMap)
	case core.NodeTypeManagedCluster:
		s.extractAKSMetadata(properties, propsMap)
	case core.NodeTypeVPC:
		s.extractVNetMetadata(properties, propsMap)
	case core.NodeTypeSubnet:
		s.extractSubnetMetadata(properties, propsMap)
	case core.NodeTypeSecurityGroup:
		s.extractNSGMetadata(properties, propsMap)
	case core.NodeTypeDatabase:
		s.extractDatabaseMetadata(properties, propsMap)
	case core.NodeTypeLoadBalancer:
		s.extractLoadBalancerMetadata(properties, propsMap)
	case core.NodeTypeCache:
		s.extractCacheMetadata(properties, propsMap)
	case core.NodeTypeNetworkInterface:
		s.extractNICMetadata(properties, propsMap)
	case core.NodeTypeCDN:
		s.extractCDNMetadata(properties, propsMap)
	case core.NodeTypeDNSZone:
		s.extractDNSZoneMetadata(properties, propsMap)
	}
}
