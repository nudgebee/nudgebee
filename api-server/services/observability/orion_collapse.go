package observability

import (
	"math"
	"sort"
)

// orionCollapseOverTime answers an aggregate request with one point per series.
//
// SWQL cannot do it in the query: Orion's statistics tables already hold
// per-observation averages, and its own aggregate functions cannot be combined
// with the per-row timestamp the series needs. So the range is fetched and reduced
// here, the way SolarWinds and Datadog do for the aggregates their APIs cannot be
// asked for.
//
// A `count` is converted to the caller's step, because Orion's reading interval is
// its own: ten minutes on the detail tier, and longer once a window reaches back
// into the hourly or daily tier. Counting raw rows would report a tenth of the
// samples a Prometheus store reports for the same window.
func orionCollapseOverTime(results []QueryResult, meta RequestMetadata) {
	if meta.Aggregate.Name == "" {
		return
	}
	step := aggregateStepSeconds(meta)
	for i := range results {
		width := orionReadingSeconds(results[i])
		for j := range results[i].Payload {
			series := &results[i].Payload[j]
			if len(series.Timestamps) == 0 {
				continue
			}
			value, ok := reduceOverTime(series.Values, meta.Aggregate)
			switch {
			case !ok:
				// Every sample was a gap. Still one point, carrying NaN (null)
				// rather than a zero that would read as an idle machine.
				value = math.NaN()
			case meta.Aggregate.Name == AggregateCount:
				value = stepEquivalentCount(value, width, step)
			}
			last := series.Timestamps[len(series.Timestamps)-1]
			series.Timestamps, series.Values = []int64{last}, []float64{value}
		}
	}
}

// orionReadingSeconds is how much time one reading stands for, taken as the typical
// gap between consecutive readings. Orion does not report a bucket width, and the
// gap is not constant across a window that spans retention tiers, so the median is
// the honest estimate. Zero when there are not two readings to compare, which
// stepEquivalentCount reads as "do not convert".
func orionReadingSeconds(result QueryResult) float64 {
	var gaps []int64
	for _, series := range result.Payload {
		for k := 1; k < len(series.Timestamps); k++ {
			if gap := series.Timestamps[k] - series.Timestamps[k-1]; gap > 0 {
				gaps = append(gaps, gap)
			}
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return float64(gaps[len(gaps)/2])
}
