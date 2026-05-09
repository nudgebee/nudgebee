package traces

import (
	"testing"
)

func TestCreateExternalServiceApplications(t *testing.T) {
	builder := NewTraceServiceMapBuilder()

	// Create mock dependency map with external services
	dependencyMap := map[string]*ServiceDependency{
		"user-service->postgres-db": {
			Source:         "user-service",
			Target:         "postgres-prod.example.com",
			CallCount:      50,
			ErrorCount:     2,
			TotalDuration:  250000000, // 50 calls * 5ms each = 250ms total in nanoseconds
			Protocol:       "POSTGRESQL",
			DependencyType: "db_connection",
			Environment:    "production",
		},
		"worker-service->kafka-topic": {
			Source:         "worker-service",
			Target:         "production-dlq-global-worker",
			CallCount:      25,
			ErrorCount:     0,
			TotalDuration:  50000000000, // 50ms in nanoseconds
			Protocol:       "KAFKA",
			DependencyType: "messaging_system",
			Environment:    "production",
		},
		"cache-service->redis": {
			Source:         "cache-service",
			Target:         "redis-cluster.internal",
			CallCount:      100,
			ErrorCount:     1,
			TotalDuration:  20000000, // 100 calls * 0.2ms each = 20ms total in nanoseconds
			Protocol:       "REDIS",
			DependencyType: "db_connection",
			Environment:    "production",
		},
		"user-service->order-service": {
			Source:         "user-service",
			Target:         "order-service",
			CallCount:      30,
			ErrorCount:     0,
			Protocol:       "HTTP",
			DependencyType: "direct_service",
			Environment:    "production",
		},
	}

	// Create mock service stats (only for actual services)
	serviceStats := map[string]*serviceMetrics{
		"user-service": {
			ServiceName: "user-service",
			Namespace:   "default",
		},
		"worker-service": {
			ServiceName: "worker-service",
			Namespace:   "workers",
		},
		"cache-service": {
			ServiceName: "cache-service",
			Namespace:   "default",
		},
		"order-service": {
			ServiceName: "order-service",
			Namespace:   "default",
		},
	}

	// Test external service application creation with 30 minute duration
	durationMinutes := 30.0
	externalApps := builder.createExternalServiceApplications(dependencyMap, serviceStats, durationMinutes)

	// Should create 3 external applications (postgres, kafka, redis) but not order-service
	if len(externalApps) != 3 {
		t.Errorf("Expected 3 external applications, got %d", len(externalApps))
	}

	// Verify each external application
	appNames := make(map[string]bool)
	for _, app := range externalApps {
		appNames[app.Id.Name] = true

		// Check that it's marked as external
		if app.Id.Kind != "ExternalService" {
			t.Errorf("Expected Kind=ExternalService for %s, got %s", app.Id.Name, app.Id.Kind)
		}

		if app.Category.Category != "external" {
			t.Errorf("Expected Category=external for %s, got %s", app.Id.Name, app.Category.Category)
		}

		if app.Labels["external"] != "true" {
			t.Errorf("Expected external=true label for %s", app.Id.Name)
		}

		// Check specific application types
		switch app.Id.Name {
		case "postgres-prod.example.com":
			expectedTypes := []string{"postgres", "database"}
			if !equalStringSlices(app.Type, expectedTypes) {
				t.Errorf("Expected types %v for postgres, got %v", expectedTypes, app.Type)
			}
			if app.Labels["protocol"] != "POSTGRESQL" {
				t.Errorf("Expected protocol=POSTGRESQL for postgres, got %s", app.Labels["protocol"])
			}

		case "production-dlq-global-worker":
			expectedTypes := []string{"kafka", "messaging"}
			if !equalStringSlices(app.Type, expectedTypes) {
				t.Errorf("Expected types %v for kafka, got %v", expectedTypes, app.Type)
			}
			if app.Labels["protocol"] != "KAFKA" {
				t.Errorf("Expected protocol=KAFKA for kafka, got %s", app.Labels["protocol"])
			}

		case "redis-cluster.internal":
			expectedTypes := []string{"redis", "cache"}
			if !equalStringSlices(app.Type, expectedTypes) {
				t.Errorf("Expected types %v for redis, got %v", expectedTypes, app.Type)
			}
			if app.Labels["protocol"] != "REDIS" {
				t.Errorf("Expected protocol=REDIS for redis, got %s", app.Labels["protocol"])
			}
		}

		// Verify downstreams point back to dependent services
		if len(app.Downstreams) == 0 {
			t.Errorf("Expected downstreams for external service %s", app.Id.Name)
		}
	}

	// Verify expected external services are present
	expectedServices := []string{"postgres-prod.example.com", "production-dlq-global-worker", "redis-cluster.internal"}
	for _, expected := range expectedServices {
		if !appNames[expected] {
			t.Errorf("Expected external service %s not found", expected)
		}
	}

	// Verify order-service is NOT in external apps (it's a real service)
	if appNames["order-service"] {
		t.Errorf("order-service should not be created as external application")
	}
}

func TestIsExternalDependency(t *testing.T) {
	builder := NewTraceServiceMapBuilder()

	testCases := []struct {
		dependencyType string
		expected       bool
	}{
		{"messaging_system", true},
		{"db_connection", true},
		{"net_peer", true},
		{"http_external", true},
		{"external_address", true},
		{"ip_address", true},
		{"direct_service", false},
		{"trace_relationship", false},
	}

	for _, tc := range testCases {
		result := builder.isExternalDependency(tc.dependencyType)
		if result != tc.expected {
			t.Errorf("isExternalDependency(%s) = %v, expected %v", tc.dependencyType, result, tc.expected)
		}
	}
}

func TestFormatLinkStatsWithDuration(t *testing.T) {
	builder := NewTraceServiceMapBuilder()

	testCases := []struct {
		callCount       int64
		avgLatencyMs    float64
		durationMinutes float64
		expectedReqRate string
		expectedLatency string
	}{
		{60, 10.5, 30.0, "2.0 req/min", "10.5ms"},  // 60 calls in 30 min = 2 req/min
		{120, 5.0, 60.0, "2.0 req/min", "5.0ms"},   // 120 calls in 60 min = 2 req/min
		{10, 100.0, 5.0, "2.0 req/min", "100.0ms"}, // 10 calls in 5 min = 2 req/min
		{30, 25.0, 10.0, "3.0 req/min", "25.0ms"},  // 30 calls in 10 min = 3 req/min
		{0, 0.0, 30.0, "0 req/min", "0ms"},         // Zero case
		{5, 15.0, 0.0, "5.0 req/min", "15.0ms"},    // Invalid duration - should fallback
	}

	for _, tc := range testCases {
		stats := builder.formatLinkStats(tc.callCount, tc.avgLatencyMs, tc.durationMinutes)

		if len(stats) != 2 {
			t.Errorf("Expected 2 stats items for callCount=%d, durationMinutes=%.1f, got %d",
				tc.callCount, tc.durationMinutes, len(stats))
			continue
		}

		reqStats := stats[0]
		latencyStats := stats[1]

		if reqStats != tc.expectedReqRate {
			t.Errorf("Expected req rate '%s' for %d calls in %.1f minutes, got '%s'",
				tc.expectedReqRate, tc.callCount, tc.durationMinutes, reqStats)
		}

		if latencyStats != tc.expectedLatency {
			t.Errorf("Expected latency '%s' for %.1fms, got '%s'",
				tc.expectedLatency, tc.avgLatencyMs, latencyStats)
		}
	}
}

func TestDetectExternalApplicationTypes(t *testing.T) {
	builder := NewTraceServiceMapBuilder()

	testCases := []struct {
		protocol       string
		dependencyType string
		expected       []string
	}{
		{"POSTGRESQL", "db_connection", []string{"postgres", "database"}},
		{"MYSQL", "db_connection", []string{"mysql", "database"}},
		{"REDIS", "db_connection", []string{"redis", "cache"}},
		{"KAFKA", "messaging_system", []string{"kafka", "messaging"}},
		{"RABBITMQ", "messaging_system", []string{"rabbitmq", "messaging"}},
		{"HTTP", "http_external", []string{"http", "external_api"}},
		{"UNKNOWN_PROTOCOL", "messaging_system", []string{"messaging", "unknown_protocol"}},
		{"UNKNOWN_PROTOCOL", "db_connection", []string{"database", "unknown_protocol"}},
		{"UNKNOWN_PROTOCOL", "net_peer", []string{"external", "unknown_protocol"}},
	}

	for _, tc := range testCases {
		info := &ExternalServiceInfo{
			Protocol:       tc.protocol,
			DependencyType: tc.dependencyType,
		}
		result := builder.detectExternalApplicationTypes(info)
		if !equalStringSlices(result, tc.expected) {
			t.Errorf("detectExternalApplicationTypes(%s, %s) = %v, expected %v",
				tc.protocol, tc.dependencyType, result, tc.expected)
		}
	}
}

// Helper function to compare string slices
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
