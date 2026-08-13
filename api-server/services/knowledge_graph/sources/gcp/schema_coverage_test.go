package gcp

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSpecificTypeHasSchema is the coverage guardrail for the GCP source: every
// concrete specific_type label that determineSpecificType (specific_type.go) can
// return must have a registered concrete-property schema
// (core.SpecificTypeSchema) co-located in this package. A newly-added GCP
// resource type therefore cannot ship a specific_type without also declaring its
// concrete schema, which in turn drives query_attributes.
//
// The list is the exact set of string literals returned by the
// determineSpecificType switch (specific_type.go). All but "GCPStorage" are also
// keys of the gcpOntology map (core/ontology_data_gcp.go); "GCPStorage" is the
// NodeTypeStorage default fallback, which has no ontology spec — so it is
// enumerated here (schema coverage) but not by the ontology layer.
func TestSpecificTypeHasSchema(t *testing.T) {
	labels := []string{
		"GCEInstance",
		"CloudSQLInstance",
		"BigQueryDataset",
		"GCPDatabase",
		"GKECluster",
		"GCSBucket",
		"FilestoreInstance",
		"PersistentDisk",
		"GCPStorage",
		"MemorystoreInstance",
		"CloudRunService",
		"PubSubTopic",
		"ArtifactRegistry",
		"GCPLoadBalancer",
		"GCPBackendService",
		"GCPVpc",
		"GCPSubnet",
		"GCPFirewall",
		"CloudDNSZone",
		"CloudNATGateway",
		"GCPExternalIP",
		"CloudLogging",
		"CloudMonitoring",
		"VertexAI",
		"SecretManagerSecret",
		"CloudKMSKey",
		"GCPServiceAccount",
	}
	for _, label := range labels {
		if _, ok := core.LookupSpecificTypeSchema(label); !ok {
			t.Errorf("GCP specific_type %q has no concrete schema — declare a core.SpecificTypeSchema and RegisterSpecificTypeSchema in its sources/gcp/*.go file", label)
		}
	}
}
