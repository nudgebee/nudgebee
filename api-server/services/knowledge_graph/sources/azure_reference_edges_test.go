package sources

import (
	"log/slog"
	"os"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestAzureTopLevelResourceID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "already top-level",
			in:   "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pip1",
			want: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/publicIPAddresses/pip1",
		},
		{
			name: "VMSS backend ipconfig collapses to the scale set",
			in:   "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/vmss1/virtualMachines/0/networkInterfaces/nic1/ipConfigurations/ip1",
			want: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/vmss1",
		},
		{
			name: "standalone NIC ipconfig collapses to the NIC",
			in:   "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/nic2/ipConfigurations/ipconfig1",
			want: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/networkInterfaces/nic2",
		},
		{
			name: "non-conforming id returned as-is",
			in:   "not-an-azure-id",
			want: "not-an-azure-id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := azureTopLevelResourceID(tt.in); got != tt.want {
				t.Errorf("azureTopLevelResourceID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func arnNode(id string, nodeType core.NodeType, arn string) *core.DbNode {
	return &core.DbNode{
		ID:       id,
		NodeType: nodeType,
		Properties: map[string]interface{}{
			"name": id,
			"arn":  arn,
		},
	}
}

// TestCreateReferenceEdges_LoadBalancer verifies LB frontend/backend edges are
// derived from ARG meta, with case-insensitive ARN matching (PascalCase meta vs
// lowercase node arns) and sub-resource normalization to VMSS / NIC nodes.
func TestCreateReferenceEdges_LoadBalancer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	const sub = "00000000-0000-0000-0000-000000000001"
	lbArn := "/subscriptions/" + sub + "/resourcegroups/rg/providers/microsoft.network/loadbalancers/lb1"
	pipArn := "/subscriptions/" + sub + "/resourcegroups/rg/providers/microsoft.network/publicipaddresses/pip1"
	vmssArn := "/subscriptions/" + sub + "/resourcegroups/rg/providers/microsoft.compute/virtualmachinescalesets/vmss1"
	nicArn := "/subscriptions/" + sub + "/resourcegroups/rg/providers/microsoft.network/networkinterfaces/nic2"

	nodes := []*core.DbNode{
		arnNode("lb1", core.NodeTypeLoadBalancer, lbArn),
		arnNode("pip1", core.NodeTypePublicIP, pipArn),
		arnNode("vmss1", core.NodeTypeComputeInstance, vmssArn),
		arnNode("nic2", core.NodeTypeNetworkInterface, nicArn),
		arnNode("unrelated", core.NodeTypeStorage, "/subscriptions/"+sub+"/resourcegroups/rg/providers/microsoft.storage/storageaccounts/sa1"),
	}
	lookup := newNodeLookup(nodes)

	// ARG meta uses PascalCase IDs and deeply-nested backend ipconfig references.
	meta := `{
      "id": "/subscriptions/` + sub + `/resourceGroups/RG/providers/Microsoft.Network/loadBalancers/lb1",
      "nb_source": "api",
      "properties": {
        "frontendIPConfigurations": [
          {"properties": {"publicIPAddress": {"id": "/subscriptions/` + sub + `/resourceGroups/RG/providers/Microsoft.Network/publicIPAddresses/pip1"}}}
        ],
        "backendAddressPools": [
          {"properties": {"backendIPConfigurations": [
            {"id": "/subscriptions/` + sub + `/resourceGroups/RG/providers/Microsoft.Compute/virtualMachineScaleSets/vmss1/virtualMachines/0/networkInterfaces/nic1/ipConfigurations/ip1"},
            {"id": "/subscriptions/` + sub + `/resourceGroups/RG/providers/Microsoft.Network/networkInterfaces/nic2/ipConfigurations/ipconfig1"}
          ]}}
        ]
      }
    }`

	resources := []CloudResourceRow{{
		ServiceName: "microsoft.network/loadbalancers",
		ARN:         lbArn,
		Meta:        []byte(meta),
	}}

	edges := source.createReferenceEdges(resources, lookup, &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"})

	type ek struct {
		src, dst string
		rel      core.RelationshipType
	}
	got := make(map[ek]bool)
	for _, e := range edges {
		got[ek{e.SourceNodeID, e.DestinationNodeID, e.RelationshipType}] = true
	}

	want := []ek{
		{"pip1", "lb1", core.RelationshipAssociatedWith}, // frontend PublicIP → LB
		{"lb1", "vmss1", core.RelationshipRoutesTo},      // backend VMSS (sub-resource collapsed)
		{"lb1", "nic2", core.RelationshipRoutesTo},       // backend NIC (sub-resource collapsed)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %s --%s--> %s", w.src, w.rel, w.dst)
		}
	}
	if len(edges) != len(want) {
		t.Errorf("got %d edges, want %d: %+v", len(edges), len(want), got)
	}
}

// TestCreateReferenceEdges_SkipsUnresolvable ensures references to nodes that are
// not in the graph (or missing meta) produce no edges and never panic.
func TestCreateReferenceEdges_SkipsUnresolvable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	lbArn := "/subscriptions/s/resourcegroups/rg/providers/microsoft.network/loadbalancers/lb1"
	lookup := newNodeLookup([]*core.DbNode{arnNode("lb1", core.NodeTypeLoadBalancer, lbArn)})

	resources := []CloudResourceRow{
		{ // backend points at a VMSS that does not exist as a node → no edge
			ServiceName: "microsoft.network/loadbalancers",
			ARN:         lbArn,
			Meta:        []byte(`{"properties":{"backendAddressPools":[{"properties":{"backendIPConfigurations":[{"id":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/ghost/virtualMachines/0/networkInterfaces/n/ipConfigurations/i"}]}}]}}`),
		},
		{ // LB with no meta
			ServiceName: "microsoft.network/loadbalancers",
			ARN:         lbArn,
			Meta:        nil,
		},
		{ // non-LB service is ignored
			ServiceName: "microsoft.storage/storageaccounts",
			ARN:         "/subscriptions/s/resourcegroups/rg/providers/microsoft.storage/storageaccounts/sa1",
			Meta:        []byte(`{"properties":{}}`),
		},
	}

	edges := source.createReferenceEdges(resources, lookup, &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"})
	if len(edges) != 0 {
		t.Errorf("expected no edges for unresolvable references, got %d", len(edges))
	}
}

func arnNodeWithService(id string, nodeType core.NodeType, arn, serviceName string) *core.DbNode {
	n := arnNode(id, nodeType, arn)
	n.Properties["service_name"] = serviceName
	return n
}

// TestCreateReferenceEdges_AKS_NIC_PE_Identity covers the additional slices: AKS →
// Subnet (HOSTED_ON) + AKS → node-pool VMSS (MANAGES via managed RG), NIC →
// PublicIP, PaaS → PrivateEndpoint, and resource → user-assigned identity.
func TestCreateReferenceEdges_AKS_NIC_PE_Identity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	const sub = "00000000-0000-0000-0000-000000000002"
	id := func(rg, provider, typ, name string) string {
		return "/subscriptions/" + sub + "/resourcegroups/" + rg + "/providers/" + provider + "/" + typ + "/" + name
	}
	// PascalCase variant for embedded meta references.
	pid := func(rg, provider, typ, name string) string {
		return "/subscriptions/" + sub + "/resourceGroups/" + rg + "/providers/" + provider + "/" + typ + "/" + name
	}

	aksArn := id("rg", "microsoft.containerservice", "managedclusters", "aks1")
	subnetArn := id("rg", "microsoft.network", "virtualnetworks", "vnet1") + "/subnets/default"
	vmssArn := id("mc_rg_aks1_eastus", "microsoft.compute", "virtualmachinescalesets", "aks-agentpool-vmss")
	nicArn := id("rg", "microsoft.network", "networkinterfaces", "nic1")
	pipArn := id("rg", "microsoft.network", "publicipaddresses", "pip1")
	saArn := id("rg", "microsoft.storage", "storageaccounts", "sa1")
	peArn := id("rg", "microsoft.network", "privateendpoints", "pe1")
	miArn := id("rg", "microsoft.managedidentity", "userassignedidentities", "mi1")

	nodes := []*core.DbNode{
		arnNode("aks1", core.NodeTypeManagedCluster, aksArn),
		arnNode("subnet1", core.NodeTypeSubnet, subnetArn),
		arnNodeWithService("vmss1", core.NodeTypeComputeInstance, vmssArn, "microsoft.compute/virtualmachinescalesets"),
		arnNode("nic1", core.NodeTypeNetworkInterface, nicArn),
		arnNode("pip1", core.NodeTypePublicIP, pipArn),
		arnNode("sa1", core.NodeTypeStorage, saArn),
		arnNode("pe1", core.NodeTypePrivateEndpoint, peArn),
		arnNode("mi1", core.NodeTypeServiceIdentity, miArn),
	}
	lookup := newNodeLookup(nodes)

	resources := []CloudResourceRow{
		{
			ServiceName: "microsoft.containerservice/managedclusters",
			ARN:         aksArn,
			Meta: []byte(`{"properties":{"nodeResourceGroup":"MC_rg_aks1_eastus","agentPoolProfiles":[{"vnetSubnetID":"` +
				pid("rg", "Microsoft.Network", "virtualNetworks", "vnet1") + `/subnets/default"}]}}`),
		},
		{
			ServiceName: "microsoft.network/networkinterfaces",
			ARN:         nicArn,
			Meta:        []byte(`{"properties":{"ipConfigurations":[{"properties":{"publicIPAddress":{"id":"` + pid("rg", "Microsoft.Network", "publicIPAddresses", "pip1") + `"}}}]}}`),
		},
		{
			ServiceName: "microsoft.storage/storageaccounts",
			ARN:         saArn,
			Meta:        []byte(`{"properties":{"privateEndpointConnections":[{"properties":{"privateEndpoint":{"id":"` + pid("rg", "Microsoft.Network", "privateEndpoints", "pe1") + `"}}}]}}`),
		},
		{
			ServiceName: "microsoft.compute/virtualmachinescalesets",
			ARN:         vmssArn,
			Meta:        []byte(`{"identity":{"userAssignedIdentities":{"` + pid("rg", "Microsoft.ManagedIdentity", "userAssignedIdentities", "mi1") + `":{}}}}`),
		},
	}

	edges := source.createReferenceEdges(resources, lookup, &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"})

	type ek struct {
		src, dst string
		rel      core.RelationshipType
	}
	got := make(map[ek]bool)
	for _, e := range edges {
		got[ek{e.SourceNodeID, e.DestinationNodeID, e.RelationshipType}] = true
	}
	want := []ek{
		{"aks1", "subnet1", core.RelationshipHostedOn},    // AKS → agent-pool subnet
		{"aks1", "vmss1", core.RelationshipManages},       // AKS → node-pool VMSS (managed RG)
		{"pip1", "nic1", core.RelationshipAssociatedWith}, // NIC public IP
		{"pe1", "sa1", core.RelationshipAssociatedWith},   // storage private endpoint
		{"vmss1", "mi1", core.RelationshipRunsAs},         // user-assigned identity
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %s --%s--> %s", w.src, w.rel, w.dst)
		}
	}
	if len(edges) != len(want) {
		t.Errorf("got %d edges, want %d: %+v", len(edges), len(want), got)
	}
}

// TestCreateReferenceEdges_VM_NSG_Function covers VM → NIC (ASSOCIATED_WITH),
// NSG → Subnet/NIC (PROTECTS), and ServerlessFunction → Subnet (HOSTED_ON).
func TestCreateReferenceEdges_VM_NSG_Function(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	const sub = "00000000-0000-0000-0000-000000000003"
	lid := func(provider, typ, name string) string {
		return "/subscriptions/" + sub + "/resourcegroups/rg/providers/" + provider + "/" + typ + "/" + name
	}
	pid := func(provider, typ, name string) string {
		return "/subscriptions/" + sub + "/resourceGroups/RG/providers/" + provider + "/" + typ + "/" + name
	}

	vmArn := lid("microsoft.compute", "virtualmachines", "vm1")
	nicArn := lid("microsoft.network", "networkinterfaces", "nic1")
	nsgArn := lid("microsoft.network", "networksecuritygroups", "nsg1")
	fnArn := lid("microsoft.web", "sites", "func1")
	// Subnet nodes are addressed by resource_id (they carry no arn).
	subnetRID := lid("microsoft.network", "virtualnetworks", "vnet1") + "/subnets/default"
	subnetNode := arnNode("subnet1", core.NodeTypeSubnet, "")
	delete(subnetNode.Properties, "arn")
	subnetNode.Properties["resource_id"] = subnetRID

	nodes := []*core.DbNode{
		arnNode("vm1", core.NodeTypeComputeInstance, vmArn),
		arnNode("nic1", core.NodeTypeNetworkInterface, nicArn),
		arnNode("nsg1", core.NodeTypeSecurityGroup, nsgArn),
		arnNode("func1", core.NodeTypeServerlessFunction, fnArn),
		subnetNode,
	}
	lookup := newNodeLookup(nodes)

	subnetPID := pid("Microsoft.Network", "virtualNetworks", "vnet1") + "/subnets/default"
	resources := []CloudResourceRow{
		{ServiceName: "microsoft.compute/virtualmachines", ARN: vmArn,
			Meta: []byte(`{"properties":{"networkProfile":{"networkInterfaces":[{"id":"` + pid("Microsoft.Network", "networkInterfaces", "nic1") + `"}]}}}`)},
		{ServiceName: "microsoft.network/networksecuritygroups", ARN: nsgArn,
			Meta: []byte(`{"properties":{"subnets":[{"id":"` + subnetPID + `"}],"networkInterfaces":[{"id":"` + pid("Microsoft.Network", "networkInterfaces", "nic1") + `"}]}}`)},
		{ServiceName: "microsoft.web/sites", ARN: fnArn,
			Meta: []byte(`{"properties":{"virtualNetworkSubnetId":"` + subnetPID + `"}}`)},
	}

	edges := source.createReferenceEdges(resources, lookup, &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"})

	type ek struct {
		src, dst string
		rel      core.RelationshipType
	}
	got := make(map[ek]bool)
	for _, e := range edges {
		got[ek{e.SourceNodeID, e.DestinationNodeID, e.RelationshipType}] = true
	}
	want := []ek{
		{"vm1", "nic1", core.RelationshipAssociatedWith},
		{"nsg1", "subnet1", core.RelationshipProtects},
		{"nsg1", "nic1", core.RelationshipProtects},
		{"func1", "subnet1", core.RelationshipHostedOn},
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %s --%s--> %s", w.src, w.rel, w.dst)
		}
	}
	if len(edges) != len(want) {
		t.Errorf("got %d edges, want %d: %+v", len(edges), len(want), got)
	}
}

// TestCreateReferenceEdges_VNetRules_FileService covers VNet service-endpoint rules
// (Storage networkAcls + Cosmos top-level) → Subnet, and storage fileservice →
// parent storage account.
func TestCreateReferenceEdges_VNetRules_FileService(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	const sub = "00000000-0000-0000-0000-000000000004"
	lid := func(provider, typ, name string) string {
		return "/subscriptions/" + sub + "/resourcegroups/rg/providers/" + provider + "/" + typ + "/" + name
	}
	pid := func(provider, typ, name string) string {
		return "/subscriptions/" + sub + "/resourceGroups/RG/providers/" + provider + "/" + typ + "/" + name
	}

	saArn := lid("microsoft.storage", "storageaccounts", "sa1")
	cosmosArn := lid("microsoft.documentdb", "databaseaccounts", "cdb1")
	fsArn := lid("microsoft.storage", "storageaccounts", "sa1") + "/fileServices/default"
	subnetRID := lid("microsoft.network", "virtualnetworks", "vnet1") + "/subnets/default"
	subnetNode := arnNode("subnet1", core.NodeTypeSubnet, "")
	delete(subnetNode.Properties, "arn")
	subnetNode.Properties["resource_id"] = subnetRID

	nodes := []*core.DbNode{
		arnNode("sa1", core.NodeTypeStorage, saArn),
		arnNode("cdb1", core.NodeTypeDatabase, cosmosArn),
		arnNodeWithService("fs1", core.NodeTypeStorage, fsArn, "microsoft.storage/storageaccounts/fileservices"),
		subnetNode,
	}
	lookup := newNodeLookup(nodes)

	subnetPID := pid("Microsoft.Network", "virtualNetworks", "vnet1") + "/subnets/default"
	resources := []CloudResourceRow{
		{ServiceName: "microsoft.storage/storageaccounts", ARN: saArn,
			Meta: []byte(`{"properties":{"networkAcls":{"virtualNetworkRules":[{"id":"` + subnetPID + `"}]}}}`)},
		{ServiceName: "microsoft.documentdb/databaseaccounts", ARN: cosmosArn,
			Meta: []byte(`{"properties":{"virtualNetworkRules":[{"id":"` + subnetPID + `"}]}}`)},
		{ServiceName: "microsoft.storage/storageaccounts/fileservices", ARN: fsArn,
			Meta: []byte(`{"properties":{}}`)},
	}

	edges := source.createReferenceEdges(resources, lookup, &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"})

	type ek struct {
		src, dst string
		rel      core.RelationshipType
	}
	got := make(map[ek]bool)
	for _, e := range edges {
		got[ek{e.SourceNodeID, e.DestinationNodeID, e.RelationshipType}] = true
	}
	want := []ek{
		{"sa1", "subnet1", core.RelationshipAssociatedWith},  // storage VNet service-endpoint rule
		{"cdb1", "subnet1", core.RelationshipAssociatedWith}, // cosmos VNet rule
		{"fs1", "sa1", core.RelationshipBelongsTo},           // fileservice → parent storage
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %s --%s--> %s", w.src, w.rel, w.dst)
		}
	}
	if len(edges) != len(want) {
		t.Errorf("got %d edges, want %d: %+v", len(edges), len(want), got)
	}
}

// TestCreateReferenceEdges_ApplicationGateway verifies an Application Gateway's
// frontend PublicIP is connected (AppGW shares the LB frontend shape).
func TestCreateReferenceEdges_ApplicationGateway(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	const sub = "00000000-0000-0000-0000-000000000005"
	agwArn := "/subscriptions/" + sub + "/resourcegroups/rg/providers/microsoft.network/applicationgateways/agw1"
	pipArn := "/subscriptions/" + sub + "/resourcegroups/rg/providers/microsoft.network/publicipaddresses/pip1"

	nodes := []*core.DbNode{
		arnNode("agw1", core.NodeTypeLoadBalancer, agwArn),
		arnNode("pip1", core.NodeTypePublicIP, pipArn),
	}
	lookup := newNodeLookup(nodes)

	resources := []CloudResourceRow{{
		ServiceName: "microsoft.network/applicationgateways",
		ARN:         agwArn,
		Meta: []byte(`{"properties":{"frontendIPConfigurations":[{"properties":{"publicIPAddress":{"id":"/subscriptions/` + sub +
			`/resourceGroups/RG/providers/Microsoft.Network/publicIPAddresses/pip1"}}}]}}`),
	}}

	edges := source.createReferenceEdges(resources, lookup, &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"})
	found := false
	for _, e := range edges {
		if e.SourceNodeID == "pip1" && e.DestinationNodeID == "agw1" && e.RelationshipType == core.RelationshipAssociatedWith {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pip1 --ASSOCIATED_WITH--> agw1 (AppGW frontend), got %d edges", len(edges))
	}
}
