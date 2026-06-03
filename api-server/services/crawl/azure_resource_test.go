package crawl

import (
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAzureCraw(t *testing.T) {
	testenv.RequireEnv(t, "TEST_LIVE_CLOUD_PRICING")
	azurePricingData, err := get_azure_pricing_data("centralindia", "Virtual Machines", []string{"Standard_D2as_v5"})
	assert.Nil(t, err)
	found := false
	for _, data := range azurePricingData {
		assert.NotNil(t, data)
		if data.ResourceType == "Standard_D2as_v5" {
			assert.Equal(t, 0.1459999978542328, data.ResourceCost)
			found = true
		}
		assert.Equal(t, "centralindia", data.ResourceRegion)
	}
	assert.True(t, found, "Standard_D2as_v5 not found")
	assert.NotNil(t, azurePricingData)
}

func TestAzureResourceMeta(t *testing.T) {
	testenv.RequireMetastore(t)
	tenant, _, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	err := AzureResourceMeta(ctxt)
	assert.Nil(t, err)
}
