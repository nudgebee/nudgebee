package gcloud

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestGcloudDiskPerformanceProfile(t *testing.T) {
	profile, ok := gcloudDiskPerformanceProfile(providers.DiskConfig{DiskType: "hyperdisk-balanced"})
	require.True(t, ok)
	assert.Equal(t, 3000.0, profile.MinIOPS)
	assert.Equal(t, 140.0, profile.MinThroughputMiBps)
	assert.Equal(t, 0.25, profile.MaxThroughputMiBpsPerIOPS)
	assert.Equal(t, 1.0/256, profile.MinThroughputMiBpsPerIOPS)
	assert.InDelta(t, 0.0, profile.MonthlyIOPSCost(3000), 1e-9)
	assert.InDelta(t, 85.0, profile.MonthlyIOPSCost(20000), 1e-9)
	assert.InDelta(t, 0.0, profile.MonthlyThroughputCost(140), 1e-9)
	assert.InDelta(t, 6.0, profile.MonthlyThroughputCost(290), 1e-9)

	for _, diskType := range []string{"hyperdisk-balanced-high-availability", "hyperdisk-extreme", "hyperdisk-throughput", "hyperdisk-ml", "pd-extreme", "pd-ssd", "pd-balanced", "pd-standard", ""} {
		_, ok := gcloudDiskPerformanceProfile(providers.DiskConfig{DiskType: diskType})
		assert.False(t, ok, "disk type %q should have no profile", diskType)
	}
}

func gcloudTestDisk(name, diskType string, iops, throughput int64, users ...string) providers.Resource {
	resource := gcloudDiskResource(&computepb.Disk{
		SizeGb:                proto.Int64(200),
		ProvisionedIops:       proto.Int64(iops),
		ProvisionedThroughput: proto.Int64(throughput),
		Users:                 users,
	}, diskType)
	resource.Id = testGcloudZoneURL + "/disks/" + name
	resource.Arn = resource.Id
	resource.Name = name
	return resource
}

// gcloudTestHistory is a stretch of constant usage of one device on one instance: IOPS
// and MiB/s, all of it reads, over the given range of five-minute intervals.
type gcloudTestHistory struct {
	instanceID string
	device     string
	iops       float64
	mibps      float64
	from, to   int // interval numbers, from inclusive, to exclusive
}

// fakeGcloudDiskUsage answers series queries from a set of histories, the way Cloud
// Monitoring would: one series per instance and device, each point stamped with the
// end of its interval.
func fakeGcloudDiskUsage(histories []gcloudTestHistory, calls *[]gcloudTestQueryCall) gcloudDiskSeriesQuery {
	return func(zone string, deviceNames []string, instanceID, metric string, start, end time.Time) ([]providers.MetricItem, error) {
		*calls = append(*calls, gcloudTestQueryCall{zone, deviceNames, instanceID, metric})
		var items []providers.MetricItem
		for _, history := range histories {
			if instanceID != "" && history.instanceID != instanceID {
				continue
			}
			wanted := false
			for _, name := range deviceNames {
				wanted = wanted || name == history.device
			}
			if !wanted {
				continue
			}
			value := 0.0
			switch metric {
			case "disk/read_ops_count":
				value = history.iops * providers.DiskMetricPeriod.Seconds()
			case "disk/read_bytes_count":
				value = history.mibps * 1024 * 1024 * providers.DiskMetricPeriod.Seconds()
			}
			item := gcloudTestMetric(metric, history.instanceID, history.device, nil, nil)
			for interval := history.from; interval < history.to; interval++ {
				item.Values = append(item.Values, value)
				item.Timestamps = append(item.Timestamps, start.Add(time.Duration(interval+1)*providers.DiskMetricPeriod))
			}
			items = append(items, item)
		}
		return items, nil
	}
}

const gcloudTestWindowIntervals = 8640

func gcloudTestRuleOptions() providers.DiskPerformanceRuleOptions {
	return providers.DiskPerformanceRuleOptions{
		Thresholds: providers.DiskPerformanceThresholds{HeadroomPercent: 30, MaxUtilizationPercent: 50, MinMonthlySavings: 1},
		MaxDisks:   200,
		Now:        time.Date(2026, 10, 9, 11, 7, 0, 0, time.UTC),
	}
}

// gcloudTestLookup answers instance lookups for vm-1, which has the given disks
// attached under the given device names, plus a boot disk.
func gcloudTestLookup(t *testing.T, lookups *int, devices map[string]string) gcloudInstanceLookup {
	return func(project, zone, name string) (*computepb.Instance, error) {
		*lookups++
		assert.Equal(t, "my-project", project)
		assert.Equal(t, "us-central1-a", zone)
		assert.Equal(t, "vm-1", name)
		attached := []*computepb.AttachedDisk{gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/boot", "persistent-disk-0")}
		for diskName, device := range devices {
			attached = append(attached, gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/"+diskName, device))
		}
		return gcloudTestInstance(111, attached...), nil
	}
}

func TestGcloudDiskPerformanceRecommendations(t *testing.T) {
	overID := testGcloudZoneURL + "/disks/over"
	resources := []providers.Resource{
		gcloudTestDisk("over", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1),
		gcloudTestDisk("at-baseline", "hyperdisk-balanced", 3000, 140, testGcloudInstanceV1),
		gcloudTestDisk("unattached", "hyperdisk-balanced", 20000, 290),
		gcloudTestDisk("extreme", "hyperdisk-extreme", 100000, 0, testGcloudInstanceV1),
		gcloudTestDisk("standard", "pd-ssd", 0, 0, testGcloudInstanceV1),
	}

	lookups := 0
	getInstance := gcloudTestLookup(t, &lookups, map[string]string{"over": "data-device"})
	var calls []gcloudTestQueryCall
	// The data disk peaks at 2,000 IOPS and 50 MiB/s; the boot disk on the same
	// instance is far busier and must not be mistaken for it.
	query := fakeGcloudDiskUsage([]gcloudTestHistory{
		{"111", "data-device", 2000, 50, 0, gcloudTestWindowIntervals},
		{"111", "persistent-disk-0", 19000, 280, 0, gcloudTestWindowIntervals},
	}, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	// Only the attached Hyperdisk Balanced disk above its free baseline needs its
	// instance looked up and its four metrics read, on that instance only.
	assert.Equal(t, 1, lookups)
	require.Len(t, calls, 4)
	for _, call := range calls {
		assert.Equal(t, []string{"data-device"}, call.devices)
		assert.Equal(t, "111", call.instanceID)
		assert.Equal(t, "us-central1-a", call.zone)
	}
	require.Len(t, recommendations, 1)

	recommendation := recommendations[0]
	assert.False(t, recommendation.Unevaluated)
	assert.Equal(t, "disk_performance_overprovisioned", recommendation.RuleName)
	assert.Equal(t, providers.RecommendationCategoryRightSizing, recommendation.CategoryName)
	assert.Equal(t, providers.RecommendationActionModify, recommendation.Action)
	assert.Equal(t, overID, recommendation.ResourceId)
	assert.Equal(t, ServiceNameDisk, recommendation.ResourceServiceName)
	assert.Equal(t, "storage", recommendation.ResourceType)
	assert.Equal(t, "us-central1", recommendation.ResourceRegion)
	// 17,000 IOPS at $0.005 plus 150 MiB/s at $0.04.
	assert.InDelta(t, 91.0, recommendation.Savings, 1e-6)

	data := recommendation.Data
	assert.Equal(t, overID, data["resource_id"])
	assert.Equal(t, "over", data["resource_name"])
	assert.Equal(t, "us-central1-a", data["zone"])
	assert.Equal(t, "hyperdisk-balanced", data["disk_type"])
	assert.Equal(t, int64(20000), data["current_iops"])
	assert.Equal(t, int64(3000), data["recommended_iops"])
	assert.Equal(t, 2000.0, data["peak_iops"])
	assert.Equal(t, 290.0, data["current_throughput_mibps"])
	assert.Equal(t, 140.0, data["recommended_throughput_mibps"])
	assert.Equal(t, 50.0, data["peak_throughput_mibps"])
	assert.Equal(t, 100.0, data["coverage_percent"])
	assert.Equal(t, int64(300), data["peak_resolution_seconds"])
}

// A Kubernetes volume whose node was replaced three weeks into the month. Read on its
// current instance alone it has a week of history and is not judged. Because it is
// attached under its own name, its history on the earlier instance is found too, and
// the full month is covered.
func TestGcloudDiskPerformanceRecommendationsHistorySurvivesInstanceReplacement(t *testing.T) {
	resources := []providers.Resource{gcloudTestDisk("pvc-1", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	lookups := 0
	getInstance := gcloudTestLookup(t, &lookups, map[string]string{"pvc-1": "pvc-1"})
	moved := gcloudTestWindowIntervals * 3 / 4
	var calls []gcloudTestQueryCall
	query := fakeGcloudDiskUsage([]gcloudTestHistory{
		{"999", "pvc-1", 2000, 50, 0, moved},
		{"111", "pvc-1", 1500, 40, moved, gcloudTestWindowIntervals},
	}, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	// Read by the disk's name across the zone, not by the current instance.
	require.Len(t, calls, 4)
	assert.Equal(t, "", calls[0].instanceID)
	assert.Equal(t, []string{"pvc-1"}, calls[0].devices)

	require.Len(t, recommendations, 1)
	data := recommendations[0].Data
	assert.Equal(t, 100.0, data["coverage_percent"])
	// The peak is the earlier instance's, which the current instance never saw.
	assert.Equal(t, 2000.0, data["peak_iops"])
	assert.Equal(t, int64(3000), data["recommended_iops"])
}

// The same volume attached under a per-instance device name cannot be followed, so
// only its week on the current instance counts. It is not judged, and is reported as
// unevaluated so a recommendation it already has is kept.
func TestGcloudDiskPerformanceRecommendationsInsufficientCoverageWithoutStableName(t *testing.T) {
	resources := []providers.Resource{gcloudTestDisk("pvc-1", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	lookups := 0
	getInstance := gcloudTestLookup(t, &lookups, map[string]string{"pvc-1": "persistent-disk-1"})
	moved := gcloudTestWindowIntervals * 3 / 4
	var calls []gcloudTestQueryCall
	query := fakeGcloudDiskUsage([]gcloudTestHistory{
		// Some other disk sat in the same slot of an earlier instance.
		{"999", "persistent-disk-1", 90000, 900, 0, moved},
		{"111", "persistent-disk-1", 100, 5, moved, gcloudTestWindowIntervals},
	}, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	require.Len(t, calls, 4)
	assert.Equal(t, "111", calls[0].instanceID)
	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
}

// Another instance in the zone has a device carrying this disk's name, attached at
// the same time. The two cannot be one disk, so nothing is concluded from either.
func TestGcloudDiskPerformanceRecommendationsUnrelatedDeviceWithSameNameBlocksRecommendation(t *testing.T) {
	resources := []providers.Resource{gcloudTestDisk("pvc-1", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	lookups := 0
	getInstance := gcloudTestLookup(t, &lookups, map[string]string{"pvc-1": "pvc-1"})
	var calls []gcloudTestQueryCall
	query := fakeGcloudDiskUsage([]gcloudTestHistory{
		{"111", "pvc-1", 100, 5, 0, gcloudTestWindowIntervals},
		{"777", "pvc-1", 50, 2, 1000, 2000},
	}, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	// Refused: no recommendation. Nothing could be concluded about the disk, so it
	// is reported as unevaluated and a recommendation it already has is kept.
	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
}

// Cloud Monitoring failing for a disk must not look like the disk no longer being
// over-provisioned.
func TestGcloudDiskPerformanceRecommendationsMetricFailureReportsDiskAsUnevaluated(t *testing.T) {
	overID := testGcloudZoneURL + "/disks/pvc-1"
	resources := []providers.Resource{gcloudTestDisk("pvc-1", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	lookups := 0
	getInstance := gcloudTestLookup(t, &lookups, map[string]string{"pvc-1": "pvc-1"})
	query := func(string, []string, string, string, time.Time, time.Time) ([]providers.MetricItem, error) {
		return nil, errors.New("rpc error: code = Unavailable")
	}

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
	assert.Equal(t, overID, recommendations[0].ResourceId)
	assert.Equal(t, "disk_performance_overprovisioned", recommendations[0].RuleName)
	assert.Equal(t, ServiceNameDisk, recommendations[0].ResourceServiceName)
}

// The instance lookup failing leaves the disk unevaluated in the same way.
func TestGcloudDiskPerformanceRecommendationsInstanceLookupFailureReportsDiskAsUnevaluated(t *testing.T) {
	resources := []providers.Resource{gcloudTestDisk("pvc-1", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	getInstance := func(_, _, _ string) (*computepb.Instance, error) {
		return nil, errors.New("googleapi: Error 503: backend unavailable")
	}
	var calls []gcloudTestQueryCall
	query := fakeGcloudDiskUsage(nil, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	assert.Empty(t, calls)
	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
}

// The recommended Hyperdisk Balanced settings must be ones Compute Engine accepts: at
// least 3,000 IOPS and 140 MiB/s, with throughput between IOPS/256 and IOPS/4.
func TestGcloudDiskPerformanceRecommendationsTargetsAreValidHyperdiskSettings(t *testing.T) {
	resources := []providers.Resource{
		gcloudTestDisk("busy-iops", "hyperdisk-balanced", 80000, 400, testGcloudInstanceV1),
		gcloudTestDisk("busy-throughput", "hyperdisk-balanced", 16000, 1000, testGcloudInstanceV1),
	}
	lookups := 0
	getInstance := gcloudTestLookup(t, &lookups, map[string]string{"busy-iops": "busy-iops", "busy-throughput": "busy-throughput"})
	var calls []gcloudTestQueryCall
	query := fakeGcloudDiskUsage([]gcloudTestHistory{
		{"111", "busy-iops", 60000, 100, 0, gcloudTestWindowIntervals},
		{"111", "busy-throughput", 500, 900, 0, gcloudTestWindowIntervals},
	}, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	require.Len(t, recommendations, 2)
	for _, recommendation := range recommendations {
		data := recommendation.Data
		iops := float64(data["current_iops"].(int64))
		if recommended, ok := data["recommended_iops"].(int64); ok {
			iops = float64(recommended)
		}
		throughput := data["current_throughput_mibps"].(float64)
		if recommended, ok := data["recommended_throughput_mibps"].(float64); ok {
			throughput = recommended
		}
		name := data["resource_name"]
		assert.GreaterOrEqual(t, iops, 3000.0, name)
		assert.GreaterOrEqual(t, throughput, 140.0, name)
		assert.LessOrEqual(t, throughput, iops/4, name)
		assert.GreaterOrEqual(t, throughput, iops/256, name)
		assert.Greater(t, recommendation.Savings, 0.0, name)
	}
}

func TestGcloudDiskPerformanceRuleCanBeSwitchedOff(t *testing.T) {
	previous := config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled
	config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled = false
	defer func() { config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled = previous }()

	// With the switch off nothing is built or fetched, so no GCP credentials are needed.
	resources := []providers.Resource{gcloudTestDisk("over", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	recommendations := (&diskService{}).diskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{}, providers.ListRecommendationsRequest{}, resources)
	assert.Nil(t, recommendations)
}

// The disk type upgrade rule (gcp_disk_type_upgrade) acts on pd-standard disks. This
// rule must never act on those, so one disk never gets conflicting advice.
func TestGcloudDiskPerformanceRuleDoesNotOverlapTypeUpgradeRule(t *testing.T) {
	resources := []providers.Resource{
		gcloudTestDisk("standard", "pd-standard", 0, 0, testGcloudInstanceV1),
		gcloudTestDisk("balanced", "pd-balanced", 0, 0, testGcloudInstanceV1),
	}
	getInstance := func(_, _, _ string) (*computepb.Instance, error) {
		t.Fatal("no instance lookup expected")
		return nil, nil
	}
	var calls []gcloudTestQueryCall
	query := fakeGcloudDiskUsage(nil, &calls)

	recommendations := gcloudDiskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		resources, getInstance, query, gcloudTestRuleOptions())

	assert.Empty(t, calls)
	assert.Empty(t, recommendations)
}

// The read-only list path skips the rule even when it is switched on, so listing
// recommendations never reads usage history from the cloud.
func TestGcpDiskPerformanceRuleSkippedOnListPath(t *testing.T) {
	previous := config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled
	config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled = true
	defer func() { config.Config.CloudCollectorDiskPerformanceRuleGcpEnabled = previous }()

	resources := []providers.Resource{gcloudTestDisk("over", "hyperdisk-balanced", 20000, 290, testGcloudInstanceV1)}
	recommendations := (&diskService{}).diskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{},
		providers.ListRecommendationsRequest{SkipDiskPerformance: true}, resources)
	assert.Nil(t, recommendations)
}
