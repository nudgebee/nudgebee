package azure

import (
	"fmt"
	"math"
	"strings"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
)

// Premium SSD v2 limits, per the Azure managed disk types reference: every disk
// includes 3,000 IOPS and 125 MB/s at no extra cost, and throughput cannot exceed
// 0.25 MB/s per provisioned IOPS.
//
// Ultra Disk has no entry: its minimum-setting rules are not confirmed, so it is not
// recommended on.
const (
	premiumV2BaselineIOPS           = 3000
	premiumV2BaselineThroughputMBps = 125
	premiumV2MaxThroughputMBpsPerIO = 0.25

	// Azure Retail Prices API product and meters for Premium SSD v2 performance.
	// Both meters are hourly and tiered: a free tier for the baseline and a paid
	// tier above it.
	premiumV2RetailProductName     = "Premium SSD v2"
	premiumV2RetailIOPSMeter       = "Premium LRS Provisioned IOPS"
	premiumV2RetailThroughputMeter = "Premium LRS Provisioned Throughput (MBps)"
)

// azureDiskPerformancePrices are the monthly list prices of one provisioned IOPS and
// one provisioned MB/s above a disk's free baseline, in one region.
type azureDiskPerformancePrices struct {
	IOPSPerMonth           float64
	ThroughputPerMBpsMonth float64
}

// azureDiskPerformancePriceLookup returns the performance prices of a disk SKU in a
// region. ok is false when they cannot be read, in which case a saving cannot be
// verified and nothing may be recommended.
type azureDiskPerformancePriceLookup func(ctx providers.CloudProviderContext, sku, region string) (prices azureDiskPerformancePrices, ok bool)

// premiumV2PricesFromRetailItems reads the Premium SSD v2 performance prices out of
// the Retail Prices API items for a region. Each meter comes as a free tier and a paid
// tier; the paid tier is the price above the baseline. ok is false unless both meters
// carry a paid price.
func premiumV2PricesFromRetailItems(items []RetailPriceItem) (azureDiskPerformancePrices, bool) {
	var iopsPerHour, throughputPerHour float64
	for _, item := range items {
		switch item.MeterName {
		case premiumV2RetailIOPSMeter:
			iopsPerHour = math.Max(iopsPerHour, retailPriceOf(item))
		case premiumV2RetailThroughputMeter:
			throughputPerHour = math.Max(throughputPerHour, retailPriceOf(item))
		}
	}
	if iopsPerHour <= 0 || throughputPerHour <= 0 {
		return azureDiskPerformancePrices{}, false
	}
	return azureDiskPerformancePrices{
		IOPSPerMonth:           iopsPerHour * hoursPerMonth,
		ThroughputPerMBpsMonth: throughputPerHour * hoursPerMonth,
	}, true
}

// retailDiskPerformancePrices looks the prices up in the Azure Retail Prices API
// through the shared pricing cache. There is deliberately no hardcoded fallback: a
// price that could not be read for the disk's own region is not a verified one.
func retailDiskPerformancePrices(ctx providers.CloudProviderContext, sku, region string) (azureDiskPerformancePrices, bool) {
	cache := GetPricingCache()
	if !strings.EqualFold(sku, "PremiumV2_LRS") {
		return azureDiskPerformancePrices{}, false
	}
	if !cache.IsEnabled() {
		ctx.GetLogger().Warn("azure disk performance rule: the pricing cache is disabled, so no saving can be verified and no disk is recommended on", "region", region)
		return azureDiskPerformancePrices{}, false
	}
	normalizedRegion := normalizeAzureRegion(region)
	iopsKey := fmt.Sprintf("diskperf:iops:%s:%s", sku, normalizedRegion)
	throughputKey := fmt.Sprintf("diskperf:throughput:%s:%s", sku, normalizedRegion)
	if iops, found := cache.getCachedPrice(iopsKey); found {
		if throughput, found := cache.getCachedPrice(throughputKey); found {
			return azureDiskPerformancePrices{IOPSPerMonth: iops, ThroughputPerMBpsMonth: throughput}, true
		}
	}

	filter := fmt.Sprintf("serviceName eq 'Storage' and armRegionName eq '%s' and contains(productName, '%s') and priceType eq 'Consumption'",
		normalizedRegion, premiumV2RetailProductName)
	items, err := cache.fetchPrices(ctx, filter)
	if err != nil {
		ctx.GetLogger().Warn("failed to fetch azure disk performance prices", "error", err, "sku", sku, "region", normalizedRegion)
		return azureDiskPerformancePrices{}, false
	}
	prices, ok := premiumV2PricesFromRetailItems(items)
	if !ok {
		ctx.GetLogger().Warn("azure disk performance prices not found", "sku", sku, "region", normalizedRegion, "items", len(items))
		return azureDiskPerformancePrices{}, false
	}
	entry := PriceEntry{ServiceName: "Storage", SKUName: sku, Region: normalizedRegion, CurrencyCode: "USD", UnitOfMeasure: "1/Month", FetchedAt: time.Now()}
	entry.UnitPrice = prices.IOPSPerMonth
	cache.setCachedPrice(iopsKey, entry)
	entry.UnitPrice = prices.ThroughputPerMBpsMonth
	cache.setCachedPrice(throughputKey, entry)
	return prices, true
}

// azureDiskPerformanceSupported reports whether a managed disk SKU's adjustable
// performance is one this rule knows the limits of.
func azureDiskPerformanceSupported(disk providers.DiskConfig) bool {
	return strings.EqualFold(disk.DiskType, "PremiumV2_LRS")
}

// azureDiskPerformanceProfile returns the limits and prices of a managed disk SKU's
// adjustable performance, converted from Azure's MB/s to the MiB/s DiskConfig uses.
// SKUs without an entry are not recommended on.
func azureDiskPerformanceProfile(disk providers.DiskConfig, prices azureDiskPerformancePrices) (providers.DiskPerformanceProfile, bool) {
	if !azureDiskPerformanceSupported(disk) || prices.IOPSPerMonth <= 0 || prices.ThroughputPerMBpsMonth <= 0 {
		return providers.DiskPerformanceProfile{}, false
	}
	priceThroughputInMBps := providers.DiskLinearCost(premiumV2BaselineThroughputMBps, prices.ThroughputPerMBpsMonth)
	return providers.DiskPerformanceProfile{
		MinIOPS:                   premiumV2BaselineIOPS,
		MinThroughputMiBps:        providers.MBpsToMiBps(premiumV2BaselineThroughputMBps),
		MaxThroughputMiBpsPerIOPS: providers.MBpsToMiBps(premiumV2MaxThroughputMBpsPerIO),
		MonthlyIOPSCost:           providers.DiskLinearCost(premiumV2BaselineIOPS, prices.IOPSPerMonth),
		MonthlyThroughputCost: func(mibps float64) float64 {
			return priceThroughputInMBps(providers.MiBpsToMBps(mibps))
		},
	}, true
}

// premiumV2AboveBaseline reports whether a disk is provisioned above the IOPS or
// throughput Premium SSD v2 includes for free.
func premiumV2AboveBaseline(disk providers.DiskConfig) bool {
	if disk.IOPSSource == providers.DiskPerformanceExplicit && disk.ProvisionedIOPS != nil && *disk.ProvisionedIOPS > premiumV2BaselineIOPS {
		return true
	}
	return disk.ThroughputSource == providers.DiskPerformanceExplicit && disk.ProvisionedThroughputMiBps != nil &&
		*disk.ProvisionedThroughputMiBps > providers.MBpsToMiBps(premiumV2BaselineThroughputMBps)+1e-9
}

// azureWholeMBps converts MiB/s to the whole MB/s Azure takes as a setting, rounding
// up so the result is never below the value it came from.
func azureWholeMBps(mibps float64) int64 {
	return int64(math.Ceil(providers.MiBpsToMBps(mibps) - 1e-6))
}

// azureDiskPerformanceRecommendations reports the attached managed disks among
// resources whose provisioned IOPS or throughput is far above their observed peak.
// It reads usage from Azure Monitor through query and prices through priceLookup, and
// never returns an error: a failure is logged, and each disk that could not be
// evaluated — its usage or its region's prices could not be read — is returned as an
// unevaluated placeholder so a recommendation it already has is left as it is.
func azureDiskPerformanceRecommendations(ctx providers.CloudProviderContext, account providers.Account, resources []providers.Resource, query providers.DiskMetricsQuery, priceLookup azureDiskPerformancePriceLookup, options providers.DiskPerformanceRuleOptions) []providers.Recommendation {
	type regionPrices struct {
		prices azureDiskPerformancePrices
		ok     bool
	}
	pricesByRegion := map[string]regionPrices{}
	resourceByID := map[string]providers.Resource{}
	var disks, unpriced []providers.DiskConfig
	for _, resource := range resources {
		disk, ok := azureDiskConfigFromResource(resource)
		if !ok || !disk.Attached || !azureDiskPerformanceSupported(disk) {
			continue
		}
		// Look prices up only for disks paying for performance above the free
		// baseline. A disk already at the baseline has nothing to save, so it
		// needs no price and must not be reported as unevaluated for lack of one.
		if !premiumV2AboveBaseline(disk) {
			continue
		}
		resourceByID[disk.DiskID] = resource
		region, looked := pricesByRegion[disk.Region]
		if !looked {
			region.prices, region.ok = priceLookup(ctx, disk.DiskType, disk.Region)
			pricesByRegion[disk.Region] = region
		}
		if !region.ok {
			unpriced = append(unpriced, disk)
			continue
		}
		disks = append(disks, disk)
	}

	profileFor := func(disk providers.DiskConfig) (providers.DiskPerformanceProfile, bool) {
		return azureDiskPerformanceProfile(disk, pricesByRegion[disk.Region].prices)
	}
	fetch := func(group []providers.DiskConfig, start, end time.Time, yield func(providers.DiskPerformanceSeries)) error {
		return fetchAzureDiskSeries(ctx, account, group, start, end, query, yield)
	}
	findings, unevaluated := providers.FindOverprovisionedDisks(ctx, disks, profileFor, fetch, options)

	var recommendations []providers.Recommendation
	for _, finding := range findings {
		resource := resourceByID[finding.Config.DiskID]
		data := providers.DiskPerformanceRecommendationData(finding, options.Thresholds)
		data["resource_id"] = resource.Id
		data["resource_name"] = resource.Name
		data["sku"] = finding.Config.DiskType
		// Azure sets throughput in whole MB/s, so give the figures in that unit too.
		if finding.Config.ProvisionedThroughputMiBps != nil {
			data["current_throughput_mbps"] = azureWholeMBps(*finding.Config.ProvisionedThroughputMiBps)
		}
		if finding.Target.ThroughputMiBps != nil {
			data["recommended_throughput_mbps"] = azureWholeMBps(*finding.Target.ThroughputMiBps)
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            constants.AzureDiskPerformanceOverprovisioned,
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             finding.Target.MonthlySavings,
			Data:                data,
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: resource.ServiceName,
			ResourceId:          resource.Id,
			ResourceType:        resource.Type,
			ResourceRegion:      resource.Region,
		})
	}
	for _, disk := range append(unevaluated, unpriced...) {
		recommendations = append(recommendations, providers.UnevaluatedRecommendation(
			constants.AzureDiskPerformanceOverprovisioned, providers.RecommendationCategoryRightSizing, resourceByID[disk.DiskID]))
	}
	return recommendations
}

// diskPerformanceRecommendations runs the disk performance rule unless it is switched
// off for Azure or the request skips it, with the settings from configuration.
func (s *diskService) diskPerformanceRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, resources []providers.Resource) []providers.Recommendation {
	if !config.Config.CloudCollectorDiskPerformanceRuleAzureEnabled || filter.SkipDiskPerformance {
		return nil
	}
	return azureDiskPerformanceRecommendations(ctx, account, resources, s.QueryMetrices, retailDiskPerformancePrices, providers.DiskPerformanceRuleOptionsFromConfig())
}
