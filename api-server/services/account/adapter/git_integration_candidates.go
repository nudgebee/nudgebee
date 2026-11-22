package adapter

import (
	"errors"
	"fmt"
	"log/slog"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
)

// A pull request is not always raised with the integration the user picked: the
// code agent chooses its own credential for the repository. A status lookup
// with the picked integration then gets a 404 from a pull request that exists.
// So every lookup of an existing pull request tries the tenant's enabled
// integrations of that type in turn, the picked one first, until one can see it.

// gitIntegrationConfig is one enabled integration's decrypted config values
// (url, username, password, auth_type, ...).
type gitIntegrationConfig struct {
	Name   string
	Values map[string]string
}

// loadGitIntegrationConfigs returns the tenant's enabled integrations of
// integrationType, preferredName first and the rest by name. An integration
// whose values cannot be decrypted is left out and logged.
func loadGitIntegrationConfigs(logger *slog.Logger, dbms *database.DatabaseManager, tenantID, integrationType, preferredName string) ([]gitIntegrationConfig, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant ID is empty")
	}
	rows, err := dbms.Db.Queryx(`
		SELECT i.id::text, i.name::text, icv.name::text, icv.value::text, icv.is_encrypted
		FROM integrations i
		JOIN integration_config_values icv ON icv.integration_id = i.id
		WHERE i.tenant_id = $1 AND i.type = $2 AND i.status = 'enabled'
		ORDER BY (i.name = $3) DESC, i.name, i.id`, tenantID, integrationType, preferredName)
	if err != nil {
		return nil, fmt.Errorf("querying %s integrations: %w", integrationType, err)
	}
	defer func() { _ = rows.Close() }()

	var configs []gitIntegrationConfig
	var broken []bool
	lastID := ""
	for rows.Next() {
		var id, integrationName, name, value string
		var encrypted bool
		if err := rows.Scan(&id, &integrationName, &name, &value, &encrypted); err != nil {
			return nil, fmt.Errorf("scanning %s integration config: %w", integrationType, err)
		}
		if id != lastID {
			configs = append(configs, gitIntegrationConfig{Name: integrationName, Values: map[string]string{}})
			broken = append(broken, false)
			lastID = id
		}
		if encrypted && value != "" {
			decrypted, derr := common.Decrypt(value)
			if derr != nil {
				logger.Warn("git integration: cannot decrypt config value; skipping integration",
					"integration", integrationName, "config", name, "error", derr)
				broken[len(broken)-1] = true
				continue
			}
			value = decrypted
		}
		configs[len(configs)-1].Values[name] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading %s integration config: %w", integrationType, err)
	}

	var usable []gitIntegrationConfig
	for i, c := range configs {
		if !broken[i] && c.Values["password"] != "" {
			usable = append(usable, c)
		}
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("no enabled %s integration with a credential", integrationType)
	}
	return usable, nil
}

// errAppTokenFailed marks a GitHub App integration whose installation token
// could not be minted. Another integration may still see the pull request.
var errAppTokenFailed = errors.New("GitHub App token")

// credentialCannotSee reports whether a provider's answer means this
// credential cannot see the pull request, so another integration may.
func credentialCannotSee(status int) bool {
	return status == 401 || status == 403 || status == 404
}

// providerStatusError is a provider answer other than 200.
type providerStatusError struct {
	endpoint string
	status   int
}

func (e *providerStatusError) Error() string {
	return fmt.Sprintf("%s returned %d", e.endpoint, e.status)
}

// errCredentialCannotSee reports whether err came from a credential that
// cannot see the pull request: an App token that could not be minted, or a
// 401, 403 or 404 from the provider.
func errCredentialCannotSee(err error) bool {
	if errors.Is(err, errAppTokenFailed) {
		return true
	}
	var statusErr *providerStatusError
	return errors.As(err, &statusErr) && credentialCannotSee(statusErr.status)
}
