package gcloud

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/collector/cloud/providers"
)

func TestLoadFunctionsAlarmTemplates(t *testing.T) {
	templates, err := LoadGCPAlarmTemplates(ServiceNameFunctions)
	require.NoError(t, err)
	require.Len(t, templates, 3)

	pinned := map[string]string{} // template name -> pinned status value
	for _, tpl := range templates {
		assert.NotEmpty(t, tpl.Configuration.MetricTypeFilter, "template %s", tpl.Name)
		assert.Equal(t, "cloud_function", tpl.Configuration.ResourceType, "template %s", tpl.Name)
		if v, ok := tpl.Configuration.MetricLabelFilters["status"]; ok {
			pinned[tpl.Name] = v
		}
	}

	// Error-rate and OOM pin the status label; execution-time does not
	assert.Equal(t, map[string]string{
		"gcp_function_error_rate_alarm_missing": "error",
		"gcp_function_oom_alarm_missing":        "out of memory", // exact value, spaces included
	}, pinned)
}

func functionsTestResource(name string) providers.Resource {
	return providers.Resource{
		Id:          name,
		Name:        name,
		Type:        "cloudfunctions.googleapis.com/Function",
		ServiceName: ServiceNameFunctions,
		Status:      providers.ResourceStatusActive,
		Region:      "us-central1",
		Tags:        map[string][]string{"team": {"payments"}},
		Meta: map[string]interface{}{
			"runtime":     "go122",
			"environment": "GEN_2",
			"memory":      "256M",
			"selfLink":    "projects/acme-project/locations/us-central1/functions/" + name,
		},
	}
}

// TestFunctionsAlarmRecommendations proves all three templates fire for an
// unmonitored function and the pinned metric labels ride into alarm_config.
func TestFunctionsAlarmRecommendations(t *testing.T) {
	s := &cloudFunctionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{functionsTestResource("resize-images")})
	require.NoError(t, err)

	var rules []string
	for _, rec := range recs {
		if !strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			continue
		}
		rules = append(rules, rec.RuleName)

		cfg, ok := rec.Data["alarm_config"].(providers.AlarmCreationConfig)
		require.True(t, ok, "rule %s", rec.RuleName)
		if rec.RuleName == "gcp_function_error_rate_alarm_missing" {
			assert.Equal(t, map[string]string{"status": "error"}, cfg.MetricLabelFilters,
				"error-rate card must carry the pinned status label into Create Alert")
		}
	}

	assert.ElementsMatch(t, []string{
		"gcp_function_error_rate_alarm_missing",
		"gcp_function_execution_time_alarm_missing",
		"gcp_function_oom_alarm_missing",
	}, rules)
}

// TestFunctionsPinnedTemplatesNotSuppressedByPlainAlert proves the checker
// semantics on shared metrics: a plain execution_count alert (no status pin)
// suppresses NEITHER the error-rate nor the OOM template, while an
// error-scoped alert suppresses only the error-rate template.
func TestFunctionsPinnedTemplatesNotSuppressedByPlainAlert(t *testing.T) {
	s := &cloudFunctionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	policyWithFilter := func(filter string) []interface{} {
		return []interface{}{map[string]interface{}{
			"displayName": "existing",
			"conditions": []interface{}{map[string]interface{}{
				"displayName": "cond",
				"conditionThreshold": map[string]interface{}{
					"filter": filter, "thresholdValue": 1,
				},
			}},
		}}
	}
	base := `resource.type="cloud_function" AND resource.labels.function_name="resize-images" AND metric.type="cloudfunctions.googleapis.com/function/execution_count"`

	// Plain execution_count alert → both pinned templates still fire
	plain := functionsTestResource("resize-images")
	plain.Meta["AlertPolicies"] = policyWithFilter(base)
	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{plain})
	require.NoError(t, err)
	var rules []string
	for _, rec := range recs {
		if strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			rules = append(rules, rec.RuleName)
		}
	}
	assert.Contains(t, rules, "gcp_function_error_rate_alarm_missing")
	assert.Contains(t, rules, "gcp_function_oom_alarm_missing")

	// Error-scoped alert → only the error-rate template is suppressed
	scoped := functionsTestResource("resize-images")
	scoped.Meta["AlertPolicies"] = policyWithFilter(base + ` AND metric.labels.status="error"`)
	recs, err = s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{scoped})
	require.NoError(t, err)
	rules = nil
	for _, rec := range recs {
		if strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			rules = append(rules, rec.RuleName)
		}
	}
	assert.NotContains(t, rules, "gcp_function_error_rate_alarm_missing",
		"error-scoped alert satisfies the error-rate template")
	assert.Contains(t, rules, "gcp_function_oom_alarm_missing",
		"error-scoped alert must not satisfy the OOM template")
}
