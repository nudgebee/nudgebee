package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"nudgebee/collector/cloud/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func int64Ptr(v int64) *int64 { return &v }

var testThresholds = DiskPerformanceThresholds{HeadroomPercent: 30, MaxUtilizationPercent: 50, MinMonthlySavings: 1}

// testGp3LikeProfile mirrors a type with a free baseline of 3,000 IOPS and 125 MiB/s,
// throughput capped at a quarter of IOPS, at $0.005 per IOPS and $0.04 per MiB/s.
func testGp3LikeProfile() DiskPerformanceProfile {
	return DiskPerformanceProfile{
		MinIOPS:                   3000,
		MinThroughputMiBps:        125,
		MaxThroughputMiBpsPerIOPS: 0.25,
		MonthlyIOPSCost:           DiskLinearCost(3000, 0.005),
		MonthlyThroughputCost:     DiskLinearCost(125, 0.04),
	}
}

func testAdjustableDisk(iops int64, throughput float64) DiskConfig {
	return DiskConfig{
		DiskID:                     "disk-1",
		DiskName:                   "disk-1",
		DiskType:                   "gp3",
		Region:                     "us-east-1",
		CapacityGiB:                floatPtr(500),
		ProvisionedIOPS:            int64Ptr(iops),
		ProvisionedThroughputMiBps: floatPtr(throughput),
		IOPSSource:                 DiskPerformanceExplicit,
		ThroughputSource:           DiskPerformanceExplicit,
		Attached:                   true,
	}
}

func testCompleteAnalysis(peakIOPS, peakThroughput float64) DiskPerformanceAnalysis {
	end := time.Date(2026, 10, 9, 11, 5, 0, 0, time.UTC)
	return DiskPerformanceAnalysis{
		DiskID:                "disk-1",
		PeakIOPS:              floatPtr(peakIOPS),
		PeakThroughputMiBps:   floatPtr(peakThroughput),
		ObservationStart:      end.Add(-DiskObservationWindow),
		ObservationEnd:        end,
		PeriodSeconds:         300,
		PeakResolutionSeconds: 60,
		CoveragePercent:       100,
		DataQuality:           DiskDataQualityComplete,
	}
}

func TestDiskLinearCost(t *testing.T) {
	cost := DiskLinearCost(3000, 0.005)
	assert.InDelta(t, 0.0, cost(0), 1e-9)
	assert.InDelta(t, 0.0, cost(3000), 1e-9)
	assert.InDelta(t, 35.0, cost(10000), 1e-9)

	everyUnit := DiskLinearCost(0, 0.065)
	assert.InDelta(t, 650.0, everyUnit(10000), 1e-9)
}

func TestDiskTieredCost(t *testing.T) {
	cost := DiskTieredCost([]DiskPriceTier{{UpTo: 32000, Price: 0.065}, {UpTo: 64000, Price: 0.0455}, {Price: 0.032}})

	assert.InDelta(t, 0.0, cost(0), 1e-9)
	assert.InDelta(t, 650.0, cost(10000), 1e-9)
	assert.InDelta(t, 2080.0, cost(32000), 1e-9)
	// 32,000 at the first rate and 8,000 at the second.
	assert.InDelta(t, 2080.0+364.0, cost(40000), 1e-9)
	// Both lower tiers in full and 6,000 at the third.
	assert.InDelta(t, 2080.0+1456.0+192.0, cost(70000), 1e-9)
}

func TestRecommendDiskPerformanceClearlyOverprovisioned(t *testing.T) {
	target, ok := RecommendDiskPerformance(testAdjustableDisk(10000, 500), testCompleteAnalysis(2000, 50), testGp3LikeProfile(), testThresholds)
	require.True(t, ok)

	// Peak plus headroom is 2,600 IOPS and 65 MiB/s, both under the free baseline,
	// so the targets are the baseline.
	require.NotNil(t, target.IOPS)
	assert.Equal(t, int64(3000), *target.IOPS)
	require.NotNil(t, target.ThroughputMiBps)
	assert.InDelta(t, 125.0, *target.ThroughputMiBps, 1e-9)
	// 7,000 IOPS at $0.005 plus 375 MiB/s at $0.04.
	assert.InDelta(t, 50.0, target.MonthlySavings, 1e-9)
}

func TestRecommendDiskPerformanceTargetIsPeakPlusHeadroomNotBarePeak(t *testing.T) {
	target, ok := RecommendDiskPerformance(testAdjustableDisk(16000, 125), testCompleteAnalysis(4010, 20), testGp3LikeProfile(), testThresholds)
	require.True(t, ok)

	// 4,010 × 1.3 = 5,213, rounded up to the next hundred.
	require.NotNil(t, target.IOPS)
	assert.Equal(t, int64(5300), *target.IOPS)
	// Throughput is already at the baseline, so it is left alone.
	assert.Nil(t, target.ThroughputMiBps)
	assert.InDelta(t, (16000-5300)*0.005, target.MonthlySavings, 1e-9)
}

func TestRecommendDiskPerformanceSuppressed(t *testing.T) {
	gp3 := testGp3LikeProfile()
	tests := []struct {
		name     string
		config   DiskConfig
		analysis DiskPerformanceAnalysis
		profile  DiskPerformanceProfile
	}{
		{"a significant spike keeps utilization above the threshold", testAdjustableDisk(10000, 125), testCompleteAnalysis(6500, 20), gp3},
		{"utilization exactly at the threshold", testAdjustableDisk(10000, 125), testCompleteAnalysis(5000, 20), gp3},
		{"partial data", testAdjustableDisk(10000, 500), func() DiskPerformanceAnalysis {
			a := testCompleteAnalysis(2000, 50)
			a.DataQuality = DiskDataQualityPartial
			a.CoveragePercent = 60
			return a
		}(), gp3},
		{"no data", testAdjustableDisk(10000, 500), DiskPerformanceAnalysis{DataQuality: DiskDataQualityNoData}, gp3},
		{"already at the lowest setting", testAdjustableDisk(3000, 125), testCompleteAnalysis(100, 5), gp3},
		{"no pricing for the disk type", testAdjustableDisk(10000, 500), testCompleteAnalysis(2000, 50), DiskPerformanceProfile{MinIOPS: 3000, MinThroughputMiBps: 125}},
		{"saving below the minimum worth reporting", testAdjustableDisk(3100, 125), testCompleteAnalysis(100, 5), gp3},
		{"settings are derived, not explicit", func() DiskConfig {
			c := testAdjustableDisk(10000, 500)
			c.IOPSSource = DiskPerformanceDerived
			c.ThroughputSource = DiskPerformanceDerived
			return c
		}(), testCompleteAnalysis(2000, 50), gp3},
		{"provisioned settings unknown", func() DiskConfig {
			c := testAdjustableDisk(10000, 500)
			c.ProvisionedIOPS = nil
			c.ProvisionedThroughputMiBps = nil
			return c
		}(), testCompleteAnalysis(2000, 50), gp3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, ok := RecommendDiskPerformance(tt.config, tt.analysis, tt.profile, testThresholds)
			assert.False(t, ok)
			assert.Equal(t, DiskPerformanceTarget{}, target)
		})
	}
}

func TestRecommendDiskPerformanceOnlyPricedSettingIsLowered(t *testing.T) {
	profile := testGp3LikeProfile()
	profile.MonthlyThroughputCost = nil

	target, ok := RecommendDiskPerformance(testAdjustableDisk(10000, 500), testCompleteAnalysis(2000, 50), profile, testThresholds)
	require.True(t, ok)
	require.NotNil(t, target.IOPS)
	assert.Equal(t, int64(3000), *target.IOPS)
	assert.Nil(t, target.ThroughputMiBps)
	assert.InDelta(t, 35.0, target.MonthlySavings, 1e-9)
}

func TestRecommendDiskPerformanceExceededFlagProtectsThatSetting(t *testing.T) {
	analysis := testCompleteAnalysis(2000, 50)
	exceeded := true
	analysis.IOPSLimitExceeded = &exceeded

	target, ok := RecommendDiskPerformance(testAdjustableDisk(10000, 500), analysis, testGp3LikeProfile(), testThresholds)
	require.True(t, ok)
	assert.Nil(t, target.IOPS)
	require.NotNil(t, target.ThroughputMiBps)
	assert.InDelta(t, 125.0, *target.ThroughputMiBps, 1e-9)
	assert.InDelta(t, 15.0, target.MonthlySavings, 1e-9)

	analysis.ThroughputLimitExceeded = &exceeded
	_, ok = RecommendDiskPerformance(testAdjustableDisk(10000, 500), analysis, testGp3LikeProfile(), testThresholds)
	assert.False(t, ok)
}

// Throughput stays high, and the type allows at most a quarter MiB/s per IOPS, so
// IOPS cannot drop all the way to what the workload uses.
func TestRecommendDiskPerformanceKeepsThroughputWithinIOPSLimit(t *testing.T) {
	target, ok := RecommendDiskPerformance(testAdjustableDisk(16000, 1000), testCompleteAnalysis(1000, 900), testGp3LikeProfile(), testThresholds)
	require.True(t, ok)

	// Throughput peaked at 90% and is left at 1,000 MiB/s, which needs 4,000 IOPS.
	assert.Nil(t, target.ThroughputMiBps)
	require.NotNil(t, target.IOPS)
	assert.Equal(t, int64(4000), *target.IOPS)
	assert.InDelta(t, 12000*0.005, target.MonthlySavings, 1e-9)
}

func TestRecommendDiskPerformanceNoIOPSChangeWhenThroughputNeedsAllOfIt(t *testing.T) {
	_, ok := RecommendDiskPerformance(testAdjustableDisk(4000, 1000), testCompleteAnalysis(500, 900), testGp3LikeProfile(), testThresholds)
	assert.False(t, ok)
}

// IOPS stays high, and the type requires at least IOPS/256 MiB/s, so throughput
// cannot drop to its baseline.
func TestRecommendDiskPerformanceKeepsThroughputAboveIOPSFloor(t *testing.T) {
	profile := DiskPerformanceProfile{
		MinIOPS:                   3000,
		MinThroughputMiBps:        140,
		MaxThroughputMiBpsPerIOPS: 1.0 / 4,
		MinThroughputMiBpsPerIOPS: 1.0 / 256,
		MonthlyIOPSCost:           DiskLinearCost(3000, 0.005),
		MonthlyThroughputCost:     DiskLinearCost(140, 0.04),
	}

	target, ok := RecommendDiskPerformance(testAdjustableDisk(80000, 400), testCompleteAnalysis(60000, 100), profile, testThresholds)
	require.True(t, ok)

	// IOPS peaked at 75% and stays at 80,000, which needs at least 312.5 MiB/s.
	assert.Nil(t, target.IOPS)
	require.NotNil(t, target.ThroughputMiBps)
	assert.InDelta(t, 313.0, *target.ThroughputMiBps, 1e-9)
	assert.InDelta(t, (400-313)*0.04, target.MonthlySavings, 1e-9)
}

func TestRecommendDiskPerformanceIOPSOnlyDisk(t *testing.T) {
	config := testAdjustableDisk(70000, 0)
	config.ProvisionedThroughputMiBps = nil
	config.ThroughputSource = DiskPerformanceDerived
	profile := DiskPerformanceProfile{
		MinIOPS:         100,
		MonthlyIOPSCost: DiskTieredCost([]DiskPriceTier{{UpTo: 32000, Price: 0.065}, {UpTo: 64000, Price: 0.0455}, {Price: 0.032}}),
	}
	analysis := testCompleteAnalysis(7000, 0)
	analysis.PeakThroughputMiBps = floatPtr(300)

	target, ok := RecommendDiskPerformance(config, analysis, profile, testThresholds)
	require.True(t, ok)
	require.NotNil(t, target.IOPS)
	assert.Equal(t, int64(9100), *target.IOPS)
	assert.Nil(t, target.ThroughputMiBps)
	// 70,000 IOPS cost 3,728 across the three tiers; 9,100 cost 591.50.
	assert.InDelta(t, 3728.0-591.5, target.MonthlySavings, 1e-9)
}

func TestRecommendDiskPerformanceRespectsTypeMinimum(t *testing.T) {
	profile := DiskPerformanceProfile{MinIOPS: 100, MonthlyIOPSCost: DiskLinearCost(0, 0.065)}
	config := testAdjustableDisk(5000, 0)
	config.ProvisionedThroughputMiBps = nil
	config.ThroughputSource = DiskPerformanceDerived

	target, ok := RecommendDiskPerformance(config, testCompleteAnalysis(0, 0), profile, testThresholds)
	require.True(t, ok)
	require.NotNil(t, target.IOPS)
	assert.Equal(t, int64(100), *target.IOPS)
}

// fakeDiskFetcher answers with a full window at a constant level for the disks it
// has usage for, and records what it was asked for.
type fakeDiskFetcher struct {
	usage  map[string][2]float64 // disk id -> IOPS, MiB/s
	groups [][]string
	err    error
}

func (f *fakeDiskFetcher) fetch(disks []DiskConfig, start, end time.Time, yield func(DiskPerformanceSeries)) error {
	var ids []string
	for _, disk := range disks {
		ids = append(ids, disk.DiskID)
		level, ok := f.usage[disk.DiskID]
		if !ok {
			continue
		}
		item := func(value float64) *MetricItem {
			item := &MetricItem{}
			for ts := start; ts.Before(end); ts = ts.Add(DiskMetricPeriod) {
				item.Values = append(item.Values, value)
				item.Timestamps = append(item.Timestamps, ts)
			}
			return item
		}
		yield(DiskPerformanceSeries{
			DiskID: disk.DiskID, Start: start, End: end, PeriodSeconds: 300, ValuesAreRates: true,
			ReadOps: item(level[0]), WriteOps: item(0), ReadBytes: item(level[1] * 1024 * 1024), WriteBytes: item(0),
		})
	}
	f.groups = append(f.groups, ids)
	return f.err
}

func testRuleOptions() DiskPerformanceRuleOptions {
	return DiskPerformanceRuleOptions{
		Thresholds: testThresholds,
		MaxDisks:   200,
		Now:        time.Date(2026, 10, 9, 11, 7, 0, 0, time.UTC),
	}
}

func testProfileFor(disk DiskConfig) (DiskPerformanceProfile, bool) {
	if disk.DiskType != "gp3" {
		return DiskPerformanceProfile{}, false
	}
	return testGp3LikeProfile(), true
}

func namedDisk(id string, iops int64, throughput float64) DiskConfig {
	disk := testAdjustableDisk(iops, throughput)
	disk.DiskID = id
	disk.DiskName = id
	return disk
}

func TestFindOverprovisionedDisksFetchesOnlyDisksThatCouldSave(t *testing.T) {
	unattached := namedDisk("unattached", 10000, 500)
	unattached.Attached = false
	derived := namedDisk("derived", 10000, 500)
	derived.IOPSSource = DiskPerformanceDerived
	derived.ThroughputSource = DiskPerformanceDerived
	unknownType := namedDisk("unknown-type", 10000, 500)
	unknownType.DiskType = "st1"

	disks := []DiskConfig{
		namedDisk("overprovisioned", 10000, 500),
		namedDisk("busy", 10000, 125),
		namedDisk("at-baseline", 3000, 125),
		unattached, derived, unknownType,
	}
	fetcher := &fakeDiskFetcher{usage: map[string][2]float64{
		"overprovisioned": {2000, 50},
		"busy":            {9000, 100},
	}}

	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()), disks, testProfileFor, fetcher.fetch, testRuleOptions())

	// Every candidate was read, so none is left unevaluated — including the busy
	// disk, which was evaluated and simply needs what it has.
	assert.Empty(t, unevaluated)

	// Only the two disks paying for performance above the baseline are fetched.
	require.Len(t, fetcher.groups, 1)
	assert.ElementsMatch(t, []string{"overprovisioned", "busy"}, fetcher.groups[0])

	require.Len(t, findings, 1)
	finding := findings[0]
	assert.Equal(t, "overprovisioned", finding.Config.DiskID)
	assert.Equal(t, DiskDataQualityComplete, finding.Analysis.DataQuality)
	require.NotNil(t, finding.Analysis.PeakIOPS)
	assert.InDelta(t, 2000.0, *finding.Analysis.PeakIOPS, 1e-9)
	require.NotNil(t, finding.Target.IOPS)
	assert.Equal(t, int64(3000), *finding.Target.IOPS)
	assert.InDelta(t, 50.0, finding.Target.MonthlySavings, 1e-9)
}

func TestFindOverprovisionedDisksNothingToFetch(t *testing.T) {
	fetcher := &fakeDiskFetcher{}
	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()),
		[]DiskConfig{namedDisk("at-baseline", 3000, 125)}, testProfileFor, fetcher.fetch, testRuleOptions())
	assert.Empty(t, findings)
	assert.Empty(t, unevaluated)
	assert.Empty(t, fetcher.groups)
}

func TestFindOverprovisionedDisksCapKeepsHighestSavingDisks(t *testing.T) {
	var disks []DiskConfig
	usage := map[string][2]float64{}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("disk-%02d", i)
		// Later disks have more provisioned, so more to save.
		disks = append(disks, namedDisk(id, int64(4000+i*100), 125))
		usage[id] = [2]float64{100, 10}
	}
	fetcher := &fakeDiskFetcher{usage: usage}
	options := testRuleOptions()
	options.MaxDisks = 30

	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()), disks, testProfileFor, fetcher.fetch, options)

	// Thirty disks, fetched in groups of twenty-five, highest saving first.
	require.Len(t, fetcher.groups, 2)
	assert.Len(t, fetcher.groups[0], 25)
	assert.Len(t, fetcher.groups[1], 5)
	assert.Equal(t, "disk-59", fetcher.groups[0][0])
	assert.Equal(t, "disk-30", fetcher.groups[1][4])
	assert.Len(t, findings, 30)

	// The thirty disks beyond the cap were not looked at, which is reported as such
	// rather than as "nothing to recommend".
	require.Len(t, unevaluated, 30)
	beyondCap := map[string]bool{}
	for _, disk := range unevaluated {
		beyondCap[disk.DiskID] = true
	}
	assert.True(t, beyondCap["disk-29"])
	assert.True(t, beyondCap["disk-00"])
	assert.False(t, beyondCap["disk-30"])
}

func TestFindOverprovisionedDisksStopsWhenTimeBudgetIsUsedUp(t *testing.T) {
	fetcher := &fakeDiskFetcher{usage: map[string][2]float64{"disk-1": {2000, 50}}}
	options := testRuleOptions()
	options.TimeBudget = time.Nanosecond

	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()),
		[]DiskConfig{namedDisk("disk-1", 10000, 500)}, testProfileFor, fetcher.fetch, options)

	assert.Empty(t, findings)
	assert.Empty(t, fetcher.groups)
	// The disk the budget ran out before is unevaluated, not cleared.
	require.Len(t, unevaluated, 1)
	assert.Equal(t, "disk-1", unevaluated[0].DiskID)
}

func TestFindOverprovisionedDisksFetchFailureDoesNotFailTheRule(t *testing.T) {
	fetcher := &fakeDiskFetcher{
		usage: map[string][2]float64{"read": {2000, 50}},
		err:   errors.New("throttled"),
	}
	disks := []DiskConfig{namedDisk("read", 10000, 500), namedDisk("failed", 10000, 500)}

	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()), disks, testProfileFor, fetcher.fetch, testRuleOptions())

	// The disk that was read still gets its finding. The one whose usage could not
	// be read gets none, and is reported as unevaluated so that a recommendation it
	// already has is not closed on the strength of a failed read.
	require.Len(t, findings, 1)
	assert.Equal(t, "read", findings[0].Config.DiskID)
	require.Len(t, unevaluated, 1)
	assert.Equal(t, "failed", unevaluated[0].DiskID)
}

func TestDiskPerformanceRecommendationData(t *testing.T) {
	finding := DiskPerformanceFinding{
		Config:   testAdjustableDisk(10000, 500),
		Analysis: testCompleteAnalysis(2000, 50),
		Target:   DiskPerformanceTarget{IOPS: int64Ptr(3000), ThroughputMiBps: floatPtr(125), MonthlySavings: 50},
	}

	data := DiskPerformanceRecommendationData(finding, testThresholds)

	assert.Equal(t, "disk-1", data["disk_id"])
	assert.Equal(t, "gp3", data["disk_type"])
	assert.Equal(t, "us-east-1", data["disk_region"])
	assert.Equal(t, 500.0, data["disk_size_gib"])
	assert.Equal(t, int64(10000), data["current_iops"])
	assert.Equal(t, int64(3000), data["recommended_iops"])
	assert.Equal(t, 2000.0, data["peak_iops"])
	assert.Equal(t, 20.0, data["iops_utilization_percent"])
	assert.Equal(t, 500.0, data["current_throughput_mibps"])
	assert.Equal(t, 125.0, data["recommended_throughput_mibps"])
	assert.Equal(t, 50.0, data["peak_throughput_mibps"])
	assert.Equal(t, 10.0, data["throughput_utilization_percent"])
	assert.Equal(t, 30, data["observation_days"])
	assert.Equal(t, "2026-09-09T11:05:00Z", data["observation_start"])
	assert.Equal(t, "2026-10-09T11:05:00Z", data["observation_end"])
	assert.Equal(t, int64(300), data["metric_period_seconds"])
	assert.Equal(t, int64(60), data["peak_resolution_seconds"])
	assert.Equal(t, 100.0, data["coverage_percent"])
	assert.Equal(t, 30.0, data["headroom_percent"])
	assert.Equal(t, 50.0, data["estimated_monthly_saving"])
	assert.Equal(t, true, data["savings_is_estimate"])

	reason, _ := data["reason"].(string)
	assert.Contains(t, reason, "last 30 days")
	assert.Contains(t, reason, "peaked at 2000 IOPS against 10000 provisioned")
	assert.Contains(t, reason, "50.0 MiB/s against 500.0 provisioned")
	assert.Contains(t, reason, "Lower IOPS to 3000.")
	assert.Contains(t, reason, "Lower throughput to 125.0 MiB/s.")
	assert.Contains(t, reason, "estimate")
	assert.Contains(t, reason, "busiest 1-minute measurement")
}

func TestDiskPerformanceRecommendationDataLeavesOutUnchangedSetting(t *testing.T) {
	finding := DiskPerformanceFinding{
		Config:   testAdjustableDisk(10000, 125),
		Analysis: testCompleteAnalysis(2000, 50),
		Target:   DiskPerformanceTarget{IOPS: int64Ptr(3000), MonthlySavings: 35},
	}

	data := DiskPerformanceRecommendationData(finding, testThresholds)

	assert.Equal(t, int64(3000), data["recommended_iops"])
	assert.NotContains(t, data, "recommended_throughput_mibps")
	assert.NotContains(t, data["reason"], "Lower throughput")
}

func TestDiskPerformanceRuleOptionsFromConfig(t *testing.T) {
	previous := config.Config
	defer func() { config.Config = previous }()
	config.Config.CloudCollectorDiskPerformanceHeadroomPercent = 25
	config.Config.CloudCollectorDiskPerformanceMaxUtilizationPercent = 40
	config.Config.CloudCollectorDiskPerformanceMinMonthlySavings = 5
	config.Config.CloudCollectorDiskPerformanceMaxDisks = 150
	config.Config.CloudCollectorDiskPerformanceTimeBudgetSeconds = 120

	before := time.Now()
	options := DiskPerformanceRuleOptionsFromConfig()

	assert.Equal(t, DiskPerformanceThresholds{HeadroomPercent: 25, MaxUtilizationPercent: 40, MinMonthlySavings: 5}, options.Thresholds)
	assert.Equal(t, 150, options.MaxDisks)
	assert.Equal(t, 2*time.Minute, options.TimeBudget)
	assert.False(t, options.Now.Before(before))
}

// A disk that was read but has no usable history cannot be judged. It is reported as
// unevaluated, like a disk that could not be read, so a recommendation it already has
// is not closed on the strength of missing data.
func TestFindOverprovisionedDisksNoDataIsUnevaluated(t *testing.T) {
	fetch := func(disks []DiskConfig, start, end time.Time, yield func(DiskPerformanceSeries)) error {
		for _, disk := range disks {
			yield(DiskPerformanceSeries{DiskID: disk.DiskID, Start: start, End: end, PeriodSeconds: 300})
		}
		return nil
	}

	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()),
		[]DiskConfig{namedDisk("no-history", 10000, 500)}, testProfileFor, fetch, testRuleOptions())

	assert.Empty(t, findings)
	require.Len(t, unevaluated, 1)
	assert.Equal(t, "no-history", unevaluated[0].DiskID)
}

// Partial history — the disk's instance was stopped for part of the month — is not
// enough to judge either. Without this, the disk's open recommendation would be
// archived while coverage is low and raised again once it recovers.
func TestFindOverprovisionedDisksPartialHistoryIsUnevaluated(t *testing.T) {
	fetch := func(disks []DiskConfig, start, end time.Time, yield func(DiskPerformanceSeries)) error {
		half := start.Add(end.Sub(start) / 2)
		item := func(value float64) *MetricItem {
			item := &MetricItem{}
			for ts := half; ts.Before(end); ts = ts.Add(DiskMetricPeriod) {
				item.Values = append(item.Values, value)
				item.Timestamps = append(item.Timestamps, ts)
			}
			return item
		}
		for _, disk := range disks {
			yield(DiskPerformanceSeries{DiskID: disk.DiskID, Start: start, End: end, PeriodSeconds: 300, ValuesAreRates: true,
				ReadOps: item(100), WriteOps: item(0), ReadBytes: item(0), WriteBytes: item(0)})
		}
		return nil
	}

	findings, unevaluated := FindOverprovisionedDisks(NewCloudProviderContext(context.Background()),
		[]DiskConfig{namedDisk("half-month", 10000, 500)}, testProfileFor, fetch, testRuleOptions())

	assert.Empty(t, findings)
	require.Len(t, unevaluated, 1)
	assert.Equal(t, "half-month", unevaluated[0].DiskID)
}

func TestUnevaluatedRecommendation(t *testing.T) {
	resource := Resource{Id: "vol-1", ServiceName: "AmazonEC2", Type: "storage", Region: "us-east-1"}

	placeholder := UnevaluatedRecommendation("disk_performance_overprovisioned", RecommendationCategoryRightSizing, resource)

	assert.True(t, placeholder.Unevaluated)
	assert.Equal(t, "disk_performance_overprovisioned", placeholder.RuleName)
	assert.Equal(t, "vol-1", placeholder.ResourceId)
	assert.Equal(t, "AmazonEC2", placeholder.ResourceServiceName)
	assert.Equal(t, "storage", placeholder.ResourceType)
	assert.Equal(t, "us-east-1", placeholder.ResourceRegion)
	assert.Zero(t, placeholder.Savings)
	assert.Nil(t, placeholder.Data)
}

// Whatever the usage, a recommended setting must be one the disk type accepts: not
// below the type's minimum, not above what is provisioned now, throughput within the
// limits tied to IOPS, and always cheaper.
func TestRecommendDiskPerformanceNeverReturnsAnInvalidTarget(t *testing.T) {
	profiles := map[string]DiskPerformanceProfile{
		"quarter throughput per IOPS": testGp3LikeProfile(),
		"throughput floor and ceiling per IOPS": {
			MinIOPS: 3000, MinThroughputMiBps: 140, MaxThroughputMiBpsPerIOPS: 1.0 / 4, MinThroughputMiBpsPerIOPS: 1.0 / 256,
			MonthlyIOPSCost: DiskLinearCost(3000, 0.005), MonthlyThroughputCost: DiskLinearCost(140, 0.04),
		},
	}
	iopsSettings := []int64{3000, 3100, 4000, 8000, 16000, 80000, 160000}
	throughputSettings := []float64{125, 140, 250, 500, 1000, 2400}
	peaks := []float64{0, 0.01, 0.1, 0.3, 0.49, 0.5, 0.9}

	checked, recommended := 0, 0
	for name, profile := range profiles {
		for _, iops := range iopsSettings {
			for _, throughput := range throughputSettings {
				// Skip starting points the disk type itself would not accept.
				if throughput < profile.MinThroughputMiBps || throughput > float64(iops)*profile.MaxThroughputMiBpsPerIOPS ||
					throughput < float64(iops)*profile.MinThroughputMiBpsPerIOPS {
					continue
				}
				for _, iopsPeak := range peaks {
					for _, throughputPeak := range peaks {
						checked++
						config := testAdjustableDisk(iops, throughput)
						target, ok := RecommendDiskPerformance(config, testCompleteAnalysis(float64(iops)*iopsPeak, throughput*throughputPeak), profile, testThresholds)
						if !ok {
							continue
						}
						recommended++
						label := fmt.Sprintf("%s: %d IOPS / %.0f MiB/s, peaks %.2f / %.2f", name, iops, throughput, iopsPeak, throughputPeak)

						targetIOPS, targetThroughput := float64(iops), throughput
						if target.IOPS != nil {
							targetIOPS = float64(*target.IOPS)
							assert.Less(t, targetIOPS, float64(iops), label)
							assert.GreaterOrEqual(t, targetIOPS, profile.MinIOPS, label)
							assert.GreaterOrEqual(t, targetIOPS, float64(iops)*iopsPeak, label)
						}
						if target.ThroughputMiBps != nil {
							targetThroughput = *target.ThroughputMiBps
							assert.Less(t, targetThroughput, throughput, label)
							assert.GreaterOrEqual(t, targetThroughput, profile.MinThroughputMiBps, label)
							assert.GreaterOrEqual(t, targetThroughput, throughput*throughputPeak, label)
						}
						assert.LessOrEqual(t, targetThroughput, targetIOPS*profile.MaxThroughputMiBpsPerIOPS+1e-9, label)
						assert.GreaterOrEqual(t, targetThroughput, targetIOPS*profile.MinThroughputMiBpsPerIOPS-1e-9, label)
						assert.GreaterOrEqual(t, target.MonthlySavings, testThresholds.MinMonthlySavings, label)
					}
				}
			}
		}
	}
	// Guard against the loops silently checking nothing.
	assert.Greater(t, checked, 1000)
	assert.Greater(t, recommended, 100)
}
