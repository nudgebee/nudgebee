package providers

import (
	"sort"
	"time"
)

const (
	// DiskObservationWindow is how far back disk usage is read when judging whether
	// a disk's performance is over-provisioned.
	DiskObservationWindow = 30 * 24 * time.Hour
	// DiskMetricPeriod is the interval each usage measurement covers.
	DiskMetricPeriod = 5 * time.Minute
)

// DiskObservationRange returns the observation window ending at now, with both ends
// on a DiskMetricPeriod boundary so the window holds a whole number of intervals.
func DiskObservationRange(now time.Time) (start, end time.Time) {
	end = now.UTC().Truncate(DiskMetricPeriod)
	return end.Add(-DiskObservationWindow), end
}

// DiskMetricsQuery fetches metrics from a cloud's monitoring service. Each provider's
// QueryMetrices implementation satisfies it; tests substitute a fake.
type DiskMetricsQuery func(ctx CloudProviderContext, account Account, filter QueryMetricsRequest) (QueryMetricsResponse, error)

// DiskPerformanceSeries is the raw usage history of one disk over an observation
// window, exactly as the cloud reported it. A nil series means the cloud returned no
// measurements for it, which is not the same as zero usage; likewise an interval with
// no point in a series is missing, not idle.
type DiskPerformanceSeries struct {
	DiskID        string
	Start         time.Time
	End           time.Time
	PeriodSeconds int64
	// ValuesAreRates is true when the values are already per-second rates (Azure)
	// and false when they are totals (AWS, GCP).
	ValuesAreRates bool
	// SampleSeconds is how long a time each total covers, which is what it is
	// divided by to get a rate. It equals PeriodSeconds when a value is the total for
	// its whole interval, and is shorter when a value is the total of the busiest
	// sample within the interval: AWS reports the busiest minute of each five. Zero
	// means PeriodSeconds.
	SampleSeconds int64
	ReadOps       *MetricItem
	WriteOps      *MetricItem
	ReadBytes     *MetricItem
	WriteBytes    *MetricItem
	// IOPSExceeded and ThroughputExceeded are AWS-only daily flags: 1 when the
	// workload tried to drive more than the provisioned setting that day. They are a
	// supplementary signal and are nil where the cloud does not report them.
	IOPSExceeded       *MetricItem
	ThroughputExceeded *MetricItem
}

// MergeMetricItems joins the pieces of one metric series into a single series in
// ascending time order. A cloud can return one series split over several pages, and
// in either time order. It returns nil when there are no points. Where the same
// timestamp appears more than once the first value seen is kept.
func MergeMetricItems(items []MetricItem) *MetricItem {
	type point struct {
		timestamp time.Time
		value     float64
	}
	var points []point
	seen := map[int64]bool{}
	for _, item := range items {
		for i := 0; i < len(item.Values) && i < len(item.Timestamps); i++ {
			key := item.Timestamps[i].UnixNano()
			if seen[key] {
				continue
			}
			seen[key] = true
			points = append(points, point{item.Timestamps[i], item.Values[i]})
		}
	}
	if len(points) == 0 {
		return nil
	}
	sort.Slice(points, func(i, j int) bool { return points[i].timestamp.Before(points[j].timestamp) })

	merged := items[0]
	merged.Values = make([]float64, len(points))
	merged.Timestamps = make([]time.Time, len(points))
	for i, p := range points {
		merged.Values[i] = p.value
		merged.Timestamps[i] = p.timestamp
	}
	return &merged
}
