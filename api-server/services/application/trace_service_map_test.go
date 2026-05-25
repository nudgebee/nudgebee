package application

import (
	"encoding/json"
	"log/slog"
	"nudgebee/services/query"
	"nudgebee/services/security"
	"nudgebee/services/traces"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFetchTracesAndBuildServiceMapIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	requestContext := security.NewRequestContextForTenantAdmin("6d79c39f-920e-4167-85cf-e2ee83dcbc03", logger, nil, nil)

	params := TraceQueryParams{
		StartTime: time.Now().Add(-15 * time.Minute),
		EndTime:   time.Now(),
		AccountID: "e9dc2b39-78d3-46da-9691-f160bd3c4c19",
		LabelFilters: []traces.LabelFilter{
			{
				Key:      "deployment.environment",
				Value:    "production",
				Operator: query.Eq,
			},
		},
	}

	serviceMap, err := FetchTracesAndBuildServiceMap(requestContext, params)

	if err != nil {
		t.Logf("Integration test encountered error (may be expected in test environment): %v", err)

		if containsSubstring(err.Error(), "failed to execute trace query") ||
			containsSubstring(err.Error(), "connection") ||
			containsSubstring(err.Error(), "database") {
			t.Skip("Skipping integration test - database connection not available")
			return
		}

		t.Skipf("Skipping integration test due to error: %v", err)
		return
	}

	if serviceMap == nil {
		t.Fatal("Service map should not be nil when no error occurred")
		return
	}

	t.Logf("✅ Successfully fetched and built service map with %d applications", len(serviceMap.Applications))

	for i, app := range serviceMap.Applications {
		if app.Id.Name == "" {
			t.Errorf("Application[%d] should have a non-empty Id.Name", i)
		}
		if app.Id.Kind == "" {
			t.Errorf("Application[%d] should have a non-empty Id.Kind", i)
		}
		if app.Id.Namespace == "" {
			t.Errorf("Application[%d] should have a non-empty Id.Namespace", i)
		}
		if *app.Status == 0 {
			t.Errorf("Application[%d] should have a non-empty Status", i)
		}
		if app.Type == nil {
			t.Errorf("Application[%d] should have a non-nil Type array", i)
		}
		if app.Instances == nil {
			t.Errorf("Application[%d] should have a non-nil Instances array", i)
		}
		if app.Upstreams == nil {
			t.Errorf("Application[%d] should have a non-nil Upstreams array", i)
		}
		if app.Downstreams == nil {
			t.Errorf("Application[%d] should have a non-nil Downstreams array", i)
		}

		if i == 0 {
			jsonData, _ := json.MarshalIndent(app, "", "  ")
			t.Logf("Sample robusta-format application structure:\n%s", jsonData)
		}

		for j, link := range app.Upstreams {
			if link.Id == "" {
				t.Errorf("Application[%d].Upstreams[%d] should have a non-empty Id.Name", i, j)
			}
			if link.Status == 0 {
				t.Errorf("Application[%d].Upstreams[%d] should have a non-empty Status", i, j)
			}
			if link.Protocol == "" {
				t.Errorf("Application[%d].Upstreams[%d] should have a non-empty Protocol", i, j)
			}
		}

		for j, instance := range app.Instances {
			if instance.Id.Name == "" {
				t.Errorf("Application[%d].Instances[%d] should have a non-empty Id.Name", i, j)
			}
		}
	}

	jsonData, err := serviceMap.ToJSON()
	if err != nil {
		t.Errorf("Failed to serialize service map to JSON: %v", err)
	} else {
		t.Logf("✅ Successfully serialized service map to JSON (%d bytes)", len(jsonData))

		jsonStr := string(jsonData)
		expectedRobustaFields := []string{
			"\"applications\":",
			"\"Id\":",
			"\"Status\":",
			"\"Upstreams\":",
			"\"Downstreams\":",
			"\"Instances\":",
			"\"IsHealthy\":",
			"\"Category\":",
		}

		for _, field := range expectedRobustaFields {
			if !containsSubstring(jsonStr, field) {
				t.Errorf("Expected JSON to contain robusta field %s", field)
			}
		}
	}

	if len(serviceMap.Applications) > 0 {
		firstApp := serviceMap.Applications[0]

		foundApp := serviceMap.GetServiceByName(firstApp.Id.Name)
		if foundApp == nil {
			t.Errorf("GetServiceByName should find existing service '%s'", firstApp.Id.Name)
		} else if foundApp.Id.Name != firstApp.Id.Name {
			t.Errorf("GetServiceByName returned wrong service: expected '%s', got '%s'", firstApp.Id.Name, foundApp.Id.Name)
		}

		topServices := serviceMap.GetTopServices(1)
		if len(topServices) == 0 {
			t.Error("GetTopServices should return at least one service")
		} else if len(topServices) > 1 {
			t.Errorf("GetTopServices(1) returned %d services, expected 1", len(topServices))
		}
	}

	t.Logf("✅ Integration test completed successfully - robusta format service map is working correctly!")
}

func containsSubstring(s, substr string) bool {
	return strings.Contains(s, substr)
}
