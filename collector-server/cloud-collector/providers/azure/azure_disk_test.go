package azure

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPremiumV2Decision(t *testing.T) {
	// The gate must fail OPEN on any untrustworthy answer and suppress only when a region
	// positively lists disk SKUs without PremiumV2_LRS among them.
	tests := []struct {
		name          string
		listErr       bool
		sawDiskSKU    bool
		foundV2       bool
		wantAvailable bool
	}{
		{"list error fails open", true, false, false, true},
		{"list error fails open even if disks were seen", true, true, false, true},
		{"no disk SKUs seen fails open (query/format issue)", false, false, false, true},
		{"disks seen with PremiumV2_LRS present is available", false, true, true, true},
		{"disks seen without PremiumV2_LRS is unavailable", false, true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantAvailable, premiumV2Decision(tt.listErr, tt.sawDiskSKU, tt.foundV2))
		})
	}
}

// seedAzureDiskTierPrice caches a disk tier's monthly retail price, so pricing
// a disk in that tier does not call the Retail Prices API.
func seedAzureDiskTierPrice(t *testing.T, sku, region string, sizeGB, monthly float64) {
	t.Helper()
	cache := GetPricingCache()
	wasEnabled := cache.IsEnabled()
	cache.SetEnabled(true)
	key := fmt.Sprintf("disk:%s:%s:%s", sku, normalizeAzureRegion(region), azureDiskTierMeter(sku, sizeGB))
	cache.setCachedPrice(key, PriceEntry{UnitPrice: monthly, FetchedAt: time.Now()})
	t.Cleanup(func() {
		cache.mu.Lock()
		delete(cache.prices, key)
		cache.mu.Unlock()
		cache.SetEnabled(wasEnabled)
	})
}

func azureTestDiskWithState(state armcompute.DiskState, status providers.ResourceStatus) providers.Resource {
	disk := azureDiskResource(armcompute.Disk{
		SKU: azureDiskSKU("Premium_LRS"),
		Properties: &armcompute.DiskProperties{
			DiskSizeGB: to.Ptr(int32(100)),
			DiskState:  to.Ptr(state),
		},
	})
	disk.Status = status
	// Stored rows carry the service name lowercased, as ListResources returns it.
	disk.ServiceName = strings.ToLower(disk.ServiceName)
	return disk
}

func listAzureDiskRecommendations(t *testing.T, disk providers.Resource) map[string]providers.Recommendation {
	t.Helper()
	response, err := defaultAzureProvider.ListRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{},
		providers.ListRecommendationsRequest{ServiceName: "microsoft.compute/disks", SkipDiskPerformance: true}, []providers.Resource{disk})
	require.NoError(t, err)
	byRule := map[string]providers.Recommendation{}
	for _, recommendation := range response.Items {
		byRule[recommendation.RuleName] = recommendation
	}
	return byRule
}

// An unattached disk is stored Inactive. It gets the remove-disk
// recommendation priced at its billed tier: 100 GiB Premium bills as P10.
func TestAzureUnattachedDiskGetsRemoveRecommendation(t *testing.T) {
	seedAzureDiskTierPrice(t, "Premium_LRS", "eastus", 100, 19.71)

	byRule := listAzureDiskRecommendations(t, azureTestDiskWithState(armcompute.DiskStateUnattached, providers.ResourceStatusInactive))

	require.Contains(t, byRule, "azure_disk_unattached_volume")
	remove := byRule["azure_disk_unattached_volume"]
	assert.InDelta(t, 19.71, remove.Savings, 0.001)
	assert.Equal(t, providers.RecommendationActionDelete, remove.Action)
	assert.Equal(t, testAzureDiskID, remove.ResourceId)
	assert.Contains(t, byRule, "azure_disk_unattached_unencrypted")
	assert.NotContains(t, byRule, "azure_disk_premium_ssd_v2_upgrade", "a disk to delete is not also offered an upgrade")
}

func TestAzureAttachedDiskStillGetsUpgradeRecommendation(t *testing.T) {
	byRule := listAzureDiskRecommendations(t, azureTestDiskWithState(armcompute.DiskStateAttached, providers.ResourceStatusActive))

	assert.Contains(t, byRule, "azure_disk_premium_ssd_v2_upgrade")
	assert.NotContains(t, byRule, "azure_disk_unattached_volume")
}
