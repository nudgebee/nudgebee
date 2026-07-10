package core

// Azure ontology field mappings — how each concrete Azure specific_type populates
// the cross-cloud-normalized ontology_attributes for its NodeType. Mirrors
// ontology_data_aws.go (the aws entries).
//
// Keys are the Azure specific_type labels emitted by
// sources/azure/specific_type.go (determineSpecificType). NodeField names are the
// property keys present at NewNode time — i.e. the base keys set in
// sources/azure/classify.go (name, region, type/subtype, status, arn,
// resource_id, service_name, resource_group) plus the meta-derived keys the
// per-type extract* funcs write BEFORE core.NewNode runs. Fields Azure does not
// expose are omitted (BuildOntologyAttributes drops absent/empty values), so
// several labels below are intentionally minimal.
//
// Note on `location`: ComputeInstance's canonical vocab uses `region`; every
// other NodeType uses `location`. Azure keeps its resource location in the base
// `region` property, so non-compute types map location <- region (as AWS RDS does).

func init() { registerOntology(azureOntology) }

var azureOntology = map[string]OntologyNodeSpec{
	// ---- Compute ----
	"AzureVirtualMachine": {NodeType: NodeTypeComputeInstance, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "region", NodeField: "region"},
		{OntologyField: "type", NodeField: "vm_size"},             // extractVMMetadata
		{OntologyField: "state", NodeField: "provisioning_state"}, // extractVMMetadata
		{OntologyField: "public_ip_address", NodeField: "public_ip"},
		{OntologyField: "private_ip_address", NodeField: "private_ip"},
		// created_at: not extracted by the Azure source — omitted.
	}},

	// ---- Databases (both roll up to NodeTypeDatabase) ----
	"AzureSQLDatabase": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},      // databases/servers/flexibleservers
		{OntologyField: "endpoint", NodeField: "dns_name"}, // extractDatabaseMetadata (fqdn/endpoint)
		{OntologyField: "port", NodeField: "port"},         // extractDatabaseMetadata
		// version, encrypted: not extracted — omitted.
	}},
	"AzureCosmosDB": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", Special: "static_value", Extra: map[string]any{"value": "cosmosdb"}},
		{OntologyField: "endpoint", NodeField: "dns_name"}, // extractDatabaseMetadata
		{OntologyField: "port", NodeField: "port"},
	}},

	// ---- Managed Kubernetes ----
	"AKSCluster": {NodeType: NodeTypeManagedCluster, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "version", NodeField: "kubernetes_version"}, // extractAKSMetadata
		{OntologyField: "endpoint", NodeField: "dns_name"},          // extractAKSMetadata (fqdn)
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- Storage (no extractStorageMetadata; minimal) ----
	"AzureStorageAccount": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
		// encrypted, versioning, public, size: not extracted — omitted.
	}},

	// ---- Cache ----
	"AzureCacheForRedis": {NodeType: NodeTypeCache, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "cache_type"},   // extractCacheMetadata ("redis")
		{OntologyField: "endpoint", NodeField: "dns_name"}, // extractCacheMetadata (hostName)
		{OntologyField: "port", NodeField: "port"},         // extractCacheMetadata (sslPort)
		// version: not extracted — omitted.
	}},

	// ---- Serverless (no extractFunctionAppMetadata; runtime/memory unavailable) ----
	"AzureFunctionApp": {NodeType: NodeTypeServerlessFunction, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		// runtime, memory: not extracted — omitted.
	}},

	// ---- Load balancing ----
	"AzureLoadBalancer": {NodeType: NodeTypeLoadBalancer, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},      // loadbalancers/applicationgateways
		{OntologyField: "dns_name", NodeField: "dns_name"}, // extractLoadBalancerMetadata (frontend IP)
		{OntologyField: "state", NodeField: "status"},
		// scheme: no dedicated string key (only is_public_entry bool) — omitted.
	}},
	// AzureBackendPool: no BackendPool node is emitted by the Azure source and no
	// protocol/port/target_type keys exist — minimal spec.
	"AzureBackendPool": {NodeType: NodeTypeBackendPool, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- Networking ----
	"AzureVirtualNetwork": {NodeType: NodeTypeVPC, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "cidr", NodeField: "cidr_blocks"}, // extractVNetMetadata (addressPrefixes[])
		{OntologyField: "state", NodeField: "status"},
	}},
	"AzureSubnet": {NodeType: NodeTypeSubnet, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "cidr", NodeField: "cidr_block"}, // extractSubnetMetadata / ensureSubnetNodes
		{OntologyField: "vpc", NodeField: "vnet_id"},
	}},
	"AzureNetworkSecurityGroup": {NodeType: NodeTypeSecurityGroup, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "vpc", NodeField: "vnet_id"}, // via extractCommonNetworkIDs when present
	}},
	"AzureNetworkInterface": {NodeType: NodeTypeNetworkInterface, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "private_ip_address", NodeField: "private_ip"}, // extractNICMetadata
		{OntologyField: "subnet", NodeField: "subnet_id"},
		{OntologyField: "vpc", NodeField: "vnet_id"},
		// public_ip_address: only a public_ip_id (resource ID, not an address) exists — omitted.
	}},
	"AzureRouteTable": {NodeType: NodeTypeRouteTable, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "vpc", NodeField: "vnet_id"}, // via extractCommonNetworkIDs when present
	}},
	"AzureNATGateway": {NodeType: NodeTypeNetworkGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "vpc", NodeField: "vnet_id"},
	}},
	"AzurePrivateEndpoint": {NodeType: NodeTypePrivateEndpoint, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "vpc", NodeField: "vnet_id"}, // via extractCommonNetworkIDs when present
	}},
	// AzurePublicIP: the source captures no IP-address value on the public-IP node
	// itself — minimal spec.
	"AzurePublicIP": {NodeType: NodeTypePublicIP, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		// address: not extracted — omitted.
	}},

	// ---- Edge / DNS ----
	"AzureCDNProfile": {NodeType: NodeTypeCDN, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
		{OntologyField: "domain_name", NodeField: "frontend_hostnames"}, // extractCDNMetadata
	}},
	"AzureDNSZone": {NodeType: NodeTypeDNSZone, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "record_count", NodeField: "record_set_count"}, // extractDNSZoneMetadata
	}},

	// ---- Container registry (no loginServer/uri extracted; minimal) ----
	"AzureContainerRegistry": {NodeType: NodeTypeContainerRegistry, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		// uri: not extracted — omitted.
	}},

	// ---- Security / keys ----
	"AzureKeyVault": {NodeType: NodeTypeSecretVault, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
	}},
	// AzureKeyVaultKey: no EncryptionKey node is emitted by the Azure source (no
	// type→NodeTypeEncryptionKey mapping); usage unavailable — near-minimal.
	"AzureKeyVaultKey": {NodeType: NodeTypeEncryptionKey, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- API management (no node emitted / no protocol/endpoint keys; minimal) ----
	"AzureAPIManagement": {NodeType: NodeTypeAPIGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- Observability (both service_names are filtered out before ingest; minimal) ----
	"AzureMonitor": {NodeType: NodeTypeMonitoringService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"},
	}},
	"AzureLogAnalytics": {NodeType: NodeTypeLogAggregator, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		// retention_days: not extracted — omitted.
	}},

	// ---- Identity (Azure resource ID lives in the base `arn` property) ----
	"AzureManagedIdentity": {NodeType: NodeTypeServiceIdentity, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"}, // userassignedidentities
		{OntologyField: "arn", NodeField: "arn"},
	}},
}
