package integrations

import (
	"nudgebee/services/integrations/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMsTeamsTeam_RegisteredAsTenantScopedMessaging(t *testing.T) {
	got, ok := core.GetIntegration(IntegrationMsTeamsTeam)
	assert.True(t, ok, "ms_teams_team must be registered at init")
	assert.Equal(t, IntegrationMsTeamsTeam, got.Name())
	assert.Equal(t, core.IntegrationCategoryMessaging, got.Category())
	assert.True(t, got.Category().IsTenantScoped())
}

func TestMsTeamsTeam_ConfigSchemaIsHidden(t *testing.T) {
	schema := MsTeamsTeam{}.ConfigSchema()
	for _, key := range []string{MsTeamsTeamConfigThreadId, MsTeamsTeamConfigTeamName, MsTeamsTeamConfigServiceUrl,
		MsTeamsTeamConfigAadTenantId, MsTeamsTeamConfigAppId} {
		assert.Contains(t, schema.Properties, key)
		assert.True(t, schema.Properties[key].Hidden, key)
	}
}
