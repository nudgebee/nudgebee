package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(KafkaProxy{})
}

const IntegrationKafkaProxy = "kafka_proxy"

type KafkaProxy struct{}

func (m KafkaProxy) Name() string {
	return IntegrationKafkaProxy
}

func (m KafkaProxy) Category() core.IntegrationCategory {
	return core.IntegrationCategoryProxy
}

func (m KafkaProxy) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"brokers"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"proxy_type": {
				Type:    core.ToolSchemaTypeString,
				Default: "kafka-proxy",
				Hidden:  true,
			},
			"brokers": {
				Type:        core.ToolSchemaTypeString,
				Description: "Comma-separated list of Kafka broker addresses (host:port)",
			},
			"sasl_mechanism": {
				Type:        core.ToolSchemaTypeString,
				Description: "SASL authentication mechanism",
				Default:     "none",
				Enum:        []any{"none", "plain", "scram-sha-256", "scram-sha-512"},
			},
			"tls_enabled": {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Enable TLS encryption",
				Default:     false,
			},
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "aws_sm", "gcp_sm", "azure_kv", "local"},
			},
			"sasl_username": {
				Type:        core.ToolSchemaTypeString,
				Description: "SASL username",
				ShowWhen:    map[string]any{"credential_source": "cloud_push", "sasl_mechanism": []any{"plain", "scram-sha-256", "scram-sha-512"}},
			},
			"sasl_password": {
				Type:        core.ToolSchemaTypeString,
				Description: "SASL password",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"credential_source": "cloud_push", "sasl_mechanism": []any{"plain", "scram-sha-256", "scram-sha-512"}},
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret reference in the secret manager",
				ShowWhen:    map[string]any{"credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name of Kafka Proxy integration",
			},
		},
	}
}

func (m KafkaProxy) ValidateConfig(_ *security.SecurityContext, config []core.IntegrationConfigValue, _ string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	var errs []error
	if configMap["brokers"] == "" {
		errs = append(errs, fmt.Errorf("brokers is required"))
	}

	saslMechanism := configMap["sasl_mechanism"]
	credSource := configMap["credential_source"]

	if saslMechanism != "" && saslMechanism != "none" {
		if credSource == "" || credSource == "cloud_push" {
			if configMap["sasl_username"] == "" {
				errs = append(errs, fmt.Errorf("sasl_username is required when SASL is enabled"))
			}
			if configMap["sasl_password"] == "" {
				errs = append(errs, fmt.Errorf("sasl_password is required when SASL is enabled"))
			}
		}
	}

	if credSource == "aws_sm" || credSource == "gcp_sm" || credSource == "azure_kv" {
		if configMap["secret_ref"] == "" {
			errs = append(errs, fmt.Errorf("secret_ref is required for %s credential source", credSource))
		}
	}

	return errs
}
