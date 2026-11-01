package observability

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
)

// Per-pod container usage on Datadog, for rightsizing.
//
// Datadog answers these differently from every other provider, in two ways that the
// contract has to absorb rather than expose:
//
//   - **There is no percentile over time.** `p95:`/`p99:` are distribution-metric
//     aggregators for APM traces, not rollups over a gauge, so a percentile of
//     kubernetes.cpu.usage.total has to be computed here from the raw points.
//   - **Resolution is chosen, not given.** Left alone, Datadog rolls a 14-day query
//     up into 2-hour buckets — measured on a live account: 168 points per pod, so a
//     "p99" over eight readings is barely more than a max. An explicit
//     `.rollup(avg, 300)` gives 4,032 points per pod instead, which is a real
//     percentile, and 28.5 MB for a workload with 387 pod incarnations. The rollup
//     is therefore picked from how many pods the caller asked about.
const (
	// datadogPerPodMetricCPU and friends are the gauges Datadog's agent reports.
	datadogPerPodMetricCPU           = "kubernetes.cpu.usage.total"
	datadogPerPodMetricMemory        = "kubernetes.memory.working_set"
	datadogPerPodMetricCPURequest    = "kubernetes.cpu.requests"
	datadogPerPodMetricCPULimit      = "kubernetes.cpu.limits"
	datadogPerPodMetricMemoryRequest = "kubernetes.memory.requests"
	datadogPerPodMetricMemoryLimit   = "kubernetes.memory.limits"

	datadogPerPodPointBudget     = 200_000
	datadogFinestRollupSeconds   = 300
	datadogCoarsestRollupSeconds = 3600
)

// datadogPerPodKeys are answered per pod and container rather than per workload.
var datadogPerPodKeys = map[UtilisationKey]string{
	KeyContainerCPUUsageByPod:         datadogPerPodMetricCPU,
	KeyContainerMemoryWorkingSetByPod: datadogPerPodMetricMemory,
	KeyContainerCPURequestByPod:       datadogPerPodMetricCPURequest,
	KeyContainerCPULimitByPod:         datadogPerPodMetricCPULimit,
	KeyContainerMemoryRequestByPod:    datadogPerPodMetricMemoryRequest,
	KeyContainerMemoryLimitByPod:      datadogPerPodMetricMemoryLimit,
}

// datadogUnanswerableKeys are keys Datadog cannot answer, and why. Kept as a list
// rather than an absence so the reason reaches the caller: a key merely missing from
// the builder is dropped from the response, which is indistinguishable from a
// workload that used nothing.
var datadogUnanswerableKeys = map[UtilisationKey]string{
	KeyContainerMemoryMaxUsageByPod: "Datadog reports no cgroup high-water mark. " +
		"kubernetes.memory.working_set is a different number — answering with it would size a limit too low.",
	KeyContainerOOMKilledLimitByPod: "Datadog reports no per-container terminated reason, so there is nothing " +
		"to join a memory limit against. kubernetes_state.container.restarts counts restarts of every cause — " +
		"answering with it would report a limit for containers that were never OOM-killed.",

	// kubernetes.network.{rx,tx}_bytes exists, but whether it reports a per-second
	// rate or a per-interval total decides whether bytes-per-bucket is
	// `value * bucket` or `sum(values)`, and the two differ by the scrape interval.
	// Measured on a live account, the ratio between the raw series and a summed
	// hourly rollup was ~133 — consistent with neither reading cleanly. Rather than
	// ship a conversion that is plausible and possibly wrong by a factor of the
	// scrape interval, the key says it is unanswerable until the semantics are
	// confirmed against Datadog's own metric metadata.
	KeyNetworkReceiveBytesIncrease:  "Not implemented for Datadog: kubernetes.network.rx_bytes' rate-versus-total semantics are unconfirmed, and guessing decides the unit.",
	KeyNetworkTransmitBytesIncrease: "Not implemented for Datadog: kubernetes.network.tx_bytes' rate-versus-total semantics are unconfirmed, and guessing decides the unit.",
}

// datadogRollupSecondsForPods picks the finest rollup whose response stays inside the
// point budget.
//
// points = (window / rollup) * pods, and the budget is what keeps a churny workload
// from pulling tens of megabytes: 387 pod incarnations at 5-minute resolution over 14
// days measured 28.5 MB, while a typical six-pod workload at the same resolution is
// well under a megabyte. Coarsening costs resolution, which the note says; the
// alternative is a response nobody can afford to fetch.
func datadogRollupSecondsForPods(windowSeconds int64, pods int) int {
	if pods < 1 {
		pods = 1
	}
	for rollup := datadogFinestRollupSeconds; rollup <= datadogCoarsestRollupSeconds; rollup *= 3 {
		if (windowSeconds/int64(rollup))*int64(pods) <= datadogPerPodPointBudget {
			return rollup
		}
	}
	return datadogCoarsestRollupSeconds
}

// buildDatadogPerPodQuery renders one per-pod metric, grouped by pod and container.
//
// A plain aggregate is pushed down: rolling up over the WHOLE window collapses each
// series to one point, which is what Aggregate means, and costs one value per pod on
// the wire. A percentile cannot be pushed down, so the query asks for the finest
// resolution the budget allows and datadogCollapseOverTime does the arithmetic.
func buildDatadogPerPodQuery(metric string, meta RequestMetadata, filterStr string, windowSeconds int64) string {
	query := fmt.Sprintf("avg:%s{%s} by {kube_namespace,pod_name,kube_container_name}", metric, datadogPerPodFilter(filterStr, meta))
	if meta.Aggregate.Name == "" {
		return query
	}
	return fmt.Sprintf("%s.rollup(%s, %d)", query,
		datadogRollupFunc(meta.Aggregate), datadogPerPodRollupSeconds(meta, windowSeconds))
}

// datadogRollupFunc is the function Datadog buckets with, which is not always the
// aggregate the caller asked for.
//
// The aggregate's own function wherever Datadog has it. Bucketing by average and then
// taking the maximum on this side does not give a maximum: a spike shorter than the
// bucket is averaged away before this side ever sees it. Measured on a workload with
// 69 pod incarnations, `max` over max-buckets read 6.7x higher than `max` over
// avg-buckets at the same 900s width, and 21x higher at the extreme.
//
// `avg` for a percentile, because Datadog has no percentile rollup and the quantile is
// computed here across the buckets.
//
// `count` asks Datadog to count instead, which is not the same question as "how many
// buckets came back". A bucket's VALUE under this rollup is the number of raw
// submissions inside it, so summing the values measures observation directly and is
// independent of how wide the buckets are. Counting non-empty buckets instead credits a
// bucket holding one sample with its entire width: measured on a 2700s bucket, a pod
// that reported 3 samples was credited 36 step-equivalent points where it had earned
// 0.8 -- a 45x over-credit, against a floor of 50.
func datadogRollupFunc(agg ParsedAggregate) string {
	if agg.IsQuantile() {
		return "avg"
	}
	return agg.Name
}

// datadogPerPodRollupSeconds is the bucket width those aggregates are computed from.
//
// Shared by the builder and the collapse rather than recomputed in each, because the
// count conversion multiplies by exactly this number: a collapse that assumed a
// different rollup from the one the query asked for would misreport coverage by that
// ratio, silently and only on Datadog.
func datadogPerPodRollupSeconds(meta RequestMetadata, windowSeconds int64) int {
	pods := len(meta.PodNames)
	if !meta.PodNamesSupplied {
		// No pod list means no series count, but "unknown" is not a reason to ignore
		// the WINDOW too. The old code returned the coarsest rollup outright, which
		// charged a six-hour request the resolution a fourteen-day one needs: at 6h
		// even 1000 pods at 300s is 72k points, a third of the budget.
		//
		// Measured against Prometheus on the same cluster, that cost real accuracy --
		// and not only for percentiles, which was the surprise. Workload-scoped, as
		// a fraction of the true value:
		//
		//     CPU p99     17-26%   (a p99 over hour-wide averages is not a p99)
		//     memory max  39%      on one workload; exact on the other five
		//     coverage    111-195% (a bucket with one sample is credited its width)
		//
		// The memory result is why this does not special-case the aggregate. `max`
		// reads as exact under coarse buckets on most workloads, so an earlier version
		// of this change kept `max` coarse to save budget -- which would have
		// under-reported one workload's memory by 61%, the OOM direction. Every
		// aggregate gets the finest rollup the budget allows.
		pods = datadogWorkloadScopedPodEstimate
	}
	return datadogRollupSecondsForPods(windowSeconds, pods)
}

// datadogWorkloadScopedPodEstimate stands in for the series count when the caller
// names no pods.
//
// It is an assumption, so it is deliberately pessimistic: 291 is the most pod
// incarnations one workload has been measured with (a 14-day window on a workload that
// redeploys constantly), and this rounds that up. Being pessimistic only costs
// resolution on long windows -- a 14-day request lands on 2700s rather than 300s --
// while short windows, where the budget has room to spare whatever the pod count, get
// the finest rollup regardless.
//
// A caller that knows its pods should send them: an exact list sizes the budget on the
// real number and is always at least as good as this.
const datadogWorkloadScopedPodEstimate = 300

// datadogPerPodFilter scopes the query to the caller's exact pod list.
//
// Datadog rejects a scope that mixes comma-separated filters with boolean ones, so
// naming pods means re-joining the whole filter with AND. Verified against the live
// API: `{kube_namespace:x AND (pod_name:a OR pod_name:b)}` returns exactly those two
// pods, while the comma form alongside a boolean does not parse.
//
// Pod names need no quoting: RFC1123 limits them to lower-case alphanumerics, "-" and
// ".", none of which are Datadog scope metacharacters.
func datadogPerPodFilter(filterStr string, meta RequestMetadata) string {
	if !meta.PodNamesSupplied {
		// No list means "every pod under this workload", which is the comma-joined
		// filter every other Datadog key already uses. Left byte-identical.
		return filterStr
	}

	// Split on the comma and trim, not on ", ": a clause added later without the
	// space would otherwise survive into a boolean scope, and Datadog rejects the
	// mix. An empty filter contributes no clause rather than a leading " AND ".
	var clauses []string
	for _, clause := range strings.Split(filterStr, ",") {
		if trimmed := strings.TrimSpace(clause); trimmed != "" {
			clauses = append(clauses, trimmed)
		}
	}

	if len(meta.PodNames) == 0 {
		// The caller resolved the pods and found none. That is an answer, not a
		// missing parameter, so the filter must match nothing — the workload tag
		// alone would hand back every pod and size the workload on pods the caller
		// had already excluded. An unsatisfiable tag returns no series and no error,
		// confirmed against the live API.
		clauses = append(clauses, "pod_name:"+datadogNoPodsSentinel)
	} else {
		clauses = append(clauses, datadogPodNameClause(meta.PodNames))
	}
	return strings.Join(clauses, " AND ")
}

// datadogNoPodsSentinel is a pod name Kubernetes cannot produce (RFC1123 forbids
// underscores). The same literal as the Prometheus builder's, so the two providers
// express "no pods" identically.
const datadogNoPodsSentinel = "__no_pods_resolved__"

// datadogPodNameClause renders the pod list as one OR group, which is the form that
// can sit beside the AND-joined scope filters.
func datadogPodNameClause(pods []string) string {
	terms := make([]string, len(pods))
	for i, pod := range pods {
		terms[i] = "pod_name:" + pod
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

// datadogCollapseOverTime reduces each series to one point, over time, for the
// aggregates the query could not be asked for.
//
// Deliberately not computePercentileFromSeries, which reduces ACROSS series at each
// timestamp — a different question that happens to share the name "p90". This is the
// axis ooPercentile implements for OpenObserve, and the one rightsizing means: how
// hot did THIS pod get.
//
// `count` is reduced here too, and for a different reason: Datadog can answer it, but
// in its own buckets. See metrics_aggregate_count.go for the unit the contract picks.
func datadogCollapseOverTime(results []QueryResult, meta RequestMetadata) {
	if meta.Aggregate.Name == "" {
		return
	}
	rollup := float64(datadogPerPodRollupSeconds(meta, int64(datadogRollupSeconds(meta.RangeWindow))))
	step := aggregateStepSeconds(meta)

	for i := range results {
		// How often this metric is submitted, derived from the answer rather than
		// assumed: a bucket that was fully covered holds rollup/interval samples, so
		// the busiest bucket anywhere in the response gives the interval. Reading it
		// from the data means no constant to keep in step with an agent's config, and
		// it is per result because two keys can be submitted at different rates.
		samplePeriod := datadogSamplePeriod(results[i], rollup)

		// Every key collapses here now. Nothing is pushed down as a bucket the width
		// of the window, and Datadog's source reads neither `instant` nor `step`, so
		// everything arrives as a range whatever the caller asked for.
		_, isPerPod := datadogPerPodKeys[UtilisationKey(results[i].QueryKey)]
		for j := range results[i].Payload {
			series := &results[i].Payload[j]
			if len(series.Timestamps) == 0 {
				continue
			}
			var value float64
			if isPerPod && meta.Aggregate.Name == AggregateCount {
				// Each bucket's value is how many raw samples it holds, so their SUM is
				// the number of readings the pod actually produced. Multiplying by the
				// interval between readings turns that into seconds observed, and
				// dividing by Step makes it comparable to Prometheus's
				// count_over_time -- and to the floor the caller checks it against.
				value = stepEquivalentCount(sumFinite(series.Values), samplePeriod, step)
			} else {
				v, ok := datadogReduceOverTime(series.Values, meta.Aggregate)
				if !ok {
					// Every sample was a gap — normal for a pod that lived through part of
					// the window. Still exactly one point, because that is what Aggregate
					// promises and what callers reading payload[0] expect, carrying NaN
					// (which marshals to null) rather than a zero that would read as idle.
					v = math.NaN()
				}
				value = v
			}
			last := series.Timestamps[len(series.Timestamps)-1]
			series.Timestamps, series.Values = []int64{last}, []float64{value}
		}
	}
}

// datadogReduceOverTime applies the requested aggregate to one series' own values.
//
// Datadog can push a plain aggregate down as a rollup, and does so for the per-pod
// keys where the response would otherwise be enormous. Every other key arrives as a
// range whatever was asked for, so the collapse happens here — otherwise `aggregate`
// silently means nothing on Datadog for those keys, and a consumer reading payload[0]
// gets the oldest point in the window instead of the statistic it asked for.
//
// Reports false when the series holds no finite value, which the caller turns into a
// single NaN rather than a zero.
func datadogReduceOverTime(values []float64, aggregate ParsedAggregate) (float64, bool) {
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

// datadogNoteUnanswerableKeys gives each requested key Datadog cannot answer an
// empty result carrying the reason, rather than leaving it out of the response.
//
// Silence and "used nothing" look identical to a caller, and rightsizing acts on the
// difference: a missing high-water mark must not read as a workload that never
// allocated.
func datadogNoteUnanswerableKeys(results []QueryResult, requested []string) []QueryResult {
	for _, key := range requested {
		reason, unanswerable := datadogUnanswerableKeys[UtilisationKey(key)]
		if !unanswerable {
			continue
		}
		results = append(results, QueryResult{QueryKey: key, Note: reason})
	}
	return results
}

// sumFinite totals the readable values, ignoring gaps.
//
// A gap arrives as NaN and is not an observation: counting it would credit a pod with
// coverage over an interval it did not exist in, which is the opposite of what the floor
// is asked to detect. Zero IS a measurement and contributes, which matters here because
// an idle container reports zero rather than nothing.
func sumFinite(values []float64) float64 {
	var total float64
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			total += v
		}
	}
	return total
}

// datadogSamplePeriod is the seconds between raw submissions, derived from the response.
//
// Under a `count` rollup a saturated bucket holds rollup/interval samples, so the
// largest bucket in the whole result gives the interval. Taking the maximum across every
// series matters: one pod may have lived only a fraction of a bucket, and sizing on that
// would overstate the interval and so overstate everyone's coverage.
//
// Falls back to the rollup width when nothing usable came back, which reproduces the old
// bucket-counting behaviour rather than inventing a number.
func datadogSamplePeriod(result QueryResult, rollup float64) float64 {
	var busiest float64
	for _, series := range result.Payload {
		for _, v := range series.Values {
			if !math.IsNaN(v) && !math.IsInf(v, 0) && v > busiest {
				busiest = v
			}
		}
	}
	if busiest < 1 || rollup <= 0 {
		// Falling back to the bucket width reproduces the old bucket-counting answer,
		// which is a defensible number -- but reaching here means the values were not
		// the sample counts they should be. That happened once already: the nanocore
		// conversion had scaled them by 1e-9, and this branch turned a 7-million-fold
		// error into a plausible-looking one. Say so rather than absorb it.
		slog.Warn("datadog: count values are not sample counts, falling back to bucket width",
			"busiest", busiest, "rollup", rollup)
		return rollup
	}
	period := rollup / busiest
	if period < 1 {
		return 1
	}
	return period
}
