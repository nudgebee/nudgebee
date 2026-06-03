package gcloud

import (
	"nudgebee/collector/cloud/providers"
	"testing"
)

func TestListGcloudMonitoringMetrics_ComputeEngine(t *testing.T) {
	resp, err := listGcloudMonitoringMetrics(providers.ListMetricsRequest{
		ServiceName: "Compute Engine",
	})
	if err != nil {
		t.Fatalf("listGcloudMonitoringMetrics() error = %v", err)
	}
	if len(resp.Metrics) == 0 {
		t.Error("expected non-empty metrics for Compute Engine")
	}
	for _, m := range resp.Metrics {
		if m.Name == "" {
			t.Error("metric name should not be empty")
		}
	}
}

func TestListGcloudMonitoringMetrics_CloudSQL(t *testing.T) {
	resp, err := listGcloudMonitoringMetrics(providers.ListMetricsRequest{
		ServiceName: "Cloud SQL",
	})
	if err != nil {
		t.Fatalf("listGcloudMonitoringMetrics() error = %v", err)
	}
	if len(resp.Metrics) == 0 {
		t.Error("expected non-empty metrics for Cloud SQL")
	}
}

func TestListGcloudMonitoringMetrics_UnknownService(t *testing.T) {
	resp, err := listGcloudMonitoringMetrics(providers.ListMetricsRequest{
		ServiceName: "NonExistentService",
	})
	if err != nil {
		t.Fatalf("listGcloudMonitoringMetrics() error = %v", err)
	}
	if len(resp.Metrics) != 0 {
		t.Errorf("expected empty metrics for unknown service, got %d", len(resp.Metrics))
	}
}

func TestListGcloudMonitoringMetrics_CaseInsensitive(t *testing.T) {
	resp1, _ := listGcloudMonitoringMetrics(providers.ListMetricsRequest{ServiceName: "Compute Engine"})
	resp2, _ := listGcloudMonitoringMetrics(providers.ListMetricsRequest{ServiceName: "compute engine"})
	if len(resp1.Metrics) != len(resp2.Metrics) {
		t.Errorf("case sensitivity issue: got %d vs %d metrics", len(resp1.Metrics), len(resp2.Metrics))
	}
}

func TestListGcloudMonitoringMetrics_HasStatistics(t *testing.T) {
	resp, _ := listGcloudMonitoringMetrics(providers.ListMetricsRequest{
		ServiceName: "Compute Engine",
	})
	hasStats := false
	for _, m := range resp.Metrics {
		if len(m.Statistics) > 0 {
			hasStats = true
			break
		}
	}
	if !hasStats {
		t.Error("expected at least some Compute Engine metrics to have statistics")
	}
}
