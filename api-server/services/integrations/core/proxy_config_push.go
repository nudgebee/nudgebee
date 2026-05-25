package core

import (
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

	var datasources []relay.ProxyDatasourceConfig

	for rows.Next() {
		var integrationID, integrationType, integrationName string
		if err := rows.Scan(&integrationID, &integrationType, &integrationName); err != nil {
			slog.Error("integrations: failed to scan proxy integration", "error", err)
			continue
		}

		// Query config values for this integration
		configRows, err := dbms.Db.Queryx(`
			SELECT name::text, value::text, is_encrypted
			FROM integration_config_values
			WHERE integration_id = $1
		`, integrationID)
		if err != nil {
			slog.Error("integrations: failed to query config values", "integration_id", integrationID, "error", err)
			continue
		}

		configMap := map[string]any{}
		credentials := map[string]string{}
		var proxyType, credentialSource, credentialRef string

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
		if cerr := configRows.Close(); cerr != nil {
			slog.Error("integrations: failed to close config value rows", "error", cerr)
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
