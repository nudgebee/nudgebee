package playbooks

import (
	"fmt"
	"nudgebee/services/internal/database"

	"github.com/google/uuid"
)

// resolveDatasourceKey resolves a datasource identifier to the agent-side datasource_key.
// If the input is a UUID (integration ID), it looks up the datasource_key from
// integration_config_values. Otherwise, it returns the input as-is (already a key).
func resolveDatasourceKey(datasourceID string) (string, error) {
	if _, err := uuid.Parse(datasourceID); err != nil {
		// Not a UUID — assume it's already a datasource_key
		return datasourceID, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return "", fmt.Errorf("failed to get database manager: %w", err)
	}

	var datasourceKey string
	err = dbms.Db.Get(&datasourceKey,
		`SELECT value FROM integration_config_values WHERE integration_id = $1 AND name = 'datasource_key'`,
		datasourceID)
	if err != nil {
		// Fallback: use the integration ID itself as the datasource key
		return datasourceID, nil
	}

	return datasourceKey, nil
}

// resolveAccountIDForIntegration looks up the cloud_account_id linked to an integration.
// Returns empty string if the integration ID is not a UUID or has no linked account.
func resolveAccountIDForIntegration(datasourceID string) string {
	if _, err := uuid.Parse(datasourceID); err != nil {
		return ""
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}

	var accountID string
	err = dbms.Db.Get(&accountID,
		`SELECT cloud_account_id FROM integrations_cloud_accounts WHERE integration_id = $1 LIMIT 1`,
		datasourceID)
	if err != nil {
		return ""
	}
	return accountID
}
