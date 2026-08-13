package gcp

// GCP dispatch-completeness (Tier A): every entry in the GCP resource-type
// registry must map to the NodeType it declares. Moved here from package sources
// because it reads the unexported gcp registry maps + determineNodeType.

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

const sentinelUnknownType = "__e2e_nonexistent_type__"

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
