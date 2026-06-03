package eventrule

import (
	"context"
	"fmt"
	"log/slog"
	_ "nudgebee/services/cloud"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAnomoly(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Account)
	err := refreshAgentPlaybooks(context.Background(), slog.Default(), m[testenv.Account])
	assert.NoError(t, err)
}

func TestTemplateEvaluation(t *testing.T) {
	response, err := evaluateRawParamsTemplates(map[string]any{
		"query": `(rate(java_http_server_duration_milliseconds_count{http_status_code=~"^[4].*",environment=~"{{alert.labels['environment']}}",job=~"{{alert.labels['job']}}"}[5m]))`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"alertname":   "HighFileSystemUtilizationNbDev",
			"environment": "prod",
			"job":         "prometheus",
		},
	}, map[string]any{}, map[string]map[string]any{})

	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `(rate(java_http_server_duration_milliseconds_count{http_status_code=~"^[4].*",environment=~"prod",job=~"prometheus"}[5m]))`,
	}, response)

	response, err = evaluateRawParamsTemplates(map[string]any{
		"query": `{{ labels.device }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"device": "prometheus",
		},
	}, map[string]any{}, map[string]map[string]any{})

	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `prometheus`,
	}, response)
}

func TestTemplateEvaluation2(t *testing.T) {
	response, err := evaluateRawParamsTemplates(map[string]any{
		"query": `'k' in {{ alert.labels['job'] }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"alertname":   "HighFileSystemUtilizationNbDev",
			"environment": "prod",
			"job":         "prometheus",
			"k":           "abc",
		},
	}, map[string]any{}, map[string]map[string]any{})

	assert.NoError(t, err)
	assert.NotEmpty(t, response)

	response, err = evaluateRawParamsTemplates(map[string]any{
		"query": `{{ not ('k' in alert.labels['job']) }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"alertname":   "HighFileSystemUtilizationNbDev",
			"environment": "prod",
			"job":         "prometheus",
		},
	}, map[string]any{}, map[string]map[string]any{})

	assert.NoError(t, err)
	assert.NotEmpty(t, response)
}

func TestExecutePlaybook(t *testing.T) {
	testenv.RequireEnv(t, testenv.Account)
	response, err := ExecutePlaybook(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), os.Getenv("TEST_ACCOUNT"), playbooks.PlaybookEvent{
		Name: "HighFileSystemUtilizationNbDev",
		Labels: map[string]string{
			"alertname":  "HighFileSystemUtilizationNbDev",
			"device":     "/dev/vda1",
			"host_name":  "nb-dev-db",
			"instance":   "nb-dev-db",
			"mode":       "rw",
			"mountpoint": "/",
			"node_name":  "nb-dev-db",
			"os_type":    "linux",
			"type":       "ext4",
		},
		Annotations: map[string]string{},
		StartedAt:   nil,
		EndedAt:     nil,
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestExecutePlaybookForPagerDuty(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Account)
	start := time.UnixMilli(1755047287955)
	end := time.UnixMilli(1755050887955)
	response, err := ExecutePlaybook(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), m[testenv.Account], playbooks.PlaybookEvent{
		Name: "java-4xx-error-rate",
		Labels: map[string]string{
			"alertname":           "java-4xx-error-rate",
			"end":                 "1755050887955",
			"environment":         "prod",
			"job":                 "eurl-service",
			"nb_webhook_event_id": "Q2Q2H95LZ1JJY9",
			"nb_webhook_id":       "01FWRHI9Y2FAX9QCRPFNK2ZI7Q",
			"nb_webhook_source":   "pagerduty_webhook",
			"nb_webhook_url":      "https://api.pagerduty.com/incidents/Q2Q2H95LZ1JJY9",
			"pattern_hash":        "Q2Q2H95LZ1JJY9",
			"receiver":            "pagerduty-orchestration-notifier",
			"receiver-type":       "pagerduty",
			"rule_id":             "java-4xx-error-rate",
			"rule_type":           "static_threshold",
			"severity":            "HIGH",
			"start":               "1755047287955",
			"status":              "CRITICAL",
		},
		Annotations: map[string]string{},
		StartedAt:   &start,
		EndedAt:     &end,
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
	response2 := []any{}
	for _, r := range response {
		response2 = append(response2, r.Response)
	}
	respB, err := common.MarshalJson(response2)
	assert.Nil(t, err)
	print(string(respB))
}

func TestExecutePlaybookForAws(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "TEST_CLOUD_ACCOUNT", "TEST_AWS_ACCOUNT_NUMBER")
	start := time.UnixMilli(1755047287955)
	end := time.UnixMilli(1755050887955)
	response, err := ExecutePlaybook(security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), slog.Default(), nil, nil), os.Getenv("TEST_CLOUD_ACCOUNT"), playbooks.PlaybookEvent{
		Name: "HighCPUUtilization",
		Labels: map[string]string{
			"alertname":                  "HighCPUUtilization",
			"rule_id":                    "HighCPUUtilization",
			"aws_event_arn":              fmt.Sprintf("arn:aws:cloudwatch:us-east-1:%s:alarm:HighCPUUtilization", os.Getenv("TEST_AWS_ACCOUNT_NUMBER")),
			"aws_event_name":             "HighCPUUtilization",
			"aws_event_state":            "firing",
			"aws_region":                 "us-east-1",
			"aws_event_metric_name":      "CPUUtilization",
			"aws_event_metric_namespace": "AWS/EC2",
			"aws_event_metric_statistic": "Average",
			"aws_event_threshold":        "70",
			"aws_event_instance":         "i-0695d9d318b7bbf30",
			"aws_service_name":           "amazonec2",
			"aws_account":                os.Getenv("TEST_AWS_ACCOUNT_NUMBER"),
		},
		Annotations: map[string]string{},
		StartedAt:   &start,
		EndedAt:     &end,
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
	response2 := []any{}
	for _, r := range response {
		response2 = append(response2, r.Response)
	}
	respB, err := common.MarshalJson(response2)
	assert.Nil(t, err)
	print(string(respB))
}

func TestExecutePlaybookProxyDBQuery(t *testing.T) {
	m := testenv.RequireEnv(t, testenv.Account)
	response, err := ExecutePlaybook(
		security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil),
		m[testenv.Account],
		playbooks.PlaybookEvent{
			Name:           "test_proxy_db_query",
			AggregationKey: "test_proxy_db_query",
			Labels:         map[string]string{},
			Annotations:    map[string]string{},
		},
	)
	assert.Nil(t, err)
	assert.NotNil(t, response)
	for _, r := range response {
		t.Logf("Action: %s, Error: %v", r.ActionName, r.Error)
		if r.Response != nil {
			respB, _ := common.MarshalJson(r.Response)
			t.Logf("Response: %s", string(respB))
		}
	}
}

func TestEval(t *testing.T) {
	// Test with production prefix
	response, err := evaluateRawParamsTemplates(map[string]any{
		"query": `{{ (labels.dimension_QueueName | split(sep='-') | slice('1:') | join('-')) }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"dimension_QueueName": "production-air-worker",
			"alertname":           "HighFileSystemUtilizationNbDev",
			"environment":         "prod",
			"job":                 "prometheus",
		},
	}, map[string]any{}, map[string]map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `air-worker`,
	}, response)

	// Test with staging prefix
	response, err = evaluateRawParamsTemplates(map[string]any{
		"query": `{{ (labels.dimension_QueueName | split(sep='-') | slice('1:') | join('-')) }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"dimension_QueueName": "staging-air-worker",
		},
	}, map[string]any{}, map[string]map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `air-worker`,
	}, response)

	// Test with dev prefix
	response, err = evaluateRawParamsTemplates(map[string]any{
		"query": `{{ (labels.dimension_QueueName | split(sep='-') | slice('1:') | join('-')) }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"dimension_QueueName": "dev-queue-worker",
		},
	}, map[string]any{}, map[string]map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `queue-worker`,
	}, response)

	// Test extracting just the prefix (first part before dash)
	response, err = evaluateRawParamsTemplates(map[string]any{
		"query": `{{ (labels.dimension_QueueName | split(sep='-') | first) }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"dimension_QueueName": "staging-air-worker",
		},
	}, map[string]any{}, map[string]map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `staging`,
	}, response)

	// Test extracting just the last part after all dashes
	response, err = evaluateRawParamsTemplates(map[string]any{
		"query": `{{ (labels.dimension_QueueName | split(sep='-') | last) }}`,
	}, playbooks.PlaybookEvent{
		Labels: map[string]string{
			"dimension_QueueName": "production-air-worker",
		},
	}, map[string]any{}, map[string]map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{
		"query": `worker`,
	}, response)
}

func TestNormalizeEventSource(t *testing.T) {
	cases := []struct {
		name    string
		in      EventConfig
		wantSrc string
	}{
		{
			name:    "default nudgebee source on a metric rule is coerced to prometheus",
			in:      EventConfig{Source: "nudgebee", AlertType: "metric"},
			wantSrc: "prometheus",
		},
		{
			name:    "empty source on a metric rule is coerced to prometheus",
			in:      EventConfig{Source: "", AlertType: "metric"},
			wantSrc: "prometheus",
		},
		{
			name:    "non-metric rule with source nudgebee is left alone",
			in:      EventConfig{Source: "nudgebee", AlertType: "event"},
			wantSrc: "nudgebee",
		},
		{
			name:    "external provider source is left alone",
			in:      EventConfig{Source: "datadog", AlertType: "metric"},
			wantSrc: "datadog",
		},
		{
			name:    "metric_provider reverse-maps and wins over the default",
			in:      EventConfig{Source: "nudgebee", AlertType: "metric", MetricProvider: "aws_cloudwatch"},
			wantSrc: "cloudwatch",
		},
		{
			name:    "explicit prometheus source is preserved",
			in:      EventConfig{Source: "prometheus", AlertType: "metric"},
			wantSrc: "prometheus",
		},
		{
			name:    "empty alert_type on nudgebee source is coerced to prometheus (mirrors upsert default)",
			in:      EventConfig{Source: "nudgebee", AlertType: ""},
			wantSrc: "prometheus",
		},
		{
			name:    "empty alert_type and empty source is coerced to prometheus",
			in:      EventConfig{Source: "", AlertType: ""},
			wantSrc: "prometheus",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.in
			normalizeEventSource(&req)
			assert.Equal(t, tc.wantSrc, req.Source)
		})
	}
}
