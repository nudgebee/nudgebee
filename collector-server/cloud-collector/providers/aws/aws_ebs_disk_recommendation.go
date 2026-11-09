package aws

import (
	"strings"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
)

// EBS performance limits, per the Amazon EBS volume types reference, and us-east-1
// list prices, as returned by the AWS Price List API (AmazonEC2, regionCode
// us-east-1). Like ebsStoragePricePerGBMonth, the prices are one region's list
// prices, so savings built on them are estimates, and are off by the regional price
// difference for volumes elsewhere.
const (
	// gp3 includes 3,000 IOPS and 125 MiB/s in the storage price (gp3DefaultIOPS,
	// gp3DefaultThroughput); only what is provisioned above them is billed.
	gp3IOPSPricePerMonth = 0.005
	// Listed as $40.96 per GiBps-month, which is $0.04 per MiB/s-month.
	gp3ThroughputPricePerMiBpsMonth = 0.04
	// gp3 throughput cannot exceed 0.25 MiB/s per provisioned IOPS.
	gp3MaxThroughputMiBpsPerIOPS = 0.25

	// io1 and io2 bill every provisioned IOPS and accept as few as 100.
	provisionedIOPSVolumeMinIOPS = 100
	io1IOPSPricePerMonth         = 0.065
)

// io2 IOPS are billed in tiers: the first 32,000 at one rate, the next 32,000 at a
// lower one, and anything above 64,000 lower again.
var io2IOPSPriceTiers = []providers.DiskPriceTier{
	{UpTo: 32000, Price: 0.065},
	{UpTo: 64000, Price: 0.0455},
	{Price: 0.03185},
}

// ebsDiskPerformanceProfile returns the limits and prices of an EBS volume type's
// adjustable performance. Types without an entry are not recommended on.
func ebsDiskPerformanceProfile(disk providers.DiskConfig) (providers.DiskPerformanceProfile, bool) {
	switch strings.ToLower(disk.DiskType) {
	case "gp3":
		return providers.DiskPerformanceProfile{
			MinIOPS:                   gp3DefaultIOPS,
			MinThroughputMiBps:        gp3DefaultThroughput,
			MaxThroughputMiBpsPerIOPS: gp3MaxThroughputMiBpsPerIOPS,
			MonthlyIOPSCost:           providers.DiskLinearCost(gp3DefaultIOPS, gp3IOPSPricePerMonth),
			MonthlyThroughputCost:     providers.DiskLinearCost(gp3DefaultThroughput, gp3ThroughputPricePerMiBpsMonth),
		}, true
	case "io1":
		return providers.DiskPerformanceProfile{
			MinIOPS:         provisionedIOPSVolumeMinIOPS,
			MonthlyIOPSCost: providers.DiskLinearCost(0, io1IOPSPricePerMonth),
		}, true
	case "io2":
		return providers.DiskPerformanceProfile{
			MinIOPS:         provisionedIOPSVolumeMinIOPS,
			MonthlyIOPSCost: providers.DiskTieredCost(io2IOPSPriceTiers),
		}, true
	}
	return providers.DiskPerformanceProfile{}, false
}

// ebsPerformanceRecommendations reports the attached EBS volumes among resources
// whose provisioned IOPS or throughput is far above their observed peak. It reads
// usage from CloudWatch through query and never returns an error: a failure is
// logged, and each volume that could not be evaluated is returned as an unevaluated
// placeholder so a recommendation it already has is left as it is.
func ebsPerformanceRecommendations(ctx providers.CloudProviderContext, account providers.Account, resources []providers.Resource, query providers.DiskMetricsQuery, options providers.DiskPerformanceRuleOptions) []providers.Recommendation {
	resourceByID := map[string]providers.Resource{}
	var disks []providers.DiskConfig
	for _, resource := range resources {
		if disk, ok := ebsDiskConfigFromResource(resource); ok {
			resourceByID[disk.DiskID] = resource
			disks = append(disks, disk)
		}
	}

	fetch := func(group []providers.DiskConfig, start, end time.Time, yield func(providers.DiskPerformanceSeries)) error {
		return fetchEbsDiskSeries(ctx, account, group, start, end, query, yield)
	}

	findings, unevaluated := providers.FindOverprovisionedDisks(ctx, disks, ebsDiskPerformanceProfile, fetch, options)

	var recommendations []providers.Recommendation
	for _, finding := range findings {
		resource := resourceByID[finding.Config.DiskID]
		data := providers.DiskPerformanceRecommendationData(finding, options.Thresholds)
		// The identifiers the other EBS recommendations carry.
		data["volume_id"] = resource.Id
		data["volume_arn"] = resource.Arn
		data["volume_type"] = finding.Config.DiskType
		data["volume_region"] = resource.Region
		if finding.Config.CapacityGiB != nil {
			data["volume_size"] = *finding.Config.CapacityGiB
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            constants.AwsEC2EBSPerformanceOverprovisioned,
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
	for _, disk := range unevaluated {
		recommendations = append(recommendations, providers.UnevaluatedRecommendation(
			constants.AwsEC2EBSPerformanceOverprovisioned, providers.RecommendationCategoryRightSizing, resourceByID[disk.DiskID]))
	}
	return recommendations
}

// diskPerformanceRecommendations runs the EBS performance rule unless it is switched
// off for AWS or the request skips it, with the settings from configuration.
func (a *amazonEc2) diskPerformanceRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, resources []providers.Resource) []providers.Recommendation {
	if !config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled || filter.SkipDiskPerformance {
		return nil
	}
	return ebsPerformanceRecommendations(ctx, account, resources, a.QueryMetrices, providers.DiskPerformanceRuleOptionsFromConfig())
}
