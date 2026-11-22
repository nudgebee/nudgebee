package observability

import (
	"sort"
	"strings"
	"testing"
	"time"

	"nudgebee/services/integrations/orionclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Values below are from a live SolarWinds Platform 2026.4.0 test bed (2026-10-22).
// Node 1 reported AvgLoad 16 with CPUCount 4 and AvgMemoryUsed 10416750592 on a
// 17176043520-byte machine, and SWIS returned its timestamps without a zone.

func TestOrionVMQueries_RendersOnlyWhatOrionHolds(t *testing.T) {
	meta := RequestMetadata{VMInstance: "1"}
	queries := buildOrionVMQueries(meta, allUtilisationKeyStrings())

	for _, key := range orionVMKeyList() {
		assert.Contains(t, queries, key, "key %s should be rendered", key)
	}

	// Orion stores disk I/O as current operation counts with no history entity, and
	// has no per-service breakdown of a host. These must stay unrendered so
	// completeUtilisationResults explains each one instead of drawing a flat zero.
	for _, key := range []UtilisationKey{
		KeyDiskReadBytes, KeyDiskWriteBytes,
		KeyVMServiceCPUUsage, KeyVMServiceMemoryUsage, KeyVMServiceDiskUsed, KeyVMServiceGit,
	} {
		assert.NotContains(t, queries, string(key))
	}

	// Kubernetes keys are never rendered for Orion, whatever the caller asks for.
	for _, key := range []UtilisationKey{KeyCPURequest, KeyCPULimit, KeyMemoryRequest, KeyMemoryLimit, KeyPodCount} {
		assert.NotContains(t, queries, string(key))
	}
}

func TestOrionVMQueries_CPUIsConvertedToCores(t *testing.T) {
	queries := buildOrionVMQueries(RequestMetadata{VMInstance: "1"}, []string{string(KeyCPUUsage)})
	q := queries[string(KeyCPUUsage)]

	// Orion stores a percentage; the key is cores. Without the core count a 16%
	// reading would render as 16 cores.
	assert.Contains(t, q, "AvgLoad / 100.0")
	assert.Contains(t, q, "n.CPUCount")
	assert.Contains(t, q, "JOIN Orion.Nodes")
	assert.Contains(t, q, "NodeID = 1")
	assert.Contains(t, q, orionParamStart)
	assert.Contains(t, q, orionParamEnd)
}

func TestOrionVMQueries_NoNodeRendersNothing(t *testing.T) {
	// An unscoped statement would return every machine in the Orion install.
	assert.Empty(t, buildOrionVMQueries(RequestMetadata{}, []string{string(KeyCPUUsage)}))
}

func TestOrionFleetQueries_CarryTheLabelsTheListMatchesOn(t *testing.T) {
	queries := buildOrionVMFleetQueries(allUtilisationKeyStrings())
	for _, key := range orionVMFleetKeyList() {
		assert.Contains(t, queries, key)
	}

	// joinFleetMetrics reads `instance` on every series, `local_ipv4` on the
	// identity series and `hostname` on the hostname series.
	for key, q := range queries {
		assert.Contains(t, q, "AS instance", "%s must carry instance", key)
	}
	assert.Contains(t, queries[string(KeyVMFleetIdentity)], "AS local_ipv4")
	assert.Contains(t, queries[string(KeyVMFleetHostname)], "AS hostname")

	// A node Orion has never polled reports CPULoad -2; drawing that as load would
	// put a negative gauge on the list.
	assert.Contains(t, queries[string(KeyVMFleetCPUUsed)], "CPULoad >= 0")
}

func TestOrionRowsToSeries_GroupsByLabelsAndKeepsOrder(t *testing.T) {
	at := time.Unix(1700000000, 0).UTC()
	rows := []orionclient.Row{
		{"nb_time": "2026-10-22T08:09:53.5928361", "nb_value": 0.04, "instance": float64(1)},
		{"nb_time": "2026-10-22T08:19:53.7244169", "nb_value": 0.0, "instance": float64(1)},
		{"nb_time": "2026-10-22T08:29:53.7288429", "nb_value": 0.64, "instance": float64(2)},
	}
	series := orionRowsToSeries(rows, at)
	require.Len(t, series, 2, "one series per distinct label set")

	assert.Equal(t, "1", series[0].Metric["instance"], "an integer id becomes a string label")
	assert.Equal(t, []float64{0.04, 0}, series[0].Values)
	assert.Equal(t, int64(1792656593), series[0].Timestamps[0], "SWIS timestamps have no zone and are UTC: 2026-10-22T08:09:53Z")
	assert.Equal(t, []float64{0.64}, series[1].Values)
}

func TestOrionRowsToSeries_NullIsAGapNotAZero(t *testing.T) {
	// Orion returns null for a poll it never received. Carrying that as 0 would
	// draw an idle machine.
	series := orionRowsToSeries([]orionclient.Row{
		{"nb_time": "2026-10-22T08:09:53", "nb_value": nil, "instance": "1"},
		{"nb_time": "2026-10-22T08:19:53", "nb_value": 1.5, "instance": "1"},
	}, time.Now())
	require.Len(t, series, 1)
	assert.Equal(t, []float64{1.5}, series[0].Values)
}

func TestOrionRowsToSeries_CurrentValueIsStampedAtWindowEnd(t *testing.T) {
	// The fleet gauges read Orion.Nodes, which has no timestamp column.
	end := time.Unix(1700000000, 0).UTC()
	series := orionRowsToSeries([]orionclient.Row{
		{"nb_value": float64(4), "instance": float64(1)},
	}, end)
	require.Len(t, series, 1)
	assert.Equal(t, []int64{end.Unix()}, series[0].Timestamps)
}

func TestOrionNodeProbes_StrongestIdentifierFirst(t *testing.T) {
	probes := orionNodeProbes(RequestMetadata{VMIP: "10.128.0.107", VMHostname: "orion-testbed"})
	require.Len(t, probes, 4)
	assert.Equal(t, "IPAddress", probes[0].Using)
	assert.Equal(t, []string{"SysName", "DNS", "Caption"}, []string{probes[1].Using, probes[2].Using, probes[3].Using})

	// Identifiers come from the request, so they are bound, never rendered.
	for _, p := range probes {
		assert.Contains(t, p.Query, "@value")
		assert.NotContains(t, p.Query, "10.128.0.107")
	}
	assert.Equal(t, "10.128.0.107", probes[0].Params["value"])
	assert.Empty(t, orionNodeProbes(RequestMetadata{}))
}

func TestOrionNodeIDValid_OnlyIntegers(t *testing.T) {
	// The resolver's answer is rendered into statement text, so nothing else may
	// reach the builders.
	assert.True(t, orionNodeIDValid("1"))
	assert.False(t, orionNodeIDValid(""))
	assert.False(t, orionNodeIDValid("1 OR 1=1"))
	assert.False(t, orionNodeIDValid("-1"))
	assert.False(t, orionNodeIDValid("abc"))
}

func TestOrionWrapAggregate_RefusesRatherThanLie(t *testing.T) {
	src := &OrionMetricSource{}

	expr, err := src.WrapAggregate("SELECT 1", "")
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1", expr)

	// The default wrap would produce `sum(SELECT ...)`, which SWIS cannot run.
	_, err = src.WrapAggregate("SELECT 1", "sum")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestOrionParamsFor_OnlySendsPlaceholdersTheQueryNames(t *testing.T) {
	all := map[string]any{"start": "a", "end": "b"}
	assert.Equal(t, map[string]any{"start": "a"}, orionParamsFor("WHERE t >= @start", all))
	// SWIS rejects a parameter the statement does not name.
	assert.Nil(t, orionParamsFor("SELECT NodeID FROM Orion.Nodes", all))
}

func TestOrionTime_ParsesSWISShapes(t *testing.T) {
	for _, in := range []string{
		"2026-10-22T08:29:53.7288429", // what SWIS actually returns
		"2026-10-22T08:29:53",
		"2026-10-22T08:29:53Z",
	} {
		got, ok := orionTime(in)
		require.True(t, ok, in)
		assert.Equal(t, 2026, got.Year())
		assert.Equal(t, time.UTC, got.Location())
	}
	_, ok := orionTime(nil)
	assert.False(t, ok)
}

func TestOrionVMKeysAreInTheRegistry(t *testing.T) {
	// A key that is not declared would be answered with "unknown utilisation key".
	for _, key := range append(orionVMKeyList(), orionVMFleetKeyList()...) {
		assert.True(t, isKnownUtilisationKey(key), "%s must be a declared key", key)
	}
}

func TestOrionJoinQueryPath(t *testing.T) {
	got, err := orionclient.JoinQueryPath("https://orion.example.com:17774")
	require.NoError(t, err)
	assert.Equal(t, "https://orion.example.com:17774"+orionclient.QueryPath, got)

	// A trailing slash, and a base that already carries the path.
	got, err = orionclient.JoinQueryPath("https://orion.example.com:17774/")
	require.NoError(t, err)
	assert.Equal(t, "https://orion.example.com:17774"+orionclient.QueryPath, got)
	got, err = orionclient.JoinQueryPath("https://orion.example.com:17774" + orionclient.QueryPath)
	require.NoError(t, err)
	assert.Equal(t, "https://orion.example.com:17774"+orionclient.QueryPath, got)

	for _, bad := range []string{"", "orion.example.com:17774", "ftp://orion/x"} {
		_, err := orionclient.JoinQueryPath(bad)
		assert.Error(t, err, bad)
	}
}

// allUtilisationKeyStrings asks the builders for everything, so a key that must not
// be rendered is caught rather than merely untested.
func allUtilisationKeyStrings() []string {
	out := make([]string, 0, len(utilisationKeys))
	for k := range utilisationKeys {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func TestOrionVMQueries_StatementsLookLikeSWQLNotPromQL(t *testing.T) {
	for key, q := range buildOrionVMQueries(RequestMetadata{VMInstance: "7"}, allUtilisationKeyStrings()) {
		assert.True(t, strings.HasPrefix(q, "SELECT "), "%s should be SWQL: %s", key, q)
		assert.Contains(t, q, "FROM Orion.")
	}
}

// A filter this source cannot translate must fail the request. Dropping it and
// answering anyway returns numbers for a broader question than was asked, and
// nothing downstream can tell the difference. Same rule as SolarWinds.
func TestOrionRejectsFiltersItCannotApply(t *testing.T) {
	src := &OrionMetricSource{}

	// Nothing is advertised, so the builder cannot offer a chip that gets dropped.
	assert.Empty(t, src.GetSupportedOperators())

	for _, c := range []struct {
		name string
		req  FetchMetricsRequest
	}{
		{"label matcher", FetchMetricsRequest{LabelMatchers: []LabelMatcher{{Label: "node", Operator: "_eq", Value: "db-01"}}}},
		{"labels map", FetchMetricsRequest{Labels: map[string]string{"node": "db-01"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Error(t, orionRejectMatchers(c.req))

			_, err := src.GetQuery(nil, c.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not supported")
		})
	}

	// An unfiltered request is untouched.
	assert.NoError(t, orionRejectMatchers(FetchMetricsRequest{Queries: map[string]string{"k": "SELECT 1"}}))
}

// An aggregate must come back as ONE point per series. Orion cannot express it in
// SWQL, so the range is fetched and reduced afterwards; before this existed the
// caller got the whole series and read its last point as the answer.
func TestOrionCollapseOverTime_ReducesToOnePoint(t *testing.T) {
	build := func() []QueryResult {
		return []QueryResult{{
			QueryKey: string(KeyCPUUsage),
			Payload: []Result{{
				Metric:     map[string]string{"instance": "1"},
				Timestamps: []int64{100, 700, 1300},
				Values:     []float64{0.2, 0.8, 0.4},
			}},
		}}
	}

	for _, c := range []struct {
		aggregate string
		want      float64
	}{
		{"max", 0.8},
		{"min", 0.2},
		{"avg", 0.4666666666666667},
		{"sum", 1.4000000000000001},
	} {
		t.Run(c.aggregate, func(t *testing.T) {
			results := build()
			meta := RequestMetadata{Aggregate: ParsedAggregate{Name: c.aggregate}}
			orionCollapseOverTime(results, meta)

			series := results[0].Payload[0]
			require.Len(t, series.Values, 1, "a collapse is one point")
			assert.InDelta(t, c.want, series.Values[0], 1e-9)
			assert.Equal(t, []int64{1300}, series.Timestamps, "stamped at the last reading")
		})
	}

	// No aggregate: the series is left exactly as fetched.
	results := build()
	orionCollapseOverTime(results, RequestMetadata{})
	assert.Len(t, results[0].Payload[0].Values, 3)
}

func TestOrionReadingSeconds_IsTheTypicalGap(t *testing.T) {
	// Orion's detail tier writes every ten minutes; a window reaching into the
	// hourly tier mixes widths, so the median is what a reading stands for.
	result := QueryResult{Payload: []Result{{Timestamps: []int64{0, 600, 1200, 4800}}}}
	assert.Equal(t, 600.0, orionReadingSeconds(result))

	// Fewer than two readings: nothing to compare, so no conversion.
	assert.Equal(t, 0.0, orionReadingSeconds(QueryResult{Payload: []Result{{Timestamps: []int64{0}}}}))
}
