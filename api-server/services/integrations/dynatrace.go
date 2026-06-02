package integrations

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

const (
	DynatraceConfigApiToken = "api_token"
	DynatraceConfigBaseUrl  = "base_url"
)

func init() {
	core.RegisterIntegration(Dynatrace{})
}

const IntegrationDynatrace = "dynatrace"

type Dynatrace struct{}

func (m Dynatrace) Name() string {
	return IntegrationDynatrace
}

func (m Dynatrace) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m Dynatrace) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{DynatraceConfigApiToken, DynatraceConfigBaseUrl},
		Properties: map[string]core.IntegrationSchemaProperty{
			DynatraceConfigApiToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Dynatrace Platform Bearer Token (requires storage:logs:read, storage:spans:read, storage:metrics:read scopes)",
				IsEncrypted: true,
				IsTestable:  true,
				Priority:    70,
			},
			DynatraceConfigBaseUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "Dynatrace Environment URL (e.g. https://{env-id}.apps.dynatrace.com)",
				IsTestable:  true,
				Priority:    80,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name for Dynatrace Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Dynatrace default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Dynatrace default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         13,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Dynatrace default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         11,
			},
		},
	}
}

// ValidateDynatraceConfig verifies connectivity and Bearer token validity by submitting
// a minimal DQL query to the Dynatrace Grail API. Returns nil on success.
func (m *Dynatrace) ValidateDynatraceConfig(apiToken, baseURL string) error {
	if apiToken == "" {
		return errors.New("api_token must not be empty")
	}
	if baseURL == "" {
		return errors.New("base_url must not be empty")
	}

	// POST a minimal DQL query to the Grail execute endpoint.
	// A 200/202 response means the Bearer token is valid and has storage access.
	// 401 = invalid/expired token, 403 = valid token but missing required scopes.
	executeURL := strings.TrimRight(baseURL, "/") + "/platform/storage/query/v1/query:execute"
	headers := map[string]string{
		"Authorization": "Bearer " + apiToken,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}

	res, err := common.HttpPost(executeURL,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(map[string]string{"query": "fetch logs | limit 1"}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to Dynatrace at %s: %w", baseURL, err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to read validate response body: %w", err)
	}

	switch res.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid or expired Bearer token (HTTP 401)")
	case http.StatusForbidden:
		return fmt.Errorf("token lacks required scopes — ensure storage:logs:read, storage:spans:read, storage:metrics:read are granted (HTTP 403): %s", string(body))
	default:
		return fmt.Errorf("Dynatrace API returned unexpected status %d: %s", res.StatusCode, string(body))
	}
}

// ValidateConfig implements the core.Integration interface. Called automatically
// by core.CreateIntegrationConfig() before saving credentials.
func (m Dynatrace) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	apiToken := ""
	baseURL := ""

	for _, config := range integrationConfig {
		switch config.Name {
		case DynatraceConfigApiToken:
			apiToken = config.Value
			if config.IsEncrypted {
				var err error
				apiToken, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("dynatrace config validation failed: %w", err)}
				}
			}
		case DynatraceConfigBaseUrl:
			baseURL = config.Value
		}
	}

	if err := m.ValidateDynatraceConfig(apiToken, baseURL); err != nil {
		return []error{fmt.Errorf("dynatrace config validation failed: %w", err)}
	}
	return nil
}

// GetDynatraceConfigs retrieves and decrypts the stored Dynatrace credentials for an account.
func GetDynatraceConfigs(sc *security.RequestContext, accountId string) (string, string, error) {
	apiToken := ""
	baseURL := ""

	dynatraceIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationDynatrace)
	if err != nil {
		return apiToken, baseURL, fmt.Errorf("failed to list dynatrace integration configs: %w", err)
	}
	if len(dynatraceIntegrations) == 0 {
		return apiToken, baseURL, fmt.Errorf("dynatrace integration not found for account: %s", accountId)
	}

	dynatraceIntegration := dynatraceIntegrations[0]
	for _, config := range dynatraceIntegration.Configs {
		switch config.Name {
		case DynatraceConfigApiToken:
			apiToken = config.Value
			if config.IsEncrypted {
				var decErr error
				apiToken, decErr = common.Decrypt(config.Value)
				if decErr != nil {
					return apiToken, baseURL, fmt.Errorf("failed to decrypt dynatrace api token: %w", decErr)
				}
			}
		case DynatraceConfigBaseUrl:
			baseURL = config.Value
		}
	}

	return apiToken, baseURL, nil
}
