package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(PostgreSql{})
}

const IntegrationPostgreSql = "postgresql"

type PostgreSql struct {
}

func (m PostgreSql) Name() string {
	return IntegrationPostgreSql
}

func (m PostgreSql) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (m PostgreSql) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"connection_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "Connection mode",
				Default:     "k8s",
				Enum:        []any{"k8s", "vm_agent"},
				Priority:    90,
				IsTestable:  true,
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
			// K8s fields
			"k8s_secret": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Kubernetes secret containing PGDATABASE, PGHOST, PGUSER, PGPASSWORD keys",
				ShowWhen:     map[string]any{"connection_mode": "k8s"},
				RequiredWhen: map[string]any{"connection_mode": "k8s"},
				Priority:     84,
				IsTestable:   true,
			},
			// VM agent connection fields
			"host": {
				Type:         core.ToolSchemaTypeString,
				Description:  "PostgreSQL host (e.g. db.example.com or 10.0.1.5)",
				RequiredWhen: map[string]any{"connection_mode": "vm_agent"},
				Priority:     82,
				IsTestable:   true,
			},
			"port": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "PostgreSQL port",
				Default:     5432,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    75,
				IsTestable:  true,
			},
			"database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Database name to connect to",
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    70,
				IsTestable:  true,
			},
			"ssl_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "SSL mode for the connection",
				Default:     "disable",
				Enum:        []any{"disable", "require", "verify-full"},
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    65,
				IsTestable:  true,
			},
			// Credential fields
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where database credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "aws_sm", "gcp_sm", "azure_kv", "local"},
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    60,
				IsTestable:  true,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Database username",
				ShowWhen:     map[string]any{"connection_mode": "vm_agent", "credential_source": "cloud_push"},
				RequiredWhen: map[string]any{"credential_source": "cloud_push"},
				Priority:     55,
				IsTestable:   true,
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Database password",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"connection_mode": "vm_agent", "credential_source": "cloud_push"},
				RequiredWhen: map[string]any{"credential_source": "cloud_push"},
				Priority:     54,
				IsTestable:   true,
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret name or ARN in the secret manager (e.g. arn:aws:secretsmanager:...:secret:my-db-creds)",
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
				Priority:    53,
				IsTestable:  true,
			},
			// Advanced options
			"read_only": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Restrict to read-only queries",
				Default:     true,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    20,
			},
			"max_open_connections": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Maximum open connections in the pool",
				Default:     5,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    10,
			},
		},
	}
}

func (m PostgreSql) ValidateConfig(sc *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	if configMap["connection_mode"] == "vm_agent" {
		return m.validateVMAgent(configMap)
	}
	return m.validateK8s(configMap, accountId)
}

func (m PostgreSql) validateK8s(configMap map[string]string, accountId string) []error {
	secretName := configMap["k8s_secret"]
	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	resp, err := relay.CommandExecutor(accountId, `psql -c "SELECT 1 + 1"`, secretName, map[string]string{})
	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}
	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from postgresql server: %v", resp)}
	}

	if !strings.Contains(respStr, "2") {
		return []error{fmt.Errorf("validation failed: expected result '2' not found in response")}
	}

	return nil
}

func (m PostgreSql) validateVMAgent(configMap map[string]string) []error {
	return validateVMAgentDBConfig(configMap)
}
