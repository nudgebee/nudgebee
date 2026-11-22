package providers

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testAnalysisStart = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// analysisSeries builds a series over a window of the given number of five-minute
// intervals. Each metric is given as a map from interval number to value, so an
// interval left out of a map is a missing measurement.
func analysisSeries(intervals int, readOps, writeOps, readBytes, writeBytes map[int]float64) DiskPerformanceSeries {
	return DiskPerformanceSeries{
		DiskID:        "disk-1",
		Start:         testAnalysisStart,
		End:           testAnalysisStart.Add(time.Duration(intervals) * DiskMetricPeriod),
		PeriodSeconds: int64(DiskMetricPeriod.Seconds()),
		ReadOps:       analysisMetric(readOps),
		WriteOps:      analysisMetric(writeOps),
		ReadBytes:     analysisMetric(readBytes),
		WriteBytes:    analysisMetric(writeBytes),
	}
}

func analysisMetric(points map[int]float64) *MetricItem {
	if points == nil {
		return nil
	}
	item := &MetricItem{}
	for interval, value := range points {
		item.Values = append(item.Values, value)
		item.Timestamps = append(item.Timestamps, testAnalysisStart.Add(time.Duration(interval)*DiskMetricPeriod))
	}
	return item
}

// every returns the same value for each of the first n intervals.
func every(n int, value float64) map[int]float64 {
	points := make(map[int]float64, n)
	for i := 0; i < n; i++ {
		points[i] = value
	}
	return points
}

const mib = 1024 * 1024

func TestDiskIOPSFromOps(t *testing.T) {
	iops, ok := DiskIOPSFromOps(90_000, 300, false)
	assert.True(t, ok)
	assert.InDelta(t, 300.0, iops, 1e-9)

	iops, ok = DiskIOPSFromOps(600_000, 60, false)
	assert.True(t, ok)
	assert.InDelta(t, 10_000.0, iops, 1e-9)

	// A rate is not divided again.
	iops, ok = DiskIOPSFromOps(150, 300, true)
	assert.True(t, ok)
	assert.InDelta(t, 150.0, iops, 1e-9)

	iops, ok = DiskIOPSFromOps(0, 300, false)
	assert.True(t, ok)
	assert.InDelta(t, 0.0, iops, 1e-9)

	_, ok = DiskIOPSFromOps(90_000, 0, false)
	assert.False(t, ok)
	_, ok = DiskIOPSFromOps(90_000, -300, false)
	assert.False(t, ok)
}

func TestDiskMiBpsFromBytes(t *testing.T) {
	// 300 MiB written over a 300-second interval is 1 MiB/s.
	mibps, ok := DiskMiBpsFromBytes(300*mib, 300, false)
	assert.True(t, ok)
	assert.InDelta(t, 1.0, mibps, 1e-9)

	// 125 MiB/s sustained for five minutes.
	mibps, ok = DiskMiBpsFromBytes(125*mib*300, 300, false)
	assert.True(t, ok)
	assert.InDelta(t, 125.0, mibps, 1e-9)

	// Bytes per second are only rescaled, not divided by the interval.
	mibps, ok = DiskMiBpsFromBytes(2*mib, 300, true)
	assert.True(t, ok)
	assert.InDelta(t, 2.0, mibps, 1e-9)

	// 1,000,000 bytes/s is a decimal megabyte, a little under one MiB.
	mibps, ok = DiskMiBpsFromBytes(1_000_000, 300, true)
	assert.True(t, ok)
	assert.InDelta(t, 0.95367431640625, mibps, 1e-9)

	_, ok = DiskMiBpsFromBytes(300*mib, 0, false)
	assert.False(t, ok)
}

func TestAnalyzeDiskPerformancePeaksFromIntervalTotals(t *testing.T) {
	series := analysisSeries(3,
		map[int]float64{0: 30_000, 1: 60_000, 2: 0},
		map[int]float64{0: 60_000, 1: 240_000, 2: 0},
		map[int]float64{0: 300 * mib, 1: 0, 2: 0},
		map[int]float64{0: 0, 1: 600 * mib, 2: 0},
	)

	analysis := AnalyzeDiskPerformance(series)

	assert.Equal(t, "disk-1", analysis.DiskID)
	assert.Equal(t, series.Start, analysis.ObservationStart)
	assert.Equal(t, series.End, analysis.ObservationEnd)
	assert.Equal(t, int64(300), analysis.PeriodSeconds)
	// Interval IOPS are 300, 1000 and 0; interval throughputs are 1, 2 and 0 MiB/s.
	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 1000.0, *analysis.PeakIOPS, 1e-9)
	require.NotNil(t, analysis.PeakThroughputMiBps)
	assert.InDelta(t, 2.0, *analysis.PeakThroughputMiBps, 1e-9)
	assert.InDelta(t, 100.0, analysis.CoveragePercent, 1e-9)
	assert.Equal(t, DiskDataQualityComplete, analysis.DataQuality)
	assert.Nil(t, analysis.IOPSLimitExceeded)
	assert.Nil(t, analysis.ThroughputLimitExceeded)
}

// Reads peak in one interval and writes in another. The peak is the busiest single
// interval, not the read peak added to the write peak.
func TestAnalyzeDiskPerformancePeakIsNotSumOfSeparatePeaks(t *testing.T) {
	series := analysisSeries(2,
		map[int]float64{0: 900_000, 1: 30_000},
		map[int]float64{0: 60_000, 1: 600_000},
		map[int]float64{0: 900 * mib, 1: 0},
		map[int]float64{0: 0, 1: 600 * mib},
	)

	analysis := AnalyzeDiskPerformance(series)

	// Interval totals are 960,000 and 630,000 operations: 3,200 and 2,100 IOPS.
	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 3200.0, *analysis.PeakIOPS, 1e-9)
	require.NotNil(t, analysis.PeakThroughputMiBps)
	assert.InDelta(t, 3.0, *analysis.PeakThroughputMiBps, 1e-9)
}

func TestAnalyzeDiskPerformanceRatesAreNotDividedAgain(t *testing.T) {
	series := analysisSeries(2,
		map[int]float64{0: 120, 1: 40},
		map[int]float64{0: 30, 1: 10},
		map[int]float64{0: 1 * mib, 1: 0},
		map[int]float64{0: 0.5 * mib, 1: 0},
	)
	series.ValuesAreRates = true

	analysis := AnalyzeDiskPerformance(series)

	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 150.0, *analysis.PeakIOPS, 1e-9)
	require.NotNil(t, analysis.PeakThroughputMiBps)
	assert.InDelta(t, 1.5, *analysis.PeakThroughputMiBps, 1e-9)
}

func TestAnalyzeDiskPerformanceCoverage(t *testing.T) {
	tests := []struct {
		name         string
		measured     int
		wantCoverage float64
		wantQuality  DiskDataQuality
	}{
		{"every interval measured", 12, 100, DiskDataQualityComplete},
		{"eleven of twelve is complete", 11, 100.0 * 11 / 12, DiskDataQualityComplete},
		{"ten of twelve is partial", 10, 100.0 * 10 / 12, DiskDataQualityPartial},
		{"half measured is partial", 6, 50, DiskDataQualityPartial},
		{"a single interval is partial", 1, 100.0 / 12, DiskDataQualityPartial},
		// A cloud that stamps points at the end of each interval can return one more
		// point than the window has intervals.
		{"an extra point does not exceed full coverage", 13, 100, DiskDataQualityComplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := AnalyzeDiskPerformance(analysisSeries(12,
				every(tt.measured, 3000), every(tt.measured, 3000), every(tt.measured, mib), every(tt.measured, mib)))

			assert.InDelta(t, tt.wantCoverage, analysis.CoveragePercent, 1e-9)
			assert.Equal(t, tt.wantQuality, analysis.DataQuality)
			require.NotNil(t, analysis.PeakIOPS)
			assert.InDelta(t, 20.0, *analysis.PeakIOPS, 1e-9)
		})
	}
}

func TestAnalyzeDiskPerformanceThirtyDayWindow(t *testing.T) {
	start, end := DiskObservationRange(time.Date(2026, 10, 9, 11, 7, 0, 0, time.UTC))
	item := func(value float64) *MetricItem {
		item := &MetricItem{}
		for ts := start; ts.Before(end); ts = ts.Add(DiskMetricPeriod) {
			item.Values = append(item.Values, value)
			item.Timestamps = append(item.Timestamps, ts)
		}
		return item
	}

	analysis := AnalyzeDiskPerformance(DiskPerformanceSeries{
		DiskID: "disk-1", Start: start, End: end, PeriodSeconds: 300,
		ReadOps: item(150_000), WriteOps: item(150_000), ReadBytes: item(0), WriteBytes: item(0),
	})

	assert.InDelta(t, 100.0, analysis.CoveragePercent, 1e-9)
	assert.Equal(t, DiskDataQualityComplete, analysis.DataQuality)
	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 1000.0, *analysis.PeakIOPS, 1e-9)
}

// The busiest read interval has no write measurement. It is left out rather than
// counted with writes read as zero.
func TestAnalyzeDiskPerformanceSkipsIntervalsMissingAMeasurement(t *testing.T) {
	series := analysisSeries(4,
		map[int]float64{0: 30_000, 1: 3_000_000, 2: 60_000, 3: 90_000},
		map[int]float64{0: 30_000, 2: 60_000, 3: 90_000},
		map[int]float64{0: mib, 1: mib, 2: mib, 3: mib},
		map[int]float64{0: mib, 1: mib, 2: mib},
	)

	analysis := AnalyzeDiskPerformance(series)

	// Only intervals 0 and 2 have all four measurements.
	assert.InDelta(t, 50.0, analysis.CoveragePercent, 1e-9)
	assert.Equal(t, DiskDataQualityPartial, analysis.DataQuality)
	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 400.0, *analysis.PeakIOPS, 1e-9)
}

func TestAnalyzeDiskPerformanceNoData(t *testing.T) {
	full := every(12, 3000)
	tests := []struct {
		name   string
		series DiskPerformanceSeries
	}{
		{"no series at all", analysisSeries(12, nil, nil, nil, nil)},
		{"write operations never returned", analysisSeries(12, full, nil, full, full)},
		{"read bytes never returned", analysisSeries(12, full, full, nil, full)},
		{"series with no points", analysisSeries(12, map[int]float64{}, map[int]float64{}, map[int]float64{}, map[int]float64{})},
		{"measurements never line up", analysisSeries(12, map[int]float64{0: 1}, map[int]float64{1: 1}, map[int]float64{2: 1}, map[int]float64{3: 1})},
		{"zero period", func() DiskPerformanceSeries {
			s := analysisSeries(12, full, full, full, full)
			s.PeriodSeconds = 0
			return s
		}()},
		{"window ends before it starts", func() DiskPerformanceSeries {
			s := analysisSeries(12, full, full, full, full)
			s.End = s.Start.Add(-time.Hour)
			return s
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := AnalyzeDiskPerformance(tt.series)

			// Unknown, not zero.
			assert.Nil(t, analysis.PeakIOPS)
			assert.Nil(t, analysis.PeakThroughputMiBps)
			assert.InDelta(t, 0.0, analysis.CoveragePercent, 1e-9)
			assert.Equal(t, DiskDataQualityNoData, analysis.DataQuality)
			assert.Equal(t, "disk-1", analysis.DiskID)
		})
	}
}

// A disk that reported zero activity all window was measured: its peak is zero, which
// is different from having no measurements.
func TestAnalyzeDiskPerformanceIdleDiskHasZeroPeak(t *testing.T) {
	zero := every(12, 0)
	analysis := AnalyzeDiskPerformance(analysisSeries(12, zero, zero, zero, zero))

	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 0.0, *analysis.PeakIOPS, 1e-9)
	require.NotNil(t, analysis.PeakThroughputMiBps)
	assert.InDelta(t, 0.0, *analysis.PeakThroughputMiBps, 1e-9)
	assert.InDelta(t, 100.0, analysis.CoveragePercent, 1e-9)
	assert.Equal(t, DiskDataQualityComplete, analysis.DataQuality)
}

func TestAnalyzeDiskPerformanceDropsImpossibleValues(t *testing.T) {
	series := analysisSeries(4,
		map[int]float64{0: 30_000, 1: -1, 2: math.NaN(), 3: math.Inf(1)},
		every(4, 30_000), every(4, mib), every(4, mib),
	)

	analysis := AnalyzeDiskPerformance(series)

	assert.InDelta(t, 25.0, analysis.CoveragePercent, 1e-9)
	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 200.0, *analysis.PeakIOPS, 1e-9)
}

func TestAnalyzeDiskPerformanceLimitExceeded(t *testing.T) {
	flags := func(values ...float64) *MetricItem {
		item := &MetricItem{Values: values}
		for i := range values {
			item.Timestamps = append(item.Timestamps, testAnalysisStart.AddDate(0, 0, i))
		}
		return item
	}

	series := analysisSeries(12, every(12, 3000), every(12, 3000), every(12, mib), every(12, mib))
	series.IOPSExceeded = flags(0, 0, 1, 0)
	series.ThroughputExceeded = flags(0, 0, 0, 0)
	analysis := AnalyzeDiskPerformance(series)
	require.NotNil(t, analysis.IOPSLimitExceeded)
	assert.True(t, *analysis.IOPSLimitExceeded)
	require.NotNil(t, analysis.ThroughputLimitExceeded)
	assert.False(t, *analysis.ThroughputLimitExceeded)

	// Not reported is unknown, and an empty series is the same as not reported.
	series.IOPSExceeded = nil
	series.ThroughputExceeded = &MetricItem{}
	analysis = AnalyzeDiskPerformance(series)
	assert.Nil(t, analysis.IOPSLimitExceeded)
	assert.Nil(t, analysis.ThroughputLimitExceeded)

	// The flags are read even when there is no usage history to go with them.
	noUsage := analysisSeries(12, nil, nil, nil, nil)
	noUsage.IOPSExceeded = flags(1)
	analysis = AnalyzeDiskPerformance(noUsage)
	assert.Equal(t, DiskDataQualityNoData, analysis.DataQuality)
	require.NotNil(t, analysis.IOPSLimitExceeded)
	assert.True(t, *analysis.IOPSLimitExceeded)
}

func TestDiskPerformanceAnalysisUtilizationPercent(t *testing.T) {
	int64Ptr := func(v int64) *int64 { return &v }
	explicit := DiskConfig{
		ProvisionedIOPS:            int64Ptr(10_000),
		ProvisionedThroughputMiBps: floatPtr(125),
		IOPSSource:                 DiskPerformanceExplicit,
		ThroughputSource:           DiskPerformanceExplicit,
	}
	analysis := DiskPerformanceAnalysis{PeakIOPS: floatPtr(2000), PeakThroughputMiBps: floatPtr(62.5)}

	t.Run("peak as a share of the explicit setting", func(t *testing.T) {
		iops := analysis.IOPSUtilizationPercent(explicit)
		require.NotNil(t, iops)
		assert.InDelta(t, 20.0, *iops, 1e-9)
		throughput := analysis.ThroughputUtilizationPercent(explicit)
		require.NotNil(t, throughput)
		assert.InDelta(t, 50.0, *throughput, 1e-9)
	})

	t.Run("a peak above the setting is over one hundred percent", func(t *testing.T) {
		busy := DiskPerformanceAnalysis{PeakIOPS: floatPtr(15_000)}
		iops := busy.IOPSUtilizationPercent(explicit)
		require.NotNil(t, iops)
		assert.InDelta(t, 150.0, *iops, 1e-9)
	})

	t.Run("a zero peak is zero percent, not unknown", func(t *testing.T) {
		idle := DiskPerformanceAnalysis{PeakIOPS: floatPtr(0), PeakThroughputMiBps: floatPtr(0)}
		iops := idle.IOPSUtilizationPercent(explicit)
		require.NotNil(t, iops)
		assert.InDelta(t, 0.0, *iops, 1e-9)
	})

	t.Run("unknown peak", func(t *testing.T) {
		assert.Nil(t, DiskPerformanceAnalysis{}.IOPSUtilizationPercent(explicit))
		assert.Nil(t, DiskPerformanceAnalysis{}.ThroughputUtilizationPercent(explicit))
	})

	t.Run("missing provisioned setting", func(t *testing.T) {
		config := DiskConfig{IOPSSource: DiskPerformanceExplicit, ThroughputSource: DiskPerformanceExplicit}
		assert.Nil(t, analysis.IOPSUtilizationPercent(config))
		assert.Nil(t, analysis.ThroughputUtilizationPercent(config))
	})

	t.Run("zero or negative provisioned setting is not divided by", func(t *testing.T) {
		config := explicit
		config.ProvisionedIOPS = int64Ptr(0)
		config.ProvisionedThroughputMiBps = floatPtr(-1)
		assert.Nil(t, analysis.IOPSUtilizationPercent(config))
		assert.Nil(t, analysis.ThroughputUtilizationPercent(config))
	})

	t.Run("a derived setting is not a limit to measure against", func(t *testing.T) {
		config := explicit
		config.IOPSSource = DiskPerformanceDerived
		config.ThroughputSource = DiskPerformanceDerived
		assert.Nil(t, analysis.IOPSUtilizationPercent(config))
		assert.Nil(t, analysis.ThroughputUtilizationPercent(config))
	})

	t.Run("an unavailable setting", func(t *testing.T) {
		config := explicit
		config.IOPSSource = DiskPerformanceUnavailable
		config.ThroughputSource = DiskPerformanceUnavailable
		assert.Nil(t, analysis.IOPSUtilizationPercent(config))
		assert.Nil(t, analysis.ThroughputUtilizationPercent(config))
	})

	t.Run("IOPS only disk has no throughput utilization", func(t *testing.T) {
		config := explicit
		config.ThroughputSource = DiskPerformanceDerived
		assert.NotNil(t, analysis.IOPSUtilizationPercent(config))
		assert.Nil(t, analysis.ThroughputUtilizationPercent(config))
	})
}

// The cloud reports the busiest minute of each five-minute interval. A one-minute
// burst is then measured over the minute it happened in, not spread over five.
func TestAnalyzeDiskPerformanceUsesSampleLengthForShortPeaks(t *testing.T) {
	// One minute at 3,000 IOPS (180,000 operations in the busiest minute) in an
	// otherwise idle interval, and 60 MiB/s for one minute.
	series := analysisSeries(3,
		map[int]float64{0: 600, 1: 180_000, 2: 600},
		map[int]float64{0: 0, 1: 0, 2: 0},
		map[int]float64{0: 0, 1: 60 * mib * 60, 2: 0},
		map[int]float64{0: 0, 1: 0, 2: 0},
	)
	series.SampleSeconds = 60

	analysis := AnalyzeDiskPerformance(series)

	assert.Equal(t, int64(300), analysis.PeriodSeconds)
	assert.Equal(t, int64(60), analysis.PeakResolutionSeconds)
	require.NotNil(t, analysis.PeakIOPS)
	assert.InDelta(t, 3000.0, *analysis.PeakIOPS, 1e-9)
	require.NotNil(t, analysis.PeakThroughputMiBps)
	assert.InDelta(t, 60.0, *analysis.PeakThroughputMiBps, 1e-9)
	// Coverage is still counted in five-minute intervals.
	assert.InDelta(t, 100.0, analysis.CoveragePercent, 1e-9)

	// Read as five-minute totals, the same numbers would hide the burst fivefold.
	series.SampleSeconds = 0
	averaged := AnalyzeDiskPerformance(series)
	assert.Equal(t, int64(300), averaged.PeakResolutionSeconds)
	assert.InDelta(t, 600.0, *averaged.PeakIOPS, 1e-9)
}

func TestAnalyzeDiskPerformanceResolutionOfRates(t *testing.T) {
	series := analysisSeries(1, every(1, 100), every(1, 50), every(1, mib), every(1, mib))
	series.ValuesAreRates = true

	analysis := AnalyzeDiskPerformance(series)

	// An average rate over the interval resolves nothing shorter than the interval.
	assert.Equal(t, int64(300), analysis.PeakResolutionSeconds)
	assert.InDelta(t, 150.0, *analysis.PeakIOPS, 1e-9)
}
