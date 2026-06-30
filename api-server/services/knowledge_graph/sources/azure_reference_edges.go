package sources

import (
	"encoding/json"
	"strings"

	"nudgebee/services/knowledge_graph/core"
)

// Azure reference-edge resolution.
//
// The base Azure source only wires up networking topology (VNet/Subnet/NIC/NSG/VM)
// via live Azure CLI calls, leaving LoadBalancers, PublicIPs, and their backends
// orphaned. The Azure Resource Graph payload already collected in
// cloud_resourses.meta (nb_source="api") embeds the relationships as resource IDs
// — e.g. a LoadBalancer's frontend PublicIPs and backend pool members. This file
// mines those embedded IDs and matches them against existing nodes to emit edges,
// without any extra Azure API calls.
//
// IDs are matched case-insensitively: node `arn` is stored lowercase while ARG
// embeds PascalCase resource IDs. Sub-resource IDs (…/backendAddressPools/…/
// ipConfigurations/…) are normalized up to their top-level resource so they
// resolve to the VMSS / NIC / PublicIP node that actually exists in the graph.

// azureTopLevelResourceID truncates an Azure resource ID to its top-level
// resource: /subscriptions/{s}/resourceGroups/{rg}/providers/{ns}/{type}/{name}.
// Sub-resource IDs (extra /{subtype}/{subname}… segments) collapse to the parent
// resource, which is what exists as a node. Non-conforming IDs are returned as-is.
func azureTopLevelResourceID(id string) string {
	parts := strings.Split(id, "/")
	// Expected: ["", "subscriptions", "{s}", "resourceGroups", "{rg}",
	//            "providers", "{ns}", "{type}", "{name}", ...sub-resource...]
	if len(parts) >= 9 &&
		strings.EqualFold(parts[1], "subscriptions") &&
		strings.EqualFold(parts[5], "providers") {
		return strings.Join(parts[:9], "/")
	}
	return id
}

// lowercaseResourceIndex builds a case-insensitive resource-ID → node index keyed
// by both `arn` and `resource_id`, so PascalCase ARG references resolve to nodes
// regardless of casing or which identifier a node carries. (Synthetic Subnet
// nodes have no `arn` and are only addressable by their full `resource_id`.)
func lowercaseResourceIndex(lookup *NodeLookup) map[string]*core.DbNode {
	idx := make(map[string]*core.DbNode, len(lookup.byARN)+len(lookup.byResourceID))
	for arn, node := range lookup.byARN {
		idx[strings.ToLower(arn)] = node
	}
	for rid, node := range lookup.byResourceID {
		k := strings.ToLower(rid)
		if _, exists := idx[k]; !exists {
			idx[k] = node
		}
	}
	return idx
}

// azureIDRef is the ubiquitous { "id": "/subscriptions/..." } reference shape.
type azureIDRef struct {
	ID string `json:"id"`
}

// azureLoadBalancerMeta is the subset of a LoadBalancer's ARG payload needed to
// resolve its frontend PublicIPs and backend pool members.
type azureLoadBalancerMeta struct {
	Properties struct {
		FrontendIPConfigurations []struct {
			Properties struct {
				PublicIPAddress *azureIDRef `json:"publicIPAddress"`
			} `json:"properties"`
		} `json:"frontendIPConfigurations"`
		BackendAddressPools []struct {
			Properties struct {
				BackendIPConfigurations []azureIDRef `json:"backendIPConfigurations"`
			} `json:"properties"`
		} `json:"backendAddressPools"`
	} `json:"properties"`
}

// azureAKSMeta resolves an AKS cluster's node-pool subnets and the managed
// resource group that holds its node-pool VM scale sets.
type azureAKSMeta struct {
	Properties struct {
		NodeResourceGroup string `json:"nodeResourceGroup"`
		AgentPoolProfiles []struct {
			VnetSubnetID string `json:"vnetSubnetID"`
		} `json:"agentPoolProfiles"`
	} `json:"properties"`
}

// azureNICMeta resolves a network interface's attached PublicIPs.
type azureNICMeta struct {
	Properties struct {
		IPConfigurations []struct {
			Properties struct {
				PublicIPAddress *azureIDRef `json:"publicIPAddress"`
			} `json:"properties"`
		} `json:"ipConfigurations"`
	} `json:"properties"`
}

// azurePEConnectionsMeta resolves the private endpoints attached to a PaaS
// resource (storage account, key vault, SQL, …) — present on the resource side
// regardless of the endpoint's own collection source.
type azurePEConnectionsMeta struct {
	Properties struct {
		PrivateEndpointConnections []struct {
			Properties struct {
				PrivateEndpoint *azureIDRef `json:"privateEndpoint"`
			} `json:"properties"`
		} `json:"privateEndpointConnections"`
	} `json:"properties"`
}

// azureIdentityMeta resolves user-assigned managed identities attached to any
// resource. The map keys are the identity resource IDs.
type azureIdentityMeta struct {
	Identity struct {
		UserAssignedIdentities map[string]json.RawMessage `json:"userAssignedIdentities"`
	} `json:"identity"`
}

// azureVMMeta resolves a virtual machine's attached network interfaces.
type azureVMMeta struct {
	Properties struct {
		NetworkProfile struct {
			NetworkInterfaces []azureIDRef `json:"networkInterfaces"`
		} `json:"networkProfile"`
	} `json:"properties"`
}

// azureNSGMeta resolves the subnets and network interfaces a network security
// group is attached to.
type azureNSGMeta struct {
	Properties struct {
		Subnets           []azureIDRef `json:"subnets"`
		NetworkInterfaces []azureIDRef `json:"networkInterfaces"`
	} `json:"properties"`
}

// azureFunctionMeta resolves an App Service / Function app's VNet-integration
// subnet.
type azureFunctionMeta struct {
	Properties struct {
		VirtualNetworkSubnetID string `json:"virtualNetworkSubnetId"`
	} `json:"properties"`
}

// azureVNetRuleMeta resolves VNet service-endpoint / firewall rules that allow a
// PaaS resource (storage account, Cosmos DB, key vault, …) from specific subnets.
// Storage/KeyVault put the rules under networkAcls; Cosmos at the top level.
type azureVNetRuleMeta struct {
	Properties struct {
		VirtualNetworkRules []azureIDRef `json:"virtualNetworkRules"`
		NetworkACLs         struct {
			VirtualNetworkRules []azureIDRef `json:"virtualNetworkRules"`
		} `json:"networkAcls"`
	} `json:"properties"`
}

// createReferenceEdges derives cross-resource edges from the embedded resource IDs
// in each resource's ARG meta, matched against existing nodes. It is additive to
// the networking edges built elsewhere and runs directly off the raw resource rows
// (not nodes) so it is unaffected by node de-duplication of sparse billing copies.
func (s *AzureSource) createReferenceEdges(resources []CloudResourceRow, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	arnIndex := lowercaseResourceIndex(lookup)
	vmssByRG := vmssByResourceGroup(lookup)
	var edges []*core.DbEdge
	seen := make(map[string]struct{})

	addEdge := func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string) {
		if src == nil || dst == nil || src.ID == dst.ID {
			return
		}
		key := src.ID + "|" + dst.ID + "|" + string(rel)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		edges = append(edges, s.createEdge(src, dst, rel, map[string]interface{}{
			"connection_type": connectionType,
		}, req))
	}

	// resolve matches an embedded resource ID to a node. It tries the exact ID
	// first (so sub-resources that are themselves nodes — e.g. subnets — match
	// directly), then falls back to the top-level resource (so deep refs like a
	// backend ipConfiguration collapse to the VMSS / NIC node that exists).
	resolve := func(id string) *core.DbNode {
		if id == "" {
			return nil
		}
		if n := arnIndex[strings.ToLower(id)]; n != nil {
			return n
		}
		return arnIndex[strings.ToLower(azureTopLevelResourceID(id))]
	}

	for i := range resources {
		r := &resources[i]
		srcNode := arnIndex[strings.ToLower(r.ARN)]
		if srcNode == nil || len(r.Meta) == 0 {
			continue
		}
		s.dispatchResourceEdges(r, srcNode, resolve, vmssByRG, addEdge)
	}
	return edges
}

// dispatchResourceEdges routes a single resource to its service-specific resolver
// and runs the generic (any-resource) resolvers.
func (s *AzureSource) dispatchResourceEdges(
	r *CloudResourceRow,
	srcNode *core.DbNode,
	resolve func(string) *core.DbNode,
	vmssByRG map[string][]*core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	switch strings.ToLower(r.ServiceName) {
	case "microsoft.network/loadbalancers", "microsoft.network/applicationgateways":
		// Application gateways are L7 load balancers (typed as LoadBalancer nodes)
		// and share the frontend/backend ARG shape, so the same resolver connects
		// their frontend PublicIPs and any NIC/VMSS backend members.
		s.resolveLoadBalancerEdges(r, srcNode, resolve, addEdge)
	case "microsoft.containerservice/managedclusters":
		s.resolveAKSEdges(r, srcNode, resolve, vmssByRG, addEdge)
	case "microsoft.network/networkinterfaces":
		s.resolveNICEdges(r, srcNode, resolve, addEdge)
	case "microsoft.compute/virtualmachines":
		s.resolveVMEdges(r, srcNode, resolve, addEdge)
	case "microsoft.network/networksecuritygroups":
		s.resolveNSGEdges(r, srcNode, resolve, addEdge)
	case "microsoft.web/sites":
		s.resolveFunctionEdges(r, srcNode, resolve, addEdge)
	case "microsoft.storage/storageaccounts/fileservices":
		// A file service is a sub-resource of its storage account; link it to the
		// parent (collapsing its own id to the top-level resource).
		if parent := resolve(azureTopLevelResourceID(r.ARN)); parent != nil {
			addEdge(srcNode, parent, core.RelationshipBelongsTo, "storage_subresource")
		}
	}

	// Generic reference edges available on any resource type.
	s.resolvePrivateEndpointEdges(r, srcNode, resolve, addEdge)
	s.resolveUserAssignedIdentityEdges(r, srcNode, resolve, addEdge)
	s.resolveVNetRuleEdges(r, srcNode, resolve, addEdge)
}

// resolveVNetRuleEdges emits resource → Subnet (ASSOCIATED_WITH) for VNet
// service-endpoint / firewall rules that allow the resource from specific subnets.
func (s *AzureSource) resolveVNetRuleEdges(
	r *CloudResourceRow,
	resourceNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureVNetRuleMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	rules := append(meta.Properties.VirtualNetworkRules, meta.Properties.NetworkACLs.VirtualNetworkRules...)
	for _, rule := range rules {
		if subnet := resolve(rule.ID); subnet != nil {
			addEdge(resourceNode, subnet, core.RelationshipAssociatedWith, "vnet_service_endpoint")
		}
	}
}

// vmssByResourceGroup indexes VM Scale Set nodes by their (lowercased) resource
// group, so an AKS cluster can be linked to the node-pool scale sets that live in
// its managed resource group.
func vmssByResourceGroup(lookup *NodeLookup) map[string][]*core.DbNode {
	idx := make(map[string][]*core.DbNode)
	for _, node := range lookup.byNodeType[core.NodeTypeComputeInstance] {
		svc, _ := node.Properties["service_name"].(string)
		if !strings.HasSuffix(strings.ToLower(svc), "virtualmachinescalesets") {
			continue
		}
		arn, _ := node.Properties["arn"].(string)
		if rg := azureResourceGroupFromID(arn); rg != "" {
			idx[rg] = append(idx[rg], node)
		}
	}
	return idx
}

// azureResourceGroupFromID extracts the lowercased resource group from an Azure
// resource ID (/subscriptions/{s}/resourceGroups/{rg}/...). Returns "" if absent.
func azureResourceGroupFromID(id string) string {
	parts := strings.Split(id, "/")
	if len(parts) >= 5 && strings.EqualFold(parts[3], "resourceGroups") {
		return strings.ToLower(parts[4])
	}
	return ""
}

// resolveLoadBalancerEdges emits LoadBalancer → backend (VMSS/NIC) ROUTES_TO edges
// and PublicIP → LoadBalancer ASSOCIATED_WITH edges from the LB's ARG meta.
func (s *AzureSource) resolveLoadBalancerEdges(
	r *CloudResourceRow,
	lbNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureLoadBalancerMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}

	// Frontend: PublicIP --ASSOCIATED_WITH--> LoadBalancer (mirrors the AWS
	// PublicIP → compute convention where the address is the edge source).
	for _, fe := range meta.Properties.FrontendIPConfigurations {
		if fe.Properties.PublicIPAddress == nil {
			continue
		}
		if pip := resolve(fe.Properties.PublicIPAddress.ID); pip != nil {
			addEdge(pip, lbNode, core.RelationshipAssociatedWith, "lb_frontend_ip")
		}
	}

	// Backend: LoadBalancer --ROUTES_TO--> VMSS / NIC member. The backend IP
	// config IDs are deeply nested (…/virtualMachineScaleSets/x/…/ipConfigurations/y
	// or …/networkInterfaces/x/ipConfigurations/y); azureTopLevelResourceID
	// collapses both forms to the node that exists.
	for _, pool := range meta.Properties.BackendAddressPools {
		for _, ipc := range pool.Properties.BackendIPConfigurations {
			if backend := resolve(ipc.ID); backend != nil {
				addEdge(lbNode, backend, core.RelationshipRoutesTo, "lb_backend_pool")
			}
		}
	}
}

// resolveAKSEdges emits AKS → Subnet (HOSTED_ON) from agent-pool subnet refs and
// AKS → VMSS (MANAGES) for the node-pool scale sets in the cluster's managed
// (MC_*) resource group.
func (s *AzureSource) resolveAKSEdges(
	r *CloudResourceRow,
	aksNode *core.DbNode,
	resolve func(string) *core.DbNode,
	vmssByRG map[string][]*core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureAKSMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}

	for _, pool := range meta.Properties.AgentPoolProfiles {
		if subnet := resolve(pool.VnetSubnetID); subnet != nil {
			addEdge(aksNode, subnet, core.RelationshipHostedOn, "aks_node_subnet")
		}
	}

	// The node-pool VM scale sets live in the cluster's managed resource group;
	// Azure dedicates one MC_* group per cluster, so every VMSS there belongs to
	// this AKS.
	if rg := strings.ToLower(meta.Properties.NodeResourceGroup); rg != "" {
		for _, vmss := range vmssByRG[rg] {
			addEdge(aksNode, vmss, core.RelationshipManages, "aks_node_pool")
		}
	}
}

// resolveNICEdges emits PublicIP → NIC (ASSOCIATED_WITH) for the public addresses
// attached to a network interface's IP configurations.
func (s *AzureSource) resolveNICEdges(
	r *CloudResourceRow,
	nicNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureNICMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	for _, ipc := range meta.Properties.IPConfigurations {
		if ipc.Properties.PublicIPAddress == nil {
			continue
		}
		if pip := resolve(ipc.Properties.PublicIPAddress.ID); pip != nil {
			addEdge(pip, nicNode, core.RelationshipAssociatedWith, "nic_public_ip")
		}
	}
}

// resolvePrivateEndpointEdges emits PrivateEndpoint → resource (ASSOCIATED_WITH)
// for every private endpoint a PaaS resource (storage/key vault/SQL/…) lists.
// Works regardless of the endpoint's own collection source, since the linkage is
// recorded on the resource side.
func (s *AzureSource) resolvePrivateEndpointEdges(
	r *CloudResourceRow,
	resourceNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azurePEConnectionsMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	for _, conn := range meta.Properties.PrivateEndpointConnections {
		if conn.Properties.PrivateEndpoint == nil {
			continue
		}
		if pe := resolve(conn.Properties.PrivateEndpoint.ID); pe != nil {
			addEdge(pe, resourceNode, core.RelationshipAssociatedWith, "private_endpoint")
		}
	}
}

// resolveUserAssignedIdentityEdges emits resource → ManagedIdentity (RUNS_AS) for
// every user-assigned identity attached to a resource.
func (s *AzureSource) resolveUserAssignedIdentityEdges(
	r *CloudResourceRow,
	resourceNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureIdentityMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	for identityID := range meta.Identity.UserAssignedIdentities {
		if id := resolve(identityID); id != nil {
			addEdge(resourceNode, id, core.RelationshipRunsAs, "user_assigned_identity")
		}
	}
}

// resolveVMEdges emits VirtualMachine → NIC (ASSOCIATED_WITH) from the VM's
// network profile, connecting VMs that the live-CLI NIC fetch missed.
func (s *AzureSource) resolveVMEdges(
	r *CloudResourceRow,
	vmNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureVMMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	for _, nic := range meta.Properties.NetworkProfile.NetworkInterfaces {
		if n := resolve(nic.ID); n != nil {
			addEdge(vmNode, n, core.RelationshipAssociatedWith, "vm_nic")
		}
	}
}

// resolveNSGEdges emits NSG → Subnet / NIC (PROTECTS) for the subnets and network
// interfaces a network security group is attached to.
func (s *AzureSource) resolveNSGEdges(
	r *CloudResourceRow,
	nsgNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureNSGMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	for _, sn := range meta.Properties.Subnets {
		if subnet := resolve(sn.ID); subnet != nil {
			addEdge(nsgNode, subnet, core.RelationshipProtects, "nsg_subnet")
		}
	}
	for _, ni := range meta.Properties.NetworkInterfaces {
		if nic := resolve(ni.ID); nic != nil {
			addEdge(nsgNode, nic, core.RelationshipProtects, "nsg_nic")
		}
	}
}

// resolveFunctionEdges emits ServerlessFunction → Subnet (HOSTED_ON) for an App
// Service / Function app's VNet integration.
func (s *AzureSource) resolveFunctionEdges(
	r *CloudResourceRow,
	fnNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azureFunctionMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	if subnet := resolve(meta.Properties.VirtualNetworkSubnetID); subnet != nil {
		addEdge(fnNode, subnet, core.RelationshipHostedOn, "function_subnet")
	}
}
