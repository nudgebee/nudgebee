package integrations

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	SolarWindsConfigAPIToken   = "api_token"
	SolarWindsConfigDataCenter = "data_center"
	SolarWindsDataCenterNA01   = "na-01"
	SolarWindsDataCenterNA02   = "na-02"
	SolarWindsDataCenterEU01   = "eu-01"
	SolarWindsDataCenterAP01   = "ap-01"
	IntegrationSolarWinds      = "solarwinds"
)

func init() {
	core.RegisterIntegration(SolarWinds{})
}

type SolarWinds struct{}

func (m SolarWinds) Name() string {
	return IntegrationSolarWinds
}

func (m SolarWinds) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m SolarWinds) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{SolarWindsConfigAPIToken, SolarWindsConfigDataCenter},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name for SolarWinds Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			SolarWindsConfigAPIToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "SolarWinds API Access Token (from Settings > API Tokens)",
				IsEncrypted: true,
				Priority:    90,
				IsTestable:  true,
			},
			SolarWindsConfigDataCenter: {
				Type:        core.ToolSchemaTypeString,
				Description: "SolarWinds Data Center Region",
				Enum:        []any{SolarWindsDataCenterNA01, SolarWindsDataCenterNA02, SolarWindsDataCenterEU01, SolarWindsDataCenterAP01},
				Default:     SolarWindsDataCenterNA01,
				Priority:    85,
				IsTestable:  true,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make SolarWinds default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         30,
			},
			// core.DefaultTraceProvider: {
			// 	Type:             core.ToolSchemaTypeBoolean,
			// 	Description:      "Make SolarWinds default Trace Provider",
			// 	Default:          false,
			// 	AutoGenerateFunc: "",
			// 	Priority:         25,
			// },
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make SolarWinds default Metric Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         24,
			},
		},
	}
}

func (m SolarWinds) ValidateConfig(secCtx *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	apiToken := ""
	dataCenter := SolarWindsDataCenterNA01

	for _, config := range integrationConfig {
		switch config.Name {
		case SolarWindsConfigAPIToken:
			apiToken = config.Value
			if config.IsEncrypted {
				var err error
				apiToken, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("solarwinds config validation failed: %w", err)}
				}
			}
		case SolarWindsConfigDataCenter:
			dataCenter = config.Value
		}
	}

	if err := ValidateSolarWindsConfig(apiToken, dataCenter); err != nil {
		return []error{fmt.Errorf("solarwinds config validation failed: %w", err)}
	}
	return nil
}

// ValidateSolarWindsConfig tests the API token against the metrics list endpoint.
// A 200 response confirms the token is valid and has API Access (not just Ingestion) permissions.
func ValidateSolarWindsConfig(apiToken, dataCenter string) error {
	if apiToken == "" {
		return errors.New("api token must not be empty")
	}
	if !isValidSolarWindsDataCenter(dataCenter) {
		return fmt.Errorf("invalid data center %q, must be one of: na-01, na-02, eu-01, ap-01", dataCenter)
	}

	baseURL := SolarWindsAPIBaseURL(dataCenter)
	_, statusCode, err := DoSolarWindsGET(apiToken, baseURL, "/v1/metrics", map[string]string{"pageSize": "1"})
	if err != nil {
		return fmt.Errorf("failed to connect to SolarWinds API: %w", err)
	}
	switch statusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return errors.New("invalid credentials: unauthorized (401)")
	case http.StatusForbidden:
		return errors.New("insufficient permissions (403): use an API Access token, not an Ingestion token")
	default:
		return fmt.Errorf("unexpected response from SolarWinds API: HTTP %d", statusCode)
	}
}

// GetSolarWindsConfigs retrieves and decrypts the SolarWinds configuration for an account.
// Returns (apiToken, dataCenter, error).
func GetSolarWindsConfigs(sc *security.RequestContext, accountId string) (string, string, error) {
	apiToken := ""
	dataCenter := SolarWindsDataCenterNA01

	swIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationSolarWinds)
	if err != nil {
		return apiToken, dataCenter, fmt.Errorf("failed to list SolarWinds integration configs: %w", err)
	}
	if len(swIntegrations) == 0 {
		return apiToken, dataCenter, fmt.Errorf("solarWinds integration not found for account: %s", accountId)
	}

	// Use the first configured integration, consistent with other provider GetXxxConfigs functions
	// (e.g. GetNewRelicConfigs, GetDatadogConfigs). Accounts typically configure one instance.
	swIntegration := swIntegrations[0]
	for _, config := range swIntegration.Configs {
		switch config.Name {
		case SolarWindsConfigAPIToken:
			apiToken = config.Value
			if config.IsEncrypted {
				var err error
				apiToken, err = common.Decrypt(config.Value)
				if err != nil {
					return apiToken, dataCenter, fmt.Errorf("failed to decrypt SolarWinds API token: %w", err)
				}
			}
		case SolarWindsConfigDataCenter:
			dataCenter = config.Value
		}
	}

	return apiToken, dataCenter, nil
}

// SolarWindsAPIBaseURL returns the base REST API URL for the given data center.
// e.g. "https://api.ap-01.cloud.solarwinds.com"
func SolarWindsAPIBaseURL(dataCenter string) string {
	return fmt.Sprintf("https://api.%s.cloud.solarwinds.com", dataCenter)
}

// DoSolarWindsGET performs an authenticated GET request to the SolarWinds REST API.
// Returns (body, httpStatusCode, error).
func DoSolarWindsGET(apiToken, baseURL, path string, params map[string]string) ([]byte, int, error) {
	url := baseURL + path
	headers := map[string]string{
		"Authorization": "Bearer " + apiToken,
		"Accept":        "application/json",
	}
	resp, err := common.HttpGet(url, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params))
	if err != nil {
		return nil, 0, fmt.Errorf("request to SolarWinds API failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read SolarWinds response body: %w", err)
	}
	return body, resp.StatusCode, nil
}

func isValidSolarWindsDataCenter(dc string) bool {
	switch dc {
	case SolarWindsDataCenterNA01, SolarWindsDataCenterNA02, SolarWindsDataCenterEU01, SolarWindsDataCenterAP01:
		return true
	}
	return false
}
