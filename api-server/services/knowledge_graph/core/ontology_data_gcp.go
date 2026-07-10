package core

// GCP ontology field mappings — how each concrete GCP specific_type populates the
// cross-cloud-normalized ontology_attributes for its NodeType. The GCP analog of
// ontology_data_aws.go.
//
// NodeField values are the REAL keys written by the gcp source
// (sources/gcp/*.go): base props set in classify.go createNodeFromResource
// (name/type/status/region/arn/subtype/gcp_project_id/dns_name...) plus the
// per-type extractors (extractGCP{Compute,CloudSQL,GKE,PersistentDisk,Subnet}Metadata,
// enrich*FromCLI, ensureGCP{VPC,Subnet}Nodes, createServiceIdentityFromGCPSA).
// Every OntologyField is in canonicalOntologyVocab for its NodeType (enforced by
// TestOntologyFieldsAreCanonical). "location" normalizes to the source "region"
// key; static_value is used where GCP guarantees a value (BigQuery type,
// encryption-at-rest which is mandatory and non-disableable on GCP).

func init() { registerOntology(gcpOntology) }

var gcpOntology = map[string]OntologyNodeSpec{
	// ---- Compute -----------------------------------------------------------
	"GCEInstance": {NodeType: NodeTypeComputeInstance, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "region", NodeField: "region"},
		{OntologyField: "type", NodeField: "machine_type"},
		{OntologyField: "state", NodeField: "instance_state", Special: "coalesce", Extra: map[string]any{"fields": []string{"status"}}},
		{OntologyField: "public_ip_address", NodeField: "public_ip"},
		{OntologyField: "private_ip_address", NodeField: "private_ip"},
	}},

	// ---- Databases ---------------------------------------------------------
	"CloudSQLInstance": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "engine"},                                                // databaseVersion, e.g. POSTGRES_14
		{OntologyField: "endpoint", NodeField: "dns_name"},                                          // connectionName
		{OntologyField: "encrypted", Special: "static_value", Extra: map[string]any{"value": true}}, // GCP encrypts at rest, always
	}},
	"BigQueryDataset": {NodeType: NodeTypeDatabase, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", Special: "static_value", Extra: map[string]any{"value": "bigquery"}},
		{OntologyField: "encrypted", Special: "static_value", Extra: map[string]any{"value": true}},
	}},
	"GCPDatabase": {NodeType: NodeTypeDatabase, Fields: []OntologyField{ // Firestore/Spanner/etc. fallback (minimal)
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"}, // raw asset type
		{OntologyField: "encrypted", Special: "static_value", Extra: map[string]any{"value": true}},
	}},

	// ---- Cache -------------------------------------------------------------
	"MemorystoreInstance": {NodeType: NodeTypeCache, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"}, // e.g. redis.googleapis.com/Instance
	}},

	// ---- Managed cluster ---------------------------------------------------
	"GKECluster": {NodeType: NodeTypeManagedCluster, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "location", Special: "coalesce", Extra: map[string]any{"fields": []string{"region"}}},
		{OntologyField: "version", NodeField: "kubernetes_version"},
		{OntologyField: "endpoint", NodeField: "dns_name"},
		{OntologyField: "state", NodeField: "cluster_state", Special: "coalesce", Extra: map[string]any{"fields": []string{"status"}}},
	}},

	// ---- Storage -----------------------------------------------------------
	"GCSBucket": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"}, // "CloudStorage"
		{OntologyField: "encrypted", Special: "static_value", Extra: map[string]any{"value": true}},
	}},
	"FilestoreInstance": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"}, // "Filestore"
		{OntologyField: "encrypted", Special: "static_value", Extra: map[string]any{"value": true}},
	}},
	"PersistentDisk": {NodeType: NodeTypeStorage, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region", Special: "coalesce", Extra: map[string]any{"fields": []string{"zone"}}},
		{OntologyField: "type", NodeField: "disk_type"}, // pd-ssd / pd-standard
		{OntologyField: "size", NodeField: "size_gb"},
		{OntologyField: "encrypted", Special: "static_value", Extra: map[string]any{"value": true}},
	}},

	// ---- Serverless --------------------------------------------------------
	"CloudRunService": {NodeType: NodeTypeServerlessFunction, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},

	// ---- Messaging ---------------------------------------------------------
	"PubSubTopic": {NodeType: NodeTypeTopic, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"}, // "PubSubTopic"
	}},

	// ---- Container registry ------------------------------------------------
	"ArtifactRegistry": {NodeType: NodeTypeContainerRegistry, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- Load balancing ----------------------------------------------------
	"GCPLoadBalancer": {NodeType: NodeTypeLoadBalancer, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
		{OntologyField: "dns_name", NodeField: "dns_name"},
		{OntologyField: "state", NodeField: "status"},
	}},
	"GCPBackendService": {NodeType: NodeTypeBackendPool, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- Networking --------------------------------------------------------
	"GCPVpc": {NodeType: NodeTypeVPC, Fields: []OntologyField{ // minimal (GCP VPCs are global, no CIDR)
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},
	"GCPSubnet": {NodeType: NodeTypeSubnet, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "cidr", NodeField: "ip_cidr_range"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},
	"GCPFirewall": {NodeType: NodeTypeSecurityGroup, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "vpc", NodeField: "vpc_id"},
	}},
	"CloudDNSZone": {NodeType: NodeTypeDNSZone, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},
	"CloudNATGateway": {NodeType: NodeTypeNetworkGateway, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
		{OntologyField: "state", NodeField: "status"},
	}},
	"GCPExternalIP": {NodeType: NodeTypePublicIP, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- Observability -----------------------------------------------------
	"CloudLogging": {NodeType: NodeTypeLogAggregator, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},
	"CloudMonitoring": {NodeType: NodeTypeMonitoringService, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "subtype"}, // "CloudMonitoring"
	}},

	// ---- AI ----------------------------------------------------------------
	"VertexAI": {NodeType: NodeTypeAIService, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
	}},

	// ---- Security / identity ----------------------------------------------
	"SecretManagerSecret": {NodeType: NodeTypeSecretVault, Fields: []OntologyField{ // minimal
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "type", NodeField: "type"},
	}},
	"CloudKMSKey": {NodeType: NodeTypeEncryptionKey, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"},
		{OntologyField: "state", NodeField: "status"},
	}},
	"GCPServiceAccount": {NodeType: NodeTypeServiceIdentity, Fields: []OntologyField{
		{OntologyField: "name", NodeField: "name", Required: true},
		{OntologyField: "location", NodeField: "region"}, // "global"
		{OntologyField: "type", NodeField: "subtype"},    // "GCPServiceAccount"
		{OntologyField: "arn", NodeField: "arn"},         // projects/{p}/serviceAccounts/{email}
	}},
}
