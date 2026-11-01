package observability

import (
	"fmt"
	"math"
	"nudgebee/services/security"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenObserve cluster-utilisation query builders.
//
// OpenObserve serves PromQL on /api/{org}/prometheus/api/v1/query_range, so most of the
// Prometheus builders in prometheus_queries.go render for it unchanged -- the workload and
// node keys (cpu_usage, memory_usage, the request/limit pairs, the http/disk/network family)
// are plain sum()/rate() over a single selector and evaluate correctly.
//
// The CLUSTER aggregation keys do not, because OpenObserve's PromQL engine is a subset.
// Measured against OpenObserve with the families present and populated:
//
//	sum(A) or sum(B)   with B empty  -> EMPTY   (real PromQL returns A)
//	sum(A) or sum(B)   both present  -> works
//	sum(A - B), sum(A) - sum(B)      -> EMPTY   (binary vector arithmetic)
//	quantile(0.5, ...)               -> EMPTY   (aggregation-over-series)
//	subquery [range:step] with `or`  -> HTTP 400 "Unsupported subquery"
//
// Every cluster key in buildPrometheusWorkloadQueries trips at least one of these: each
// offers the NudgeBee agent's node_resources_* spelling as an `or` fallback, and the memory
// keys are expressed as Total - Available. The non-falling-back `or` is the worst of them --
// it means the fallback arm silently blanks the primary arm.
//
// So the cluster keys are served here instead: the `or` alternatives become separate query
// keys coalesced in Go, the subtraction happens in Go, and the percentiles are computed over
// the fetched series rather than asked of the engine. Families are DISCOVERED by taking the
// first candidate that returns series -- the same rule openobserve_workload_metrics.go states
// for this provider, and for the same reason: what lands in OpenObserve depends entirely on
// the collector writing to it, so assuming a convention renders an empty chart that reads as
// "idle" rather than "queried the wrong family".

// openObserveClusterCandidate is one convention a cluster-level node metric may be published
// under. Ordering decides only which wins when an instance carries several.
type openObserveClusterCandidate struct {
	Family string
	// Counter marks a cumulative series that must be rate()d to mean anything. The OTel
	// node gauges are already expressed in cores/bytes.
	Counter bool
	// ExcludeIdle restricts to busy CPU time on families that break usage down by mode.
	ExcludeIdle bool
	// Matchers narrow a family that packs several quantities into one metric name.
	// The OTel receiver publishes both network directions as k8s_pod_network_io and
	// separates them with a `direction` label, where cAdvisor uses two metric names.
	Matchers []string
	// Increase asks for the counter's RISE across each bucket rather than its rate.
	// A bytes-per-bucket key is not a bytes-per-second key, and answering one with the
	// other is wrong by exactly the bucket width.
	Increase bool
}

// openObserveClusterRole is the abstract quantity a candidate list resolves.
type openObserveClusterRole string

const (
	ooRoleCPUCapacity openObserveClusterRole = "cpu_capacity"
	ooRoleCPUUsage    openObserveClusterRole = "cpu_usage"
	ooRoleMemTotal    openObserveClusterRole = "mem_total"
	ooRoleMemAvail    openObserveClusterRole = "mem_available"
	ooRoleMemUsed     openObserveClusterRole = "mem_used"
	ooRoleNetRx       openObserveClusterRole = "net_receive"
	ooRoleNetTx       openObserveClusterRole = "net_transmit"
)

// OpenObserveClusterCandidates is the probe order per role. Package-level so tests can
// override it. node-exporter/cAdvisor first (the most common Kubernetes scrape), then the
// NudgeBee agent's own spellings, then the OTel kubeletstats receiver.
var OpenObserveClusterCandidates = map[openObserveClusterRole][]openObserveClusterCandidate{
	ooRoleCPUCapacity: {
		{Family: "machine_cpu_cores"},
		{Family: "node_resources_cpu_logical_cores"},
		{Family: "k8s_node_allocatable_cpu"},
	},
	ooRoleCPUUsage: {
		{Family: "node_cpu_seconds_total", Counter: true, ExcludeIdle: true},
		{Family: "node_resources_cpu_usage_seconds_total", Counter: true, ExcludeIdle: true},
		{Family: "k8s_node_cpu_usage"},
	},
	// Both spellings of the node-exporter memory families are probed because OpenObserve
	// stores each metric as a stream and normalises the stream name to lower case: an
	// instance fed node_memory_MemTotal_bytes answers only to node_memory_memtotal_bytes.
	// These are the only families in the utilisation set carrying capitals, which is why
	// memory can come back empty on an instance whose CPU metrics resolve perfectly.
	ooRoleMemTotal: {
		{Family: "node_memory_MemTotal_bytes"},
		{Family: "node_memory_memtotal_bytes"},
		{Family: "node_resources_memory_total_bytes"},
		{Family: "k8s_node_allocatable_memory"},
	},
	ooRoleMemAvail: {
		{Family: "node_memory_MemAvailable_bytes"},
		{Family: "node_memory_memavailable_bytes"},
		{Family: "node_resources_memory_available_bytes"},
	},
	ooRoleMemUsed: {
		{Family: "k8s_node_memory_usage"},
		{Family: "node_resources_memory_used_bytes"},
	},
	// cAdvisor gives each direction its own metric name; the OTel receiver gives both
	// one name and a `direction` label. Probing in this order means an instance scraping
	// cAdvisor needs no translation, and one fed by OTel is translated automatically --
	// neither has to be configured, and an instance carrying both gets the name it
	// already agrees with Prometheus on.
	ooRoleNetRx: {
		{Family: "container_network_receive_bytes_total", Increase: true},
		{Family: "k8s_pod_network_io", Increase: true, Matchers: []string{`direction="receive"`}},
	},
	ooRoleNetTx: {
		{Family: "container_network_transmit_bytes_total", Increase: true},
		{Family: "k8s_pod_network_io", Increase: true, Matchers: []string{`direction="transmit"`}},
	},
}

// openObserveClusterKeys are the utilisation keys served by this file rather than by the
// Prometheus builders, each mapped to the roles it needs resolved. Everything not listed
// here still goes through those builders.
//
// The mapping keeps the probe batch proportional to what was actually asked for. The card
// fetches in three separate calls (cpu, memory, percentiles), so probing every role on each
// one would put three times the necessary query load on the customer's OpenObserve.
//
// Memory usage names all three memory roles because it is satisfied either by a direct
// usage family or by Total - Available.
var openObserveClusterKeys = map[UtilisationKey][]openObserveClusterRole{
	KeyCPUTotal:      {ooRoleCPUCapacity},
	KeyCPUReal:       {ooRoleCPUUsage},
	KeyCPUUsageTrend: {ooRoleCPUUsage},
	KeyP50CPU:        {ooRoleCPUUsage},
	KeyP90CPU:        {ooRoleCPUUsage},
	KeyMaxUsageCPU:   {ooRoleCPUUsage},
	KeyMemTotal:      {ooRoleMemTotal},
	KeyMemReal:       {ooRoleMemUsed, ooRoleMemTotal, ooRoleMemAvail},
	KeyMemUsageTrend: {ooRoleMemUsed, ooRoleMemTotal, ooRoleMemAvail},
	KeyP50Mem:        {ooRoleMemUsed, ooRoleMemTotal, ooRoleMemAvail},
	KeyP90Mem:        {ooRoleMemUsed, ooRoleMemTotal, ooRoleMemAvail},
	KeyMaxUsageMem:   {ooRoleMemUsed, ooRoleMemTotal, ooRoleMemAvail},
	// Bytes per bucket, so they are trends rather than scalars.
	KeyNetworkReceiveBytesIncrease:  {ooRoleNetRx},
	KeyNetworkTransmitBytesIncrease: {ooRoleNetTx},
}

// ooBaseQueryKey namespaces the internal probe keys so they cannot collide with a caller's
// metric key in the merged response.
func ooBaseQueryKey(role openObserveClusterRole, i int) string {
	return fmt.Sprintf("__oo_%s_%d", role, i)
}

// buildOpenObserveClusterQuery renders one candidate. Each is a single aggregated selector --
// no `or`, no arithmetic, no subquery -- which is the subset OpenObserve evaluates correctly.
func buildOpenObserveClusterQuery(c openObserveClusterCandidate, rateWindow string) string {
	var matchers []string
	if c.ExcludeIdle {
		matchers = append(matchers, `mode!="idle"`)
	}
	matchers = append(matchers, c.Matchers...)
	selector := fmt.Sprintf("%s{%s}", c.Family, strings.Join(matchers, ","))
	switch {
	case c.Increase:
		return fmt.Sprintf("sum(increase(%s[%s]))", selector, rateWindow)
	case c.Counter:
		return fmt.Sprintf("sum(rate(%s[%s]))", selector, rateWindow)
	}
	return fmt.Sprintf("sum(%s)", selector)
}

// fetchOpenObserveUtilisation splits the request: cluster aggregation keys are served by the
// OpenObserve-safe path below, everything else by the Prometheus builders, which OpenObserve
// evaluates correctly. Results from both are merged under the caller's original metric keys.
func fetchOpenObserveUtilisation(ctx *security.RequestContext, req GetUtilisationTrendRequest, meta RequestMetadata, integrationSource string, instant bool) (OutputMetricQuery, error) {
	var clusterKeys, perPodKeys, passthrough []string
	for _, m := range meta.RequestedMetrics {
		switch {
		case openObserveClusterKeys[UtilisationKey(m)] != nil:
			clusterKeys = append(clusterKeys, m)
		// Served natively because the METRIC is absent under the name the Prometheus
		// builders use, not because the engine cannot evaluate their query.
		case ooServesPerPod(m):
			perPodKeys = append(perPodKeys, m)
		default:
			passthrough = append(passthrough, m)
		}
	}

	out := OutputMetricQuery{Results: []QueryResult{}}

	if len(passthrough) > 0 {
		var queries map[string]string
		if meta.Kind == "node" {
			queries = buildPrometheusNodeQueries(meta, passthrough)
		} else {
			queries = buildPrometheusWorkloadQueries(meta, passthrough)
		}
		res, err := fetchOpenObserveFallbackRounds(ctx, req, integrationSource, queries, instant)
		if err != nil {
			return out, err
		}
		out.Results = append(out.Results, res...)
	}

	if len(perPodKeys) > 0 {
		res, err := fetchOpenObservePerPod(ctx, req, meta, integrationSource, perPodKeys)
		if err != nil {
			return out, err
		}
		out.Results = append(out.Results, res...)
	}

	if len(clusterKeys) > 0 {
		res, err := fetchOpenObserveClusterAggregates(ctx, req, meta, integrationSource, clusterKeys)
		if err != nil {
			return out, err
		}
		out.Results = append(out.Results, res...)
	}

	return out, nil
}

// fetchOpenObserveClusterAggregates resolves each role's family in one batch, then derives
// every requested cluster key from the resolved series in Go.
func fetchOpenObserveClusterAggregates(ctx *security.RequestContext, req GetUtilisationTrendRequest, meta RequestMetadata, integrationSource string, keys []string) ([]QueryResult, error) {
	rateWindow := meta.RateWindow
	if rateWindow == "" {
		rateWindow = "300s"
	}

	// Only the roles the requested keys actually need. Probing serially would cost a round
	// trip per family, so every candidate of those roles goes in one batch; the queries are
	// cheap aggregates and OpenObserve answers an absent family with an empty result rather
	// than an error.
	needed := map[openObserveClusterRole]bool{}
	for _, key := range keys {
		for _, role := range openObserveClusterKeys[UtilisationKey(key)] {
			needed[role] = true
		}
	}

	queries := map[string]string{}
	for role := range needed {
		for i, c := range OpenObserveClusterCandidates[role] {
			queries[ooBaseQueryKey(role, i)] = buildOpenObserveClusterQuery(c, rateWindow)
		}
	}

	out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
		AccountId:            req.AccountId,
		MetricProvider:       "openobserve",
		MetricProviderSource: integrationSource,
		Queries:              queries,
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
		StepInterval:         openObserveClusterStep(meta, req),
	})
	if err != nil {
		return nil, err
	}

	byKey := map[string]QueryResult{}
	for _, r := range out.Results {
		byKey[r.QueryKey] = r
	}

	// First candidate that actually returned points wins the role.
	resolved := map[openObserveClusterRole]ooSeries{}
	probed := map[openObserveClusterRole][]string{}
	for role := range needed {
		for i, c := range OpenObserveClusterCandidates[role] {
			probed[role] = append(probed[role], c.Family)
			r, ok := byKey[ooBaseQueryKey(role, i)]
			if !ok || r.Error != nil || len(r.Payload) == 0 {
				continue
			}
			if _, already := resolved[role]; already {
				continue
			}
			if s := ooSeriesFrom(r.Payload[0]); len(s.Values) > 0 {
				s.Query = r.Query
				resolved[role] = s
			}
		}
	}

	// Memory usage is published directly by some collectors and as Total - Available by
	// others. Prefer the direct family; otherwise subtract pointwise in Go, because
	// OpenObserve returns empty for vector arithmetic.
	memUsed, haveMemUsed := resolved[ooRoleMemUsed]
	if !haveMemUsed {
		total, okT := resolved[ooRoleMemTotal]
		avail, okA := resolved[ooRoleMemAvail]
		if okT && okA {
			q := fmt.Sprintf("%s - %s", total.Query, avail.Query)
			memUsed = ooSubtract(total, avail)
			memUsed.Query = q
			haveMemUsed = len(memUsed.Values) > 0
		}
	}

	netRx, haveNetRx := resolved[ooRoleNetRx]
	netTx, haveNetTx := resolved[ooRoleNetTx]

	cpuUsage, haveCPUUsage := resolved[ooRoleCPUUsage]
	cpuCap, haveCPUCap := resolved[ooRoleCPUCapacity]
	memTotal, haveMemTotal := resolved[ooRoleMemTotal]

	results := make([]QueryResult, 0, len(keys))
	for _, key := range keys {
		switch UtilisationKey(key) {
		case KeyCPUTotal:
			results = append(results, ooScalar(key, cpuCap, haveCPUCap, ooLast, probed[ooRoleCPUCapacity]))
		case KeyCPUReal:
			// Prometheus renders this as rate() over the whole picker range, i.e. the
			// window's mean usage. The mean of the per-step rate series is the same figure.
			results = append(results, ooScalar(key, cpuUsage, haveCPUUsage, ooMean, probed[ooRoleCPUUsage]))
		case KeyP50CPU:
			results = append(results, ooScalar(key, cpuUsage, haveCPUUsage, ooPercentile(0.50), probed[ooRoleCPUUsage]))
		case KeyP90CPU:
			results = append(results, ooScalar(key, cpuUsage, haveCPUUsage, ooPercentile(0.90), probed[ooRoleCPUUsage]))
		case KeyMaxUsageCPU:
			results = append(results, ooScalar(key, cpuUsage, haveCPUUsage, ooMax, probed[ooRoleCPUUsage]))
		case KeyNetworkReceiveBytesIncrease:
			results = append(results, ooTrend(key, netRx, haveNetRx, probed[ooRoleNetRx]))
		case KeyNetworkTransmitBytesIncrease:
			results = append(results, ooTrend(key, netTx, haveNetTx, probed[ooRoleNetTx]))
		case KeyCPUUsageTrend:
			results = append(results, ooTrend(key, cpuUsage, haveCPUUsage, probed[ooRoleCPUUsage]))
		case KeyMemTotal:
			results = append(results, ooScalar(key, memTotal, haveMemTotal, ooLast, probed[ooRoleMemTotal]))
		case KeyMemReal:
			results = append(results, ooScalar(key, memUsed, haveMemUsed, ooLast, ooMemUsedProbed(probed)))
		// The Prometheus form asks quantile() ACROSS NODES at a single instant, which is not
		// what the card's "P50 Usage" label means and is unsupported here besides. These are
		// percentiles OVER TIME of cluster usage -- the same thing the CPU keys compute.
		case KeyP50Mem:
			results = append(results, ooScalar(key, memUsed, haveMemUsed, ooPercentile(0.50), ooMemUsedProbed(probed)))
		case KeyP90Mem:
			results = append(results, ooScalar(key, memUsed, haveMemUsed, ooPercentile(0.90), ooMemUsedProbed(probed)))
		case KeyMaxUsageMem:
			results = append(results, ooScalar(key, memUsed, haveMemUsed, ooMax, ooMemUsedProbed(probed)))
		case KeyMemUsageTrend:
			results = append(results, ooTrend(key, memUsed, haveMemUsed, ooMemUsedProbed(probed)))
		}
	}
	return results, nil
}

// ooMemUsedProbed lists every family memory usage could have come from, for the
// no-family-matched error.
func ooMemUsedProbed(probed map[openObserveClusterRole][]string) []string {
	out := append([]string{}, probed[ooRoleMemUsed]...)
	out = append(out, probed[ooRoleMemTotal]...)
	return append(out, probed[ooRoleMemAvail]...)
}

// openObserveClusterStep keeps the derived percentiles at the resolution the Prometheus
// subquery form would have used.
//
// meta.Step is a PromQL duration string. promAggWindow emits seconds ("60s") today, but
// any Go/PromQL duration is legitimate there -- parsing only the "<n>s" form would make a
// later switch to "5m" fall through to the default and silently halve the resolution.
// The bare-integer fallback covers callers that set the field without a unit.
func openObserveClusterStep(meta RequestMetadata, req GetUtilisationTrendRequest) int {
	if meta.Step != "" {
		if d, err := time.ParseDuration(meta.Step); err == nil && d > 0 {
			return int(d.Seconds())
		}
		if v, err := strconv.Atoi(meta.Step); err == nil && v > 0 {
			return v
		}
	}
	if req.StepInterval > 0 {
		return req.StepInterval
	}
	return 60
}

// ooSeries is one resolved cluster timeseries.
type ooSeries struct {
	Timestamps []int64
	Values     []float64
	Query      string
}

func ooSeriesFrom(r Result) ooSeries {
	return ooSeries{Timestamps: r.Timestamps, Values: r.Values}
}

// ooSubtract pairs the two series by timestamp. A collector can publish total and available
// on different scrape cadences, so index-wise subtraction would silently pair mismatched
// instants.
func ooSubtract(a, b ooSeries) ooSeries {
	idx := make(map[int64]float64, len(b.Timestamps))
	for i, ts := range b.Timestamps {
		if i < len(b.Values) {
			idx[ts] = b.Values[i]
		}
	}
	out := ooSeries{}
	for i, ts := range a.Timestamps {
		if i >= len(a.Values) {
			break
		}
		if v, ok := idx[ts]; ok {
			out.Timestamps = append(out.Timestamps, ts)
			out.Values = append(out.Values, a.Values[i]-v)
		}
	}
	return out
}

// Reducers turning a resolved series into the single figure a gauge row shows.
func ooLast(s ooSeries) float64 { return s.Values[len(s.Values)-1] }

func ooMax(s ooSeries) float64 {
	m := math.Inf(-1)
	for _, v := range s.Values {
		if v > m {
			m = v
		}
	}
	return m
}

func ooMean(s ooSeries) float64 {
	var sum float64
	for _, v := range s.Values {
		sum += v
	}
	return sum / float64(len(s.Values))
}

// ooPercentile is the linear-interpolation percentile over time, matching the
// quantile_over_time() the Prometheus path asks the engine for.
func ooPercentile(p float64) func(ooSeries) float64 {
	return func(s ooSeries) float64 {
		vals := append([]float64{}, s.Values...)
		sort.Float64s(vals)
		if len(vals) == 1 {
			return vals[0]
		}
		pos := p * float64(len(vals)-1)
		lo := int(math.Floor(pos))
		hi := int(math.Ceil(pos))
		if hi >= len(vals) {
			return vals[len(vals)-1]
		}
		return vals[lo] + (vals[hi]-vals[lo])*(pos-float64(lo))
	}
}

// ooScalar renders a reduced figure as a one-point series, which is what the card reads
// (it takes the last sample of whatever comes back). When no family resolved it returns an
// explicit error rather than a zero -- a zeroed gauge is indistinguishable from an idle
// cluster, which is the failure this provider is most prone to.
func ooScalar(key string, s ooSeries, ok bool, reduce func(ooSeries) float64, probed []string) QueryResult {
	if !ok || len(s.Values) == 0 {
		return ooNoFamily(key, probed)
	}
	ts := s.Timestamps[len(s.Timestamps)-1]
	return QueryResult{
		QueryKey: key,
		Query:    s.Query,
		Payload:  []Result{{Metric: map[string]string{}, Timestamps: []int64{ts}, Values: []float64{reduce(s)}}},
	}
}

func ooTrend(key string, s ooSeries, ok bool, probed []string) QueryResult {
	if !ok || len(s.Values) == 0 {
		return ooNoFamily(key, probed)
	}
	return QueryResult{
		QueryKey: key,
		Query:    s.Query,
		Payload:  []Result{{Metric: map[string]string{}, Timestamps: s.Timestamps, Values: s.Values}},
	}
}

func ooNoFamily(key string, probed []string) QueryResult {
	msg := fmt.Sprintf("openobserve: no metric family carries %q (probed: %s)", key, strings.Join(probed, ", "))
	return QueryResult{QueryKey: key, Error: &msg}
}
