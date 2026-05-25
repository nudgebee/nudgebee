package integrations

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	// Register under "es:user" so that when the UI creates an ES integration
	// with source="user", it resolves to this full schema (with URL, auth, etc.)
	// instead of the minimal ElasticsearchAgent schema.
	// No plain RegisterIntegration call — that key ("es") belongs to ElasticsearchAgent.
	core.RegisterIntegrationWithSource("ES", "user", Elasticsearch{})
}

type Elasticsearch struct {
}

func (m Elasticsearch) Name() string {
	return "ES"
}

func (m Elasticsearch) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m Elasticsearch) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"url", "auth_type"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the Elasticsearch/OpenSearch endpoint (e.g., https://my-domain.us-east-1.es.amazonaws.com)",
			},
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     "basic",
				Enum:        []any{"basic", "cognito", "api_key", "bearer_token"},
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Username for basic auth or Cognito User Pool",
				ShowWhen:     map[string]any{"auth_type": []any{"basic", "cognito"}},
				RequiredWhen: map[string]any{"auth_type": []any{"basic", "cognito"}},
			},
			"password": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Password for basic auth or Cognito User Pool",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": []any{"basic", "cognito"}},
				RequiredWhen: map[string]any{"auth_type": []any{"basic", "cognito"}},
			},
			"api_key": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Elasticsearch API key (Base64-encoded id:api_key)",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "api_key"},
				RequiredWhen: map[string]any{"auth_type": "api_key"},
			},
			"bearer_token": {
				Type:         core.ToolSchemaTypeString,
				Description:  "OAuth2 or service-account bearer token",
				IsEncrypted:  true,
				ShowWhen:     map[string]any{"auth_type": "bearer_token"},
				RequiredWhen: map[string]any{"auth_type": "bearer_token"},
			},
			"region": {
				Type:        core.ToolSchemaTypeString,
				Description: "AWS region (e.g., us-east-1)",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
			},
			"user_pool_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cognito User Pool ID (e.g., us-east-1_xxxxxx)",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
			},
			"identity_pool_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cognito Identity Pool ID (e.g., us-east-1:xxxxx-xxxx-xxx)",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
			},
			"app_client_id": {
				Type:        core.ToolSchemaTypeString,
				Description: "Cognito App Client ID",
				ShowWhen:    map[string]any{"auth_type": "cognito"},
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Elasticsearch integration",
				Default:          "",
				AutoGenerateFunc: "",
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Elasticsearch default Logs Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			"log_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Log Index",
				ShowWhen:    map[string]any{core.DefaultLogProvider: true},
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Elasticsearch default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			"metrics_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Metrics Index",
				ShowWhen:    map[string]any{core.DefaultMetricsProvider: true},
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Elasticsearch default Traces Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			"trace_index": {
				Type:        core.ToolSchemaTypeString,
				Description: "Trace Index",
				ShowWhen:    map[string]any{core.DefaultTraceProvider: true},
			},
		},
	}
}

func (m Elasticsearch) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	if configMap["url"] == "" {
		return []error{fmt.Errorf("url is required")}
	}

	authType := configMap["auth_type"]
	if authType == "" {
		authType = "basic"
	}

	var errs []error
	switch authType {
	case "basic":
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for basic auth"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for basic auth"))
		}
	case "cognito":
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for cognito auth"))
		}
		if configMap["password"] == "" {
			errs = append(errs, fmt.Errorf("password is required for cognito auth"))
		}
		if configMap["region"] == "" {
			errs = append(errs, fmt.Errorf("region is required for cognito auth"))
		}
		if configMap["user_pool_id"] == "" {
			errs = append(errs, fmt.Errorf("user_pool_id is required for cognito auth"))
		}
		if configMap["identity_pool_id"] == "" {
			errs = append(errs, fmt.Errorf("identity_pool_id is required for cognito auth"))
		}
		if configMap["app_client_id"] == "" {
			errs = append(errs, fmt.Errorf("app_client_id is required for cognito auth"))
		}
	case "api_key":
		if configMap["api_key"] == "" {
			errs = append(errs, fmt.Errorf("api_key is required for api_key auth"))
		}
	case "bearer_token":
		if configMap["bearer_token"] == "" {
			errs = append(errs, fmt.Errorf("bearer_token is required for bearer_token auth"))
		}
	}

	if len(errs) > 0 {
		return errs
	}

	// Cognito auth requires AWS SDK flow; skip connection test here
	if authType == "cognito" {
		return nil
	}

	// Build auth header based on auth type (values are already decrypted by the framework)
	var authHeader string
	switch authType {
	case "basic":
		authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(configMap["username"]+":"+configMap["password"]))
	case "api_key":
		authHeader = "ApiKey " + configMap["api_key"]
	case "bearer_token":
		authHeader = "Bearer " + configMap["bearer_token"]
	}

	esURL := strings.TrimRight(configMap["url"], "/")
	resp, err := common.HttpGet(
		fmt.Sprintf("%s/_cluster/health", esURL),
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Accept":        "application/json",
		}),
		common.HttpWithInsecureSkipVerify(),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to Elasticsearch: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return []error{fmt.Errorf("invalid Elasticsearch credentials (HTTP 401)")}
	case http.StatusForbidden:
		return []error{fmt.Errorf("insufficient permissions for Elasticsearch (HTTP 403)")}
	default:
		return []error{fmt.Errorf("Elasticsearch returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}
