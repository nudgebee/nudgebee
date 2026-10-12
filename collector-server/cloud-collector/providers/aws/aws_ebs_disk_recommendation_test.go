package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/config"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEbsDiskPerformanceProfile(t *testing.T) {
	gp3, ok := ebsDiskPerformanceProfile(providers.DiskConfig{DiskType: "gp3"})
	require.True(t, ok)
	assert.Equal(t, 3000.0, gp3.MinIOPS)
	assert.Equal(t, 125.0, gp3.MinThroughputMiBps)
	assert.Equal(t, 0.25, gp3.MaxThroughputMiBpsPerIOPS)
	assert.Zero(t, gp3.MinThroughputMiBpsPerIOPS)
	// The baseline is free; 7,000 IOPS above it cost $35 and 375 MiB/s cost $15.
	assert.InDelta(t, 0.0, gp3.MonthlyIOPSCost(3000), 1e-9)
	assert.InDelta(t, 35.0, gp3.MonthlyIOPSCost(10000), 1e-9)
	assert.InDelta(t, 0.0, gp3.MonthlyThroughputCost(125), 1e-9)
	assert.InDelta(t, 15.0, gp3.MonthlyThroughputCost(500), 1e-9)

	io1, ok := ebsDiskPerformanceProfile(providers.DiskConfig{DiskType: "io1"})
	require.True(t, ok)
	assert.Equal(t, 100.0, io1.MinIOPS)
	assert.InDelta(t, 650.0, io1.MonthlyIOPSCost(10000), 1e-9)
	assert.Nil(t, io1.MonthlyThroughputCost)

	io2, ok := ebsDiskPerformanceProfile(providers.DiskConfig{DiskType: "IO2"})
	require.True(t, ok)
	assert.Equal(t, 100.0, io2.MinIOPS)
	assert.InDelta(t, 650.0, io2.MonthlyIOPSCost(10000), 1e-9)
	// 32,000 at $0.065, 32,000 at $0.0455 and 6,000 at $0.03185.
	assert.InDelta(t, 2080.0+1456.0+191.1, io2.MonthlyIOPSCost(70000), 1e-9)
	assert.Nil(t, io2.MonthlyThroughputCost)

	for _, volumeType := range []string{"gp2", "st1", "sc1", "standard", "", "gp9"} {
		_, ok := ebsDiskPerformanceProfile(providers.DiskConfig{DiskType: volumeType})
		assert.False(t, ok, "volume type %q should have no profile", volumeType)
	}
}

// fakeEbsUsage answers CloudWatch queries with a full window at a constant level per
// volume: IOPS and MiB/s, all of it reads. The usage metrics are asked for as each
// interval's Maximum, which is the total of its busiest minute.
func fakeEbsUsage(t *testing.T, usage map[string][2]float64, requests *int) providers.DiskMetricsQuery {
	return func(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		*requests++
		var items []providers.MetricItem
		for _, query := range filter.MetricQueries {
			volumeID, metric, found := strings.Cut(query.Label, "|")
			require.True(t, found)
			level, ok := usage[volumeID]
			if !ok {
				continue
			}
			period := time.Duration(query.MetricStat.Period) * time.Second
			value := 0.0
			switch metric {
			case "VolumeReadOps":
				require.Equal(t, "Maximum", query.MetricStat.Stat)
				value = level[0] * 60
			case "VolumeReadBytes":
				require.Equal(t, "Maximum", query.MetricStat.Stat)
				value = level[1] * 1024 * 1024 * 60
			}
			item := providers.MetricItem{Name: query.Label, Statistics: query.MetricStat.Stat}
			for ts := *filter.StartDate; ts.Before(*filter.EndDate); ts = ts.Add(period) {
				item.Values = append(item.Values, value)
				item.Timestamps = append(item.Timestamps, ts)
			}
			items = append(items, item)
		}
		return providers.QueryMetricsResponse{Items: items}, nil
	}
}

func ebsTestVolume(id string, volumeType types.VolumeType, iops, throughput int32, attached bool) providers.Resource {
	volume := types.Volume{
		VolumeId:         aws.String(id),
		VolumeType:       volumeType,
		Size:             aws.Int32(500),
		AvailabilityZone: aws.String("us-east-1a"),
		State:            types.VolumeStateAvailable,
	}
	if iops > 0 {
		volume.Iops = aws.Int32(iops)
	}
	if throughput > 0 {
		volume.Throughput = aws.Int32(throughput)
	}
	if attached {
		volume.State = types.VolumeStateInUse
		volume.Attachments = []types.VolumeAttachment{{InstanceId: aws.String("i-0abc")}}
	}
	return ebsVolumeResource(volume)
}

func ebsTestRuleOptions() providers.DiskPerformanceRuleOptions {
	return providers.DiskPerformanceRuleOptions{
		Thresholds: providers.DiskPerformanceThresholds{HeadroomPercent: 30, MaxUtilizationPercent: 50, MinMonthlySavings: 1},
		MaxDisks:   200,
		Now:        time.Date(2026, 10, 9, 11, 7, 0, 0, time.UTC),
	}
}

func TestEbsPerformanceRecommendations(t *testing.T) {
	resources := []providers.Resource{
		ebsTestVolume("vol-over", types.VolumeTypeGp3, 10000, 500, true),
		ebsTestVolume("vol-busy", types.VolumeTypeGp3, 10000, 125, true),
		ebsTestVolume("vol-default", types.VolumeTypeGp3, 3000, 125, true),
		ebsTestVolume("vol-gp2", types.VolumeTypeGp2, 1500, 0, true),
		ebsTestVolume("vol-unattached", types.VolumeTypeGp3, 16000, 1000, false),
		ebsTestVolume("vol-io2", types.VolumeTypeIo2, 20000, 0, true),
		{Id: "i-0abc", Type: "compute-instance", ServiceName: ServiceNameEc2, Meta: map[string]any{"InstanceId": "i-0abc"}},
	}
	requests := 0
	query := fakeEbsUsage(t, map[string][2]float64{
		"vol-over": {2000, 50},
		"vol-busy": {9000, 100},
		"vol-io2":  {1000, 10},
	}, &requests)

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	// One request covers the three volumes that could save anything; the volume at
	// gp3's free baseline, the gp2 volume and the unattached one are never fetched.
	assert.Equal(t, 1, requests)
	require.Len(t, recommendations, 2)

	byVolume := map[string]providers.Recommendation{}
	for _, recommendation := range recommendations {
		byVolume[recommendation.ResourceId] = recommendation
	}

	gp3, ok := byVolume["vol-over"]
	require.True(t, ok)
	assert.Equal(t, "disk_performance_overprovisioned", gp3.RuleName)
	assert.Equal(t, providers.RecommendationCategoryRightSizing, gp3.CategoryName)
	assert.Equal(t, providers.RecommendationActionModify, gp3.Action)
	assert.Equal(t, providers.RecommendationSeverityMedium, gp3.Severity)
	assert.Equal(t, ServiceNameEc2, gp3.ResourceServiceName)
	assert.Equal(t, "storage", gp3.ResourceType)
	assert.Equal(t, "us-east-1", gp3.ResourceRegion)
	// 7,000 IOPS at $0.005 plus 375 MiB/s at $0.04.
	assert.InDelta(t, 50.0, gp3.Savings, 1e-6)
	assert.Equal(t, "vol-over", gp3.Data["volume_id"])
	assert.Equal(t, "arn:aws:ec2:us-east-1:123456789012:volume/vol-over", gp3.Data["volume_arn"])
	assert.Equal(t, "gp3", gp3.Data["volume_type"])
	assert.Equal(t, "us-east-1", gp3.Data["volume_region"])
	assert.Equal(t, 500.0, gp3.Data["volume_size"])
	assert.Equal(t, int64(10000), gp3.Data["current_iops"])
	assert.Equal(t, int64(3000), gp3.Data["recommended_iops"])
	assert.Equal(t, 2000.0, gp3.Data["peak_iops"])
	assert.Equal(t, 125.0, gp3.Data["recommended_throughput_mibps"])
	assert.Equal(t, 50.0, gp3.Data["peak_throughput_mibps"])
	assert.Equal(t, 30, gp3.Data["observation_days"])
	assert.Equal(t, 100.0, gp3.Data["coverage_percent"])

	// io2: 1,000 peak plus headroom is 1,300 IOPS, saving 18,700 IOPS at $0.065.
	io2, ok := byVolume["vol-io2"]
	require.True(t, ok)
	assert.Equal(t, int64(1300), io2.Data["recommended_iops"])
	assert.NotContains(t, io2.Data, "recommended_throughput_mibps")
	assert.InDelta(t, 18700*0.065, io2.Savings, 1e-6)
}

func TestEbsPerformanceRecommendationsNoUsageIsUnevaluated(t *testing.T) {
	resources := []providers.Resource{ebsTestVolume("vol-over", types.VolumeTypeGp3, 10000, 500, true)}
	requests := 0
	query := fakeEbsUsage(t, map[string][2]float64{}, &requests)

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	// CloudWatch returned nothing for the volume: unknown usage, not zero usage. No
	// recommendation is made, and one it already has is kept.
	assert.Equal(t, 1, requests)
	assertOnlyUnevaluated(t, recommendations, "vol-over")
}

func TestEbsPerformanceRecommendationsSuppressedWhenAwsFlagsLimitExceeded(t *testing.T) {
	resources := []providers.Resource{ebsTestVolume("vol-over", types.VolumeTypeGp3, 10000, 125, true)}
	requests := 0
	usage := fakeEbsUsage(t, map[string][2]float64{"vol-over": {2000, 50}}, &requests)
	query := func(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		resp, err := usage(ctx, account, filter)
		for i := range resp.Items {
			if resp.Items[i].Name == "vol-over|VolumeIOPSExceededCheck" {
				resp.Items[i].Values[3] = 1
			}
		}
		return resp, err
	}

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	assert.Empty(t, recommendations)
}

func TestEbsDiskPerformanceRuleCanBeSwitchedOff(t *testing.T) {
	previous := config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled
	config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled = false
	defer func() { config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled = previous }()

	// With the switch off nothing is built or fetched, so no AWS credentials are needed.
	resources := []providers.Resource{ebsTestVolume("vol-over", types.VolumeTypeGp3, 10000, 500, true)}
	recommendations := (&amazonEc2{}).diskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{}, providers.ListRecommendationsRequest{}, resources)
	assert.Nil(t, recommendations)
}

// ebsUsageWith wraps a fake CloudWatch so a test can reshape what it returns.
func ebsUsageWith(usage providers.DiskMetricsQuery, reshape func(item *providers.MetricItem)) providers.DiskMetricsQuery {
	return func(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		resp, err := usage(ctx, account, filter)
		for i := range resp.Items {
			reshape(&resp.Items[i])
		}
		return resp, err
	}
}

// A volume that is quiet all month except for a single minute at 8,000 IOPS is sized
// for that minute, so it is not over-provisioned. Averaged over its five-minute
// interval the same minute would read as 2,000 IOPS, 20% of what is provisioned, and
// the volume would wrongly be recommended down to 3,000.
func TestEbsPerformanceRecommendationsShortLivedPeakPreventsRecommendation(t *testing.T) {
	resources := []providers.Resource{ebsTestVolume("vol-spiky", types.VolumeTypeGp3, 10000, 125, true)}
	requests := 0
	query := ebsUsageWith(fakeEbsUsage(t, map[string][2]float64{"vol-spiky": {500, 10}}, &requests), func(item *providers.MetricItem) {
		if item.Name == "vol-spiky|VolumeReadOps" {
			item.Values[4000] = 8000 * 60 // the busiest minute of one interval ran at 8,000 IOPS
		}
	})

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	assert.Empty(t, recommendations)
}

// Without the spike the same volume is recommended down, which shows the spike alone
// is what held the recommendation back above.
func TestEbsPerformanceRecommendationsQuietVolumeIsRecommendedDown(t *testing.T) {
	resources := []providers.Resource{ebsTestVolume("vol-quiet", types.VolumeTypeGp3, 10000, 125, true)}
	requests := 0
	query := fakeEbsUsage(t, map[string][2]float64{"vol-quiet": {500, 10}}, &requests)

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	require.Len(t, recommendations, 1)
	assert.Equal(t, int64(3000), recommendations[0].Data["recommended_iops"])
}

// A volume with only its last ten days of history, such as one attached recently, has
// too little data to judge, however idle those ten days were. It is reported as
// unevaluated, so a recommendation it already has is not archived meanwhile.
func TestEbsPerformanceRecommendationsInsufficientHistory(t *testing.T) {
	resources := []providers.Resource{ebsTestVolume("vol-new", types.VolumeTypeGp3, 10000, 500, true)}
	requests := 0
	options := ebsTestRuleOptions()
	_, end := providers.DiskObservationRange(options.Now)
	cutoff := end.Add(-10 * 24 * time.Hour)
	query := ebsUsageWith(fakeEbsUsage(t, map[string][2]float64{"vol-new": {100, 5}}, &requests), func(item *providers.MetricItem) {
		var values []float64
		var timestamps []time.Time
		for i, ts := range item.Timestamps {
			if !ts.Before(cutoff) {
				values = append(values, item.Values[i])
				timestamps = append(timestamps, ts)
			}
		}
		item.Values, item.Timestamps = values, timestamps
	})

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, options)

	assert.Equal(t, 1, requests)
	assertOnlyUnevaluated(t, recommendations, "vol-new")
}

// The gp2 to gp3 upgrade rule (aws_ec2_ebs_generation_upgrade) acts on gp2 volumes.
// This rule must never act on the same volume, so the two cannot give conflicting
// advice about one disk's type and performance.
func TestEbsPerformanceRuleDoesNotOverlapGenerationUpgradeRule(t *testing.T) {
	_, hasProfile := ebsDiskPerformanceProfile(providers.DiskConfig{DiskType: "gp2"})
	assert.False(t, hasProfile)

	// Even a gp2 volume reporting very high baseline IOPS and no usage is left alone.
	resources := []providers.Resource{ebsTestVolume("vol-gp2", types.VolumeTypeGp2, 16000, 0, true)}
	requests := 0
	query := fakeEbsUsage(t, map[string][2]float64{"vol-gp2": {0, 0}}, &requests)

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	assert.Zero(t, requests)
	assert.Empty(t, recommendations)
}

// What the upgrade rule tells a user to provision on gp3 to match a large gp2 volume
// is a setting this rule accepts as valid: the two rules agree on gp3's limits.
func TestEbsPerformanceRuleAgreesWithGenerationUpgradeTargets(t *testing.T) {
	profile, ok := ebsDiskPerformanceProfile(providers.DiskConfig{DiskType: "gp3"})
	require.True(t, ok)

	assert.Equal(t, float64(gp3DefaultIOPS), profile.MinIOPS)
	assert.Equal(t, float64(gp3DefaultThroughput), profile.MinThroughputMiBps)
	for _, sizeGiB := range []float64{100, 500, 2000, 6000} {
		match := gp2ToGp3Match(sizeGiB)
		iops, throughput := float64(gp3DefaultIOPS), float64(gp3DefaultThroughput)
		if match.RecommendIOPS > 0 {
			iops = float64(match.RecommendIOPS)
		}
		if match.RecommendThroughput > 0 {
			throughput = float64(match.RecommendThroughput)
		}
		assert.GreaterOrEqual(t, iops, profile.MinIOPS)
		assert.GreaterOrEqual(t, throughput, profile.MinThroughputMiBps)
		assert.LessOrEqual(t, throughput, iops*profile.MaxThroughputMiBpsPerIOPS, "size %v GiB", sizeGiB)
	}
}

// CloudWatch failing for a volume must not look like the volume no longer being
// over-provisioned. The volume comes back as an unevaluated placeholder, which the
// store step uses to leave the recommendation it already has open.
func TestEbsPerformanceRecommendationsMetricFailureReportsVolumeAsUnevaluated(t *testing.T) {
	resources := []providers.Resource{
		ebsTestVolume("vol-over", types.VolumeTypeGp3, 10000, 500, true),
		ebsTestVolume("vol-default", types.VolumeTypeGp3, 3000, 125, true),
	}
	query := func(providers.CloudProviderContext, providers.Account, providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		return providers.QueryMetricsResponse{}, errors.New("ThrottlingException: Rate exceeded")
	}

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	// Only the volume that needed its usage read is affected; the one at gp3's free
	// baseline was never a candidate.
	require.Len(t, recommendations, 1)
	placeholder := recommendations[0]
	assert.True(t, placeholder.Unevaluated)
	assert.Equal(t, "disk_performance_overprovisioned", placeholder.RuleName)
	assert.Equal(t, "vol-over", placeholder.ResourceId)
	assert.Equal(t, ServiceNameEc2, placeholder.ResourceServiceName)
	assert.Equal(t, "storage", placeholder.ResourceType)
	assert.Equal(t, "us-east-1", placeholder.ResourceRegion)
	assert.Zero(t, placeholder.Savings)
}

// A volume whose instance type does not publish per-minute maximums returns nothing
// for them. It gets no recommendation and is not judged from coarser data; it is
// reported as unevaluated, so a recommendation it already has is kept.
func TestEbsPerformanceRecommendationsNoMaximumsIsUnevaluated(t *testing.T) {
	resources := []providers.Resource{ebsTestVolume("vol-xen", types.VolumeTypeGp3, 10000, 500, true)}
	query := func(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		for _, q := range filter.MetricQueries {
			assert.NotEqual(t, "Sum", q.MetricStat.Stat, "five-minute totals must not be used as a fallback")
		}
		return providers.QueryMetricsResponse{}, nil
	}

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	assertOnlyUnevaluated(t, recommendations, "vol-xen")
}

// The recommended gp3 settings must be ones EC2 ModifyVolume accepts: at least 3,000
// IOPS and 125 MiB/s, and no more than 0.25 MiB/s of throughput per IOPS.
func TestEbsPerformanceRecommendationsTargetsAreValidGp3Settings(t *testing.T) {
	resources := []providers.Resource{
		ebsTestVolume("vol-a", types.VolumeTypeGp3, 16000, 1000, true), // throughput stays busy
		ebsTestVolume("vol-b", types.VolumeTypeGp3, 16000, 1000, true), // both idle
		ebsTestVolume("vol-c", types.VolumeTypeGp3, 6000, 750, true),   // IOPS stays busy
	}
	requests := 0
	query := fakeEbsUsage(t, map[string][2]float64{
		"vol-a": {500, 900},
		"vol-b": {100, 10},
		"vol-c": {5500, 20},
	}, &requests)

	recommendations := ebsPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()),
		providers.Account{AccountNumber: "123456789012"}, resources, query, ebsTestRuleOptions())

	require.Len(t, recommendations, 3)
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
		assert.GreaterOrEqual(t, iops, 3000.0, recommendation.ResourceId)
		assert.GreaterOrEqual(t, throughput, 125.0, recommendation.ResourceId)
		assert.LessOrEqual(t, throughput, iops*0.25, recommendation.ResourceId)
		assert.Greater(t, recommendation.Savings, 0.0, recommendation.ResourceId)
	}
}

// The read-only list path skips the rule even when it is switched on, so listing
// recommendations never reads usage history from the cloud.
func TestAwsDiskPerformanceRuleSkippedOnListPath(t *testing.T) {
	previous := config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled
	config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled = true
	defer func() { config.Config.CloudCollectorDiskPerformanceRuleAwsEnabled = previous }()

	resources := []providers.Resource{ebsTestVolume("vol-over", types.VolumeTypeGp3, 10000, 500, true)}
	recommendations := (&amazonEc2{}).diskPerformanceRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{},
		providers.ListRecommendationsRequest{SkipDiskPerformance: true}, resources)
	assert.Nil(t, recommendations)
}

// assertOnlyUnevaluated checks that the rule made no recommendation and reported
// exactly the given resource as unevaluated.
func assertOnlyUnevaluated(t *testing.T, recommendations []providers.Recommendation, resourceID string) {
	t.Helper()
	require.Len(t, recommendations, 1)
	assert.True(t, recommendations[0].Unevaluated)
	assert.Equal(t, resourceID, recommendations[0].ResourceId)
	assert.Equal(t, "disk_performance_overprovisioned", recommendations[0].RuleName)
	assert.Zero(t, recommendations[0].Savings)
}
