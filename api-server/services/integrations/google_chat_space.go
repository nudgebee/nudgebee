package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(GoogleChatSpace{})
}

const (
	IntegrationGoogleChatSpace = "google_chat_space"

	GoogleChatSpaceConfigDisplayName = "display_name"
	GoogleChatSpaceConfigSpaceType   = "space_type"
)

// GoogleChatSpace binds a Google Chat space (spaces/XYZ) to a Nudgebee tenant.
// The binding's primary key on the integrations table is the space ID, stored
// in integrations.name; a partial unique index on (name) WHERE type =
// 'google_chat_space' keeps it globally unique across tenants (Google Chat
// space IDs are globally unique on Google's side).
type GoogleChatSpace struct{}

func (GoogleChatSpace) Name() string {
	return IntegrationGoogleChatSpace
}

func (GoogleChatSpace) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (GoogleChatSpace) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			GoogleChatSpaceConfigDisplayName: {
				Type:        core.ToolSchemaTypeString,
				Description: "Human-readable space name captured at bind time",
				Hidden:      true,
			},
			GoogleChatSpaceConfigSpaceType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Google Chat space type (SPACE or DIRECT_MESSAGE)",
				Hidden:      true,
				Default:     "SPACE",
			},
		},
	}
}

func (GoogleChatSpace) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}
