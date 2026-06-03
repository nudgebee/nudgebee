package alertrule

import (
	"fmt"

	"nudgebee/services/security"
)

// AlertRuleConfig is the common input for creating alert rules across providers.
type AlertRuleConfig struct {
	AccountId      string                 `json:"account_id"`
	Name           string                 `json:"name"`
	AlertType      string                 `json:"alert_type"`  // "metric" or "log"
	Query          string                 `json:"query"`       // provider-native query expression
	Severity       string                 `json:"severity"`    // critical, warning, info
	Duration       string                 `json:"duration"`    // evaluation window
	Annotations    map[string]string      `json:"annotations"` // summary, description
	Labels         map[string]string      `json:"labels"`      // custom labels/tags
	Enabled        bool                   `json:"enabled"`
	ProviderConfig map[string]interface{} `json:"provider_config"` // provider-specific fields
}

// AlertRuleResult is returned after creating/updating an alert rule.
type AlertRuleResult struct {
	ExternalRuleId string `json:"external_rule_id"` // ID in the external system
	Name           string `json:"name"`
	Status         string `json:"status"` // "created", "updated", "deleted"
}

// AlertRuleSource is implemented by each provider that supports alert rule creation.
type AlertRuleSource interface {
	CreateAlertRule(ctx *security.RequestContext, config AlertRuleConfig) (*AlertRuleResult, error)
	UpdateAlertRule(ctx *security.RequestContext, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error)
	DeleteAlertRule(ctx *security.RequestContext, accountId string, externalRuleId string) error
}

func getAlertRuleSource(provider, integrationSource string) (AlertRuleSource, error) {
	switch {
	case provider == "datadog" && integrationSource == "user":
		return &DatadogAlertRuleSource{}, nil
	case provider == "newrelic" && integrationSource == "user":
		return &NewRelicAlertRuleSource{}, nil
	case provider == "dynatrace" && integrationSource == "user":
		return &DynatraceAlertRuleSource{}, nil
	case provider == "splunk_observability_platform" && integrationSource == "user":
		return &SplunkAlertRuleSource{}, nil
	case provider == "ES" && integrationSource == "user":
		return &ElasticsearchAlertRuleSource{}, nil
	case provider == "signoz" && integrationSource == "user":
		return &SigNozAlertRuleSource{}, nil
	case provider == "grafana" && integrationSource == "user":
		return &GrafanaAlertRuleSource{}, nil
	case provider == "chronosphere" && integrationSource == "user":
		return &ChronosphereAlertRuleSource{}, nil
	case provider == "loki" && integrationSource == "agent":
		return &LokiAlertRuleSource{}, nil
	case provider == "aws_cloudwatch" || provider == "azure_app_insights" || provider == "gcp_monitoring":
		return &CloudAlertRuleSource{}, nil
	default:
		return nil, fmt.Errorf("alert rule creation not supported for provider %s/%s", provider, integrationSource)
	}
}

// CreateAlertRule creates an alert rule in the external system.
func CreateAlertRule(ctx *security.RequestContext, provider, providerSource string, config AlertRuleConfig) (*AlertRuleResult, error) {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return nil, err
	}
	return source.CreateAlertRule(ctx, config)
}

// UpdateAlertRule updates an existing alert rule in the external system.
func UpdateAlertRule(ctx *security.RequestContext, provider, providerSource, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return nil, err
	}
	return source.UpdateAlertRule(ctx, externalRuleId, config)
}

// DeleteAlertRule deletes an alert rule from the external system.
func DeleteAlertRule(ctx *security.RequestContext, provider, providerSource, accountId, externalRuleId string) error {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return err
	}
	return source.DeleteAlertRule(ctx, accountId, externalRuleId)
}
