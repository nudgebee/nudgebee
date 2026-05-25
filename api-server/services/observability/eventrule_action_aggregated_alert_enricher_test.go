package observability

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestCtx() playbooks.PlaybookActionContext {
	return playbooks.NewPlaybookActionContext("test-tenant", "test-account", slog.Default(), playbooks.PlaybookEvent{})
}

func TestPickTopSeriesMetric_FlatArray(t *testing.T) {
	// Actual relay response format: ExecuteAndExtractResponse parses evidence data
	// into a flat array under "A", not wrapped in vector_result/series_list_result
	relayResponse := map[string]any{
		"data": `{"A": [{"metric": {"container_id": "/k8s/splunk/splunk-otel-collector-agent-m5r8p/otel-collector", "path": "/{id}", "method": "GET", "status": "404"}, "value": [1772893628, "2"]}, {"metric": {"container_id": "/k8s/splunk/splunk-otel-collector-agent-m5r8p/otel-collector", "path": "/{id}", "method": "GET", "status": "403"}, "value": [1772893628, "4"]}, {"metric": {"container_id": "/k8s/splunk/splunk-otel-collector-agent-m5r8p/otel-collector", "path": "/{id}", "method": "GET", "status": "400"}, "value": [1772893628, "1"]}]}`,
	}

	result := pickTopSeriesMetric(relayResponse, newTestCtx())
	assert.NotNil(t, result)
	// Should pick the series with highest value (4)
	assert.Equal(t, "/k8s/splunk/splunk-otel-collector-agent-m5r8p/otel-collector", result["container_id"])
	assert.Equal(t, "/{id}", result["path"])
	assert.Equal(t, "GET", result["method"])
	assert.Equal(t, "403", result["status"])
}

func TestPickTopSeriesMetric_VectorResult(t *testing.T) {
	// Alternative format: data["A"] is a map with vector_result key
	relayResponse := map[string]any{
		"data": map[string]any{
			"A": map[string]any{
				"result_type": "vector",
				"vector_result": []any{
					map[string]any{
						"metric": map[string]any{
							"container_id": "/k8s/app-162/payments-service-7bc6fbbc9b-m5j56/payments-service",
							"path":         "/api/checkout",
							"method":       "POST",
							"status":       "500",
						},
						"value": []any{1709827200.0, "5"},
					},
					map[string]any{
						"metric": map[string]any{
							"container_id": "/k8s/app-162/orders-service-abc123/orders-service",
							"path":         "/api/orders",
							"method":       "GET",
							"status":       "503",
						},
						"value": []any{1709827200.0, "12"},
					},
					map[string]any{
						"metric": map[string]any{
							"container_id": "/k8s/app-162/auth-service-def456/auth-service",
							"path":         "/api/login",
							"method":       "POST",
							"status":       "401",
						},
						"value": []any{1709827200.0, "3"},
					},
				},
			},
		},
	}

	result := pickTopSeriesMetric(relayResponse, newTestCtx())
	assert.NotNil(t, result)
	// Should pick the series with highest value (12)
	assert.Equal(t, "/k8s/app-162/orders-service-abc123/orders-service", result["container_id"])
	assert.Equal(t, "/api/orders", result["path"])
	assert.Equal(t, "GET", result["method"])
	assert.Equal(t, "503", result["status"])
}

func TestPickTopSeriesMetric_SeriesListResult(t *testing.T) {
	// Range query response format: series_list_result with separate timestamps/values arrays
	// (relay agent transforms Prometheus matrix into this format)
	relayResponse := map[string]any{
		"data": map[string]any{
			"A": map[string]any{
				"result_type": "matrix",
				"series_list_result": []any{
					map[string]any{
						"metric": map[string]any{
							"container_id": "/k8s/newrelic/newrelic-bundle-nri-kube-events-abc/nri-kube-events",
							"sample":       "ERROR - Health check failed",
						},
						"timestamps": []any{1709827200.0, 1709827260.0},
						"values":     []any{"8", "15"},
					},
					map[string]any{
						"metric": map[string]any{
							"container_id": "/k8s/newrelic/newrelic-infra-abc/infra-agent",
							"sample":       "CRITICAL - Connection refused",
						},
						"timestamps": []any{1709827200.0, 1709827260.0},
						"values":     []any{"2", "4"},
					},
				},
			},
		},
	}

	result := pickTopSeriesMetric(relayResponse, newTestCtx())
	assert.NotNil(t, result)
	// Should pick series with highest last value (15)
	assert.Equal(t, "/k8s/newrelic/newrelic-bundle-nri-kube-events-abc/nri-kube-events", result["container_id"])
	assert.Equal(t, "ERROR - Health check failed", result["sample"])
}

func TestPickTopSeriesMetric_DataAsString(t *testing.T) {
	// Data comes as a JSON string from ExecuteAndExtractResponse (evidence data field)
	relayResponse := map[string]any{
		"data": `{"A":[{"metric":{"container_id":"/k8s/test/pod/container","path":"/health","status":"404"},"value":[1709827200,"7"]}]}`,
	}

	result := pickTopSeriesMetric(relayResponse, newTestCtx())
	assert.NotNil(t, result)
	assert.Equal(t, "/k8s/test/pod/container", result["container_id"])
	assert.Equal(t, "/health", result["path"])
}

func TestPickTopSeriesMetric_EmptyResults(t *testing.T) {
	tests := []struct {
		name     string
		response map[string]any
	}{
		{"nil data", map[string]any{"data": nil}},
		{"empty data", map[string]any{"data": map[string]any{}}},
		{"no key A", map[string]any{"data": map[string]any{"B": map[string]any{}}}},
		{"empty flat array", map[string]any{"data": map[string]any{"A": []any{}}}},
		{"empty vector_result", map[string]any{"data": map[string]any{"A": map[string]any{"vector_result": []any{}}}}},
		{"empty series_list_result", map[string]any{"data": map[string]any{"A": map[string]any{"series_list_result": []any{}}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pickTopSeriesMetric(tt.response, newTestCtx())
			assert.Nil(t, result)
		})
	}
}

func TestCanAutoExecute(t *testing.T) {
	enricher := &aggregatedAlertLabelEnricher{}

	tests := []struct {
		name     string
		event    playbooks.PlaybookEvent
		expected bool
	}{
		{
			name: "ApplicationAPIFailures with required labels",
			event: playbooks.PlaybookEvent{
				AggregationKey: "ApplicationAPIFailures",
				Labels: map[string]string{
					"destination_workload_name":      "ingress-nginx-controller",
					"destination_workload_namespace": "ingress-nginx",
				},
			},
			expected: true,
		},
		{
			name: "ApplicationAPIFailures missing dest_workload_name",
			event: playbooks.PlaybookEvent{
				AggregationKey: "ApplicationAPIFailures",
				Labels: map[string]string{
					"destination_workload_namespace": "ingress-nginx",
				},
			},
			expected: false,
		},
		{
			name: "HighErrorCriticalLogs with required labels",
			event: playbooks.PlaybookEvent{
				AggregationKey: "HighErrorCriticalLogs",
				Labels: map[string]string{
					"app_id": "/k8s/newrelic/newrelic-bundle-nri-kube-events",
				},
			},
			expected: true,
		},
		{
			name: "HighErrorCriticalLogs missing app_id",
			event: playbooks.PlaybookEvent{
				AggregationKey: "HighErrorCriticalLogs",
				Labels:         map[string]string{},
			},
			expected: false,
		},
		{
			name: "runs even when container_id already set",
			event: playbooks.PlaybookEvent{
				AggregationKey: "ApplicationAPIFailures",
				Labels: map[string]string{
					"destination_workload_name":      "frontend-proxy",
					"destination_workload_namespace": "demo",
					"container_id":                   "/k8s/wrong/container",
				},
			},
			expected: true,
		},
		{
			name: "unsupported aggregation key",
			event: playbooks.PlaybookEvent{
				AggregationKey: "SomeOtherAlert",
				Labels:         map[string]string{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := playbooks.NewPlaybookActionContext("test-tenant", "test-account", slog.Default(), tt.event)
			assert.Equal(t, tt.expected, enricher.CanAutoExecute(ctx))
		})
	}
}

func TestAutoExecute_RealRelayResponse(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("TEST_ACCOUNT not set, skipping integration test")
	}

	enricher := &aggregatedAlertLabelEnricher{}
	ctx := playbooks.NewPlaybookActionContext(
		os.Getenv("TEST_TENANT"),
		os.Getenv("TEST_ACCOUNT"),
		slog.Default(),
		playbooks.PlaybookEvent{
			AggregationKey: "ApplicationAPIFailures",
			Labels: map[string]string{
				"destination_workload_name":      "ingress-nginx-controller",
				"destination_workload_namespace": "ingress-nginx",
			},
		},
	)

	resp, err := enricher.AutoExecute(ctx)
	if err != nil {
		t.Logf("AutoExecute returned error (may be expected if no data): %v", err)
		return
	}
	t.Logf("Response: %+v", resp)
	if labelExtractor, ok := resp.(playbooks.PlaybookActionResponseLabelExtractor); ok {
		t.Logf("Extracted labels: %+v", labelExtractor.ExtractLabels())
	}
}
