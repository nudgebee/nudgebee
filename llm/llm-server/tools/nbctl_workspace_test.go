package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNbctlProviderPart(t *testing.T) {
	assert.Equal(t, "Its logs are in loki; write `nbctl logs` queries as LogQL.", nbctlProviderPart("logs", "loki", ""))
	// The agent's ClickHouse is queried in LogQL with the same labels.
	assert.Equal(t, "Its logs are in otel_clickhouse; write `nbctl logs` queries as LogQL.", nbctlProviderPart("logs", "otel_clickhouse", ""))
	es := nbctlProviderPart("logs", "ES", "logs-*")
	assert.True(t, strings.HasPrefix(es, "Its logs are in es (default index `logs-*`); write `nbctl logs` queries as a full Elasticsearch search body"), es)
	assert.Contains(t, es, "list-labels --fields-only", "non-native providers are told how to find field names")
	assert.Contains(t, nbctlProviderPart("metrics", "newrelic", ""), "that provider's own query language")
	// Fallbacks the logs and metrics actions cannot serve point at the tools.
	assert.Equal(t, "`nbctl logs` does not reach this account's logs; use the logs tool.", nbctlProviderPart("logs", "k8s", ""))
	assert.Equal(t, "`nbctl metrics` does not reach this account's metrics; use the metrics tool.", nbctlProviderPart("metrics", "aws", ""))
	assert.Contains(t, nbctlProviderPart("logs", "aws_cloudwatch", ""), "CloudWatch Logs Insights")
	assert.NotContains(t, nbctlProviderPart("metrics", "prometheus", ""), "--fields-only")
	assert.Empty(t, nbctlProviderPart("logs", "", "x"))
}
