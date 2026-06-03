package integrations

import (
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArgoCD_Name(t *testing.T) {
	integration := ArgoCD{}
	assert.Equal(t, IntegrationArgoCD, integration.Name())
}

func TestArgoCD_Category(t *testing.T) {
	integration := ArgoCD{}
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}

func TestArgoCD_ConfigSchema(t *testing.T) {
	integration := ArgoCD{}
	schema := integration.ConfigSchema()

	// Test required fields
	assert.Contains(t, schema.Required, ArgoCDConfigK8sSecret)
	assert.Contains(t, schema.Required, ArgoCDConfigServer)

	// Test properties
	assert.Contains(t, schema.Properties, ArgoCDConfigK8sSecret)
	assert.Contains(t, schema.Properties, ArgoCDConfigServer)
	assert.Contains(t, schema.Properties, ArgoCDConfigTimeout)
	assert.Contains(t, schema.Properties, ArgoCDConfigInsecure)

	// Test default values
	assert.Equal(t, "30", schema.Properties[ArgoCDConfigTimeout].Default)
	assert.Equal(t, "false", schema.Properties[ArgoCDConfigInsecure].Default)

	// Test enum values
	assert.Contains(t, schema.Properties[ArgoCDConfigInsecure].Enum, "true")
	assert.Contains(t, schema.Properties[ArgoCDConfigInsecure].Enum, "false")
}

func TestArgoCD_ValidateConfig(t *testing.T) {
	integration := ArgoCD{}
	securityContext := &security.SecurityContext{}
	accountId := os.Getenv("TEST_ACCOUNT")

	// Test with empty config values
	errors := integration.ValidateConfig(securityContext, []core.IntegrationConfigValue{}, accountId)
	assert.Empty(t, errors)

	// Test with basic config values
	configValues := []core.IntegrationConfigValue{
		{
			Name:  ArgoCDConfigK8sSecret,
			Value: "argocd-secret",
		},
		{
			Name:  ArgoCDConfigServer,
			Value: "https://argocd.example.com",
		},
	}

	errors = integration.ValidateConfig(securityContext, configValues, accountId)
	assert.Empty(t, errors)
}

func TestArgoCD_Integration_Registration(t *testing.T) {
	// Test that the integration is registered correctly
	integration, found := core.GetIntegration(IntegrationArgoCD)
	assert.True(t, found)
	assert.Equal(t, IntegrationArgoCD, integration.Name())
	assert.Equal(t, core.IntegrationCategoryCICD, integration.Category())
}
