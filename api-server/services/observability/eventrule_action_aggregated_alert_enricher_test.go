package observability

import (
	"log/slog"
	"nudgebee/services/eventrule/playbooks"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPickTopSeriesMetric_PicksHighestLatestSample(t *testing.T) {
	output := OutputMetricQuery{Results: []QueryResult{{QueryKey: "A", Payload: []Result{
		{Metric: map[string]string{"path": "/{id}", "method": "GET", "status": "404"}, Timestamps: []int64{1, 2}, Values: []float64{9, 2}},
		{Metric: map[string]string{"path": "/{id}", "method": "GET", "status": "403"}, Timestamps: []int64{1, 2}, Values: []float64{1, 4}},
		{Metric: map[string]string{"path": "/{id}", "method": "GET", "status": "400"}, Timestamps: []int64{1}, Values: []float64{1}},
	}}}}
	result := pickTopSeriesMetric(output)
	assert.NotNil(t, result)
	assert.Equal(t, "403", result["status"], "the latest sample decides, not the peak")
	assert.Equal(t, "GET", result["method"])
}

func TestPickTopSeriesMetric_EmptyResults(t *testing.T) {
	assert.Nil(t, pickTopSeriesMetric(OutputMetricQuery{}))
	assert.Nil(t, pickTopSeriesMetric(OutputMetricQuery{Results: []QueryResult{{QueryKey: "A"}}}))
	assert.Nil(t, pickTopSeriesMetric(OutputMetricQuery{Results: []QueryResult{{QueryKey: "A", Payload: []Result{{Metric: map[string]string{"a": "b"}}}}}}))
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
