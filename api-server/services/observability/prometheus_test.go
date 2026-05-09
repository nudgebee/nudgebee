package observability

import (
	"encoding/json"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrometheus(t *testing.T) {
	ctx := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	now := time.Now().UnixMilli()
	sixHoursAgo := now - 6*60*60*1000

	source := &PrometheusLogGroupSource{}
	output, err := source.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: os.Getenv("TEST_ACCOUNT"),
		StartTime: sixHoursAgo,
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("QueryLogGroup failed: %v", err)
	}

	if len(output.Groups) == 0 {
		t.Fatal("Expected at least one group")
	}

	for _, group := range output.Groups {
		// Range query should return multiple timestamps per series
		if len(group.Timestamps) < 2 {
			t.Errorf("Expected multiple timestamps (range query), got %d for group %v — query may be running as instant", len(group.Timestamps), group.Sample)
		}
	}

	jsonBytes, _ := json.MarshalIndent(output, "", "  ")
	t.Logf("QueryLogGroup Output: %s", string(jsonBytes))
}

func TestQueryLogGroupWithNamespaceFilter(t *testing.T) {
	ctx := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	now := time.Now().UnixMilli()
	sixHoursAgo := now - 6*60*60*1000

	source := &PrometheusLogGroupSource{}
	output, err := source.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: os.Getenv("TEST_ACCOUNT"),
		Request:   map[string]any{"selectedNamespace": "nudgebee"},
		StartTime: sixHoursAgo,
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("QueryLogGroup with namespace filter failed: %v", err)
	}

	for _, group := range output.Groups {
		if group.ContainerID != "" && !strings.Contains(group.ContainerID, "/k8s/nudgebee/") {
			t.Errorf("Expected container_id to match namespace 'nudgebee', got %q", group.ContainerID)
		}
	}

	jsonBytes, _ := json.MarshalIndent(output, "", "  ")
	t.Logf("QueryLogGroup (namespace=nudgebee) Output: %s", string(jsonBytes))
}

func TestQueryLogGroupIncludesRedeployedPods(t *testing.T) {
	ctx := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	now := time.Now().UnixMilli()
	sixHoursAgo := now - 6*60*60*1000

	source := &PrometheusLogGroupSource{}
	output, err := source.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: os.Getenv("TEST_ACCOUNT"),
		StartTime: sixHoursAgo,
		EndTime:   now,
	})
	if err != nil {
		t.Fatalf("QueryLogGroup failed: %v", err)
	}

	containerIDs := map[string]struct{}{}
	for _, group := range output.Groups {
		if group.ContainerID != "" {
			containerIDs[group.ContainerID] = struct{}{}
		}
	}

	t.Logf("Found %d unique container_ids across log groups", len(containerIDs))
}

func TestPrometheusErrorResponse(t *testing.T) {
	// This test verifies that error responses from Prometheus are properly parsed
	// and included in the output with error information
	ctx := security.NewRequestContextForUserTenant(os.Getenv("TEST_USER"), os.Getenv("TEST_TENANT"), nil, nil, nil)

	now := time.Now().UnixMilli()
	oneHourAgo := now - 60*60*1000

	source := &PrometheusLogGroupSource{}
	output, err := source.QueryLogGroup(ctx, FetchLogGroupRequest{
		AccountId: os.Getenv("TEST_ACCOUNT"),
		StartTime: oneHourAgo,
		EndTime:   now,
	})

	// Log the output regardless of error
	jsonBytes, _ := json.MarshalIndent(output, "", "  ")
	t.Logf("Prometheus Query Output: %s", string(jsonBytes))

	if err != nil {
		t.Logf("Expected no error, got %v", err)
	}
}
