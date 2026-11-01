package db

import (
	"context"
	"errors"
	"fmt"

	"nudgebee/relay-server/pkg/utils"
)

// ErrMCPIntegrationNotFound means the integration does not exist, is not an
// enabled direct-mode MCP integration, or is not linked to the account.
var ErrMCPIntegrationNotFound = errors.New("mcp integration not found for account")

// MCPIntegrationAuth is a direct-mode MCP integration's endpoint and auth, in
// the action_params shape the relay's MCP client reads (auth_type +
// auth_credentials).
type MCPIntegrationAuth struct {
	URL             string
	AuthType        string
	AuthCredentials map[string]string
}

// GetMCPIntegrationAuth loads a direct-mode MCP integration's URL and
// credentials, decrypted, for the relay to end a session it did not open (see
// docs/mcp-sessions.md). The integration must be enabled, of type mcp, and
// linked to accountID; a caller-supplied id from another account is refused.
// Agent-proxied (vm_agent) integrations are excluded: their sessions live in
// forager. A value that fails to decrypt is an error, not a silent skip, so a
// close never goes out with partial credentials.
func (p *pgStore) GetMCPIntegrationAuth(ctx context.Context, accountID, integrationID string) (MCPIntegrationAuth, error) {
	rows, err := p.db.QueryxContext(ctx, `
		SELECT icv.name::text, COALESCE(icv.value::text, ''), COALESCE(icv.is_encrypted, false)
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON ica.integration_id = i.id
		JOIN integration_config_values icv ON icv.integration_id = i.id
		WHERE i.id::text = $1
		AND ica.cloud_account_id::text = $2
		AND i.type = 'mcp'
		AND i.status = 'enabled'
		AND NOT EXISTS (
			SELECT 1 FROM integration_config_values m
			WHERE m.integration_id = i.id AND m.name = 'connection_mode' AND m.value = 'vm_agent'
		)
	`, integrationID, accountID)
	if err != nil {
		return MCPIntegrationAuth{}, err
	}
	defer rows.Close() // nolint:errcheck

	auth := MCPIntegrationAuth{AuthCredentials: map[string]string{}}
	found := false
	for rows.Next() {
		var name, value string
		var isEncrypted bool
		if err := rows.Scan(&name, &value, &isEncrypted); err != nil {
			return MCPIntegrationAuth{}, err
		}
		found = true
		if isEncrypted && value != "" {
			if p.encryptionKey == "" {
				return MCPIntegrationAuth{}, fmt.Errorf("config %q is encrypted but the relay has no encryption key", name)
			}
			decrypted, err := utils.Decrypt(p.encryptionKey, value)
			if err != nil {
				return MCPIntegrationAuth{}, fmt.Errorf("decrypting config %q: %w", name, err)
			}
			value = decrypted
		}
		switch name {
		case "url":
			auth.URL = value
		case "auth_type":
			auth.AuthType = value
		case "bearer_token", "username", "password", "custom_header_name", "custom_header_value",
			"oauth_token_url", "oauth_client_id", "oauth_client_secret", "oauth_scope", "oauth_audience":
			auth.AuthCredentials[name] = value
		}
	}
	if err := rows.Err(); err != nil {
		return MCPIntegrationAuth{}, err
	}
	if !found {
		return MCPIntegrationAuth{}, ErrMCPIntegrationNotFound
	}
	if auth.AuthType == "none" {
		auth.AuthType = ""
	}
	return auth, nil
}
