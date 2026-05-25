package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(VMAgent{})
}

const IntegrationVMAgent = "vm_agent"

type VMAgent struct{}

func (v VMAgent) Name() string {
	return IntegrationVMAgent
}

func (v VMAgent) Category() core.IntegrationCategory {
	return core.IntegrationCategoryProxy
}

func (v VMAgent) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Proxy Agent",
			},
		},
	}
}

func (v VMAgent) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}
