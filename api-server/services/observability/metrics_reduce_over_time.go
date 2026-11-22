package observability

import (
	"math"
	"sort"
)

// Pure reducers over one series' own values, shared by every provider that collapses
// a range on this side (Datadog, OpenObserve, SolarWinds). Each provider decides
// WHEN to call them and how `count` converts into the contract's unit; these only do
// the arithmetic.

// reduceOverTime applies the requested aggregate to one series' own values.
//
// A provider that cannot push the aggregate down -- for every key, or for some --
// hands the range to this side, and the collapse happens here. Otherwise `aggregate`
// silently means nothing for those keys, and a consumer reading payload[0] gets the
// oldest point in the window instead of the statistic it asked for.
//
// `count` is the number of finite readings in the provider's own buckets; each
// caller converts it into the contract's unit (see metrics_aggregate_count.go).
//
// Reports false when the series holds no finite value, which the caller turns into a
// single NaN rather than a zero.
func reduceOverTime(values []float64, aggregate ParsedAggregate) (float64, bool) {
	if aggregate.IsQuantile() {
		return percentileOverTime(values, aggregate.Quantile)
	}

	finite := make([]float64, 0, len(values))
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			finite = append(finite, v)
		}
	}
	if len(finite) == 0 {
		return 0, false
	}

	switch aggregate.Name {
	case AggregateMax:
		out := finite[0]
		for _, v := range finite[1:] {
			if v > out {
				out = v
			}
		}
		return out, true
	case AggregateMin:
		out := finite[0]
		for _, v := range finite[1:] {
			if v < out {
				out = v
			}
		}
		return out, true
	case AggregateSum, AggregateAvg:
		total := 0.0
		for _, v := range finite {
			total += v
		}
		if aggregate.Name == AggregateSum {
			return total, true
		}
		return total / float64(len(finite)), true
	case AggregateCount:
		return float64(len(finite)), true
	}
	return 0, false
}

// percentileOverTime is linear interpolation over one series' own values, matching
// the quantile_over_time() the Prometheus path asks the engine for and the
// np.percentile() the Python Datadog service used before this moved server-side.
func percentileOverTime(values []float64, quantile float64) (float64, bool) {
	finite := make([]float64, 0, len(values))
	for _, v := range values {
		// A gap arrives as NaN and is not a measurement; counting it as one is the
		// bug that made every Datadog percentile read low.
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			finite = append(finite, v)
		}
	}
	if len(finite) == 0 {
		return 0, false
	}
	sort.Float64s(finite)
	if len(finite) == 1 {
		return finite[0], true
	}
	pos := quantile * float64(len(finite)-1)
	lo, hi := int(math.Floor(pos)), int(math.Ceil(pos))
	if hi >= len(finite) {
		return finite[len(finite)-1], true
	}
	return finite[lo] + (finite[hi]-finite[lo])*(pos-float64(lo)), true
}
