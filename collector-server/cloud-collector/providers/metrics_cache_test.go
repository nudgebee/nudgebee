package providers

import (
	"testing"
	"time"
)

func TestSetAndGetCachedMetrics(t *testing.T) {
	key := "test:set-get"
	resp := ListMetricsResponse{
		Metrics: []AvailableMetric{
			{Name: "CPUUtilization", Namespace: "AWS/EC2"},
			{Name: "NetworkIn", Namespace: "AWS/EC2"},
		},
	}

	SetCachedMetrics(key, resp)

	cached := GetCachedMetrics(key)
	if cached == nil {
		t.Fatal("expected cached response, got nil")
		return
	}
	if len(cached.Metrics) != 2 {
		t.Errorf("expected 2 metrics, got %d", len(cached.Metrics))
	}
	if cached.Metrics[0].Name != "CPUUtilization" {
		t.Errorf("expected CPUUtilization, got %s", cached.Metrics[0].Name)
	}
}

func TestGetCachedMetrics_Miss(t *testing.T) {
	cached := GetCachedMetrics("test:nonexistent-key")
	if cached != nil {
		t.Errorf("expected nil for missing key, got %v", cached)
	}
}

func TestGetCachedMetrics_Expired(t *testing.T) {
	key := "test:expired"

	// Manually insert an expired entry
	metricsCacheMu.Lock()
	metricsCache[key] = metricsCacheEntry{
		response: ListMetricsResponse{
			Metrics: []AvailableMetric{{Name: "OldMetric"}},
		},
		expiresAt: time.Now().Add(-1 * time.Second),
	}
	metricsCacheMu.Unlock()

	cached := GetCachedMetrics(key)
	if cached != nil {
		t.Errorf("expected nil for expired entry, got %v", cached)
	}
}

func TestSetCachedMetrics_Overwrites(t *testing.T) {
	key := "test:overwrite"

	SetCachedMetrics(key, ListMetricsResponse{
		Metrics: []AvailableMetric{{Name: "First"}},
	})
	SetCachedMetrics(key, ListMetricsResponse{
		Metrics: []AvailableMetric{{Name: "Second"}, {Name: "Third"}},
	})

	cached := GetCachedMetrics(key)
	if cached == nil {
		t.Fatal("expected cached response, got nil")
		return
	}
	if len(cached.Metrics) != 2 {
		t.Errorf("expected 2 metrics after overwrite, got %d", len(cached.Metrics))
	}
	if cached.Metrics[0].Name != "Second" {
		t.Errorf("expected Second, got %s", cached.Metrics[0].Name)
	}
}

func TestCachedMetrics_EmptyResponse(t *testing.T) {
	key := "test:empty"

	SetCachedMetrics(key, ListMetricsResponse{
		Metrics: []AvailableMetric{},
	})

	cached := GetCachedMetrics(key)
	if cached == nil {
		t.Fatal("expected cached response for empty metrics, got nil")
		return
	}
	if len(cached.Metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(cached.Metrics))
	}
}
