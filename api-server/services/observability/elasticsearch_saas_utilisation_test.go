package observability

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonPath walks a marshalled query body by map keys, so assertions read as the
// path they check rather than a chain of type assertions.
func jsonPath(t *testing.T, body map[string]any, path ...string) any {
	t.Helper()
	var cur any = body
	for _, p := range path {
		m, ok := cur.(map[string]any)
		require.Truef(t, ok, "path %v: %q is not an object", path, p)
		cur, ok = m[p]
		require.Truef(t, ok, "path %v: %q missing", path, p)
	}
	return cur
}

func bodyAsMap(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestESUtilisationScope(t *testing.T) {
	// Cluster scope is the case the previous isNode boolean got wrong: no namespace,
	// no name, no node is the Account Overview panel, and it was routed to the
	// container branch.
	assert.Equal(t, esScopeCluster, esUtilisationScope(RequestMetadata{}))
	assert.Equal(t, esScopeNode, esUtilisationScope(RequestMetadata{Kind: "node"}))
	assert.Equal(t, esScopeNode, esUtilisationScope(RequestMetadata{NodeName: "ip-10-0-0-1"}))
	assert.Equal(t, esScopeWorkload, esUtilisationScope(RequestMetadata{Namespace: "shop", Name: "web", Kind: "deployment"}))
	assert.Equal(t, esScopePod, esUtilisationScope(RequestMetadata{Namespace: "shop", Name: "web-abc-123", Kind: "pod"}))
}

// Keys are grouped so one search answers several, which is only sound when the grouped
// keys would have sent the identical body. The grouping key used to be a hand-listed
// summary of esMetricSource fields, and it omitted Agg and WorkloadScoped — two keys
// differing only in those would have been merged, and one answered by the other's
// query. Keying on the rendered body removes the whole class; this holds it: any two
// keys that render the same body must agree on every field that shapes the query.
func TestGroupedKeysRenderIdenticalQueryBodies(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "web", Kind: "deployment"}
	bodies := map[string]string{}
	for _, key := range allESUtilKeys {
		plan, ok := esUtilisationMetric(key, esScopeWorkload, false)
		if !ok {
			continue
		}
		raw, err := json.Marshal(buildESUtilisationAggQuery(plan, meta, esScopeWorkload, 0, 3600000, 60))
		require.NoError(t, err)
		body := string(raw)
		for otherKey, otherBody := range bodies {
			if body != otherBody {
				continue
			}
			otherPlan, _ := esUtilisationMetric(otherKey, esScopeWorkload, false)
			require.Equal(t, len(plan.Sources), len(otherPlan.Sources),
				"%q and %q render one body but have different sources", key, otherKey)
			for i := range plan.Sources {
				assert.Equalf(t, otherPlan.Sources[i].Agg, plan.Sources[i].Agg,
					"%q and %q share a query but disagree on Agg", key, otherKey)
				assert.Equalf(t, otherPlan.Sources[i].WorkloadScoped, plan.Sources[i].WorkloadScoped,
					"%q and %q share a query but disagree on WorkloadScoped", key, otherKey)
			}
		}
		bodies[key] = body
	}
}

// At pod scope the requested name is a pod, and the Prometheus path labels that series
// "pod". Labelling it "workload" instead both misnames it and drops the label consumers
// key on — the app's dashboards read metric['pod'] to tie a series to its pod, so an
// ES-backed account would have found nothing there.
func TestPodScopeLabelsTheSeriesWithThePodName(t *testing.T) {
	src := esMetricSource{Field: "kubernetes.pod.memory.working_set.bytes"}

	pod := esUtilSeriesLabels(src, RequestMetadata{Namespace: "shop", Name: "web-abc-123", Kind: "pod"})
	assert.Equal(t, "web-abc-123", pod["pod"])
	assert.NotContains(t, pod, "workload")

	// Workload scope is unchanged: there the name really is the workload's.
	wl := esUtilSeriesLabels(src, RequestMetadata{Namespace: "shop", Name: "web", Kind: "deployment"})
	assert.Equal(t, "web", wl["workload"])
	assert.NotContains(t, wl, "pod")
}

// esSourceAgg builds a one- or two-level grouping and indexes GroupBy[1] for the
// second. A source with a different shape would either panic or, if handled by
// grouping on something arbitrary, answer confidently with a wrong number. The
// source table is a package literal, so the invariant is checkable here — this test
// is what keeps the `default: return nil` arm of that switch unreachable in practice.
func TestEverySourceGroupsByOneOrTwoFields(t *testing.T) {
	for _, key := range allESUtilKeys {
		for _, scope := range []esUtilScope{esScopeCluster, esScopeNode, esScopeWorkload, esScopePod} {
			for _, containerNamed := range []bool{false, true} {
				plan, ok := esUtilisationMetric(key, scope, containerNamed)
				if !ok {
					continue
				}
				for i, src := range plan.Sources {
					assert.Containsf(t, []int{1, 2}, len(src.GroupBy),
						"key %q scope %v source %d groups by %d fields", key, scope, i, len(src.GroupBy))
				}
			}
		}
	}
}

// allESUtilKeys is every key esUtilisationMetric answers, kept beside the mapping it
// mirrors so a new key that forgets its sources is caught by the invariant sweep.
var allESUtilKeys = []string{
	"cpu_usage", "cpu_usage_pod", "cpu_real", "cpu_usage_trend",
	"memory_usage", "mem_real", "mem_usage_trend",
	"disk_total", "disk_used",
	"replica_defined", "replica_ready",
	"cpu_usage_line", "memory_usage_line",
	"p50_cpu", "p90_cpu", "max_usage_cpu",
	"p50_mem", "p90_mem", "max_usage_mem",
	"cpu_request", "cpu_request_pod", "cpu_limit", "cpu_limit_pod",
	"memory_request", "memory_limit",
	"cpu_total", "mem_total",
	"network_receive_packet", "network_transmit_packets",
	"pvc_usage", "pvc_requests",
}

// Every key the utilisation panel asks for must resolve. Twelve of these returned
// an empty payload before, which is what put "-" in the panel's rows.
func TestESUtilisationMetricCoversPanelKeys(t *testing.T) {
	clusterKeys := []string{
		"cpu_real", "cpu_total", "cpu_request", "cpu_limit",
		"mem_real", "mem_total", "memory_limit", "memory_request",
		"p90_mem", "p90_cpu", "p50_mem", "p50_cpu", "max_usage_mem", "max_usage_cpu",
	}
	for _, key := range clusterKeys {
		plan, ok := esUtilisationMetric(key, esScopeCluster, false)
		assert.Truef(t, ok, "cluster key %q unresolved", key)
		assert.NotEmptyf(t, plan.Sources, "cluster key %q has no sources", key)
	}

	workloadKeys := []string{
		"cpu_usage", "memory_usage", "cpu_request", "cpu_limit", "memory_request", "memory_limit",
		"network_receive_packet", "network_transmit_packets",
	}
	for _, key := range workloadKeys {
		plan, ok := esUtilisationMetric(key, esScopeWorkload, false)
		assert.Truef(t, ok, "workload key %q unresolved", key)
		assert.NotEmptyf(t, plan.Sources, "workload key %q has no sources", key)
	}

	// A key with no Elasticsearch equivalent stays unresolved so the caller can say
	// so, rather than returning an empty payload that reads as "no data".
	_, ok := esUtilisationMetric("http_latency_p95", esScopeWorkload, false)
	assert.False(t, ok)
}

// Pod and container metricsets spell the same quantity differently. Getting this
// wrong costs nothing at compile time and everything at runtime.
func TestESMemorySourceFieldSpelling(t *testing.T) {
	plan, ok := esUtilisationMetric("memory_usage", esScopeWorkload, false)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(plan.Sources), 2)
	assert.Equal(t, "kubernetes.pod.memory.working_set.bytes", plan.Sources[0].Field)
	assert.Equal(t, "kubernetes.container.memory.workingset.bytes", plan.Sources[1].Field)
}

// Naming a container promotes the container-level source ahead of the pod-level one,
// because a pod document cannot be narrowed to one container.
func TestESSourceOrderPrefersContainerWhenNamed(t *testing.T) {
	plan, ok := esUtilisationMetric("cpu_usage", esScopeWorkload, true)
	require.True(t, ok)
	assert.Equal(t, "kubernetes.container.cpu.usage.nanocores", plan.Sources[0].Field)

	plan, ok = esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	require.True(t, ok)
	assert.Equal(t, "kubernetes.pod.cpu.usage.nanocores", plan.Sources[0].Field)
}

func TestBuildESUtilisationAggQueryWorkloadCPU(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "web", Kind: "deployment"}
	plan, ok := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	require.True(t, ok)

	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeWorkload, 1000, 2000, 300))

	// Aggregation-only: the raw-hit form this replaces capped at size 10000 and
	// silently lost the tail of a busy window.
	assert.EqualValues(t, 0, body["size"])

	filters := jsonPath(t, body, "aggs", "src_0", "filter", "bool", "filter").([]any)
	var haveExists, haveNamespace, havePrefix bool
	for _, f := range filters {
		m := f.(map[string]any)
		if ex, ok := m["exists"].(map[string]any); ok && ex["field"] == "kubernetes.pod.cpu.usage.nanocores" {
			haveExists = true
		}
		if term, ok := m["term"].(map[string]any); ok && term["kubernetes.namespace"] == "shop" {
			haveNamespace = true
		}
		if pre, ok := m["prefix"].(map[string]any); ok && pre["kubernetes.pod.name"] == "web-" {
			havePrefix = true
		}
	}
	// The exists clause is what selects the dataset: only pod-metricset documents
	// carry this field, so no metricset.name term is needed.
	assert.True(t, haveExists, "exists filter on the value field selects the dataset")
	assert.True(t, haveNamespace)
	assert.True(t, havePrefix, "workload scope matches every pod of the workload by prefix")

	assert.Equal(t, "300s", jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "date_histogram", "fixed_interval"))
	assert.Equal(t, "kubernetes.pod.name",
		jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "g0", "terms", "field"))
	// Summing per-series aggregates, not raw documents: a pod reporting several
	// samples inside one bucket must count once.
	assert.Equal(t, "g0>v",
		jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "total", "sum_bucket", "buckets_path"))

	// Every candidate layout is evaluated in the same request.
	aggs := jsonPath(t, body, "aggs").(map[string]any)
	assert.Len(t, aggs, len(plan.Sources))
}

// Pod scope matches one pod exactly; the workload prefix would also match
// "web-abc-123-something".
func TestBuildESUtilisationAggQueryPodScopeUsesExactTerm(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "web-abc-123", Kind: "pod"}
	plan, _ := esUtilisationMetric("cpu_usage", esScopePod, false)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopePod, 1000, 2000, 300))

	filters := jsonPath(t, body, "aggs", "src_0", "filter", "bool", "filter").([]any)
	var exact bool
	for _, f := range filters {
		if term, ok := f.(map[string]any)["term"].(map[string]any); ok && term["kubernetes.pod.name"] == "web-abc-123" {
			exact = true
		}
	}
	assert.True(t, exact)
}

// Spec metrics group by pod AND container, which must be nested terms rather than
// multi_terms (OpenSearch only gained multi_terms in 2.1), with the outer group
// summing its inner groups.
//
// Node-scope behaviour is covered by TestNodeScopeDropsSourcesWithNoNodeDimension:
// these sources have no node dimension, so a node-scoped request drops them entirely
// rather than answering with a cluster total.
func TestBuildESUtilisationAggQuerySpecUsesNestedTerms(t *testing.T) {
	plan, ok := esUtilisationMetric("memory_request", esScopeCluster, false)
	require.True(t, ok)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, RequestMetadata{}, esScopeCluster, 1000, 2000, 300))

	assert.Equal(t, "kubernetes.pod.name",
		jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "g0", "terms", "field"))
	assert.Equal(t, "kubernetes.container.name",
		jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "g0", "aggs", "g1", "terms", "field"))
	assert.Equal(t, "g0>gsum",
		jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "total", "sum_bucket", "buckets_path"))
}

// Elasticsearch rejects a derivative whose parent histogram drops empty buckets:
// "parent histogram of derivative aggregation must have min_doc_count of 0".
// Verified against a live cluster on 2026-08-27.
func TestBuildESUtilisationAggQueryCounterNeedsZeroMinDocCount(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "web", Kind: "deployment"}
	plan, ok := esUtilisationMetric("network_receive_packet", esScopeWorkload, false)
	require.True(t, ok)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeWorkload, 1000, 2000, 300))

	assert.EqualValues(t, 0, jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "date_histogram", "min_doc_count"))
	assert.Equal(t, "total", jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "rate", "derivative", "buckets_path"))
	assert.Equal(t, "1s", jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "aggs", "rate", "derivative", "unit"))
}

// Non-counter histograms keep min_doc_count 1 so a gap in the data stays a gap
// rather than becoming a run of zeroes in the chart.
func TestBuildESUtilisationAggQueryGaugeKeepsMinDocCountOne(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, RequestMetadata{}, esScopeWorkload, 1000, 2000, 300))
	assert.EqualValues(t, 1, jsonPath(t, body, "aggs", "src_0", "aggs", "ts", "date_histogram", "min_doc_count"))
}

func TestESUtilStepSeconds(t *testing.T) {
	// The picker-derived step wins, so ES buckets line up with what the Prometheus
	// path would have produced for the same range.
	assert.EqualValues(t, 300, esUtilStepSeconds(RequestMetadata{Step: "300s"}, 0, 0))
	// Fallbacks: floor 60s, ceiling 1800s.
	assert.EqualValues(t, 60, esUtilStepSeconds(RequestMetadata{}, 0, 3_600_000))
	assert.EqualValues(t, 1800, esUtilStepSeconds(RequestMetadata{}, 0, 30*24*3_600_000))
	assert.EqualValues(t, 300, esUtilStepSeconds(RequestMetadata{}, 100, 100))
}

// esAggFixture builds a response body with the given per-source aggregation blobs.
func esAggFixture(t *testing.T, sources ...string) []byte {
	t.Helper()
	body := `{"aggregations":{`
	for i, s := range sources {
		if i > 0 {
			body += ","
		}
		body += `"` + esSourceAggName(i) + `":` + s
	}
	return []byte(body + `}}`)
}

const esEmptySource = `{"doc_count":0,"ts":{"buckets":[]}}`

func esBucketsSource(docCount int, buckets string) string {
	return `{"doc_count":` + itoa(docCount) + `,"ts":{"buckets":[` + buckets + `]}}`
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// Nanocores are the stored unit for every Metricbeat CPU field; the product's unit
// is cores. A missing conversion is off by a factor of a billion and still renders.
func TestParseESUtilisationAggsScalesNanocores(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := esAggFixture(t, esBucketsSource(214,
		`{"key":1787803200000,"total":{"value":2500000000.0}},{"key":1787803500000,"total":{"value":1000000000.0}}`))

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{Namespace: "shop", Name: "web"})
	results, srcIdx, matched := out.Series, out.SourceIdx, out.DocsMatched
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 0, srcIdx)
	assert.EqualValues(t, 214, matched)
	assert.Equal(t, []float64{2.5, 1.0}, results[0].Values)
	// Timestamps are epoch seconds, matching every other metric provider.
	assert.Equal(t, []int64{1787803200, 1787803500}, results[0].Timestamps)
	assert.Equal(t, "kubernetes.pod.cpu.usage.nanocores", results[0].Metric["__name__"])
	assert.Equal(t, "shop", results[0].Metric["namespace"])
	assert.Equal(t, "web", results[0].Metric["workload"])
}

// An empty first candidate must fall through to the next layout rather than
// returning "no data" — this is the whole point of evaluating them together.
func TestParseESUtilisationAggsFallsThroughToNextSource(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := esAggFixture(t,
		esEmptySource,
		esBucketsSource(11, `{"key":1787803200000,"total":{"value":3000000000.0}}`),
		esEmptySource,
	)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results, srcIdx := out.Series, out.SourceIdx
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, srcIdx, "the container-level source answered")
	assert.Equal(t, []float64{3.0}, results[0].Values)
	assert.Equal(t, "kubernetes.container.cpu.usage.nanocores", results[0].Metric["__name__"])
}

// A source that matched documents but produced no usable bucket value is a shape
// that did not fit, not data. It must not shadow a later candidate that does fit.
func TestParseESUtilisationAggsSkipsSourceWithOnlyNullTotals(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := esAggFixture(t,
		esBucketsSource(5, `{"key":1787803200000,"total":{"value":null}}`),
		esBucketsSource(9, `{"key":1787803200000,"total":{"value":4000000000.0}}`),
	)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results, srcIdx, matched := out.Series, out.SourceIdx, out.DocsMatched
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, srcIdx)
	assert.Equal(t, []float64{4.0}, results[0].Values)
	// Documents seen while walking past the unusable source still count as matched,
	// so the "no series" note can report the real search volume.
	assert.EqualValues(t, 14, matched)
}

func TestParseESUtilisationAggsReducesPercentile(t *testing.T) {
	plan, _ := esUtilisationMetric("p90_cpu", esScopeCluster, false)
	body := []byte(`{"aggregations":{"src_0":{"doc_count":290,
	  "ts":{"buckets":[{"key":1787803200000,"total":{"value":1000000000.0}},
	                   {"key":1787803500000,"total":{"value":9000000000.0}}]},
	  "reduced_p90":{"values":{"90.0":8500000000.0}}}}}`)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results := out.Series
	require.NoError(t, err)
	require.Len(t, results, 1)
	// One point, not a series: the gauge row shows a single number.
	assert.Equal(t, []float64{8.5}, results[0].Values)
	assert.Equal(t, []int64{1787803500}, results[0].Timestamps, "stamped at the last bucket")
}

func TestParseESUtilisationAggsReducesMax(t *testing.T) {
	plan, _ := esUtilisationMetric("max_usage_mem", esScopeCluster, false)
	body := []byte(`{"aggregations":{"src_0":{"doc_count":290,
	  "ts":{"buckets":[{"key":1787803200000,"total":{"value":100.0}}]},
	  "reduced_max":{"value":12898543616.0,"keys":["2026-08-27T06:05:00.000Z"]}}}}`)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results := out.Series
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []float64{12898543616.0}, results[0].Values)
}

// Counters read the derivative, and a reset or pod churn makes it negative. A
// negative byte rate would drag down the average the abandoned-workload scan
// computes, making a busy workload look idle.
func TestParseESUtilisationAggsCounterRateClampsNegative(t *testing.T) {
	plan, _ := esUtilisationMetric("network_receive_packet", esScopeWorkload, false)
	body := []byte(`{"aggregations":{"src_0":{"doc_count":214,"ts":{"buckets":[
	  {"key":1787803200000,"total":{"value":4266054325.0}},
	  {"key":1787803500000,"total":{"value":4275552552.0},"rate":{"normalized_value":31660.8}},
	  {"key":1787803800000,"total":{"value":10.0},"rate":{"normalized_value":-14251807.0}}]}}}}`)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results := out.Series
	require.NoError(t, err)
	require.Len(t, results, 1)
	// First bucket has no derivative yet and is skipped, not read as zero.
	assert.Equal(t, []float64{31660.8, 0}, results[0].Values)
}

func TestParseESUtilisationAggsNoDataReturnsNothing(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	out, err := parseESUtilisationAggs(
		esAggFixture(t, esEmptySource, esEmptySource, esEmptySource), plan, RequestMetadata{})
	results, srcIdx, matched := out.Series, out.SourceIdx, out.DocsMatched
	require.NoError(t, err)
	assert.Nil(t, results)
	assert.Equal(t, -1, srcIdx)
	assert.EqualValues(t, 0, matched)

	// The note must name what was searched: an unexplained empty payload is the
	// defect this path replaces.
	note := esUtilNoDataNote(plan, 0)
	assert.Contains(t, note, "kubernetes.pod.cpu.usage.nanocores")
	assert.Contains(t, note, "kubernetes.container.cpu.usage.nanocores")
}

func TestESUtilUnsupportedNoteNamesScope(t *testing.T) {
	note := esUtilUnsupportedNote("http_latency_p95", esScopeWorkload)
	assert.Contains(t, note, "http_latency_p95")
	assert.Contains(t, note, "workload")
}

// The OTLP / Data Prepper layout stays a candidate, so tenants already on it keep
// working — with their own attribute paths, which are not ECS's.
func TestESUtilisationKeepsOTLPCandidate(t *testing.T) {
	plan, ok := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	require.True(t, ok)
	// Located by name, not by position: candidates get appended over time, and an
	// assertion on "the last source" breaks every time a layout is added.
	var otlp *esMetricSource
	for i := range plan.Sources {
		if plan.Sources[i].Name == "container.cpu.usage" {
			otlp = &plan.Sources[i]
		}
	}
	require.NotNil(t, otlp, "the Data Prepper OTLP candidate must still be present")
	assert.Equal(t, "value", otlp.Field)
	assert.Equal(t, esOTLPFields.Pod, otlp.GroupBy[0])

	body := bodyAsMap(t, buildESUtilisationAggQuery(plan,
		RequestMetadata{Namespace: "shop", Name: "web"}, esScopeWorkload, 1000, 2000, 300))
	filters := jsonPath(t, body, "aggs", "src_2", "filter", "bool", "filter").([]any)
	var haveName, haveNamespace bool
	for _, f := range filters {
		if term, ok := f.(map[string]any)["term"].(map[string]any); ok {
			if term["name.keyword"] == "container.cpu.usage" {
				haveName = true
			}
			if term[esOTLPFields.Namespace] == "shop" {
				haveNamespace = true
			}
		}
	}
	assert.True(t, haveName)
	assert.True(t, haveNamespace)
}

// Node capacity prefers allocatable over capacity: allocatable is what the
// scheduler can place work on, which is what a utilisation gauge divides by.
func TestESCapacityPrefersAllocatable(t *testing.T) {
	plan, ok := esUtilisationMetric("cpu_total", esScopeCluster, false)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(plan.Sources), 2)
	// Order is the contract; the count is not — more layouts may be appended.
	assert.Equal(t, "kubernetes.node.cpu.allocatable.cores", plan.Sources[0].Field)
	assert.Equal(t, "kubernetes.node.cpu.capacity.cores", plan.Sources[1].Field)
}

// Cluster and node scope read node-level usage; workload scope reads pod-level.
// Sending a cluster request down the container branch is the bug this replaces.
func TestESUsageSourcesDifferByScope(t *testing.T) {
	cluster, _ := esUtilisationMetric("cpu_real", esScopeCluster, false)
	assert.Equal(t, "kubernetes.node.cpu.usage.nanocores", cluster.Sources[0].Field)

	workload, _ := esUtilisationMetric("cpu_real", esScopeWorkload, false)
	assert.Equal(t, "kubernetes.pod.cpu.usage.nanocores", workload.Sources[0].Field)
}

// A real customer's Elastic Agent stream (gd-ehq) enables pod/container/state_pod/
// state_node but NOT the `node` metricset, so cluster usage has no direct source and
// every usage key came back empty through the full stack. Pod usage is the fallback:
// lower than true node usage by the system overhead outside pod cgroups, but a usable
// gauge instead of a blank one.
func TestClusterUsageFallsBackToPodMetricsetWhenNodeMetricsetIsAbsent(t *testing.T) {
	for _, tc := range []struct{ key, direct, fallback string }{
		{"cpu_real", "kubernetes.node.cpu.usage.nanocores", "kubernetes.pod.cpu.usage.nanocores"},
		{"mem_real", "kubernetes.node.memory.usage.bytes", "kubernetes.pod.memory.working_set.bytes"},
	} {
		plan, ok := esUtilisationMetric(tc.key, esScopeCluster, false)
		require.Truef(t, ok, "%s unresolved", tc.key)
		require.GreaterOrEqualf(t, len(plan.Sources), 2, "%s has no fallback", tc.key)
		// The direct node source stays first so clusters that do ship it are unaffected.
		assert.Equal(t, tc.direct, plan.Sources[0].Field)
		assert.Equal(t, tc.fallback, plan.Sources[1].Field)
	}
}

// OTel-native indices (the ES exporter's `mapping.mode: otel`) key metrics by name
// under `metrics` and dimensions under `resource.attributes` — a third layout, and
// one the utilisation path did not know until it was added alongside the others.
// parseESMetricsHitsWithStats has read it since the metrics explorer shipped, so a
// cluster on that layout would otherwise have hit the same blank dashboard.
func TestESUtilisationCarriesAnOTelNativeCandidate(t *testing.T) {
	for _, tc := range []struct {
		key   string
		scope esUtilScope
		field string
	}{
		{"cpu_usage", esScopeWorkload, "metrics.k8s.pod.cpu.usage"},
		{"memory_usage", esScopeWorkload, "metrics.k8s.pod.memory.working_set"},
		{"cpu_total", esScopeCluster, "metrics.k8s.node.allocatable_cpu"},
		{"mem_total", esScopeCluster, "metrics.k8s.node.allocatable_memory"},
		{"cpu_request", esScopeWorkload, "metrics.k8s.container.cpu_request"},
		{"memory_limit", esScopeWorkload, "metrics.k8s.container.memory_limit"},
	} {
		plan, ok := esUtilisationMetric(tc.key, tc.scope, false)
		require.Truef(t, ok, "%s unresolved", tc.key)
		var found bool
		for _, src := range plan.Sources {
			if src.Field == tc.field {
				found = true
				// Dimensions come from resource.attributes on this layout, not from
				// the ECS `kubernetes.*` paths.
				assert.Equal(t, esOTelNativeFields, src.Fields, tc.key)
			}
		}
		assert.Truef(t, found, "%s has no OTel-native candidate for %s", tc.key, tc.field)
	}
}

// The ECS candidates must stay ahead of the OTel-native ones: a cluster shipping both
// (dev ships three layouts into one pattern) should keep answering from the layout it
// answered from before this candidate existed.
func TestOTelNativeCandidateRanksAfterECS(t *testing.T) {
	plan, ok := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	require.True(t, ok)
	var ecsIdx, nativeIdx = -1, -1
	for i, src := range plan.Sources {
		if src.Field == "kubernetes.pod.cpu.usage.nanocores" {
			ecsIdx = i
		}
		if src.Field == "metrics.k8s.pod.cpu.usage" {
			nativeIdx = i
		}
	}
	require.NotEqual(t, -1, ecsIdx)
	require.NotEqual(t, -1, nativeIdx)
	assert.Less(t, ecsIdx, nativeIdx)
}

// Requests and limits have no node dimension on any layout, so the node filter must
// stay off the OTel-native spec candidate too.
func TestOTelNativeSpecCandidateSkipsNodeFilter(t *testing.T) {
	plan, ok := esUtilisationMetric("memory_request", esScopeNode, false)
	require.True(t, ok)
	for _, src := range plan.Sources {
		assert.Truef(t, src.NoNodeFilter, "spec source %s must not be node-filtered", src.Field)
	}
}

// Coverage decides, not declaration order. When a customer enables a metricset partway
// through a window the preferred source holds a handful of points and the fallback
// holds the whole range; first-wins would chart the handful and call it the answer.
func TestParseESUtilisationAggsPrefersTheSourceWithBetterCoverage(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := esAggFixture(t,
		// Preferred (pod) source: only two buckets — the metricset was just turned on.
		esBucketsSource(5, `{"key":1787803200000,"total":{"value":1000000000.0}},
		                    {"key":1787803500000,"total":{"value":1000000000.0}}`),
		// Fallback (container) source: the whole window.
		esBucketsSource(90, `{"key":1787802600000,"total":{"value":4000000000.0}},
		                     {"key":1787802900000,"total":{"value":4000000000.0}},
		                     {"key":1787803200000,"total":{"value":4000000000.0}},
		                     {"key":1787803500000,"total":{"value":4000000000.0}}`),
	)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results, srcIdx := out.Series, out.SourceIdx
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, srcIdx, "the fuller series must win")
	assert.Len(t, results[0].Values, 4)
	assert.Equal(t, "kubernetes.container.cpu.usage.nanocores", results[0].Metric["__name__"])
}

// Equal coverage is the normal case — including a cluster dual-shipping two layouts —
// and there the declared preference order must still decide, unchanged.
func TestParseESUtilisationAggsKeepsPreferenceOrderOnEqualCoverage(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	same := `{"key":1787803200000,"total":{"value":1000000000.0}},{"key":1787803500000,"total":{"value":2000000000.0}}`
	body := esAggFixture(t, esBucketsSource(5, same), esBucketsSource(90, same))

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	srcIdx := out.SourceIdx
	require.NoError(t, err)
	assert.Equal(t, 0, srcIdx, "ties go to the preferred candidate")
}

// Documents matched is not coverage: a shape that did not fit can match thousands of
// documents and still produce nothing chartable, and must not outrank a source that
// produced real points.
func TestParseESUtilisationAggsIgnoresDocCountWhenRankingCoverage(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := esAggFixture(t,
		esBucketsSource(100000, `{"key":1787803200000,"total":{"value":null}}`),
		esBucketsSource(3, `{"key":1787803200000,"total":{"value":7000000000.0}}`),
	)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results, srcIdx := out.Series, out.SourceIdx
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, srcIdx)
	assert.Equal(t, []float64{7.0}, results[0].Values)
}

// esUtilQueryKey renders the search body a key would send, which is exactly the key
// fetchESMetricUtilisation groups on. Tests assert sharing through the same value the
// production path uses, so a change that splits or merges searches shows up here.
func esUtilQueryKey(t *testing.T, key string) string {
	t.Helper()
	plan, ok := esUtilisationMetric(key, esScopeCluster, false)
	require.Truef(t, ok, "%s unresolved", key)
	raw, err := json.Marshal(buildESUtilisationAggQuery(plan, RequestMetadata{}, esScopeCluster, 0, 3600000, 300))
	require.NoError(t, err)
	return string(raw)
}

// Keys that differ only by reduction resolve to the same sources and must therefore
// share one search. Four CPU keys and four memory keys collapsing to one query each
// is the difference between 14 and 6 aggregations per panel load.
func TestKeysDifferingOnlyByReductionShareASignature(t *testing.T) {
	sig := func(key string) string { return esUtilQueryKey(t, key) }
	cpu := sig("cpu_real")
	for _, k := range []string{"p50_cpu", "p90_cpu", "max_usage_cpu"} {
		assert.Equalf(t, cpu, sig(k), "%s should share cpu_real's query", k)
	}
	mem := sig("mem_real")
	for _, k := range []string{"p50_mem", "p90_mem", "max_usage_mem"} {
		assert.Equalf(t, mem, sig(k), "%s should share mem_real's query", k)
	}
	// Genuinely different quantities must NOT collapse together.
	assert.NotEqual(t, cpu, mem)
	assert.NotEqual(t, cpu, sig("cpu_total"))
	assert.NotEqual(t, sig("cpu_request"), sig("cpu_limit"))
}

// The panel's fourteen keys must reduce to eight distinct searches: the four CPU
// keys collapse to one and the four memory keys to one, while cpu_total, mem_total
// and the four request/limit keys each read a different FIELD and so genuinely need
// their own search. (Those four share a dataset and filters and could be folded into
// one multi-field search — a further 8 -> 5 — but that needs the builder to carry
// several value fields per source, which this does not.)
func TestPanelKeysCollapseToEightSearches(t *testing.T) {
	keys := []string{
		"cpu_real", "cpu_total", "cpu_request", "cpu_limit",
		"mem_real", "mem_total", "memory_request", "memory_limit",
		"p50_cpu", "p90_cpu", "max_usage_cpu", "p50_mem", "p90_mem", "max_usage_mem",
	}
	sigs := map[string]bool{}
	for _, k := range keys {
		sigs[esUtilQueryKey(t, k)] = true
	}
	assert.Len(t, sigs, 8, "14 keys should need only 8 searches")
}

// Every query now carries all three reductions, which is what makes sharing possible.
func TestQueryCarriesAllThreeReductions(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_real", esScopeCluster, false)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, RequestMetadata{}, esScopeCluster, 1000, 2000, 300))
	aggs := jsonPath(t, body, "aggs", "src_0", "aggs").(map[string]any)
	for _, want := range []string{"reduced_p50", "reduced_p90", "reduced_max"} {
		assert.Containsf(t, aggs, want, "missing %s", want)
	}
}

// One response, read once per member with that member's own reduction.
func TestOneResponseServesEachReduction(t *testing.T) {
	body := []byte(`{"aggregations":{"src_0":{"doc_count":290,
	  "ts":{"buckets":[{"key":1787803200000,"total":{"value":1000000000.0}},
	                   {"key":1787803500000,"total":{"value":9000000000.0}}]},
	  "reduced_p50":{"values":{"50.0":2000000000.0}},
	  "reduced_p90":{"values":{"90.0":8500000000.0}},
	  "reduced_max":{"value":9000000000.0}}}}`)

	for _, tc := range []struct {
		key  string
		want float64
	}{
		{"p50_cpu", 2.0}, {"p90_cpu", 8.5}, {"max_usage_cpu", 9.0},
	} {
		plan, ok := esUtilisationMetric(tc.key, esScopeCluster, false)
		require.True(t, ok)
		out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
		results := out.Series
		require.NoError(t, err)
		require.Lenf(t, results, 1, tc.key)
		assert.Equalf(t, []float64{tc.want}, results[0].Values, tc.key)
	}

	// And the un-reduced key reads the series from that same response.
	plan, _ := esUtilisationMetric("cpu_real", esScopeCluster, false)
	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results := out.Series
	require.NoError(t, err)
	assert.Equal(t, []float64{1.0, 9.0}, results[0].Values)
}

// The nodes list asks for cpu_usage_line / memory_usage_line and matches each series
// to a node row. Unmapped, they hit the "no equivalent" branch, which returns a note
// WITHOUT issuing a search — the nodes page showed 0% and the logs showed no ES query
// at all, which is what made it look like a data problem rather than a mapping gap.
func TestNodeUsageLineKeysResolveAsPerNodeBreakdown(t *testing.T) {
	for _, key := range []string{"cpu_usage_line", "memory_usage_line"} {
		plan, ok := esUtilisationMetric(key, esScopeCluster, false)
		require.Truef(t, ok, "%s must resolve", key)
		assert.Truef(t, plan.Breakdown, "%s must return per-node series, not a sum", key)
		require.NotEmpty(t, plan.Sources)
		// Every candidate must group by node: the pod-metricset fallback groups by
		// pod and would return pod series wearing node labels.
		for _, src := range plan.Sources {
			require.Len(t, src.GroupBy, 1, key)
			assert.Containsf(t, []string{esECSFields.Node, esOTelNativeFields.Node, esOTLPFields.Node},
				src.GroupBy[0], "%s candidate %s is not node-grouped", key, src.Field)
		}
	}
}

func TestBreakdownReturnsOneLatestPointPerNode(t *testing.T) {
	plan, ok := esUtilisationMetric("cpu_usage_line", esScopeCluster, false)
	require.True(t, ok)
	// Two nodes across two buckets; the later bucket must win per node.
	body := []byte(`{"aggregations":{"src_0":{"doc_count":40,"ts":{"buckets":[
	  {"key":1787803200000,"g0":{"buckets":[
	     {"key":"node-a","v":{"value":1000000000.0}},
	     {"key":"node-b","v":{"value":2000000000.0}}]}},
	  {"key":1787803500000,"g0":{"buckets":[
	     {"key":"node-a","v":{"value":3000000000.0}},
	     {"key":"node-b","v":{"value":4000000000.0}}]}}]}}}}`)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results := out.Series
	require.NoError(t, err)
	require.Len(t, results, 2, "one series per node")

	byNode := map[string]Result{}
	for _, r := range results {
		byNode[r.Metric["node"]] = r
	}
	// Nanocores scaled to cores, latest bucket, single point (the consumer reads values[0]).
	require.Contains(t, byNode, "node-a")
	assert.Equal(t, []float64{3.0}, byNode["node-a"].Values)
	assert.Equal(t, []int64{1787803500}, byNode["node-a"].Timestamps)
	assert.Equal(t, []float64{4.0}, byNode["node-b"].Values)
	// Matched on either label by the nodes list, so both must carry the node name.
	assert.Equal(t, "node-a", byNode["node-a"].Metric["instance"])
}

// A grouped source with two levels reports its per-group sum, so a breakdown still
// yields one value per outer group rather than dropping the series.
func TestBreakdownFallsBackToGroupSum(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage_line", esScopeCluster, false)
	body := []byte(`{"aggregations":{"src_0":{"doc_count":5,"ts":{"buckets":[
	  {"key":1787803200000,"g0":{"buckets":[{"key":"node-a","gsum":{"value":5000000000.0}}]}}]}}}}`)
	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	results := out.Series
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []float64{5.0}, results[0].Values)
}

// The nodes list builds its node selector for PromQL's =~ matcher — every node joined
// as "<name>.*" with "|". Fed to an Elasticsearch term that whole string matches
// nothing, so every row rendered 0% while the single-node chart worked.
func TestNodeSelectorAcceptsAPromQLRegexAlternation(t *testing.T) {
	clause := esNodeNameClause("kubernetes.node.name", "node-a.*|node-b.*|node-c.*")
	raw, err := json.Marshal(clause)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	shoulds := jsonPath(t, got, "bool", "should").([]any)
	assert.Len(t, shoulds, 3)
	assert.EqualValues(t, 1, jsonPath(t, got, "bool", "minimum_should_match"))
	// ".*" is exactly a prefix match, and prefix is far cheaper than regexp.
	first := shoulds[0].(map[string]any)
	assert.Equal(t, "node-a", jsonPath(t, first, "prefix", "kubernetes.node.name"))
}

func TestNodeSelectorKeepsASingleLiteralAsATerm(t *testing.T) {
	clause := esNodeNameClause("kubernetes.node.name", "gke-node-1")
	raw, _ := json.Marshal(clause)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "gke-node-1", jsonPath(t, got, "term", "kubernetes.node.name"))
}

// A bare ".*" branch matches every node. Translating it to prefix:"" would match every
// document too, so a node-scoped chart would silently answer with cluster-wide totals —
// the same wrong-data failure as answering from a source that has no node dimension.
// No clause at all is the honest translation, and the caller must append nothing.
func TestNodeSelectorEmitsNoClauseWhenItCannotNarrow(t *testing.T) {
	for name, selector := range map[string]string{
		"bare match-all":            ".*",
		"match-all inside an alt":   "node-a.*|.*",
		"empty selector":            "",
		"only separators and space": " | ",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, esNodeNameClause("kubernetes.node.name", selector))
		})
	}

	src := esMetricSource{Fields: esFieldSet{Node: "kubernetes.node.name"}}
	// No nil placeholder is appended either: a nil entry would marshal into the
	// filter array as `null`, which Elasticsearch rejects outright.
	filters := esSourceScopeFilters(src, RequestMetadata{NodeName: ".*", Kind: "node"}, esScopeNode)
	assert.Nil(t, filters)
}

// state_container has no node field. Answering a node-scoped question from it returns
// the CLUSTER total — the node chart drew "CPU Limit 97 cores" for a 4-core node.
// Absent beats confidently wrong.
func TestNodeScopeDropsSourcesWithNoNodeDimension(t *testing.T) {
	meta := RequestMetadata{NodeName: "gke-node-1", Kind: "node"}
	plan, ok := esUtilisationMetric("cpu_limit", esScopeNode, false)
	require.True(t, ok)

	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeNode, 1000, 2000, 300))
	aggs := jsonPath(t, body, "aggs").(map[string]any)
	assert.Empty(t, aggs, "no candidate can answer a node-scoped request for a spec metric")

	// Cluster scope is unaffected — there the un-narrowed sum is the right answer.
	clusterBody := bodyAsMap(t, buildESUtilisationAggQuery(plan, RequestMetadata{}, esScopeCluster, 1000, 2000, 300))
	assert.NotEmpty(t, jsonPath(t, clusterBody, "aggs").(map[string]any))
}

// Usage metrics DO carry a node field, so a node-scoped request still resolves.
func TestNodeScopeKeepsNodeAwareSources(t *testing.T) {
	meta := RequestMetadata{NodeName: "gke-node-1", Kind: "node"}
	plan, ok := esUtilisationMetric("cpu_real", esScopeNode, false)
	require.True(t, ok)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeNode, 1000, 2000, 300))
	assert.NotEmpty(t, jsonPath(t, body, "aggs").(map[string]any))
}

// A truncated sum is WRONG, not merely incomplete: it reads low while looking
// authoritative. It must reach the caller, not just the log.
func TestTruncatedSumIsReportedNotSilentlyLow(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := []byte(`{"aggregations":{"src_0":{"doc_count":50,"ts":{"buckets":[
	  {"key":1787803200000,"total":{"value":1000000000.0},"g0":{"sum_other_doc_count":4200}},
	  {"key":1787803500000,"total":{"value":2000000000.0},"g0":{"sum_other_doc_count":3800}}]}}}}`)

	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	require.NoError(t, err)
	require.Len(t, out.Series, 1)
	assert.EqualValues(t, 8000, out.TruncatedDocs, "dropped docs are summed across buckets")

	note := esUtilTruncatedNote("kubernetes.pod.cpu.usage.nanocores", out.TruncatedDocs)
	assert.Contains(t, note, "INCOMPLETE")
	assert.Contains(t, note, "8000")
}

func TestUntruncatedSumReportsNoTruncation(t *testing.T) {
	plan, _ := esUtilisationMetric("cpu_usage", esScopeWorkload, false)
	body := []byte(`{"aggregations":{"src_0":{"doc_count":50,"ts":{"buckets":[
	  {"key":1787803200000,"total":{"value":1000000000.0},"g0":{"sum_other_doc_count":0}}]}}}}`)
	out, err := parseESUtilisationAggs(body, plan, RequestMetadata{})
	require.NoError(t, err)
	assert.Zero(t, out.TruncatedDocs)
}

// The cap has to clear a whole cluster's pod count, because cluster-scope requests
// and limits group by pod — not by workload.
func TestTermsCapClearsAClusterWorthOfPods(t *testing.T) {
	assert.GreaterOrEqual(t, esUtilTermsSize, 10000)
}

// Disk utilisation read empty because these keys were never mapped. The kubelet
// `node` metricset carries them; they are node-level in the Prometheus builder too,
// so cluster scope sums across nodes and node scope narrows to one.
func TestDiskKeysResolveFromNodeFilesystem(t *testing.T) {
	for _, tc := range []struct{ key, field string }{
		{"disk_total", "kubernetes.node.fs.capacity.bytes"},
		{"disk_used", "kubernetes.node.fs.used.bytes"},
	} {
		for _, scope := range []esUtilScope{esScopeCluster, esScopeNode} {
			plan, ok := esUtilisationMetric(tc.key, scope, false)
			require.Truef(t, ok, "%s at %s", tc.key, scope)
			require.NotEmpty(t, plan.Sources)
			assert.Equal(t, tc.field, plan.Sources[0].Field)
			// Node-grouped, so a node-scoped request can actually narrow to its node
			// rather than being dropped like the spec metrics are.
			assert.Equal(t, esECSFields.Node, plan.Sources[0].GroupBy[0])
			assert.False(t, plan.Sources[0].NoNodeFilter)
		}
	}
}

// Replica counts describe the controller, not its pods. Field names verified against
// the same Elastic Kubernetes integration the customer runs (state_deployment /
// state_replicaset), rather than inferred from documentation.
func TestReplicaKeysResolveFromKubeStateMetrics(t *testing.T) {
	for _, tc := range []struct{ key, deployment, replicaset string }{
		{"replica_defined", "kubernetes.deployment.replicas.desired", "kubernetes.replicaset.replicas.desired"},
		{"replica_ready", "kubernetes.deployment.replicas.available", "kubernetes.replicaset.replicas.ready"},
	} {
		plan, ok := esUtilisationMetric(tc.key, esScopeWorkload, false)
		require.Truef(t, ok, "%s must resolve", tc.key)
		require.Len(t, plan.Sources, 2)
		// Deployment first: it names the controller directly. The replicaset dataset
		// is the fallback for clusters shipping only state_replicaset.
		assert.Equal(t, tc.deployment, plan.Sources[0].Field)
		assert.Equal(t, tc.replicaset, plan.Sources[1].Field)
		for _, src := range plan.Sources {
			assert.True(t, src.WorkloadScoped, tc.key)
			// A replica count is a level, not a rate: averaging across a scale event
			// would invent a fractional replica.
			assert.Equal(t, "max", src.Agg, tc.key)
		}
	}
}

// Workload-scoped sources match the controller exactly. The pod-prefix filter would
// match nothing here — these documents carry no pod dimension at all.
func TestWorkloadScopedSourceFiltersOnControllerNotPodPrefix(t *testing.T) {
	meta := RequestMetadata{Namespace: "shop", Name: "web", Kind: "deployment"}
	plan, ok := esUtilisationMetric("replica_defined", esScopeWorkload, false)
	require.True(t, ok)
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeWorkload, 1000, 2000, 300))

	filters := jsonPath(t, body, "aggs", "src_0", "filter", "bool", "filter").([]any)
	var exactWorkload, podPrefix bool
	for _, f := range filters {
		m := f.(map[string]any)
		if term, ok := m["term"].(map[string]any); ok && term["kubernetes.deployment.name"] == "web" {
			exactWorkload = true
		}
		if _, ok := m["prefix"]; ok {
			podPrefix = true
		}
	}
	assert.True(t, exactWorkload, "must match the controller by name")
	assert.False(t, podPrefix, "must not prefix-match pod names on a workload dataset")
}

// PVC usage and size had no Elasticsearch mapping at all, so an ES-backed account got
// "unsupported" and an empty storage panel while the PromQL path answered fine.
func TestPVCMetricsResolveAndScopeToTheClaim(t *testing.T) {
	for _, key := range []string{"pvc_usage", "pvc_requests"} {
		plan, ok := esUtilisationMetric(key, esScopeWorkload, false)
		require.Truef(t, ok, "%s unresolved", key)
		require.Len(t, plan.Sources, 1)
		assert.Truef(t, plan.Sources[0].PVCScoped, "%s must be claim-scoped", key)
		assert.Equalf(t, []string{"kubernetes.persistentvolumeclaim.name"}, plan.Sources[0].GroupBy,
			"%s series is one claim", key)
	}
}

// Only PVC-backed volumes carry a claim name; configMap/projected/emptyDir mounts
// report the NODE's filesystem size. Without the exists guard they land in the same
// sum and a 6 GiB claim reads as ~96 GiB.
func TestPVCQueryExcludesNonClaimVolumes(t *testing.T) {
	plan, ok := esUtilisationMetric("pvc_usage", esScopeWorkload, false)
	require.True(t, ok)
	meta := RequestMetadata{Namespace: "loki", PVCName: "storage-loki-0"}
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeWorkload, 1000, 2000, 300))

	filters := jsonPath(t, body, "aggs", "src_0", "filter", "bool", "filter").([]any)
	var hasExists, hasTerm bool
	for _, f := range filters {
		m := f.(map[string]any)
		if e, ok := m["exists"].(map[string]any); ok && e["field"] == "kubernetes.persistentvolumeclaim.name" {
			hasExists = true
		}
		if tm, ok := m["term"].(map[string]any); ok && tm["kubernetes.persistentvolumeclaim.name"] == "storage-loki-0" {
			hasTerm = true
		}
	}
	assert.True(t, hasExists, "must require a claim name so non-PVC mounts drop out")
	assert.True(t, hasTerm, "must narrow to the requested claim")
}

// A StatefulSet's claims are {template}-{workload}-{ordinal}, so the workload name sits
// mid-string. A prefix match — what the pod path uses — finds none of them.
func TestPVCFallsBackToAContainsMatchOnWorkloadName(t *testing.T) {
	src := esMetricSource{Fields: esECSFields, PVCScoped: true}
	filters := esSourceScopeFilters(src, RequestMetadata{Namespace: "loki", Name: "loki"}, esScopeWorkload)
	raw, err := json.Marshal(filters)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"wildcard"`)
	assert.Contains(t, string(raw), `*loki*`)
	// The pod filters must not also be applied: one claim can be mounted by many pods.
	assert.NotContains(t, string(raw), "kubernetes.pod.name")
}

// A wildcard query interprets "*" and "?", so an unescaped name widens the match and can
// be made arbitrarily expensive to evaluate. Only the contains-match path is exposed —
// the pod-prefix and term filters take their value literally.
func TestPVCContainsMatchEscapesWildcardMetacharacters(t *testing.T) {
	src := esMetricSource{Fields: esECSFields, PVCScoped: true}
	filters := esSourceScopeFilters(src, RequestMetadata{Namespace: "loki", Name: "a*b?c"}, esScopeWorkload)
	raw, err := json.Marshal(filters)
	require.NoError(t, err)
	// json.Marshal doubles each backslash, so the escaped form reads as \\* here.
	assert.Contains(t, string(raw), `*a\\*b\\?c*`)
}

// A claim is not node-local, and the PVC source ignores the node filter by
// construction — answering a node-scoped ask would return the whole cluster's claims.
func TestPVCSourcesAreDroppedAtNodeScope(t *testing.T) {
	plan, ok := esUtilisationMetric("pvc_usage", esScopeNode, false)
	require.True(t, ok)
	meta := RequestMetadata{NodeName: "gke-node-1", Kind: "node"}
	body := bodyAsMap(t, buildESUtilisationAggQuery(plan, meta, esScopeNode, 1000, 2000, 300))
	assert.Empty(t, body["aggs"], "no candidate should answer a node-scoped PVC ask")
}

// meta.Name is a workload; hanging it on a claim's series mislabels it.
func TestPVCSeriesIsLabelledWithTheClaimNotTheWorkload(t *testing.T) {
	src := esMetricSource{Field: "kubernetes.volume.fs.used.bytes", Fields: esECSFields, PVCScoped: true}
	labels := esUtilSeriesLabels(src, RequestMetadata{Namespace: "loki", Name: "loki", PVCName: "storage-loki-0"})
	assert.Equal(t, "storage-loki-0", labels["persistentvolumeclaim"])
	assert.NotContains(t, labels, "workload")
	assert.NotContains(t, labels, "pod")
}

// state_container carries requests and limits but no node field. Dropping it at node
// scope (the previous behaviour) left the request/limit lines blank; answering without
// a node filter returns the CLUSTER total, which once drew "CPU Limit 97 cores" against
// a 4-core node. The pod join is the third option: narrow by the pods on that node.
func TestNodeScopeAnswersNodelessSourcesViaThePodJoin(t *testing.T) {
	plan, ok := esUtilisationMetric("memory_limit", esScopeNode, false)
	require.True(t, ok)

	var nodeless esMetricSource
	for _, src := range plan.Sources {
		if src.NoNodeFilter {
			nodeless = src
		}
	}
	require.NotEmpty(t, nodeless.Field, "memory_limit should carry a node-less candidate")

	withoutPods := RequestMetadata{NodeName: "gke-node-1", Kind: "node"}
	assert.True(t, esSourceUnusableAtScope(nodeless, withoutPods, esScopeNode),
		"without a pod list it must stay dropped, not answer cluster-wide")

	withPods := RequestMetadata{NodeName: "gke-node-1", Kind: "node", NodePods: []string{"a-1", "b-2"}}
	assert.False(t, esSourceUnusableAtScope(nodeless, withPods, esScopeNode))

	raw, err := json.Marshal(esSourceScopeFilters(nodeless, withPods, esScopeNode))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"terms"`)
	assert.Contains(t, string(raw), "a-1")
	assert.Contains(t, string(raw), "b-2")
}

// A claim is not node-local and no pod list rescues it — one claim can be mounted by
// several pods and outlive any of them.
func TestPVCSourcesStayDroppedAtNodeScopeEvenWithAPodList(t *testing.T) {
	src := esMetricSource{Fields: esECSFields, PVCScoped: true}
	meta := RequestMetadata{NodeName: "gke-node-1", Kind: "node", NodePods: []string{"a-1"}}
	assert.True(t, esSourceUnusableAtScope(src, meta, esScopeNode))
}

// The join must not fire for scopes that already carry a node field, or it would cost a
// round trip per request for nothing.
func TestPodJoinOnlyRunsWhenANodelessSourceNeedsIt(t *testing.T) {
	limit, _ := esUtilisationMetric("memory_limit", esScopeNode, false)
	usage, _ := esUtilisationMetric("mem_real", esScopeNode, false)
	node := RequestMetadata{NodeName: "gke-node-1", Kind: "node"}

	assert.True(t, esScopeNeedsPodJoin([]esUtilMetric{limit}, node, esScopeNode))
	assert.False(t, esScopeNeedsPodJoin([]esUtilMetric{usage}, node, esScopeNode),
		"usage sources carry a node field; no join needed")
	assert.False(t, esScopeNeedsPodJoin([]esUtilMetric{limit}, RequestMetadata{}, esScopeCluster))
	assert.False(t, esScopeNeedsPodJoin([]esUtilMetric{limit}, RequestMetadata{Kind: "node"}, esScopeNode),
		"no node name means nothing to join on")
}
