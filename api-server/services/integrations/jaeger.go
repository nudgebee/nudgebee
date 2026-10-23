package integrations

import (
	"fmt"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(Jaeger{})
}

const IntegrationJaeger = "jaeger"

type Jaeger struct{}

func (j Jaeger) Name() string {
	return IntegrationJaeger
}

func (j Jaeger) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (j Jaeger) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{"jaeger_query_url"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"jaeger_query_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Jaeger Query API URL (e.g., https://jaeger.example.com:16686)",
				IsTestable:  true,
			},
			"jaeger_api_token": {
				Type:        core.ToolSchemaTypeString,
				Description: "Bearer token for Jaeger API authentication (optional)",
				IsEncrypted: true,
				IsTestable:  true,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "List of available accounts that can be linked with Jaeger integration",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this Jaeger integration configuration",
				Default:          "",
				AutoGenerateFunc: "",
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Jaeger default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
		},
	}
}

func (j Jaeger) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	jaegerURL := configMap["jaeger_query_url"]
	if jaegerURL == "" {
		return []error{fmt.Errorf("jaeger_query_url is required")}
	}

	jaegerURL = strings.TrimRight(jaegerURL, "/")
	headers := map[string]string{
		"Accept": "application/json",
	}
	if token := configMap["jaeger_api_token"]; token != "" {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", token)
	}

	resp, err := common.HttpGet(
		fmt.Sprintf("%s/api/services", jaegerURL),
		common.HttpWithHeaders(headers),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Jaeger API: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Jaeger API token (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Jaeger API (HTTP 403)")}
	default:
		return []error{fmt.Errorf("Jaeger API returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}

// JaegerConfig is a user-connected Jaeger query API: where it is and the optional
// bearer token it wants.
type JaegerConfig struct {
	URL   string
	Token string
}

// GetJaegerConfig reads the Jaeger integration linked to an account. An empty
// accountId is refused: ListIntegrationConfigs only filters by account when one is
// given, so it would otherwise return another account's Jaeger.
func GetJaegerConfig(sc *security.RequestContext, accountId string) (JaegerConfig, error) {
	if accountId == "" {
		return JaegerConfig{}, fmt.Errorf("account_id is required to resolve a Jaeger integration")
	}
	configs, err := core.ListIntegrationConfigs(sc, accountId, IntegrationJaeger)
	if err != nil {
		return JaegerConfig{}, fmt.Errorf("failed to list Jaeger integration configs: %w", err)
	}
	for _, c := range configs {
		cfg, err := jaegerConfigFromValues(c.Configs)
		if err != nil {
			return JaegerConfig{}, err
		}
		if cfg.URL != "" {
			return cfg, nil
		}
	}
	return JaegerConfig{}, fmt.Errorf("jaeger integration not found for account: %s", accountId)
}

func jaegerConfigFromValues(values []core.IntegrationConfigValue) (JaegerConfig, error) {
	var cfg JaegerConfig
	for _, v := range values {
		switch v.Name {
		case "jaeger_query_url":
			cfg.URL = strings.TrimRight(strings.TrimSpace(v.Value), "/")
		case "jaeger_api_token":
			token := strings.TrimSpace(v.Value)
			if token != "" && v.IsEncrypted {
				d, err := common.Decrypt(v.Value)
				if err != nil {
					return JaegerConfig{}, fmt.Errorf("failed to decrypt jaeger_api_token: %w", err)
				}
				token = strings.TrimSpace(d)
			}
			cfg.Token = token
		}
	}
	return cfg, nil
}
