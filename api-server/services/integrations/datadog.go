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
	"sync"
)

const (
	DatadogConfigApiKey = "api_key"
	DatadogConfigAppKey = "app_key"
	DatadogConfigSite   = "site"
)

func init() {
	core.RegisterIntegration(Datadog{})
}

const IntegrationDatadog = "datadog"

type Datadog struct {
}

func (m Datadog) Name() string {
	return IntegrationDatadog
}

func (m Datadog) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m Datadog) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{DatadogConfigApiKey, DatadogConfigAppKey, DatadogConfigSite},
		Properties: map[string]core.IntegrationSchemaProperty{
			DatadogConfigApiKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "Datadog API Key",
				IsEncrypted: true,
				IsTestable:  true,
				Priority:    70,
			},
			DatadogConfigAppKey: {
				Type:        core.ToolSchemaTypeString,
				Description: "Datadog App Key",
				IsEncrypted: true,
				IsTestable:  true,
				Priority:    68,
			},
			DatadogConfigSite: {
				Type:        core.ToolSchemaTypeString,
				Description: "Datadog Site",
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
				Description:      "Name to Datadog Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Datadog default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Datadog default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         13,
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Datadog default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         11,
			},
		},
	}
}

func (m *Datadog) ValidateDatadogConfig(apiKey, appKey, site string) error {
	if apiKey == "" {
		return errors.New("api key must not be empty")
	}
	if appKey == "" {
		return errors.New("application key must not be empty")
	}
	if site == "" {
		return errors.New("site must not be empty")
	}

	site = NormalizeDatadogSite(site)

	// Step 1: Validate API key using Datadog's dedicated validation endpoint.
	// This endpoint only requires a valid API key (no special role/permissions).
	validateURL := fmt.Sprintf("https://%s/api/v1/validate", site)
	headers := map[string]string{
		"Accept":     "application/json",
		"DD-API-KEY": apiKey,
	}

	res, err := common.HttpGet(validateURL, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("could not connect to Datadog at %q, verify the site URL is correct (e.g., app.datadoghq.com, app.datadoghq.eu, us3.datadoghq.com)", site)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			fmt.Printf("error closing response body: %v\n", err)
		}
	}()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read validation response: %w", err)
	}

	if isHTMLResponse(body) {
		return fmt.Errorf("the site URL %q is not a valid Datadog API endpoint. Use a Datadog site like app.datadoghq.com, app.datadoghq.eu, or us3.datadoghq.com", site)
	}

	switch res.StatusCode {
	case http.StatusOK:
		// API key is valid, proceed to validate Application key
	case http.StatusForbidden:
		return errors.New("invalid API key, verify your Datadog API key is correct and active")
	default:
		return fmt.Errorf("API key validation failed (HTTP %d), verify your Datadog credentials and site URL", res.StatusCode)
	}

	// Step 2: Validate Application key and data access permissions in parallel.
	// The previous endpoint (/api/v2/application_keys) required admin permissions.
	// Now we check actual data endpoints the integration uses (logs, metrics, traces).
	headers["DD-APPLICATION-KEY"] = appKey

	type permCheck struct {
		name   string
		status int
		err    error
	}

	checks := []struct {
		name string
		url  string
	}{
		{"Logs", fmt.Sprintf("https://%s/api/v2/logs/events?filter[query]=*&filter[from]=now-1m&filter[to]=now&page[limit]=1", site)},
		{"Metrics", fmt.Sprintf("https://%s/api/v2/metrics?window[seconds]=3600", site)},
		{"Traces", fmt.Sprintf("https://%s/api/v2/spans/events?filter[query]=*&filter[from]=now-1m&filter[to]=now&page[limit]=1", site)},
	}

	results := make([]permCheck, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(idx int, name, checkURL string) {
			defer wg.Done()
			res, err := common.HttpGet(checkURL, common.HttpWithHeaders(headers))
			if err != nil {
				results[idx] = permCheck{name: name, err: err}
				return
			}
			defer func() {
				_ = res.Body.Close()
			}()
			_, _ = io.ReadAll(res.Body)
			results[idx] = permCheck{name: name, status: res.StatusCode}
		}(i, c.name, c.url)
	}
	wg.Wait()

	// Categorize results
	var forbidden []string
	var networkErrors int
	anySuccess := false
	for _, r := range results {
		if r.err != nil {
			networkErrors++
			continue
		}
		switch r.status {
		case http.StatusOK:
			anySuccess = true
		case http.StatusForbidden, http.StatusUnauthorized:
			forbidden = append(forbidden, r.name)
		}
	}

	// If all requests failed with network errors, report connectivity issue
	if networkErrors == len(checks) {
		return errors.New("could not verify application key permissions: all permission checks failed due to network errors")
	}

	// If ALL endpoints returned 403/401, the app key itself is likely invalid
	// (Datadog returns 403 for both invalid keys and missing permissions).
	if !anySuccess && len(forbidden) > 0 && networkErrors == 0 {
		return errors.New("invalid application key or insufficient permissions, verify your Datadog application key is correct and has at least Reader role access")
	}

	// If some succeed and some fail, report exactly which permissions are missing
	if anySuccess && len(forbidden) > 0 {
		return fmt.Errorf("application key is missing permissions for: %s, ensure the key has access to these data types", strings.Join(forbidden, ", "))
	}

	return nil
}

// NormalizeDatadogSite cleans up common site URL formatting issues.
// Strips protocol prefixes, trailing slashes, and auto-corrects bare Datadog
// domains (e.g. "datadoghq.com") to their app-prefixed API-serving equivalents.
func NormalizeDatadogSite(site string) string {
	site = strings.TrimSpace(site)
	site = strings.TrimPrefix(site, "https://")
	site = strings.TrimPrefix(site, "http://")
	site = strings.TrimRight(site, "/")
	site = strings.TrimPrefix(site, "www.")

	// Bare Datadog domains don't serve the API — they return the marketing site HTML.
	// Prefix with "app." which serves both the web UI and the API.
	switch site {
	case "datadoghq.com":
		return "app.datadoghq.com"
	case "datadoghq.eu":
		return "app.datadoghq.eu"
	case "ddog-gov.com":
		return "app.ddog-gov.com"
	}

	return site
}

// isHTMLResponse checks if a response body appears to be HTML rather than JSON.
func isHTMLResponse(body []byte) bool {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	return strings.HasPrefix(trimmed, "<!") ||
		strings.HasPrefix(strings.ToLower(trimmed), "<html")
}

func (m Datadog) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	apiKey := ""
	appKey := ""
	site := ""
	var errors []error
	for _, config := range integrationConfig {
		switch config.Name {
		case DatadogConfigApiKey:
			apiKey = config.Value
			if config.IsEncrypted {
				var err error
				apiKey, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("datadog config validation failed: %w", err)}
				}
			}
		case DatadogConfigAppKey:
			appKey = config.Value
			if config.IsEncrypted {
				var err error
				appKey, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("datadog config validation failed: %w", err)}
				}
			}
		case DatadogConfigSite:
			site = config.Value
		}
	}

	if err := m.ValidateDatadogConfig(apiKey, appKey, site); err != nil {
		errors = []error{fmt.Errorf("datadog config validation failed: %w", err)}
	}
	return errors
}

func GetDatadogConfigs(sc *security.RequestContext, accountId string) (string, string, string, error) {
	apiKey := ""
	appKey := ""
	site := ""

	datadogIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationDatadog)
	if err != nil {
		return apiKey, appKey, site, fmt.Errorf("failed to list datadog integration configs: %w", err)
	}
	if len(datadogIntegrations) == 0 {
		return apiKey, appKey, site, fmt.Errorf("datadog integration not found for account: %s", accountId)
	}
	datadogIntegration := datadogIntegrations[0]
	for _, config := range datadogIntegration.Configs {
		switch config.Name {
		case DatadogConfigApiKey:
			apiKey = config.Value
			if config.IsEncrypted {
				var err error
				apiKey, err = common.Decrypt(config.Value)
				if err != nil {
					return apiKey, appKey, site, fmt.Errorf("failed to decrypt datadog api key: %w", err)
				}
			}
		case DatadogConfigAppKey:
			appKey = config.Value
			if config.IsEncrypted {
				var err error
				appKey, err = common.Decrypt(config.Value)
				if err != nil {
					return apiKey, appKey, site, fmt.Errorf("failed to decrypt datadog app key: %w", err)
				}
			}
		case DatadogConfigSite:
			site = config.Value
		}
	}

	site = NormalizeDatadogSite(site)

	return apiKey, appKey, site, nil
}

type DatadogLog struct {
	Type       string               `json:"type"`
	ID         string               `json:"id"`
	Attributes DatadogLogAttributes `json:"attributes"`
}

type DatadogLogAttributes struct {
	Host      string   `json:"host"`
	Message   string   `json:"message"`
	Service   string   `json:"service"`
	Status    string   `json:"status"`
	Timestamp string   `json:"timestamp"`
	Tags      []string `json:"tags"`
}
