package playbooks

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"strings"
	"time"
)

type datadogMonitorsSearchAction struct{}

// Request parameters
type datadogMonitorsSearchParams struct {
	Status   string `json:"status"`             // Monitor status (e.g., "alert", "warn", "no data")
	Env      string `json:"env,omitempty"`      // Environment tag filter
	Query    string `json:"query,omitempty"`    // Custom query string (optional)
	Limit    int    `json:"limit,omitempty"`    // Number of results per page (default: 30, max: 100)
	Service  string `json:"service,omitempty"`  // Service tag filter
	Duration int    `json:"duration,omitempty"` // Duration in hours to look back for triggered monitors
}

// Datadog API response structures
type datadogMonitorsSearchResponse struct {
	Metadata datadogMetadata  `json:"metadata"`
	Counts   datadogCounts    `json:"counts"`
	Monitors []datadogMonitor `json:"monitors"`
}

type datadogMetadata struct {
	TotalCount int `json:"total_count"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	PageCount  int `json:"page_count"`
}

type datadogCounts struct {
	Status []datadogCountItem `json:"status"`
	Type   []datadogCountItem `json:"type"`
	Tag    []datadogCountItem `json:"tag"`
	Muted  []datadogCountItem `json:"muted"`
}

type datadogCountItem struct {
	Name  interface{} `json:"name"` // Can be string or bool
	Count int         `json:"count"`
}

type datadogMonitor struct {
	ID                   int64                 `json:"id"`
	OrgID                int64                 `json:"org_id"`
	Name                 string                `json:"name"`
	Type                 string                `json:"type"`
	Classification       string                `json:"classification"`
	Status               string                `json:"status"`
	OverallStateModified int64                 `json:"overall_state_modified"`
	Metrics              []string              `json:"metrics"`
	Scopes               []string              `json:"scopes"`
	Notifications        []datadogNotification `json:"notifications"`
	Creator              datadogCreator        `json:"creator"`
	MutedUntilTs         *int64                `json:"muted_until_ts"`
	LastTriggeredTs      int64                 `json:"last_triggered_ts"`
	Query                string                `json:"query"`
	RestrictedRoles      []string              `json:"restricted_roles"`
	Priority             *int                  `json:"priority"`
	Created              int64                 `json:"created"`
	Modified             int64                 `json:"modified"`
	SchedulingType       string                `json:"scheduling_type"`
	EvaluationWindowType string                `json:"evaluation_window_type"`
	DraftStatus          string                `json:"draft_status"`
	QualityIssues        []string              `json:"quality_issues"`
	Tags                 []string              `json:"tags"`
}

type datadogNotification struct {
	Name   string `json:"name"`
	Handle string `json:"handle"`
}

type datadogCreator struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
}

// Response structure for playbook
type monitorsSearchResponse struct {
	TotalMonitors int              `json:"total_monitors"`
	Status        string           `json:"status"`
	Environment   string           `json:"environment,omitempty"`
	Monitors      []monitorSummary `json:"monitors"`
}

type monitorSummary struct {
	ID             int64                 `json:"id"`
	Name           string                `json:"name"`
	Type           string                `json:"type"`
	Classification string                `json:"classification"`
	Status         string                `json:"status"`
	LastTriggered  string                `json:"last_triggered,omitempty"`
	Query          string                `json:"query"`
	Metrics        []string              `json:"metrics"`
	Scopes         []string              `json:"scopes"`
	Notifications  []datadogNotification `json:"notifications"`
	Tags           []string              `json:"tags"`
	QualityIssues  []string              `json:"quality_issues"`
	Creator        string                `json:"creator"`
	IsMuted        bool                  `json:"is_muted"`
}

// Implement PlaybookAutoAction interface
func (a *datadogMonitorsSearchAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	source := ctx.GetEvent().Source
	return source == "datadog_webhook" || source == "datadog"
}

func (a *datadogMonitorsSearchAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	return a.Execute(ctx, map[string]any{
		"status": "alert",
	})
}

// Implement PlaybookAction interface
func (a *datadogMonitorsSearchAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	// Parse and validate parameters
	params, err := parseDatadogMonitorsParams(ctx, rawParams)
	if err != nil {
		return nil, err
	}

	// Set defaults
	if params.Status == "" {
		params.Status = "alert"
	}
	if params.Limit <= 0 {
		params.Limit = 30
	}
	if params.Limit > 100 {
		params.Limit = 100
	}

	// Get Datadog credentials from integration config
	accountId := ctx.GetAccountId()
	apiKey, appKey, site, err := getDatadogConfigsForAccount(accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get Datadog configuration: %w", err)
	}

	// Build query string
	query := buildDatadogQuery(params)

	// Fetch monitors from Datadog API
	monitors, metadata, err := fetchDatadogMonitors(apiKey, appKey, site, query, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch monitors from Datadog: %w", err)
	}

	// Build response - include all monitors matching the query (status/env/service filters
	// are already applied by the Datadog API). No additional time-window filtering since
	// monitors currently in alert state are inherently relevant to the event.
	response := monitorsSearchResponse{
		Status:      params.Status,
		Environment: params.Env,
		Monitors:    []monitorSummary{},
	}

	for _, monitor := range monitors {
		var lastTriggeredStr string
		if monitor.LastTriggeredTs > 0 {
			lastTriggeredStr = time.Unix(monitor.LastTriggeredTs, 0).Format(time.RFC3339)
		}

		summary := monitorSummary{
			ID:             monitor.ID,
			Name:           monitor.Name,
			Type:           monitor.Type,
			Classification: monitor.Classification,
			Status:         monitor.Status,
			Query:          monitor.Query,
			Metrics:        monitor.Metrics,
			Scopes:         monitor.Scopes,
			Notifications:  monitor.Notifications,
			Tags:           monitor.Tags,
			QualityIssues:  monitor.QualityIssues,
			Creator:        monitor.Creator.Name,
			IsMuted:        monitor.MutedUntilTs != nil && *monitor.MutedUntilTs > time.Now().Unix(),
			LastTriggered:  lastTriggeredStr,
		}

		response.Monitors = append(response.Monitors, summary)
	}
	response.TotalMonitors = len(response.Monitors)

	// Generate insights from the same monitors shown in the response
	insights := generateMonitorInsights(monitors, params)

	// Generate labels for correlation
	labels := map[string]any{
		"monitor_count": len(response.Monitors),
		"status":        params.Status,
	}

	if params.Env != "" {
		labels["environment"] = params.Env
	}

	metadata_map := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
		"datadog_query":        query,
		"total_count":          metadata.TotalCount,
	}

	additionalInfo := map[string]any{
		"monitors_returned": len(response.Monitors),
		"monitors_total":    metadata.TotalCount,
		"page":              metadata.Page,
		"per_page":          metadata.PerPage,
	}

	return NewPlaybookActionResponseJsonWithLabels(response, additionalInfo, insights, metadata_map, labels), nil
}

// Helper function to parse parameters
func parseDatadogMonitorsParams(ctx PlaybookActionContext, rawParams map[string]any) (datadogMonitorsSearchParams, error) {
	params := datadogMonitorsSearchParams{}

	if status, ok := rawParams["status"].(string); ok {
		params.Status = status
	}
	if env, ok := ctx.GetEvent().Labels["env"]; ok {
		params.Env = env
	}

	if service, ok := ctx.GetEvent().Labels["service"]; ok {
		params.Service = service
	}

	if env, ok := rawParams["env"].(string); ok {
		params.Env = env
	}

	switch v := rawParams["duration"].(type) {
	case float64:
		params.Duration = int(v)
	case int:
		params.Duration = v
	case int64:
		params.Duration = int(v)
	}

	if query, ok := rawParams["query"].(string); ok {
		params.Query = query
	}

	if limit, ok := rawParams["limit"].(float64); ok {
		params.Limit = int(limit)
	} else if limit, ok := rawParams["limit"].(int); ok {
		params.Limit = limit
	}

	return params, nil
}

// Helper function to get Datadog configs for an account
// This function retrieves the Datadog integration configuration directly from the database
// using SQL queries similar to core.ListIntegrationConfigs to avoid import cycles
func getDatadogConfigsForAccount(accountId string) (apiKey, appKey, site string, err error) {
	const datadogIntegrationName = "datadog"
	const apiKeyConfigName = "api_key"
	const appKeyConfigName = "app_key"
	const siteConfigName = "site"

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("integrations: failed to get database manager", "error", err)
		return apiKey, appKey, site, err
	}

	// Query to get Datadog integration for the account
	query := `
		SELECT i.id::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica
		  ON i.id = ica.integration_id
		WHERE i.type = $1
		  AND ica.cloud_account_id = $2
		LIMIT 1
	`

	var integrationId string
	err = dbms.Db.QueryRowx(query, datadogIntegrationName, accountId).Scan(&integrationId)
	if err != nil {
		return apiKey, appKey, site, fmt.Errorf("datadog integration not found for account %s: %w", accountId, err)
	}

	// Get config values for the integration
	configValueRows, err := dbms.Db.Queryx(`
		SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1`, integrationId)
	if err != nil {
		slog.Error("integrations: failed to get config values", "error", err)
		return apiKey, appKey, site, err
	}
	defer func() {
		if cerr := configValueRows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config value rows", "error", cerr)
		}
	}()

	for configValueRows.Next() {
		var name, value string
		var isEncrypted bool
		err = configValueRows.Scan(&name, &value, &isEncrypted)
		if err != nil {
			slog.Error("integrations: failed to scan integration config value", "error", err)
			continue
		}

		switch name {
		case apiKeyConfigName:
			apiKey = value
			if isEncrypted {
				apiKey, err = common.Decrypt(value)
				if err != nil {
					return apiKey, appKey, site, fmt.Errorf("failed to decrypt datadog api key: %w", err)
				}
			}
		case appKeyConfigName:
			appKey = value
			if isEncrypted {
				appKey, err = common.Decrypt(value)
				if err != nil {
					return apiKey, appKey, site, fmt.Errorf("failed to decrypt datadog app key: %w", err)
				}
			}
		case siteConfigName:
			site = value
		}
	}

	if apiKey == "" || appKey == "" || site == "" {
		return apiKey, appKey, site, fmt.Errorf("incomplete datadog configuration for account: %s", accountId)
	}

	site = normalizeDatadogSite(site)
	return apiKey, appKey, site, nil
}

// normalizeDatadogSite mirrors integrations.NormalizeDatadogSite. Duplicated here
// because integrations imports this package (eventrule/playbooks), so we cannot
// import integrations back without an import cycle.
func normalizeDatadogSite(site string) string {
	site = strings.TrimSpace(site)
	site = strings.TrimPrefix(site, "https://")
	site = strings.TrimPrefix(site, "http://")
	site = strings.TrimRight(site, "/")
	site = strings.TrimPrefix(site, "www.")

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

// Helper function to build Datadog query string
func buildDatadogQuery(params datadogMonitorsSearchParams) string {
	// If custom query is provided, use it
	if params.Query != "" {
		return params.Query
	}

	// Build query from parameters
	var queryParts []string

	if params.Status != "" {
		queryParts = append(queryParts, fmt.Sprintf("status:%s", params.Status))
	}

	if params.Service != "" {
		queryParts = append(queryParts, fmt.Sprintf("service:%s", params.Service))
	}

	if params.Env != "" {
		queryParts = append(queryParts, fmt.Sprintf("env:%s", params.Env))
	}

	return strings.Join(queryParts, " ")
}

// Helper function to fetch monitors from Datadog API
func fetchDatadogMonitors(apiKey, appKey, site, query string, limit int) ([]datadogMonitor, datadogMetadata, error) {
	// Build URL with query parameters
	baseURL := fmt.Sprintf("https://%s/api/v1/monitor/search", site)
	params := url.Values{}
	params.Add("query", query)
	if limit > 0 {
		params.Add("per_page", fmt.Sprintf("%d", limit))
	}

	apiURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	// Make HTTP request
	resp, err := common.HttpGet(apiURL, common.HttpWithHeaders(map[string]string{
		"DD-API-KEY":         apiKey,
		"DD-APPLICATION-KEY": appKey,
	}))
	if err != nil {
		return nil, datadogMetadata{}, fmt.Errorf("datadog API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, datadogMetadata{}, fmt.Errorf("failed to read Datadog API response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, datadogMetadata{}, fmt.Errorf("datadog API error (status %d): %s", resp.StatusCode, string(body))
	}

	var apiResponse datadogMonitorsSearchResponse
	err = json.Unmarshal(body, &apiResponse)
	if err != nil {
		return nil, datadogMetadata{}, fmt.Errorf("failed to parse Datadog API response: %w", err)
	}

	return apiResponse.Monitors, apiResponse.Metadata, nil
}

// Helper function to generate insights based on monitor data
func generateMonitorInsights(monitors []datadogMonitor, params datadogMonitorsSearchParams) []PlaybookActionResponseInsight {
	insights := []PlaybookActionResponseInsight{}

	if len(monitors) == 0 {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("No monitors found with status '%s'", params.Status),
			Severity: "info",
		})
		return insights
	}

	// Insight 1: Summary
	insights = append(insights, PlaybookActionResponseInsight{
		Message:  fmt.Sprintf("Found %d monitor(s) with status '%s'", len(monitors), params.Status),
		Severity: "info",
	})

	// Insight 2: Count monitors by type
	typeCount := make(map[string]int)
	for _, monitor := range monitors {
		typeCount[monitor.Classification]++
	}

	if len(typeCount) > 0 {
		var typeSummary []string
		for monType, count := range typeCount {
			typeSummary = append(typeSummary, fmt.Sprintf("%s (%d)", monType, count))
		}
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("Monitor types: %s", strings.Join(typeSummary, ", ")),
			Severity: "info",
		})
	}

	// Insight 3: Recently triggered monitors (within last hour)
	recentlyTriggered := 0
	now := time.Now().Unix()
	for _, monitor := range monitors {
		if monitor.LastTriggeredTs > 0 && (now-monitor.LastTriggeredTs) < 3600 {
			recentlyTriggered++
		}
	}

	if recentlyTriggered > 0 {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%d monitor(s) triggered in the last hour", recentlyTriggered),
			Severity: "warning",
		})
	}

	// Insight 4: Monitors with quality issues
	monitorsWithIssues := 0
	for _, monitor := range monitors {
		if len(monitor.QualityIssues) > 0 {
			monitorsWithIssues++
		}
	}

	if monitorsWithIssues > 0 {
		insights = append(insights, PlaybookActionResponseInsight{
			Message:  fmt.Sprintf("%d monitor(s) have quality issues", monitorsWithIssues),
			Severity: "warning",
		})
	}

	return insights
}
