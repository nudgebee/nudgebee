package crawl

import (
	"fmt"
	"net/url"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"strings"
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

func TestRetailPriceURLs(t *testing.T) {
	filterOf := func(t *testing.T, rawURL string) string {
		t.Helper()
		parsed, err := url.Parse(rawURL)
		assert.Nil(t, err)
		return parsed.Query().Get("$filter")
	}

	t.Run("no sku names", func(t *testing.T) {
		urls := retailPriceURLs("centralus", "Virtual Machines", "USD", nil)
		assert.Len(t, urls, 1)
		assert.Equal(t, "serviceName eq 'Virtual Machines' and armRegionName eq 'centralus'", filterOf(t, urls[0]))
	})

	t.Run("several sku names", func(t *testing.T) {
		urls := retailPriceURLs("centralus", "Virtual Machines", "USD", []string{"Standard_D2s_v5", "Standard_D4as_v5"})
		assert.Len(t, urls, 1)
		assert.Equal(t,
			"serviceName eq 'Virtual Machines' and armRegionName eq 'centralus' and (armSkuName eq 'Standard_D2s_v5' or armSkuName eq 'Standard_D4as_v5')",
			filterOf(t, urls[0]))
	})

	t.Run("sku names are batched", func(t *testing.T) {
		skuNames := make([]string, 0, 25)
		for i := range 25 {
			skuNames = append(skuNames, fmt.Sprintf("Standard_D%ds_v5", i))
		}
		urls := retailPriceURLs("centralus", "Virtual Machines", "USD", skuNames)
		assert.Len(t, urls, 3)
		seen := 0
		for _, u := range urls {
			filter := filterOf(t, u)
			assert.NotContains(t, filter, " in (")
			assert.Contains(t, filter, "armRegionName eq 'centralus'")
			inBatch := strings.Count(filter, "armSkuName eq ")
			assert.LessOrEqual(t, inBatch, azureSkuFilterBatchSize)
			seen += inBatch
		}
		assert.Equal(t, len(skuNames), seen)
	})
}

func TestAzureCrawlSeveralFlavors(t *testing.T) {
	testenv.RequireEnv(t, "TEST_LIVE_CLOUD_PRICING")
	flavors := []string{"Standard_D4ds_v4", "Standard_D4ds_v5", "Standard_DS2_v2"}
	// Pad past one batch with names that match no SKU, so more than one request goes out.
	for i := range 2 * azureSkuFilterBatchSize {
		flavors = append(flavors, fmt.Sprintf("Standard_NoSuchSku%d", i))
	}
	azurePricingData, err := get_azure_pricing_data("eastus", "Virtual Machines", flavors)
	assert.Nil(t, err)
	priced := map[string]bool{}
	for _, data := range azurePricingData {
		assert.Equal(t, "eastus", data.ResourceRegion)
		if data.PricingModel == "on_demand" && data.ResourceCost > 0 {
			priced[data.ResourceType] = true
		}
	}
	for _, flavor := range flavors[:3] {
		assert.True(t, priced[flavor], "no on-demand price for %s", flavor)
	}
}

func TestAzureResourceMeta(t *testing.T) {
	testenv.RequireMetastore(t)
	tenant, _, user := testenv.RequireTenant(t)
	ctxt := security.NewRequestContextForUserTenant(user, tenant, nil, nil, nil)
	err := AzureResourceMeta(ctxt)
	assert.Nil(t, err)
}
