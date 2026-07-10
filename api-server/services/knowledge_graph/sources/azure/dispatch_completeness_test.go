package azure

// Azure dispatch-completeness (Tier A): every entry in the Azure resource-type
// registry must map to the NodeType it declares. Moved here from package sources
// because it reads the unexported azure registry maps + determineNodeType.

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

const sentinelUnknownType = "__e2e_nonexistent_type__"

func TestE2E_AzureDispatchCompleteness(t *testing.T) {
	s, err := NewAzureSource(AzureSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAzureSource: %v", err)
	}

	// Primary registry is flat: resourceType -> NodeType (serviceName irrelevant).
	for resourceType, want := range azureTypeToNodeType {
		if got := s.determineNodeType(resourceType, ""); got != want {
			t.Errorf("azure primary [%s]: want %s, got %s", resourceType, want, got)
		}
	}

	// Service-prefix fallback: serviceName-prefix -> NodeType (unknown type forces it).
	for prefix, want := range azureServicePrefixToNodeType {
		if got := s.determineNodeType(sentinelUnknownType, prefix); got != want {
			t.Errorf("azure prefix fallback [%s]: want %s, got %s", prefix, want, got)
		}
	}

	if got := s.determineNodeType(sentinelUnknownType, "__e2e_nonexistent_service__"); got != core.NodeTypeCloudResource {
		t.Errorf("azure unknown fallthrough: want %s, got %s", core.NodeTypeCloudResource, got)
	}
}
