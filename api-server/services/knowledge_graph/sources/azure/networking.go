package azure

import (
	"encoding/json"
	"fmt"
	"nudgebee/services/cloud"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/security"
	"sort"
	"strings"
)

// extractNSGMetadata derives internet-exposure signals from an NSG's inbound
// securityRules (metaMap is the `properties` sub-object): open_to_internet (any
// Inbound+Allow rule whose source is *, 0.0.0.0/0, or the "Internet" service tag),
// exposed_ports (its destination port ranges), and ingress_cidrs (all inbound source
// prefixes). Runs at node-build time so open_to_internet/exposed_ports are Indexed —
// mirrors the AWS SG-exposure. Only runs when the NSG carries rules.
func (s *AzureSource) extractNSGMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	rules, ok := metaMap["securityRules"].([]interface{})
	if !ok {
		return
	}

	internetSrc := func(v string) bool { return v == "*" || v == "0.0.0.0/0" || v == "Internet" }

	openToInternet := false
	exposedPorts := map[string]struct{}{}
	cidrs := map[string]struct{}{}

	for _, r := range rules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		rp, ok := rm["properties"].(map[string]interface{})
		if !ok || rp["direction"] != "Inbound" {
			continue
		}

		var srcs []string
		if v, ok := rp["sourceAddressPrefix"].(string); ok && v != "" {
			srcs = append(srcs, v)
		}
		if arr, ok := rp["sourceAddressPrefixes"].([]interface{}); ok {
			for _, a := range arr {
				if v, ok := a.(string); ok && v != "" {
					srcs = append(srcs, v)
				}
			}
		}
		for _, v := range srcs {
			cidrs[v] = struct{}{}
		}

		if rp["access"] != "Allow" {
			continue
		}
		ruleOpen := false
		for _, v := range srcs {
			if internetSrc(v) {
				ruleOpen = true
			}
		}
		if !ruleOpen {
			continue
		}
		openToInternet = true

		if p, ok := rp["destinationPortRange"].(string); ok && p != "" {
			exposedPorts[p] = struct{}{}
		}
		if arr, ok := rp["destinationPortRanges"].([]interface{}); ok {
			for _, a := range arr {
				if p, ok := a.(string); ok && p != "" {
					exposedPorts[p] = struct{}{}
				}
			}
		}
	}

	properties["open_to_internet"] = openToInternet
	if len(exposedPorts) > 0 {
		ports := make([]string, 0, len(exposedPorts))
		for p := range exposedPorts {
			ports = append(ports, p)
		}
		sort.Strings(ports)
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

// Concrete per-specific_type property schemas for the Azure networking
// specific_types. Names are
// the node.Properties keys written by the extractors in this file (extractVNet/
// Subnet/NIC-Metadata, extractCommonNetworkIDs, ensureSubnetNodes/NICNodes/
// NSGNodes) plus the base keys stamped by classify.go; universal base keys are
// implicit. NOTE: for NIC, the subnet_id/vnet_id/nsg_id/public_ip_id/
// attached_vm_id enrichments applied to pre-existing nodes happen AFTER
// core.NewNode in ensureNICNodes, so they are declared but NOT indexed (their
// query_attributes are not recomputed); subnet_id still populates via the
// per-NodeType QueryablePropertiesMap fallback.
var azureVirtualNetworkSchema = core.SpecificTypeSchema{
	SpecificType: "AzureVirtualNetwork",
	NodeType:     core.NodeTypeVPC,
	Properties: []core.PropertyDef{
		{Name: "vnet_id", Indexed: true},
		{Name: "cidr_blocks"}, // ~ cidr (addressPrefixes list)
		{Name: "resource_group", Indexed: true},
		{Name: "peered_vnet_ids"}, // remote VNet ARM IDs → VNet↔VNet peering edges
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "provisioning_state"},
	},
}

var azureSubnetSchema = core.SpecificTypeSchema{
	SpecificType: "AzureSubnet",
	NodeType:     core.NodeTypeSubnet,
	Properties: []core.PropertyDef{
		{Name: "subnet_id", Indexed: true},
		{Name: "cidr_block", Indexed: true},
		{Name: "vnet_id", Indexed: true},
		{Name: "nsg_id"},
		{Name: "vnet_name"},
		{Name: "vnet_name_hierarchy"},
		{Name: "inferred"},
		{Name: "resource_group", Indexed: true},
	},
}

var azureNetworkInterfaceSchema = core.SpecificTypeSchema{
	SpecificType: "AzureNetworkInterface",
	NodeType:     core.NodeTypeNetworkInterface,
	Properties: []core.PropertyDef{
		{Name: "private_ip", Indexed: true},
		{Name: "resource_group", Indexed: true},
		// Set post-NewNode via CLI enrichment (ensureNICNodes) for existing nodes —
		// declared only, not indexed.
		{Name: "subnet_id"},
		{Name: "vnet_id"},
		{Name: "nsg_id"},
		{Name: "public_ip_id"},
		{Name: "attached_vm_id"},
		{Name: "inferred"},
		// Additional provider fields (not yet emitted by our extractor):
		// (private_ip_addresses is the full list; private_ip above is the primary)
		{Name: "mac_address"},
		{Name: "private_ip_addresses"},
	},
}

var azureNetworkSecurityGroupSchema = core.SpecificTypeSchema{
	SpecificType: "AzureNetworkSecurityGroup",
	NodeType:     core.NodeTypeSecurityGroup,
	Properties: []core.PropertyDef{
		{Name: "vnet_id", Indexed: true},
		{Name: "nsg_id"},
		{Name: "inferred"},
		{Name: "resource_group", Indexed: true},
		// Internet-exposure signals from inbound securityRules (extractNSGMetadata,
		// node-build time → Indexed, like the AWS SG-exposure).
		{Name: "open_to_internet", Indexed: true},
		{Name: "exposed_ports", Indexed: true},
		{Name: "ingress_cidrs"},
	},
}

var azureRouteTableSchema = core.SpecificTypeSchema{
	SpecificType: "AzureRouteTable",
	NodeType:     core.NodeTypeRouteTable,
	Properties: []core.PropertyDef{
		{Name: "vnet_id", Indexed: true},
		{Name: "resource_group", Indexed: true},
	},
}

var azureNATGatewaySchema = core.SpecificTypeSchema{
	SpecificType: "AzureNATGateway",
	NodeType:     core.NodeTypeNetworkGateway,
	Properties: []core.PropertyDef{
		{Name: "vnet_id", Indexed: true},
		{Name: "resource_group", Indexed: true},
	},
}

var azurePrivateEndpointSchema = core.SpecificTypeSchema{
	SpecificType: "AzurePrivateEndpoint",
	NodeType:     core.NodeTypePrivateEndpoint,
	Properties: []core.PropertyDef{
		{Name: "vnet_id", Indexed: true},
		{Name: "resource_group", Indexed: true},
	},
}

// azurePublicIPSchema is minimal — the source captures no IP-address value on the
// public-IP node itself; registered to satisfy specific_type coverage.
var azurePublicIPSchema = core.SpecificTypeSchema{
	SpecificType: "AzurePublicIP",
	NodeType:     core.NodeTypePublicIP,
	Properties: []core.PropertyDef{
		{Name: "resource_group", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "ip_address"},
		{Name: "public_ip_allocation_method"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(azureVirtualNetworkSchema)
	core.RegisterSpecificTypeSchema(azureSubnetSchema)
	core.RegisterSpecificTypeSchema(azureNetworkInterfaceSchema)
	core.RegisterSpecificTypeSchema(azureNetworkSecurityGroupSchema)
	core.RegisterSpecificTypeSchema(azureRouteTableSchema)
	core.RegisterSpecificTypeSchema(azureNATGatewaySchema)
	core.RegisterSpecificTypeSchema(azurePrivateEndpointSchema)
	core.RegisterSpecificTypeSchema(azurePublicIPSchema)
}

// extractCommonNetworkIDs extracts vnet_id, subnet_id, nsg_id from metadata.
// Checks both flat keys (vnetId) and nested Azure Resource Graph structures (networkProfile.networkInterfaces).
func (s *AzureSource) extractCommonNetworkIDs(properties map[string]interface{}, metaMap, propsMap map[string]interface{}) {
	// Flat keys (used in simplified/mock data)
	if vnetID, ok := metaMap["vnetId"].(string); ok && vnetID != "" {
		properties["vnet_id"] = vnetID
	}
	if subnetID, ok := metaMap["subnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if nsgID, ok := metaMap["nsgId"].(string); ok && nsgID != "" {
		properties["nsg_id"] = nsgID
	}
	if rg, ok := metaMap["resourceGroup"].(string); ok && rg != "" {
		properties["resource_group"] = rg
	}

	// Also check propsMap (nested properties)
	if vnetID, ok := propsMap["vnetId"].(string); ok && vnetID != "" {
		properties["vnet_id"] = vnetID
	}
	if subnetID, ok := propsMap["subnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if nsgID, ok := propsMap["nsgId"].(string); ok && nsgID != "" {
		properties["nsg_id"] = nsgID
	}
}

func (s *AzureSource) extractVNetMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if addressSpace, ok := metaMap["addressSpace"].(map[string]interface{}); ok {
		if prefixes, ok := addressSpace["addressPrefixes"].([]interface{}); ok {
			properties["cidr_blocks"] = prefixes
		}
	}
	if vnetID, ok := metaMap["vnetId"].(string); ok && vnetID != "" {
		properties["vnet_id"] = vnetID
	}
	// Also use resource_id as vnet_id for VNet nodes
	if resourceID, ok := properties["resource_id"].(string); ok && resourceID != "" {
		properties["vnet_id"] = resourceID
	}

	// Peered VNet ARM IDs (for VNet↔VNet peering edges). Azure Resource Graph nests
	// these under properties.virtualNetworkPeerings[].properties.remoteVirtualNetwork.id
	// (metaMap here is already the `properties` sub-object).
	if peerings, ok := metaMap["virtualNetworkPeerings"].([]interface{}); ok && len(peerings) > 0 {
		peeredIDs := make([]string, 0, len(peerings))
		for _, p := range peerings {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			pp, ok := pm["properties"].(map[string]interface{})
			if !ok {
				continue
			}
			remote, ok := pp["remoteVirtualNetwork"].(map[string]interface{})
			if !ok {
				continue
			}
			if id, ok := remote["id"].(string); ok && id != "" {
				peeredIDs = append(peeredIDs, id)
			}
		}
		if len(peeredIDs) > 0 {
			properties["peered_vnet_ids"] = peeredIDs
		}
	}
}

// createVNetPeeringEdges builds VNet↔VNet peering edges from peered_vnet_ids (the
// remote VNet ARM IDs extracted from properties.virtualNetworkPeerings). The remote
// ID is matched to the target VNet node by resource_id (case-insensitive). Emitted as
// ASSOCIATED_WITH(connection_type="vnet_peering"), directional per peering — a mutual
// peering yields one edge in each direction.
func (s *AzureSource) createVNetPeeringEdges(vnetNodes []*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	vnetByResourceID := make(map[string]*core.DbNode)
	for _, vnetNode := range vnetNodes {
		if resourceID, ok := vnetNode.Properties["resource_id"].(string); ok && resourceID != "" {
			vnetByResourceID[strings.ToLower(resourceID)] = vnetNode
		}
	}

	for _, vnetNode := range vnetNodes {
		var peered []string
		switch v := vnetNode.Properties["peered_vnet_ids"].(type) {
		case []string:
			peered = v
		case []interface{}:
			for _, r := range v {
				if id, ok := r.(string); ok {
					peered = append(peered, id)
				}
			}
		}
		for _, remoteID := range peered {
			if remoteID == "" {
				continue
			}
			if target, exists := vnetByResourceID[strings.ToLower(remoteID)]; exists && target.ID != vnetNode.ID {
				edges = append(edges, s.createEdge(vnetNode, target, core.RelationshipAssociatedWith,
					map[string]interface{}{"connection_type": "vnet_peering"}, req))
			}
		}
	}

	return edges
}

func (s *AzureSource) extractSubnetMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if addressPrefix, ok := metaMap["addressPrefix"].(string); ok && addressPrefix != "" {
		properties["cidr_block"] = addressPrefix
	}
	if subnetID, ok := metaMap["subnetId"].(string); ok && subnetID != "" {
		properties["subnet_id"] = subnetID
	}
	if resourceID, ok := properties["resource_id"].(string); ok && resourceID != "" {
		properties["subnet_id"] = resourceID
	}
}

func (s *AzureSource) extractNICMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if privateIP, ok := metaMap["privateIpAddress"].(string); ok && privateIP != "" {
		properties["private_ip"] = privateIP
	}
	if vmID, ok := metaMap["virtualMachineId"].(string); ok && vmID != "" {
		properties["attached_vm_id"] = vmID
	}
}

// propagateVNetNamesToResources updates resources with VNet names for hierarchy
func (s *AzureSource) propagateVNetNamesToResources(nodes []*core.DbNode, lookup *sources.NodeLookup) {
	// Build VNet ID → name map
	vnetNameMap := make(map[string]string)
	if vnetNodes, ok := lookup.ByNodeType[core.NodeTypeVPC]; ok {
		for _, vnetNode := range vnetNodes {
			if vnetID, ok := vnetNode.Properties["vnet_id"].(string); ok && vnetID != "" {
				if name, ok := vnetNode.Properties["name"].(string); ok && name != "" {
					vnetNameMap[vnetID] = name
				}
			}
			// Also map by resource_id
			if resourceID, ok := vnetNode.Properties["resource_id"].(string); ok && resourceID != "" {
				if name, ok := vnetNode.Properties["name"].(string); ok && name != "" {
					vnetNameMap[resourceID] = name
				}
			}
		}
	}

	// Propagate VNet names
	for _, node := range nodes {
		if node.NodeType == core.NodeTypeVPC {
			continue
		}
		if vnetID, ok := node.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetName, found := vnetNameMap[vnetID]; found {
				node.Properties["vnet_name_hierarchy"] = vnetName
			}
		}
	}
}

// createVNetEdges creates edges for VNet → Subnet relationships
func (s *AzureSource) createVNetEdges(vnetNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	subnetNodes, hasSubnets := lookup.ByNodeType[core.NodeTypeSubnet]
	if !hasSubnets {
		return edges
	}

	// Build VNet resource_id lookup (case-insensitive)
	vnetByResourceID := make(map[string]*core.DbNode)
	for _, vnetNode := range vnetNodes {
		if resourceID, ok := vnetNode.Properties["resource_id"].(string); ok && resourceID != "" {
			vnetByResourceID[strings.ToLower(resourceID)] = vnetNode
		}
	}

	for _, subnetNode := range subnetNodes {
		// Try to find parent VNet via vnet_id in subnet metadata
		if vnetID, ok := subnetNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			vnetIDLower := strings.ToLower(vnetID)
			if vnetNode, exists := vnetByResourceID[vnetIDLower]; exists {
				edges = append(edges, s.createEdge(subnetNode, vnetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vnet_subnet"}, req))
			} else if vnetNode, exists := lookup.ByResourceID[vnetIDLower]; exists {
				edges = append(edges, s.createEdge(subnetNode, vnetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vnet_subnet"}, req))
			}
		}
	}

	return edges
}

// createDefaultVNetEdges creates basic VNet relationship for resources with vnet_id
func (s *AzureSource) createDefaultVNetEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		if vnetID, ok := node.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.ByResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(node, vnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vnet"}, req))
			}
		}
	}

	return edges
}

// createSubnetEdges creates edges for Subnet → VNet and Subnet → NSG relationships
func (s *AzureSource) createSubnetEdges(subnetNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, subnetNode := range subnetNodes {
		// Subnet → VNet (belongs_to)
		if vnetID, ok := subnetNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if vnetNode, exists := lookup.ByResourceID[strings.ToLower(vnetID)]; exists {
				edges = append(edges, s.createEdge(subnetNode, vnetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "vnet_subnet"}, req))
			}
		}

		// Subnet → NSG (protects)
		if nsgID, ok := subnetNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if nsgNode, exists := lookup.ByResourceID[strings.ToLower(nsgID)]; exists {
				edges = append(edges, s.createEdge(nsgNode, subnetNode, core.RelationshipProtects,
					map[string]interface{}{"connection_type": "nsg"}, req))
			}
		}
	}

	return edges
}

// createNICEdges creates edges for NIC → VM, NIC → Subnet, NIC → NSG relationships
func (s *AzureSource) createNICEdges(nicNodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Build VM lookup by resource_id (case-insensitive)
	vmByID := make(map[string]*core.DbNode)
	if vmNodes, ok := lookup.ByNodeType[core.NodeTypeComputeInstance]; ok {
		for _, vm := range vmNodes {
			if rid, ok := vm.Properties["resource_id"].(string); ok && rid != "" {
				vmByID[strings.ToLower(rid)] = vm
			}
			if arn, ok := vm.Properties["arn"].(string); ok && arn != "" {
				vmByID[strings.ToLower(arn)] = vm
			}
		}
	}

	for _, nicNode := range nicNodes {
		// NIC → Subnet (hosted_on)
		if subnetID, ok := nicNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if subnetNode, exists := lookup.ByResourceID[strings.ToLower(subnetID)]; exists {
				edges = append(edges, s.createEdge(nicNode, subnetNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "subnet"}, req))
			}
		}

		// NIC → NSG (protects)
		if nsgID, ok := nicNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if nsgNode, exists := lookup.ByResourceID[strings.ToLower(nsgID)]; exists {
				edges = append(edges, s.createEdge(nsgNode, nicNode, core.RelationshipProtects,
					map[string]interface{}{"connection_type": "nsg"}, req))
			}
		}

		// VM → NIC (associated_with)
		if vmID, ok := nicNode.Properties["attached_vm_id"].(string); ok && vmID != "" {
			if vmNode, exists := vmByID[strings.ToLower(vmID)]; exists {
				edges = append(edges, s.createEdge(vmNode, nicNode, core.RelationshipAssociatedWith,
					map[string]interface{}{"connection_type": "network_interface"}, req))
			}
		}
	}

	return edges
}

// ensureSubnetNodes extracts subnets embedded in VNet metadata and creates synthetic subnet nodes.
// Azure Resource Graph stores subnets inside VNet's properties.subnets[], not as separate resources.
func (s *AzureSource) ensureSubnetNodes(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbNode {
	vnetNodes, hasVNets := lookup.ByNodeType[core.NodeTypeVPC]
	if !hasVNets {
		return nodes
	}

	for _, vnetNode := range vnetNodes {
		vnetResourceID, _ := vnetNode.Properties["resource_id"].(string)
		vnetName, _ := vnetNode.Properties["name"].(string)
		region, _ := vnetNode.Properties["region"].(string)

		// Parse subnets from the raw metadata stored during node creation.
		// _raw_meta is stored as json.RawMessage; accept either that or []byte.
		var metaJSON []byte
		switch v := vnetNode.Properties["_raw_meta"].(type) {
		case json.RawMessage:
			metaJSON = []byte(v)
		case []byte:
			metaJSON = v
		}
		if len(metaJSON) == 0 {
			continue
		}

		var metaMap map[string]interface{}
		if err := json.Unmarshal(metaJSON, &metaMap); err != nil {
			continue
		}

		propsMap, _ := metaMap["properties"].(map[string]interface{})
		if propsMap == nil {
			propsMap = metaMap
		}

		subnets, ok := propsMap["subnets"].([]interface{})
		if !ok {
			continue
		}

		for _, subnetRaw := range subnets {
			subnetMap, ok := subnetRaw.(map[string]interface{})
			if !ok {
				continue
			}

			subnetID, _ := subnetMap["id"].(string)
			subnetName, _ := subnetMap["name"].(string)
			if subnetID == "" {
				continue
			}

			// Skip if already exists
			subnetIDLower := strings.ToLower(subnetID)
			if _, exists := lookup.ByResourceID[subnetIDLower]; exists {
				continue
			}

			// Extract CIDR from subnet properties
			cidr := ""
			if subProps, ok := subnetMap["properties"].(map[string]interface{}); ok {
				if prefix, ok := subProps["addressPrefix"].(string); ok {
					cidr = prefix
				} else if prefixes, ok := subProps["addressPrefixes"].([]interface{}); ok && len(prefixes) > 0 {
					if p, ok := prefixes[0].(string); ok {
						cidr = p
					}
				}
			}

			// Extract NSG ID from subnet properties
			nsgID := ""
			if subProps, ok := subnetMap["properties"].(map[string]interface{}); ok {
				if nsgMap, ok := subProps["networkSecurityGroup"].(map[string]interface{}); ok {
					if id, ok := nsgMap["id"].(string); ok {
						nsgID = strings.ToLower(id)
					}
				}
			}

			properties := map[string]interface{}{
				"name":           subnetName,
				"resource_id":    subnetIDLower,
				"subnet_id":      subnetIDLower,
				"vnet_id":        strings.ToLower(vnetResourceID),
				"vnet_name":      vnetName,
				"region":         region,
				"cloud_provider": "Azure",
				"service_name":   "microsoft.network/virtualnetworks/subnets",
				"type":           "subnets",
				"subtype":        "subnets",
				"inferred":       true,
			}
			if cidr != "" {
				properties["cidr_block"] = cidr
			}
			if nsgID != "" {
				properties["nsg_id"] = nsgID
			}

			tempNode := &core.DbNode{
				NodeType:       core.NodeTypeSubnet,
				Properties:     properties,
				CloudAccountID: req.CloudAccountID,
			}
			uniqueKey := s.GenerateUniqueKey(tempNode)

			subnetNode := core.NewNode(core.NodeTypeSubnet, uniqueKey, properties, req.TenantID, req.CloudAccountID, "azure")
			nodes = append(nodes, subnetNode)
			lookup.ByResourceID[subnetIDLower] = subnetNode
			lookup.ByNodeType[core.NodeTypeSubnet] = append(lookup.ByNodeType[core.NodeTypeSubnet], subnetNode)

			s.logger.Debug("created inferred subnet node from VNet metadata",
				"subnet_name", subnetName,
				"subnet_id", subnetIDLower,
				"vnet_name", vnetName)
		}
	}

	s.logger.Info("ensureSubnetNodes completed",
		"total_subnets", len(lookup.ByNodeType[core.NodeTypeSubnet]))

	return nodes
}

// ensureNICNodes fetches network interfaces via Azure CLI and creates NIC nodes.
// NICs are critical for resolving VM → Subnet → VNet relationships in Azure.
func (s *AzureSource) ensureNICNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbNode {
	if reqCtx == nil || req.CloudAccountID == "" {
		s.logger.Warn("skipping NIC fetch: missing reqCtx or cloud account ID")
		return nodes
	}

	nics, err := s.fetchNICsFromAzure(reqCtx, req)
	if err != nil {
		s.logger.Warn("failed to fetch NICs from Azure CLI, edges may be incomplete",
			"error", err)
		return nodes
	}

	for _, nic := range nics {
		nicIDLower := strings.ToLower(nic.ID)

		// Build the network-relationship properties from the CLI response
		netProps := map[string]interface{}{}
		if len(nic.IPConfigurations) > 0 {
			ipConfig := nic.IPConfigurations[0]
			if ipConfig.PrivateIPAddress != "" {
				netProps["private_ip"] = ipConfig.PrivateIPAddress
			}
			if ipConfig.Subnet.ID != "" {
				subnetID := strings.ToLower(ipConfig.Subnet.ID)
				netProps["subnet_id"] = subnetID
				if vnetID := extractVNetIDFromSubnetID(subnetID); vnetID != "" {
					netProps["vnet_id"] = vnetID
				}
			}
			if ipConfig.PublicIPAddress != nil && ipConfig.PublicIPAddress.ID != "" {
				netProps["public_ip_id"] = strings.ToLower(ipConfig.PublicIPAddress.ID)
			}
		}
		if nic.NetworkSecurityGroup != nil && nic.NetworkSecurityGroup.ID != "" {
			netProps["nsg_id"] = strings.ToLower(nic.NetworkSecurityGroup.ID)
		}
		if nic.VirtualMachine != nil && nic.VirtualMachine.ID != "" {
			netProps["attached_vm_id"] = strings.ToLower(nic.VirtualMachine.ID)
		}

		// If a NIC with this resource_id is already in lookup (from cloud_resourses),
		// enrich it with the CLI-derived properties so VM<->NIC<->Subnet edges can be created.
		if existing, exists := lookup.ByResourceID[nicIDLower]; exists {
			if existing.Properties == nil {
				existing.Properties = make(map[string]interface{})
			}
			for k, v := range netProps {
				if _, already := existing.Properties[k]; !already {
					existing.Properties[k] = v
				}
			}
			continue
		}

		properties := map[string]interface{}{
			"name":           nic.Name,
			"resource_id":    nicIDLower,
			"region":         nic.Location,
			"resource_group": nic.ResourceGroup,
			"cloud_provider": "Azure",
			"service_name":   "microsoft.network/networkinterfaces",
			"type":           "networkinterfaces",
			"subtype":        "networkinterfaces",
			"inferred":       true,
		}
		for k, v := range netProps {
			properties[k] = v
		}

		tempNode := &core.DbNode{
			NodeType:       core.NodeTypeNetworkInterface,
			Properties:     properties,
			CloudAccountID: req.CloudAccountID,
		}
		uniqueKey := s.GenerateUniqueKey(tempNode)

		nicNode := core.NewNode(core.NodeTypeNetworkInterface, uniqueKey, properties, req.TenantID, req.CloudAccountID, "azure")
		nodes = append(nodes, nicNode)
		lookup.ByResourceID[nicIDLower] = nicNode
		lookup.ByNodeType[core.NodeTypeNetworkInterface] = append(lookup.ByNodeType[core.NodeTypeNetworkInterface], nicNode)
	}

	s.logger.Info("ensureNICNodes completed",
		"nics_fetched", len(nics),
		"total_nic_nodes", len(lookup.ByNodeType[core.NodeTypeNetworkInterface]))

	return nodes
}

// ensureNSGNodes fetches network security groups via Azure CLI and creates NSG nodes.
func (s *AzureSource) ensureNSGNodes(reqCtx *security.RequestContext, nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbNode {
	if reqCtx == nil || req.CloudAccountID == "" {
		return nodes
	}

	nsgs, err := s.fetchNSGsFromAzure(reqCtx, req)
	if err != nil {
		s.logger.Warn("failed to fetch NSGs from Azure CLI",
			"error", err)
		return nodes
	}

	for _, nsg := range nsgs {
		nsgIDLower := strings.ToLower(nsg.ID)
		if _, exists := lookup.ByResourceID[nsgIDLower]; exists {
			continue
		}

		properties := map[string]interface{}{
			"name":           nsg.Name,
			"resource_id":    nsgIDLower,
			"region":         nsg.Location,
			"resource_group": nsg.ResourceGroup,
			"cloud_provider": "Azure",
			"service_name":   "microsoft.network/networksecuritygroups",
			"type":           "networksecuritygroups",
			"subtype":        "networksecuritygroups",
			"inferred":       true,
		}

		tempNode := &core.DbNode{
			NodeType:       core.NodeTypeSecurityGroup,
			Properties:     properties,
			CloudAccountID: req.CloudAccountID,
		}
		uniqueKey := s.GenerateUniqueKey(tempNode)

		nsgNode := core.NewNode(core.NodeTypeSecurityGroup, uniqueKey, properties, req.TenantID, req.CloudAccountID, "azure")
		nodes = append(nodes, nsgNode)
		lookup.ByResourceID[nsgIDLower] = nsgNode
		lookup.ByNodeType[core.NodeTypeSecurityGroup] = append(lookup.ByNodeType[core.NodeTypeSecurityGroup], nsgNode)
	}

	s.logger.Info("ensureNSGNodes completed",
		"nsgs_fetched", len(nsgs),
		"total_nsg_nodes", len(lookup.ByNodeType[core.NodeTypeSecurityGroup]))

	return nodes
}

// resolveVMNetworkRelationships uses NIC data to set vnet_id, subnet_id, nsg_id on VMs.
// In Azure, the chain is: VM → NIC → IP Config → Subnet → VNet.
// This method back-propagates network info from NICs to their attached VMs.
func (s *AzureSource) resolveVMNetworkRelationships(nodes []*core.DbNode, lookup *sources.NodeLookup) {
	nicNodes, hasNICs := lookup.ByNodeType[core.NodeTypeNetworkInterface]
	if !hasNICs {
		return
	}

	// Build VM resource_id → node map (case-insensitive)
	vmByResourceID := make(map[string]*core.DbNode)
	if vmNodes, ok := lookup.ByNodeType[core.NodeTypeComputeInstance]; ok {
		for _, vm := range vmNodes {
			if rid, ok := vm.Properties["resource_id"].(string); ok && rid != "" {
				vmByResourceID[strings.ToLower(rid)] = vm
			}
			// Also index by arn (Azure resource ID stored in arn column)
			if arn, ok := vm.Properties["arn"].(string); ok && arn != "" {
				vmByResourceID[strings.ToLower(arn)] = vm
			}
		}
	}

	resolved := 0
	for _, nicNode := range nicNodes {
		attachedVMID, _ := nicNode.Properties["attached_vm_id"].(string)
		if attachedVMID == "" {
			continue
		}

		vmNode, exists := vmByResourceID[strings.ToLower(attachedVMID)]
		if !exists {
			continue
		}

		// Propagate subnet_id from NIC to VM
		if subnetID, ok := nicNode.Properties["subnet_id"].(string); ok && subnetID != "" {
			if _, alreadySet := vmNode.Properties["subnet_id"].(string); !alreadySet {
				vmNode.Properties["subnet_id"] = subnetID
			}
		}

		// Propagate vnet_id from NIC to VM
		if vnetID, ok := nicNode.Properties["vnet_id"].(string); ok && vnetID != "" {
			if _, alreadySet := vmNode.Properties["vnet_id"].(string); !alreadySet {
				vmNode.Properties["vnet_id"] = vnetID
			}
		}

		// Propagate nsg_id from NIC to VM
		if nsgID, ok := nicNode.Properties["nsg_id"].(string); ok && nsgID != "" {
			if _, alreadySet := vmNode.Properties["nsg_id"].(string); !alreadySet {
				vmNode.Properties["nsg_id"] = nsgID
			}
		}

		// Propagate private_ip from NIC to VM
		if privateIP, ok := nicNode.Properties["private_ip"].(string); ok && privateIP != "" {
			if _, alreadySet := vmNode.Properties["private_ip"].(string); !alreadySet {
				vmNode.Properties["private_ip"] = privateIP
			}
		}

		resolved++
	}

	s.logger.Info("resolved VM network relationships via NICs",
		"vms_resolved", resolved)
}

// fetchNICsFromAzure fetches all network interfaces via Azure CLI using the cloud collector
func (s *AzureSource) fetchNICsFromAzure(reqCtx *security.RequestContext, req *core.SourceBuildRequest) ([]AzureNICData, error) {
	cmd := "az network nic list --output json"

	s.logger.Debug("fetching NICs from Azure",
		"account_id", req.CloudAccountID,
		"command", cmd)

	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: req.CloudAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute Azure CLI command: %w", err)
	}

	output := sources.ExtractCLIOutput(resp)
	if output == "" {
		return nil, fmt.Errorf("invalid response format from cloud CLI for NIC list")
	}

	var nics []AzureNICData
	if err := json.Unmarshal([]byte(output), &nics); err != nil {
		s.logger.Error("failed to parse NIC JSON", "error", err)
		return nil, fmt.Errorf("failed to parse NIC response: %w", err)
	}

	s.logger.Info("successfully fetched NICs from Azure",
		"count", len(nics))

	return nics, nil
}

// fetchNSGsFromAzure fetches all network security groups via Azure CLI
func (s *AzureSource) fetchNSGsFromAzure(reqCtx *security.RequestContext, req *core.SourceBuildRequest) ([]AzureNSGData, error) {
	cmd := "az network nsg list --output json"

	s.logger.Debug("fetching NSGs from Azure",
		"account_id", req.CloudAccountID,
		"command", cmd)

	resp, err := cloud.ExecuteCli(reqCtx, cloud.CloudExecuteCliCommandRequest{
		AccountID: req.CloudAccountID,
		Command:   cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute Azure CLI command: %w", err)
	}

	output := sources.ExtractCLIOutput(resp)
	if output == "" {
		return nil, fmt.Errorf("invalid response format from cloud CLI for NSG list")
	}

	var nsgs []AzureNSGData
	if err := json.Unmarshal([]byte(output), &nsgs); err != nil {
		s.logger.Error("failed to parse NSG JSON", "error", err)
		return nil, fmt.Errorf("failed to parse NSG response: %w", err)
	}

	s.logger.Info("successfully fetched NSGs from Azure",
		"count", len(nsgs))

	return nsgs, nil
}

// extractVNetIDFromSubnetID extracts the VNet resource ID from a subnet resource ID.
// Subnet ID format: /subscriptions/.../virtualNetworks/<vnet>/subnets/<subnet>
// VNet ID format:   /subscriptions/.../virtualNetworks/<vnet>
func extractVNetIDFromSubnetID(subnetID string) string {
	lower := strings.ToLower(subnetID)
	idx := strings.Index(lower, "/subnets/")
	if idx > 0 {
		return subnetID[:idx]
	}
	return ""
}
