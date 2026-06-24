package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(Slack{})
}

const (
	IntegrationSlack = "slack"

	// Secret config values — must stay in sync with the encrypted set written by
	// notifications-server (services/messaging_installations.py).
	SlackConfigBotToken     = "bot_token"
	SlackConfigRefreshToken = "refresh_token"

	SlackConfigTokenExpiresAt        = "token_expires_at"
	SlackConfigRefreshTokenExpiresAt = "refresh_token_expires_at"
	SlackConfigBotId                 = "bot_id"
	SlackConfigAppId                 = "app_id"
	SlackConfigClientId              = "client_id"
	SlackConfigInstalledBy           = "installed_by"
	SlackConfigScopes                = "scopes"
	SlackConfigTeamName              = "team_name"
	SlackConfigDefaultChannelId      = "default_channel_id"
	SlackConfigDefaultChannelName    = "default_channel_name"
)

// Slack connects a Slack workspace (one per tenant, code-enforced at install) to
// a Nudgebee tenant for notification delivery. integrations.name holds the Slack
// team (workspace) ID. The OAuth bot token and refresh token are stored encrypted
// in integration_config_values; the default destination is the scalar
// default_channel_id (sending uses the ID). default_channel_name is a cached,
// non-authoritative display label kept only so the UI can show the channel name
// without a live Slack lookup.
type Slack struct{}

func (Slack) Name() string {
	return IntegrationSlack
}

func (Slack) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (Slack) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			SlackConfigBotToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Slack bot OAuth token",
				IsEncrypted: true,
				Hidden:      true,
			},
			SlackConfigRefreshToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Slack OAuth refresh token",
				IsEncrypted: true,
				Hidden:      true,
			},
			SlackConfigTokenExpiresAt:        {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigRefreshTokenExpiresAt: {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigBotId:                 {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigAppId:                 {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigClientId:              {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigInstalledBy:           {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigScopes:                {Type: core.ToolSchemaTypeString, Hidden: true},
			SlackConfigTeamName:              {Type: core.ToolSchemaTypeString, Description: "Slack workspace name", Hidden: true},
			SlackConfigDefaultChannelId:      {Type: core.ToolSchemaTypeString, Description: "Default Slack channel ID for notifications"},
			SlackConfigDefaultChannelName:    {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Slack channel", Hidden: true},
		},
	}
}

func (Slack) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}
