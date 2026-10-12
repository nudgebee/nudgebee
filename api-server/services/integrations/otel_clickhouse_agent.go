package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(OtelClickhouseAgent{})
}

type OtelClickhouseAgent struct{}

func (m OtelClickhouseAgent) Name() string {
	return "otel_clickhouse"
}

func (m OtelClickhouseAgent) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m OtelClickhouseAgent) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		// No user-sourced otel_clickhouse exists, so the Integrations list reads this
		// schema to decide whether a row offers Test Connection.
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"default_log_provider": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Set as default log provider for this account",
				Default:     false,
				Priority:    13,
			},
			"default_traces_provider": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Set as default traces provider for this account",
				Default:     false,
				Priority:    14,
			},
		},
	}
}

func (m OtelClickhouseAgent) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

// TestConnection implements core.TestableIntegration. The agent's ClickHouse is
// configured in the agent's Helm values, not in this form, so the probe asks the
// agent itself to query it, and reports why it could not.
func (m OtelClickhouseAgent) TestConnection(sc *security.RequestContext, config []core.IntegrationConfigValue, accountId string) error {
	return relay.TestAgentClickHouse(accountId)
}
