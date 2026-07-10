package azure

import (
	"strings"

	"nudgebee/services/knowledge_graph/core"
)

// databaseProperties is the shared concrete-property set for Azure managed
// databases — both roll up to NodeTypeDatabase and are populated by
// extractDatabaseMetadata + extractCommonNetworkIDs. Universal base keys
// (name/region/subtype/...) are implicit.
var databaseProperties = []core.PropertyDef{
	{Name: "dns_name", Indexed: true}, // ~ endpoint (fqdn / endpoint)
	{Name: "port", Indexed: true},
	{Name: "resource_group", Indexed: true},
	{Name: "vnet_id"},
	{Name: "subnet_id"},
	{Name: "nsg_id"},
}

// azureSQLDatabaseSchema / azureCosmosDBSchema are the concrete per-specific_type
// property schemas for the two Azure database specific_types resolved in
// sources/azure/specific_type.go. azureSQLDatabaseParityProps /
// azureCosmosDBParityProps document the remaining provider fields not yet emitted
// by our extractor. They are appended per-specific_type because the two database
// models diverge — kept out of the shared databaseProperties for that reason.
// Additional provider fields (not yet emitted by our extractor):
var azureSQLDatabaseParityProps = []core.PropertyDef{
	{Name: "kind"},
	{Name: "creation_date"},
	{Name: "database_id"},
	{Name: "max_size_bytes"},
	{Name: "license_type"},
	{Name: "default_secondary_location"},
	{Name: "elastic_pool_id"},
	{Name: "collation"},
	{Name: "failover_group_id"},
	{Name: "zone_redundant"},
	{Name: "restorable_dropped_database_id"},
	{Name: "recoverable_database_id"},
}

// Additional provider fields (not yet emitted by our extractor):
// (document_endpoint is represented by the declared dns_name field)
var azureCosmosDBParityProps = []core.PropertyDef{
	{Name: "kind"},
	{Name: "ipruleslist"},
	{Name: "capabilities"},
	{Name: "is_virtual_network_filter_enabled"},
	{Name: "enable_automatic_failover"},
	{Name: "provisioning_state"},
	{Name: "enable_multiple_write_locations"},
	{Name: "database_account_offer_type"},
	{Name: "public_network_access"},
	{Name: "enable_cassandra_connector"},
	{Name: "connector_offer"},
	{Name: "disable_key_based_metadata_write_access"},
	{Name: "key_vault_key_uri"},
	{Name: "enable_free_tier"},
	{Name: "enable_analytical_storage"},
	{Name: "default_consistency_level"}, // consistency_policy.default_consistency_level
	{Name: "max_staleness_prefix"},      // consistency_policy.max_staleness_prefix
	{Name: "max_interval_in_seconds"},   // consistency_policy.max_interval_in_seconds
}

var azureSQLDatabaseSchema = core.SpecificTypeSchema{
	SpecificType: "AzureSQLDatabase",
	NodeType:     core.NodeTypeDatabase,
	Properties:   append(append([]core.PropertyDef{}, databaseProperties...), azureSQLDatabaseParityProps...),
}

var azureCosmosDBSchema = core.SpecificTypeSchema{
	SpecificType: "AzureCosmosDB",
	NodeType:     core.NodeTypeDatabase,
	Properties:   append(append([]core.PropertyDef{}, databaseProperties...), azureCosmosDBParityProps...),
}

func init() {
	core.RegisterSpecificTypeSchema(azureSQLDatabaseSchema)
	core.RegisterSpecificTypeSchema(azureCosmosDBSchema)
}

// createSQLServerDatabaseEdges links each Azure SQL database to its parent SQL server
// (the parent SQL server contains the SQL database). Both are collected as
// NodeTypeDatabase nodes; a database's ARM ID is its server's ARM ID + "/databases/<db>",
// so the child is matched to its parent by ARM-ID prefix. Emitted as
// database -BELONGS_TO(azure_sql_server)-> server (child→parent, like the RDS
// cluster-membership edge). Generic on the "/databases/" segment, so it also links
// MySQL/MariaDB server→db where both are collected; Cosmos (no separate db nodes) is
// unaffected.
func (s *AzureSource) createSQLServerDatabaseEdges(nodes []*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	// Index the database-family nodes by lowercased ARM ID (Azure IDs are case-insensitive).
	byARN := make(map[string]*core.DbNode)
	for _, n := range nodes {
		if arn, ok := n.Properties["arn"].(string); ok && arn != "" {
			byARN[strings.ToLower(arn)] = n
		}
	}

	const dbSeg = "/databases/"
	for _, node := range nodes {
		arn, ok := node.Properties["arn"].(string)
		if !ok || arn == "" {
			continue
		}
		lower := strings.ToLower(arn)
		idx := strings.Index(lower, dbSeg)
		if idx < 0 {
			continue // server nodes (and non-server-scoped DBs) carry no "/databases/" segment
		}
		serverARN := lower[:idx]
		if server, exists := byARN[serverARN]; exists && server.ID != node.ID {
			edges = append(edges, s.createEdge(node, server, core.RelationshipBelongsTo,
				map[string]interface{}{"connection_type": "azure_sql_server"}, req))
		}
	}

	return edges
}

func (s *AzureSource) extractDatabaseMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if fqdn, ok := metaMap["fullyQualifiedDomainName"].(string); ok && fqdn != "" {
		properties["dns_name"] = fqdn
	}
	if endpoint, ok := metaMap["endpoint"].(string); ok && endpoint != "" {
		properties["dns_name"] = endpoint
	}
	if port, ok := metaMap["port"]; ok {
		properties["port"] = port
	}
}
