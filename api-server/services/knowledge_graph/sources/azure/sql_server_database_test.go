package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

func TestCreateSQLServerDatabaseEdges(t *testing.T) {
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
	s := &AzureSource{}

	const serverARN = "/subscriptions/s1/resourceGroups/rg/providers/Microsoft.Sql/servers/mysqlsrv"
	server := &core.DbNode{ID: "srv", NodeType: core.NodeTypeDatabase, Properties: map[string]interface{}{
		"name": "mysqlsrv", "arn": serverARN,
	}}
	db1 := &core.DbNode{ID: "db1", NodeType: core.NodeTypeDatabase, Properties: map[string]interface{}{
		"name": "orders", "arn": serverARN + "/databases/orders",
	}}
	db2 := &core.DbNode{ID: "db2", NodeType: core.NodeTypeDatabase, Properties: map[string]interface{}{
		"name": "billing", "arn": serverARN + "/databases/billing",
	}}
	// A database whose server node is absent → no edge.
	orphan := &core.DbNode{ID: "orphan", NodeType: core.NodeTypeDatabase, Properties: map[string]interface{}{
		"name": "x", "arn": "/subscriptions/s1/.../Microsoft.Sql/servers/other/databases/x",
	}}

	edges := s.createSQLServerDatabaseEdges([]*core.DbNode{server, db1, db2, orphan}, req)

	if len(edges) != 2 {
		t.Fatalf("expected 2 database→server edges (db1, db2), got %d", len(edges))
	}
	for _, e := range edges {
		conn, _ := e.Properties["connection_type"].(string)
		if e.DestinationNodeID != "srv" || e.RelationshipType != core.RelationshipBelongsTo || conn != "azure_sql_server" {
			t.Errorf("edge %s→%s (%s/%s), want *→srv BELONGS_TO/azure_sql_server", e.SourceNodeID, e.DestinationNodeID, e.RelationshipType, conn)
		}
	}
	// The server itself and the orphan must not be sources of an edge.
	for _, e := range edges {
		if e.SourceNodeID == "srv" || e.SourceNodeID == "orphan" {
			t.Errorf("unexpected edge source %s", e.SourceNodeID)
		}
	}
}
