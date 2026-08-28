package observability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time proof the log source satisfies the OPTIONAL grouping interface.
// SupportsLogGroups and the Log Groups tab are both derived from a runtime type
// assertion, so dropping QueryLogGroup would not break the build — it would silently
// turn the tab back into "Log Grouping not supported" with nothing to catch it.
var _ LogGroupSource = (*SplunkEnterpriseLogSource)(nil)

func TestBuildSplunkEnterpriseLogGroupSPL(t *testing.T) {
	t.Run("canonical shape", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 0)
		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(spl, `search index="otel_logs" | eval `), spl)
		assert.Contains(t, spl, `| stats count AS nb_lg_count, max(_time) AS nb_lg_last_time, max(nb_lg_levelnum) AS nb_lg_levelnum BY `)
		assert.Contains(t, spl, `| sort - nb_lg_count | head 100`)
		// A leading pipe would bypass the index scope; the shared log validator must
		// accept what this builder emits, or every log-group query is refused.
		assert.NoError(t, validateSplunkEnterpriseQuery(spl))
	})

	t.Run("every grouped field is coalesced to a non-null default", func(t *testing.T) {
		// The load-bearing property of the whole query. `stats ... BY f` drops every
		// event where f is null, so a single absent field empties the tab rather than
		// degrading the grouping. Each grouped coalesce therefore has to end in a
		// literal "". Confirmed live: this is what lets an index with no
		// k8s.container.name still return rows.
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 0)
		require.NoError(t, err)

		for _, alias := range []string{
			splunkLogGroupMessageCol, splunkLogGroupNamespaceCol, splunkLogGroupPodCol,
			splunkLogGroupContainerCol, splunkLogGroupWorkloadCol, splunkLogGroupLevelCol,
		} {
			idx := strings.Index(spl, alias+"=coalesce(")
			require.NotEqual(t, -1, idx, "%s must be assigned by coalesce", alias)
			end := strings.Index(spl[idx:], ")")
			require.NotEqual(t, -1, end)
			assert.True(t, strings.HasSuffix(spl[idx:idx+end], `, ""`),
				"%s must fall back to an empty literal, got %q", alias, spl[idx:idx+end])
			byClause := spl[strings.Index(spl, " BY "):]
			assert.Contains(t, byClause, alias, "%s must be part of the BY clause", alias)
		}
	})

	t.Run("the severity number is numeric and survives the aggregation", func(t *testing.T) {
		// stats discards every field that is neither a BY key nor an aggregate. An
		// earlier revision computed nb_lg_levelnum and then dropped it from all 100
		// rows Splunk returned, which made the FATAL branch of the converter dead code.
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 0)
		require.NoError(t, err)

		assert.Contains(t, spl, `nb_lg_levelnum=coalesce(tonumber('severity_number'), -1)`,
			"a string default would make max() and >= compare lexicographically")
		assert.Contains(t, spl, `max(nb_lg_levelnum) AS nb_lg_levelnum`)
		assert.NotContains(t, spl, `BY nb_lg_message, nb_lg_namespace, nb_lg_pod, nb_lg_workload, nb_lg_container, nb_lg_level, nb_lg_levelnum`,
			"grouping on it too would only risk splitting a group")
	})

	t.Run("dotted OTel field names are single-quoted", func(t *testing.T) {
		// Bare k8s.namespace.name is not read as a field reference by eval.
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 0)
		require.NoError(t, err)
		assert.Contains(t, spl, `'k8s.namespace.name'`)
		assert.NotContains(t, spl, `coalesce(k8s.namespace.name`)
	})

	t.Run("namespace filter is applied to the resolved column", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "nudgebee", "", 0)
		require.NoError(t, err)
		assert.Contains(t, spl, `nb_lg_namespace="nudgebee"`)
	})

	t.Run("workload filter matches the pod prefix or the workload field", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "checkout", 0)
		require.NoError(t, err)
		// like() takes SQL wildcards, so the '%' is the wildcard and escapeSplunkString's
		// '*' escaping cannot interfere with it.
		assert.Contains(t, spl, `like(nb_lg_pod, "checkout-%")`)
		assert.Contains(t, spl, `nb_lg_workload="checkout"`)
	})

	t.Run("filter values are escaped", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", `x" OR nb_lg_namespace="`, "", 0)
		require.NoError(t, err)
		assert.NotContains(t, spl, `nb_lg_namespace="x" OR`)
		assert.Contains(t, spl, `\"`)
	})

	t.Run("unsafe index is rejected", func(t *testing.T) {
		_, err := buildSplunkEnterpriseLogGroupSPL(`bad" index`, "", "", 0)
		assert.Error(t, err)
	})

	t.Run("limit is clamped rather than trusted", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 100000)
		require.NoError(t, err)
		assert.Contains(t, spl, "| head 100")
	})

	t.Run("infrastructure containers are excluded", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 0)
		require.NoError(t, err)
		assert.Contains(t, spl, `NOT nb_lg_container IN ("prometheus", "grafana", "nudgebee-agent")`)
	})

	t.Run("message-content fallback only applies when severity is absent", func(t *testing.T) {
		spl, err := buildSplunkEnterpriseLogGroupSPL("otel_logs", "", "", 0)
		require.NoError(t, err)
		// Guarding the fallback on an empty severity is what stops an INFO line that
		// merely mentions "ERROR" from being reported as an error.
		assert.Contains(t, spl, `nb_lg_level="" AND (`)
		assert.Contains(t, spl, `match(nb_lg_message, "ERROR")`)
		assert.Contains(t, spl, `nb_lg_levelnum>=17`)
	})
}

func TestConvertSplunkEnterpriseLogGroups(t *testing.T) {
	const fallback int64 = 1700000000

	t.Run("maps an aggregated row onto the shared contract", func(t *testing.T) {
		out := convertSplunkEnterpriseLogGroups([]map[string]any{{
			splunkLogGroupMessageCol:   "connection refused",
			splunkLogGroupNamespaceCol: "nudgebee",
			splunkLogGroupPodCol:       "checkout-7d9f-abc",
			splunkLogGroupWorkloadCol:  "checkout",
			splunkLogGroupContainerCol: "app",
			splunkLogGroupLevelCol:     "ERROR",
			splunkLogGroupCountCol:     "12",
			splunkLogGroupLastTimeCol:  "1787806840.500",
		}}, fallback)

		require.Len(t, out.Groups, 1)
		g := out.Groups[0]
		assert.Equal(t, "connection refused", g.Sample)
		assert.Equal(t, "nudgebee", g.Namespace)
		assert.Equal(t, "checkout", g.Workload)
		assert.Equal(t, "app", g.Container)
		assert.Equal(t, "ERROR", g.Level)
		assert.Equal(t, int64(12), g.Count)
		assert.Equal(t, []float64{12}, g.Values)
		// max(_time) is epoch seconds inside stats; the frontend multiplies by 1000, so
		// emitting milliseconds here would place every group ~55,000 years in the future.
		assert.Equal(t, []int64{1787806840}, g.Timestamps)
		assert.Equal(t, "/k8s/nudgebee/checkout/app", g.ContainerID)
		assert.NotEmpty(t, g.PatternHash)
	})

	t.Run("workload is derived from the pod name only when the shipper recorded none", func(t *testing.T) {
		out := convertSplunkEnterpriseLogGroups([]map[string]any{{
			splunkLogGroupMessageCol:  "boom",
			splunkLogGroupPodCol:      "checkout-7d9f8b6c4d-x2k9p",
			splunkLogGroupWorkloadCol: "",
			splunkLogGroupCountCol:    "1",
		}}, fallback)
		require.Len(t, out.Groups, 1)
		assert.Equal(t, "checkout", out.Groups[0].Workload)
	})

	t.Run("severity number distinguishes fatal when there is no severity text", func(t *testing.T) {
		rows := []map[string]any{
			{splunkLogGroupMessageCol: "a", splunkLogGroupCountCol: "1", splunkLogGroupLevelNumCol: "21"},
			{splunkLogGroupMessageCol: "b", splunkLogGroupCountCol: "1", splunkLogGroupLevelNumCol: "17"},
			{splunkLogGroupMessageCol: "c", splunkLogGroupCountCol: "1"},
		}
		out := convertSplunkEnterpriseLogGroups(rows, fallback)
		require.Len(t, out.Groups, 3)
		assert.Equal(t, "fatal", out.Groups[0].Level)
		assert.Equal(t, "error", out.Groups[1].Level)
		assert.Equal(t, "error", out.Groups[2].Level, "no severity at all still reads as an error")
	})

	t.Run("rows without a usable count or sample are dropped", func(t *testing.T) {
		out := convertSplunkEnterpriseLogGroups([]map[string]any{
			{splunkLogGroupMessageCol: "a"},
			{splunkLogGroupCountCol: "3"},
			{splunkLogGroupMessageCol: "b", splunkLogGroupCountCol: "not-a-number"},
		}, fallback)
		assert.Empty(t, out.Groups)
	})

	t.Run("an unreadable last-time falls back to the window edge", func(t *testing.T) {
		out := convertSplunkEnterpriseLogGroups([]map[string]any{{
			splunkLogGroupMessageCol:  "a",
			splunkLogGroupCountCol:    "1",
			splunkLogGroupLastTimeCol: "",
		}}, fallback)
		require.Len(t, out.Groups, 1)
		assert.Equal(t, []int64{fallback}, out.Groups[0].Timestamps)
	})

	t.Run("container_id is omitted unless namespace and workload are both known", func(t *testing.T) {
		out := convertSplunkEnterpriseLogGroups([]map[string]any{{
			splunkLogGroupMessageCol:   "a",
			splunkLogGroupCountCol:     "1",
			splunkLogGroupNamespaceCol: "nudgebee",
		}}, fallback)
		require.Len(t, out.Groups, 1)
		assert.Empty(t, out.Groups[0].ContainerID, "a half-formed path would parse back wrong in the UI")
	})
}

func TestSplunkEnterpriseCoalesce(t *testing.T) {
	expr, err := splunkEnterpriseCoalesce([]string{"body", "k8s.pod.name"}, `""`)
	require.NoError(t, err)
	assert.Equal(t, `coalesce('body', 'k8s.pod.name', "")`, expr)

	numeric, err := splunkEnterpriseNumericCoalesce([]string{"severity_number"}, "-1")
	require.NoError(t, err)
	assert.Equal(t, `coalesce(tonumber('severity_number'), -1)`, numeric)

	_, err = splunkEnterpriseCoalesce([]string{`bad" name`}, `""`)
	assert.Error(t, err, "an unsafe field name must not reach the query")

	_, err = splunkEnterpriseNumericCoalesce([]string{`bad" name`}, "-1")
	assert.Error(t, err, "an unsafe field name must not reach the query")
}
