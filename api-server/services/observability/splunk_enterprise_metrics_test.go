package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time proof the source satisfies both the required interface and the optional
// playbook one. The optional assertion matters most: PlaybookQueryGenerator is looked up
// by type assertion at runtime, so dropping a method would silently fall back to PromQL
// generation that Splunk cannot execute, with no compile error to catch it.
var (
	_ MetricSource           = (*SplunkEnterpriseMetricSource)(nil)
	_ PlaybookQueryGenerator = (*SplunkEnterpriseMetricSource)(nil)
)

func TestBuildSplunkMStatsQuery(t *testing.T) {
	tests := []struct {
		name    string
		item    QueryItem
		labels  map[string]string
		span    int
		instant bool
		want    string
	}{
		{
			name: "canonical label is mapped to the OTel dimension and grouped",
			item: QueryItem{
				Metric:        "k8s.pod.cpu.usage",
				LabelMatchers: []LabelMatcher{{Label: "namespace", Operator: "_eq", Value: "nudgebee"}},
			},
			span: 300,
			want: `| mstats avg(k8s.pod.cpu.usage) AS nb_value WHERE index="otel_metrics" AND k8s.namespace.name="nudgebee" span=300s BY k8s.namespace.name`,
		},
		{
			name: "aggregate operator is honoured",
			item: QueryItem{Metric: "k8s.pod.memory.usage", AggregateOperator: "sum"},
			span: 300,
			want: `| mstats sum(k8s.pod.memory.usage) AS nb_value WHERE index="otel_metrics" span=300s`,
		},
		{
			name:    "instant query omits span",
			item:    QueryItem{Metric: "k8s.pod.cpu.usage"},
			instant: true,
			want:    `| mstats avg(k8s.pod.cpu.usage) AS nb_value WHERE index="otel_metrics"`,
		},
		{
			name: "absent span falls back to the default rather than emitting span=0s",
			item: QueryItem{Metric: "k8s.pod.cpu.usage"},
			span: 0,
			want: `| mstats avg(k8s.pod.cpu.usage) AS nb_value WHERE index="otel_metrics" span=60s`,
		},
		{
			name:   "eq labels and matchers combine, sorted for stability",
			item:   QueryItem{Metric: "cpu", LabelMatchers: []LabelMatcher{{Label: "pod", Operator: "_neq", Value: "x"}}},
			labels: map[string]string{"namespace": "demo"},
			span:   60,
			want:   `| mstats avg(cpu) AS nb_value WHERE index="otel_metrics" AND k8s.namespace.name="demo" AND k8s.pod.name!="x" span=60s BY k8s.namespace.name, k8s.pod.name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildSplunkMStatsQuery(tt.item, tt.labels, "otel_metrics", tt.span, tt.instant)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// escapeSplunkString turns a literal `*` into `\*`. A pod-name prefix therefore cannot
// carry its wildcard in the value — it would be escaped and match nothing. Verified
// against Splunk 10.4.2: the unescaped form returned 28 rows, the escaped form none.
func TestSplunkMetricPrefixOperatorKeepsWildcardUnescaped(t *testing.T) {
	got, err := buildSplunkWorkloadCPUQuery("k8s.pod.cpu.usage", "api-server", "nudgebee", "otel_metrics")
	require.NoError(t, err)
	assert.Contains(t, got, `k8s.pod.name="api-server-*"`)
	assert.NotContains(t, got, `\*`, "the trailing wildcard must not be escaped")
}

func TestBuildSplunkMStatsQueryRejectsUnsafeInput(t *testing.T) {
	_, err := buildSplunkMStatsQuery(QueryItem{Metric: `cpu" | delete`}, nil, "otel_metrics", 60, false)
	assert.Error(t, err, "a metric name that could close the aggregation must be rejected")

	_, err = buildSplunkMStatsQuery(QueryItem{Metric: "cpu"}, nil, `bad" index`, 60, false)
	assert.Error(t, err, "an unsafe index name must be rejected")

	_, err = buildSplunkMStatsQuery(
		QueryItem{Metric: "cpu", LabelMatchers: []LabelMatcher{{Label: `bad" dim`, Operator: "_eq", Value: "x"}}},
		nil, "otel_metrics", 60, false)
	assert.Error(t, err, "an unsafe dimension name must be rejected")
}

func TestValidateSplunkEnterpriseMetricQuery(t *testing.T) {
	const index = "otel_metrics"

	t.Run("accepts the generated shapes", func(t *testing.T) {
		for _, spl := range []string{
			`| mstats avg(cpu) AS nb_value WHERE index="otel_metrics" span=60s`,
			`| mcatalog values(metric_name) WHERE index="otel_metrics"`,
			`| mcatalog values(_dims) WHERE index="otel_metrics"`,
		} {
			assert.NoError(t, validateSplunkEnterpriseMetricQuery(spl, index), spl)
		}
	})

	t.Run("requires a leading generating command", func(t *testing.T) {
		err := validateSplunkEnterpriseMetricQuery(`search index="otel_metrics" | head 10`, index)
		assert.Error(t, err)
	})

	t.Run("only mstats and mcatalog may open the query", func(t *testing.T) {
		err := validateSplunkEnterpriseMetricQuery(`| inputlookup creds WHERE index="otel_metrics"`, index)
		assert.Error(t, err)
	})

	// The whole point of the tightened index rule: a leading pipe lets the query pick its
	// own index, so an unscoped one could read anything on the server.
	t.Run("must be scoped to the configured index", func(t *testing.T) {
		err := validateSplunkEnterpriseMetricQuery(`| mstats avg(cpu) AS nb_value WHERE index="secrets" span=60s`, index)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configured metrics index")
	})

	t.Run("a prefix of the configured index does not satisfy the scope check", func(t *testing.T) {
		err := validateSplunkEnterpriseMetricQuery(`| mstats avg(cpu) AS nb_value WHERE index="otel_metrics_secret" span=60s`, index)
		assert.Error(t, err)
	})

	t.Run("disallowed commands are still rejected downstream", func(t *testing.T) {
		err := validateSplunkEnterpriseMetricQuery(`| mstats avg(cpu) AS nb_value WHERE index="otel_metrics" | outputlookup stolen.csv`, index)
		assert.Error(t, err)
	})

	t.Run("empty is rejected", func(t *testing.T) {
		assert.Error(t, validateSplunkEnterpriseMetricQuery("   ", index))
	})
}

func TestConvertSplunkMetricRows(t *testing.T) {
	t.Run("groups rows into one series per dimension set", func(t *testing.T) {
		rows := []map[string]any{
			{"_time": "2026-08-27T15:20:00.000+00:00", "k8s.namespace.name": "nudgebee", "nb_value": "1.5"},
			{"_time": "2026-08-27T15:25:00.000+00:00", "k8s.namespace.name": "nudgebee", "nb_value": "2.5"},
			{"_time": "2026-08-27T15:20:00.000+00:00", "k8s.namespace.name": "demo", "nb_value": "9"},
		}
		got := convertSplunkMetricRows("cpu", "spl", rows)
		require.Len(t, got.Payload, 2)

		byNamespace := map[string]Result{}
		for _, r := range got.Payload {
			byNamespace[r.Metric["k8s.namespace.name"]] = r
		}
		assert.Equal(t, []float64{1.5, 2.5}, byNamespace["nudgebee"].Values)
		assert.Len(t, byNamespace["nudgebee"].Timestamps, 2)
		assert.Equal(t, []float64{9}, byNamespace["demo"].Values)
	})

	t.Run("instant rows carry no _time and still produce a series", func(t *testing.T) {
		rows := []map[string]any{{"k8s.deployment.name": "checkout", "nb_value": "1.38"}}
		got := convertSplunkMetricRows("cpu", "spl", rows)
		require.Len(t, got.Payload, 1)
		assert.Equal(t, []float64{1.38}, got.Payload[0].Values)
		assert.Len(t, got.Payload[0].Timestamps, 1, "a value must still be timestamped")
	})

	// A null bucket must not become a zero: zero is a measurement, absence is not.
	t.Run("non-numeric values are skipped rather than zeroed", func(t *testing.T) {
		rows := []map[string]any{
			{"_time": "2026-08-27T15:20:00.000+00:00", "nb_value": "1"},
			{"_time": "2026-08-27T15:25:00.000+00:00", "nb_value": ""},
			{"_time": "2026-08-27T15:30:00.000+00:00", "nb_value": "3"},
		}
		got := convertSplunkMetricRows("cpu", "spl", rows)
		require.Len(t, got.Payload, 1)
		assert.Equal(t, []float64{1, 3}, got.Payload[0].Values)
	})

	t.Run("internal columns are not leaked as labels", func(t *testing.T) {
		rows := []map[string]any{
			{"_time": "2026-08-27T15:20:00.000+00:00", "_span": "60", "k8s.namespace.name": "demo", "nb_value": "1"},
		}
		got := convertSplunkMetricRows("cpu", "spl", rows)
		require.Len(t, got.Payload, 1)
		assert.Equal(t, map[string]string{"k8s.namespace.name": "demo"}, got.Payload[0].Metric)
	})

	t.Run("rows with no value column report why the payload is empty", func(t *testing.T) {
		rows := []map[string]any{{"_time": "2026-08-27T15:20:00.000+00:00", "other": "x"}}
		got := convertSplunkMetricRows("cpu", "spl", rows)
		assert.Empty(t, got.Payload)
		require.NotNil(t, got.DocsMatched)
		assert.Equal(t, int64(1), *got.DocsMatched)
		assert.NotEmpty(t, got.Note)
	})
}

// mcatalog returns a single row whose column is an array. Joining it with commas (as the
// display formatter does) would split any value containing a comma in two.
func TestSplunkEnterpriseCatalogValues(t *testing.T) {
	rows := []map[string]any{
		{"values(metric_name)": []any{"k8s.pod.cpu.usage", "k8s.pod.memory.usage", "k8s.pod.cpu.usage"}},
	}
	got := splunkEnterpriseCatalogValues(rows, "values(metric_name)")
	assert.Equal(t, []string{"k8s.pod.cpu.usage", "k8s.pod.memory.usage"}, got, "duplicates collapse, order is stable")

	assert.Empty(t, splunkEnterpriseCatalogValues(rows, "values(missing)"))
}

func TestRankSplunkCPUMetricNames(t *testing.T) {
	metrics := []OutputMetrics{
		{Metric: "k8s.pod.memory.usage"},
		{Metric: "container.cpu.usage"},
		{Metric: "k8s.pod.network.rx"},
		{Metric: "k8s.pod.cpu.usage"},
	}
	got := rankSplunkCPUMetricNames(metrics)

	require.NotEmpty(t, got)
	assert.Equal(t, "k8s.pod.cpu.usage", got[0], "the most specific spelling must be probed first")
	assert.Contains(t, got, "container.cpu.usage")
	assert.NotContains(t, got, "k8s.pod.memory.usage", "non-CPU metrics must not be probed")
	assert.NotContains(t, got, "k8s.pod.network.rx")
}

func TestRankSplunkCPUMetricNamesEmptyWhenNothingMatches(t *testing.T) {
	got := rankSplunkCPUMetricNames([]OutputMetrics{{Metric: "disk.io"}, {Metric: "net.rx"}})
	assert.Empty(t, got, "no CPU-like metric means discovery must fail loudly, not probe everything")
}

func TestSplunkEnterpriseAggregation(t *testing.T) {
	assert.Equal(t, "sum", splunkEnterpriseAggregation("sum"))
	assert.Equal(t, "avg", splunkEnterpriseAggregation(""))
	// mstats has no stddev over metrics; falling back beats emitting invalid SPL.
	assert.Equal(t, "avg", splunkEnterpriseAggregation("stddev"))
	assert.Equal(t, "max", splunkEnterpriseAggregation("MAX"))
}
