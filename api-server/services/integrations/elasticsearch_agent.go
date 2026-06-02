package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(ElasticsearchAgent{})
}

type ElasticsearchAgent struct{}

func (m ElasticsearchAgent) Name() string {
	return "ES"
}

func (m ElasticsearchAgent) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m ElasticsearchAgent) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"default_log_provider": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Set as default log provider for this account",
				Default:     false,
				Priority:    18,
			},
			"log_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Log Index",
				ShowWhen:    map[string]any{"default_log_provider": true},
				Priority:    17,
			},
			"default_traces_provider": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Set as default traces provider for this account",
				Default:     false,
				Priority:    14,
			},
			"trace_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Trace Index",
				ShowWhen:    map[string]any{"default_traces_provider": true},
				Priority:    13,
			},
			"default_metrics_provider": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Set as default metrics provider for this account",
				Default:     false,
				Priority:    16,
			},
			"metrics_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Metrics Index",
				ShowWhen:    map[string]any{"default_metrics_provider": true},
				Priority:    15,
			},
		},
	}
}

func (m ElasticsearchAgent) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}
