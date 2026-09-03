package alertrule

import (
	"fmt"
	"log/slog"

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
	case provider == "cubeapm" && integrationSource == "user":
		return &CubeAPMAlertRuleSource{}, nil
	case provider == "loki" && integrationSource == "agent":
		return &LokiAlertRuleSource{}, nil
	case provider == "loki" && integrationSource == "user":
		return &LokiSaasAlertRuleSource{}, nil
	case provider == "aws_cloudwatch" || provider == "azure_app_insights" || provider == "gcp_monitoring":
		return &CloudAlertRuleSource{}, nil
	default:
		return nil, fmt.Errorf("alert rule creation not supported for provider %s/%s", provider, integrationSource)
	}
}

// resolveProviderSource corrects the integration source for providers that exist in both
// agent and user flavours, since callers derive it from a static map that cannot know
// which one an account actually has.
//
// Loki is the case that needs it: eventrule's resolveProviderFromSource answers "agent"
// for every account, so an account whose Loki is a user-configured integration would have
// its rule CRUD dispatched to the relay and fail against an agent it does not run.
//
// The override is deliberately made only when the answer is unambiguous — a user row and
// no agent row. An account holding both keeps the caller's source instead, because
// nothing on the event_rules row records which backend actually stored a given
// external_rule_id: silently retargeting would send a delete to the ruler that never held
// the rule, get a 404, report success, and leave the real rule firing forever on the other
// backend. Operators with both rows should disable the one they are not using.
func resolveProviderSource(ctx *security.RequestContext, provider, providerSource, accountId string) string {
	if provider != "loki" || accountId == "" {
		return providerSource
	}
	hasUser, err := hasIntegrationWithSource(ctx, accountId, "loki", "user")
	if err != nil {
		slog.Error("alertrule: failed to check for user-sourced loki integration, keeping caller's source",
			"account_id", accountId, "source", providerSource, "error", err)
		return providerSource
	}
	if !hasUser {
		return providerSource
	}

	hasAgent, err := hasIntegrationWithSource(ctx, accountId, "loki", "agent")
	if err != nil {
		slog.Error("alertrule: failed to check for agent-sourced loki integration, keeping caller's source",
			"account_id", accountId, "source", providerSource, "error", err)
		return providerSource
	}
	if hasAgent {
		slog.Warn("alertrule: account has both agent- and user-sourced loki integrations; routing alert rules to the agent. Disable the unused integration to make this unambiguous.",
			"account_id", accountId, "source", providerSource)
		return providerSource
	}
	return "user"
}

// CreateAlertRule creates an alert rule in the external system.
func CreateAlertRule(ctx *security.RequestContext, provider, providerSource string, config AlertRuleConfig) (*AlertRuleResult, error) {
	source, err := getAlertRuleSource(provider, resolveProviderSource(ctx, provider, providerSource, config.AccountId))
	if err != nil {
		return nil, err
	}
	return source.CreateAlertRule(ctx, config)
}

// UpdateAlertRule updates an existing alert rule in the external system.
func UpdateAlertRule(ctx *security.RequestContext, provider, providerSource, externalRuleId string, config AlertRuleConfig) (*AlertRuleResult, error) {
	source, err := getAlertRuleSource(provider, resolveProviderSource(ctx, provider, providerSource, config.AccountId))
	if err != nil {
		return nil, err
	}
	return source.UpdateAlertRule(ctx, externalRuleId, config)
}

// DeleteAlertRule deletes an alert rule from the external system.
func DeleteAlertRule(ctx *security.RequestContext, provider, providerSource, accountId, externalRuleId string) error {
	source, err := getAlertRuleSource(provider, resolveProviderSource(ctx, provider, providerSource, accountId))
	if err != nil {
		return err
	}
	return source.DeleteAlertRule(ctx, accountId, externalRuleId)
}
