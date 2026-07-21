package observability

import (
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func TestBuildElasticsearchConfigFromValues_Indexes(t *testing.T) {
	cfg := BuildElasticsearchConfigFromValues([]core.IntegrationConfigValue{
		{Name: ElasticsearchUrl, Value: "https://es.example.com/"},
		{Name: ElasticsearchLogIndex, Value: "logs-generic.*"},
		{Name: ElasticsearchMetricsIndex, Value: "metrics-*"},
		{Name: ElasticsearchTraceIndex, Value: "otel-v1-apm-span-*"},
	})
	assert.Equal(t, "https://es.example.com", cfg.Url) // trailing slash trimmed
	assert.Equal(t, "logs-generic.*", cfg.LogIndex)
	assert.Equal(t, "metrics-*", cfg.MetricsIndex)
	assert.Equal(t, "otel-v1-apm-span-*", cfg.TraceIndex)
	assert.Equal(t, "basic", cfg.AuthType) // default
}

func TestResolveESIndexOverride(t *testing.T) {
	const mapping = `[
		{"account_id":"acc-a","log_index":"logs-a-*","metrics_index":"metrics-a-*","trace_index":"traces-a-*"},
		{"account_id":"acc-b","log_index":"logs-b-*"},
		{"account_id":"acc-c","metrics_index":"  ","trace_index":"traces-c-*"}
	]`

	cases := []struct {
		name      string
		mapping   string
		accountID string
		indexType string
		want      string
	}{
		{"logs hit", mapping, "acc-a", "logs", "logs-a-*"},
		{"metrics hit", mapping, "acc-a", "metrics", "metrics-a-*"},
		{"traces hit", mapping, "acc-a", "traces", "traces-a-*"},
		{"partial row: metrics unset falls back", mapping, "acc-b", "metrics", ""},
		{"partial row: logs hit", mapping, "acc-b", "logs", "logs-b-*"},
		{"blank/whitespace index falls back", mapping, "acc-c", "metrics", ""},
		{"blank row: traces still hits", mapping, "acc-c", "traces", "traces-c-*"},
		{"unknown account falls back", mapping, "acc-z", "logs", ""},
		{"empty mapping", "", "acc-a", "logs", ""},
		{"whitespace mapping", "   ", "acc-a", "logs", ""},
		{"empty account id", mapping, "", "logs", ""},
		{"malformed json", `{not-an-array}`, "acc-a", "logs", ""},
		{"object (not array) json", `{"account_id":"acc-a"}`, "acc-a", "logs", ""},
		{"unknown index type", mapping, "acc-a", "spans", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveESIndexOverride(tc.mapping, tc.accountID, tc.indexType))
		})
	}
}
