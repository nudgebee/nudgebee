package aws

// AWS dispatch-completeness (Tier A): every entry in the AWS resource-type
// registry must map to the NodeType it declares. Moved here from package sources
// because it reads the unexported awsResourceTypeMap/awsServiceFallbackMap and
// calls the unexported determineNodeType.

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// sentinelUnknownType is a resource type guaranteed absent from the registry,
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
