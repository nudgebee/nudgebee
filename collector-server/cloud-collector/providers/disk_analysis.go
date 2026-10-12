package providers

import (
	"math"
	"time"
)

// DiskCompleteCoveragePercent is the coverage at or above which a disk's measurements
// count as complete. It is a provisional value, to be settled against real workloads.
const DiskCompleteCoveragePercent = 90.0

// DiskIOPSFromOps converts an operations measurement to IOPS. A total is divided by
// the number of seconds it covers; a value that is already a per-second rate is
// returned unchanged. ok is false when a total cannot be converted because the time
// it covers is not positive.
func DiskIOPSFromOps(ops float64, sampleSeconds int64, isRate bool) (iops float64, ok bool) {
	if isRate {
		return ops, true
	}
	if sampleSeconds <= 0 {
		return 0, false
	}
	return ops / float64(sampleSeconds), true
}

// DiskMiBpsFromBytes converts a bytes measurement to MiB/s. A total is divided by the
// number of seconds it covers; a value that is already bytes per second is only
// rescaled. ok is false when a total cannot be converted because the time it covers
// is not positive.
func DiskMiBpsFromBytes(bytes float64, sampleSeconds int64, isRate bool) (mibps float64, ok bool) {
	if isRate {
		return bytes / bytesPerMiB, true
	}
	if sampleSeconds <= 0 {
		return 0, false
	}
	return bytes / (float64(sampleSeconds) * bytesPerMiB), true
}

// AnalyzeDiskPerformance reduces a disk's raw usage history to its peak IOPS and peak
// throughput over the observation window.
//
// An interval counts only when all four measurements — read and write operations,
// read and write bytes — are present for it. Its IOPS is read plus write operations
// and its throughput read plus write bytes, each as a per-second rate; the peak is the
// highest such interval. An interval missing any measurement is left out rather than
// read as idle, because a partial total would understate the peak. CoveragePercent is
// the share of the window's intervals that counted, and the peaks stay nil when none
// did.
//
// Where the values are each interval's busiest sample (SampleSeconds shorter than
// PeriodSeconds), the busiest read sample and the busiest write sample of an interval
// need not be the same sample, so their sum can overstate that interval's true
// combined peak. It never understates it, which is the safe direction here.
func AnalyzeDiskPerformance(series DiskPerformanceSeries) DiskPerformanceAnalysis {
	analysis := DiskPerformanceAnalysis{
		DiskID:                  series.DiskID,
		ObservationStart:        series.Start,
		ObservationEnd:          series.End,
		PeriodSeconds:           series.PeriodSeconds,
		DataQuality:             DiskDataQualityNoData,
		IOPSLimitExceeded:       diskLimitExceeded(series.IOPSExceeded),
		ThroughputLimitExceeded: diskLimitExceeded(series.ThroughputExceeded),
	}
	if series.PeriodSeconds <= 0 {
		return analysis
	}
	sampleSeconds := series.SampleSeconds
	if sampleSeconds <= 0 {
		sampleSeconds = series.PeriodSeconds
	}
	analysis.PeakResolutionSeconds = sampleSeconds
	expectedIntervals := int64(series.End.Sub(series.Start) / (time.Duration(series.PeriodSeconds) * time.Second))
	if expectedIntervals <= 0 {
		return analysis
	}

	readOps := diskMeasurementsByTime(series.ReadOps)
	writeOps := diskMeasurementsByTime(series.WriteOps)
	readBytes := diskMeasurementsByTime(series.ReadBytes)
	writeBytes := diskMeasurementsByTime(series.WriteBytes)

	var measured int64
	var peakIOPS, peakThroughput float64
	for timestamp, read := range readOps {
		write, hasWriteOps := writeOps[timestamp]
		bytesRead, hasReadBytes := readBytes[timestamp]
		bytesWritten, hasWriteBytes := writeBytes[timestamp]
		if !hasWriteOps || !hasReadBytes || !hasWriteBytes {
			continue
		}
		iops, _ := DiskIOPSFromOps(read+write, sampleSeconds, series.ValuesAreRates)
		throughput, _ := DiskMiBpsFromBytes(bytesRead+bytesWritten, sampleSeconds, series.ValuesAreRates)
		measured++
		peakIOPS = math.Max(peakIOPS, iops)
		peakThroughput = math.Max(peakThroughput, throughput)
	}
	if measured == 0 {
		return analysis
	}

	analysis.PeakIOPS = &peakIOPS
	analysis.PeakThroughputMiBps = &peakThroughput
	analysis.CoveragePercent = math.Min(100, float64(measured)/float64(expectedIntervals)*100)
	analysis.DataQuality = DiskDataQualityPartial
	if analysis.CoveragePercent >= DiskCompleteCoveragePercent {
		analysis.DataQuality = DiskDataQualityComplete
	}
	return analysis
}

// diskMeasurementsByTime indexes a series by timestamp. Values that cannot be a real
// measurement — negative, NaN or infinite — are dropped, so their intervals count as
// missing.
func diskMeasurementsByTime(item *MetricItem) map[int64]float64 {
	if item == nil {
		return nil
	}
	measurements := make(map[int64]float64, len(item.Values))
	for i := 0; i < len(item.Values) && i < len(item.Timestamps); i++ {
		value := item.Values[i]
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		measurements[item.Timestamps[i].Unix()] = value
	}
	return measurements
}

// diskLimitExceeded reads an exceeded-check series: true if any point is set, false if
// the series has points and none is, nil if there is nothing to read.
func diskLimitExceeded(item *MetricItem) *bool {
	if item == nil || len(item.Values) == 0 {
		return nil
	}
	exceeded := false
	for _, value := range item.Values {
		if value > 0 {
			exceeded = true
			break
		}
	}
	return &exceeded
}

// IOPSUtilizationPercent is the observed peak IOPS as a percentage of the provisioned
// IOPS. It is nil unless the peak is known and the disk's IOPS is an explicit,
// positive setting: a derived figure such as a burstable baseline is not a limit the
// peak can be measured against.
func (a DiskPerformanceAnalysis) IOPSUtilizationPercent(config DiskConfig) *float64 {
	if config.IOPSSource != DiskPerformanceExplicit || config.ProvisionedIOPS == nil {
		return nil
	}
	return diskUtilizationPercent(a.PeakIOPS, float64(*config.ProvisionedIOPS))
}

// ThroughputUtilizationPercent is the observed peak throughput as a percentage of the
// provisioned throughput, under the same conditions as IOPSUtilizationPercent.
func (a DiskPerformanceAnalysis) ThroughputUtilizationPercent(config DiskConfig) *float64 {
	if config.ThroughputSource != DiskPerformanceExplicit || config.ProvisionedThroughputMiBps == nil {
		return nil
	}
	return diskUtilizationPercent(a.PeakThroughputMiBps, *config.ProvisionedThroughputMiBps)
}

func diskUtilizationPercent(peak *float64, provisioned float64) *float64 {
	if peak == nil || provisioned <= 0 {
		return nil
	}
	utilization := *peak / provisioned * 100
	return &utilization
}
