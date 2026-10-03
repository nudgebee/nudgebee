package triage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A Prometheus connected without an agent delivers through the public
// Alertmanager webhook and stores its rules under prometheus_user; both must be
// PromQL sources or the tuner neither suggests for those events nor applies to
// those rules.
func TestPromQLSourcesCoverAgentlessPrometheus(t *testing.T) {
	assert.True(t, promQLSources["prometheus_alertmanager_webhook"])
	assert.True(t, promQLSources["prometheus_user"])
	assert.True(t, thresholdSuggestionSources["prometheus_alertmanager_webhook"])
}

func TestExtractAlertRuleKey_AlertmanagerWebhookUsesAlertname(t *testing.T) {
	labels := map[string]interface{}{"alertname": "PaymentsHighErrorRate", "severity": "critical"}
	assert.Equal(t, "PaymentsHighErrorRate", ExtractAlertRuleKey("prometheus_alertmanager_webhook", labels))
	assert.Equal(t, ExtractAlertRuleKey("prometheus", labels), ExtractAlertRuleKey("prometheus_alertmanager_webhook", labels))
}

// The source filter must follow the event's own source: an agent-delivered and a
// webhook-delivered firing of the same alertname are different rows and must not
// be counted into each other's history.
func TestAlertRuleWhereClause_AlertmanagerWebhookFiltersItsOwnSource(t *testing.T) {
	where, filter := alertRuleWhereClause("prometheus_alertmanager_webhook", "PaymentsHighErrorRate")
	assert.Equal(t, "labels->>'alertname' = $1", where)
	assert.Equal(t, "AND source = 'prometheus_alertmanager_webhook'", filter)

	_, agentFilter := alertRuleWhereClause("prometheus", "PaymentsHighErrorRate")
	assert.Equal(t, "AND source = 'prometheus'", agentFilter)
}
