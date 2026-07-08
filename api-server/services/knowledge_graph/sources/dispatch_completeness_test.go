package sources

// Dispatch-completeness tests (E2E KG test module, Tier A).
//
// These assert that EVERY entry in each cloud source's canonical resource-type
// registry maps to the NodeType the registry declares. They need no fixtures
// and no DB, and they fail the moment a new resource type is added to a registry
// without a conscious NodeType decision — the exhaustive "every type is mapped"
// guarantee described in the plan.

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// sentinelUnknownType is a resource type guaranteed absent from every registry,
// used to force determineNodeType down its service-name fallback path.
const sentinelUnknownType = "__e2e_nonexistent_type__"

func TestE2E_AWSDispatchCompleteness(t *testing.T) {
	s, err := NewAWSSource(AWSSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewAWSSource: %v", err)
	}

	// Primary registry: (resourceType, serviceName) -> NodeType.
	for resourceType, serviceMap := range awsResourceTypeMap {
		for serviceName, want := range serviceMap {
			if got := s.determineNodeType(resourceType, serviceName); got != want {
				t.Errorf("aws primary [%s / %s]: want %s, got %s", resourceType, serviceName, want, got)
			}
		}
	}

	// Fallback registry: serviceName -> NodeType (exercised with an unknown type).
	for serviceName, want := range awsServiceFallbackMap {
		if got := s.determineNodeType(sentinelUnknownType, serviceName); got != want {
			t.Errorf("aws fallback [%s]: want %s, got %s", serviceName, want, got)
		}
	}

	// An entirely unknown (type, service) must degrade to CloudResource.
	if got := s.determineNodeType(sentinelUnknownType, "__e2e_nonexistent_service__"); got != core.NodeTypeCloudResource {
		t.Errorf("aws unknown fallthrough: want %s, got %s", core.NodeTypeCloudResource, got)
	}
}

func TestE2E_GCPDispatchCompleteness(t *testing.T) {
	s, err := NewGCPSource(GCPSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewGCPSource: %v", err)
	}

	for resourceType, serviceMap := range gcpResourceTypeMap {
		for serviceName, want := range serviceMap {
			if got := s.determineNodeType(resourceType, serviceName); got != want {
				t.Errorf("gcp primary [%s / %s]: want %s, got %s", resourceType, serviceName, want, got)
			}
		}
	}

	for serviceName, want := range gcpServiceFallbackMap {
		if got := s.determineNodeType(sentinelUnknownType, serviceName); got != want {
			t.Errorf("gcp fallback [%s]: want %s, got %s", serviceName, want, got)
		}
	}

	if got := s.determineNodeType(sentinelUnknownType, "__e2e_nonexistent_service__"); got != core.NodeTypeCloudResource {
		t.Errorf("gcp unknown fallthrough: want %s, got %s", core.NodeTypeCloudResource, got)
	}
}

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
