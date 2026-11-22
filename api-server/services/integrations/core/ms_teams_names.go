package core

import (
	"database/sql"
	"errors"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
)

// The names of the Microsoft Teams integrations are public (a Microsoft tenant
// id, a Teams team id), so the generic RPCs can't create, rename, delete or
// re-status one; see common.IsMsTeamsOwnedIntegrationType.
var (
	ErrMsTeamsCreatedByConnect = errors.New("integrations: Microsoft Teams is connected from the Microsoft Teams integration, not this form")
	ErrMsTeamsNameIsFixed      = errors.New("integrations: the name of a Microsoft Teams connection can't be changed")
	ErrMsTeamsTeamIsFixed      = errors.New("integrations: a Microsoft Teams team can't be edited here")
	ErrMsTeamsFieldIsFixed     = errors.New("integrations: only the default team and channel of a Microsoft Teams connection can be changed here")
	ErrMsTeamsDisconnectOnly   = common.ErrMsTeamsDisconnectOnly
)

// IsMsTeamsOwnedType reports whether notifications-server owns the
// integration type, matched on the canonical type.
func IsMsTeamsOwnedType(integrationType string) bool {
	return common.IsMsTeamsOwnedIntegrationType(integrationType)
}

// CheckMsTeamsGenericDeleteOrStatus rejects a generic delete or status change
// of an ms_teams or ms_teams_team integration.
func CheckMsTeamsGenericDeleteOrStatus(integrationType string) error {
	if IsMsTeamsOwnedType(integrationType) {
		return ErrMsTeamsDisconnectOnly
	}
	return nil
}

// msTeamsEditableFields are the only config values the generic update may write
// on a workspace's ms_teams row: its default destination.
var msTeamsEditableFields = map[string]bool{
	"default_team_id":      true,
	"default_team_name":    true,
	"default_channel_id":   true,
	"default_channel_name": true,
}

// CheckMsTeamsGenericWrite rejects a generic create of an ms_teams or
// ms_teams_team integration, any generic update of an ms_teams_team one, and an
// update of an ms_teams one that renames it or writes anything but its default
// destination. Creates are matched on the canonical type, the way the registry
// resolves it. Updates are judged by the stored row's type, whatever type the
// request names, because the update path never changes a row's type.
func CheckMsTeamsGenericWrite(tenantId, integrationType, integrationId, configName string, values []IntegrationConfigValue) error {
	if integrationId == "" {
		if IsMsTeamsOwnedType(integrationType) {
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
	switch strings.ToLower(storedType) {
	case "ms_teams_team":
		return ErrMsTeamsTeamIsFixed
	case "ms_teams":
		if storedName != configName {
			return ErrMsTeamsNameIsFixed
		}
		for _, v := range values {
			if !msTeamsEditableFields[v.Name] {
				return ErrMsTeamsFieldIsFixed
			}
		}
	}
	return nil
}
