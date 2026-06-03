package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strconv"
	"strings"
)

func init() {
	core.RegisterIntegration(Mysql{})
}

const IntegrationMysql = "mysql"

type Mysql struct {
}

func (m Mysql) Name() string {
	return IntegrationMysql
}

func (m Mysql) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (m Mysql) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:         core.ToolSchemaTypeObject,
		Testable:     true,
		TestableWhen: map[string]any{"connection_mode": "vm_agent"},
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
				Description:  "Kubernetes secret containing MYSQL_DATABASE, MYSQL_HOST, MYSQL_USER, MYSQL_PWD keys",
				ShowWhen:     map[string]any{"connection_mode": "k8s"},
				RequiredWhen: map[string]any{"connection_mode": "k8s"},
				Priority:     84,
				IsTestable:   true,
			},
			// VM agent connection fields
			"host": {
				Type:         core.ToolSchemaTypeString,
				Description:  "MySQL host (e.g. db.example.com or 10.0.1.5)",
				RequiredWhen: map[string]any{"connection_mode": "vm_agent"},
				Priority:     82,
				IsTestable:   true,
			},
			"port": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "MySQL port",
				Default:     3306,
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
			"tls_enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Enable TLS encryption",
				Default:     false,
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

func (m Mysql) ValidateConfig(_ *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	if configMap["connection_mode"] == "vm_agent" {
		return validateVMAgentDBConfig(configMap)
	}

	return m.validateK8s(configMap, accountId)
}

func (m Mysql) validateK8s(configMap map[string]string, accountId string) []error {
	secretName := configMap["k8s_secret"]
	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	resp, err := relay.CommandExecutor(accountId, `mysql -h "$MYSQL_HOST" -u "$MYSQL_USER" --password="$MYSQL_PWD" "$MYSQL_DATABASE" -e "SELECT 1"`, secretName, map[string]string{})
	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}
	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from mysql server: %v", resp)}
	}

	if strings.Contains(respStr, "1") {
		return nil
	}
	return []error{fmt.Errorf("validation failed: expected result '1' not found in response")}
}

// validateVMAgentDBConfig performs schema-only validation for VM agent database connections.
// Shared by mysql, mssql, clickhouse, and postgresql integrations.
func validateVMAgentDBConfig(configMap map[string]string) []error {
	var errs []error

	if configMap["host"] == "" {
		errs = append(errs, fmt.Errorf("host is required"))
	}

	if p := configMap["port"]; p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			errs = append(errs, fmt.Errorf("port must be between 1 and 65535"))
		}
	}

	credSource := configMap["credential_source"]
	if credSource == "" || credSource == "cloud_push" {
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for cloud_push credentials"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for cloud_push credentials"))
		}
	}

	if credSource == "aws_sm" || credSource == "gcp_sm" || credSource == "azure_kv" {
		if configMap["secret_ref"] == "" {
			errs = append(errs, fmt.Errorf("secret_ref is required for %s credential source", credSource))
		}
	}

	return errs
}
