package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

func init() {
	core.RegisterIntegration(MsTeamsTeam{})
}

const (
	IntegrationMsTeamsTeam = "ms_teams_team"

	MsTeamsTeamConfigThreadId    = "team_thread_id"
	MsTeamsTeamConfigTeamName    = "team_name"
	MsTeamsTeamConfigServiceUrl  = "service_url"
	MsTeamsTeamConfigAadTenantId = "aad_tenant_id"
	MsTeamsTeamConfigAppId       = "app_id"
)

// MsTeamsTeam is a Teams team that has the NudgeBee bot installed. notifications-
// server writes it from Bot Framework install events, bound to the tenant whose
// ms_teams integration is named after the team's Microsoft tenant. The row's
// primary key on the integrations table is the team's Entra group id, stored in
// integrations.name; a partial unique index on (name) WHERE type =
// 'ms_teams_team' keeps it globally unique. Uninstall disables the row rather
// than deleting it.
type MsTeamsTeam struct{}

func (MsTeamsTeam) Name() string {
	return IntegrationMsTeamsTeam
}

func (MsTeamsTeam) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessaging
}

func (MsTeamsTeam) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.IntegrationSchemaProperty{
			MsTeamsTeamConfigThreadId:    {Type: core.ToolSchemaTypeString, Description: "Bot Framework team id (19:...@thread.tacv2)", Hidden: true},
			MsTeamsTeamConfigTeamName:    {Type: core.ToolSchemaTypeString, Description: "Team display name captured from Teams", Hidden: true},
			MsTeamsTeamConfigServiceUrl:  {Type: core.ToolSchemaTypeString, Description: "Bot Framework service URL for the team", Hidden: true},
			MsTeamsTeamConfigAadTenantId: {Type: core.ToolSchemaTypeString, Description: "Microsoft tenant id of the team", Hidden: true},
			MsTeamsTeamConfigAppId:       {Type: core.ToolSchemaTypeString, Description: "Bot app id installed in the team", Hidden: true},
		},
	}
}

func (MsTeamsTeam) ValidateConfig(_ *security.SecurityContext, _ []core.IntegrationConfigValue, _ string) []error {
	return nil
}
