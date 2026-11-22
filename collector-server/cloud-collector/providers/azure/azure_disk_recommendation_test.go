package azure

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAzurePrices are the East US Premium SSD v2 rates the Retail Prices API returned
// when this was written: $0.000007 per IOPS-hour and $0.000055 per MB/s-hour.
var testAzurePrices = azureDiskPerformancePrices{IOPSPerMonth: 0.00511, ThroughputPerMBpsMonth: 0.04015}

func testAzurePriceLookup(regions ...string) (azureDiskPerformancePriceLookup, *[]string) {
	var asked []string
	return func(_ providers.CloudProviderContext, sku, region string) (azureDiskPerformancePrices, bool) {
		asked = append(asked, sku+"@"+region)
		for _, priced := range regions {
			if priced == region {
				return testAzurePrices, true
			}
		}
		return azureDiskPerformancePrices{}, false
	}, &asked
}

func TestPremiumV2PricesFromRetailItems(t *testing.T) {
	// The shape the Retail Prices API returns for East US: each performance meter as
	// a free tier and a paid tier, among unrelated meters of the same product.
	items := []RetailPriceItem{
		{MeterName: "Confidential Compute Encryption LRS Provisioned Capacity", RetailPrice: 0.000111},
		{MeterName: "Premium LRS Provisioned Throughput (MBps)", RetailPrice: 0},
		{MeterName: "Premium LRS Provisioned Throughput (MBps)", RetailPrice: 0.000055, TierMinimumUnits: 125},
		{MeterName: "Premium LRS Provisioned IOPS", RetailPrice: 0},
		{MeterName: "Premium LRS Provisioned IOPS", RetailPrice: 0.000007, TierMinimumUnits: 3000},
		{MeterName: "Premium LRS Provisioned Capacity", RetailPrice: 0.00011},
	}

	prices, ok := premiumV2PricesFromRetailItems(items)
	require.True(t, ok)
	// Hourly rates over Azure's 730-hour month.
	assert.InDelta(t, 0.00511, prices.IOPSPerMonth, 1e-9)
	assert.InDelta(t, 0.04015, prices.ThroughputPerMBpsMonth, 1e-9)

	t.Run("a missing meter means the price is not known", func(t *testing.T) {
		_, ok := premiumV2PricesFromRetailItems(items[:3])
		assert.False(t, ok)
	})
	t.Run("only free tiers means the price is not known", func(t *testing.T) {
		_, ok := premiumV2PricesFromRetailItems([]RetailPriceItem{items[1], items[3]})
		assert.False(t, ok)
	})
	t.Run("no items", func(t *testing.T) {
		_, ok := premiumV2PricesFromRetailItems(nil)
		assert.False(t, ok)
	})
}

func TestAzureDiskPerformanceProfile(t *testing.T) {
	profile, ok := azureDiskPerformanceProfile(providers.DiskConfig{DiskType: "PremiumV2_LRS"}, testAzurePrices)
	require.True(t, ok)
	assert.Equal(t, 3000.0, profile.MinIOPS)
	// 125 MB/s and 0.25 MB/s per IOPS, expressed in MiB/s.
	assert.InDelta(t, 119.20928955078125, profile.MinThroughputMiBps, 1e-9)
	assert.InDelta(t, 0.2384185791015625, profile.MaxThroughputMiBpsPerIOPS, 1e-12)
	assert.Zero(t, profile.MinThroughputMiBpsPerIOPS)
	assert.InDelta(t, 0.0, profile.MonthlyIOPSCost(3000), 1e-9)
	assert.InDelta(t, 5000*0.00511, profile.MonthlyIOPSCost(8000), 1e-9)
	// Throughput is priced per MB/s: the baseline is free and 300 MB/s costs 175 × $0.04015.
	assert.InDelta(t, 0.0, profile.MonthlyThroughputCost(providers.MBpsToMiBps(125)), 1e-9)
	assert.InDelta(t, 175*0.04015, profile.MonthlyThroughputCost(providers.MBpsToMiBps(300)), 1e-9)

	_, ok = azureDiskPerformanceProfile(providers.DiskConfig{DiskType: "premiumv2_lrs"}, testAzurePrices)
	assert.True(t, ok)

	for _, sku := range []string{"UltraSSD_LRS", "Premium_LRS", "StandardSSD_LRS", "Standard_LRS", ""} {
		_, ok := azureDiskPerformanceProfile(providers.DiskConfig{DiskType: sku}, testAzurePrices)
		assert.False(t, ok, "SKU %q should have no profile", sku)
	}

	// Without a price for the disk's region there is no profile, so nothing is recommended.
	_, ok = azureDiskPerformanceProfile(providers.DiskConfig{DiskType: "PremiumV2_LRS"}, azureDiskPerformancePrices{})
	assert.False(t, ok)
	_, ok = azureDiskPerformanceProfile(providers.DiskConfig{DiskType: "PremiumV2_LRS"}, azureDiskPerformancePrices{IOPSPerMonth: 0.00511})
	assert.False(t, ok)
}

func TestAzureWholeMBps(t *testing.T) {
	// A value that came from whole MB/s converts back to exactly that.
	assert.Equal(t, int64(125), azureWholeMBps(providers.MBpsToMiBps(125)))
	assert.Equal(t, int64(300), azureWholeMBps(providers.MBpsToMiBps(300)))
	// Otherwise it rounds up: 120 MiB/s is 125.83 MB/s.
	assert.Equal(t, int64(126), azureWholeMBps(120))
}

func azureTestDisk(n int, sku string, iops, mbps int64, attached bool) providers.Resource {
	disk := armcompute.Disk{
		SKU: azureDiskSKU(sku),
		Properties: &armcompute.DiskProperties{
			DiskSizeGB:        to.Ptr(int32(512)),
			DiskIOPSReadWrite: to.Ptr(iops),
			DiskMBpsReadWrite: to.Ptr(mbps),
			DiskState:         to.Ptr(armcompute.DiskStateUnattached),
		},
	}
	if attached {
		disk.ManagedBy = to.Ptr(testAzureVMID)
		disk.Properties.DiskState = to.Ptr(armcompute.DiskStateAttached)
	}
	resource := azureDiskResource(disk)
	resource.Id = azureTestDiskID(n)
	resource.Arn = resource.Id
	return resource
}

// fakeAzureDiskUsage answers Azure Monitor queries with a full window at a constant
// level per disk: operations per second and bytes per second, all of it reads.
func fakeAzureDiskUsage(usage map[string][2]float64, requested *[]string) providers.DiskMetricsQuery {
	return func(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		var items []providers.MetricItem
		for _, diskID := range filter.ResourceIds {
			*requested = append(*requested, diskID)
			level, ok := usage[diskID]
			if !ok {
				continue
			}
			for _, metric := range filter.MetricNames {
				value := 0.0
				switch metric {
				case "Composite Disk Read Operations/sec":
					value = level[0]
				case "Composite Disk Read Bytes/sec":
					value = level[1]
				}
				item := providers.MetricItem{Name: metric, Statistics: "Average", ResourceId: diskID}
				for ts := *filter.StartDate; ts.Before(*filter.EndDate); ts = ts.Add(filter.Step) {
					item.Values = append(item.Values, value)
					item.Timestamps = append(item.Timestamps, ts)
				}
				items = append(items, item)
			}
		}
		return providers.QueryMetricsResponse{Items: items}, nil
	}
}

func azureTestRuleOptions() providers.DiskPerformanceRuleOptions {
	return providers.DiskPerformanceRuleOptions{
		Thresholds: providers.DiskPerformanceThresholds{HeadroomPercent: 30, MaxUtilizationPercent: 50, MinMonthlySavings: 1},
		MaxDisks:   200,
		Now:        time.Date(2026, 10, 9, 11, 7, 0, 0, time.UTC),
	}
}

func TestAzureDiskPerformanceRecommendations(t *testing.T) {
	resources := []providers.Resource{
		azureTestDisk(1, "PremiumV2_LRS", 8000, 300, true),
		azureTestDisk(2, "PremiumV2_LRS", 3000, 125, true),
		azureTestDisk(3, "Premium_LRS", 1100, 125, true),
		azureTestDisk(4, "PremiumV2_LRS", 20000, 600, false),
		azureTestDisk(5, "UltraSSD_LRS", 50000, 1000, true),
	}
	var requested []string
	// Disk 1 peaks at 1,000 operations/s and 50,000,000 bytes/s (50 MB/s).
	query := fakeAzureDiskUsage(map[string][2]float64{azureTestDiskID(1): {1000, 50_000_000}}, &requested)

	priceLookup, pricesAsked := testAzurePriceLookup("eastus")
	recommendations := azureDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{}, resources, query, priceLookup, azureTestRuleOptions())

	// Only the attached Premium SSD v2 disk above its free baseline is fetched: the
	// one at baseline, the tiered Premium SSD, the unattached disk and the Ultra
	// Disk (limits not confirmed) are not. Prices are looked up once per region.
	assert.Equal(t, []string{azureTestDiskID(1)}, requested)
	assert.Equal(t, []string{"PremiumV2_LRS@eastus"}, *pricesAsked)
	require.Len(t, recommendations, 1)

	recommendation := recommendations[0]
	assert.Equal(t, "disk_performance_overprovisioned", recommendation.RuleName)
	assert.Equal(t, providers.RecommendationCategoryRightSizing, recommendation.CategoryName)
	assert.Equal(t, providers.RecommendationActionModify, recommendation.Action)
	assert.Equal(t, azureTestDiskID(1), recommendation.ResourceId)
	assert.Equal(t, "Microsoft.Compute/disks", recommendation.ResourceServiceName)
	assert.Equal(t, "Microsoft.Compute/disks", recommendation.ResourceType)
	assert.Equal(t, "eastus", recommendation.ResourceRegion)
	// 5,000 IOPS at $0.00511 plus 175 MB/s at $0.04015.
	assert.InDelta(t, 5000*0.00511+175*0.04015, recommendation.Savings, 1e-6)

	data := recommendation.Data
	assert.Equal(t, azureTestDiskID(1), data["resource_id"])
	assert.Equal(t, "PremiumV2_LRS", data["sku"])
	assert.Equal(t, int64(8000), data["current_iops"])
	assert.Equal(t, int64(3000), data["recommended_iops"])
	assert.Equal(t, 1000.0, data["peak_iops"])
	// Throughput in Azure's own unit: 300 MB/s provisioned, lowered to the 125 MB/s baseline.
	assert.Equal(t, int64(300), data["current_throughput_mbps"])
	assert.Equal(t, int64(125), data["recommended_throughput_mbps"])
	// 50,000,000 bytes/s is 47.7 MiB/s.
	assert.Equal(t, 47.7, data["peak_throughput_mibps"])
	assert.Equal(t, 100.0, data["coverage_percent"])
}

func TestAzureDiskPerformanceRuleCanBeSwitchedOff(t *testing.T) {
	previous := config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled
	config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled = false
	defer func() { config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled = previous }()

	// With the switch off nothing is built or fetched, so no Azure credentials are needed.
	resources := []providers.Resource{azureTestDisk(1, "PremiumV2_LRS", 8000, 300, true)}
	recommendations := (&diskService{}).diskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{}, providers.ListRecommendationsRequest{}, resources)
	assert.Nil(t, recommendations)
}

// The Premium SSD v2 upgrade rule (azure_disk_premium_ssd_v2_upgrade) and the SKU
// upgrade rule act on Premium_LRS, StandardSSD_LRS and Standard_LRS disks. This rule
// must never act on those, so one disk never gets conflicting advice.
func TestAzureDiskPerformanceRuleDoesNotOverlapUpgradeRules(t *testing.T) {
	resources := []providers.Resource{
		azureTestDisk(1, "Premium_LRS", 5000, 200, true),
		azureTestDisk(2, "StandardSSD_LRS", 500, 60, true),
		azureTestDisk(3, "Standard_LRS", 500, 60, true),
	}
	var requested []string
	query := fakeAzureDiskUsage(map[string][2]float64{}, &requested)
	priceLookup, pricesAsked := testAzurePriceLookup("eastus")

	recommendations := azureDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{}, resources, query, priceLookup, azureTestRuleOptions())

	assert.Empty(t, requested)
	assert.Empty(t, *pricesAsked)
	assert.Empty(t, recommendations)
}

// When the price of a disk's region cannot be read, its saving cannot be verified, so
// nothing is recommended and its usage is not even fetched. It is reported as
// unevaluated, so a price lookup failing does not close a recommendation it already has.
func TestAzureDiskPerformanceRecommendationsMissingPricingMeansNoRecommendation(t *testing.T) {
	resources := []providers.Resource{azureTestDisk(1, "PremiumV2_LRS", 8000, 300, true)}
	var requested []string
	query := fakeAzureDiskUsage(map[string][2]float64{azureTestDiskID(1): {1000, 50_000_000}}, &requested)
	priceLookup, pricesAsked := testAzurePriceLookup() // no region is priced

	recommendations := azureDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{}, resources, query, priceLookup, azureTestRuleOptions())

	assert.Equal(t, []string{"PremiumV2_LRS@eastus"}, *pricesAsked)
	assert.Empty(t, requested)
	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
	assert.Equal(t, azureTestDiskID(1), recommendations[0].ResourceId)
	assert.Equal(t, "disk_performance_overprovisioned", recommendations[0].RuleName)
	assert.Zero(t, recommendations[0].Savings)
}

// Azure Monitor failing for a disk must not look like the disk no longer being
// over-provisioned.
func TestAzureDiskPerformanceRecommendationsMetricFailureReportsDiskAsUnevaluated(t *testing.T) {
	resources := []providers.Resource{azureTestDisk(1, "PremiumV2_LRS", 8000, 300, true)}
	query := func(providers.CloudProviderContext, providers.Account, providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		return providers.QueryMetricsResponse{}, errors.New("failed to query metrics for all resources")
	}
	priceLookup, _ := testAzurePriceLookup("eastus")

	recommendations := azureDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{}, resources, query, priceLookup, azureTestRuleOptions())

	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
	assert.Equal(t, azureTestDiskID(1), recommendations[0].ResourceId)
}

// The recommended Premium SSD v2 settings must be ones Azure accepts: at least 3,000
// IOPS and 125 MB/s, and no more than 0.25 MB/s of throughput per IOPS.
func TestAzureDiskPerformanceRecommendationsTargetsAreValidPremiumV2Settings(t *testing.T) {
	resources := []providers.Resource{azureTestDisk(1, "PremiumV2_LRS", 20000, 1000, true)}
	var requested []string
	// IOPS are idle while throughput stays busy at 900 MB/s, so IOPS cannot drop to
	// the baseline: 1,000 MB/s needs at least 4,000 IOPS.
	query := fakeAzureDiskUsage(map[string][2]float64{azureTestDiskID(1): {500, 900_000_000}}, &requested)
	priceLookup, _ := testAzurePriceLookup("eastus")

	recommendations := azureDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{}, resources, query, priceLookup, azureTestRuleOptions())

	require.Len(t, recommendations, 1)
	data := recommendations[0].Data
	assert.Equal(t, int64(4000), data["recommended_iops"])
	assert.NotContains(t, data, "recommended_throughput_mbps")
	assert.Equal(t, int64(1000), data["current_throughput_mbps"])
	assert.InDelta(t, 16000*0.00511, recommendations[0].Savings, 1e-6)
}

// The read-only list path skips the rule even when it is switched on, so listing
// recommendations never reads usage history from the cloud.
func TestAzureDiskPerformanceRuleSkippedOnListPath(t *testing.T) {
	previous := config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled
	config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled = true
	defer func() { config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled = previous }()

	resources := []providers.Resource{azureTestDisk(1, "PremiumV2_LRS", 8000, 300, true)}
	recommendations := (&diskService{}).diskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{},
		providers.ListRecommendationsRequest{SkipDiskPerformance: true}, resources)
	assert.Nil(t, recommendations)
}

// A disk already at the free baseline has nothing to save, so its region's prices
// are not looked up and a failed lookup does not report it as unevaluated.
func TestAzureDiskPerformanceRecommendationsPricesOnlyDisksAboveBaseline(t *testing.T) {
	resources := []providers.Resource{
		azureTestDisk(1, "PremiumV2_LRS", 3000, 125, true),
		azureTestDisk(2, "PremiumV2_LRS", 3000, 125, true),
	}
	var requested []string
	query := fakeAzureDiskUsage(map[string][2]float64{}, &requested)
	priceLookup, pricesAsked := testAzurePriceLookup() // every lookup fails

	recommendations := azureDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{}, resources, query, priceLookup, azureTestRuleOptions())

	assert.Empty(t, *pricesAsked)
	assert.Empty(t, requested)
	assert.Empty(t, recommendations)
}

func TestPremiumV2AboveBaseline(t *testing.T) {
	disk := func(iops int64, mbps float64) providers.DiskConfig {
		throughput := providers.MBpsToMiBps(mbps)
		return providers.DiskConfig{
			ProvisionedIOPS: &iops, ProvisionedThroughputMiBps: &throughput,
			IOPSSource: providers.DiskPerformanceExplicit, ThroughputSource: providers.DiskPerformanceExplicit,
		}
	}
	assert.False(t, premiumV2AboveBaseline(disk(3000, 125)))
	assert.True(t, premiumV2AboveBaseline(disk(3001, 125)))
	assert.True(t, premiumV2AboveBaseline(disk(3000, 126)))
	assert.False(t, premiumV2AboveBaseline(providers.DiskConfig{}))
}

// Azure documents diskMBpsReadWrite as decimal megabytes ("MB here uses the ISO
// notation, of powers of 10"): 1,000,000 bytes per second is one MB/s.
func TestAzureThroughputUnitIsDecimalMegabytes(t *testing.T) {
	assert.InDelta(t, 1_000_000.0/(1024*1024), providers.MBpsToMiBps(1), 1e-12)
}
