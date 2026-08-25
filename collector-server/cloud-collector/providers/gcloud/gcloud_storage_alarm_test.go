package gcloud

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/collector/cloud/providers"
)

// TestMetricLabelClauseNegation proves the "!" convention: a leading "!"
// renders != (quoted for strings, unquoted for numerics); the bare value
// keeps rendering equality exactly as before.
func TestMetricLabelClauseNegation(t *testing.T) {
	assert.Equal(t, `metric.labels.response_code!="OK"`, metricLabelClause("response_code", "!OK"))
	assert.Equal(t, `metric.labels.response_code_class!=500`, metricLabelClause("response_code_class", "!500"))
	// equality forms unchanged
	assert.Equal(t, `metric.labels.response_code="OK"`, metricLabelClause("response_code", "OK"))
	assert.Equal(t, `metric.labels.response_code_class=500`, metricLabelClause("response_code_class", "500"))
}

func TestLoadStorageAlarmTemplates(t *testing.T) {
	templates, err := LoadGCPAlarmTemplates(ServiceNameStorage)
	require.NoError(t, err)
	require.Len(t, templates, 1)

	tpl := templates[0]
	assert.Equal(t, "gcp_storage_request_errors_alarm_missing", tpl.Name)
	assert.Equal(t, "gcs_bucket", tpl.Configuration.ResourceType)
	assert.Equal(t, map[string]string{"response_code": "!OK"}, tpl.Configuration.MetricLabelFilters)
}

func storageTestBucket(name string) providers.Resource {
	return providers.Resource{
		Id:          name,
		Name:        name,
		Type:        "storage.googleapis.com/Bucket",
		ServiceName: ServiceNameStorage,
		Status:      providers.ResourceStatusActive,
		Region:      "us-central1",
		Tags:        map[string][]string{"team": {"payments"}},
		Meta: map[string]interface{}{
			"StorageClass": "STANDARD",
			"selfLink":     "projects/acme-project/buckets/" + name,
		},
	}
}

// TestStorageAlarmRecommendations proves the wiring end to end: an unmonitored
// bucket gets the error-rate card, and the negated pin rides into alarm_config
// so Create Alert builds the != filter.
func TestStorageAlarmRecommendations(t *testing.T) {
	s := &cloudStorageService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{storageTestBucket("nudgebee-templates")})
	require.NoError(t, err)

	var alarmRecs []providers.Recommendation
	for _, rec := range recs {
		if strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			alarmRecs = append(alarmRecs, rec)
		}
	}
	require.Len(t, alarmRecs, 1)
	assert.Equal(t, "gcp_storage_request_errors_alarm_missing", alarmRecs[0].RuleName)

	cfg, ok := alarmRecs[0].Data["alarm_config"].(providers.AlarmCreationConfig)
	require.True(t, ok)
	assert.Equal(t, map[string]string{"response_code": "!OK"}, cfg.MetricLabelFilters)

	condition, err := buildSimpleCondition(cfg)
	require.NoError(t, err)
	assert.Contains(t, condition.GetConditionThreshold().GetFilter(),
		` AND metric.labels.response_code!="OK"`)
}

// TestStorageAlarmSuppressionSemantics proves the negated pin's matching in
// both directions: an existing not-OK alert suppresses the template, while an
// alert pinned to one specific error code does not (it watches a fraction of
// the failures, not the error rate).
func TestStorageAlarmSuppressionSemantics(t *testing.T) {
	s := &cloudStorageService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	policyWithFilter := func(filter string) []interface{} {
		return []interface{}{map[string]interface{}{
			"displayName": "existing",
			"conditions": []interface{}{map[string]interface{}{
				"displayName": "cond",
				"conditionThreshold": map[string]interface{}{
					"filter": filter, "thresholdValue": 5,
				},
			}},
		}}
	}
	base := `resource.type="gcs_bucket" AND resource.labels.bucket_name="nudgebee-templates" AND metric.type="storage.googleapis.com/api/request_count"`

	countAlarmCards := func(r providers.Resource) int {
		recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
			providers.ListRecommendationsRequest{}, []providers.Resource{r})
		require.NoError(t, err)
		n := 0
		for _, rec := range recs {
			if strings.HasSuffix(rec.RuleName, "_alarm_missing") {
				n++
			}
		}
		return n
	}

	// Existing not-OK alert (GCP-native form) → suppressed
	covered := storageTestBucket("nudgebee-templates")
	covered.Meta["AlertPolicies"] = policyWithFilter(base + ` AND metric.labels.response_code!="OK"`)
	assert.Equal(t, 0, countAlarmCards(covered), "a not-OK alert satisfies the error-rate template")

	// Alert pinned to a single error code → still recommended
	partial := storageTestBucket("nudgebee-templates")
	partial.Meta["AlertPolicies"] = policyWithFilter(base + ` AND metric.labels.response_code="NOT_FOUND"`)
	assert.Equal(t, 1, countAlarmCards(partial), "a single-code alert must not satisfy the full error-rate template")
}
