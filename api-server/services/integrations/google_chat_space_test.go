package integrations

import (
	"nudgebee/services/integrations/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGoogleChatSpace_Identity(t *testing.T) {
	g := GoogleChatSpace{}
	assert.Equal(t, IntegrationGoogleChatSpace, g.Name())
	assert.Equal(t, core.IntegrationCategoryMessaging, g.Category())
}

func TestGoogleChatSpace_IsTenantScoped(t *testing.T) {
	g := GoogleChatSpace{}
	assert.True(t, g.Category().IsTenantScoped(),
		"messaging category must be tenant-scoped so CreateIntegrationConfig accepts empty accountIds")
}

func TestGoogleChatSpace_RegisteredAndLookup(t *testing.T) {
	got, ok := core.GetIntegration(IntegrationGoogleChatSpace)
	assert.True(t, ok, "google_chat_space must be registered at init")
	assert.Equal(t, IntegrationGoogleChatSpace, got.Name())
}

func TestGoogleChatSpace_ConfigSchema_HiddenKeys(t *testing.T) {
	schema := GoogleChatSpace{}.ConfigSchema()
	assert.Contains(t, schema.Properties, GoogleChatSpaceConfigDisplayName)
	assert.Contains(t, schema.Properties, GoogleChatSpaceConfigSpaceType)
	assert.True(t, schema.Properties[GoogleChatSpaceConfigDisplayName].Hidden)
	assert.True(t, schema.Properties[GoogleChatSpaceConfigSpaceType].Hidden)
	assert.Equal(t, "SPACE", schema.Properties[GoogleChatSpaceConfigSpaceType].Default)
}

func TestIntegrationCategory_IsTenantScoped(t *testing.T) {
	assert.True(t, core.IntegrationCategoryTicketing.IsTenantScoped())
	assert.True(t, core.IntegrationCategoryMessaging.IsTenantScoped())
	assert.False(t, core.IntegrationCategoryDatabase.IsTenantScoped())
	assert.False(t, core.IntegrationCategoryMetrics.IsTenantScoped())
	assert.False(t, core.IntegrationCategoryCICD.IsTenantScoped())
}
