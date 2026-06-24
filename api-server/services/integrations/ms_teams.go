package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(MsTeams{})
}

const (
	IntegrationMsTeams = "ms_teams"

	// Secret config values — must stay in sync with the encrypted set written by
	// notifications-server (services/messaging_installations.py).
	MsTeamsConfigAccessToken  = "access_token"
	MsTeamsConfigRefreshToken = "refresh_token"

	MsTeamsConfigTokenExpiresAt        = "token_expires_at"
	MsTeamsConfigRefreshTokenExpiresAt = "refresh_token_expires_at"
	MsTeamsConfigAppId                 = "app_id"
	MsTeamsConfigClientId              = "client_id"
	MsTeamsConfigInstalledBy           = "installed_by"
	MsTeamsConfigScopes                = "scopes"
	MsTeamsConfigDefaultTeamId         = "default_team_id"
	MsTeamsConfigDefaultChannelId      = "default_channel_id"
	MsTeamsConfigDefaultTeamName       = "default_team_name"
	MsTeamsConfigDefaultChannelName    = "default_channel_name"
)

// MsTeams connects Microsoft Teams (one per tenant, code-enforced at install) to
// a Nudgebee tenant for notification delivery. integrations.name holds the Azure
// AD tenant ID. The Graph access token and refresh token are stored encrypted in
// integration_config_values; the default destination is the scalar pair
// default_team_id + default_channel_id (a Teams channel is a (team_id,channel_id)
// compound; sending uses the IDs). default_team_name / default_channel_name are
// cached, non-authoritative display labels kept only so the UI can show the
// team/channel names without a live Microsoft Graph lookup.
type MsTeams struct{}

func (MsTeams) Name() string {
	return IntegrationMsTeams
}

func (MsTeams) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (MsTeams) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			MsTeamsConfigAccessToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Microsoft Graph access token",
				IsEncrypted: true,
				Hidden:      true,
			},
			MsTeamsConfigRefreshToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Microsoft Graph refresh token",
				IsEncrypted: true,
				Hidden:      true,
			},
			MsTeamsConfigTokenExpiresAt:        {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigRefreshTokenExpiresAt: {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigAppId:                 {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigClientId:              {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigInstalledBy:           {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigScopes:                {Type: core.ToolSchemaTypeString, Hidden: true},
			MsTeamsConfigDefaultTeamId:         {Type: core.ToolSchemaTypeString, Description: "Default Microsoft Teams team ID for notifications"},
			MsTeamsConfigDefaultChannelId:      {Type: core.ToolSchemaTypeString, Description: "Default Microsoft Teams channel ID for notifications"},
			MsTeamsConfigDefaultTeamName:       {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Microsoft Teams team", Hidden: true},
			MsTeamsConfigDefaultChannelName:    {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Microsoft Teams channel", Hidden: true},
		},
	}
}

func (MsTeams) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}
