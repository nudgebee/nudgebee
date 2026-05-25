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
	core.RegisterIntegration(Redis{})
}

const IntegrationRedis = "redis"

type Redis struct {
}

func (r Redis) Name() string {
	return IntegrationRedis
}

func (r Redis) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (r Redis) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"connection_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "Connection mode",
				Default:     "k8s",
				Enum:        []any{"k8s", "vm_agent"},
				Priority:    100,
				IsTestable:  true,
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Integration name",
				Priority:    95,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         90,
			},
			// K8s fields
			"k8s_secret": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Kubernetes secret containing REDIS_HOST, REDIS_PASSWORD, REDIS_USER keys",
				ShowWhen:     map[string]any{"connection_mode": "k8s"},
				RequiredWhen: map[string]any{"connection_mode": "k8s"},
				Priority:     80,
				IsTestable:   true,
			},
			// VM agent connection fields
			"host": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Redis host (e.g. redis.example.com or 10.0.1.5)",
				RequiredWhen: map[string]any{"connection_mode": "vm_agent"},
				Priority:     80,
				IsTestable:   true,
			},
			"port": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Redis port",
				Default:     6379,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    75,
				IsTestable:  true,
			},
			"db": {
				Type:        core.ToolSchemaTypeInteger,
				Description: "Redis database number",
				Default:     0,
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
				Type:        core.ToolSchemaTypeString,
				Description: "Redis username (ACL, Redis 6+)",
				ShowWhen:    map[string]any{"credential_source": "cloud_push"},
				Priority:    55,
				IsTestable:  true,
			},
			"password": {
				Type:        core.ToolSchemaTypeString,
				Description: "Redis password",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"credential_source": "cloud_push"},
				Priority:    54,
				IsTestable:  true,
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret name or ARN in the secret manager",
				ShowWhen:    map[string]any{"credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
				Priority:    53,
				IsTestable:  true,
			},
		},
	}
}

func (r Redis) ValidateConfig(sc *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	if configMap["connection_mode"] == "vm_agent" {
		return r.validateVMAgent(configMap)
	}

	return r.validateK8s(configMap, accountId)
}

func (r Redis) validateK8s(configMap map[string]string, accountId string) []error {
	secretName := configMap["k8s_secret"]
	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	resp, err := relay.CommandExecutor(accountId, "redis-cli -h $REDIS_HOST --user $REDIS_USER --pass $REDIS_PASSWORD --no-auth-warning PING", secretName, map[string]string{})
	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}
	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from redis server: %v", resp)}
	}
	if strings.Contains(respStr, "PONG") {
		return nil
	}
	return []error{fmt.Errorf("unknown error occurred while validating redis config")}
}

func (r Redis) validateVMAgent(configMap map[string]string) []error {
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
