package gcp

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// Concrete per-specific_type property schemas for the GCP specific_types that
// are produced entirely by the shared createNodeFromResource path below — they
// have no per-type extractor and carry only universal base keys (name/region/
// subtype/status/labels/…) plus the raw `meta` blob. They are declared here,
// co-located with the builder that writes their properties, so every concrete
// label the source can emit (specific_type.go) has a registered schema even when
// it exposes no resource-specific, filterable field. GCSBucket additionally
// carries a synthesized public dns_name (declared, not Indexed — it is already
// part of every GCS node in existing goldens).
var (
	gcpDatabaseSchema       = core.SpecificTypeSchema{SpecificType: "GCPDatabase", NodeType: core.NodeTypeDatabase}
	gcpStorageSchema        = core.SpecificTypeSchema{SpecificType: "GCPStorage", NodeType: core.NodeTypeStorage}
	filestoreInstanceSchema = core.SpecificTypeSchema{SpecificType: "FilestoreInstance", NodeType: core.NodeTypeStorage}
	gcsBucketSchema         = core.SpecificTypeSchema{SpecificType: "GCSBucket", NodeType: core.NodeTypeStorage, Properties: []core.PropertyDef{
		{Name: "dns_name"},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "bucket_id"},
		{Name: "project_number"},
		{Name: "self_link"},
		{Name: "kind"},
		{Name: "location_type"},
		{Name: "meta_generation"},
		{Name: "storage_class"},
		{Name: "time_created"},
		{Name: "retention_period"},
		{Name: "iam_config_bucket_policy_only"},
		{Name: "iam_config_public_access_prevention"},
		{Name: "owner_entity"},
		{Name: "owner_entity_id"},
		{Name: "versioning_enabled"},
		{Name: "log_bucket"},
		{Name: "requester_pays"},
		{Name: "default_kms_key_name"},
		{Name: "acl_public"},
	}}
	artifactRegistrySchema = core.SpecificTypeSchema{SpecificType: "ArtifactRegistry", NodeType: core.NodeTypeContainerRegistry, Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "format"},
		{Name: "mode"},
		{Name: "description"},
		{Name: "registry_uri"},
		{Name: "size_bytes"},
		{Name: "kms_key_name"},
		{Name: "create_time"},
		{Name: "update_time"},
		{Name: "cleanup_policy_dry_run"},
		{Name: "vulnerability_scanning_enabled"},
		{Name: "project_id"},
	}}
	cloudNATGatewaySchema     = core.SpecificTypeSchema{SpecificType: "CloudNATGateway", NodeType: core.NodeTypeNetworkGateway}
	gcpExternalIPSchema       = core.SpecificTypeSchema{SpecificType: "GCPExternalIP", NodeType: core.NodeTypePublicIP}
	cloudLoggingSchema        = core.SpecificTypeSchema{SpecificType: "CloudLogging", NodeType: core.NodeTypeLogAggregator}
	cloudMonitoringSchema     = core.SpecificTypeSchema{SpecificType: "CloudMonitoring", NodeType: core.NodeTypeMonitoringService}
	vertexAISchema            = core.SpecificTypeSchema{SpecificType: "VertexAI", NodeType: core.NodeTypeAIService}
	secretManagerSecretSchema = core.SpecificTypeSchema{SpecificType: "SecretManagerSecret", NodeType: core.NodeTypeSecretVault, Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "project_id"},
		{Name: "rotation_enabled"},
		{Name: "rotation_period"},
		{Name: "rotation_next_time"},
		{Name: "created_date"},
		{Name: "expire_time"},
		{Name: "replication_type"},
		{Name: "etag"},
		{Name: "topics"},
		{Name: "version_aliases"},
	}}
	cloudKMSKeySchema = core.SpecificTypeSchema{SpecificType: "CloudKMSKey", NodeType: core.NodeTypeEncryptionKey, Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "rotation_period"},
		{Name: "purpose"},
		{Name: "key_ring_id"},
	}}
)

func init() {
	for _, s := range []core.SpecificTypeSchema{
		gcpDatabaseSchema, gcpStorageSchema, filestoreInstanceSchema, gcsBucketSchema,
		artifactRegistrySchema, cloudNATGatewaySchema, gcpExternalIPSchema, cloudLoggingSchema,
		cloudMonitoringSchema, vertexAISchema, secretManagerSecretSchema, cloudKMSKeySchema,
	} {
		core.RegisterSpecificTypeSchema(s)
	}
}

// createNodeFromResource creates a knowledge graph node from a GCP resource row.
// Returns nil when the row should be suppressed (e.g. NIC / vm-manager noise
// via shouldSuppressGCPResource, or rows redirected to a typed node such as
// IAM ServiceAccount → ServiceIdentity below); callers must skip nil.
func (s *GCPSource) createNodeFromResource(resource *sources.CloudResourceRow, req *core.SourceBuildRequest) *core.DbNode {
	if shouldSuppressGCPResource(resource) {
		return nil
	}

	source := "gcp"
	nodeType := s.determineNodeType(resource.Type, resource.ServiceName)

	// GCP IAM ServiceAccounts are first-class identities, not generic
	// CloudResource catch-all rows. They're the GCP analog of AWS IAM
	// Roles/Users (see createServiceIdentityFromIAMUser, aws_source.go:4981)
	// and the target of the Workload Identity chain wired by the
	// k8s_serviceaccount_to_gcp_iam_sa_wi rule. Issue #31101 gap #2.
	if resource.ServiceName == "IAM" && strings.EqualFold(resource.Type, "iam.googleapis.com/serviceaccount") {
		return s.createServiceIdentityFromGCPSA(resource, req)
	}

	properties := make(map[string]interface{})
	properties["name"] = resource.Name
	properties["type"] = resource.Type
	properties["status"] = resource.Status
	properties["cloud_provider"] = resource.CloudProvider
	properties["region"] = resource.Region
	properties["labels"] = resource.Tags
	properties["arn"] = resource.ARN
	properties["resource_id"] = resource.ResourceID
	properties["service_name"] = resource.ServiceName
	properties["is_active"] = resource.IsActive
	properties["external_resource_id"] = resource.ExternalResourceID

	// Store identifiers
	properties["nb_resource_id"] = resource.ID
	properties["nb_account_id"] = resource.Account
	properties["account_number"] = resource.AccountNumber

	// GCP project ID == the cloud account's project, carried as account_number.
	// Prefer it over parsing resource.Name: most GCP resources have a bare Name
	// (e.g. "default", "backendgaeservice"), so extractGCPProjectID(Name) wrongly
	// yields the resource's own name as the project — corrupting gcp_project_id on
	// nearly every node and any DNS synthesized from it (synthesizeGCPEndpointDNS).
	projectID := resource.AccountNumber
	if projectID == "" {
		projectID = extractGCPProjectID(resource.Name)
	}
	if projectID != "" {
		properties["gcp_project_id"] = projectID
	}

	// Add subtype for GCP resources
	switch nodeType {
	case core.NodeTypeDatabase:
		switch resource.ServiceName {
		case "Cloud SQL":
			properties["subtype"] = "CloudSQL"
		case "BigQuery", "bigquery.googleapis.com":
			properties["subtype"] = "BigQuery"
		default:
			properties["subtype"] = "Database"
		}
	case core.NodeTypeManagedCluster:
		properties["subtype"] = "GKE"
	case core.NodeTypeComputeInstance:
		properties["subtype"] = "ComputeEngine"
	case core.NodeTypeStorage:
		switch resource.ServiceName {
		case "Cloud Storage":
			properties["subtype"] = "CloudStorage"
		case "Cloud Filestore":
			properties["subtype"] = "Filestore"
		case "compute.googleapis.com/Disk":
			properties["subtype"] = "PersistentDisk"
		default:
			properties["subtype"] = "Storage"
		}
	case core.NodeTypeAIService:
		properties["subtype"] = resource.ServiceName
	case core.NodeTypeLogAggregator:
		properties["subtype"] = "CloudLogging"
	case core.NodeTypeMonitoringService:
		properties["subtype"] = "CloudMonitoring"
	case core.NodeTypeServerlessFunction:
		properties["subtype"] = "CloudRun"
	case core.NodeTypeContainerRegistry:
		properties["subtype"] = "ArtifactRegistry"
	case core.NodeTypeTopic:
		properties["subtype"] = "PubSubTopic"
	case core.NodeTypeSecurityGroup:
		if resource.Type == "firewall-rule" {
			properties["subtype"] = "FirewallRule"
		}
	default:
		if _, exists := properties["subtype"]; !exists {
			properties["subtype"] = resource.Type
		}
	}

	// Declare the concrete cloud label (specific_type). NewNode lifts this out of
	// properties into the dedicated column.
	properties["specific_type"] = s.determineSpecificType(nodeType, resource.ServiceName, resource.Type)

	// Parse metadata if available and extract essential fields
	if len(resource.Meta) > 0 && string(resource.Meta) != "{}" {
		var metaMap map[string]interface{}
		if err := json.Unmarshal(resource.Meta, &metaMap); err == nil && len(metaMap) > 0 {
			properties["meta"] = metaMap
			s.extractGCPMetadataByNodeType(properties, metaMap, nodeType, resource.ServiceName)
		}
	}

	// For Cloud Run: extract registry_name from container_image in meta
	if nodeType == core.NodeTypeServerlessFunction {
		if metaMap, ok := properties["meta"].(map[string]interface{}); ok {
			if containerImage, ok := metaMap["container_image"].(string); ok && containerImage != "" {
				if registryName := extractArtifactRegistryName(containerImage); registryName != "" {
					properties["registry_name"] = registryName
				}
			}
		}
	}

	// Synthesize public DNS for GCP resources whose metadata doesn't expose
	// one (Cloud Storage today). Runs unconditionally — even for rows with
	// empty `meta` — since the synthesizer reads only `name` + `service_name`
	// + `region`, all set above. No-op when dns_name is already populated by
	// Cloud SQL connectionName / GKE endpoint / Cloud Run url extractors.
	synthesizeGCPEndpointDNS(properties)

	// Parse and add tags (normalize array values to strings for GCP Asset Inventory format)
	if len(resource.Tags) > 0 && string(resource.Tags) != "{}" {
		var tagsMap map[string]interface{}
		if err := json.Unmarshal(resource.Tags, &tagsMap); err == nil {
			normalizedLabels := normalizeGCPLabels(tagsMap)
			properties["labels"] = normalizedLabels

			// Extract compute-specific properties from labels (for resources without metadata)
			if nodeType == core.NodeTypeComputeInstance {
				extractComputePropertiesFromLabels(properties, normalizedLabels)
			}
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

// extractGCPMetadataByNodeType extracts essential metadata fields from GCP resource meta
func (s *GCPSource) extractGCPMetadataByNodeType(properties map[string]interface{}, metaMap map[string]interface{}, nodeType core.NodeType, serviceName string) {
	switch nodeType {
	case core.NodeTypeComputeInstance:
		s.extractGCPComputeMetadata(properties, metaMap)
	case core.NodeTypeDatabase:
		if serviceName == "Cloud SQL" {
			s.extractGCPCloudSQLMetadata(properties, metaMap)
		}
	case core.NodeTypeManagedCluster:
		s.extractGCPGKEMetadata(properties, metaMap)
	case core.NodeTypeLoadBalancer:
		s.extractGCPForwardingRuleMetadata(properties, metaMap)
	case core.NodeTypeBackendPool:
		s.extractGCPTargetPoolMetadata(properties, metaMap)
	case core.NodeTypeServerlessFunction:
		// Cloud Run carries a per-service URL in `meta.url`; copy its host
		// to dns_name so DirectEndpointMatch hits when eBPF observes it.
		extractGCPCloudRunURL(properties, metaMap)
	case core.NodeTypeStorage:
		if serviceName == "compute.googleapis.com/Disk" {
			s.extractGCPPersistentDiskMetadata(properties, metaMap)
		}
	case core.NodeTypeSubnet:
		s.extractGCPSubnetMetadata(properties, metaMap)
	}
}

// shouldSuppressGCPResource returns true for cloud_resourses rows that should
// NEVER produce a KG node. Two categories today (issue #31101 gaps #8/#9):
//
//  1. NetworkInterface rows: GCP names every primary NIC `nic0` and our
//     unique-key derivation has no per-instance discriminator, so 16 distinct
//     NICs collapse to 1 KG node every build. The data they carry
//     (internal_ip, external_ip, subnet, network) is already on the parent
//     ComputeInstance node — mirroring AWS, which doesn't emit ENIs as
//     first-class KG nodes either.
//
//  2. `vm-manager` billing stubs: GCP billing export emits a single placeholder
//     row per project for the VM Manager service. It's not a listable resource
//     (the lister returns ErrUnsupported); the only `cloud_resourses` row for
//     it has `meta.nb_source="billing"` and a project-ID name. Zero query
//     value as a CloudResource node.
func shouldSuppressGCPResource(r *sources.CloudResourceRow) bool {
	if r == nil {
		// Future refactors / tests could call this with a nil pointer; the
		// current caller always passes non-nil but the guard is cheap.
		// Suppressing nil is the safer default.
		return true
	}
	if strings.EqualFold(r.ServiceName, "compute.googleapis.com/NetworkInterface") {
		return true
	}
	if strings.EqualFold(r.Type, "vm-manager") {
		return true
	}
	// IAM policy binding rows feed HAS_ACCESS_TO / PUBLISHES_TO / SUBSCRIBES_TO
	// edges (createHasAccessEdges); they are relationship data, not graph nodes.
	if strings.EqualFold(r.ServiceName, gcpIAMPolicyServiceName) || strings.EqualFold(r.Type, gcpIAMBindingType) {
		return true
	}
	// BigQuery dataset/table/view rows are high-cardinality data-tier leaf nodes —
	// the collector emits one row per table and a single project can hold tens of
	// thousands, swamping the graph with nodes that carry no topology value — so
	// these three resource types are excluded from the KG. Only the dataset/table/view
	// leaves are dropped; the BigQuery service is otherwise untouched (MaterializedView
	// and ExternalTable are already excluded upstream by GCPDefaultServiceTypeFilter).
	// This gate is the single on/off switch — the BigQuery type mappings, hierarchy
	// edge builder (createBigQueryEdges) and IAM dataset target are deliberately left
	// in place (inert while this gate drops the rows) so re-enabling is a one-line
	// removal here.
	switch strings.ToLower(r.Type) {
	case "bigquery.googleapis.com/dataset",
		"bigquery.googleapis.com/table",
		"bigquery.googleapis.com/view":
		return true
	}
	// GCP billing-export rollup rows (meta.nb_source="billing") are cost-reporting
	// aggregates — one per project per billed service, always named after the project
	// — not real resources. They carry no topology data and would otherwise become
	// orphaned nodes named after the project: phantom ComputeInstance (compute-engine),
	// and generic CloudResource / LogAggregator / MonitoringService for Cloud Tasks,
	// Secret Manager, KMS, Maps/Time-Zone APIs, Cloud Logging/Monitoring/Trace, etc.
	// Real resources always arrive via nb_source "api"/"asset" with their own names
	// and metadata, so they are unaffected. (Subsumes the vm-manager billing stub.)
	if gcpResourceMetaSource(r) == "billing" {
		return true
	}
	return false
}

// gcpResourceMetaSource returns the meta.nb_source marker ("billing", "api",
// "asset", …) for a cloud_resourses row, or "" when absent/unparseable.
func gcpResourceMetaSource(r *sources.CloudResourceRow) string {
	if r == nil || len(r.Meta) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(r.Meta, &m); err != nil {
		return ""
	}
	source, _ := m["nb_source"].(string)
	return source
}

// determineNodeType determines the knowledge graph node type from GCP resource type and service name
func (s *GCPSource) determineNodeType(resourceType, serviceName string) core.NodeType {
	resourceTypeLower := strings.ToLower(resourceType)

	// First, try exact match with type + service_name combination
	if serviceMap, exists := gcpResourceTypeMap[resourceTypeLower]; exists {
		if nodeType, found := serviceMap[serviceName]; found {
			return nodeType
		}
	}

	// Second, try service name fallback
	if nodeType, exists := gcpServiceFallbackMap[serviceName]; exists {
		return nodeType
	}

	// Default fallback for unmapped resources
	return core.NodeTypeCloudResource
}
