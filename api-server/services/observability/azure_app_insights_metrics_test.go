package observability

import (
	"strings"
	"testing"

	"nudgebee/services/integrations"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

func TestBuildAzureMetricKql_MetricNamePath(t *testing.T) {
	kql, err := buildAzureMetricKql("requests/duration", 1700000000000, 1700003600000, 5, nil, nil)
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(kql, "customMetrics"), "should query customMetrics table")
	assert.Contains(t, kql, "where timestamp between (datetime(")
	assert.Contains(t, kql, "where name == 'requests/duration'")
	assert.Contains(t, kql, "summarize value = avg(value) by bin(timestamp, 5m)")
	assert.Contains(t, kql, "order by timestamp asc")
}

func TestBuildAzureMetricKql_LabelsAndMatchers(t *testing.T) {
	kql, err := buildAzureMetricKql(
		"cpu",
		1700000000000, 1700003600000, 1,
		map[string]string{"role": "api"},
		[]LabelMatcher{{Label: "region", Operator: "_eq", Value: "us-east"}, {Label: "env", Operator: "_contains", Value: "prod"}},
	)
	assert.NoError(t, err)
	assert.Contains(t, kql, "tostring(customDimensions['role']) == 'api'")
	assert.Contains(t, kql, "tostring(customDimensions['region']) == 'us-east'")
	assert.Contains(t, kql, "tostring(customDimensions['env']) contains 'prod'")
}

func TestBuildAzureMetricKql_UnsupportedOperator(t *testing.T) {
	_, err := buildAzureMetricKql("cpu", 1, 2, 1, nil, []LabelMatcher{{Label: "x", Operator: "_regex", Value: "y"}})
	assert.Error(t, err)
}

func TestBuildAzureMetricKql_RawPassthrough(t *testing.T) {
	raw := "performanceCounters\n| summarize avg(value) by bin(timestamp, 5m)"
	kql, err := buildAzureMetricKql(raw, 1700000000000, 1700003600000, 5, nil, nil)
	assert.NoError(t, err)
	// time-range guard injected after the table, original body preserved
	assert.Contains(t, kql, "performanceCounters")
	assert.Contains(t, kql, "where timestamp between")
	assert.Contains(t, kql, "summarize avg(value)")
}

func TestBuildAzureMetricKql_RawWithExistingTimeFilterUntouched(t *testing.T) {
	raw := "customMetrics | where timestamp > ago(1h) | summarize avg(value)"
	kql, err := buildAzureMetricKql(raw, 1, 2, 5, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, raw, kql)
}

func TestEscapeKqlValue(t *testing.T) {
	assert.Equal(t, "it''s", escapeKqlValue("it's"))
	assert.Equal(t, "a b", escapeKqlValue("a\nb"))
}

func TestAzureMetricsStepMinutes(t *testing.T) {
	// 1h range, no requested step -> at least 1 minute
	assert.Equal(t, 1, azureMetricsStepMinutes(0, 1700000000000, 1700003600000))
	// requested 5m honored when above the floor
	assert.Equal(t, 5, azureMetricsStepMinutes(300, 1700000000000, 1700003600000))
	// huge range (1,000,000 minutes) forces step up to keep buckets bounded
	start := int64(1700000000000)
	end := start + int64(1000000)*60000 // 1,000,000 minutes in ms
	assert.GreaterOrEqual(t, azureMetricsStepMinutes(60, start, end), 1000)
}

func TestAzureParseTimestampMs(t *testing.T) {
	ms, ok := azureParseTimestampMs("2023-11-14T22:13:20Z")
	assert.True(t, ok)
	assert.Equal(t, int64(1700000000000), ms)
	_, ok = azureParseTimestampMs("not-a-time")
	assert.False(t, ok)
}

func TestAzureMetricResponseToQueryResult_SingleSeries(t *testing.T) {
	resp := AzureResponse{Tables: []Table{{
		Columns: []Column{{Name: "timestamp", Type: "datetime"}, {Name: "value", Type: "real"}},
		Rows: [][]any{
			{"2023-11-14T22:13:20Z", float64(10)},
			{"2023-11-14T22:18:20Z", float64(20)},
		},
	}}}
	qr := azureMetricResponseToQueryResult(resp, "q1", "kql")
	assert.Equal(t, "q1", qr.QueryKey)
	assert.Len(t, qr.Payload, 1)
	assert.Equal(t, []float64{10, 20}, qr.Payload[0].Values)
	assert.Len(t, qr.Payload[0].Timestamps, 2)
}

func TestAzureMetricResponseToQueryResult_MultiSeriesByDimension(t *testing.T) {
	resp := AzureResponse{Tables: []Table{{
		Columns: []Column{{Name: "timestamp"}, {Name: "value"}, {Name: "role"}},
		Rows: [][]any{
			{"2023-11-14T22:13:20Z", float64(1), "api"},
			{"2023-11-14T22:13:20Z", float64(2), "worker"},
			{"2023-11-14T22:18:20Z", float64(3), "api"},
		},
	}}}
	qr := azureMetricResponseToQueryResult(resp, "q1", "kql")
	assert.Len(t, qr.Payload, 2, "one series per distinct dimension value")
	for _, p := range qr.Payload {
		assert.Contains(t, []string{"api", "worker"}, p.Metric["role"])
	}
}

func TestAzureMetricResponseToQueryResult_EmptyAndMalformed(t *testing.T) {
	assert.Empty(t, azureMetricResponseToQueryResult(AzureResponse{}, "q", "k").Payload)
	// missing value column -> no payload, no panic
	resp := AzureResponse{Tables: []Table{{Columns: []Column{{Name: "timestamp"}}, Rows: [][]any{{"2023-11-14T22:13:20Z"}}}}}
	assert.Empty(t, azureMetricResponseToQueryResult(resp, "q", "k").Payload)
}

func TestAzureMetricSource_FetchMetricsQuery_ViaSeam(t *testing.T) {
	src := &AzureAppInsightsMetricSource{
		getConfigs: func(_ *security.RequestContext, _ string) (integrations.AzureAppInsightsConfig, error) {
			return integrations.AzureAppInsightsConfig{AzureAppInsightsAppID: "app"}, nil
		},
		execQuery: func(_ *security.RequestContext, _ integrations.AzureAppInsightsConfig, _ string) (AzureResponse, error) {
			return AzureResponse{Tables: []Table{{
				Columns: []Column{{Name: "timestamp"}, {Name: "value"}},
				Rows:    [][]any{{"2023-11-14T22:13:20Z", float64(42)}},
			}}}, nil
		},
	}
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	out, err := src.FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId: "acc",
		Queries:   map[string]string{"q1": "cpu"},
		StartTime: 1700000000000,
		EndTime:   1700003600000,
	})
	assert.NoError(t, err)
	assert.Len(t, out.Results, 1)
	assert.Nil(t, out.Results[0].Error)
	assert.Len(t, out.Results[0].Payload, 1)
	assert.Equal(t, []float64{42}, out.Results[0].Payload[0].Values)
}

func TestInjectAzureKqlAfterTable_SkipsLetAndComments(t *testing.T) {
	// A pipe inside a let/string before the first real pipe must not be the
	// injection point; the clause goes after the table line, before the op.
	raw := "// preamble\nlet prefix = \"a|b\";\ncustomMetrics\n| summarize avg(value)"
	out := injectAzureKqlAfterTable(raw, "| where ts > ago(1h)")
	assert.Contains(t, out, "let prefix = \"a|b\";")
	assert.Contains(t, out, "customMetrics\n| where ts > ago(1h)")
	// the let-line pipe was not used as the split point
	assert.NotContains(t, out, "let prefix = \"a\n")
}
