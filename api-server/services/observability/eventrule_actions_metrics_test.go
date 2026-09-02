package observability

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/testenv"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusEnricherAction(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	prometheusEnricherAction := prometheusAction{}
	defaultPlaybookActionContext := playbooks.NewPlaybookActionContext(os.Getenv("TEST_TENANT"), os.Getenv("TEST_ACCOUNT"), slog.Default(), playbooks.PlaybookEvent{
		Name:        "HighFileSystemUtilizationNbDev",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartedAt:   nil,
		EndedAt:     nil,
	})
	response, err := prometheusEnricherAction.Execute(defaultPlaybookActionContext, map[string]any{
		"instant":      false,
		"duration":     map[string]any{},
		"step":         "1m",
		"promql_query": "",
		"promql_queries": []playbooks.NamedQuery{
			{
				Key:   "A",
				Query: `system.filesystem.utilization{host.name="nb-dev-db"}`,
			},
		},
	})
	assert.NotNil(t, response)
	assert.Nil(t, err)
}

func TestJsonSerialization(t *testing.T) {
	a1 := []playbooks.PlaybookActionResponse{}
	a1 = append(a1, playbooks.NewPlaybookActionResponseJson("test", map[string]any{}, nil, nil))
	a1 = append(a1, playbooks.PrometheusActionResponse{Metadata: map[string]any{}, Data: map[string]any{}, AdditionalInfo: map[string]any{}})
	bytes, err := common.MarshalJson(a1)
	assert.NoError(t, err)
	assert.Greater(t, len(bytes), 0)
	print(string(bytes))
}

func TestFormatRelativeTime(t *testing.T) {
	tests := []struct {
		name     string
		duration int
		expected string
	}{
		{"10 minutes", 10, "10m"},
		{"45 minutes", 45, "45m"},
		{"59 minutes", 59, "59m"},
		{"60 minutes (1 hour)", 60, "1h"},
		{"120 minutes (2 hours)", 120, "2h"},
		{"1439 minutes", 1439, "23h"},
		{"1440 minutes (1 day)", 1440, "1d"},
		{"2880 minutes (2 days)", 2880, "2d"},
		{"10080 minutes (7 days)", 10080, "7d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRelativeTime(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildChronosphereMetricsExplorerURL_SingleQuery(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{
		{
			Key:   "A",
			Query: `rate(rails_requests_total{status=~"^[4].*",environment=~"production"}[5m])`,
		},
	}
	durationMinutes := 60

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, baseURL, "URL should contain base URL")
	assert.Contains(t, url, "/metrics/explorer-v2", "URL should contain metrics explorer path")
	assert.Contains(t, url, "queries=", "URL should contain queries parameter")
	assert.Contains(t, url, "start=1h", "URL should contain start=1h for 60 minutes")
	assert.Contains(t, url, "formulas=[]", "URL should contain formulas parameter")

	t.Logf("Generated URL: %s", url)
}

func TestBuildChronosphereMetricsExplorerURL_MultipleQueries(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{
		{
			Key:   "A",
			Query: `rate(rails_requests_total{status=~"^[4].*"}[5m])`,
		},
		{
			Key:   "B",
			Query: `rate(rails_requests_total{status=~"^[5].*"}[5m])`,
		},
	}
	durationMinutes := 15

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, "start=15m", "URL should contain start=15m for 15 minutes")

	t.Logf("Generated URL with multiple queries: %s", url)
}

func TestBuildChronosphereMetricsExplorerURL_NoQueries(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{}
	durationMinutes := 60

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.NotNil(t, err, "Should return an error")
	assert.Empty(t, url, "URL should be empty on error")
	assert.Contains(t, err.Error(), "no PromQL queries", "Error should mention no queries")
}

func TestBuildChronosphereMetricsExplorerURL_ComplexQuery(t *testing.T) {
	baseURL := "https://example.chronosphere.io"
	promqlQueries := []playbooks.NamedQuery{
		{
			Key:   "A",
			Query: ` (sum(rate(rails_requests_total{status=~"^[4].*",environment=~"production", service_name=~"geo-service"}[5m])) by (service_name,environment, status)/sum(rate(rails_requests_total{environment=~"production", service_name=~"geo-service"}[5m])) by (service_name,environment,status)) *100 > 1`,
		},
	}
	durationMinutes := 60

	url, err := buildChronosphereMetricsExplorerURL(baseURL, promqlQueries, durationMinutes)

	assert.Nil(t, err, "Should not return an error")
	assert.NotEmpty(t, url, "URL should not be empty")
	assert.Contains(t, url, baseURL, "URL should contain base URL")
	assert.Contains(t, url, "/metrics/explorer-v2", "URL should contain metrics explorer path")
	assert.Contains(t, url, "queries=", "URL should contain queries parameter")
	assert.Contains(t, url, "start=1h", "URL should contain start=1h for 60 minutes")
	assert.Contains(t, url, "formulas=[]", "URL should contain formulas parameter")

	// Verify the query structure is correct
	assert.Contains(t, url, "DataQuery", "URL should contain DataQuery kind")
	assert.Contains(t, url, "PrometheusTimeSeriesQuery", "URL should contain PrometheusTimeSeriesQuery kind")
	assert.Contains(t, url, "rails_requests_total", "URL should contain the actual query metric")

	t.Logf("Generated URL for complex query: %s", url)
}

func TestWorkloadKindFromEvent(t *testing.T) {
	cases := []struct {
		name     string
		event    playbooks.PlaybookEvent
		expected string
	}{
		{
			name:     "pod subject owned by Deployment -> deployment (casing normalised)",
			event:    playbooks.PlaybookEvent{SubjectType: "pod", SubjectName: "web-abc-123", SubjectOwner: "web", SubjectOwnerKind: "Deployment"},
			expected: "deployment",
		},
		{
			name:     "pod subject owned by DaemonSet -> daemonset",
			event:    playbooks.PlaybookEvent{SubjectType: "pod", SubjectName: "fluentd-xyz", SubjectOwner: "fluentd", SubjectOwnerKind: "DaemonSet"},
			expected: "daemonset",
		},
		{
			name:     "pod subject owned by StatefulSet -> statefulset",
			event:    playbooks.PlaybookEvent{SubjectType: "pod", SubjectName: "kafka-0", SubjectOwner: "kafka", SubjectOwnerKind: "StatefulSet"},
			expected: "statefulset",
		},
		{
			name:     "owner set but kind unknown (e.g. Job) falls back to deployment",
			event:    playbooks.PlaybookEvent{SubjectType: "pod", SubjectName: "backup-1", SubjectOwner: "backup", SubjectOwnerKind: "Job"},
			expected: "deployment",
		},
		{
			name:     "no owner, deployment subject -> deployment from subject_type",
			event:    playbooks.PlaybookEvent{SubjectType: "deployment", SubjectName: "web"},
			expected: "deployment",
		},
		{
			name:     "no owner, statefulset subject -> statefulset from subject_type",
			event:    playbooks.PlaybookEvent{SubjectType: "statefulset", SubjectName: "kafka"},
			expected: "statefulset",
		},
		{
			name:     "no owner, bare pod subject -> pod (exact match, not regex)",
			event:    playbooks.PlaybookEvent{SubjectType: "pod", SubjectName: "orphan-pod"},
			expected: "pod",
		},
		{
			name:     "no owner, unknown subject type -> deployment default",
			event:    playbooks.PlaybookEvent{SubjectType: "service", SubjectName: "svc"},
			expected: "deployment",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, workloadKindFromEvent(tc.event))
		})
	}
}

func TestWorkloadMetricTimeRange(t *testing.T) {
	const hourMs = int64(60 * 60 * 1000)
	started := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ended := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)

	t.Run("both timestamps set widen start by 60m", func(t *testing.T) {
		s, e := workloadMetricTimeRange(playbooks.PlaybookEvent{StartedAt: &started, EndedAt: &ended})
		assert.Equal(t, started.Add(-60*time.Minute).UnixMilli(), s)
		assert.Equal(t, ended.UnixMilli(), e)
	})

	t.Run("only start set defaults end to now", func(t *testing.T) {
		before := time.Now().UnixMilli()
		s, e := workloadMetricTimeRange(playbooks.PlaybookEvent{StartedAt: &started})
		after := time.Now().UnixMilli()
		assert.Equal(t, started.Add(-60*time.Minute).UnixMilli(), s)
		assert.GreaterOrEqual(t, e, before)
		assert.LessOrEqual(t, e, after)
	})

	t.Run("only end set derives start one hour earlier", func(t *testing.T) {
		s, e := workloadMetricTimeRange(playbooks.PlaybookEvent{EndedAt: &ended})
		assert.Equal(t, ended.UnixMilli(), e)
		assert.Equal(t, ended.UnixMilli()-hourMs, s)
	})

	t.Run("no timestamps default to trailing hour ending now", func(t *testing.T) {
		before := time.Now().UnixMilli()
		s, e := workloadMetricTimeRange(playbooks.PlaybookEvent{})
		after := time.Now().UnixMilli()
		assert.GreaterOrEqual(t, e, before)
		assert.LessOrEqual(t, e, after)
		assert.Equal(t, e-hourMs, s)
	})
}

func TestBuildWorkloadMetricsResponse(t *testing.T) {
	queryMeta := map[string]any{"workload_name": "web", "workload_namespace": "shop"}

	t.Run("nil when no results", func(t *testing.T) {
		assert.Nil(t, buildWorkloadMetricsResponse(queryMeta, OutputMetricQuery{}))
	})

	t.Run("nil when all payloads empty", func(t *testing.T) {
		out := OutputMetricQuery{Results: []QueryResult{
			{QueryKey: "cpu_usage", Payload: []Result{}},
		}}
		assert.Nil(t, buildWorkloadMetricsResponse(queryMeta, out))
	})

	// Regression for the reviewer-flagged label-extraction fix: labels must come
	// from the first result that actually has a payload, not blindly Results[0].
	t.Run("labels lifted from first non-empty payload", func(t *testing.T) {
		out := OutputMetricQuery{Results: []QueryResult{
			{QueryKey: "empty", Payload: []Result{}},
			{QueryKey: "cpu_usage", Payload: []Result{
				{Metric: map[string]string{"pod": "web-abc", "namespace": "shop"}, Values: []float64{0.5}},
			}},
		}}
		resp := buildWorkloadMetricsResponse(queryMeta, out)
		assert.NotNil(t, resp)

		jsonResp, ok := resp.(playbooks.PlaybookActionResponseJson)
		assert.True(t, ok, "expected PlaybookActionResponseJson")
		assert.Equal(t, "web-abc", jsonResp.Labels["pod"])
		assert.Equal(t, "shop", jsonResp.Labels["namespace"])
		assert.Equal(t, "1.0", jsonResp.Metadata["query-result-version"])
		assert.Equal(t, queryMeta, jsonResp.Metadata["query"])
	})
}

// TestAutoExecuteByWorkloadIntegration exercises observabilityMetricsAction.
// autoExecuteByWorkload end to end against a live DB + relay. It is skipped unless
// the required env vars are set, so it never runs in CI.
//
// Run (PromQL/agent example):
//
//	APP_DATABASE_URL='postgres://user:pass@host:5432/db?sslmode=disable' \
//	RELAY_SERVER_ENDPOINT='http://localhost:52832' \
//	RELAY_SERVER_SECRET_KEY='...'            # only if your relay requires it \
//	NUDGEBEE_ENCRYPTION_KEY='...'            # only for user-source providers \
//	TEST_TENANT='<tenant>' TEST_ACCOUNT='<cloud_account_id>' \
//	TEST_NAMESPACE='<namespace>' TEST_WORKLOAD='<deployment-or-workload-name>' \
//	TEST_WORKLOAD_KIND='Deployment'          # optional: Deployment|StatefulSet|DaemonSet|pod \
//	go test ./observability/ -run TestAutoExecuteByWorkloadIntegration -v -count=1
func TestAutoExecuteByWorkloadIntegration(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, testenv.Account, "TEST_NAMESPACE", "TEST_WORKLOAD")

	workload := env["TEST_WORKLOAD"]
	namespace := env["TEST_NAMESPACE"]
	ownerKind := os.Getenv("TEST_WORKLOAD_KIND")
	if ownerKind == "" {
		ownerKind = "Deployment"
	}

	start := time.Now().Add(-1 * time.Hour)
	end := time.Now()

	event := playbooks.PlaybookEvent{
		Name:        "TestAutoExecuteByWorkload",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
		StartedAt:   &start,
		EndedAt:     &end,
		// getEventWorkload keys on SubjectOwner first; SubjectName is the fallback.
		// Set both so the query targets the workload regardless.
		SubjectName:      workload,
		SubjectOwner:     workload,
		SubjectOwnerKind: ownerKind,
		SubjectType:      "pod",
		SubjectNamespace: namespace,
	}

	ctx := playbooks.NewPlaybookActionContext(env[testenv.Tenant], env[testenv.Account], slog.Default(), event)

	t.Logf("resolved kind=%q workload=%q namespace=%q", workloadKindFromEvent(event), getEventWorkload(event), getEventNamespace(event))

	action := &observabilityMetricsAction{}
	resp, err := action.autoExecuteByWorkload(ctx)
	require.NoError(t, err, "autoExecuteByWorkload returned an error")

	if resp == nil {
		t.Fatalf("no metric data returned for workload %q in namespace %q (query ran but matched no series)", workload, namespace)
	}

	data, mErr := common.MarshalJson(resp.GetData())
	require.NoError(t, mErr)
	t.Logf("labels: %v", resp.GetAdditionalInfo())
	t.Logf("metric data:\n%s", string(data))
}
