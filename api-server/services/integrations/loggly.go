package integrations

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

var logglySubdomainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

const (
	LogglySubdomain = "subdomain"
	LogglyApiToken  = "api_token"
)

func init() {
	core.RegisterIntegration(Loggly{})
}

const IntegrationLoggly = "loggly"

type LogglyConfig struct {
	Subdomain string `json:"subdomain"`
	ApiToken  string `json:"api_token"`
}

type Loggly struct {
}

func (m Loggly) Name() string {
	return IntegrationLoggly
}

func (m Loggly) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m Loggly) BaseURL(config LogglyConfig) string {
	return fmt.Sprintf("https://%s.loggly.com", config.Subdomain)

}

type LogglyLogRequestBody struct {
}

type LogglyLogRequestParam struct {
	Query string `json:"q"`
	From  string `json:"from"`
	Until string `json:"until"`
	Size  int    `json:"size"`
}

func (m Loggly) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{LogglySubdomain, LogglyApiToken},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Name for this Loggly integration (e.g. 'Loggly — Production').",
				Default:     "",
				Priority:    100,
			},
			LogglySubdomain: {
				Type:        core.ToolSchemaTypeString,
				Description: "Your Loggly subdomain — the prefix in https://{subdomain}.loggly.com. Find it in the URL when you log in to Loggly.",
				Priority:    90,
			},
			LogglyApiToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Loggly API token with read access to events. Generate it in Loggly under Source Setup → Customer Tokens, or use an existing bearer token from Account Settings → API Tokens.",
				IsEncrypted: true,
				Priority:    80,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Nudgebee accounts that should use this Loggly connection for log queries.",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         70,
			},
			core.DefaultLogProvider: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Make Loggly the default log provider for the selected accounts.",
				Default:     false,
				Priority:    60,
			},
		},
	}
}

func (m *Loggly) ValidateLogglyConfig(cfg LogglyConfig) error {
	if cfg.Subdomain == "" {
		return fmt.Errorf("subdomain must not be empty")
	}
	if !logglySubdomainRegex.MatchString(cfg.Subdomain) {
		return fmt.Errorf("subdomain must be the prefix only (e.g. 'mycompany'), not a URL or domain")
	}
	if cfg.ApiToken == "" {
		return fmt.Errorf("api token must not be empty")
	}

	resp, err := common.HttpGet(
		fmt.Sprintf("%s/apiv2/events/iterate", m.BaseURL(cfg)),
		common.HttpWithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", cfg.ApiToken),
			"Accept":        "application/json",
		}),
		common.HttpWithQueryParams(map[string]string{"q": "*", "from": "-1h", "until": "now", "size": "1"}),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to Loggly API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
			return fmt.Errorf("Loggly API returned non-JSON response — verify subdomain and token")
		}
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("invalid Loggly API token (HTTP 401)")
	case http.StatusForbidden:
		return fmt.Errorf("insufficient permissions for Loggly API (HTTP 403)")
	default:
		return fmt.Errorf("Loggly API returned unexpected status: HTTP %d", resp.StatusCode)
	}
}

func GetLogglyConfigs(sc *security.RequestContext, accountId string) (LogglyConfig, error) {
	subdomain := ""
	apiToken := ""

	logglyIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationLoggly)
	if err != nil {
		return LogglyConfig{}, fmt.Errorf("failed to list loggly integration configs: %w", err)
	}
	if len(logglyIntegrations) == 0 {
		return LogglyConfig{}, fmt.Errorf("loggly integration not found for account: %s", accountId)
	}
	logglyIntegration := logglyIntegrations[0]
	for _, config := range logglyIntegration.Configs {
		switch config.Name {
		case LogglySubdomain:
			subdomain = config.Value
		case LogglyApiToken:
			apiToken = config.Value
			if config.IsEncrypted {
				var decErr error
				apiToken, decErr = common.Decrypt(config.Value)
				if decErr != nil {
					return LogglyConfig{}, fmt.Errorf("failed to decrypt loggly api token: %w", decErr)
				}
			}
		}

	}
	logglyConfig := LogglyConfig{
		Subdomain: subdomain,
		ApiToken:  apiToken,
	}

	return logglyConfig, nil
}

func (m Loggly) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	var errors []error
	cfg := LogglyConfig{}
	for _, c := range config {
		switch c.Name {
		case LogglySubdomain:
			cfg.Subdomain = c.Value
		case LogglyApiToken:
			cfg.ApiToken = c.Value
		}
	}

	if err := m.ValidateLogglyConfig(cfg); err != nil {
		errors = append(errors, fmt.Errorf("loggly config validation failed: %w", err))
	}
	return errors
}
