package datadog

import (
	"log/slog"
	"nudgebee/collector/otel/metrics"
	"nudgebee/collector/otel/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromQLToDatadogQuery(t *testing.T) {
	// Suppress logger output during tests for cleaner test results
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	// If you need to see debug/info logs from the reader during a specific test,
	// you can change the level or pass a different logger.

	// Mock reader - only logger is used by promQLToDatadogQuery directly
	// If NewReader had complex dependencies, we might need a more involved mock.
	// For now, we can instantiate it if it's simple or just pass the logger.
	// Since NewReader initializes API clients which might try to use env vars,
	// and promQLToDatadogQuery doesn't use those clients, we can create a minimal Reader.
	r := &Reader{
		logger: logger,
	}

	testCases := []struct {
		name                   string
		promqlQuery            string
		expectedDDQuery        string
		expectError            bool
		expectedErrorMsgPrefix string // If expectError is true, check if error message starts with this
	}{
		{
			name:            "Simple metric name",
			promqlQuery:     "kubernetes_cpu_usage_total",
			expectedDDQuery: "kubernetes.cpu.usage.total{*}",
		},
		{
			name:            "Metric with one label",
			promqlQuery:     `kubernetes_memory_usage_bytes{pod_name="my-pod-123"}`,
			expectedDDQuery: `kubernetes.memory.usage.bytes{pod_name:my-pod-123}`,
		},
		{
			name:            "Metric with multiple labels",
			promqlQuery:     `http_requests_total{method="GET",code="200"}`,
			expectedDDQuery: `http.requests.total{method:GET,code:200}`,
		},
		{
			name:            "Metric with not-equal label",
			promqlQuery:     `http_requests_total{method!="POST"}`,
			expectedDDQuery: `http.requests.total{!method:POST}`,
		},
		{
			name:            "Metric with __name__ label",
			promqlQuery:     `{__name__="up", job="node_exporter"}`,
			expectedDDQuery: `up{job:node_exporter}`,
		},
		{
			name:            "Metric name in selector and __name__ label",
			promqlQuery:     `{__name__="my_metric", type="foo"}`, // Correct: __name__ inside, no metric name outside
			expectedDDQuery: `my.metric{type:foo}`,
		},
		{
			name:            "Metric name in selector and conflicting __name__ label (selector name preferred)",
			promqlQuery:     `my_explicit_metric{type="foo"}`, // Correct: explicit name outside, no __name__ inside
			expectedDDQuery: `my.explicit.metric{type:foo}`,
		},
		{
			name:            "Metric with regex label (simplified translation)",
			promqlQuery:     `http_requests_total{path=~"/api/v1/.*"}`,
			expectedDDQuery: `http.requests.total{path:/api/v1/.*}`, // Current simplified translation
		},
		{
			name:            "Metric with not-regex label (simplified translation)",
			promqlQuery:     `http_requests_total{path!~"/health"}`,
			expectedDDQuery: `http.requests.total{!path:/health}`, // Current simplified translation
		},
		{
			name:            "Sum aggregation",
			promqlQuery:     `sum(kubernetes_cpu_usage_total)`,
			expectedDDQuery: `sum:kubernetes.cpu.usage.total{*}`,
		},
		{
			name:            "Sum aggregation with labels",
			promqlQuery:     `sum(kubernetes_cpu_usage_total{node="node-a"})`,
			expectedDDQuery: `sum:kubernetes.cpu.usage.total{node:node-a}`,
		},
		{
			name:            "Sum aggregation with by clause",
			promqlQuery:     `sum by (pod_name) (kubernetes_cpu_usage_total{namespace="prod"})`,
			expectedDDQuery: `sum:kubernetes.cpu.usage.total{namespace:prod} by {pod_name}`,
		},
		{
			name:                   "Unsupported aggregation (count)",
			promqlQuery:            `count(http_requests_total)`,
			expectError:            true,
			expectedErrorMsgPrefix: "unsupported aggregation operator: count",
		},
		{
			name:                   "Unsupported function (rate)",
			promqlQuery:            `rate(http_requests_total[5m])`,
			expectError:            true,
			expectedErrorMsgPrefix: "unsupported PromQL function: rate",
		},
		{
			name:                   "Invalid PromQL syntax",
			promqlQuery:            `http_requests_total{`,
			expectError:            true,
			expectedErrorMsgPrefix: "failed to parse PromQL query",
		},
		{
			name:                   "Query without metric name (only labels)",
			promqlQuery:            `{job="node_exporter"}`,
			expectError:            true,
			expectedErrorMsgPrefix: "could not extract metric name from PromQL query",
		},
		{
			name:                   "Query with unsupported matcher type (placeholder for future)",
			promqlQuery:            `http_requests_total{job*="app"}`, // Assuming '*' is an unsupported type for now
			expectError:            true,                              // This will fail parsing first
			expectedErrorMsgPrefix: "failed to parse PromQL query",    // because `job*="app"` is not valid PromQL
		},
		{
			name:            "Avg aggregation with by clause and multiple labels",
			promqlQuery:     `avg by (cluster, namespace) (container_memory_usage_bytes{container!="", image!=""})`,
			expectedDDQuery: `avg:container.memory.usage.bytes{!container:,!image:} by {cluster,namespace}`,
		},
		{
			name:            "Max aggregation",
			promqlQuery:     `max(process_resident_memory_bytes)`,
			expectedDDQuery: `max:process.resident.memory.bytes{*}`,
		},
		{
			name:            "Min aggregation with labels",
			promqlQuery:     `min(go_goroutines{job="my_app"})`,
			expectedDDQuery: `min:go.goroutines{job:my_app}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ddQuery, _, err := r.promQLToDatadogQuery(tc.promqlQuery)

			if tc.expectError {
				require.Error(t, err, "Expected an error for PromQL: %s", tc.promqlQuery)
				if tc.expectedErrorMsgPrefix != "" {
					assert.True(t, strings.HasPrefix(err.Error(), tc.expectedErrorMsgPrefix),
						"Error message prefix mismatch. Expected prefix: '%s', Got: '%s'", tc.expectedErrorMsgPrefix, err.Error())
				}
			} else {
				require.NoError(t, err, "Did not expect an error for PromQL: %s", tc.promqlQuery)
				assert.Equal(t, tc.expectedDDQuery, ddQuery, "Translated Datadog query mismatch for PromQL: %s", tc.promqlQuery)
			}
		})
	}
}

func TestPromQLToDatadogQueryExecute(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	r, err := NewReader(logger, os.Getenv("DD_API_KEY"), os.Getenv("DD_APP_KEY"), os.Getenv("DD_SITE"))
	assert.Nil(t, err)

	testCases := []struct {
		name                   string
		promqlQuery            string
		expectedDDQuery        string
		expectError            bool
		expectedErrorMsgPrefix string // If expectError is true, check if error message starts with this
	}{
		{
			name:        "Simple metric name",
			promqlQuery: "kubernetes_cpu_usage_total",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			response := r.Query(security.Account{}, logger, metrics.QueryParams{
				Query: tc.promqlQuery,
			})
			assert.Equal(t, response.Error, nil)
		})
	}
}
