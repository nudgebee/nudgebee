package sources

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/knowledge_graph/core"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// ============================================================================
// Azure Source Creation & Registration Tests
// ============================================================================

func TestNewAzureSource(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	source, err := NewAzureSource(AzureSourceConfig{}, logger)
	if err != nil {
		t.Fatalf("NewAzureSource() returned error: %v", err)
	}

	if source.GetName() != "azure" {
		t.Errorf("GetName() = %q, want %q", source.GetName(), "azure")
	}
	if !source.IsEnabled() {
		t.Error("IsEnabled() = false, want true")
	}
	if err := source.Validate(); err != nil {
		t.Errorf("Validate() returned error: %v", err)
	}
}

func TestNewAzureSource_NilLogger(t *testing.T) {
	source, err := NewAzureSource(AzureSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAzureSource() with nil logger returned error: %v", err)
	}
	if source.logger == nil {
		t.Error("Expected default logger to be set")
	}
}

func TestAzureSourceRegistered(t *testing.T) {
	if !IsSourceRegistered("azure") {
		t.Error("Azure source not found in global registry")
	}

	sources := ListRegisteredSources()
	found := false
	for _, s := range sources {
		if s == "azure" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Azure not in ListRegisteredSources(): %v", sources)
	}
}

// ============================================================================
// DetermineNodeType Tests
// ============================================================================

func TestAzureDetermineNodeType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	tests := []struct {
		name         string
		resourceType string
		serviceName  string
		want         core.NodeType
	}{
		{"VM", "VirtualMachine", "Microsoft.Compute", core.NodeTypeComputeInstance},
		{"AKS", "ManagedCluster", "Microsoft.ContainerService", core.NodeTypeManagedCluster},
		{"VNet", "VirtualNetwork", "Microsoft.Network", core.NodeTypeVPC},
		{"Subnet", "Subnet", "Microsoft.Network", core.NodeTypeSubnet},
		{"LB", "LoadBalancer", "Microsoft.Network", core.NodeTypeLoadBalancer},
		{"AppGW", "ApplicationGateway", "Microsoft.Network", core.NodeTypeLoadBalancer},
		{"NSG", "NetworkSecurityGroup", "Microsoft.Network", core.NodeTypeSecurityGroup},
		{"SQL DB", "SqlDatabase", "Microsoft.Sql", core.NodeTypeDatabase},
		{"SQL Server", "SqlServer", "Microsoft.Sql", core.NodeTypeDatabase},
		{"CosmosDB", "CosmosDBAccount", "Microsoft.DocumentDB", core.NodeTypeDatabase},
		{"Redis", "RedisCache", "Microsoft.Cache", core.NodeTypeCache},
		{"Storage", "StorageAccount", "Microsoft.Storage", core.NodeTypeStorage},
		{"Function", "FunctionApp", "Microsoft.Web", core.NodeTypeServerlessFunction},
		{"ServiceBus", "ServiceBusNamespace", "Microsoft.ServiceBus", core.NodeTypeMessageQueue},
		{"EventHub", "EventHubNamespace", "Microsoft.EventHub", core.NodeTypeMessageQueue},
		{"KeyVault", "Vault", "Microsoft.KeyVault", core.NodeTypeSecretVault},
		{"PublicIP", "PublicIPAddress", "Microsoft.Network", core.NodeTypePublicIP},
		{"NIC", "NetworkInterface", "Microsoft.Network", core.NodeTypeNetworkInterface},
		{"NAT GW", "NatGateway", "Microsoft.Network", core.NodeTypeNetworkGateway},
		{"Route Table", "RouteTable", "Microsoft.Network", core.NodeTypeRouteTable},
		{"ACR", "ContainerRegistry", "Microsoft.ContainerRegistry", core.NodeTypeContainerRegistry},
		{"DNS Zone", "DnsZone", "Microsoft.Network", core.NodeTypeDNSZone},
		{"Private DNS", "PrivateDnsZone", "Microsoft.Network", core.NodeTypeDNSZone},
		{"Private Endpoint", "PrivateEndpoint", "Microsoft.Network", core.NodeTypePrivateEndpoint},
		{"CDN", "CdnProfile", "Microsoft.Cdn", core.NodeTypeCDN},
		{"Front Door", "FrontDoor", "Microsoft.Network", core.NodeTypeCDN},
		// Case insensitivity
		{"VM lowercase", "virtualmachine", "Microsoft.Compute", core.NodeTypeComputeInstance},
		{"VM mixed case", "VirtualMACHINE", "Microsoft.Compute", core.NodeTypeComputeInstance},

		// Real DB format (plural lowercase type, full service path)
		{"DB: VMs", "virtualmachines", "microsoft.compute/virtualmachines", core.NodeTypeComputeInstance},
		{"DB: VMSS", "virtualmachinescalesets", "microsoft.compute/virtualmachinescalesets", core.NodeTypeComputeInstance},
		{"DB: AKS", "managedclusters", "microsoft.containerservice/managedclusters", core.NodeTypeManagedCluster},
		{"DB: VNets", "virtualnetworks", "microsoft.network/virtualnetworks", core.NodeTypeVPC},
		{"DB: LB", "loadbalancers", "microsoft.network/loadbalancers", core.NodeTypeLoadBalancer},
		{"DB: PublicIPs", "publicipaddresses", "microsoft.network/publicipaddresses", core.NodeTypePublicIP},
		{"DB: Storage", "storageaccounts", "microsoft.storage/storageaccounts", core.NodeTypeStorage},
		{"DB: SQL databases", "databases", "microsoft.sql/servers", core.NodeTypeDatabase},
		{"DB: SQL servers", "servers", "microsoft.sql/servers", core.NodeTypeDatabase},
		{"DB: Sites", "sites", "microsoft.web/sites", core.NodeTypeServerlessFunction},
		{"DB: ServiceBus NS", "namespaces", "microsoft.servicebus/namespaces", core.NodeTypeMessageQueue},
		{"DB: ACR", "registries", "microsoft.containerregistry/registries", core.NodeTypeContainerRegistry},
		{"DB: CDN", "profiles", "microsoft.cdn/profiles", core.NodeTypeCDN},
		{"DB: Disks", "disks", "microsoft.compute/disks", core.NodeTypeComputeInstance}, // fallback to service prefix

		// Service prefix fallback
		{"Unknown Compute type", "SomeNewType", "microsoft.compute/somethingnew", core.NodeTypeComputeInstance},
		{"Unknown Sql type", "SomeNewType", "microsoft.sql/somethingnew", core.NodeTypeDatabase},
		{"Legacy service format", "SomeNewType", "Microsoft.Compute", core.NodeTypeComputeInstance},
		// Full fallback
		{"Completely unknown", "UnknownType", "UnknownService", core.NodeTypeCloudResource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.determineNodeType(tt.resourceType, tt.serviceName)
			if got != tt.want {
				t.Errorf("determineNodeType(%q, %q) = %v, want %v", tt.resourceType, tt.serviceName, got, tt.want)
			}
		})
	}
}

// ============================================================================
// GenerateUniqueKey Tests
// ============================================================================

func TestAzureGenerateUniqueKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	tests := []struct {
		name    string
		node    *core.DbNode
		wantKey string
	}{
		{
			name: "VNet node (no hierarchy)",
			node: &core.DbNode{
				NodeType:       core.NodeTypeVPC,
				CloudAccountID: "acc-1",
				Properties: map[string]interface{}{
					"name":   "my-vnet",
					"region": "eastus",
				},
			},
			wantKey: "azure:acc-1:eastus:VPC::my-vnet",
		},
		{
			name: "VM with VNet hierarchy",
			node: &core.DbNode{
				NodeType:       core.NodeTypeComputeInstance,
				CloudAccountID: "acc-1",
				Properties: map[string]interface{}{
					"name":                "my-vm",
					"region":              "westus2",
					"vnet_name_hierarchy": "prod-vnet",
				},
			},
			wantKey: "azure:acc-1:westus2:ComputeInstance:prod-vnet:my-vm",
		},
		{
			name: "AKS with service hierarchy",
			node: &core.DbNode{
				NodeType:       core.NodeTypeManagedCluster,
				CloudAccountID: "acc-1",
				Properties: map[string]interface{}{
					"name":         "my-aks",
					"region":       "eastus",
					"service_name": "Microsoft.ContainerService",
				},
			},
			wantKey: "azure:acc-1:eastus:ManagedCluster:AKS:my-aks",
		},
		{
			name: "VM with vnet_id fallback",
			node: &core.DbNode{
				NodeType:       core.NodeTypeComputeInstance,
				CloudAccountID: "acc-1",
				Properties: map[string]interface{}{
					"name":    "my-vm",
					"region":  "eastus",
					"vnet_id": "vnet-123",
				},
			},
			wantKey: "azure:acc-1:eastus:ComputeInstance:vnet-123:my-vm",
		},
		{
			name:    "Nil node",
			node:    nil,
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := source.GenerateUniqueKey(tt.node)
			if got != tt.wantKey {
				t.Errorf("GenerateUniqueKey() = %q, want %q", got, tt.wantKey)
			}
		})
	}
}

func TestAzureGenerateUniqueKey_Deterministic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	node := &core.DbNode{
		NodeType:       core.NodeTypeComputeInstance,
		CloudAccountID: "acc-1",
		Properties: map[string]interface{}{
			"name":   "test-vm",
			"region": "eastus",
		},
	}

	key1 := source.GenerateUniqueKey(node)
	key2 := source.GenerateUniqueKey(node)
	if key1 != key2 {
		t.Errorf("GenerateUniqueKey not deterministic: %q != %q", key1, key2)
	}
}

// ============================================================================
// ShouldIncludeResource Tests
// ============================================================================

func TestAzureShouldIncludeResource(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("no filter includes all", func(t *testing.T) {
		source, _ := NewAzureSource(AzureSourceConfig{}, logger)
		r := &CloudResourceRow{ServiceName: "Microsoft.Compute", Type: "VirtualMachine"}
		if !source.shouldIncludeResource(r) {
			t.Error("Expected resource to be included with no filter")
		}
	})

	t.Run("filter allows matching type", func(t *testing.T) {
		source, _ := NewAzureSource(AzureSourceConfig{
			ServiceTypeFilter: map[string][]string{
				"Microsoft.Compute": {"VirtualMachine"},
			},
		}, logger)
		r := &CloudResourceRow{ServiceName: "Microsoft.Compute", Type: "VirtualMachine"}
		if !source.shouldIncludeResource(r) {
			t.Error("Expected matching resource to be included")
		}
	})

	t.Run("filter excludes non-matching type", func(t *testing.T) {
		source, _ := NewAzureSource(AzureSourceConfig{
			ServiceTypeFilter: map[string][]string{
				"Microsoft.Compute": {"VirtualMachine"},
			},
		}, logger)
		r := &CloudResourceRow{ServiceName: "Microsoft.Compute", Type: "Disk"}
		if source.shouldIncludeResource(r) {
			t.Error("Expected non-matching resource to be excluded")
		}
	})

	t.Run("unfiltered service includes all types", func(t *testing.T) {
		source, _ := NewAzureSource(AzureSourceConfig{
			ServiceTypeFilter: map[string][]string{
				"Microsoft.Compute": {"VirtualMachine"},
			},
		}, logger)
		r := &CloudResourceRow{ServiceName: "Microsoft.Network", Type: "VirtualNetwork"}
		if !source.shouldIncludeResource(r) {
			t.Error("Expected unfiltered service resource to be included")
		}
	})

	t.Run("case insensitive type matching", func(t *testing.T) {
		source, _ := NewAzureSource(AzureSourceConfig{
			ServiceTypeFilter: map[string][]string{
				"Microsoft.Compute": {"virtualmachine"},
			},
		}, logger)
		r := &CloudResourceRow{ServiceName: "Microsoft.Compute", Type: "VirtualMachine"}
		if !source.shouldIncludeResource(r) {
			t.Error("Expected case-insensitive match to be included")
		}
	})
}

// ============================================================================
// CreateNodeFromResource Tests
// ============================================================================

func TestAzureCreateNodeFromResource(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	meta, _ := json.Marshal(map[string]interface{}{
		"vmSize":           "Standard_D4s_v3",
		"osType":           "Linux",
		"privateIpAddress": "10.0.1.5",
		"vnetId":           "vnet-abc",
		"subnetId":         "subnet-xyz",
		"resourceGroup":    "my-rg",
	})
	tags, _ := json.Marshal(map[string]interface{}{
		"env":   "production",
		"owner": "devops",
	})

	resource := &CloudResourceRow{
		ID:            "uuid-1",
		ResourceID:    "vm-resource-id-123",
		Name:          "my-vm",
		Type:          "VirtualMachine",
		Status:        "Active",
		Account:       "acc-1",
		Tenant:        "tenant-1",
		CloudProvider: "Azure",
		Region:        "eastus",
		ARN:           "/subscriptions/sub-1/resourceGroups/my-rg/providers/Microsoft.Compute/virtualMachines/my-vm",
		Tags:          tags,
		Meta:          meta,
		ServiceName:   "Microsoft.Compute",
		IsActive:      true,
		AccountNumber: "sub-1",
	}

	node := source.createNodeFromResource(resource, req)

	// Verify basic properties
	if node.NodeType != core.NodeTypeComputeInstance {
		t.Errorf("NodeType = %v, want ComputeInstance", node.NodeType)
	}
	if node.Source != "azure" {
		t.Errorf("Source = %q, want %q", node.Source, "azure")
	}
	if node.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want %q", node.TenantID, "tenant-1")
	}
	if node.CloudAccountID != "acc-1" {
		t.Errorf("CloudAccountID = %q, want %q", node.CloudAccountID, "acc-1")
	}

	// Verify properties
	assertProp := func(key string, want interface{}) {
		t.Helper()
		got, ok := node.Properties[key]
		if !ok {
			t.Errorf("Property %q not found", key)
			return
		}
		if got != want {
			t.Errorf("Property %q = %v, want %v", key, got, want)
		}
	}

	assertProp("name", "my-vm")
	assertProp("type", "VirtualMachine")
	assertProp("cloud_provider", "Azure")
	assertProp("region", "eastus")
	assertProp("resource_id", "vm-resource-id-123")
	assertProp("service_name", "Microsoft.Compute")
	assertProp("is_active", true)
	assertProp("nb_resource_id", "uuid-1")
	assertProp("nb_account_id", "acc-1")
	assertProp("account_number", "sub-1")

	// Verify metadata extraction
	assertProp("vm_size", "Standard_D4s_v3")
	assertProp("os_type", "Linux")
	assertProp("private_ip", "10.0.1.5")
	assertProp("vnet_id", "vnet-abc")
	assertProp("subnet_id", "subnet-xyz")
	assertProp("resource_group", "my-rg")

	// Verify tags became labels
	labels, ok := node.Properties["labels"].(map[string]interface{})
	if !ok {
		t.Fatal("labels property not a map[string]interface{}")
		return
	}
	if labels["env"] != "production" {
		t.Errorf("label env = %v, want production", labels["env"])
	}

	// Verify unique key starts with azure:
	if node.UniqueKey == "" {
		t.Error("UniqueKey is empty")
	}
	if len(node.UniqueKey) < 6 || node.UniqueKey[:6] != "azure:" {
		t.Errorf("UniqueKey %q does not start with 'azure:'", node.UniqueKey)
	}

	// Verify node ID is non-empty
	if node.ID == "" {
		t.Error("Node ID is empty")
	}
}

// ============================================================================
// ConvertResourcesToGraph Tests
// ============================================================================

func TestAzureConvertResourcesToGraph_BasicNodes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	resources := []CloudResourceRow{
		{
			ID: "1", Name: "my-vnet", Type: "VirtualNetwork",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "2", Name: "my-vm", Type: "VirtualMachine",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Compute",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"vnetId": "vnet-res-1"}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "3", Name: "my-aks", Type: "ManagedCluster",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.ContainerService",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"kubernetesVersion": "1.28"}`), Tags: json.RawMessage(`{}`),
		},
	}

	nodes, _ := helper.ConvertResourcesToGraph(nil, resources, req)

	if len(nodes) != 3 {
		t.Fatalf("Expected 3 nodes, got %d", len(nodes))
	}

	// Count by type
	typeCounts := make(map[core.NodeType]int)
	for _, n := range nodes {
		typeCounts[n.NodeType]++
	}

	if typeCounts[core.NodeTypeVPC] != 1 {
		t.Errorf("Expected 1 VPC node, got %d", typeCounts[core.NodeTypeVPC])
	}
	if typeCounts[core.NodeTypeComputeInstance] != 1 {
		t.Errorf("Expected 1 ComputeInstance node, got %d", typeCounts[core.NodeTypeComputeInstance])
	}
	if typeCounts[core.NodeTypeManagedCluster] != 1 {
		t.Errorf("Expected 1 ManagedCluster node, got %d", typeCounts[core.NodeTypeManagedCluster])
	}

	// All nodes should have azure source
	for _, n := range nodes {
		if n.Source != "azure" {
			t.Errorf("Node %q Source = %q, want azure", n.Properties["name"], n.Source)
		}
	}
}

func TestAzureConvertResourcesToGraph_VMEdges(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	resources := []CloudResourceRow{
		{
			ID: "1", ResourceID: "vnet-res-1", Name: "my-vnet", Type: "VirtualNetwork",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "2", ResourceID: "subnet-res-1", Name: "my-subnet", Type: "Subnet",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"vnetId": "vnet-res-1"}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "3", ResourceID: "vm-res-1", Name: "my-vm", Type: "VirtualMachine",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Compute",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"vnetId": "vnet-res-1", "subnetId": "subnet-res-1", "vmSize": "Standard_D4s_v3"}`),
			Tags: json.RawMessage(`{}`),
		},
	}

	nodes, edges := helper.ConvertResourcesToGraph(nil, resources, req)

	if len(nodes) != 3 {
		t.Fatalf("Expected 3 nodes, got %d", len(nodes))
	}

	if len(edges) == 0 {
		t.Fatal("Expected at least 1 edge, got 0")
		return
	}

	// Verify VM → VNet and VM → Subnet edges exist
	edgeTypes := make(map[string]bool)
	for _, e := range edges {
		edgeTypes[string(e.RelationshipType)] = true
	}

	if !edgeTypes[string(core.RelationshipHostedOn)] {
		t.Error("Expected HOSTED_ON edge (VM → VNet or VM → Subnet)")
	}

	// Verify Subnet → VNet BELONGS_TO edge
	if !edgeTypes[string(core.RelationshipBelongsTo)] {
		t.Error("Expected BELONGS_TO edge (Subnet → VNet)")
	}

	// Verify all edges have azure source
	for _, e := range edges {
		if e.Source != "azure" {
			t.Errorf("Edge Source = %q, want azure", e.Source)
		}
	}
}

func TestAzureConvertResourcesToGraph_AKSEdges(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	resources := []CloudResourceRow{
		{
			ID: "1", ResourceID: "vnet-res-1", Name: "my-vnet", Type: "VirtualNetwork",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "2", ResourceID: "subnet-res-1", Name: "aks-subnet", Type: "Subnet",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"vnetId": "vnet-res-1"}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "3", ResourceID: "aks-res-1", Name: "my-aks", Type: "ManagedCluster",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.ContainerService",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"vnetId": "vnet-res-1", "subnetId": "subnet-res-1", "kubernetesVersion": "1.28", "fqdn": "my-aks-dns.hcp.eastus.azmk8s.io"}`),
			Tags: json.RawMessage(`{}`),
		},
	}

	nodes, edges := helper.ConvertResourcesToGraph(nil, resources, req)

	if len(nodes) != 3 {
		t.Fatalf("Expected 3 nodes, got %d", len(nodes))
	}

	// AKS should have HOSTED_ON edges to VNet and Subnet
	hostedOnCount := 0
	for _, e := range edges {
		if e.RelationshipType == core.RelationshipHostedOn {
			hostedOnCount++
		}
	}

	// At minimum: AKS→VNet, AKS→Subnet
	if hostedOnCount < 2 {
		t.Errorf("Expected at least 2 HOSTED_ON edges for AKS, got %d", hostedOnCount)
	}

	// Verify AKS node has kubernetes_version
	for _, n := range nodes {
		if n.NodeType == core.NodeTypeManagedCluster {
			if v, ok := n.Properties["kubernetes_version"].(string); !ok || v != "1.28" {
				t.Errorf("AKS node kubernetes_version = %v, want 1.28", n.Properties["kubernetes_version"])
			}
			if v, ok := n.Properties["dns_name"].(string); !ok || v != "my-aks-dns.hcp.eastus.azmk8s.io" {
				t.Errorf("AKS node dns_name = %v, want my-aks-dns.hcp.eastus.azmk8s.io", n.Properties["dns_name"])
			}
		}
	}
}

func TestAzureConvertResourcesToGraph_NSGProtectsVM(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	resources := []CloudResourceRow{
		{
			ID: "1", ResourceID: "nsg-res-1", Name: "my-nsg", Type: "NetworkSecurityGroup",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "2", ResourceID: "vm-res-1", Name: "my-vm", Type: "VirtualMachine",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Compute",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"nsgId": "nsg-res-1"}`), Tags: json.RawMessage(`{}`),
		},
	}

	_, edges := helper.ConvertResourcesToGraph(nil, resources, req)

	protectsFound := false
	for _, e := range edges {
		if e.RelationshipType == core.RelationshipProtects {
			protectsFound = true
		}
	}
	if !protectsFound {
		t.Error("Expected PROTECTS edge (NSG → VM)")
	}
}

// ============================================================================
// Metadata Extraction Tests
// ============================================================================

func TestAzureExtractVMMetadata(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	properties := make(map[string]interface{})
	metaMap := map[string]interface{}{
		"vmSize":           "Standard_D4s_v3",
		"osType":           "Linux",
		"privateIpAddress": "10.0.1.5",
		"publicIpAddress":  "52.1.2.3",
		"networkInterfaceIds": []interface{}{
			"nic-1", "nic-2",
		},
	}

	source.extractVMMetadata(properties, metaMap)

	if properties["vm_size"] != "Standard_D4s_v3" {
		t.Errorf("vm_size = %v, want Standard_D4s_v3", properties["vm_size"])
	}
	if properties["os_type"] != "Linux" {
		t.Errorf("os_type = %v, want Linux", properties["os_type"])
	}
	if properties["private_ip"] != "10.0.1.5" {
		t.Errorf("private_ip = %v, want 10.0.1.5", properties["private_ip"])
	}
	if properties["public_ip"] != "52.1.2.3" {
		t.Errorf("public_ip = %v, want 52.1.2.3", properties["public_ip"])
	}
	nicIDs, ok := properties["network_interface_ids"].([]interface{})
	if !ok || len(nicIDs) != 2 {
		t.Errorf("network_interface_ids = %v, want 2 NICs", properties["network_interface_ids"])
	}
}

func TestAzureExtractAKSMetadata(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	properties := make(map[string]interface{})
	metaMap := map[string]interface{}{
		"kubernetesVersion": "1.28.3",
		"fqdn":              "my-aks.hcp.eastus.azmk8s.io",
		"nodePoolCount":     float64(3),
	}

	source.extractAKSMetadata(properties, metaMap)

	if properties["kubernetes_version"] != "1.28.3" {
		t.Errorf("kubernetes_version = %v, want 1.28.3", properties["kubernetes_version"])
	}
	if properties["dns_name"] != "my-aks.hcp.eastus.azmk8s.io" {
		t.Errorf("dns_name = %v", properties["dns_name"])
	}
	if properties["node_pool_count"] != float64(3) {
		t.Errorf("node_pool_count = %v, want 3", properties["node_pool_count"])
	}
}

func TestAzureExtractDatabaseMetadata(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	properties := make(map[string]interface{})
	metaMap := map[string]interface{}{
		"fullyQualifiedDomainName": "mydb.database.windows.net",
		"port":                     float64(1433),
	}

	source.extractDatabaseMetadata(properties, metaMap)

	if properties["dns_name"] != "mydb.database.windows.net" {
		t.Errorf("dns_name = %v", properties["dns_name"])
	}
	if properties["port"] != float64(1433) {
		t.Errorf("port = %v, want 1433", properties["port"])
	}
}

func TestAzureExtractCacheMetadata(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	properties := make(map[string]interface{})
	metaMap := map[string]interface{}{
		"hostName": "myredis.redis.cache.windows.net",
		"sslPort":  float64(6380),
	}

	source.extractCacheMetadata(properties, metaMap)

	if properties["dns_name"] != "myredis.redis.cache.windows.net" {
		t.Errorf("dns_name = %v", properties["dns_name"])
	}
	if properties["port"] != float64(6380) {
		t.Errorf("port = %v, want 6380", properties["port"])
	}
	if properties["cache_type"] != "redis" {
		t.Errorf("cache_type = %v, want redis", properties["cache_type"])
	}
}

func TestAzureExtractNICMetadata(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	properties := make(map[string]interface{})
	metaMap := map[string]interface{}{
		"privateIpAddress": "10.0.1.10",
		"virtualMachineId": "vm-123",
	}

	source.extractNICMetadata(properties, metaMap)

	if properties["private_ip"] != "10.0.1.10" {
		t.Errorf("private_ip = %v", properties["private_ip"])
	}
	if properties["attached_vm_id"] != "vm-123" {
		t.Errorf("attached_vm_id = %v", properties["attached_vm_id"])
	}
}

func TestAzureExtractVNetMetadata(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	properties := make(map[string]interface{})
	properties["resource_id"] = "vnet-abc"
	metaMap := map[string]interface{}{
		"addressSpace": map[string]interface{}{
			"addressPrefixes": []interface{}{"10.0.0.0/16"},
		},
	}

	source.extractVNetMetadata(properties, metaMap)

	cidrs, ok := properties["cidr_blocks"].([]interface{})
	if !ok || len(cidrs) != 1 {
		t.Errorf("cidr_blocks = %v, want [10.0.0.0/16]", properties["cidr_blocks"])
	}
	if properties["vnet_id"] != "vnet-abc" {
		t.Errorf("vnet_id = %v, want vnet-abc", properties["vnet_id"])
	}
}

// ============================================================================
// VNet Name Propagation Tests
// ============================================================================

func TestAzurePropagateVNetNames(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)

	vnetNode := &core.DbNode{
		NodeType: core.NodeTypeVPC,
		Properties: map[string]interface{}{
			"name":        "prod-vnet",
			"vnet_id":     "vnet-res-1",
			"resource_id": "vnet-res-1",
		},
	}
	vmNode := &core.DbNode{
		NodeType: core.NodeTypeComputeInstance,
		Properties: map[string]interface{}{
			"name":    "my-vm",
			"vnet_id": "vnet-res-1",
		},
	}

	nodes := []*core.DbNode{vnetNode, vmNode}
	lookup := newNodeLookup(nodes)

	source.propagateVNetNamesToResources(nodes, lookup)

	if vmNode.Properties["vnet_name_hierarchy"] != "prod-vnet" {
		t.Errorf("VM vnet_name_hierarchy = %v, want prod-vnet", vmNode.Properties["vnet_name_hierarchy"])
	}
	// VNet node itself should NOT have vnet_name_hierarchy
	if _, ok := vnetNode.Properties["vnet_name_hierarchy"]; ok {
		t.Error("VNet node should not have vnet_name_hierarchy")
	}
}

// ============================================================================
// DefaultVNetEdges Tests
// ============================================================================

func TestAzureDefaultVNetEdges(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	// A Redis cache with a vnet_id should get a default VNet edge
	resources := []CloudResourceRow{
		{
			ID: "1", ResourceID: "vnet-res-1", Name: "my-vnet", Type: "VirtualNetwork",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "2", ResourceID: "redis-res-1", Name: "my-redis", Type: "RedisCache",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Cache",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{"vnetId": "vnet-res-1", "hostName": "my-redis.redis.cache.windows.net"}`),
			Tags: json.RawMessage(`{}`),
		},
	}

	_, edges := helper.ConvertResourcesToGraph(nil, resources, req)

	hostedOnFound := false
	for _, e := range edges {
		if e.RelationshipType == core.RelationshipHostedOn {
			hostedOnFound = true
		}
	}
	if !hostedOnFound {
		t.Error("Expected HOSTED_ON edge for Redis → VNet via default VNet edges")
	}
}

// ============================================================================
// ServiceTypeFilter in ConvertResourcesToGraph Tests
// ============================================================================

func TestAzureConvertResourcesToGraph_WithFilter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{
		ServiceTypeFilter: map[string][]string{
			"Microsoft.Compute": {"VirtualMachine"},
		},
	}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	resources := []CloudResourceRow{
		{
			ID: "1", Name: "my-vm", Type: "VirtualMachine",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Compute",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "2", Name: "my-disk", Type: "Disk",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Compute",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
		{
			ID: "3", Name: "my-vnet", Type: "VirtualNetwork",
			CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network",
			Tenant: "tenant-1", Account: "acc-1", IsActive: true,
			Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`),
		},
	}

	nodes, _ := helper.ConvertResourcesToGraph(nil, resources, req)

	// "Disk" should be filtered out (Microsoft.Compute filter only allows VirtualMachine)
	// "VirtualNetwork" should pass (Microsoft.Network is not filtered)
	if len(nodes) != 2 {
		t.Errorf("Expected 2 nodes (VM + VNet), got %d", len(nodes))
		for _, n := range nodes {
			t.Logf("  Node: type=%v name=%v", n.NodeType, n.Properties["name"])
		}
	}
}

// ============================================================================
// Empty Resources Test
// ============================================================================

func TestAzureConvertResourcesToGraph_Empty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	nodes, edges := helper.ConvertResourcesToGraph(nil, []CloudResourceRow{}, req)

	if len(nodes) != 0 {
		t.Errorf("Expected 0 nodes, got %d", len(nodes))
	}
	if len(edges) != 0 {
		t.Errorf("Expected 0 edges, got %d", len(edges))
	}
}

// ============================================================================
// Mixed Resource Types Test
// ============================================================================

func TestAzureConvertResourcesToGraph_AllResourceTypes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	source, _ := NewAzureSource(AzureSourceConfig{}, logger)
	helper := NewAzureSourceTestHelper(source)

	req := &core.SourceBuildRequest{
		TenantID:       "tenant-1",
		CloudAccountID: "acc-1",
	}

	resources := []CloudResourceRow{
		{ID: "1", Name: "vm1", Type: "VirtualMachine", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Compute", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "2", Name: "aks1", Type: "ManagedCluster", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.ContainerService", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "3", Name: "vnet1", Type: "VirtualNetwork", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "4", Name: "subnet1", Type: "Subnet", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "5", Name: "lb1", Type: "LoadBalancer", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "6", Name: "nsg1", Type: "NetworkSecurityGroup", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Network", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "7", Name: "sqldb1", Type: "SqlDatabase", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Sql", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "8", Name: "cosmos1", Type: "CosmosDBAccount", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.DocumentDB", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "9", Name: "redis1", Type: "RedisCache", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Cache", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "10", Name: "sa1", Type: "StorageAccount", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Storage", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "11", Name: "func1", Type: "FunctionApp", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.Web", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "12", Name: "sb1", Type: "ServiceBusNamespace", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.ServiceBus", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
		{ID: "13", Name: "kv1", Type: "Vault", CloudProvider: "Azure", Region: "eastus", ServiceName: "Microsoft.KeyVault", Tenant: "t", Account: "a", IsActive: true, Meta: json.RawMessage(`{}`), Tags: json.RawMessage(`{}`)},
	}

	nodes, _ := helper.ConvertResourcesToGraph(nil, resources, req)

	if len(nodes) != len(resources) {
		t.Errorf("Expected %d nodes, got %d", len(resources), len(nodes))
	}

	// Verify expected types are present
	expectedTypes := map[core.NodeType]bool{
		core.NodeTypeComputeInstance:    false,
		core.NodeTypeManagedCluster:     false,
		core.NodeTypeVPC:                false,
		core.NodeTypeSubnet:             false,
		core.NodeTypeLoadBalancer:       false,
		core.NodeTypeSecurityGroup:      false,
		core.NodeTypeDatabase:           false,
		core.NodeTypeCache:              false,
		core.NodeTypeStorage:            false,
		core.NodeTypeServerlessFunction: false,
		core.NodeTypeMessageQueue:       false,
		core.NodeTypeSecretVault:        false,
	}

	for _, n := range nodes {
		if _, expected := expectedTypes[n.NodeType]; expected {
			expectedTypes[n.NodeType] = true
		}
	}

	for nt, found := range expectedTypes {
		if !found {
			t.Errorf("Missing expected node type: %v", nt)
		}
	}
}

// ============================================================================
// DATABASE INTEGRATION TEST - Reads real data from cloud_resourses
// ============================================================================
//
// Run with:
//   go test ./knowledge_graph/sources/ -run TestAzureBuildGraph_FromDB -v
//
// Required: Database connection (set via env or .env file).
// This test is skipped automatically if the DB is unavailable.

func TestAzureBuildGraph_FromDB(t *testing.T) {
	// --- Config: tenant/account come from the environment ---
	tenantID, accountID, _ := testenv.RequireTenant(t)

	// --- Connect to DB (skips if the metastore is unreachable) ---
	dbManager := testenv.RequireMetastore(t)

	// --- Fetch Azure resources, excluding noisy policy/role types ---
	excludeTypes := []string{
		"policydefinitions", "policysetdefinitions", "policyassignments",
		"roleassignments", "assessments", "pricings",
		"metricalerts", "actiongroups", "scheduledqueryrules",
	}

	query := `
		SELECT
			cr.id, cr.resourse_id, cr.name, cr.type, cr.status, cr.account, cr.tenant,
			cr.cloud_provider, cr.region, cr.arn, cr.tags, cr.meta, cr.service_name,
			cr.is_active, cr.external_resource_id,
			COALESCE(ca.account_number, '') as account_number
		FROM cloud_resourses cr
		LEFT JOIN cloud_accounts ca ON cr.account = ca.id
		WHERE cr.tenant = $1
			AND cr.account = $2
			AND cr.cloud_provider = 'Azure'
			AND cr.type != ALL($3)
		ORDER BY cr.type, cr.name
	`

	var resources []CloudResourceRow
	err := dbManager.Db.Select(&resources, query, tenantID, accountID, pq.Array(excludeTypes))
	if err != nil {
		t.Fatalf("Failed to query cloud_resourses: %v", err)
	}

	t.Logf("=== Fetched %d Azure resources from DB ===", len(resources))
	if len(resources) == 0 {
		t.Skip("No Azure resources found in DB for this account")
	}

	// --- Print raw resource summary ---
	typeCount := make(map[string]int)
	for _, r := range resources {
		key := fmt.Sprintf("%s | %s", r.Type, r.ServiceName)
		typeCount[key]++
	}
	t.Log("\n--- Raw Resource Types ---")
	sortedTypes := make([]string, 0, len(typeCount))
	for k := range typeCount {
		sortedTypes = append(sortedTypes, k)
	}
	sort.Strings(sortedTypes)
	for _, k := range sortedTypes {
		t.Logf("  %-45s  %d", k, typeCount[k])
	}

	// --- Build the graph ---
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	source, err := NewAzureSource(AzureSourceConfig{}, logger)
	if err != nil {
		t.Fatalf("Failed to create AzureSource: %v", err)
	}

	helper := NewAzureSourceTestHelper(source)
	req := &core.SourceBuildRequest{
		TenantID:       tenantID,
		CloudAccountID: accountID,
	}

	nodes, edges := helper.ConvertResourcesToGraph(nil, resources, req)

	// === GRAPH SUMMARY ===
	t.Logf("\n=== AZURE KNOWLEDGE GRAPH ===")
	t.Logf("Total Nodes: %d", len(nodes))
	t.Logf("Total Edges: %d", len(edges))

	// --- Node type breakdown ---
	nodeTypeCount := make(map[core.NodeType]int)
	for _, n := range nodes {
		nodeTypeCount[n.NodeType]++
	}
	t.Log("\n--- Node Types ---")
	nodeTypes := make([]string, 0, len(nodeTypeCount))
	for nt := range nodeTypeCount {
		nodeTypes = append(nodeTypes, string(nt))
	}
	sort.Strings(nodeTypes)
	for _, nt := range nodeTypes {
		t.Logf("  %-30s  %d", nt, nodeTypeCount[core.NodeType(nt)])
	}

	// --- Edge type breakdown ---
	edgeTypeCount := make(map[core.RelationshipType]int)
	for _, e := range edges {
		edgeTypeCount[e.RelationshipType]++
	}
	t.Log("\n--- Edge Types ---")
	edgeTypes := make([]string, 0, len(edgeTypeCount))
	for et := range edgeTypeCount {
		edgeTypes = append(edgeTypes, string(et))
	}
	sort.Strings(edgeTypes)
	for _, et := range edgeTypes {
		t.Logf("  %-30s  %d", et, edgeTypeCount[core.RelationshipType(et)])
	}

	// --- Print ALL nodes ---
	t.Log("\n--- All Nodes ---")
	// Group by type for readability
	for _, nt := range nodeTypes {
		nodeType := core.NodeType(nt)
		t.Logf("\n  [%s] (%d nodes)", nt, nodeTypeCount[nodeType])
		for _, n := range nodes {
			if n.NodeType != nodeType {
				continue
			}
			name, _ := n.Properties["name"].(string)
			region, _ := n.Properties["region"].(string)
			resID, _ := n.Properties["resource_id"].(string)
			svcName, _ := n.Properties["service_name"].(string)

			// Collect interesting metadata
			extras := []string{}
			if vmSize, ok := n.Properties["vm_size"].(string); ok {
				extras = append(extras, "vm_size="+vmSize)
			}
			if osType, ok := n.Properties["os_type"].(string); ok {
				extras = append(extras, "os="+osType)
			}
			if dns, ok := n.Properties["dns_name"].(string); ok {
				extras = append(extras, "dns="+dns)
			}
			if k8sVer, ok := n.Properties["kubernetes_version"].(string); ok {
				extras = append(extras, "k8s="+k8sVer)
			}
			if vnetID, ok := n.Properties["vnet_id"].(string); ok {
				extras = append(extras, "vnet="+vnetID)
			}
			if rg, ok := n.Properties["resource_group"].(string); ok {
				extras = append(extras, "rg="+rg)
			}

			extraStr := ""
			if len(extras) > 0 {
				extraStr = " {" + strings.Join(extras, ", ") + "}"
			}

			t.Logf("    %-30s  region=%-12s  svc=%-45s  res_id=%s%s",
				name, region, svcName, truncate(resID, 60), extraStr)
		}
	}

	// --- Print ALL edges ---
	t.Log("\n--- All Edges ---")
	// Build node ID → name map for readable output
	nodeNameByID := make(map[string]string)
	nodeTypeByID := make(map[string]core.NodeType)
	for _, n := range nodes {
		name, _ := n.Properties["name"].(string)
		nodeNameByID[n.ID] = name
		nodeTypeByID[n.ID] = n.NodeType
	}

	for _, et := range edgeTypes {
		edgeType := core.RelationshipType(et)
		t.Logf("\n  [%s] (%d edges)", et, edgeTypeCount[edgeType])
		for _, e := range edges {
			if e.RelationshipType != edgeType {
				continue
			}
			srcName := nodeNameByID[e.SourceNodeID]
			srcType := nodeTypeByID[e.SourceNodeID]
			dstName := nodeNameByID[e.DestinationNodeID]
			dstType := nodeTypeByID[e.DestinationNodeID]

			connType := ""
			if ct, ok := e.Properties["connection_type"].(string); ok {
				connType = " (" + ct + ")"
			}

			t.Logf("    [%s] %s  --%s-->  [%s] %s%s",
				srcType, srcName, e.RelationshipType, dstType, dstName, connType)
		}
	}

	// --- Basic assertions ---
	if len(nodes) == 0 {
		t.Error("Expected at least 1 node from Azure resources")
	}

	// Verify all nodes have azure source
	for _, n := range nodes {
		if n.Source != "azure" {
			t.Errorf("Node %q has source %q, want azure", n.Properties["name"], n.Source)
		}
		if n.UniqueKey == "" {
			t.Errorf("Node %q has empty UniqueKey", n.Properties["name"])
		}
		if !strings.HasPrefix(n.UniqueKey, "azure:") {
			t.Errorf("Node %q UniqueKey %q does not start with azure:", n.Properties["name"], n.UniqueKey)
		}
	}

	// Verify all edges have azure source
	for _, e := range edges {
		if e.Source != "azure" {
			t.Errorf("Edge %s has source %q, want azure", e.RelationshipType, e.Source)
		}
	}

	t.Logf("\n=== Azure Graph Build Complete: %d nodes, %d edges ===", len(nodes), len(edges))
}

// truncate shortens a string for display
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
