package gcloud

import (
	"testing"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"

	"nudgebee/collector/cloud/providers"
)

func TestLoadCloudRunAlarmTemplates(t *testing.T) {
	templates, err := LoadGCPAlarmTemplates(ServiceNameRun)
	if err != nil {
		t.Fatalf("LoadGCPAlarmTemplates(%q) error: %v", ServiceNameRun, err)
	}
	if len(templates) != 3 {
		t.Fatalf("expected 3 Cloud Run alarm templates, got %d", len(templates))
	}

	for _, tpl := range templates {
		if tpl.Configuration.MetricTypeFilter == "" {
			t.Errorf("template %s: metric_type_filter is required for GCP templates", tpl.Name)
		}
		if tpl.Configuration.ResourceType != "cloud_run_revision" {
			t.Errorf("template %s: resource_type = %q, want cloud_run_revision", tpl.Name, tpl.Configuration.ResourceType)
		}
	}
}

func TestCalculateGCPRunThreshold(t *testing.T) {
	ceilingTemplate := providers.AlarmTemplate{
		ThresholdRules: providers.ThresholdRules{Default: 80, DefaultPercentage: 0.80},
	}
	staticTemplate := providers.AlarmTemplate{
		ThresholdRules: providers.ThresholdRules{Default: 1000},
	}

	cases := []struct {
		name     string
		template providers.AlarmTemplate
		meta     map[string]interface{}
		want     float64
	}{
		// int32 arrives fresh from the GCP SDK (runServiceToResource)
		{"percentage of int32 max_instances", ceilingTemplate, map[string]interface{}{"max_instances": int32(10)}, 8},
		// float64 arrives after a JSON round-trip through the resource store
		{"percentage of float64 max_instances", ceilingTemplate, map[string]interface{}{"max_instances": float64(1000)}, 800},
		{"missing max_instances falls back to default", ceilingTemplate, map[string]interface{}{}, 80},
		{"zero max_instances falls back to default", ceilingTemplate, map[string]interface{}{"max_instances": int32(0)}, 80},
		{"static template ignores meta", staticTemplate, map[string]interface{}{"max_instances": int32(10)}, 1000},
	}

	for _, c := range cases {
		resource := providers.Resource{Meta: c.meta}
		if got := calculateGCPRunThreshold(resource, c.template); got != c.want {
			t.Errorf("%s: calculateGCPRunThreshold = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestIsAlarmMissingPrefersMetricTypeFilter proves the checker matches a
// customer's existing alert via the template's metric_type_filter. The short
// display label ("RequestLatency") is not resolvable by the legacy hardcoded
// mapping, so before the fix this template reported the alarm as missing even
// though a matching policy exists — a false-positive recommendation.
func TestIsAlarmMissingPrefersMetricTypeFilter(t *testing.T) {
	template := providers.AlarmTemplate{
		Name: "gcp_run_request_latency_alarm_missing",
		Configuration: providers.AlarmConfiguration{
			Namespace:        "run.googleapis.com",
			MetricName:       "RequestLatency",
			MetricTypeFilter: "run.googleapis.com/request_latencies",
			ResourceType:     "cloud_run_revision",
		},
	}

	existingPolicy := map[string]interface{}{
		"displayName": "payments-api p99 latency",
		"conditions": []interface{}{
			map[string]interface{}{
				"displayName": "latency above 1s",
				"conditionThreshold": map[string]interface{}{
					"filter":         `resource.type="cloud_run_revision" AND resource.labels.service_name="payments-api" AND metric.type="run.googleapis.com/request_latencies"`,
					"thresholdValue": 1000,
				},
			},
		},
	}

	resource := providers.Resource{
		Id:          "payments-api",
		ServiceName: ServiceNameRun,
		Meta:        map[string]interface{}{"AlertPolicies": []interface{}{existingPolicy}},
	}

	resourceFilter := GetResourceFilterForService(ServiceNameRun, resource.Id)
	missing, err := IsAlarmMissing(resource, template, resourceFilter)
	if err != nil {
		t.Fatalf("IsAlarmMissing error: %v", err)
	}
	if missing {
		t.Errorf("IsAlarmMissing = true, want false: existing policy matches the template's metric_type_filter")
	}

	// A different service's policy must not suppress the recommendation
	otherFilter := GetResourceFilterForService(ServiceNameRun, "other-service")
	missing, err = IsAlarmMissing(resource, template, otherFilter)
	if err != nil {
		t.Fatalf("IsAlarmMissing error: %v", err)
	}
	if !missing {
		t.Errorf("IsAlarmMissing = false, want true: policy is scoped to a different service")
	}
}

// TestConvertStatisticToAlignerNativeNames ensures templates can carry native
// Cloud Monitoring aligner names. Distribution metrics (request_latencies,
// memory/utilizations) are invalid with ALIGN_MEAN, so silently degrading
// ALIGN_PERCENTILE_99 to the default would break alert creation.
func TestConvertStatisticToAlignerNativeNames(t *testing.T) {
	cases := []struct {
		statistic string
		want      monitoringpb.Aggregation_Aligner
	}{
		{"ALIGN_PERCENTILE_99", monitoringpb.Aggregation_ALIGN_PERCENTILE_99},
		{"ALIGN_MAX", monitoringpb.Aggregation_ALIGN_MAX},
		{"ALIGN_MEAN", monitoringpb.Aggregation_ALIGN_MEAN},
		// legacy keyword mapping still works
		{"average", monitoringpb.Aggregation_ALIGN_MEAN},
		{"max", monitoringpb.Aggregation_ALIGN_MAX},
		{"unknown", monitoringpb.Aggregation_ALIGN_MEAN},
	}
	for _, c := range cases {
		if got := convertStatisticToAligner(c.statistic); got != c.want {
			t.Errorf("convertStatisticToAligner(%q) = %v, want %v", c.statistic, got, c.want)
		}
	}
}

// TestConvertComparisonOperatorNativeNames ensures templates can carry native
// COMPARISON_* names. Before, everything outside the AWS-style keywords fell
// through to COMPARISON_GT — silently inverting less-than templates (e.g. the
// Compute Engine uptime check, which alerts when checks passed drops BELOW 1).
func TestConvertComparisonOperatorNativeNames(t *testing.T) {
	cases := []struct {
		operator string
		want     monitoringpb.ComparisonType
	}{
		{"COMPARISON_GT", monitoringpb.ComparisonType_COMPARISON_GT},
		{"COMPARISON_LT", monitoringpb.ComparisonType_COMPARISON_LT},
		{"COMPARISON_GE", monitoringpb.ComparisonType_COMPARISON_GE},
		// legacy keyword mapping still works
		{"GreaterThanThreshold", monitoringpb.ComparisonType_COMPARISON_GT},
		{"LessThanThreshold", monitoringpb.ComparisonType_COMPARISON_LT},
		{"unknown", monitoringpb.ComparisonType_COMPARISON_GT},
	}
	for _, c := range cases {
		if got := convertComparisonOperator(c.operator); got != c.want {
			t.Errorf("convertComparisonOperator(%q) = %v, want %v", c.operator, got, c.want)
		}
	}
}
