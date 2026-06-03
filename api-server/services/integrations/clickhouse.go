package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
)

const IntegrationClickHouse = "clickhouse"

// Validation patterns
var (
	// Allow alphanumeric, dots, dashes, and colons (for IPv6)
	chHostnameRegex = regexp.MustCompile(`^[a-zA-Z0-9.:-]+$`)
	chPortRegex     = regexp.MustCompile(`^[0-9]+$`)
	chDbNameRegex   = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	chEnvKeyRegex   = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

type ClickHouse struct {
}

func (ch ClickHouse) Name() string {
	return IntegrationClickHouse
}

func (ch ClickHouse) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (ch ClickHouse) ConfigSchema() core.IntegrationSchema {
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
				Description:  "Kubernetes secret containing CLICKHOUSE_DATABASE, CLICKHOUSE_HOST, CLICKHOUSE_USER, CLICKHOUSE_PASSWORD keys",
				ShowWhen:     map[string]any{"connection_mode": "k8s"},
				RequiredWhen: map[string]any{"connection_mode": "k8s"},
				Priority:     84,
				IsTestable:   true,
			},
			// Connection fields
			"host": {
				Type:         core.ToolSchemaTypeString,
				Description:  "ClickHouse host (e.g. ch.example.com or 10.0.1.5)",
				RequiredWhen: map[string]any{"connection_mode": "vm_agent"},
				Priority:     82,
				IsTestable:   true,
			},
			"port": {
				Type:         core.ToolSchemaTypeString,
				Description:  "ClickHouse port",
				ShowWhen:     map[string]any{"connection_mode": "vm_agent"},
				RequiredWhen: map[string]any{"connection_mode": "vm_agent"},
				Priority:     75,
				IsTestable:   true,
			},
			"database": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Database name to connect to",
				ShowWhen:     map[string]any{"connection_mode": "vm_agent"},
				RequiredWhen: map[string]any{"connection_mode": "vm_agent"},
				Priority:     70,
				IsTestable:   true,
			},
			// K8s-specific optional fields
			"secret_user_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name for username in k8s secret",
				Default:     "CLICKHOUSE_USER",
				ShowWhen:    map[string]any{"connection_mode": "k8s"},
				Priority:    40,
				IsTestable:  true,
			},
			"secret_password_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "Key name for password in k8s secret",
				Default:     "CLICKHOUSE_PASSWORD",
				ShowWhen:    map[string]any{"connection_mode": "k8s"},
				Priority:    39,
				IsTestable:  true,
			},
			"secure_connection": {
				Type:        core.ToolSchemaTypeString,
				Description: "Use secure connection (true/false)",
				ShowWhen:    map[string]any{"connection_mode": "k8s"},
				Priority:    38,
				IsTestable:  true,
			},
			// VM agent fields
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

func configValueExists(configs []core.IntegrationConfigValue, name string) bool {
	for _, cfg := range configs {
		if cfg.Name == name && cfg.Value != "" {
			return true
		}
	}
	return false
}

func (ch ClickHouse) ValidateConfig(securityContext *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	if configMap["connection_mode"] == "vm_agent" {
		return validateVMAgentDBConfig(configMap)
	}

	return ch.validateK8s(configs, accountId)
}

func (ch ClickHouse) validateK8s(configs []core.IntegrationConfigValue, accountId string) []error {
	secretName := ""
	chHost := ""
	chPort := "9000"
	chDatabase := "default"
	chUserKeyInSecret := "CLICKHOUSE_USER"
	chPasswordKeyInSecret := "CLICKHOUSE_PASSWORD"
	chSecure := false

	for _, cfg := range configs {
		switch cfg.Name {
		case "k8s_secret":
			secretName = cfg.Value
		case "host":
			chHost = cfg.Value
		case "port":
			if cfg.Value != "" {
				chPort = cfg.Value
			}
		case "database":
			if cfg.Value != "" {
				chDatabase = cfg.Value
			}
		case "secret_user_key":
			if cfg.Value != "" {
				chUserKeyInSecret = cfg.Value
			}
		case "secret_password_key":
			if cfg.Value != "" {
				chPasswordKeyInSecret = cfg.Value
			}
		case "secure_connection":
			if strings.ToLower(cfg.Value) == "true" {
				chSecure = true
				if chPort == "9000" && !configValueExists(configs, "port") {
					chPort = "9440"
				}
			}
		}
	}

	if chHost != "" && !chHostnameRegex.MatchString(chHost) {
		return []error{fmt.Errorf("invalid host format")}
	}

	if !chPortRegex.MatchString(chPort) {
		return []error{fmt.Errorf("invalid port format")}
	}

	if !chDbNameRegex.MatchString(chDatabase) {
		return []error{fmt.Errorf("invalid database format")}
	}

	if !chEnvKeyRegex.MatchString(chUserKeyInSecret) {
		return []error{fmt.Errorf("invalid secret_user_key format")}
	}

	if !chEnvKeyRegex.MatchString(chPasswordKeyInSecret) {
		return []error{fmt.Errorf("invalid secret_password_key format")}
	}

	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	secureFlag := ""
	if chSecure {
		secureFlag = "--secure"
	}

	if chHost == "" {
		chHost = "$CLICKHOUSE_HOST"
	}

	command := fmt.Sprintf(`clickhouse client --host %s --port %s --user $%s --password $%s --database %s %s --query "SELECT 1" --format CSVWithNames --send_logs_level=none --progress=0`,
		chHost, chPort, chUserKeyInSecret, chPasswordKeyInSecret, chDatabase, secureFlag)

	resp, err := relay.CommandExecutor(accountId, command, secretName, map[string]string{})

	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}

	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from clickhouse server: %v", resp)}
	}

	// Check if the response contains the expected CSV header and value
	if strings.Contains(respStr, "1\n1") || strings.Contains(respStr, "1") {
		return nil
	}

	return []error{fmt.Errorf("failed to validate clickhouse connection - unexpected response: %s", respStr)}
}

func init() {
	core.RegisterIntegration(ClickHouse{})
}
