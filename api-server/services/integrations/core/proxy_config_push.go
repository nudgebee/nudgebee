package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"sort"
	"strconv"
	"strings"
)

// alwaysProxyTypes are integration types that are always proxy-based (no K8s equivalent).
var alwaysProxyTypes = map[string]bool{
	"http_proxy":    true,
	"mongodb_proxy": true,
	"kafka_proxy":   true,
}

// dualModeProxyMapping maps integration types that support both K8s and vm_agent modes
// to their proxy type and db_type values expected by the forager agent.
var dualModeProxyMapping = map[string]struct {
	ProxyType string
	DbType    string
}{
	"postgresql": {ProxyType: "db-proxy", DbType: "postgresql"},
	"mysql":      {ProxyType: "db-proxy", DbType: "mysql"},
	"clickhouse": {ProxyType: "db-proxy", DbType: "clickhouse"},
	"mssql":      {ProxyType: "db-proxy", DbType: "mssql"},
	"oracle":     {ProxyType: "db-proxy", DbType: "oracle"},
	"redis":      {ProxyType: "redis-proxy", DbType: ""},
	"ssh":        {ProxyType: "ssh-proxy", DbType: ""},
	"mcp":        {ProxyType: "mcp-proxy", DbType: ""},
	"rabbitmq":   {ProxyType: "rabbitmq-proxy", DbType: ""},
	// prometheus, loki and signoz ride the forager's generic http proxy. The datasource
	// type stays the integration type, so the forager applies its Prometheus health check.
	"prometheus": {ProxyType: "http-proxy", DbType: ""},
	"loki":       {ProxyType: "http-proxy", DbType: ""},
	"signoz":     {ProxyType: "http-proxy", DbType: ""},
}

// singleInstanceDualModeTypes are dual-mode types that keep the one-per-account
// rule in their default mode. Postgres, Redis and the rest address a distinct
// backend each, so IsProxyIntegrationType exempts them from the duplicate check
// regardless of connection_mode; a direct Prometheus, Loki or SigNoz has no such
// per-row target, and two of them would make the user-row pick ambiguous. Only
// vm_agent rows (IsProxyIntegration) are proxy integrations.
var singleInstanceDualModeTypes = map[string]bool{
	"prometheus": true,
	"loki":       true,
	"signoz":     true,
}

// prometheusProxyAuthTypes maps the integration's auth_type values to the
// forager http proxy's. Everything else (SigV4, Coralogix, Azure AD) needs the
// server to sign requests and is rejected by the integration's validation.
var prometheusProxyAuthTypes = map[string]string{
	"none":         "",
	"basic":        "basic",
	"bearer_token": "bearer",
}

// prometheusIntegrationConfig is the part of a prometheus integration's config
// the forager's http proxy consumes, under the integration's own key names.
type prometheusIntegrationConfig struct {
	URL         string `json:"prometheus_url"`
	Username    string `json:"prometheus_username"`
	Password    string `json:"prometheus_password"`
	BearerToken string `json:"prometheus_bearer_token"`
	AuthType    string `json:"auth_type"`
}

// prometheusProxyDatasource is the shape the forager's http proxy reads. Empty
// fields are omitted so an unset auth_type sends none.
type prometheusProxyDatasource struct {
	BaseURL  string `json:"base_url,omitempty"`
	AuthType string `json:"auth_type,omitempty"`
}

// prometheusProxyCredentials is the secret half, kept out of the plain config.
type prometheusProxyCredentials struct {
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	BearerToken string `json:"bearer_token,omitempty"`
}

// prometheusServerOnlyKeys are server-side or unsupported through the forager,
// so they are removed from the config map and never sent.
var prometheusServerOnlyKeys = []string{
	"prometheus_extra_headers", "prometheus_additional_labels",
	"prometheus_ruler_type", "prometheus_ruler_url", "default_metrics_provider",
	"aws_access_key_id", "aws_secret_access_key", "aws_region", "aws_service_name",
	"coralogix_token", "azure_client_id", "azure_client_secret", "azure_tenant_id", "azure_resource",
}

// normalizePrometheusProxyDatasource rewrites a prometheus integration's config
// into the shape the forager's http proxy reads: prometheus_url -> base_url,
// prometheus_username/password/bearer_token -> credentials, auth_type translated.
// The integration's own keys are removed so secrets never travel in the plain
// config map and server-only settings (labels, ruler) are not sent at all.
// Keys it does not know about are left in place.
func normalizePrometheusProxyDatasource(configMap map[string]any, credentials map[string]string) {
	var in prometheusIntegrationConfig
	if raw, err := json.Marshal(configMap); err == nil {
		// A mistyped value is skipped and the rest still decode; the keys are
		// deleted below either way so nothing secret stays in the plain map.
		if err := json.Unmarshal(raw, &in); err != nil {
			slog.Warn("prometheus proxy config has a non-string value", "error", err)
		}
	}

	for _, key := range []string{"prometheus_url", "prometheus_username", "prometheus_password", "prometheus_bearer_token", "auth_type"} {
		delete(configMap, key)
	}
	for _, key := range prometheusServerOnlyKeys {
		delete(configMap, key)
	}

	out := prometheusProxyDatasource{
		BaseURL:  strings.TrimRight(strings.TrimSpace(in.URL), "/"),
		AuthType: prometheusProxyAuthTypes[in.AuthType],
	}
	mergeJSON(&configMap, out)
	mergeJSON(&credentials, prometheusProxyCredentials{
		Username:    in.Username,
		Password:    in.Password,
		BearerToken: in.BearerToken,
	})
}

// mergeJSON marshals v and decodes it into *dst (a pointer to a non-nil map), adding or overwriting only the
// keys v emits.
func mergeJSON(dst, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		slog.Error("prometheus proxy config marshal failed", "error", err)
		return
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		slog.Error("prometheus proxy config merge failed", "error", err)
	}
}

// normalizeLokiProxyDatasource is the Loki counterpart. The tenant id is dropped: the
// forager applies a custom header only for auth_type=custom_header, so the server sends
// X-Scope-OrgID per request (lokiRequestViaForager). loki_headers and the ruler URL stay
// server-side.
func normalizeLokiProxyDatasource(configMap map[string]any, credentials map[string]string) {
	take := func(key string) string {
		v, _ := configMap[key].(string)
		delete(configMap, key)
		return v
	}

	if u := strings.TrimRight(strings.TrimSpace(take("loki_url")), "/"); u != "" {
		configMap["base_url"] = u
	}
	if v := take("loki_username"); v != "" {
		credentials["username"] = v
	}
	if v := take("loki_password"); v != "" {
		credentials["password"] = v
	}
	if v := take("loki_bearer_token"); v != "" {
		credentials["bearer_token"] = v
	}
	take("loki_tenant_id")

	authType := take("loki_auth_type")
	if mapped := prometheusProxyAuthTypes[authType]; mapped != "" {
		configMap["auth_type"] = mapped
	}

	for _, key := range []string{"loki_headers", "loki_rules_url", DefaultLogProvider} {
		delete(configMap, key)
	}
}

// normalizeSignozProxyDatasource maps signoz_url to base_url and drops everything else.
// The forager's datasource is unauthenticated on purpose: SigNoz authenticates with a
// login JWT or an API key, which the forager's static auth cannot do, so those stay on
// the server and are sent per request.
func normalizeSignozProxyDatasource(configMap map[string]any) {
	if u, _ := configMap["signoz_url"].(string); strings.TrimSpace(u) != "" {
		configMap["base_url"] = strings.TrimRight(strings.TrimSpace(u), "/")
	}
	for _, key := range []string{"signoz_url", "signoz_username", "signoz_password", "signoz_api_key", "signoz_auth_type", "signoz_mode", DefaultLogProvider} {
		delete(configMap, key)
	}
}

// IsProxyIntegration returns true if the given integration is a proxy integration,
// either by type (always-proxy) or by connection_mode=vm_agent (dual-mode).
func IsProxyIntegration(integrationType string, configValues []IntegrationConfigValue) bool {
	t := strings.ToLower(integrationType)
	if alwaysProxyTypes[t] {
		return true
	}
	if _, ok := dualModeProxyMapping[t]; ok {
		for _, v := range configValues {
			if v.Name == "connection_mode" && v.Value == "vm_agent" {
				return true
			}
		}
	}
	return false
}

// IsProxyIntegrationType returns true if the given integration type could potentially
// be a proxy integration (always-proxy or dual-mode type).
// For dual-mode types, the actual determination requires checking connection_mode config.
func IsProxyIntegrationType(integrationType string) bool {
	t := strings.ToLower(integrationType)
	if singleInstanceDualModeTypes[t] {
		return false
	}
	return IsProxyCapableType(t)
}

// IsProxyCapableType is IsProxyIntegrationType without the single-instance
// exemption: true for every type that can be a proxy integration in some mode.
// Use it to decide whether to look at connection_mode (e.g. before deleting a
// row, so the forager's datasource list is refreshed); use IsProxyIntegrationType
// for the "multiple per account" rules.
func IsProxyCapableType(integrationType string) bool {
	t := strings.ToLower(integrationType)
	if alwaysProxyTypes[t] {
		return true
	}
	_, ok := dualModeProxyMapping[t]
	return ok
}

// TriggerProxyConfigPush queries all proxy integration configs for the given account
// and pushes the full datasource list to the relay server asynchronously.
// This should be called after proxy integration create/delete operations.
func TriggerProxyConfigPush(accountID string) {
	datasources, err := queryProxyDatasources(accountID)
	if err != nil {
		slog.Error("integrations: failed to query proxy datasources for config push",
			"account_id", accountID, "error", err)
		return
	}

	slog.Info("integrations: triggering proxy config push",
		"account_id", accountID, "datasource_count", len(datasources))

	relay.PushProxyConfig(accountID, datasources)
}

// BuildSingleDatasourceConfig constructs a ProxyDatasourceConfig from integration
// config values, without querying the database. Used for pre-save connectivity testing.
// Values must already be decrypted (caller is responsible for decryption).
func BuildSingleDatasourceConfig(integrationType string, configValues []IntegrationConfigValue) (relay.ProxyDatasourceConfig, error) {
	configMap := map[string]any{}
	credentials := map[string]string{}
	var proxyType, credentialSource, credentialRef string

	for _, v := range configValues {
		switch v.Name {
		case "proxy_type":
			proxyType = v.Value
		case "credential_source":
			credentialSource = v.Value
		case "secret_ref":
			credentialRef = v.Value
		case "username", "password", "bearer_token", "custom_header_name", "custom_header_value",
			"private_key", "passphrase", "sasl_username", "sasl_password":
			credentials[v.Name] = v.Value
		case "account_id", "integration_config_name", "connection_mode", "k8s_secret", "vm_agent_config_id":
			// Skip meta fields
		default:
			configMap[v.Name] = coerceConfigValue(v.Name, v.Value)
		}
	}

	if credentialSource == "" {
		credentialSource = "cloud_push"
	}

	// For dual-mode types, derive proxy_type and inject db_type from the mapping
	if mapping, ok := dualModeProxyMapping[strings.ToLower(integrationType)]; ok {
		if proxyType == "" {
			proxyType = mapping.ProxyType
		}
		if mapping.DbType != "" {
			configMap["db_type"] = mapping.DbType
		}
	}
	if strings.EqualFold(integrationType, "prometheus") {
		normalizePrometheusProxyDatasource(configMap, credentials)
	}
	if strings.EqualFold(integrationType, "loki") {
		normalizeLokiProxyDatasource(configMap, credentials)
	}
	if strings.EqualFold(integrationType, "signoz") {
		normalizeSignozProxyDatasource(configMap)
	}

	return relay.ProxyDatasourceConfig{
		Type:             integrationType,
		ProxyType:        proxyType,
		Config:           configMap,
		Credentials:      credentials,
		CredentialSource: credentialSource,
		CredentialRef:    credentialRef,
	}, nil
}

// queryProxyDatasources retrieves all proxy integration configs for an account,
// decrypts credentials, and returns them in the format expected by the relay server.
func queryProxyDatasources(accountID string) ([]relay.ProxyDatasourceConfig, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	// Build IN clause for always-proxy types
	alwaysProxyNames := make([]string, 0, len(alwaysProxyTypes))
	for t := range alwaysProxyTypes {
		alwaysProxyNames = append(alwaysProxyNames, t)
	}
	sort.Strings(alwaysProxyNames)

	// Build IN clause for dual-mode types
	dualModeNames := make([]string, 0, len(dualModeProxyMapping))
	for t := range dualModeProxyMapping {
		dualModeNames = append(dualModeNames, t)
	}
	sort.Strings(dualModeNames)

	// Build query args: $1=accountID, then always-proxy types, then dual-mode types
	queryArgs := make([]any, 0, 1+len(alwaysProxyNames)+len(dualModeNames))
	queryArgs = append(queryArgs, accountID)

	alwaysPlaceholders := make([]string, len(alwaysProxyNames))
	for i, t := range alwaysProxyNames {
		alwaysPlaceholders[i] = fmt.Sprintf("$%d", len(queryArgs)+1)
		queryArgs = append(queryArgs, t)
	}

	dualPlaceholders := make([]string, len(dualModeNames))
	for i, t := range dualModeNames {
		dualPlaceholders[i] = fmt.Sprintf("$%d", len(queryArgs)+1)
		queryArgs = append(queryArgs, t)
	}

	// Query all proxy integrations: always-proxy types + dual-mode types with connection_mode=vm_agent
	rows, err := dbms.Db.Queryx(fmt.Sprintf(`
		SELECT i.id::text, i.type::text, i.name::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE ica.cloud_account_id = $1
		AND i.status = 'enabled'
		AND (
			i.type IN (%s)
			OR (
				i.type IN (%s)
				AND EXISTS (
					SELECT 1 FROM integration_config_values icv
					WHERE icv.integration_id = i.id
					AND icv.name = 'connection_mode' AND icv.value = 'vm_agent'
				)
			)
		)
		AND NOT EXISTS (
			SELECT 1 FROM integration_config_values icv
			WHERE icv.integration_id = i.id
			AND icv.name = 'datasource_key'
		)
	`, strings.Join(alwaysPlaceholders, ", "), strings.Join(dualPlaceholders, ", ")), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			slog.Error("integrations: failed to close proxy datasource rows", "error", cerr)
		}
	}()

	// First pass: read the rows only. Config values are loaded after the cursor is
	// closed so this never holds one pool connection while asking for another —
	// the same two-pass shape ListIntegrationConfigs was converted to for #34973.
	pending := []pendingIntegration{}
	for rows.Next() {
		var integrationID, integrationType, integrationName string
		if err := rows.Scan(&integrationID, &integrationType, &integrationName); err != nil {
			slog.Error("integrations: failed to scan proxy integration", "error", err)
			continue
		}
		pending = append(pending, pendingIntegration{
			id:              integrationID,
			name:            integrationName,
			integrationType: integrationType,
		})
	}
	if err := rows.Err(); err != nil {
		slog.Error("integrations: failed to iterate proxy integration rows", "error", err)
		return nil, err
	}
	if cerr := rows.Close(); cerr != nil {
		slog.Error("integrations: failed to close proxy datasource rows", "error", cerr)
		return nil, cerr
	}

	// Second pass: the cursor is closed, so its connection is back in the pool.
	var datasources []relay.ProxyDatasourceConfig

	for _, p := range pending {
		integrationID, integrationType, integrationName := p.id, p.integrationType, p.name

		// Query config values for this integration
		configMap := map[string]any{}
		credentials := map[string]string{}
		var proxyType, credentialSource, credentialRef string

		if err := func() error {
			configRows, err := dbms.Db.Queryx(`
				SELECT name::text, value::text, is_encrypted
				FROM integration_config_values
				WHERE integration_id = $1
			`, integrationID)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := configRows.Close(); cerr != nil {
					slog.Error("integrations: failed to close config value rows", "error", cerr)
				}
			}()

			for configRows.Next() {
				var name, value string
				var isEncrypted bool
				if err := configRows.Scan(&name, &value, &isEncrypted); err != nil {
					slog.Error("integrations: failed to scan config value", "error", err)
					continue
				}

				// Decrypt encrypted values
				if isEncrypted && value != "" {
					decrypted, err := common.Decrypt(value)
					if err != nil {
						slog.Error("integrations: failed to decrypt config value",
							"integration_id", integrationID, "name", name, "error", err)
						continue
					}
					value = decrypted
				}

				// Classify fields
				switch name {
				case "proxy_type":
					proxyType = value
				case "credential_source":
					credentialSource = value
				case "secret_ref":
					credentialRef = value
				case "username", "password", "bearer_token", "custom_header_name", "custom_header_value",
					"private_key", "passphrase", "sasl_username", "sasl_password":
					credentials[name] = value
				case "account_id", "integration_config_name", "connection_mode", "k8s_secret":
					// Skip meta fields — not part of datasource config
				default:
					configMap[name] = coerceConfigValue(name, value)
				}
			}
			if err := configRows.Err(); err != nil {
				return err
			}
			return nil
		}(); err != nil {
			slog.Error("integrations: failed to query config values", "integration_id", integrationID, "error", err)
			continue
		}

		if credentialSource == "" {
			credentialSource = "cloud_push"
		}

		// For dual-mode types, derive proxy_type and inject db_type from the mapping
		if mapping, ok := dualModeProxyMapping[strings.ToLower(integrationType)]; ok {
			if proxyType == "" {
				proxyType = mapping.ProxyType
			}
			if mapping.DbType != "" {
				configMap["db_type"] = mapping.DbType
			}
		}
		if strings.EqualFold(integrationType, "prometheus") {
			normalizePrometheusProxyDatasource(configMap, credentials)
		}
		if strings.EqualFold(integrationType, "loki") {
			normalizeLokiProxyDatasource(configMap, credentials)
		}
		if strings.EqualFold(integrationType, "signoz") {
			normalizeSignozProxyDatasource(configMap)
		}

		datasources = append(datasources, relay.ProxyDatasourceConfig{
			ID:               integrationID,
			Type:             integrationType,
			ProxyType:        proxyType,
			Name:             integrationName,
			Config:           configMap,
			Credentials:      credentials,
			CredentialSource: credentialSource,
			CredentialRef:    credentialRef,
		})
	}

	return datasources, nil
}

// coerceConfigValue converts known boolean and integer config fields from their
// string database representation to native Go types so the forager agent can
// unmarshal them correctly.
func coerceConfigValue(name, value string) any {
	switch name {
	case "tls_enabled":
		if b, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return b
		}
	case "port", "max_open_connections", "max_idle_connections", "max_lifetime_sec":
		if i, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return i
		}
	}
	return value
}
