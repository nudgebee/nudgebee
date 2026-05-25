package db

import (
	"context"
	"strconv"
	"strings"

	"nudgebee/relay-server/pkg/utils"
)

// QueryProxyDatasources queries all proxy integration configs for an account,
// decrypts credentials, and returns them ready to push to the proxy agent.
// This mirrors the API server's queryProxyDatasources logic.
func (p *pgStore) QueryProxyDatasources(ctx context.Context, accountID string) ([]ProxyDatasource, error) {
	rows, err := p.db.QueryxContext(ctx, `
		SELECT i.id::text, i.type::text, i.name::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
		WHERE ica.cloud_account_id = $1
		AND i.status = 'enabled'
		AND (
			i.type IN ('http_proxy', 'mcp_proxy', 'mongodb_proxy', 'kafka_proxy')
			OR (
				i.type IN ('postgresql', 'mysql', 'clickhouse', 'mssql', 'oracle', 'redis', 'ssh', 'mcp')
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
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck

	var datasources []ProxyDatasource

	for rows.Next() {
		var integrationID, integrationType, integrationName string
		if err := rows.Scan(&integrationID, &integrationType, &integrationName); err != nil {
			continue
		}

		configRows, err := p.db.QueryxContext(ctx, `
			SELECT name::text, value::text, is_encrypted
			FROM integration_config_values
			WHERE integration_id = $1
		`, integrationID)
		if err != nil {
			continue
		}

		configMap := map[string]any{}
		credentials := map[string]string{}
		var proxyType, credentialSource, credentialRef string

		for configRows.Next() {
			var name, value string
			var isEncrypted bool
			if err := configRows.Scan(&name, &value, &isEncrypted); err != nil {
				continue
			}

			if isEncrypted && value != "" {
				decrypted, err := utils.Decrypt(p.encryptionKey, value)
				if err != nil {
					continue
				}
				value = decrypted
			}

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
			case "account_id", "integration_config_name", "connection_mode", "k8s_secret",
				"agent_type", "datasource_key":
				// Skip meta fields
			default:
				configMap[name] = coerceConfigValue(name, value)
			}
		}
		configRows.Close() // nolint:errcheck

		if credentialSource == "" {
			credentialSource = "cloud_push"
		}

		// Derive proxy_type if not explicitly set
		switch proxyType {
		case "":
			switch strings.ToLower(integrationType) {
			case "http_proxy":
				proxyType = "http-proxy"
			case "mcp_proxy", "mcp":
				proxyType = "mcp-proxy"
			case "ssh":
				proxyType = "ssh-proxy"
			case "mongodb_proxy":
				proxyType = "mongo-proxy"
			case "kafka_proxy":
				proxyType = "kafka-proxy"
			case "postgresql", "mysql", "clickhouse", "mssql", "oracle":
				proxyType = "db-proxy"
				configMap["db_type"] = strings.ToLower(integrationType)
			case "redis":
				proxyType = "redis-proxy"
			}
		case "db-proxy":
			// Ensure db_type is set for db-proxy
			if _, ok := configMap["db_type"]; !ok {
				configMap["db_type"] = strings.ToLower(integrationType)
			}
		}

		datasources = append(datasources, ProxyDatasource{
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
