package alertrule

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	neturl "net/url"
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
	ApiKey   string
	// KibanaUrl is the Kibana endpoint (:5601 by convention), which is a
	// different host and port from Url (:9200). Optional: only rule listing
	// needs it, and it cannot be derived from the Elasticsearch URL.
	KibanaUrl string
	// TlsSkipVerify mirrors the integration's es_tls_skip_verify. Self-hosted
	// Elastic commonly serves a self-signed certificate — it is the ECK default
	// — so without honouring this, rule listing fails at the TLS handshake for
	// exactly the deployments most likely to need it.
	TlsSkipVerify bool
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
		// The `elasticsearch_*` spellings never matched anything: the ES
		// integration stores its values as `url` / `username` / `password` /
		// `auth_type` (see integrations/elasticsearch.go ConfigSchema), so this
		// function previously always failed with "missing required
		// elasticsearch configuration values" and the Watcher-based
		// create/update/delete path could never have run. Both spellings are
		// accepted so any row written under the old names still resolves.
		switch c.Name {
		case "url", "elasticsearch_url":
			cfg.Url = value
		case "username", "elasticsearch_username":
			cfg.Username = value
		case "password", "elasticsearch_password":
			cfg.Password = value
		case "auth_type", "elasticsearch_auth_type":
			cfg.AuthType = value
		case "api_key":
			cfg.ApiKey = value
		case "kibana_url":
			cfg.KibanaUrl = value
		case "es_tls_skip_verify":
			cfg.TlsSkipVerify = strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}

	// An API key is a complete credential on its own, so username/password are
	// only required when they are the auth method.
	if cfg.Url == "" || (cfg.ApiKey == "" && (cfg.Username == "" || cfg.Password == "")) {
		return nil, fmt.Errorf("missing required elasticsearch configuration values")
	}
	cfg.Url = strings.TrimRight(cfg.Url, "/")
	cfg.KibanaUrl = strings.TrimRight(cfg.KibanaUrl, "/")

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

// cubeAPMConn holds CubeAPM connection config for alert-rule management.
//
// Duplicated from integrations.CubeAPMConfig rather than reused: this package
// cannot import integrations (integrations → event → triage → alertrule is an
// import cycle), which is the same reason listIntegrationConfigValues exists.
// Only the fields alert-rule management needs are carried — the query URL is here
// solely to explain, in the error, why the admin URL could not be derived.
type cubeAPMConn struct {
	QueryURL   string
	AdminURL   string
	AdminToken string
}

// CubeAPM port pair. The admin API is a separate server from the query API, and
// on a standard deployment they differ only by port — so an unset admin URL is
// derived rather than demanded.
const (
	cubeAPMQueryPort = "3140"
	cubeAPMAdminPort = "3199"
)

// getCubeAPMConfigs returns CubeAPM connection config for alert-rule management.
func getCubeAPMConfigs(sc *security.RequestContext, accountId string) (cubeAPMConn, error) {
	// listIntegrationConfigValues is always tenant-scoped, but its
	// cloud_account_id filter is conditional and the query ends in LIMIT 1 — so an
	// empty accountId returns an arbitrary CubeAPM integration from the tenant
	// rather than none, and alert rules would be written to the wrong account's
	// CubeAPM.
	if accountId == "" {
		return cubeAPMConn{}, fmt.Errorf("account_id is required to resolve a CubeAPM integration")
	}

	configs, err := listIntegrationConfigValues(sc, accountId, "cubeapm")
	if err != nil {
		return cubeAPMConn{}, fmt.Errorf("failed to list cubeapm integration configs: %w", err)
	}

	var conn cubeAPMConn
	for _, c := range configs {
		value, err := decryptConfigValue(c)
		if err != nil {
			return cubeAPMConn{}, fmt.Errorf("failed to decrypt cubeapm config %s: %w", c.Name, err)
		}
		switch c.Name {
		case "cubeapm_url":
			conn.QueryURL = normalizeCubeAPMBaseURL(value)
		case "cubeapm_admin_url":
			conn.AdminURL = normalizeCubeAPMBaseURL(value)
		case "cubeapm_admin_token":
			conn.AdminToken = strings.TrimSpace(value)
		}
	}

	if conn.AdminURL == "" {
		conn.AdminURL = deriveCubeAPMAdminBaseURL(conn.QueryURL)
	}
	return conn, nil
}

// normalizeCubeAPMBaseURL strips any path/query/fragment so a URL pasted from the
// browser still resolves. The port is preserved deliberately — for CubeAPM it
// selects which server answers.
func normalizeCubeAPMBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return parsed.Scheme + "://" + parsed.Host
}

// deriveCubeAPMAdminBaseURL turns a query URL into the admin URL by swapping the
// port. Returns "" when the query URL is on a non-standard port: guessing there
// would silently target whatever else is listening, so the caller asks the
// operator for the admin URL explicitly instead.
func deriveCubeAPMAdminBaseURL(queryURL string) string {
	parsed, err := neturl.Parse(queryURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || port != cubeAPMQueryPort {
		return ""
	}
	return parsed.Scheme + "://" + net.JoinHostPort(host, cubeAPMAdminPort)
}

// cubeAPMAdminHeaders builds the header set for an admin API call. The
// Authorization header is omitted entirely when no token is configured: CubeAPM's
// http-token-admin setting is optional, and sending `Bearer ` with an empty value
// is a malformed credential rather than an absent one.
func cubeAPMAdminHeaders(token string) map[string]string {
	headers := map[string]string{"Content-Type": "application/json"}
	if token = strings.TrimSpace(token); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return headers
}
