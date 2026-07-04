package observability

// Tests for the series-match discovery primitive (metrics_list_series). They cover the
// pure logic — selector rendering, label-values parsing, per-candidate family grouping,
// truncation signalling — without the relay/Prometheus round-trip. The fixtures mirror
// real families observed for a workload that emits under BOTH eBPF (destination_workload_*)
// and scraped (pod/job) labels, which is exactly the multi-instrumentation case a single
// selector cannot cover.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSeriesMatchSelectors_ExactAndPrefix(t *testing.T) {
	candidates := []seriesMatchCandidate{
		{NamespaceLabel: "destination_workload_namespace", WorkloadLabel: "destination_workload_name", MatchType: matchTypeExact},
		{NamespaceLabel: "namespace", WorkloadLabel: "pod", MatchType: matchTypePrefix},
	}
	sels, err := buildSeriesMatchSelectors("nudgebee", "llm-server", candidates)
	require.NoError(t, err)
	require.Len(t, sels, 2)
	assert.Equal(t, `{destination_workload_namespace="nudgebee",destination_workload_name="llm-server"}`, sels[0])
	assert.Equal(t, `{namespace="nudgebee",pod=~"llm-server.*"}`, sels[1])
}

func TestBuildSeriesMatchSelectors_OmitsNamespaceWhenEmpty(t *testing.T) {
	candidates := []seriesMatchCandidate{
		{NamespaceLabel: "namespace", WorkloadLabel: "job", MatchType: matchTypeExact},
	}
	sels, err := buildSeriesMatchSelectors("", "llm-server", candidates)
	require.NoError(t, err)
	require.Len(t, sels, 1)
	assert.Equal(t, `{job="llm-server"}`, sels[0])
}

func TestBuildSeriesMatchSelectors_EscapesValuesAndRegexMeta(t *testing.T) {
	candidates := []seriesMatchCandidate{
		{NamespaceLabel: "namespace", WorkloadLabel: "pod", MatchType: matchTypePrefix},
		{NamespaceLabel: "namespace", WorkloadLabel: "job", MatchType: matchTypeExact},
	}
	// A workload with a regex metacharacter and a namespace with a quote: neither may
	// break out of the matcher (injection guard) and the dot must stay literal in regex.
	// The anchored regex is what gives the prefix match its semantics server-side.
	sels, err := buildSeriesMatchSelectors(`ns"x`, "web.app", candidates)
	require.NoError(t, err)
	// regex meta '.' escaped to '\.', then PromQL-string-escaped to '\\.'
	assert.Equal(t, `{namespace="ns\"x",pod=~"web\\.app.*"}`, sels[0])
	assert.Equal(t, `{namespace="ns\"x",job="web.app"}`, sels[1])
}

func TestBuildSeriesMatchSelectors_RequiresWorkload(t *testing.T) {
	_, err := buildSeriesMatchSelectors("nudgebee", "", SeriesMatchCandidates)
	require.Error(t, err)
}

func TestParseFamilyValues_FiltersAndCountsTruncation(t *testing.T) {
	// label-values `data` is a list of metric-name strings; non-strings/empties dropped.
	data := []any{"http_server_active_requests", "nb_llm_llm_requests", "", 42, "go_goroutine_count"}
	fams, trunc := parseFamilyValues(data, nil, 500)
	assert.Equal(t, []string{"http_server_active_requests", "nb_llm_llm_requests", "go_goroutine_count"}, fams)
	assert.False(t, trunc, "3 < limit 500 → not truncated")

	// len >= limit backstop (VictoriaMetrics honors `limit` but omits the warning).
	_, trunc = parseFamilyValues([]any{"a", "b"}, nil, 2)
	assert.True(t, trunc, "len>=limit must flag truncation even without a warning")

	// explicit Prometheus warning.
	_, trunc = parseFamilyValues([]any{"a"}, []any{"results truncated due to limit"}, 500)
	assert.True(t, trunc)

	// nil / malformed data is empty, not a panic.
	fams, trunc = parseFamilyValues(nil, nil, 500)
	assert.Empty(t, fams)
	assert.False(t, trunc)
}

func TestAssembleSeriesResult_GroupsByCandidate(t *testing.T) {
	candidates := []seriesMatchCandidate{
		{NamespaceLabel: "destination_workload_namespace", WorkloadLabel: "destination_workload_name", MatchType: matchTypeExact},
		{NamespaceLabel: "namespace", WorkloadLabel: "job", MatchType: matchTypeExact},
		{NamespaceLabel: "namespace", WorkloadLabel: "pod", MatchType: matchTypePrefix},
		{NamespaceLabel: "namespace", WorkloadLabel: "statefulset", MatchType: matchTypeExact}, // matched nothing
	}
	families := [][]string{
		{"container_net_tcp_active_connections", "container_http_requests_total"},      // eBPF layer
		{"http_server_request_duration_milliseconds_sum"},                              // scraped via job
		{"nb_llm_llm_requests_total", "http_server_request_duration_milliseconds_sum"}, // scraped via pod
		nil, // statefulset candidate found nothing → dropped
	}
	truncated := []bool{false, false, false, false}

	res := assembleSeriesResult(candidates, families, truncated)
	require.Len(t, res.Matches, 3, "the empty statefulset candidate must be dropped")
	assert.False(t, res.Truncated)

	// deterministic sort: destination_workload_namespace < namespace; within namespace, job < pod
	assert.Equal(t, "destination_workload_name", res.Matches[0].WorkloadLabel)
	assert.Equal(t, matchTypeExact, res.Matches[0].MatchType)
	// families are sorted within a candidate
	assert.Equal(t, []string{"container_http_requests_total", "container_net_tcp_active_connections"}, res.Matches[0].Families)

	assert.Equal(t, "namespace", res.Matches[1].NamespaceLabel)
	assert.Equal(t, "job", res.Matches[1].WorkloadLabel)
	assert.Equal(t, []string{"http_server_request_duration_milliseconds_sum"}, res.Matches[1].Families)

	assert.Equal(t, "pod", res.Matches[2].WorkloadLabel)
	assert.Equal(t, matchTypePrefix, res.Matches[2].MatchType)
	assert.Equal(t, []string{"http_server_request_duration_milliseconds_sum", "nb_llm_llm_requests_total"}, res.Matches[2].Families)
}

func TestAssembleSeriesResult_DedupesAndPropagatesTruncation(t *testing.T) {
	candidates := []seriesMatchCandidate{
		{NamespaceLabel: "namespace", WorkloadLabel: "job", MatchType: matchTypeExact},
	}
	// a non-conforming engine returns a duplicate; assemble must dedup.
	families := [][]string{{"http_server_active_requests", "http_server_active_requests"}}
	res := assembleSeriesResult(candidates, families, []bool{true})
	require.Len(t, res.Matches, 1)
	assert.Equal(t, []string{"http_server_active_requests"}, res.Matches[0].Families)
	assert.True(t, res.Truncated, "truncation on any candidate propagates to the result")
}

func TestAssembleSeriesResult_AllEmpty(t *testing.T) {
	candidates := []seriesMatchCandidate{
		{NamespaceLabel: "namespace", WorkloadLabel: "job", MatchType: matchTypeExact},
	}
	res := assembleSeriesResult(candidates, [][]string{nil}, []bool{false})
	assert.Empty(t, res.Matches)
	assert.False(t, res.Truncated)
}

func TestWarningsIndicateLimit(t *testing.T) {
	assert.True(t, warningsIndicateLimit([]any{"results truncated due to limit"}))
	assert.True(t, warningsIndicateLimit([]any{"some other warning", "hit LIMIT of 2000"}))
	assert.False(t, warningsIndicateLimit([]any{"unrelated warning"}))
	assert.False(t, warningsIndicateLimit(nil))
	assert.False(t, warningsIndicateLimit("not-a-list"))
}

// Verifies the optional-capability contract the FetchMetricSeries orchestrator relies on:
// Prometheus implements MetricSeriesSource (handled), a provider without a series-match
// equivalent does not (orchestrator returns "not supported" rather than empty results).
func TestMetricSeriesSourceCapability(t *testing.T) {
	var _ MetricSeriesSource = (*PrometheusMetricSource)(nil)

	_, ok := MetricSource(&PrometheusMetricSource{}).(MetricSeriesSource)
	assert.True(t, ok, "prometheus must implement series-match")

	_, ok = MetricSource(&DatadogMetricSource{}).(MetricSeriesSource)
	assert.False(t, ok, "datadog must not implement series-match (not supported path)")
}

// Guard: the default candidate set must never carry a metric __name__ — families are
// discovered from the engine, not hardcoded (acceptance criterion for #32406).
func TestDefaultCandidates_NoHardcodedFamilies(t *testing.T) {
	for _, c := range SeriesMatchCandidates {
		assert.NotEqual(t, "__name__", c.WorkloadLabel)
		assert.NotEqual(t, "__name__", c.NamespaceLabel)
		assert.False(t, strings.Contains(c.WorkloadLabel, "_total") || strings.Contains(c.WorkloadLabel, "_seconds"),
			"candidate workload label %q looks like a metric family name", c.WorkloadLabel)
	}
}
