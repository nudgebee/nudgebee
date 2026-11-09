package gcloud

import (
	"strings"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// Hyperdisk Balanced limits and us-central1 list prices. The first 3,000 IOPS and
// 140 MiB/s of a volume are free; throughput must stay between IOPS/256 and IOPS/4
// MiB/s (Compute Engine Hyperdisk Balanced reference). The rates are the hourly
// figures on the Compute Engine disk pricing page ($0.000006849 per IOPS,
// $0.000054795 per MiB/s) over a 730-hour month; savings built on them are
// estimates.
//
// The other types with adjustable performance — Hyperdisk Balanced High
// Availability, Extreme, Throughput and ML, and pd-extreme — have no entry: their
// prices could not be confirmed, so they are not recommended on.
const (
	hyperdiskBalancedBaselineIOPS            = 3000
	hyperdiskBalancedBaselineThroughputMiBps = 140
	hyperdiskBalancedMaxThroughputPerIOPS    = 1.0 / 4
	hyperdiskBalancedMinThroughputPerIOPS    = 1.0 / 256
	hyperdiskBalancedIOPSPricePerMonth       = 0.005
	hyperdiskBalancedThroughputPricePerMonth = 0.04
)

// gcloudDiskPerformanceProfile returns the limits and prices of a Compute Engine disk
// type's adjustable performance. Types without an entry are not recommended on.
func gcloudDiskPerformanceProfile(disk providers.DiskConfig) (providers.DiskPerformanceProfile, bool) {
	if strings.EqualFold(disk.DiskType, "hyperdisk-balanced") {
		return providers.DiskPerformanceProfile{
			MinIOPS:                   hyperdiskBalancedBaselineIOPS,
			MinThroughputMiBps:        hyperdiskBalancedBaselineThroughputMiBps,
			MaxThroughputMiBpsPerIOPS: hyperdiskBalancedMaxThroughputPerIOPS,
			MinThroughputMiBpsPerIOPS: hyperdiskBalancedMinThroughputPerIOPS,
			MonthlyIOPSCost:           providers.DiskLinearCost(hyperdiskBalancedBaselineIOPS, hyperdiskBalancedIOPSPricePerMonth),
			MonthlyThroughputCost:     providers.DiskLinearCost(hyperdiskBalancedBaselineThroughputMiBps, hyperdiskBalancedThroughputPricePerMonth),
		}, true
	}
	return providers.DiskPerformanceProfile{}, false
}

// gcloudDiskPerformanceRecommendations reports the attached disks among resources
// whose provisioned IOPS or throughput is far above their observed peak. It reads
// usage from Cloud Monitoring through query, looking attached instances up with
// getInstance, and never returns an error: a failure is logged, and each disk that
// could not be evaluated is returned as an unevaluated placeholder so a
// recommendation it already has is left as it is.
func gcloudDiskPerformanceRecommendations(ctx providers.CloudProviderContext, resources []providers.Resource, getInstance gcloudInstanceLookup, query gcloudDiskSeriesQuery, options providers.DiskPerformanceRuleOptions) []providers.Recommendation {
	resourceByID := map[string]providers.Resource{}
	var disks []providers.DiskConfig
	for _, resource := range resources {
		if disk, ok := gcloudDiskConfigFromResource(resource); ok {
			resourceByID[disk.DiskID] = resource
			disks = append(disks, disk)
		}
	}

	fetch := func(group []providers.DiskConfig, start, end time.Time, yield func(providers.DiskPerformanceSeries)) error {
		return fetchGcloudDiskSeries(ctx, group, start, end, getInstance, query, yield)
	}
	findings, unevaluated := providers.FindOverprovisionedDisks(ctx, disks, gcloudDiskPerformanceProfile, fetch, options)

	var recommendations []providers.Recommendation
	for _, finding := range findings {
		resource := resourceByID[finding.Config.DiskID]
		data := providers.DiskPerformanceRecommendationData(finding, options.Thresholds)
		data["resource_id"] = resource.Id
		data["resource_name"] = resource.Name
		data["service_name"] = resource.ServiceName
		data["zone"] = finding.Config.Zone

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            constants.GCPDiskPerformanceOverprovisioned,
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
			constants.GCPDiskPerformanceOverprovisioned, providers.RecommendationCategoryRightSizing, resourceByID[disk.DiskID]))
	}
	return recommendations
}

// diskPerformanceRecommendations runs the disk performance rule unless it is switched
// off for GCP or the request skips it, with the settings from configuration. The Compute Engine and Cloud
// Monitoring clients are only created if a disk actually needs its usage read.
func (s *diskService) diskPerformanceRecommendations(ctx providers.CloudProviderContext, account providers.Account, filter providers.ListRecommendationsRequest, resources []providers.Resource) []providers.Recommendation {
	if !config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled || filter.SkipDiskPerformance {
		return nil
	}

	var lookup gcloudInstanceLookup
	var query gcloudDiskSeriesQuery
	var closers []func()
	defer func() {
		for _, closeClient := range closers {
			closeClient()
		}
	}()
	getInstance := func(project, zone, name string) (*computepb.Instance, error) {
		if lookup == nil {
			created, closeClient, err := newGcloudInstanceLookup(ctx, account)
			if err != nil {
				return nil, err
			}
			lookup = created
			closers = append(closers, closeClient)
		}
		return lookup(project, zone, name)
	}
	readSeries := func(zone string, deviceNames []string, instanceID, metric string, start, end time.Time) ([]providers.MetricItem, error) {
		if query == nil {
			created, closeClient, err := newGcloudDiskSeriesQuery(ctx, account)
			if err != nil {
				return nil, err
			}
			query = created
			closers = append(closers, closeClient)
		}
		return query(zone, deviceNames, instanceID, metric, start, end)
	}
	return gcloudDiskPerformanceRecommendations(ctx, resources, getInstance, readSeries, providers.DiskPerformanceRuleOptionsFromConfig())
}
