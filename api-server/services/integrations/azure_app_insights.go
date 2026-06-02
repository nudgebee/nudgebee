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
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			AzureAppInsightsAppID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App Insights App ID",
				IsEncrypted: false,
				Priority:    88,
				IsTestable:  true,
			},
			AppTenantID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App Tenant ID",
				IsEncrypted: false,
				Priority:    80,
				IsTestable:  true,
			},
			AppClientID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App client ID",
				IsEncrypted: false,
				Priority:    78,
				IsTestable:  true,
			},
			AppSecret: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure App secret",
				IsEncrypted: true,
				Priority:    76,
				IsTestable:  true,
			},
			TracesTableName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure trace table name",
				Default:     "dependencies",
				IsEncrypted: false,
				Priority:    52,
			},
			LogsTableName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure logs table name",
				Default:     "ContainerLogV2",
				IsEncrypted: false,
				Priority:    51,
			},
			LogsWorkbookID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Azure logs workbook id",
				IsEncrypted: false,
				Priority:    50,
				IsTestable:  true,
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name to Azure monitoring Integration",
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
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make azure app insights default log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
				IsTestable:       true,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make azure app insights default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         14,
				IsTestable:       true,
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
	resp, err := common.HttpPost(url,
		common.HttpWithBody(io.NopCloser(bytes.NewBufferString(payload))),
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(15*time.Second),
	)
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
		return nil, fmt.Errorf("failed to get token, status: %d, response: %s", resp.StatusCode, truncateBody(body, 256))
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
		slog.Error("azure_app_insights: failed to fetch oauth token", "error", err)
		return nil, err
	}
	header["Authorization"] = "Bearer " + tokenResponse.AccessToken
	return header, nil
}

// probeKQL runs a KQL `<table> | take 1` against the given query URL using a
// token obtained for `resource`. It owns body close, timeout, and status
// branching with a truncated body excerpt so callers get one-liner errors.
//
// `kind` is the user-facing label (e.g., "logs", "traces", "metrics") used in
// error messages so the failure points at which probe failed.
func (m *AzureAppInsights) probeKQL(cfg AzureAppInsightsConfig, resource, queryURL, table, kind string) error {
	headers, err := m.GetHeader(cfg, map[string]string{}, resource)
	if err != nil {
		return fmt.Errorf("%s probe: failed to get auth header: %w", kind, err)
	}
	body := map[string]string{"query": fmt.Sprintf("%s | take 1", table)}
	res, err := common.HttpPost(queryURL,
		common.HttpWithHeaders(headers),
		common.HttpWithJsonBody(body),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return fmt.Errorf("%s probe: failed to reach %s: %w", kind, queryURL, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(res.Body)
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s probe: token lacks access to %s (HTTP 401): %s", kind, resource, truncateBody(respBody, 256))
	case http.StatusForbidden:
		return fmt.Errorf("%s probe: token lacks permission for %s (HTTP 403): %s", kind, resource, truncateBody(respBody, 256))
	case http.StatusNotFound:
		return fmt.Errorf("%s probe: resource not found at %s (HTTP 404) — check ids: %s", kind, queryURL, truncateBody(respBody, 256))
	case http.StatusBadRequest:
		return fmt.Errorf("%s probe: bad request (HTTP 400) — likely a wrong table name: %s", kind, truncateBody(respBody, 256))
	default:
		return fmt.Errorf("%s probe: unexpected status HTTP %d: %s", kind, res.StatusCode, truncateBody(respBody, 256))
	}
}

func (m *AzureAppInsights) ValidateLogIntegration(cfg AzureAppInsightsConfig) error {
	queryURL := "https://api.loganalytics.io/v1/workspaces/" + cfg.LogsWorkbookID + "/query"
	return m.probeKQL(cfg, AzureAnalyticsURL, queryURL, cfg.LogsTableName, "logs")
}

func (m *AzureAppInsights) ValidateTracesIntegration(cfg AzureAppInsightsConfig) error {
	queryURL := "https://api.applicationinsights.io/v1/apps/" + cfg.AzureAppInsightsAppID + "/query"
	return m.probeKQL(cfg, AzureApplicationInsightURL, queryURL, cfg.TracesTableName, "traces")
}

// ValidateMetricsIntegration confirms the OAuth token has read access to App
// Insights by hitting /metadata (cheap, schema-only, no metric data scanned).
// Skipped when the trace probe already covered the same /apps/<id>/* surface
// to avoid a redundant call.
func (m *AzureAppInsights) ValidateMetricsIntegration(cfg AzureAppInsightsConfig) error {
	headers, err := m.GetHeader(cfg, map[string]string{}, AzureApplicationInsightURL)
	if err != nil {
		return fmt.Errorf("metrics probe: failed to get auth header: %w", err)
	}
	metadataURL := "https://api.applicationinsights.io/v1/apps/" + cfg.AzureAppInsightsAppID + "/metadata"
	res, err := common.HttpGet(metadataURL,
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return fmt.Errorf("metrics probe: failed to reach %s: %w", metadataURL, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(res.Body)
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("metrics probe: token lacks access to App Insights app %s (HTTP %d): %s", cfg.AzureAppInsightsAppID, res.StatusCode, truncateBody(respBody, 256))
	case http.StatusNotFound:
		return fmt.Errorf("metrics probe: App Insights app %q not found (HTTP 404): %s", cfg.AzureAppInsightsAppID, truncateBody(respBody, 256))
	default:
		return fmt.Errorf("metrics probe: unexpected status HTTP %d: %s", res.StatusCode, truncateBody(respBody, 256))
	}
}

// ValidateAzureAppInsightsConfig always probes the OAuth token endpoint to
// confirm the four core credentials work, then runs log/trace probes if their
// provider flags are on, then a cheap App Insights /metadata probe to round
// out the credential test when the trace probe wasn't already run.
func (m *AzureAppInsights) ValidateAzureAppInsightsConfig(cfg AzureAppInsightsConfig) error {
	// Baseline: OAuth token works for App Insights resource. If this fails,
	// nothing else will work, so short-circuit with the clearest possible error.
	if _, err := m.FetchAzureToken(cfg, AzureApplicationInsightURL); err != nil {
		return fmt.Errorf("OAuth token request failed (check app_tenant_id, app_client_id, app_secret): %w", err)
	}

	if cfg.EnableLogsIngestion {
		if err := m.ValidateLogIntegration(cfg); err != nil {
			return err
		}
	}
	if cfg.EnableTracesIngestion {
		if err := m.ValidateTracesIntegration(cfg); err != nil {
			return err
		}
	} else {
		// Trace probe wasn't run; do a cheap /metadata probe to confirm the
		// App Insights app id exists and the token can read it.
		if err := m.ValidateMetricsIntegration(cfg); err != nil {
			return err
		}
	}
	return nil
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

// requiredFieldErr returns a "<name> is required" error. Centralised so the
// literal lives in one place (matches go:S1192 lint rule).
func requiredFieldErr(name string) error {
	return fmt.Errorf("%s is required", name)
}

// unmarshalAzureConfig flattens IntegrationConfigValues into a typed config.
func unmarshalAzureConfig(config []core.IntegrationConfigValue) AzureAppInsightsConfig {
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
	return cfg
}

func (m AzureAppInsights) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	cfg := unmarshalAzureConfig(config)

	// Required-field validation before any network call so users see all
	// missing-field errors in one save round-trip instead of "OAuth failed".
	var errs []error
	if cfg.AzureAppInsightsAppID == "" {
		errs = append(errs, requiredFieldErr(AzureAppInsightsAppID))
	}
	if cfg.AppTenantID == "" {
		errs = append(errs, requiredFieldErr(AppTenantID))
	}
	if cfg.AppClientID == "" {
		errs = append(errs, requiredFieldErr(AppClientID))
	}
	if cfg.AppSecret == "" {
		errs = append(errs, requiredFieldErr(AppSecret))
	}
	if cfg.EnableLogsIngestion {
		if cfg.LogsTableName == "" {
			errs = append(errs, fmt.Errorf("%s is required when default_log_provider is enabled", LogsTableName))
		}
		if cfg.LogsWorkbookID == "" {
			errs = append(errs, fmt.Errorf("%s is required when default_log_provider is enabled", LogsWorkbookID))
		}
	}
	if cfg.EnableTracesIngestion && cfg.TracesTableName == "" {
		errs = append(errs, fmt.Errorf("%s is required when default_trace_provider is enabled", TracesTableName))
	}
	if len(errs) > 0 {
		return errs
	}

	if err := m.ValidateAzureAppInsightsConfig(cfg); err != nil {
		return []error{err}
	}
	return nil
}
