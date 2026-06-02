package integrations

import (
	"nudgebee/services/integrations/core"
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
		Properties: map[string]core.IntegrationSchemaProperty{
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
