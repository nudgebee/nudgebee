package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strconv"
	"sync"
	"time"
)

const (
	AzureAppInsightsApiSecret  = "azure_app_insights_api_secret"
	AzureAppInsightsAppID      = "azure_app_insights_app_id"
	AppClientID                = "app_client_id"
	AppSecret                  = "app_secret"
	AppTenantID                = "app_tenant_id"
	TracesTableName            = "traces_table_name"
	LogsTableName              = "logs_table_name"
	LogsWorkbookID             = "log_workbook_id"
	AzureAnalyticsURL          = "https://api.loganalytics.io"
	AzureManagementURL         = "https://management.azure.com/"
	AzureMonitoringURL         = "https://monitoring.azure.com/"
	AzureApplicationInsightURL = "https://api.applicationinsights.io/"
)

func init() {
	core.RegisterIntegration(AzureAppInsights{})
}

type AzureTokenResponse struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    string `json:"expires_in"`
	ExtExpiresIn string `json:"ext_expires_in"`
	ExpiresOn    string `json:"expires_on"`
	NotBefore    string `json:"not_before"`
	Resource     string `json:"resource"`
	AccessToken  string `json:"access_token"`
}

const IntegrationAzureAppInsights = "azure_app_insights"

// TokenCacheEntry stores token + expiry
type TokenCacheEntry struct {
	Token  *AzureTokenResponse
	Expiry time.Time
}

// Global cache (per resource)
var (
	tokenCache = make(map[string]*TokenCacheEntry)
	mu         sync.Mutex
)

type AzureAppInsightsConfig struct {
	AzureAppInsightsApiSecret string `json:"azure_app_insights_api_secret"`
	AzureAppInsightsAppID     string `json:"azure_app_insights_app_id"`
	AppClientID               string `json:"app_client_id"`
	AppSecret                 string `json:"app_secret"`
	AppTenantID               string `json:"app_tenant_id"`
	LogsWorkbookID            string `json:"log_workbook_id"`
	MetricsTableName          string `json:"metrics_table_name" default:"dependencies"`
	TracesTableName           string `json:"traces_table_name" default:"dependencies"`
	LogsTableName             string `json:"logs_table_name" default:"ContainerLogV2"`
	EnableLogsIngestion       bool   `json:"enable_logs_ingestion" default:"false"`
	EnableTracesIngestion     bool   `json:"enable_traces_ingestion" default:"false"`
}

type AzureAppInsights struct {
}

func (m AzureAppInsights) Name() string {
	return IntegrationAzureAppInsights
}

func (m AzureAppInsights) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m AzureAppInsights) BaseURL(config AzureAppInsightsConfig) string {
	return fmt.Sprintf("https://api.applicationinsights.io/v1/apps/%s/query", config.AzureAppInsightsAppID)

}

func (m AzureAppInsights) TokenBaseURL(config AzureAppInsightsConfig) string {
	return fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/token", config.AppTenantID)

}

func (m AzureAppInsights) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{AzureAppInsightsAppID, AppClientID, AppSecret, AppTenantID},
		Properties: map[string]core.IntegrationSchemaProperty{
			AzureAppInsightsAppID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App Insights App ID",
				IsEncrypted: false,
			},
			AppClientID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App client ID",
				IsEncrypted: false,
			},
			AppSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App secret",
				IsEncrypted: true,
			},
			AppTenantID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App Tenant ID",
				IsEncrypted: false,
			},
			TracesTableName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure trace table name",
				Default:     "dependencies",
				IsEncrypted: false,
			},
			LogsTableName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure logs table name",
				Default:     "ContainerLogV2",
				IsEncrypted: false,
			},
			LogsWorkbookID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure logs workbook id",
				IsEncrypted: false,
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name to Azure monitoring Integration",
				Default:          "",
				AutoGenerateFunc: "",
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make azure app insights default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make azure app insights default log Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
			},
		},
	}
}

func (m AzureAppInsights) FetchAzureToken(conf AzureAppInsightsConfig, resource string) (*AzureTokenResponse, error) {
	// Prepare form data
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", conf.AppClientID)
	data.Set("client_secret", conf.AppSecret)
	data.Set("resource", resource)
	payload := data.Encode()
	url := m.TokenBaseURL(conf)
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

	// Make HTTP request
	resp, err := common.HttpPost(url, common.HttpWithBody(io.NopCloser(bytes.NewBufferString(payload))), common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	defer func() {
		err := resp.Body.Close()
		if err != nil {
			slog.Error("Error closing response body", "error", err)
		}
	}()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get token, status: %d, response: %s", resp.StatusCode, string(body))
	}

	// Parse JSON response
	var tokenResp AzureTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token JSON: %w", err)
	}

	return &tokenResp, nil
}

func (m AzureAppInsights) GetAzureToken(azureConfig AzureAppInsightsConfig, resource string) (*AzureTokenResponse, error) {
	mu.Lock()
	defer mu.Unlock()

	// Check cache
	cacheKey := resource + "-" + azureConfig.AppClientID
	if entry, ok := tokenCache[cacheKey]; ok && time.Now().Before(entry.Expiry) {
		return entry.Token, nil
	}

	// Fetch new token
	token, err := m.FetchAzureToken(azureConfig, resource)
	if err != nil {
		return nil, err
	}

	// Compute expiry time (with 60s buffer)
	expiresIn, err := strconv.Atoi(token.ExpiresIn)
	expiry := time.Now().Add(1 * time.Hour) // default fallback
	if err == nil {
		expiry = time.Now().Add(time.Duration(expiresIn-60) * time.Second)
	}

	// Store in cache
	tokenCache[cacheKey] = &TokenCacheEntry{
		Token:  token,
		Expiry: expiry,
	}

	return token, nil
}

func (m *AzureAppInsights) GetHeader(cnf AzureAppInsightsConfig, header map[string]string, resource string) (map[string]string, error) {
	if cnf.AzureAppInsightsApiSecret != "" && cnf.EnableTracesIngestion {
		header["x-api-key"] = cnf.AzureAppInsightsApiSecret
		return header, nil
	}
	tokenResponse, err := m.GetAzureToken(cnf, resource)
	if err != nil {
		// Handle the error appropriately, e.g., log it or return
		fmt.Printf("Error fetching token: %v\n", err)
		return nil, err
	}
	header["Authorization"] = "Bearer " + tokenResponse.AccessToken
	return header, nil

}

func (m *AzureAppInsights) ValidateLogIntegration(cfg AzureAppInsightsConfig) error {
	headers, err := m.GetHeader(cfg, map[string]string{}, AzureAnalyticsURL)
	if err != nil {
		return fmt.Errorf("failed to get auth header: %w", err)
	}
	traceApiRequest := map[string]string{
		"query": fmt.Sprintf("%s | take 1", cfg.LogsTableName),
	}
	logAnalyticsURL := "https://api.loganalytics.io/v1/workspaces/" + cfg.LogsWorkbookID + "/query"
	res, err := common.HttpPost(logAnalyticsURL, common.HttpWithHeaders(headers), common.HttpWithJsonBody(traceApiRequest))

	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: status=%d body=%s", res.StatusCode, res.Body)
	}
	return nil
}

func (m *AzureAppInsights) ValidateTracesIntegration(cfg AzureAppInsightsConfig) error {
	headers, err := m.GetHeader(cfg, map[string]string{}, AzureApplicationInsightURL)
	if err != nil {
		return fmt.Errorf("failed to get auth header: %w", err)
	}
	traceApiRequest := map[string]string{
		"query": fmt.Sprintf("%s | take 1", cfg.TracesTableName),
	}
	tracesAnalyticsURL := "https://api.applicationinsights.io/v1/apps/" + cfg.AzureAppInsightsAppID + "/query"

	res, err := common.HttpPost(tracesAnalyticsURL, common.HttpWithHeaders(headers), common.HttpWithJsonBody(traceApiRequest))

	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: status=%d body=%s", res.StatusCode, res.Body)
	}
	return nil
}

func (m *AzureAppInsights) ValidateMetricsIntegration(cfg AzureAppInsightsConfig) error {
	return nil
}

func (m *AzureAppInsights) ValidateAzureAppInsightsConfig(cfg AzureAppInsightsConfig) error {
	err := error(nil)
	if cfg.EnableLogsIngestion {
		if err := m.ValidateLogIntegration(cfg); err != nil {
			return err
		}
	}
	if cfg.EnableTracesIngestion {
		if err := m.ValidateTracesIntegration(cfg); err != nil {
			return err
		}
	}

	return err
}

func GetAzureAppInsightConfigs(sc *security.RequestContext, accountId string) (AzureAppInsightsConfig, error) {
	appInsightsIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationAzureAppInsights)
	if err != nil {
		return AzureAppInsightsConfig{}, fmt.Errorf("failed to list azure integration configs: %w", err)
	}
	if len(appInsightsIntegrations) == 0 {
		return AzureAppInsightsConfig{}, fmt.Errorf("azure integration not found for account: %s", accountId)
	}
	appInsightsIntegration := appInsightsIntegrations[0]
	cfg := AzureAppInsightsConfig{}
	for _, config := range appInsightsIntegration.Configs {
		switch config.Name {
		case AzureAppInsightsAppID:
			cfg.AzureAppInsightsAppID = config.Value
		case AppClientID:
			cfg.AppClientID = config.Value
		case AppSecret:
			appSecret := config.Value
			if config.IsEncrypted && config.Value != "" {
				appSecret, err = common.Decrypt(config.Value)
				if err != nil {
					return cfg, fmt.Errorf("failed to decrypt azure app secret: %w", err)
				}
			}
			cfg.AppSecret = appSecret
		case AppTenantID:
			cfg.AppTenantID = config.Value
		case TracesTableName:
			cfg.TracesTableName = config.Value
		case LogsTableName:
			cfg.LogsTableName = config.Value
		case core.DefaultLogProvider:
			cfg.EnableLogsIngestion, _ = strconv.ParseBool(config.Value)
		case core.DefaultTraceProvider:
			cfg.EnableTracesIngestion, _ = strconv.ParseBool(config.Value)
		case LogsWorkbookID:
			cfg.LogsWorkbookID = config.Value
		}
	}

	return cfg, nil
}

func (m AzureAppInsights) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	var errors []error
	cfg := AzureAppInsightsConfig{}
	for _, c := range config {
		switch c.Name {
		case AzureAppInsightsApiSecret:
			cfg.AzureAppInsightsApiSecret = c.Value
		case AzureAppInsightsAppID:
			cfg.AzureAppInsightsAppID = c.Value
		case AppClientID:
			cfg.AppClientID = c.Value
		case AppSecret:
			cfg.AppSecret = c.Value
		case AppTenantID:
			cfg.AppTenantID = c.Value
		case TracesTableName:
			cfg.TracesTableName = c.Value
		case LogsTableName:
			cfg.LogsTableName = c.Value
		case core.DefaultLogProvider:
			cfg.EnableLogsIngestion, _ = strconv.ParseBool(c.Value)
		case core.DefaultTraceProvider:
			cfg.EnableTracesIngestion, _ = strconv.ParseBool(c.Value)
		case LogsWorkbookID:
			cfg.LogsWorkbookID = c.Value

		}

	}

	if err := m.ValidateAzureAppInsightsConfig(cfg); err != nil {
		errors = append(errors, fmt.Errorf("azure app insights config validation failed: %w", err))
	}
	return errors
}
