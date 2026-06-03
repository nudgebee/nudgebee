package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
)

func init() {
	core.RegisterIntegration(Oracle{})
}

const IntegrationOracle = "oracle"

type Oracle struct{}

func (m Oracle) Name() string {
	return IntegrationOracle
}

func (m Oracle) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (m Oracle) ConfigSchema() core.IntegrationSchema {
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
				Description: "Oracle host (e.g. db.example.com or 10.0.1.5)",
				Priority:    80,
				IsTestable:  true,
			},
			"port": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Oracle port",
				Default:     1521,
				Priority:    75,
				IsTestable:  true,
			},
			"service_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Oracle service name (e.g. ORCL, XEPDB1)",
				Priority:    70,
				IsTestable:  true,
			},
			"tls_enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Enable TLS encryption",
				Default:     false,
				Priority:    30,
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

func (m Oracle) ValidateConfig(_ *security.SecurityContext, configs []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	var errs []error

	if configMap["host"] == "" {
		errs = append(errs, fmt.Errorf("host is required"))
	}

	if configMap["service_name"] == "" {
		errs = append(errs, fmt.Errorf("service_name is required for Oracle"))
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
