package azure

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestSpecificTypeHasSchema is the forcing function for Azure: every concrete
// specific_type label determineSpecificType (sources/azure/specific_type.go) can
// emit must have a registered concrete-property schema (core.SpecificTypeSchema)
// declared co-located in its sources/azure/*.go file. A newly
// added Azure specific_type therefore cannot ship without also declaring the
// schema that drives its query_attributes. Labels are derived by driving
// determineSpecificType across every switch arm so the two stay coupled.
func TestSpecificTypeHasSchema(t *testing.T) {
	s, err := NewAzureSource(AzureSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAzureSource: %v", err)
	}

	// (NodeType, serviceName) inputs covering every arm of determineSpecificType,
	// including the Database service_name split (cosmos → AzureCosmosDB).
	cases := []struct {
		nodeType    core.NodeType
		serviceName string
	}{
		{core.NodeTypeComputeInstance, ""},
		{core.NodeTypeDatabase, "microsoft.sql/servers"},
		{core.NodeTypeDatabase, "microsoft.documentdb/databaseaccounts"},
		{core.NodeTypeManagedCluster, ""},
		{core.NodeTypeStorage, ""},
		{core.NodeTypeCache, ""},
		{core.NodeTypeServerlessFunction, ""},
		{core.NodeTypeLoadBalancer, ""},
		{core.NodeTypeBackendPool, ""},
		{core.NodeTypeVPC, ""},
		{core.NodeTypeSubnet, ""},
		{core.NodeTypeSecurityGroup, ""},
		{core.NodeTypeNetworkInterface, ""},
		{core.NodeTypeRouteTable, ""},
		{core.NodeTypeNetworkGateway, ""},
		{core.NodeTypePrivateEndpoint, ""},
		{core.NodeTypePublicIP, ""},
		{core.NodeTypeCDN, ""},
		{core.NodeTypeDNSZone, ""},
		{core.NodeTypeContainerRegistry, ""},
		{core.NodeTypeSecretVault, ""},
		{core.NodeTypeEncryptionKey, ""},
		{core.NodeTypeAPIGateway, ""},
		{core.NodeTypeMonitoringService, ""},
		{core.NodeTypeLogAggregator, ""},
		{core.NodeTypeServiceIdentity, ""},
		{core.NodeTypeWorkload, ""},
	}

	seen := map[string]bool{}
	for _, c := range cases {
		label := s.determineSpecificType(c.nodeType, c.serviceName, "")
		if seen[label] {
			continue
		}
		seen[label] = true
		if _, ok := core.LookupSpecificTypeSchema(label); !ok {
			t.Errorf("Azure specific_type %q (from NodeType %s) has no concrete schema — register a core.SpecificTypeSchema in its sources/azure/*.go file",
				label, c.nodeType)
		}
	}

	// The 27 azureOntology keys are the authoritative label set; make sure the
	// switch above actually produced them all (guards against a missed arm).
	if len(seen) != 27 {
		t.Errorf("expected 27 distinct Azure specific_type labels, drove %d — a determineSpecificType arm is likely uncovered", len(seen))
	}
}
