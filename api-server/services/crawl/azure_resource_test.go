package crawl

import (
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAzureCraw(t *testing.T) {
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
	ctxt := security.NewRequestContextForUserTenant("af4cb6af-1254-421d-bfa5-ffcfe649017e", "0053b816-4b45-4dcd-a612-19545110f8aa", nil, nil, nil)
	err := AzureResourceMeta(ctxt)
	assert.Nil(t, err)
}
