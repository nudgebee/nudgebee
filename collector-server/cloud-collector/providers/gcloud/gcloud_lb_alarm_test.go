package gcloud

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/collector/cloud/providers"
)

// TestMetricLabelClauseQuoting proves the rendering rule both ways: INT64-style
// numeric values (LB's response_code_class) render unquoted, string values
// (Functions' status) stay quoted. Creator and checker share this helper, so
// this single rule decides both what we create and what we match.
func TestMetricLabelClauseQuoting(t *testing.T) {
	assert.Equal(t, `metric.labels.response_code_class=500`, metricLabelClause("response_code_class", "500"))
	assert.Equal(t, `metric.labels.status="error"`, metricLabelClause("status", "error"))
	assert.Equal(t, `metric.labels.status="out of memory"`, metricLabelClause("status", "out of memory"))
	// mixed alphanumerics are strings
	assert.Equal(t, `metric.labels.code="5xx"`, metricLabelClause("code", "5xx"))
}

// TestContainsMetricLabelClauseBoundary proves the whole-term check for
// unquoted numeric clauses: =500 inside =5000 must NOT count as a match
// (a substring probe would falsely suppress the recommendation), while
// legitimate placements — end of filter, followed by a space or paren — do.
func TestContainsMetricLabelClauseBoundary(t *testing.T) {
	clause := metricLabelClause("response_code_class", "500") // …=500 unquoted

	assert.False(t, containsMetricLabelClause(
		`metric.type="x" AND metric.labels.response_code_class=5000`, clause),
		"=5000 must not satisfy a =500 pin")
	assert.True(t, containsMetricLabelClause(
		`metric.type="x" AND metric.labels.response_code_class=500`, clause),
		"match at end of filter")
	assert.True(t, containsMetricLabelClause(
		`metric.labels.response_code_class=500 AND resource.type="https_lb_rule"`, clause),
		"match followed by a space")
	// a later legitimate occurrence after an early longer-number decoy
	assert.True(t, containsMetricLabelClause(
		`metric.labels.other=5001 AND metric.labels.response_code_class=500`, clause),
		"scan must continue past a boundary-failing occurrence")

	// quoted clauses are unaffected (closing quote is its own boundary)
	quoted := metricLabelClause("status", "error")
	assert.True(t, containsMetricLabelClause(`metric.labels.status="error"`, quoted))
	assert.False(t, containsMetricLabelClause(`metric.labels.status="error2"`, quoted))
}

func TestLoadLBAlarmTemplates(t *testing.T) {
	templates, err := LoadGCPAlarmTemplates(ServiceNameCloudLoadBalancing)
	require.NoError(t, err)
	require.Len(t, templates, 2)

	for _, tpl := range templates {
		assert.Equal(t, "https_lb_rule", tpl.Configuration.ResourceType, "template %s", tpl.Name)
		assert.NotEmpty(t, tpl.Configuration.MetricTypeFilter, "template %s", tpl.Name)
	}
}

// TestBuildSimpleConditionNumericMetricLabel proves the built alert filter
// carries the numeric class unquoted — a quoted "500" against the INT64 label
// would be rejected by the Monitoring API.
func TestBuildSimpleConditionNumericMetricLabel(t *testing.T) {
	config := providers.AlarmCreationConfig{
		AlarmName:          "gcp_lb_backend_error_rate_alarm_missing-web-rule",
		MetricName:         "BackendErrorResponses",
		MetricTypeFilter:   "loadbalancing.googleapis.com/https/request_count",
		ResourceType:       "https_lb_rule",
		Statistic:          "ALIGN_SUM",
		Period:             300,
		EvaluationPeriods:  2,
		ComparisonOperator: "COMPARISON_GT",
		Threshold:          5,
		Dimensions:         []providers.AlarmDimension{{Name: "forwarding_rule_name", Value: "web-rule"}},
		MetricLabelFilters: map[string]string{"response_code_class": "500"},
	}

	condition, err := buildSimpleCondition(config)
	require.NoError(t, err)
	assert.Contains(t, condition.GetConditionThreshold().GetFilter(),
		` AND metric.labels.response_code_class=500`)
	assert.NotContains(t, condition.GetConditionThreshold().GetFilter(), `"500"`)
}

func lbTestForwardingRule(name, target string) providers.Resource {
	return providers.Resource{
		Id:          "acme-project/global/forwardingRules/" + name,
		Name:        name,
		Type:        "forwarding-rule",
		ServiceName: ServiceNameCloudLoadBalancing,
		Status:      providers.ResourceStatusActive,
		Region:      "global",
		Tags:        map[string][]string{"team": {"payments"}},
		Meta: map[string]interface{}{
			"target":                target,
			"load_balancing_scheme": "EXTERNAL_MANAGED",
		},
	}
}

// TestLBAlarmRecommendations proves the HTTP(S)-proxy gate: rules fronting an
// HTTP(S) proxy get both cards, TCP-proxy rules get none (their series never
// has data on https_lb_rule metrics).
func TestLBAlarmRecommendations(t *testing.T) {
	s := &cloudLoadBalancingService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	httpsRule := lbTestForwardingRule("web-rule",
		"https://www.googleapis.com/compute/v1/projects/acme-project/global/targetHttpsProxies/web-proxy")
	tcpRule := lbTestForwardingRule("tcp-rule",
		"https://www.googleapis.com/compute/v1/projects/acme-project/global/targetTcpProxies/tcp-proxy")

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{httpsRule, tcpRule})
	require.NoError(t, err)

	alarmRules := map[string][]string{}
	for _, rec := range recs {
		if !strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			continue
		}
		name, _ := rec.Data["rule_name"].(string)
		alarmRules[name] = append(alarmRules[name], rec.RuleName)
	}

	assert.ElementsMatch(t, []string{
		"gcp_lb_backend_error_rate_alarm_missing",
		"gcp_lb_backend_latency_alarm_missing",
	}, alarmRules["web-rule"])
	assert.Empty(t, alarmRules["tcp-rule"], "TCP rules must not get https_lb_rule alarm cards")
}

// TestLBAlarmSuppressedByExistingUnquotedAlert proves the checker recognizes an
// existing alert written in GCP's native unquoted-numeric form.
func TestLBAlarmSuppressedByExistingUnquotedAlert(t *testing.T) {
	s := &cloudLoadBalancingService{}
	ctx := providers.NewCloudProviderContext(context.Background())

	rule := lbTestForwardingRule("web-rule",
		"https://www.googleapis.com/compute/v1/projects/acme-project/global/targetHttpsProxies/web-proxy")
	rule.Meta["AlertPolicies"] = []interface{}{
		map[string]interface{}{
			"displayName": "web-rule 5xx",
			"conditions": []interface{}{
				map[string]interface{}{
					"displayName": "server errors",
					"conditionThreshold": map[string]interface{}{
						"filter":         `resource.type="https_lb_rule" AND resource.labels.forwarding_rule_name="web-rule" AND metric.type="loadbalancing.googleapis.com/https/request_count" AND metric.labels.response_code_class=500`,
						"thresholdValue": 5,
					},
				},
			},
		},
	}

	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{rule})
	require.NoError(t, err)

	var rules []string
	for _, rec := range recs {
		if strings.HasSuffix(rec.RuleName, "_alarm_missing") {
			rules = append(rules, rec.RuleName)
		}
	}
	assert.NotContains(t, rules, "gcp_lb_backend_error_rate_alarm_missing",
		"unquoted-numeric existing alert must suppress the error-rate template")
	assert.Contains(t, rules, "gcp_lb_backend_latency_alarm_missing")
}
