package core

import (
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
)

// ListLinkedCloudAccountIDs returns the cloud_account_ids linked to an
// integration identified by tenant + type + config name (and, when non-empty,
// source). Used by hooks that must capture which accounts are affected by an
// integration mutation — e.g. cache invalidation triggers that need the
// account list before a delete cascades the junction rows.
//
// An empty source skips the source filter, matching the fall-through behavior
// of DeleteIntegrationConfig and UpdateIntegrationConfigStatus when no source
// is supplied.
func ListLinkedCloudAccountIDs(
	ctx *security.RequestContext,
	integrationType string,
	integrationConfigName string,
	source string,
) ([]string, error) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("ListLinkedCloudAccountIDs: failed to get database manager: %w", err)
	}

	tenantId := ctx.GetSecurityContext().GetTenantId()

	var rows *sqlx.Rows
	if source != "" {
		rows, err = dbms.Db.Queryx(`
			SELECT ica.cloud_account_id::text
			FROM integrations i
			JOIN integrations_cloud_accounts ica ON ica.integration_id = i.id
			WHERE i.tenant_id = $1
			  AND lower(i.type) = lower($2)
			  AND lower(i.name) = lower($3)
			  AND lower(i.source) = lower($4)
		`, tenantId, integrationType, integrationConfigName, source)
	} else {
		rows, err = dbms.Db.Queryx(`
			SELECT ica.cloud_account_id::text
			FROM integrations i
			JOIN integrations_cloud_accounts ica ON ica.integration_id = i.id
			WHERE i.tenant_id = $1
			  AND lower(i.type) = lower($2)
			  AND lower(i.name) = lower($3)
		`, tenantId, integrationType, integrationConfigName)
	}
	if err != nil {
		return nil, fmt.Errorf("ListLinkedCloudAccountIDs: query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("ListLinkedCloudAccountIDs: scan failed: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("ListLinkedCloudAccountIDs: row iteration failed: %w", rerr)
	}
	return ids, nil
}

// ListLinkedCloudAccountIDsByIntegrationID returns the cloud_account_ids linked to
// one integration, identified by id rather than by type + name.
//
// CreateIntegrationConfig doubles as the update path and unlinks every account
// absent from the caller's accountIds, so a cache-invalidation trigger has to know
// the pre-save link set — and it has to look it up by id, because the same call can
// rename the integration, which would make a name-based lookup (ListLinkedCloudAccountIDs)
// miss. Still scoped by tenant so an id from another tenant resolves to nothing.
//
// An empty integrationId returns no ids and no error: that is the create case, where
// nothing was linked yet.
func ListLinkedCloudAccountIDsByIntegrationID(
	ctx *security.RequestContext,
	integrationId string,
) ([]string, error) {
	if integrationId == "" {
		return nil, nil
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("ListLinkedCloudAccountIDsByIntegrationID: failed to get database manager: %w", err)
	}

	rows, err := dbms.Db.Queryx(`
		SELECT ica.cloud_account_id::text
		FROM integrations i
		JOIN integrations_cloud_accounts ica ON ica.integration_id = i.id
		WHERE i.tenant_id = $1
		  AND i.id = $2
	`, ctx.GetSecurityContext().GetTenantId(), integrationId)
	if err != nil {
		return nil, fmt.Errorf("ListLinkedCloudAccountIDsByIntegrationID: query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("ListLinkedCloudAccountIDsByIntegrationID: scan failed: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("ListLinkedCloudAccountIDsByIntegrationID: row iteration failed: %w", rerr)
	}
	return ids, nil
}
