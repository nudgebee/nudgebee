package core

import (
	"database/sql"
	"errors"
	"strings"

	"nudgebee/services/internal/database"
)

// MsTeamsAadTenantId returns the Microsoft tenant id that names an ms_teams
// integration. Legacy installs stored the MSAL home_account_id
// ("<object id>.<tenant id>"); a value without a dot is already a tenant id.
func MsTeamsAadTenantId(accountId string) string {
	accountId = strings.TrimSpace(accountId)
	if _, tenant, ok := strings.Cut(accountId, "."); ok && tenant != "" {
		return tenant
	}
	return accountId
}

// The names of these integrations prove ownership: an ms_teams name is a
// Microsoft tenant id and an ms_teams_team name is a Teams team id, and both
// are public. Only Microsoft Teams Connect and the bot's install events may set
// them, so the generic create/update RPC can't create one or rename it.
var msTeamsNameOwnedTypes = map[string]bool{"ms_teams": true, "ms_teams_team": true}

var (
	ErrMsTeamsCreatedByConnect = errors.New("integrations: Microsoft Teams is connected from the Microsoft Teams integration, not this form")
	ErrMsTeamsNameIsFixed      = errors.New("integrations: the name of a Microsoft Teams connection can't be changed")
)

// CheckMsTeamsGenericWrite rejects a generic create of an ms_teams or
// ms_teams_team integration, or an update that changes one's name. Creates are
// matched on the canonical type, the way the registry resolves it. Updates are
// judged by the stored row's type, whatever type the request names, because the
// update path never changes a row's type.
func CheckMsTeamsGenericWrite(tenantId, integrationType, integrationId, configName string) error {
	if integrationId == "" {
		if msTeamsNameOwnedTypes[strings.ToLower(strings.TrimSpace(integrationType))] {
			return ErrMsTeamsCreatedByConnect
		}
		return nil
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	var storedType, storedName string
	err = dbms.Db.QueryRow(`SELECT type, name FROM integrations WHERE id = $1 AND tenant_id = $2`,
		integrationId, tenantId).Scan(&storedType, &storedName)
	if errors.Is(err, sql.ErrNoRows) {
		// The save reports its own not-found error.
		return nil
	}
	if err != nil {
		return err
	}
	if msTeamsNameOwnedTypes[storedType] && storedName != configName {
		return ErrMsTeamsNameIsFixed
	}
	return nil
}
