package azure

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// azureVirtualMachineSchema is the fixed concrete-property schema for the
// AzureVirtualMachine specific_type. Names are the node.Properties keys
// written by extractVMMetadata + extractCommonNetworkIDs (+ post-build network
// propagation); universal base keys (name/arn/region/subtype/...) are implicit
// (see core.universalBaseProperties). Indexed fields are hoisted into
// query_attributes for filtering — the Azure-native realizations of the compute
// filter intent in core.QueryablePropertiesMap[ComputeInstance].
var azureVirtualMachineSchema = core.SpecificTypeSchema{
	SpecificType: "AzureVirtualMachine",
	NodeType:     core.NodeTypeComputeInstance,
	Properties: []core.PropertyDef{
		{Name: "vm_size", Indexed: true},            // ~ instance_type
		{Name: "provisioning_state", Indexed: true}, // ~ instance_state
		{Name: "os_type", Indexed: true},
		{Name: "private_ip", Indexed: true},
		{Name: "public_ip", Indexed: true},
		{Name: "vnet_id", Indexed: true}, // ~ vpc_id
		{Name: "subnet_id", Indexed: true},
		{Name: "nsg_id", Indexed: true},
		{Name: "resource_group", Indexed: true},
		{Name: "network_interface_ids"},
		{Name: "vnet_name_hierarchy"},
		// Provider VM fields lifted from ARG meta when present.
		{Name: "license_type"},
		{Name: "computer_name"},
		{Name: "priority"},
		{Name: "eviction_policy"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "product"},           // plan.product
		{Name: "zones"},             // zones
		{Name: "ultra_ssd_enabled"}, // additional_capabilities.ultra_ssd_enabled
	},
}

func init() { core.RegisterSpecificTypeSchema(azureVirtualMachineSchema) }

func (s *AzureSource) extractVMMetadata(properties map[string]interface{}, propsMap map[string]interface{}) {
	// Flat keys (simplified/mock data)
	if vmSize, ok := propsMap["vmSize"].(string); ok && vmSize != "" {
		properties["vm_size"] = vmSize
	}
	if osType, ok := propsMap["osType"].(string); ok && osType != "" {
		properties["os_type"] = osType
	}
	if privateIP, ok := propsMap["privateIpAddress"].(string); ok && privateIP != "" {
		properties["private_ip"] = privateIP
	}
	if publicIP, ok := propsMap["publicIpAddress"].(string); ok && publicIP != "" {
		properties["public_ip"] = publicIP
	}
	if nicIDs, ok := propsMap["networkInterfaceIds"].([]interface{}); ok {
		properties["network_interface_ids"] = nicIDs
	}

	// Azure Resource Graph nested format: properties.hardwareProfile.vmSize
	if hw, ok := propsMap["hardwareProfile"].(map[string]interface{}); ok {
		if vmSize, ok := hw["vmSize"].(string); ok && vmSize != "" {
			properties["vm_size"] = vmSize
		}
	}

	// properties.storageProfile.osDisk.osType
	if sp, ok := propsMap["storageProfile"].(map[string]interface{}); ok {
		if osDisk, ok := sp["osDisk"].(map[string]interface{}); ok {
			if osType, ok := osDisk["osType"].(string); ok && osType != "" {
				properties["os_type"] = osType
			}
		}
	}

	// properties.networkProfile.networkInterfaces[].id
	if np, ok := propsMap["networkProfile"].(map[string]interface{}); ok {
		if nics, ok := np["networkInterfaces"].([]interface{}); ok {
			nicIDs := make([]interface{}, 0, len(nics))
			for _, nic := range nics {
				if nicMap, ok := nic.(map[string]interface{}); ok {
					if id, ok := nicMap["id"].(string); ok && id != "" {
						nicIDs = append(nicIDs, id)
					}
				}
			}
			if len(nicIDs) > 0 {
				properties["network_interface_ids"] = nicIDs
			}
		}
	}

	// properties.provisioningState
	if state, ok := propsMap["provisioningState"].(string); ok && state != "" {
		properties["provisioning_state"] = state
	}

	// Additional provider fields lifted from ARG meta when present (guarded — skipped
	// when absent, no new API calls). properties.licenseType + osProfile.computerName.
	if licenseType, ok := propsMap["licenseType"].(string); ok && licenseType != "" {
		properties["license_type"] = licenseType
	}
	if osProfile, ok := propsMap["osProfile"].(map[string]interface{}); ok {
		if computerName, ok := osProfile["computerName"].(string); ok && computerName != "" {
			properties["computer_name"] = computerName
		}
	}
}

// createVMEdges creates edges for VM → VNet/Subnet/NSG
func (s *AzureSource) createVMEdges(vmNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, vmNode := range vmNodes {
		// VM → Subnet
		if subnetID, ok := vmNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.ByResourceID[strings.ToLower(subnetID)]; exists {
				edges = append(edges, s.createEdge(vmNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// VM → VNet
		if vnetID, ok := vmNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.ByResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(vmNode, vnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vnet"}, req))
			}
		}

		// VM → NSG
		if nsgID, ok := vmNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if nsgNode, exists := lookup.ByResourceID[strings.ToLower(nsgID)]; exists {
				edges = append(edges, s.createEdge(nsgNode, vmNode, core.RelationshipProtects,
					map[string]interface{}{"connection_type": "nsg"}, req))
			}
		}

		// VM → NIC
		if nicIDs, ok := vmNode.Properties["network_interface_ids"].([]interface{}); ok {
			for _, nicIDRaw := range nicIDs {
				if nicID, ok := nicIDRaw.(string); ok && nicID != "" {
					if nicNode, exists := lookup.ByResourceID[strings.ToLower(nicID)]; exists {
						edges = append(edges, s.createEdge(vmNode, nicNode, core.RelationshipAssociatedWith,
							map[string]interface{}{"connection_type": "network_interface"}, req))
					}
				}
			}
		}
	}

	return edges
}
