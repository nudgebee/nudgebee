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
// subnet, its App Service Plan (compute host), and the data resources referenced
// by its app settings / connection strings (extracted secret-free by the collector
// into meta.referenced_resources).
type azureFunctionMeta struct {
	Properties struct {
		VirtualNetworkSubnetID string `json:"virtualNetworkSubnetId"`
		ServerFarmID           string `json:"serverFarmId"`
	} `json:"properties"`
	ReferencedResources []azureReferencedResource `json:"referenced_resources"`
}

// azureReferencedResource is a secret-free reference to a resource an app uses,
// extracted from its app settings / connection strings by the collector. `Name`
// is the resource's short name or host label (never a key or connection string).
type azureReferencedResource struct {
	Kind string `json:"kind"` // storage | keyvault | sql | postgres | mysql | redis | servicebus | cognitive
	Name string `json:"name"`
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
	case "microsoft.network/privateendpoints":
		s.resolvePrivateEndpointOwnEdges(r, srcNode, resolve, addEdge)
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

// azurePrivateEndpointMeta captures a Private Endpoint's own linkage — the
// consumer subnet it lives in and the PaaS resource(s) it connects to — as
// populated by the private-endpoint collector lister.
type azurePrivateEndpointMeta struct {
	Properties struct {
		Subnet                              *azureIDRef                   `json:"subnet"`
		PrivateLinkServiceConnections       []azurePrivateLinkServiceConn `json:"privateLinkServiceConnections"`
		ManualPrivateLinkServiceConnections []azurePrivateLinkServiceConn `json:"manualPrivateLinkServiceConnections"`
	} `json:"properties"`
}

type azurePrivateLinkServiceConn struct {
	Properties struct {
		PrivateLinkServiceID string `json:"privateLinkServiceId"`
	} `json:"properties"`
}

// resolvePrivateEndpointOwnEdges wires a Private Endpoint from its own metadata:
// PE → consumer Subnet (HOSTED_ON) and PE → target PaaS resource (ASSOCIATED_WITH).
// The target side covers resources (e.g. Key Vault) whose own meta doesn't expose
// privateEndpointConnections, so resolvePrivateEndpointEdges alone misses them.
// Together they make the private-link path traceable: subnet ← PE → PaaS resource.
func (s *AzureSource) resolvePrivateEndpointOwnEdges(
	r *CloudResourceRow,
	peNode *core.DbNode,
	resolve func(string) *core.DbNode,
	addEdge func(src, dst *core.DbNode, rel core.RelationshipType, connectionType string),
) {
	var meta azurePrivateEndpointMeta
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return
	}
	if meta.Properties.Subnet != nil && meta.Properties.Subnet.ID != "" {
		if subnet := resolve(meta.Properties.Subnet.ID); subnet != nil {
			addEdge(peNode, subnet, core.RelationshipHostedOn, "private_endpoint_subnet")
		}
	}
	conns := append(meta.Properties.PrivateLinkServiceConnections, meta.Properties.ManualPrivateLinkServiceConnections...)
	for _, c := range conns {
		if c.Properties.PrivateLinkServiceID == "" {
			continue
		}
		if target := resolve(c.Properties.PrivateLinkServiceID); target != nil && target.ID != peNode.ID {
			addEdge(peNode, target, core.RelationshipAssociatedWith, "private_link")
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
	// App Service Plan is the compute host for the site (serverFarmId is always
	// present on a web/site).
	if plan := resolve(meta.Properties.ServerFarmID); plan != nil {
		addEdge(fnNode, plan, core.RelationshipHostedOn, "app_service_plan")
	}
}

// azureRefKindToNodeType maps a collector-extracted app-config reference kind to
// the KG node type its Name should match.
var azureRefKindToNodeType = map[string]core.NodeType{
	"storage":    core.NodeTypeStorage,
	"keyvault":   core.NodeTypeSecretVault,
	"sql":        core.NodeTypeDatabase,
	"postgres":   core.NodeTypeDatabase,
	"mysql":      core.NodeTypeDatabase,
	"redis":      core.NodeTypeCache,
	"servicebus": core.NodeTypeMessageQueue,
	"cognitive":  core.NodeTypeAIService,
}

// createAppReferenceEdges connects an App Service / Function to the data
// resources it references in its app settings / connection strings — the
// app -> Storage/KeyVault/Database/Cache/… dependency layer. It consumes the
// secret-free references the collector extracted into meta.referenced_resources
// (kind + resource name), matching each to a node by (nodeType, name).
func (s *AzureSource) createAppReferenceEdges(resources []CloudResourceRow, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	byTypeName := make(map[core.NodeType]map[string]*core.DbNode)
	for nt, nodes := range lookup.byNodeType {
		m := make(map[string]*core.DbNode, len(nodes))
		for _, n := range nodes {
			if name, ok := n.Properties["name"].(string); ok && name != "" {
				m[strings.ToLower(name)] = n
			}
		}
		byTypeName[nt] = m
	}
	arnIndex := lowercaseResourceIndex(lookup)

	seen := make(map[string]bool)
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(r.ServiceName, "microsoft.web/sites") {
			continue
		}
		var meta azureFunctionMeta
		if err := json.Unmarshal(r.Meta, &meta); err != nil || len(meta.ReferencedResources) == 0 {
			continue
		}
		if site := arnIndex[strings.ToLower(r.ARN)]; site != nil {
			edges = append(edges, s.appRefEdgesForSite(site, meta.ReferencedResources, byTypeName, seen, req)...)
		}
	}
	s.logger.Info("created Azure app-config reference edges", "edge_count", len(edges))
	return edges
}

// appRefEdgesForSite emits deduplicated Function -> resource CALLS edges for one
// site's referenced resources.
func (s *AzureSource) appRefEdgesForSite(site *core.DbNode, refs []azureReferencedResource, byTypeName map[core.NodeType]map[string]*core.DbNode, seen map[string]bool, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0, len(refs))
	for _, ref := range refs {
		nt, ok := azureRefKindToNodeType[strings.ToLower(ref.Kind)]
		if !ok || ref.Name == "" {
			continue
		}
		target := byTypeName[nt][strings.ToLower(ref.Name)]
		if target == nil || target.ID == site.ID {
			continue
		}
		dk := site.ID + "|" + target.ID
		if seen[dk] {
			continue
		}
		seen[dk] = true
		edges = append(edges, s.createEdge(site, target, core.RelationshipCalls,
			map[string]interface{}{"connection_type": "app_config", "ref_kind": ref.Kind}, req))
	}
	return edges
}

// ============================================================================
// RBAC role-assignment access edges (ServiceIdentity -> resource HAS_ACCESS_TO)
// ============================================================================

const (
	azureRoleAssignmentService       = "microsoft.authorization/roleassignments"
	azureUserAssignedIdentityService = "microsoft.managedidentity/userassignedidentities"
)

// azureBuiltInRoleNames maps the common Azure built-in role GUIDs to friendly
// names so the emitted edges are self-describing. Unmapped roles fall back to the
// raw GUID. Keys are lowercase GUIDs.
var azureBuiltInRoleNames = map[string]string{
	"4633458b-17de-408a-b874-0445c86b69e6": "Key Vault Secrets User",
	"b86a8fe4-44ce-4948-aee5-eccb2c155cd7": "Key Vault Secrets Officer",
	"21090545-7ca7-4776-b22c-e363652d74d2": "Key Vault Reader",
	"ba92f5b4-2d11-453d-a403-e96b0029c9fe": "Storage Blob Data Contributor",
	"2a2b9908-6ea1-4ae2-8e65-a410df84e7d1": "Storage Blob Data Reader",
	"974c5e8b-45b9-4653-ba55-5f855dd0fb88": "Storage Queue Data Contributor",
	"7f951dda-4ed3-4680-a7ca-43fe172d538d": "AcrPull",
	"8311e382-0749-4cb8-b61a-304f252e45ec": "AcrPush",
	"a97b65f3-24c7-4388-baec-2e87135dc908": "Cognitive Services User",
	"00482a5a-887f-4fb3-b363-3b7fe8e74483": "Key Vault Crypto Service Encryption User",
	"4d97b98b-1d4f-4787-a291-c67834d212e7": "Network Contributor",
	"3913510d-42f4-4e42-8a64-420c390055eb": "Monitoring Metrics Publisher",
	"de139f84-1756-47ae-9be6-808fbbe84772": "Website Contributor",
	"b24988ac-6180-42a0-ab88-20f7382dd24c": "Contributor",
	"acdd72a7-3385-48ef-bd42-f606fba81ae7": "Reader",
	"8e3af657-a8ff-443c-a75c-2fe8c4bcb635": "Owner",
}

// azureRoleName extracts the trailing GUID from a roleDefinitionId and maps it to
// a friendly built-in role name, falling back to the GUID.
func azureRoleName(roleDefinitionID string) string {
	guid := roleDefinitionID
	if i := strings.LastIndex(guid, "/"); i >= 0 {
		guid = guid[i+1:]
	}
	if name, ok := azureBuiltInRoleNames[strings.ToLower(guid)]; ok {
		return name
	}
	return guid
}

// buildPrincipalIndex maps a managed-identity principalId (objectId) to the KG
// node that acts under it, covering both identity kinds:
//   - user-assigned: the userAssignedIdentity resource is the principal
//     (meta.properties.principalId) → its own ServiceIdentity node.
//   - system-assigned: the hosting resource is the principal
//     (meta.identity.principalId) → that workload's node (VM/Function/AKS/…),
//     so a role granted to a resource's system identity attributes to the
//     resource directly.
func (s *AzureSource) buildPrincipalIndex(resources []CloudResourceRow, arnIndex map[string]*core.DbNode) map[string]*core.DbNode {
	idx := make(map[string]*core.DbNode)
	for i := range resources {
		r := &resources[i]
		node := arnIndex[strings.ToLower(r.ARN)]
		if node == nil {
			node = arnIndex[strings.ToLower(r.ResourceID)]
		}
		if node == nil {
			continue
		}
		var meta struct {
			Identity struct {
				PrincipalID string `json:"principalId"`
			} `json:"identity"`
			Properties struct {
				PrincipalID string `json:"principalId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(r.Meta, &meta); err != nil {
			continue
		}
		// User-assigned identity resource: the identity itself is the principal.
		if strings.EqualFold(r.ServiceName, azureUserAssignedIdentityService) && meta.Properties.PrincipalID != "" {
			idx[strings.ToLower(meta.Properties.PrincipalID)] = node
		}
		// System-assigned identity: the hosting resource is the principal.
		// Don't clobber a user-assigned mapping for the same principalId.
		if meta.Identity.PrincipalID != "" {
			if _, exists := idx[strings.ToLower(meta.Identity.PrincipalID)]; !exists {
				idx[strings.ToLower(meta.Identity.PrincipalID)] = node
			}
		}
	}
	return idx
}

// createRoleAssignmentEdges turns Azure RBAC role assignments into
// (identity|workload) -> resource HAS_ACCESS_TO edges. It resolves each
// assignment's resource-level scope to a collected node and its principal to the
// acting node via principalId — a user-assigned ServiceIdentity node, or, for a
// system-assigned identity, the hosting workload node directly (see
// buildPrincipalIndex). Resource-group/subscription-scoped assignments (no node)
// and non-ServicePrincipal principals (users/groups) are skipped. Combined with
// resource->ServiceIdentity RUNS_AS edges, this yields the app -> data-resource
// access path.
func (s *AzureSource) createRoleAssignmentEdges(resources []CloudResourceRow, lookup *NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	arnIndex := lowercaseResourceIndex(lookup)
	principalIndex := s.buildPrincipalIndex(resources, arnIndex)
	if len(principalIndex) == 0 {
		return edges
	}

	seen := make(map[string]bool)
	for i := range resources {
		r := &resources[i]
		if !strings.EqualFold(r.ServiceName, azureRoleAssignmentService) {
			continue
		}
		var meta struct {
			PrincipalID      string `json:"principalId"`
			PrincipalType    string `json:"principalType"`
			RoleDefinitionID string `json:"roleDefinitionId"`
			Scope            string `json:"scope"`
		}
		if err := json.Unmarshal(r.Meta, &meta); err != nil {
			continue
		}
		if !strings.EqualFold(meta.PrincipalType, "ServicePrincipal") || meta.PrincipalID == "" || meta.Scope == "" {
			continue
		}
		idNode := principalIndex[strings.ToLower(meta.PrincipalID)]
		if idNode == nil {
			continue // principal is not a collected user-assigned identity
		}
		target := arnIndex[strings.ToLower(meta.Scope)]
		if target == nil || target.ID == idNode.ID {
			continue // RG/subscription scope (no node) or self-reference
		}
		dk := idNode.ID + "|" + target.ID
		if seen[dk] {
			continue
		}
		seen[dk] = true
		edges = append(edges, s.createEdge(idNode, target, core.RelationshipHasAccessTo,
			map[string]interface{}{"evidence": "rbac_role_assignment", "role": azureRoleName(meta.RoleDefinitionID)}, req))
	}
	s.logger.Info("created Azure RBAC access edges", "edge_count", len(edges))
	return edges
}
