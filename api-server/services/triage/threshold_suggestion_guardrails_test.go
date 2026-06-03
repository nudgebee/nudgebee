package triage

import (
	"testing"
)

func TestDetectMetricHints_PercentageMetric(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName: "100 * (count by(cluster, job, namespace, service) (up == 0) / count by(cluster, job, namespace, service) (up))",
		Operator:   ">",
	}
	hint := detectMetricHints(alertDef)
	if !hint.isBounded {
		t.Error("expected percentage metric to be bounded")
	}
	if hint.upperBound != 100 {
		t.Errorf("expected upper bound 100, got %f", hint.upperBound)
	}
	if hint.metricCategory != "percentage" {
		t.Errorf("expected category 'percentage', got %q", hint.metricCategory)
	}
}

func TestDetectMetricHints_TimeSinceMetric(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "time() - max by(namespace, job_name, cluster) (kube_job_status_start_time{job=\"kube-state-metrics\"})",
		Operator:         ">",
		CurrentThreshold: 43200,
	}
	hint := detectMetricHints(alertDef)
	if !hint.isMonotonic {
		t.Error("expected time-since metric to be monotonic")
	}
	if hint.metricCategory != "time_since" {
		t.Errorf("expected category 'time_since', got %q", hint.metricCategory)
	}
}

func TestDetectMetricHints_BooleanMetric(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "kube_horizontalpodautoscaler_status_current_replicas{job=\"kube-state-metrics\"} == kube_horizontalpodautoscaler_spec_max_replicas",
		Operator:         ">",
		CurrentThreshold: 1,
	}
	hint := detectMetricHints(alertDef)
	if !hint.isBoolean {
		t.Error("expected boolean metric to be detected")
	}
}

func TestCheckThresholdEligibility_SensitivityAlert(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "increase(vm_promscrape_scrapes_failed_total[5m])",
		Operator:         ">",
		CurrentThreshold: 0,
	}
	reason := checkThresholdEligibility(alertDef)
	if reason == "" {
		t.Error("expected sensitivity alert (threshold=0, operator=>) to be ineligible")
	}
}

func TestCheckThresholdEligibility_EqualityOperator(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "kube_deployment_status_replicas",
		Operator:         "==",
		CurrentThreshold: 0,
	}
	reason := checkThresholdEligibility(alertDef)
	if reason == "" {
		t.Error("expected equality operator alert to be ineligible")
	}
}

func TestCheckThresholdEligibility_AbsentMetric(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "absent(up{job=\"prometheus\"})",
		Operator:         ">",
		CurrentThreshold: 0,
	}
	reason := checkThresholdEligibility(alertDef)
	if reason == "" {
		t.Error("expected absent() alert to be ineligible")
	}
}

func TestCheckThresholdEligibility_EligibleAlert(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "rate(node_cpu_seconds_total[5m])",
		Operator:         ">",
		CurrentThreshold: 80,
	}
	reason := checkThresholdEligibility(alertDef)
	if reason != "" {
		t.Errorf("expected eligible alert to have no reason, got %q", reason)
	}
}

func TestValidateSuggestion_CapsPercentageMetric(t *testing.T) {
	// TargetDown: > 10 → > 100 (impossible for percentage metric)
	alertDef := &AlertDefinition{
		MetricName:       "100 * (count by(cluster, job) (up == 0) / count by(cluster, job) (up))",
		Operator:         ">",
		CurrentThreshold: 10,
	}
	suggestion := &ThresholdSuggestion{
		SuggestedThreshold: 100,
		EstimatedReduction: 100,
		Confidence:         "medium",
		RecommendationType: "tune_threshold",
	}
	risk := ValidateSuggestion(suggestion, alertDef, nil)
	if risk.Level != "dangerous" {
		t.Errorf("expected risk level 'dangerous', got %q", risk.Level)
	}
	if suggestion.SuggestedThreshold != 95 {
		t.Errorf("expected threshold to be capped at 95, got %f", suggestion.SuggestedThreshold)
	}
	if suggestion.Confidence != "low" {
		t.Errorf("expected confidence downgraded to 'low', got %q", suggestion.Confidence)
	}
	if len(risk.Warnings) == 0 {
		t.Error("expected at least one warning")
	}
}

func TestValidateSuggestion_MonotonicTimeMetric(t *testing.T) {
	// KubeJobNotCompleted: > 43200 → > 251929 (5.8x for a time metric)
	alertDef := &AlertDefinition{
		MetricName:       "time() - max by(namespace, job_name) (kube_job_status_start_time)",
		Operator:         ">",
		CurrentThreshold: 43200,
	}
	suggestion := &ThresholdSuggestion{
		SuggestedThreshold: 251929,
		EstimatedReduction: 100,
		Confidence:         "medium",
		RecommendationType: "tune_threshold",
	}
	risk := ValidateSuggestion(suggestion, alertDef, nil)
	if risk.Level == "safe" {
		t.Error("expected risk level to NOT be 'safe' for 5.8x time metric change")
	}
	if suggestion.Confidence == "high" {
		t.Error("confidence should not be 'high' for a flagged time-since metric")
	}
}

func TestValidateSuggestion_BooleanEquality(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "replicas == max_replicas",
		Operator:         ">",
		CurrentThreshold: 1,
	}
	suggestion := &ThresholdSuggestion{
		SuggestedThreshold: 5.5,
		Confidence:         "medium",
		RecommendationType: "tune_threshold",
	}
	risk := ValidateSuggestion(suggestion, alertDef, nil)
	if risk.Level == "safe" {
		t.Error("expected risk to flag boolean equality metric")
	}
	if len(risk.Warnings) == 0 {
		t.Error("expected warnings for boolean metric")
	}
}

func TestValidateSuggestion_EffectiveSilencing(t *testing.T) {
	alertDef := &AlertDefinition{
		MetricName:       "some_gauge",
		Operator:         ">",
		CurrentThreshold: 10,
	}
	suggestion := &ThresholdSuggestion{
		SuggestedThreshold: 50,
		EstimatedReduction: 100,
		Confidence:         "high",
		RecommendationType: "tune_threshold",
	}
	// With engaged alerts
	quality := &AlertQualityScore{
		EngagementRate: 0.5,
	}
	risk := ValidateSuggestion(suggestion, alertDef, quality)
	if risk.Level != "dangerous" {
		t.Errorf("expected 'dangerous' for 100%% reduction on engaged alerts, got %q", risk.Level)
	}
}

func TestValidateSuggestion_SafeSuggestion(t *testing.T) {
	// NodeDiskIOSaturation: > 10 → > 17.58 (reasonable 1.7x for a gauge metric)
	alertDef := &AlertDefinition{
		MetricName:       "rate(node_disk_io_time_weighted_seconds_total[5m])",
		Operator:         ">",
		CurrentThreshold: 10,
	}
	suggestion := &ThresholdSuggestion{
		SuggestedThreshold: 17.58,
		EstimatedReduction: 67,
		Confidence:         "high",
		RecommendationType: "tune_threshold",
	}
	risk := ValidateSuggestion(suggestion, alertDef, nil)
	if risk.Level != "safe" {
		t.Errorf("expected 'safe' for reasonable suggestion, got %q", risk.Level)
	}
	if suggestion.Confidence != "high" {
		t.Errorf("confidence should remain 'high' for safe suggestion, got %q", suggestion.Confidence)
	}
}

func TestFormatHumanDuration(t *testing.T) {
	tests := []struct {
		seconds  float64
		expected string
	}{
		{30, "30s"},
		{120, "2m"},
		{3600, "1.0h"},
		{43200, "12.0h"},
		{251929, "2.9 days"},
	}
	for _, tt := range tests {
		got := formatHumanDuration(tt.seconds)
		if got != tt.expected {
			t.Errorf("formatHumanDuration(%f) = %q, want %q", tt.seconds, got, tt.expected)
		}
	}
}
