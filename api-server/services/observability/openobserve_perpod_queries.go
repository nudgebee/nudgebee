package observability

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"nudgebee/services/security"
)

// Per-pod utilisation keys for OpenObserve.
//
// The cluster keys next door are served here rather than by the Prometheus builders
// because OpenObserve's PromQL is a subset. The per-pod keys are served here for a
// different reason: the METRIC does not exist under the name those builders use.
//
// OpenObserve is fed by the OTel collector's kubeletstats receiver, which publishes
// `container_cpu_usage` and `container_memory_working_set` -- already gauges, in cores
// and bytes -- where cAdvisor publishes `container_cpu_usage_seconds_total`, a counter
// needing rate(). So this is not a name substitution: swapping the family into the
// cAdvisor query would rate() a gauge and answer near-zero on every pod.
//
// Which family is present depends entirely on what writes to the instance, so they are
// DISCOVERED rather than assumed -- the same rule the cluster path states, for the same
// reason: guessing renders an empty chart that reads as "idle" rather than as "asked for
// a family nobody publishes".

// openObservePerPodCandidate is one convention a container-level metric may arrive under.
type openObservePerPodCandidate struct {
	Family string
	// Counter marks a cumulative series that must be rate()d. The OTel kubeletstats
	// gauges are already in final units; cAdvisor's totals are not.
	Counter bool
	// Labels are this family's spelling of {namespace, pod, container}. cAdvisor uses the
	// bare names, the OTel receiver uses the k8s semantic-convention ones, and grouping by
	// the wrong set silently collapses every pod into one series.
	Namespace, Pod, Container string
}

type openObservePerPodRole string

const (
	ooRolePodCPUUsage openObservePerPodRole = "pod_cpu_usage"
	ooRolePodMemWSet  openObservePerPodRole = "pod_mem_working_set"
)

// OpenObservePerPodCandidates is the probe order per role. cAdvisor first, because an
// instance scraping it answers the same names Prometheus would and needs no translation;
// then the OTel receiver's spelling. Package-level so tests can override it.
var OpenObservePerPodCandidates = map[openObservePerPodRole][]openObservePerPodCandidate{
	ooRolePodCPUUsage: {
		{Family: "container_cpu_usage_seconds_total", Counter: true,
			Namespace: "namespace", Pod: "pod", Container: "container"},
		{Family: "container_cpu_usage",
			Namespace: "k8s_namespace_name", Pod: "k8s_pod_name", Container: "k8s_container_name"},
	},
	ooRolePodMemWSet: {
		{Family: "container_memory_working_set_bytes",
			Namespace: "namespace", Pod: "pod", Container: "container"},
		{Family: "container_memory_working_set",
			Namespace: "k8s_namespace_name", Pod: "k8s_pod_name", Container: "k8s_container_name"},
		// Last resort. `usage` counts page cache the working set excludes, so it reads
		// HIGH -- the safe direction for a limit recommendation, and named in the note so
		// nobody mistakes it for a working set.
		{Family: "container_memory_usage",
			Namespace: "k8s_namespace_name", Pod: "k8s_pod_name", Container: "k8s_container_name"},
	},
}

// ooAcross is how a key reduces the per-pod series ACROSS pods at each timestamp.
//
// Six workload keys are the same two measurements asked differently: the workload total,
// its busiest pod, the per-pod breakdown, and how many pods reported. Fetching the per-pod
// series once and reducing here means they cannot disagree with each other, which they can
// when each is its own query.
type ooAcross string

const (
	ooAcrossSum     ooAcross = "sum"      // the workload total
	ooAcrossMax     ooAcross = "max"      // the busiest pod at each instant
	ooAcrossCount   ooAcross = "count"    // how many pods reported
	ooAcrossPerPod  ooAcross = "per_pod"  // one series per pod, container folded in
	ooAcrossPerCont ooAcross = "per_cont" // one series per pod AND container
)

// openObservePerPodKeys maps each key this file serves to the role that answers it and
// how it reduces across pods. The reductions are taken from what Prometheus returns for
// the same key, measured rather than assumed: cpu_usage is one unlabelled series,
// cpu_usage_by_pod carries namespace+pod but NOT container, and pod_count is a count.
var openObservePerPodKeys = map[UtilisationKey]struct {
	Role   openObservePerPodRole
	Across ooAcross
}{
	KeyContainerCPUUsageByPod:         {ooRolePodCPUUsage, ooAcrossPerCont},
	KeyContainerMemoryWorkingSetByPod: {ooRolePodMemWSet, ooAcrossPerCont},
	KeyCPUUsage:                       {ooRolePodCPUUsage, ooAcrossSum},
	KeyMemoryUsage:                    {ooRolePodMemWSet, ooAcrossSum},
	KeyCPUUsageMaxPod:                 {ooRolePodCPUUsage, ooAcrossMax},
	KeyMemoryUsageMaxPod:              {ooRolePodMemWSet, ooAcrossMax},
	KeyCPUUsageByPod:                  {ooRolePodCPUUsage, ooAcrossPerPod},
	KeyMemoryUsageByPod:               {ooRolePodMemWSet, ooAcrossPerPod},
	KeyPodCount:                       {ooRolePodCPUUsage, ooAcrossCount},
}

// ooPerPodQueryKey namespaces a probe so it cannot collide with a caller's metric key.
func ooPerPodQueryKey(role openObservePerPodRole, i int) string {
	return fmt.Sprintf("__oo_pp_%s_%d", role, i)
}

// buildOpenObservePerPodQuery renders one candidate, scoped and grouped in that family's
// own label spelling. A single aggregated selector -- no `or`, no arithmetic, no subquery,
// which is the subset OpenObserve evaluates correctly.
func buildOpenObservePerPodQuery(c openObservePerPodCandidate, meta RequestMetadata, rateWindow string) string {
	var matchers []string
	if meta.Namespace != "" {
		matchers = append(matchers, fmt.Sprintf(`%s=%q`, c.Namespace, meta.Namespace))
	}
	// An exact pod list beats the workload prefix: it is the caller's own answer, and the
	// prefix matches a sibling workload whose name extends this one.
	if meta.PodNamesSupplied && len(meta.PodNames) > 0 {
		matchers = append(matchers, fmt.Sprintf(`%s=~%q`, c.Pod, strings.Join(meta.PodNames, "|")))
	} else if meta.Name != "" {
		matchers = append(matchers, fmt.Sprintf(`%s=~%q`, c.Pod, meta.Name+"-.*"))
	}
	if meta.ContainerName != "" {
		matchers = append(matchers, fmt.Sprintf(`%s=%q`, c.Container, meta.ContainerName))
	}
	selector := fmt.Sprintf("%s{%s}", c.Family, strings.Join(matchers, ","))

	grouping := fmt.Sprintf("by (%s, %s, %s)", c.Namespace, c.Pod, c.Container)
	if c.Counter {
		return fmt.Sprintf("sum %s (rate(%s[%s]))", grouping, selector, rateWindow)
	}
	return fmt.Sprintf("sum %s (%s)", grouping, selector)
}

// fetchOpenObservePerPod resolves each requested per-pod key by probing its role's
// candidates and taking the first that returns series.
//
// A role nobody publishes comes back EMPTY WITH A NOTE naming what was probed, never
// silence: a caller reading only the payload cannot tell an unpublished family from a
// workload that used nothing, and one of those is a bug while the other is a fact.
func fetchOpenObservePerPod(ctx *security.RequestContext, req GetUtilisationTrendRequest,
	meta RequestMetadata, integrationSource string, keys []string) ([]QueryResult, error) {
	rateWindow := meta.RateWindow
	if rateWindow == "" {
		rateWindow = "300s"
	}

	needed := map[openObservePerPodRole]bool{}
	for _, key := range keys {
		if spec, ok := openObservePerPodKeys[UtilisationKey(key)]; ok {
			needed[spec.Role] = true
		}
	}

	queries := map[string]string{}
	for role := range needed {
		for i, c := range OpenObservePerPodCandidates[role] {
			queries[ooPerPodQueryKey(role, i)] = buildOpenObservePerPodQuery(c, meta, rateWindow)
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}

	out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:            req.AccountId,
		MetricProvider:       "openobserve",
		MetricProviderSource: integrationSource,
		Queries:              queries,
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
		StepInterval:         req.StepInterval,
		// Deliberately NOT an instant query when an aggregate is asked for. An instant
		// query answers with the value at the window's end, which is not a maximum, a
		// percentile, or any other statistic -- it just looks like one, because exactly
		// one number comes back. The aggregate is applied below, over the fetched
		// series, the same way the cluster path computes its percentiles.
		//
		// This was a real bug here for one revision: max and avg returned IDENTICAL
		// values 98.6% below the window's actual peak, and every shape check passed.
	})
	if err != nil {
		return nil, err
	}

	byKey := map[string]QueryResult{}
	for _, r := range out.Results {
		byKey[r.QueryKey] = r
	}

	resolved := map[openObservePerPodRole]QueryResult{}
	probed := map[openObservePerPodRole][]string{}
	for role := range needed {
		for i, c := range OpenObservePerPodCandidates[role] {
			probed[role] = append(probed[role], c.Family)
			r, ok := byKey[ooPerPodQueryKey(role, i)]
			if !ok || r.Error != nil || len(r.Payload) == 0 {
				continue
			}
			if _, already := resolved[role]; !already {
				resolved[role] = r
			}
		}
	}

	results := make([]QueryResult, 0, len(keys))
	for _, key := range keys {
		spec, ok := openObservePerPodKeys[UtilisationKey(key)]
		if !ok {
			continue
		}
		role := spec.Role
		r, found := resolved[role]
		if !found {
			note := fmt.Sprintf("OpenObserve published none of the families this key is collected from (%s)",
				strings.Join(probed[role], ", "))
			results = append(results, QueryResult{QueryKey: key, Payload: []Result{}, Note: note})
			continue
		}
		payload := ooAcrossSeries(r.Payload, spec.Across)
		if meta.Aggregate.Name != "" {
			payload = ooReducePerPod(payload, meta.Aggregate)
		}
		results = append(results, QueryResult{
			QueryKey: key,
			Query:    r.Query,
			Payload:  payload,
		})
	}
	return results, nil
}

// ooReducePerPod collapses each series to the one point an Aggregate promises.
//
// Done here rather than in PromQL because the wrap would be a subquery, and OpenObserve
// rejects a subquery whose inner expression matches nothing -- turning "this pod has no
// data" into a hard error. Reducing in Go keeps an empty series empty.
//
// datadogReduceOverTime is reused rather than reimplemented: the reduction is arithmetic
// over a slice, with nothing Datadog-specific in it, and a second copy would be a second
// place for `max` and `avg` to drift apart.
func ooReducePerPod(series []Result, agg ParsedAggregate) []Result {
	out := make([]Result, 0, len(series))
	for _, s := range series {
		// A series with no points has nothing to reduce, and no timestamp to anchor a
		// point to. Emitting one value against zero timestamps leaves the two slices
		// mismatched, which breaks any caller that zips them -- and the empty (not nil)
		// slices keep it serialising as [] rather than null. datadogCollapseOverTime
		// guards the same case a few files over; this reducer was missing it.
		if len(s.Timestamps) == 0 || len(s.Values) == 0 {
			out = append(out, Result{Metric: s.Metric, Timestamps: []int64{}, Values: []float64{}})
			continue
		}
		v, ok := datadogReduceOverTime(s.Values, agg)
		if !ok {
			// Every sample was a gap. Still exactly one point, carrying NaN rather than
			// a zero that a caller would read as a measured idle pod.
			v = math.NaN()
		}
		ts := s.Timestamps
		if len(ts) > 0 {
			ts = []int64{ts[len(ts)-1]}
		}
		out = append(out, Result{Metric: s.Metric, Timestamps: ts, Values: []float64{v}})
	}
	return out
}

// ooServesPerPod reports whether this file answers a key.
func ooServesPerPod(key string) bool {
	_, ok := openObservePerPodKeys[UtilisationKey(key)]
	return ok
}

// ooAcrossSeries reduces the per-pod series ACROSS pods at each timestamp.
//
// Deliberately across series and not over time: that is the axis these keys differ on.
// cpu_usage is the workload's total AT each instant, and summing over time instead would
// answer a question nobody asked with a number that still plots.
//
// Timestamps are aligned by value, not by index. Two pods' series can start at different
// instants -- one was created later -- and zipping them positionally pairs a pod's first
// sample with another's tenth.
func ooAcrossSeries(series []Result, across ooAcross) []Result {
	switch across {
	case ooAcrossPerCont:
		return series
	case ooAcrossPerPod:
		return ooFoldContainers(series)
	}

	byTS := map[int64][]float64{}
	for _, s := range series {
		for i, ts := range s.Timestamps {
			if i >= len(s.Values) {
				continue
			}
			v := s.Values[i]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			byTS[ts] = append(byTS[ts], v)
		}
	}
	if len(byTS) == 0 {
		return []Result{}
	}
	stamps := make([]int64, 0, len(byTS))
	for ts := range byTS {
		stamps = append(stamps, ts)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i] < stamps[j] })

	out := Result{Metric: map[string]string{}, Timestamps: stamps, Values: make([]float64, 0, len(stamps))}
	for _, ts := range stamps {
		vals := byTS[ts]
		var v float64
		switch across {
		case ooAcrossSum:
			for _, x := range vals {
				v += x
			}
		case ooAcrossMax:
			v = vals[0]
			for _, x := range vals[1:] {
				if x > v {
					v = x
				}
			}
		case ooAcrossCount:
			v = float64(len(vals))
		}
		out.Values = append(out.Values, v)
	}
	return []Result{out}
}

// ooFoldContainers sums a pod's containers into one series per pod.
//
// cpu_usage_by_pod carries namespace and pod but NOT container -- measured against what
// Prometheus returns for the same key. Leaving the container label on would make a
// two-container pod two series, and a consumer keying on pod would pair one container's
// value against the other's.
func ooFoldContainers(series []Result) []Result {
	type podKey struct{ ns, pod string }
	order := []podKey{}
	byPod := map[podKey]map[int64]float64{}
	for _, s := range series {
		// Both spellings, because this runs BEFORE canonicaliseUtilisationLabels: at this
		// point an OTel-collected series still says k8s_pod_name. Reading only the
		// canonical name found "" on every series and folded the whole workload into one
		// -- which still plotted, as the workload's total under a per-pod key's name.
		k := podKey{ooLabel(s.Metric, "namespace", "k8s_namespace_name"),
			ooLabel(s.Metric, "pod", "k8s_pod_name")}
		if _, seen := byPod[k]; !seen {
			byPod[k] = map[int64]float64{}
			order = append(order, k)
		}
		for i, ts := range s.Timestamps {
			if i >= len(s.Values) {
				continue
			}
			if v := s.Values[i]; !math.IsNaN(v) && !math.IsInf(v, 0) {
				byPod[k][ts] += v
			}
		}
	}
	out := make([]Result, 0, len(order))
	for _, k := range order {
		stamps := make([]int64, 0, len(byPod[k]))
		for ts := range byPod[k] {
			stamps = append(stamps, ts)
		}
		sort.Slice(stamps, func(i, j int) bool { return stamps[i] < stamps[j] })
		vals := make([]float64, 0, len(stamps))
		for _, ts := range stamps {
			vals = append(vals, byPod[k][ts])
		}
		out = append(out, Result{
			Metric:     map[string]string{"namespace": k.ns, "pod": k.pod},
			Timestamps: stamps, Values: vals,
		})
	}
	return out
}

// ooLabel reads the first spelling a series actually carries.
//
// This file runs before label canonicalisation, so a cAdvisor-collected series says
// `pod` and an OTel one says `k8s_pod_name`. Whichever answered the candidate probe is
// the one present, and the grouping has to work for both.
func ooLabel(m map[string]string, names ...string) string {
	for _, n := range names {
		if v := m[n]; v != "" {
			return v
		}
	}
	return ""
}
