package alertrule

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Captured from a real GET /api/alerting/rules/_find against Kibana 8.19.11 on
// a basic licence, trimmed to the fields the mapping reads.
const kibanaFindJSON = `{
  "page": 1, "per_page": 1, "total": 1,
  "data": [{
    "id": "baf85522-3bcb-41a7-bd3c-72942707a6fc",
    "name": "Sample: stderr log spike by container",
    "rule_type_id": ".es-query",
    "consumer": "stackAlerts",
    "enabled": true,
    "mute_all": false,
    "tags": ["sample", "logs", "severity:critical"],
    "schedule": {"interval": "5m"},
    "params": {
      "searchType": "esQuery",
      "index": ["logs-kubernetes.container_logs-*"],
      "timeField": "@timestamp",
      "esQuery": "{\"query\":{\"bool\":{\"filter\":[{\"term\":{\"stream\":\"stderr\"}}]}}}",
      "threshold": [100]
    }
  }]
}`

func TestKibanaRuleToExternalRule(t *testing.T) {
	var resp kibanaFindResponse
	require.NoError(t, json.Unmarshal([]byte(kibanaFindJSON), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, 1, resp.Total)

	got := kibanaRuleToExternalRule(resp.Data[0])

	assert.Equal(t, "baf85522-3bcb-41a7-bd3c-72942707a6fc", got.ExternalRuleId)
	assert.Equal(t, "Sample: stderr log spike by container", got.Name)
	assert.Equal(t, "log", got.AlertType)
	assert.Equal(t, "5m", got.Duration)
	assert.True(t, got.Enabled)
	// esQuery is the ES-query rule's own expression; the params blob is only a
	// fallback for rule types that have no single query field.
	assert.Contains(t, got.Query, `"stream":"stderr"`)
	// Kibana has no severity field — the only signal is an operator-set tag.
	assert.Equal(t, "critical", got.Severity)
	assert.Equal(t, "", got.Labels["sample"])
	assert.Equal(t, ".es-query", got.ProviderConfig["kibana_rule_type_id"])
	assert.Equal(t, "stackAlerts", got.ProviderConfig["kibana_consumer"])
}

// A muted rule still evaluates but notifies nothing, so it is not active.
func TestKibanaRuleMuteAllDisables(t *testing.T) {
	var resp kibanaFindResponse
	require.NoError(t, json.Unmarshal([]byte(kibanaFindJSON), &resp))
	r := resp.Data[0]
	r.MuteAll = true
	assert.False(t, kibanaRuleToExternalRule(r).Enabled)

	r.MuteAll = false
	r.Enabled = false
	assert.False(t, kibanaRuleToExternalRule(r).Enabled)
}

// event_rules.alert_type is FK-constrained to 'log' / 'metric'.
func TestKibanaRuleAlertType(t *testing.T) {
	allowed := map[string]bool{"log": true, "metric": true}
	for _, in := range []string{".es-query", "logs.alert.document.count", "metrics.alert.threshold", "apm.error_rate", ""} {
		got := kibanaRuleAlertType(in)
		assert.True(t, allowed[got], "rule type %q produced %q", in, got)
	}
	assert.Equal(t, "log", kibanaRuleAlertType(".es-query"))
	assert.Equal(t, "metric", kibanaRuleAlertType("metrics.alert.threshold"))
}

// event_rules.severity is FK-constrained to 'critical' / 'warning'.
func TestKibanaRuleSeverity(t *testing.T) {
	allowed := map[string]bool{"critical": true, "warning": true}
	for _, in := range []string{"critical", "p1", "high", "warning", "info", "", "banana"} {
		got := kibanaRuleSeverity(in)
		assert.True(t, allowed[got], "severity tag %q produced %q", in, got)
	}
	assert.Equal(t, "critical", kibanaRuleSeverity("critical"))
	assert.Equal(t, "warning", kibanaRuleSeverity(""))
}

// Rule params are per-rule-type, so a rule with no esQuery must still yield a
// non-empty expression rather than an empty event_rules.expr.
func TestKibanaRuleQueryFallsBackToParams(t *testing.T) {
	r := kibanaRule{Params: map[string]any{"threshold": []any{float64(90)}, "metric": "cpu"}}
	q := kibanaRuleQuery(r)
	assert.Contains(t, q, "threshold")

	assert.Equal(t, "", kibanaRuleQuery(kibanaRule{}))
}

// Kibana tags are free-form strings; `key:value` is an operator convention.
func TestKibanaTagsToLabels(t *testing.T) {
	labels := kibanaTagsToLabels([]string{"prod", "severity:critical", "team:sre", ""})
	assert.Contains(t, labels, "prod")
	assert.Equal(t, "", labels["prod"])
	assert.Equal(t, "critical", labels["severity"])
	assert.Equal(t, "sre", labels["team"])
	assert.NotContains(t, labels, "")
}

func TestKibanaHeaders(t *testing.T) {
	// An API key is a complete credential and takes precedence.
	h := kibanaHeaders(&elasticsearchConfig{ApiKey: "abc123", Username: "u", Password: "p"})
	assert.Equal(t, "ApiKey abc123", h["Authorization"])

	// Basic otherwise — Kibana delegates authentication to Elasticsearch, so the
	// integration's own credentials apply unchanged.
	h = kibanaHeaders(&elasticsearchConfig{Username: "elastic", Password: "secret"})
	assert.Equal(t, "Basic ZWxhc3RpYzpzZWNyZXQ=", h["Authorization"])

	h = kibanaHeaders(&elasticsearchConfig{})
	assert.NotContains(t, h, "Authorization")
}
