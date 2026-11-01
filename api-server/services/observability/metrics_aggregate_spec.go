package observability

import (
	"fmt"
	"strconv"
	"strings"
)

// Aggregate collapses each series a utilisation key defines to ONE point, over the
// whole window.
//
// It is the only parameterised part of the contract. Everything else — which metric,
// and whether the answer is per workload or per pod — is carried by the key name, so
// a key's shape never varies with the request. The statistic is the exception because
// it is the one value a caller legitimately varies: rightsizing reads its percentiles
// from strategy settings (92, 95, 97, 99 today), and as key names, retuning one would
// be a code change in every provider instead of a config edit.
//
// "Collapse", not "roll up": a per-step series is what the plain keys already return.
// That distinction has to be stated because the providers disagree about what a
// windowed aggregate means — computePercentileFromSeries reduces ACROSS series at
// each timestamp, ooPercentile reduces one series OVER time, and both have been
// called "p90".
const (
	AggregateAvg = "avg"
	AggregateSum = "sum"
	AggregateMax = "max"
	AggregateMin = "min"
	// AggregateCount is the one aggregate whose unit is the contract's rather than
	// the provider's: Step-wide intervals with data, not stored samples. See
	// metrics_aggregate_count.go for why, and for the conversion each provider does.
	AggregateCount = "count"
)

// ParsedAggregate is a validated Aggregate. Quantile is set only when the caller
// asked for a percentile, in which case Name is "quantile".
type ParsedAggregate struct {
	Name     string
	Quantile float64
}

// IsQuantile reports whether this aggregate needs a percentile rather than a plain
// reduction — the split that decides whether a provider can answer natively.
func (p ParsedAggregate) IsQuantile() bool { return p.Name == "quantile" }

// parseAggregate validates the caller's aggregate at the door, so no builder has to
// guess what an unknown value means. The vocabulary is closed; the percentile value
// is open.
func parseAggregate(aggregate string) (ParsedAggregate, error) {
	switch aggregate {
	case "":
		return ParsedAggregate{}, nil
	case AggregateAvg, AggregateSum, AggregateMax, AggregateMin, AggregateCount:
		return ParsedAggregate{Name: aggregate}, nil
	}

	// Fractional percentiles are allowed (p99.9 is a normal latency ask); 100 is not,
	// because that is `max` under another name and every provider already has it.
	if rest, isPercentile := strings.CutPrefix(aggregate, "p"); isPercentile {
		if n, err := strconv.ParseFloat(rest, 64); err == nil && n >= 1 && n < 100 {
			return ParsedAggregate{Name: "quantile", Quantile: n / 100}, nil
		}
	}
	return ParsedAggregate{}, fmt.Errorf(
		"observability: unsupported aggregate %q; expected one of avg, sum, max, min, count, or p1..p99.9 (use max for p100)", aggregate)
}

// promQLOverTime wraps an expression so it collapses over the window.
//
// The subquery form is what the rightsizing loaders already use — the strategy asks
// for the 99th percentile of a pod's own CPU over 14 days at a 75s step, which is
// quantile_over_time(0.99, <rate expr>[14d:75s]). The expression is parenthesised
// because a key's query can be an `or` fallback, and a subquery binds to the whole
// expression only when it is.
func promQLOverTime(agg ParsedAggregate, expr, window, step string) string {
	if agg.Name == "" {
		return expr
	}
	subquery := fmt.Sprintf("(%s)[%s:%s]", expr, window, step)
	if agg.IsQuantile() {
		// %g so 0.92 stays 0.92 rather than 0.920000.
		return fmt.Sprintf("quantile_over_time(%g, %s)", agg.Quantile, subquery)
	}
	return fmt.Sprintf("%s_over_time(%s)", agg.Name, subquery)
}

// promSelfWindowedKeys are the keys whose PromQL already collapses the request's own
// range window. The percentile/peak family aggregates over [rangeW:stepW] by
// construction and cpu_real rates across the whole window, so wrapping them again
// would take a statistic of a statistic — a max_over_time of a max_over_time. For
// these the key's own definition wins and the request's aggregate is a no-op.
var promSelfWindowedKeys = map[UtilisationKey]bool{
	KeyCPUReal:     true,
	KeyP50CPU:      true,
	KeyP90CPU:      true,
	KeyMaxUsageCPU: true,
	KeyMaxUsageMem: true,
}

// promApplyAggregate collapses every built query to one point of the requested
// statistic.
//
// It runs over the whole map at the end of a builder rather than inside each case,
// because `aggregate` is a property of the REQUEST, not of the key: a caller that
// asks for max gets max whichever key it names. Wrapping case by case is what let
// cpu_usage and memory_usage silently ignore it — aggregate=max and aggregate=avg
// returned the same number, and it was neither the maximum nor the average but the
// instantaneous value at the window end, because an aggregated request is served as
// an instant query and an unwrapped expression has nothing to collapse.
//
// Datadog already reduces every series uniformly in datadogCollapseOverTime; this is
// the Prometheus half of the same contract. With no aggregate asked for it returns
// immediately, so the default path's query text is unchanged for every key.
func promApplyAggregate(queries map[string]string, agg ParsedAggregate, rangeW, stepW string) {
	if agg.Name == "" {
		return
	}
	for key, expr := range queries {
		if promSelfWindowedKeys[UtilisationKey(key)] {
			continue
		}
		queries[key] = promQLOverTime(agg, expr, rangeW, stepW)
	}
}
