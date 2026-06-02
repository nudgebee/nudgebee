package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
)

func init() {
	core.RegisterIntegration(MongoDBProxy{})
}

const IntegrationMongoDBProxy = "mongodb_proxy"

type MongoDBProxy struct{}

func (m MongoDBProxy) Name() string {
	return IntegrationMongoDBProxy
}

func (m MongoDBProxy) Category() core.IntegrationCategory {
	return core.IntegrationCategoryProxy
}

func (m MongoDBProxy) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"host"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"proxy_type": {
				Type:     core.ToolSchemaTypeString,
				Default:  "mongo-proxy",
				Hidden:   true,
				Priority: 4,
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "MongoDB host address",
				Priority:    85,
			},
			"port": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "MongoDB port",
				Default:     27017,
				Priority:    50,
			},
			"database": {
				Type:        core.ToolSchemaTypeString,
				Description: "Default database name",
				Priority:    48,
			},
			"replica_set": {
				Type:        core.ToolSchemaTypeString,
				Description: "Replica set name (if using replica set)",
				Priority:    46,
			},
			"auth_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication database",
				Default:     "admin",
				Priority:    44,
			},
			"tls_enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Enable TLS encryption",
				Default:     false,
				Priority:    30,
			},
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "aws_sm", "gcp_sm", "azure_kv", "local"},
				Priority:    92,
			},
			"username": {
				Type:        core.ToolSchemaTypeString,
				Description: "MongoDB username",
				ShowWhen:    map[string]any{"credential_source": "cloud_push"},
				Priority:    70,
			},
			"password": {
				Type:        core.ToolSchemaTypeString,
				Description: "MongoDB password",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"credential_source": "cloud_push"},
				Priority:    68,
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret reference in the secret manager",
				ShowWhen:    map[string]any{"credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
				Priority:    66,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of MongoDB Proxy integration",
				Priority:    100,
			},
		},
	}
}

func (m MongoDBProxy) ValidateConfig(_ *security.SecurityContext, config []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

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
	if credSource == "aws_sm" || credSource == "gcp_sm" || credSource == "azure_kv" {
		if configMap["secret_ref"] == "" {
			errs = append(errs, fmt.Errorf("secret_ref is required for %s credential source", credSource))
		}
	}

	return errs
}
