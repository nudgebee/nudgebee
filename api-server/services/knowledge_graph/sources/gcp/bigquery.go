package gcp

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// bigQueryDatasetSchema is the concrete per-specific_type property schema for the
// BigQueryDataset specific_type.
// BigQuery datasets are ingested via the shared classify.go path and carry only
// universal base keys plus the raw `meta` blob (createBigQueryEdges reads
// meta.FullID for hierarchy, not a hoisted property), so no resource-specific
// fields are declared. The type/encrypted ontology fields are static_value
// constants (see core/ontology_data_gcp.go) and read no source key.
var bigQueryDatasetSchema = core.SpecificTypeSchema{
	SpecificType: "BigQueryDataset",
	NodeType:     core.NodeTypeDatabase,
	Properties: []core.PropertyDef{
		// Additional provider fields (not yet emitted by our extractor):
		{Name: "dataset_id"},
		{Name: "friendly_name"},
		{Name: "description"},
		{Name: "creation_time"},
		{Name: "last_modified_time"},
		{Name: "default_table_expiration_ms"},
		{Name: "default_partition_expiration_ms"},
		{Name: "default_kms_key_name"},
		{Name: "access_entries"},
	},
}

func init() { core.RegisterSpecificTypeSchema(bigQueryDatasetSchema) }

// createBigQueryEdges creates BELONGS_TO edges from BigQuery Tables/Views to their parent Datasets
// Uses the FullID field in meta: "project:dataset.table" → extract dataset name
func (s *GCPSource) createBigQueryEdges(nodes []*core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	dbNodes, ok := lookup.ByNodeType[core.NodeTypeDatabase]
	if !ok {
		return edges
	}

	for _, node := range dbNodes {
		serviceName, _ := node.Properties["service_name"].(string)
		if serviceName != "BigQuery" && serviceName != "bigquery.googleapis.com" {
			continue
		}

		resourceType, _ := node.Properties["type"].(string)
		resourceTypeLower := strings.ToLower(resourceType)
		if resourceTypeLower != "bigquery.googleapis.com/table" && resourceTypeLower != "bigquery.googleapis.com/view" {
			continue
		}

		// Extract dataset name from meta.FullID: "project:dataset.table"
		metaMap, ok := node.Properties["meta"].(map[string]interface{})
		if !ok {
			continue
		}
		fullID, _ := metaMap["FullID"].(string)
		datasetName := extractBigQueryDatasetName(fullID)
		if datasetName == "" {
			continue
		}

		// Find dataset node by name
		if datasetNode := findNodeByNameAndType(lookup, core.NodeTypeDatabase, datasetName); datasetNode != nil {
			// Verify it's a dataset
			datasetType, _ := datasetNode.Properties["type"].(string)
			if strings.ToLower(datasetType) == "bigquery.googleapis.com/dataset" || strings.ToLower(datasetType) == "bigquery" {
				edges = append(edges, s.createEdge(node, datasetNode, core.RelationshipBelongsTo,
					map[string]interface{}{"connection_type": "bigquery_dataset"}, req))
			}
		}
	}

	s.logger.Info("created GCP BigQuery hierarchy edges", "edge_count", len(edges))
	return edges
}

// extractBigQueryDatasetName extracts the dataset name from a BigQuery FullID
// Format: "project:dataset.table" or "project:dataset.view"
func extractBigQueryDatasetName(fullID string) string {
	if fullID == "" {
		return ""
	}
	// Split on ":" to get "project" and "dataset.table"
	colonIdx := strings.Index(fullID, ":")
	if colonIdx < 0 || colonIdx == len(fullID)-1 {
		return ""
	}
	datasetAndTable := fullID[colonIdx+1:]
	// Split on "." to get dataset name
	dotIdx := strings.Index(datasetAndTable, ".")
	if dotIdx <= 0 {
		return ""
	}
	return datasetAndTable[:dotIdx]
}
