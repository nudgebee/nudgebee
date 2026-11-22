package eventrule

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuleSourcesForEvent(t *testing.T) {
	// A PagerDuty-delivered firing can be a Prometheus rule's.
	assert.Equal(t, []string{"pagerduty_webhook", legacyUISourceName, agentEventSourceName, "prometheus_alertmanager_webhook"},
		RuleSourcesForEvent("pagerduty_webhook"))
	// EventBridge carries CloudWatch alarm state changes.
	assert.Equal(t, []string{"AWS_EventBridge", "AWS_CloudWatch_Alarm"}, RuleSourcesForEvent("AWS_EventBridge"))
	// A pushed alarm stored as an alarm still belongs to a rule written while
	// it was stored as an EventBridge event.
	assert.Equal(t, []string{"AWS_CloudWatch_Alarm", "AWS_EventBridge"}, RuleSourcesForEvent("AWS_CloudWatch_Alarm"))
	// Sources nothing else is delivered on only look up themselves.
	assert.Equal(t, []string{"datadog_webhook"}, RuleSourcesForEvent("datadog_webhook"))
	assert.Equal(t, []string{"kubernetes_api_server"}, RuleSourcesForEvent("kubernetes_api_server"))
}

func TestPickEventRule(t *testing.T) {
	tests := []struct {
		name  string
		cands []eventRuleCandidate
		want  string
	}{
		{"single definition", []eventRuleCandidate{{"p", agentEventSourceName}}, "p"},
		// A Prometheus alert routed through Alertmanager also has a webhook copy.
		{"definition beats webhook copy", []eventRuleCandidate{{"copy", "prometheus_alertmanager_webhook"}, {"p", agentEventSourceName}}, "p"},
		{"CloudWatch alarm beats its EventBridge copy", []eventRuleCandidate{{"eb", "AWS_EventBridge"}, {"cw", "AWS_CloudWatch_Alarm"}}, "cw"},
		{"only a copy", []eventRuleCandidate{{"dd", "datadog_webhook"}}, "dd"},
		{"two definitions are ambiguous", []eventRuleCandidate{{"p", agentEventSourceName}, {"u", prometheusUserSourceName}}, ""},
		{"two copies are ambiguous", []eventRuleCandidate{{"a", "pagerduty_webhook"}, {"b", "prometheus_alertmanager_webhook"}}, ""},
		{"none", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickEventRule(tc.cands)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.want != "", ok)
		})
	}
}
