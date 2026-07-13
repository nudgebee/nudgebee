package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(Discord{})
}

const (
	IntegrationDiscord = "discord"

	// Secret config values — must stay in sync with the encrypted set written by
	// notifications-server (services/messaging_installations.py).
	DiscordConfigBotToken = "bot_token"

	DiscordConfigBotId              = "bot_id"
	DiscordConfigClientId           = "client_id"
	DiscordConfigInstalledBy        = "installed_by"
	DiscordConfigTeamName           = "team_name"
	DiscordConfigDefaultChannelId   = "default_channel_id"
	DiscordConfigDefaultChannelName = "default_channel_name"
)

// Discord connects a Discord bot (one per tenant, code-enforced at install in
// notifications-server) to a Nudgebee tenant for notification delivery.
// integrations.name holds the bot username. The bot token is a long-lived
// static credential (no refresh flow) stored encrypted in
// integration_config_values; the default destination is the scalar
// default_channel_id. Installs are written by notifications-server
// (/install/discord), which validates the token against the Discord API.
type Discord struct{}

func (Discord) Name() string {
	return IntegrationDiscord
}

func (Discord) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (Discord) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			DiscordConfigBotToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Discord bot token",
				IsEncrypted: true,
				Hidden:      true,
			},
			DiscordConfigBotId:              {Type: core.ToolSchemaTypeString, Hidden: true},
			DiscordConfigClientId:           {Type: core.ToolSchemaTypeString, Hidden: true},
			DiscordConfigInstalledBy:        {Type: core.ToolSchemaTypeString, Hidden: true},
			DiscordConfigTeamName:           {Type: core.ToolSchemaTypeString, Description: "Discord bot display name", Hidden: true},
			DiscordConfigDefaultChannelId:   {Type: core.ToolSchemaTypeString, Description: "Default Discord channel ID for notifications"},
			DiscordConfigDefaultChannelName: {Type: core.ToolSchemaTypeString, Description: "Cached display name of the default Discord channel", Hidden: true},
		},
	}
}

func (Discord) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}
