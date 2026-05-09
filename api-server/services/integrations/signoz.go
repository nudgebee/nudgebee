package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(Signoz{})
}

const IntegrationSignoz = "signoz"

type Signoz struct {
}

func (m Signoz) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	return []error{}
}

func (m Signoz) Name() string {
	return IntegrationSignoz
}

func (m Signoz) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m Signoz) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"signoz_url", "signoz_username", "signoz_password"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"signoz_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the SigNoz instance (e.g., https://signoz.example.com).",
			},
			"signoz_username": {
				Type:        core.ToolSchemaTypeString,
				Description: "Username for authenticating with SigNoz.",
			},
			"signoz_password": {
				Type:        core.ToolSchemaTypeString,
				Description: "Password or API token for authenticating with SigNoz.",
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Associated account(s) for this integration.",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this SigNoz integration.",
				Default:          "",
				AutoGenerateFunc: "",
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make SigNoz default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
		},
	}
}
