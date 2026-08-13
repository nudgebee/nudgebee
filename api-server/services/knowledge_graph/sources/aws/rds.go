package aws

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"nudgebee/services/knowledge_graph/sources/model"
)

// databaseSchemaProps are the concrete properties written by extractDatabaseMetadata
// (+ extractCommonMetadataFields) for the AmazonRDS-family NodeTypeDatabase resources
// that share this extractor. Reused across the RDS/Aurora/Redshift/OpenSearch schemas.
var databaseSchemaProps = []core.PropertyDef{
	{Name: "engine", Indexed: true},
	{Name: "engine_version", Indexed: true},
	{Name: "endpoint_address", Indexed: true},
	{Name: "endpoint_port", Indexed: true},
	{Name: "encrypted", Indexed: true},
	{Name: "instance_type", Indexed: true},
	{Name: "multi_az", Indexed: true},
	{Name: "allocated_storage_gb", Indexed: true},
	{Name: "availability_zone", Indexed: true},
	{Name: "dns_name", Indexed: true},
	{Name: "vpc_id", Indexed: true},
	{Name: "subnet_ids"},
	{Name: "vpc_security_group_ids"},
	{Name: "kms_key_id"},
	{Name: "performance_insights_kms_key_id"},
}

// rdsInstanceTopologyProps are DB identifiers emitted by extractDatabaseMetadata and
// consumed by createRDSTopologyEdges to build Aurora cluster-membership + read-replica
// edges (db_instance_identifier / db_cluster_identifier / read_replica_source).
var rdsInstanceTopologyProps = []core.PropertyDef{
	{Name: "db_instance_identifier"},
	{Name: "db_cluster_identifier"},
	{Name: "read_replica_source"},
}

// rdsClusterTopologyProps — the Aurora cluster's own identifier (emitted), the match
// target for an instance's db_cluster_identifier.
var rdsClusterTopologyProps = []core.PropertyDef{
	{Name: "db_cluster_identifier"},
}

// rdsInstanceParityProps — Additional provider fields (not yet emitted by our extractor)
// for the RDS instance model.
var rdsInstanceParityProps = []core.PropertyDef{
	{Name: "db_instance_class"},
	{Name: "master_username"},
	{Name: "db_name"},
	{Name: "instance_create_time"},
	{Name: "publicly_accessible"},
	{Name: "storage_encrypted"},
	{Name: "dbi_resource_id"},
	{Name: "ca_certificate_identifier"},
	{Name: "enhanced_monitoring_resource_arn"},
	{Name: "monitoring_role_arn"},
	{Name: "performance_insights_enabled"},
	{Name: "deletion_protection"},
	{Name: "preferred_backup_window"},
	{Name: "latest_restorable_time"},
	{Name: "preferred_maintenance_window"},
	{Name: "backup_retention_period"},
	{Name: "endpoint_hosted_zone_id"},
	{Name: "iam_database_authentication_enabled"},
	{Name: "auto_minor_version_upgrade"},
}

// rdsClusterParityProps — Additional provider fields (not yet emitted by our extractor)
// for the Aurora cluster model.
var rdsClusterParityProps = []core.PropertyDef{
	{Name: "allocated_storage"},
	{Name: "availability_zones"},
	{Name: "backup_retention_period"},
	{Name: "character_set_name"},
	{Name: "database_name"},
	{Name: "db_cluster_parameter_group"},
	{Name: "earliest_restorable_time"},
	{Name: "endpoint"},
	{Name: "reader_endpoint"},
	{Name: "engine_mode"},
	{Name: "latest_restorable_time"},
	{Name: "port"},
	{Name: "master_username"},
	{Name: "preferred_backup_window"},
	{Name: "preferred_maintenance_window"},
	{Name: "hosted_zone_id"},
	{Name: "storage_encrypted"},
	{Name: "db_cluster_resource_id"},
	{Name: "clone_group_id"},
	{Name: "cluster_create_time"},
	{Name: "earliest_backtrack_time"},
	{Name: "backtrack_window"},
	{Name: "backtrack_consumed_change_records"},
	{Name: "capacity"},
	{Name: "scaling_configuration_info_min_capacity"},
	{Name: "scaling_configuration_info_max_capacity"},
	{Name: "scaling_configuration_info_auto_pause"},
	{Name: "deletion_protection"},
}

// redshiftClusterParityProps — Additional provider fields (not yet emitted by our extractor)
// for the Redshift cluster model.
var redshiftClusterParityProps = []core.PropertyDef{
	{Name: "cluster_create_time"},
	{Name: "cluster_identifier"},
	{Name: "cluster_revision_number"},
	{Name: "cluster_status"},
	{Name: "db_name"},
	{Name: "master_username"},
	{Name: "node_type"},
	{Name: "number_of_nodes"},
	{Name: "publicly_accessible"},
}

// rdsInstanceSchema / rdsClusterSchema — concrete schemas for RDS instances and
// Aurora clusters.
var rdsInstanceSchema = core.SpecificTypeSchema{
	SpecificType: "RDSInstance",
	NodeType:     core.NodeTypeDatabase,
	Properties:   concatProps(databaseSchemaProps, rdsInstanceTopologyProps, rdsInstanceParityProps),
}

var rdsClusterSchema = core.SpecificTypeSchema{
	SpecificType: "RDSCluster",
	NodeType:     core.NodeTypeDatabase,
	Properties:   concatProps(databaseSchemaProps, rdsClusterTopologyProps, rdsClusterParityProps),
}

// concatProps flattens property-def groups into one slice.
func concatProps(groups ...[]core.PropertyDef) []core.PropertyDef {
	out := []core.PropertyDef{}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// redshiftClusterSchema — Redshift is modeled on NodeTypeDatabase and shares the
// same extractDatabaseMetadata path (Endpoint.Address/Port).
var redshiftClusterSchema = core.SpecificTypeSchema{
	SpecificType: "RedshiftCluster",
	NodeType:     core.NodeTypeDatabase,
	Properties:   append(append([]core.PropertyDef{}, databaseSchemaProps...), redshiftClusterParityProps...),
}

// openSearchDomainSchema — OpenSearch/Elasticsearch Service (NodeTypeDatabase).
var openSearchDomainSchema = core.SpecificTypeSchema{
	SpecificType: "OpenSearchDomain",
	NodeType:     core.NodeTypeDatabase,
	Properties: []core.PropertyDef{
		{Name: "engine", Indexed: true},
		{Name: "engine_version", Indexed: true},
		{Name: "endpoint_address", Indexed: true},
		{Name: "encrypted", Indexed: true},
		{Name: "dns_name", Indexed: true},
		{Name: "vpc_id", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "deleted"},
		{Name: "created"},
		{Name: "endpoint"},
		{Name: "domain_name"},
		{Name: "elasticsearch_version"},
		{Name: "elasticsearch_cluster_config_instance_type"},
		{Name: "elasticsearch_cluster_config_instance_count"},
		{Name: "elasticsearch_cluster_config_dedicated_master_enabled"},
		{Name: "elasticsearch_cluster_config_zone_awareness_enabled"},
		{Name: "elasticsearch_cluster_config_dedicated_master_type"},
		{Name: "elasticsearch_cluster_config_dedicated_master_count"},
		{Name: "ebs_options_ebs_enabled"},
		{Name: "ebs_options_volume_type"},
		{Name: "ebs_options_volume_size"},
		{Name: "ebs_options_iops"},
		{Name: "encryption_at_rest_options_enabled"},
		{Name: "encryption_at_rest_options_kms_key_id"},
		{Name: "log_publishing_index_slow_logs_enabled"},
		{Name: "log_publishing_index_slow_logs_arn"},
		{Name: "log_publishing_search_slow_logs_enabled"},
		{Name: "log_publishing_search_slow_logs_arn"},
		{Name: "log_publishing_es_application_logs_enabled"},
		{Name: "log_publishing_es_application_logs_arn"},
		{Name: "log_publishing_audit_logs_enabled"},
		{Name: "log_publishing_audit_logs_arn"},
	},
}

// dynamoDBTableSchema — DynamoDB (NodeTypeDatabase). Only encrypted is read by the
// ontology; DynamoDB metadata carries no endpoint/engine.
var dynamoDBTableSchema = core.SpecificTypeSchema{
	SpecificType: "DynamoDBTable",
	NodeType:     core.NodeTypeDatabase,
	Properties: []core.PropertyDef{
		{Name: "encrypted", Indexed: true},
		{Name: "dns_name", Indexed: true},
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "table_name"},
		{Name: "rows"},
		{Name: "size"},
		{Name: "table_status"},
		{Name: "creation_date_time"},
		{Name: "provisioned_throughput_read_capacity_units"},
		{Name: "provisioned_throughput_write_capacity_units"},
	},
}

// rdsClusterSnapshotSchema — RDS cluster snapshots roll up to the generic
// NodeTypeCloudResource ontology; they carry no resource-specific extracted fields.
var rdsClusterSnapshotSchema = core.SpecificTypeSchema{
	SpecificType: "RDSClusterSnapshot",
	NodeType:     core.NodeTypeCloudResource,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "db_snapshot_identifier"},
		{Name: "public"},
		{Name: "db_instance_identifier"},
		{Name: "snapshot_create_time"},
		{Name: "engine"},
		{Name: "engine_version"},
		{Name: "allocated_storage"},
		{Name: "port"},
		{Name: "availability_zone"},
		{Name: "vpc_id"},
		{Name: "instance_create_time"},
		{Name: "master_username"},
		{Name: "license_model"},
		{Name: "snapshot_type"},
		{Name: "iops"},
		{Name: "option_group_name"},
		{Name: "percent_progress"},
		{Name: "source_region"},
		{Name: "source_db_snapshot_identifier"},
		{Name: "storage_type"},
		{Name: "tde_credential_arn"},
		{Name: "encrypted"},
		{Name: "kms_key_id"},
		{Name: "timezone"},
		{Name: "iam_database_authentication_enabled"},
		{Name: "processor_features"},
		{Name: "dbi_resource_id"},
		{Name: "original_snapshot_create_time"},
		{Name: "snapshot_database_time"},
		{Name: "snapshot_target"},
		{Name: "storage_throughput"},
	},
}

func init() {
	core.RegisterSpecificTypeSchema(rdsInstanceSchema)
	core.RegisterSpecificTypeSchema(rdsClusterSchema)
	core.RegisterSpecificTypeSchema(redshiftClusterSchema)
	core.RegisterSpecificTypeSchema(openSearchDomainSchema)
	core.RegisterSpecificTypeSchema(dynamoDBTableSchema)
	core.RegisterSpecificTypeSchema(rdsClusterSnapshotSchema)
}

// extractDatabaseMetadata extracts essential fields for database nodes (RDS, Aurora)
func (s *AWSSource) extractDatabaseMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	// Endpoint (critical for connections)
	if endpoint, ok := metaMap["Endpoint"].(map[string]interface{}); ok {
		if address, ok := endpoint["Address"].(string); ok && address != "" {
			properties["endpoint_address"] = address
		}
		if port, ok := endpoint["Port"].(float64); ok {
			properties["endpoint_port"] = int(port)
		}
	}

	// Engine info (important for compatibility)
	if engine, ok := metaMap["Engine"].(string); ok && engine != "" {
		properties["engine"] = engine
	}
	if engineVersion, ok := metaMap["EngineVersion"].(string); ok && engineVersion != "" {
		properties["engine_version"] = engineVersion
	}

	// Instance type (important for capacity planning)
	if instanceType, ok := metaMap["DBInstanceClass"].(string); ok && instanceType != "" {
		properties["instance_type"] = instanceType
	}

	// Multi-AZ (important for HA)
	if multiAZ, ok := metaMap["MultiAZ"].(bool); ok {
		properties["multi_az"] = multiAZ
	}

	// Storage info (important for capacity)
	if allocatedStorage, ok := metaMap["AllocatedStorage"].(float64); ok {
		properties["allocated_storage_gb"] = int(allocatedStorage)
	}

	// VPC ID, subnet IDs, and security group IDs from DBSubnetGroup (needed for edge creation)
	if dbSubnetGroup, ok := metaMap["DBSubnetGroup"].(map[string]interface{}); ok {
		if vpcID, ok := dbSubnetGroup["VpcId"].(string); ok && vpcID != "" {
			properties["vpc_id"] = vpcID
		}
		if subnets, ok := dbSubnetGroup["Subnets"].([]interface{}); ok && len(subnets) > 0 {
			subnetIDs := make([]string, 0, len(subnets))
			for _, s := range subnets {
				if sm, ok := s.(map[string]interface{}); ok {
					if sid, ok := sm["SubnetIdentifier"].(string); ok && sid != "" {
						subnetIDs = append(subnetIDs, sid)
					}
				}
			}
			if len(subnetIDs) > 0 {
				properties["subnet_ids"] = subnetIDs
			}
		}
	}

	// VPC security group IDs (needed for security group edge creation)
	if vpcSGs, ok := metaMap["VpcSecurityGroups"].([]interface{}); ok && len(vpcSGs) > 0 {
		sgIDs := make([]string, 0, len(vpcSGs))
		for _, sg := range vpcSGs {
			if sgMap, ok := sg.(map[string]interface{}); ok {
				if sgID, ok := sgMap["VpcSecurityGroupId"].(string); ok && sgID != "" {
					sgIDs = append(sgIDs, sgID)
				}
			}
		}
		if len(sgIDs) > 0 {
			properties["vpc_security_group_ids"] = sgIDs
		}
	}

	// DB identifiers — used by createRDSTopologyEdges to build Aurora
	// cluster-membership + read-replica edges. Present on RDS instances/clusters;
	// absent (guarded) on Redshift/OpenSearch/DynamoDB, which share this extractor.
	if id, ok := metaMap["DBInstanceIdentifier"].(string); ok && id != "" {
		properties["db_instance_identifier"] = id
	}
	if id, ok := metaMap["DBClusterIdentifier"].(string); ok && id != "" {
		properties["db_cluster_identifier"] = id
	}
	if src, ok := metaMap["ReadReplicaSourceDBInstanceIdentifier"].(string); ok && src != "" {
		properties["read_replica_source"] = src
	}
}

// createRDSEdges creates edges for RDS instances/clusters
func (s *AWSSource) createRDSEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	for _, node := range nodes {
		// Use pre-extracted properties (set by extractDatabaseMetadata and extractCommonMetadataFields).
		// Raw meta is not stored in node.Properties, so we read the flattened fields.

		// 1. RDS → VPC relationship
		if vpcID, ok := node.Properties["vpc_id"].(string); ok && vpcID != "" {
			if vpcNode, exists := lookup.ByResourceID[vpcID]; exists {
				edges = append(edges, s.createEdge(node, vpcNode, core.RelationshipHostedOn,
					map[string]interface{}{"connection_type": "vpc"}, req))
			}
		}

		// 2. RDS → Subnet relationships (subnet_ids extracted by extractDatabaseMetadata)
		if subnetIDs, ok := node.Properties["subnet_ids"].([]string); ok {
			for _, subnetID := range subnetIDs {
				if subnetNode, exists := lookup.ByResourceID[subnetID]; exists {
					edges = append(edges, s.createEdge(node, subnetNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "subnet"}, req))
				}
			}
		}

		// 3. RDS → Security Group relationships (vpc_security_group_ids extracted by extractDatabaseMetadata)
		if sgIDs, ok := node.Properties["vpc_security_group_ids"].([]string); ok {
			for _, sgID := range sgIDs {
				if sgNode, exists := lookup.ByResourceID[sgID]; exists {
					edges = append(edges, s.createEdge(node, sgNode, core.RelationshipHostedOn,
						map[string]interface{}{"connection_type": "security_group"}, req))
				}
			}
		}
	}

	return edges
}

// awsRDSRels is the declarative form of createRDSEdges: RDS/Aurora instances are
// HOSTED_ON their VPC, subnets, and security groups, each resolved by resource_id.
// The DynamoDB carve-out (DynamoDB has no VPC/subnet edges) stays at the dispatch
// call site, exactly as the hand-written path filters it before calling.
var awsRDSRels = []model.RelSpec{
	{
		Key:     "vpc_id",
		Shape:   model.Scalar,
		Lookup:  model.ByResourceID,
		RelType: core.RelationshipHostedOn,
		Props:   map[string]interface{}{"connection_type": "vpc"},
	},
	{
		Key:     "subnet_ids",
		Shape:   model.StringSlice,
		Lookup:  model.ByResourceID,
		RelType: core.RelationshipHostedOn,
		Props:   map[string]interface{}{"connection_type": "subnet"},
	},
	{
		Key:     "vpc_security_group_ids",
		Shape:   model.StringSlice,
		Lookup:  model.ByResourceID,
		RelType: core.RelationshipHostedOn,
		Props:   map[string]interface{}{"connection_type": "security_group"},
	},
}

// awsEdgeFactory returns a model.EdgeFactory that stamps edges exactly like the
// AWSSource.createEdge helper (source = "aws", tenant/account from the request).
func awsEdgeFactory(req *core.SourceBuildRequest) model.EdgeFactory {
	return func(src, dst *core.DbNode, rel core.RelationshipType, props map[string]interface{}) *core.DbEdge {
		return core.NewEdge(src.ID, dst.ID, rel, props, req.TenantID, req.CloudAccountID, "aws")
	}
}

// createRDSEdgesViaEngine is the declarative-engine equivalent of createRDSEdges.
// Kept as a thin wrapper so the dispatch call site reads the same and the
// fidelity test can compare the two implementations directly.
func (s *AWSSource) createRDSEdgesViaEngine(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	return model.BuildEdges(nodes, awsRDSRels, lookup, awsEdgeFactory(req))
}

// createRDSTopologyEdges builds the RDS/Aurora topology edges that are NOT simple
// property→resource_id attachments (so they sit outside the createRDSEdges/…ViaEngine
// fidelity pair): Aurora cluster membership and read replicas. Matching is by DB
// identifier, not resource_id — an instance's db_cluster_identifier points at the
// cluster's own db_cluster_identifier; a replica's read_replica_source points at the
// primary's db_instance_identifier (or its ARN for cross-region replicas).
//
//   - instance  -BELONGS_TO(rds_cluster_member)->  cluster
//   - replica   -ASSOCIATED_WITH(read_replica)->   source instance
func (s *AWSSource) createRDSTopologyEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Index RDS clusters + instances by their own DB identifiers.
	clusterByID := make(map[string]*core.DbNode)
	instanceByID := make(map[string]*core.DbNode)
	for _, n := range nodes {
		switch n.SpecificType {
		case "RDSCluster":
			if id, ok := n.Properties["db_cluster_identifier"].(string); ok && id != "" {
				clusterByID[id] = n
			}
		case "RDSInstance":
			if id, ok := n.Properties["db_instance_identifier"].(string); ok && id != "" {
				instanceByID[id] = n
			}
		}
	}

	for _, node := range nodes {
		if node.SpecificType != "RDSInstance" {
			continue
		}

		// Aurora cluster membership: instance BELONGS_TO its DB cluster.
		if cid, ok := node.Properties["db_cluster_identifier"].(string); ok && cid != "" {
			if cluster := clusterByID[cid]; cluster != nil && cluster.ID != node.ID {
				edges = append(edges, s.createEdge(node, cluster, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "rds_cluster_member"}, req))
			}
		}

		// Read replica: replica ASSOCIATED_WITH its source instance. The source is a
		// bare identifier for same-region replicas, or an ARN for cross-region ones.
		if src, ok := node.Properties["read_replica_source"].(string); ok && src != "" {
			primary := instanceByID[src]
			if primary == nil {
				primary = lookup.ByARN[src]
			}
			if primary != nil && primary.ID != node.ID {
				edges = append(edges, s.createEdge(node, primary, core.RelationshipAssociatedWith,
					map[string]interface{}{"connection_type": "read_replica"}, req))
			}
		}
	}

	return edges
}
