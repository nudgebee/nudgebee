package common

import (
	"errors"
	"strings"
)

// notifications-server alone writes these integration types: an ms_teams_team
// row is a Teams team bound to a workspace by the bot's connect link, and a
// workspace's ms_teams row exists while it has a bound team.
var msTeamsOwnedIntegrationTypes = map[string]bool{"ms_teams": true, "ms_teams_team": true}

var ErrMsTeamsDisconnectOnly = errors.New("integrations: Microsoft Teams is disconnected from the Microsoft Teams integration, not this form")

// IsMsTeamsOwnedIntegrationType reports whether notifications-server owns the
// integration type, matched on the canonical type.
func IsMsTeamsOwnedIntegrationType(integrationType string) bool {
	return msTeamsOwnedIntegrationTypes[strings.ToLower(strings.TrimSpace(integrationType))]
}
