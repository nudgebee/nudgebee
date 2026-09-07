package eventrule

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The row is the only record of what the rule was, so re-creating on enable has to
// rebuild the definition from it — an empty or partial config would silently
// replace the user's rule with a different one.
func TestToggledRule_ToAlertRuleConfig(t *testing.T) {
	rule := toggledRule{
		Alert:          "PaymentsHighErrorRate",
		Source:         "prometheus_user",
		Expr:           `sum(rate(http_requests_total{code=~"5.."}[5m])) > 0.05`,
		Duration:       "10m",
		Severity:       "critical",
		AlertType:      "metric",
		Annotations:    map[string]string{"summary": "Payments 5xx above 5%", "description": "too many errors"},
		Labels:         map[string]string{"severity": "critical", "team": "payments"},
		ProviderConfig: map[string]any{"namespace": "nudgebee"},
	}

	cfg := rule.toAlertRuleConfig("acc-1")
	assert.Equal(t, "acc-1", cfg.AccountId)
	assert.Equal(t, rule.Alert, cfg.Name)
	assert.Equal(t, rule.Expr, cfg.Query)
	assert.Equal(t, "10m", cfg.Duration)
	assert.Equal(t, "critical", cfg.Severity)
	assert.Equal(t, "metric", cfg.AlertType)
	assert.Equal(t, rule.Annotations, cfg.Annotations)
	assert.Equal(t, rule.Labels, cfg.Labels)
	assert.Equal(t, rule.ProviderConfig, cfg.ProviderConfig)
	assert.True(t, cfg.Enabled, "the rule is being switched on, so it must be created enabled")
}

func TestUnmarshalStringMap(t *testing.T) {
	json := `{"summary":"5xx above 5%","description":"","runbook":"https://runbook"}`
	assert.Equal(t,
		map[string]string{"summary": "5xx above 5%", "description": "", "runbook": "https://runbook"},
		unmarshalStringMap(&json))

	// A missing or empty column is a rule saved without annotations, not an error.
	empty := ""
	assert.Equal(t, map[string]string{}, unmarshalStringMap(nil))
	assert.Equal(t, map[string]string{}, unmarshalStringMap(&empty))

	// Hand-edited rows can hold non-string members; drop them rather than failing
	// the whole toggle.
	mixed := `{"summary":"ok","threshold":5,"nested":{"a":1}}`
	assert.Equal(t, map[string]string{"summary": "ok"}, unmarshalStringMap(&mixed))

	broken := `{not json`
	assert.Equal(t, map[string]string{}, unmarshalStringMap(&broken))
}

func TestDerefString(t *testing.T) {
	v := "x"
	assert.Equal(t, "x", derefString(&v))
	assert.Equal(t, "", derefString(nil))
}

// Every source that owns a rule in an external system must be re-creatable, or
// enabling it leaves the rule on in Nudgebee and absent where it is evaluated.
func TestExternalSourcesResolveToACreatableProvider(t *testing.T) {
	for _, source := range []string{"datadog", "newrelic", "dynatrace", "splunk", "elasticsearch", "signoz", "grafana", "chronosphere_user", "loki", "cloudwatch", "azure_monitor", "gcp_monitoring"} {
		assert.True(t, isExternalProviderSource(source), "%s should be an external provider source", source)
		provider, providerSource := resolveProviderFromSource(source)
		assert.NotEmpty(t, provider, "%s resolves to no provider", source)
		assert.NotEmpty(t, providerSource, "%s resolves to no provider source", source)
	}
}
