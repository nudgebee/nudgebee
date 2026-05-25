package alertrule

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// integrationConfigValue holds a single config key/value from integration_config_values.
type integrationConfigValue struct {
	Name        string
	Value       string
	IsEncrypted bool
}

// listIntegrationConfigValues fetches config values for a given integration type, tenant, and optional account.
// This is a local replacement for core.ListIntegrationConfigs to avoid import cycles.
func listIntegrationConfigValues(sc *security.RequestContext, accountId, integrationType string) ([]integrationConfigValue, error) {
	return listIntegrationConfigValuesWithSource(sc, accountId, integrationType, "")
}

// listIntegrationConfigValuesWithSource is like listIntegrationConfigValues but optionally filters by source.
func listIntegrationConfigValuesWithSource(sc *security.RequestContext, accountId, integrationType, source string) ([]integrationConfigValue, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	tenantId := sc.GetSecurityContext().GetTenantId()

	// Build parameterized query
	args := []interface{}{integrationType, tenantId}
	query := `
		SELECT i.id::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE i.type = $1 AND i.tenant_id = $2`

	paramIdx := 3
	if accountId != "" {
		query += fmt.Sprintf(" AND ica.cloud_account_id = $%d", paramIdx)
		args = append(args, accountId)
		paramIdx++
	}
	if source != "" {
		query += fmt.Sprintf(" AND i.source = $%d", paramIdx)
		args = append(args, source)
	}
	query += " LIMIT 1"

	var integrationId string
	err = dbms.Db.QueryRow(query, args...).Scan(&integrationId)
	if err != nil {
		return nil, fmt.Errorf("integration %s not found for account %s: %w", integrationType, accountId, err)
	}

	rows, err := dbms.Db.Queryx(
		`SELECT name::text, value::text, is_encrypted
		FROM integration_config_values
		WHERE integration_id = $1`, integrationId)
	if err != nil {
		return nil, fmt.Errorf("failed to get config values: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var values []integrationConfigValue
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			slog.Error("alertrule: failed to scan config value", "error", err)
			continue
		}
		values = append(values, integrationConfigValue{
			Name:        name,
			Value:       value,
			IsEncrypted: isEncrypted,
		})
	}

	return values, nil
}

// decryptConfigValue decrypts a config value if it's encrypted.
func decryptConfigValue(cfg integrationConfigValue) (string, error) {
	if cfg.IsEncrypted && cfg.Value != "" {
		return common.Decrypt(cfg.Value)
	}
	return cfg.Value, nil
}

// getDatadogConfigs returns (apiKey, appKey, site) for a Datadog integration.
func getDatadogConfigs(sc *security.RequestContext, accountId string) (string, string, string, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "datadog")
	if err != nil {
		return "", "", "", fmt.Errorf("failed to list datadog integration configs: %w", err)
	}

	var apiKey, appKey, site string
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to decrypt datadog config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "api_key":
			apiKey = value
		case "app_key":
			appKey = value
		case "site":
			site = value
		}
	}

	if site == "" {
		site = "api.datadoghq.com"
	}

	return apiKey, appKey, site, nil
}

// getNewRelicConfigs returns (apiKey, nrAccountId, region) for a New Relic integration.
func getNewRelicConfigs(sc *security.RequestContext, accountId string) (string, string, string, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "newrelic")
	if err != nil {
		return "", "", "", fmt.Errorf("failed to list newrelic integration configs: %w", err)
	}

	var apiKey, nrAccountId, region string
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to decrypt newrelic config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "api_key":
			apiKey = value
		case "account_nr_id":
			nrAccountId = value
		case "region":
			region = value
		}
	}

	return apiKey, nrAccountId, region, nil
}

// getNewRelicEndpoint returns the appropriate API endpoint based on region.
func getNewRelicEndpoint(region string) string {
	if region == "EU" {
		return "api.eu.newrelic.com"
	}
	return "api.newrelic.com"
}

// getDynatraceConfigs returns (apiToken, baseUrl) for a Dynatrace integration.
func getDynatraceConfigs(sc *security.RequestContext, accountId string) (string, string, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "dynatrace")
	if err != nil {
		return "", "", fmt.Errorf("failed to list dynatrace integration configs: %w", err)
	}

	var apiToken, baseURL string
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return "", "", fmt.Errorf("failed to decrypt dynatrace config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "api_token":
			apiToken = value
		case "base_url":
			baseURL = value
		}
	}

	return apiToken, baseURL, nil
}

// splunkO11yConfig holds Splunk Observability Platform connection config.
type splunkO11yConfig struct {
	Realm       string
	AccessToken string
}

// getSplunkO11yConfigs returns Splunk Observability Platform configs.
func getSplunkO11yConfigs(sc *security.RequestContext, accountId string) (splunkO11yConfig, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "splunk_observability_platform")
	if err != nil {
		return splunkO11yConfig{}, fmt.Errorf("failed to list splunk integration configs: %w", err)
	}

	var realm, accessToken string
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return splunkO11yConfig{}, fmt.Errorf("failed to decrypt splunk config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "realm":
			realm = value
		case "access_token":
			accessToken = value
		}
	}

	return splunkO11yConfig{Realm: realm, AccessToken: accessToken}, nil
}

// elasticsearchConfig holds Elasticsearch connection config.
type elasticsearchConfig struct {
	Url      string
	Username string
	Password string
	AuthType string
}

// getElasticsearchConfigs returns Elasticsearch configs (user-sourced integrations only).
func getElasticsearchConfigs(sc *security.RequestContext, accountId string) (*elasticsearchConfig, error) {
	configs, err := listIntegrationConfigValuesWithSource(sc, accountId, "ES", "user")
	if err != nil {
		return nil, fmt.Errorf("failed to get elasticsearch integration: %w", err)
	}

	cfg := &elasticsearchConfig{AuthType: "basic"}
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt elasticsearch config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "elasticsearch_url":
			cfg.Url = value
		case "elasticsearch_username":
			cfg.Username = value
		case "elasticsearch_password":
			cfg.Password = value
		case "elasticsearch_auth_type":
			cfg.AuthType = value
		}
	}

	if cfg.Url == "" || cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("missing required elasticsearch configuration values")
	}
	cfg.Url = strings.TrimRight(cfg.Url, "/")

	if cfg.AuthType == "" {
		cfg.AuthType = "basic"
	}

	return cfg, nil
}

// getSignozConfigs returns (signozUrl, jwtToken) for a SigNoz integration.
func getSignozConfigs(sc *security.RequestContext, accountId string) (string, string, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "signoz")
	if err != nil {
		return "", "", fmt.Errorf("failed to get signoz integration: %w", err)
	}

	var signozUrl, signozUsername, signozPassword string
	for _, c := range configs {
		switch c.Name {
		case "signoz_url":
			signozUrl = c.Value
		case "signoz_username":
			signozUsername = c.Value
		case "signoz_password":
			signozPassword = c.Value
		}
	}

	if signozUrl == "" || signozUsername == "" || signozPassword == "" {
		return "", "", fmt.Errorf("missing required signoz configuration values")
	}

	jwtToken, err := getSignozJwtToken(signozUrl, signozUsername, signozPassword)
	if err != nil {
		return "", "", fmt.Errorf("failed to get signoz jwt token: %w", err)
	}

	return signozUrl, jwtToken, nil
}

// getSignozJwtToken authenticates with SigNoz and returns a JWT token.
func getSignozJwtToken(signozUrl, username, password string) (string, error) {
	loginBody := map[string]string{
		"email":    username,
		"password": password,
	}

	res, err := common.HttpPost(fmt.Sprintf("%s/api/v1/login", signozUrl), common.HttpWithJsonBody(loginBody))
	if err != nil {
		return "", fmt.Errorf("failed to post login request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var obj struct {
		AccessJwt string `json:"accessJwt"`
	}
	if err := json.NewDecoder(res.Body).Decode(&obj); err != nil {
		return "", fmt.Errorf("failed to decode SigNoz login response: %w", err)
	}
	if obj.AccessJwt != "" {
		return obj.AccessJwt, nil
	}

	return "", fmt.Errorf("empty JWT token received from SigNoz")
}

// getGrafanaConfigs returns (grafanaUrl, apiToken) for a Grafana integration.
func getGrafanaConfigs(sc *security.RequestContext, accountId string) (string, string, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "grafana")
	if err != nil {
		return "", "", fmt.Errorf("failed to get grafana integration: %w", err)
	}

	var grafanaUrl, apiToken string
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return "", "", fmt.Errorf("failed to decrypt grafana config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "grafana_url":
			grafanaUrl = value
		case "grafana_api_token":
			apiToken = value
		}
	}

	if grafanaUrl == "" || apiToken == "" {
		return "", "", fmt.Errorf("missing required Grafana configuration (grafana_url, grafana_api_token)")
	}

	return grafanaUrl, apiToken, nil
}

// getChronosphereConfigs returns (chronosphereUrl, bearerToken) for a Chronosphere integration.
func getChronosphereConfigs(sc *security.RequestContext, accountId string) (string, string, error) {
	configs, err := listIntegrationConfigValues(sc, accountId, "chronosphere")
	if err != nil {
		return "", "", fmt.Errorf("failed to get chronosphere integration: %w", err)
	}

	var chronoUrl, bearerToken string
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return "", "", fmt.Errorf("failed to decrypt chronosphere config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "chronosphere_url":
			chronoUrl = value
		case "chronosphere_token":
			bearerToken = value
		}
	}

	if chronoUrl == "" || bearerToken == "" {
		return "", "", fmt.Errorf("missing required Chronosphere configuration (chronosphere_url, chronosphere_token)")
	}

	return chronoUrl, bearerToken, nil
}
