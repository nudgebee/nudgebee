package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(Mssql{})
}

const IntegrationMssql = "mssql"

type Mssql struct{}

func (m Mssql) Name() string {
	return IntegrationMssql
}

func (m Mssql) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (m Mssql) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"connection_mode": {
				Type:       core.ToolSchemaTypeString,
				Default:    "vm_agent",
				Hidden:     true,
				IsTestable: true,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Integration name",
				Priority:    100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			// Connection fields
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "MSSQL host (e.g. db.example.com or 10.0.1.5)",
				Priority:    80,
				IsTestable:  true,
			},
			"port": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "MSSQL port",
				Default:     1433,
				Priority:    75,
				IsTestable:  true,
			},
			"database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Database name to connect to",
				Priority:    70,
				IsTestable:  true,
			},
			"tls_enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Enable TLS encryption",
				Default:     false,
				Priority:    65,
				IsTestable:  true,
			},
			// Credential fields
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where database credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "aws_sm", "gcp_sm", "azure_kv", "local"},
				Priority:    60,
				IsTestable:  true,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Database username",
				ShowWhen:     map[string]any{"credential_source": "cloud_push"},
				RequiredWhen: map[string]any{"credential_source": "cloud_push"},
				Priority:     55,
				IsTestable:   true,
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Database password",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"credential_source": "cloud_push"},
				RequiredWhen: map[string]any{"credential_source": "cloud_push"},
				Priority:     54,
				IsTestable:   true,
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret name or ARN in the secret manager",
				ShowWhen:    map[string]any{"credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
				Priority:    53,
				IsTestable:  true,
			},
			// Advanced options
			"read_only": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Restrict to read-only queries",
				Default:     true,
				Priority:    20,
			},
			"max_open_connections": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Maximum open connections in the pool",
				Default:     5,
				Priority:    10,
			},
		},
	}
}

func (m Mssql) ValidateConfig(_ *security.SecurityContext, configs []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}
	return validateVMAgentDBConfig(configMap)
}
