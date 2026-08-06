package gcloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/collector/cloud/providers"
)

// TestBuildSimpleConditionMetricLabelFilters proves the creator appends pinned
// metric-label filters (template constants like response_code_class="5xx")
// after the per-resource dimensions, in deterministic sorted order.
func TestBuildSimpleConditionMetricLabelFilters(t *testing.T) {
	config := providers.AlarmCreationConfig{
		AlarmName:          "gcp_run_error_rate_alarm_missing-payments-api",
		MetricName:         "RequestErrorCount",
		MetricTypeFilter:   "run.googleapis.com/request_count",
		ResourceType:       "cloud_run_revision",
		Statistic:          "ALIGN_SUM",
		Period:             300,
		EvaluationPeriods:  2,
		ComparisonOperator: "COMPARISON_GT",
		Threshold:          5,
		Dimensions: []providers.AlarmDimension{
			{Name: "service_name", Value: "payments-api"},
		},
		MetricLabelFilters: map[string]string{
			"response_code_class": "5xx",
		},
	}

	condition, err := buildSimpleCondition(config)
	require.NoError(t, err)

	filter := condition.GetConditionThreshold().GetFilter()
	assert.Equal(t,
		`resource.type="cloud_run_revision"`+
			` AND metric.type="run.googleapis.com/request_count"`+
			` AND resource.labels.service_name="payments-api"`+
			` AND metric.labels.response_code_class="5xx"`,
		filter)
}

// TestMatchConditionRequiresPinnedMetricLabels proves a broader alert on the
// same metric does NOT satisfy a template that pins metric labels: a plain
// request_count alert must not suppress the 5xx-only recommendation, while a
// 5xx-scoped alert must.
func TestMatchConditionRequiresPinnedMetricLabels(t *testing.T) {
	template := providers.AlarmTemplate{
		Name: "gcp_run_error_rate_alarm_missing",
		Configuration: providers.AlarmConfiguration{
			Namespace:        "run.googleapis.com",
			MetricName:       "RequestErrorCount",
			MetricTypeFilter: "run.googleapis.com/request_count",
			ResourceType:     "cloud_run_revision",
			MetricLabelFilters: map[string]string{
				"response_code_class": "5xx",
			},
		},
	}

	policyWithFilter := func(filter string) map[string]interface{} {
		return map[string]interface{}{
			"displayName": "existing alert",
			"conditions": []interface{}{
				map[string]interface{}{
					"displayName": "cond",
					"conditionThreshold": map[string]interface{}{
						"filter":         filter,
						"thresholdValue": 5,
					},
				},
			},
		}
	}

	resourceFilter := GetResourceFilterForService(ServiceNameRun, "payments-api")

	// Broad alert (no 5xx pin) → template still missing
	broad := providers.Resource{
		Id: "payments-api", ServiceName: ServiceNameRun,
		Meta: map[string]interface{}{"AlertPolicies": []interface{}{policyWithFilter(
			`resource.type="cloud_run_revision" AND resource.labels.service_name="payments-api" AND metric.type="run.googleapis.com/request_count"`,
		)}},
	}
	missing, err := IsAlarmMissing(broad, template, resourceFilter)
	require.NoError(t, err)
	assert.True(t, missing, "a plain request_count alert must not suppress the 5xx-only template")

	// 5xx-scoped alert → template satisfied
	scoped := providers.Resource{
		Id: "payments-api", ServiceName: ServiceNameRun,
		Meta: map[string]interface{}{"AlertPolicies": []interface{}{policyWithFilter(
			`resource.type="cloud_run_revision" AND resource.labels.service_name="payments-api" AND metric.type="run.googleapis.com/request_count" AND metric.labels.response_code_class="5xx"`,
		)}},
	}
	missing, err = IsAlarmMissing(scoped, template, resourceFilter)
	require.NoError(t, err)
	assert.False(t, missing, "a 5xx-scoped alert satisfies the 5xx-only template")
}

// TestBuildGCPAlarmConfigCarriesMetricLabelFilters proves the template's pinned
// labels travel into the stored alarm config (what Create Alert later uses).
func TestBuildGCPAlarmConfigCarriesMetricLabelFilters(t *testing.T) {
	template := providers.AlarmTemplate{
		Name: "gcp_run_error_rate_alarm_missing",
		Configuration: providers.AlarmConfiguration{
			MetricName:         "RequestErrorCount",
			MetricTypeFilter:   "run.googleapis.com/request_count",
			ResourceType:       "cloud_run_revision",
			MetricLabelFilters: map[string]string{"response_code_class": "5xx"},
		},
	}
	resource := providers.Resource{Id: "payments-api"}

	cfg := buildGCPAlarmConfig(resource, template, 5, []providers.AlarmDimension{
		{Name: "service_name", Value: "payments-api"},
	})

	assert.Equal(t, map[string]string{"response_code_class": "5xx"}, cfg.MetricLabelFilters)
}
