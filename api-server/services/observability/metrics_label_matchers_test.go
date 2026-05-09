package observability

// Tests for the LabelMatchers rendering layer that GetMetricsQuery (the
// metrics_get_query Hasura action) routes BUILDER chips through. Each
// provider's GetQuery must honor req.LabelMatchers — until this layer
// existed, only Prometheus/Chronosphere did, so NewRelic/Dynatrace/Datadog/
// Splunk silently dropped the WHERE/filter clause and produced unfiltered
// queries.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- NewRelic ----

func TestNewRelicGetQuery_HonorsLabelMatchers(t *testing.T) {
	s := &NewRelicMetricSource{}
	req := FetchMetricsRequest{
		Queries:   map[string]string{"k": "ebpf.tcp.data.packet.send"},
		StartTime: 1778216782502,
		EndTime:   1778220382502,
		LabelMatchers: []LabelMatcher{
			{Label: "appName", Operator: "_eq", Value: "Python Application"},
		},
	}
	out, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	// appName is a simple identifier so escapeNRQLField leaves it unquoted.
	assert.Contains(t, out, "WHERE appName = 'Python Application'", "matcher must render into a WHERE clause")
	assert.Contains(t, out, "FROM Metric")
}

func TestNewRelicMatcher_OperatorCoverage(t *testing.T) {
	// Note: escapeNRQLField only backticks non-simple identifiers (dotted names,
	// names starting with a digit, etc.). "k" is simple, so no backticks here.
	cases := []struct {
		op       string
		expected string
	}{
		{"_eq", "k = 'v'"},
		{"_neq", "k != 'v'"},
		{"_like", "k LIKE 'v'"},
		{"_ilike", "k LIKE 'v'"},
		{"_contains", "k LIKE '%v%'"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			out, err := nrqlMatcherClause(LabelMatcher{Label: "k", Operator: tc.op, Value: "v"})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, out)
		})
	}
}

func TestNewRelicMatcher_BackticksDottedIdentifier(t *testing.T) {
	out, err := nrqlMatcherClause(LabelMatcher{Label: "k8s.pod.name", Operator: "_eq", Value: "v"})
	require.NoError(t, err)
	assert.Equal(t, "`k8s.pod.name` = 'v'", out)
}

func TestNewRelicMatcher_RejectsUnshapedOps(t *testing.T) {
	for _, op := range []string{"_in", "_not_in", "_regex", "bogus"} {
		t.Run(op, func(t *testing.T) {
			_, err := nrqlMatcherClause(LabelMatcher{Label: "k", Operator: op, Value: "v"})
			require.Error(t, err)
		})
	}
}

func TestNewRelicMatcher_DeterministicOrdering(t *testing.T) {
	out, err := buildNRQLWhereFromMatchers([]LabelMatcher{
		{Label: "z", Operator: "_eq", Value: "1"},
		{Label: "a", Operator: "_eq", Value: "1"},
		{Label: "m", Operator: "_eq", Value: "1"},
	})
	require.NoError(t, err)
	// Sorted by label.
	assert.Equal(t, "a = '1' AND m = '1' AND z = '1'", out)
}

func TestNewRelicMatcher_MergesWithLegacyLabels(t *testing.T) {
	s := &NewRelicMetricSource{}
	out, err := s.buildNRQLMetricQuery(
		"my.metric",
		1700000000, 1700003600, 60, false,
		map[string]string{"env": "prod"},
		[]LabelMatcher{{Label: "appName", Operator: "_eq", Value: "Python Application"}},
	)
	require.NoError(t, err)
	// Legacy label first, matcher AND'd after.
	assert.Contains(t, out, "WHERE env='prod' AND appName = 'Python Application'")
}

// ---- Dynatrace ----

func TestDynatraceGetQuery_HonorsLabelMatchers(t *testing.T) {
	s := &DynatraceMetricSource{}
	req := FetchMetricsRequest{
		Queries:   map[string]string{"k": "builtin:host.cpu.usage"},
		StartTime: 1778216782502,
		EndTime:   1778220382502,
		LabelMatchers: []LabelMatcher{
			{Label: "host", Operator: "_eq", Value: "ip-1.2.3.4"},
		},
	}
	out, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	assert.Contains(t, out, "filter: `host`==\"ip-1.2.3.4\"")
}

func TestDynatraceMatcher_OperatorCoverage(t *testing.T) {
	cases := []struct {
		op       string
		expected string
	}{
		{"_eq", "`k`==\"v\""},
		{"_neq", "`k`!=\"v\""},
		{"_contains", "contains(`k`, \"v\")"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			out, err := dqlMatcherClause(LabelMatcher{Label: "k", Operator: tc.op, Value: "v"})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, out)
		})
	}
}

func TestDynatraceMatcher_RejectsUnshapedOps(t *testing.T) {
	for _, op := range []string{"_in", "_not_in", "_like", "_ilike", "_regex", "bogus"} {
		t.Run(op, func(t *testing.T) {
			_, err := dqlMatcherClause(LabelMatcher{Label: "k", Operator: op, Value: "v"})
			require.Error(t, err)
		})
	}
}

// ---- Datadog ----

func TestDatadogGetQuery_HonorsLabelMatchers(t *testing.T) {
	s := &DatadogMetricSource{}
	req := FetchMetricsRequest{
		Queries: map[string]string{"k": "system.cpu.user"},
		LabelMatchers: []LabelMatcher{
			{Label: "host", Operator: "_eq", Value: "web-01"},
		},
	}
	out, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	assert.Equal(t, "system.cpu.user{host:web-01}", out)
}

func TestDatadogGetQuery_MergesLegacyLabelsAndMatchers(t *testing.T) {
	s := &DatadogMetricSource{}
	req := FetchMetricsRequest{
		Queries: map[string]string{"k": "system.cpu.user{env:prod}"},
		Labels:  map[string]string{"team": "platform"},
		LabelMatchers: []LabelMatcher{
			{Label: "host", Operator: "_neq", Value: "web-01"},
		},
	}
	out, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	assert.Contains(t, out, "env:prod")
	assert.Contains(t, out, "team:platform")
	assert.Contains(t, out, "!host:web-01")
}

func TestDatadogMatcher_OperatorCoverage(t *testing.T) {
	cases := []struct {
		op       string
		expected string
	}{
		{"_eq", "k:v"},
		{"_neq", "!k:v"},
		{"_like", "k:v"},
		{"_contains", "k:*v*"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			out, err := datadogMatcherClause(LabelMatcher{Label: "k", Operator: tc.op, Value: "v"})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, out)
		})
	}
}

func TestDatadogMatcher_RejectsUnshapedOps(t *testing.T) {
	for _, op := range []string{"_in", "_not_in", "_ilike", "_regex", "bogus"} {
		t.Run(op, func(t *testing.T) {
			_, err := datadogMatcherClause(LabelMatcher{Label: "k", Operator: op, Value: "v"})
			require.Error(t, err)
		})
	}
}

// ---- Splunk SignalFlow ----

func TestSplunkGetQuery_HonorsLabelMatchers(t *testing.T) {
	s := &SplunkMetricSource{}
	req := FetchMetricsRequest{
		Queries: map[string]string{"k": "system.cpu.utilization"},
		LabelMatchers: []LabelMatcher{
			{Label: "host", Operator: "_eq", Value: "web-01"},
		},
	}
	out, err := s.GetQuery(nil, req)
	require.NoError(t, err)
	assert.Contains(t, out, ".filter(dimension('host', 'web-01'))")
	assert.True(t, strings.HasSuffix(out, ".mean().publish()"))
}

func TestSplunkMatcher_OperatorCoverage(t *testing.T) {
	cases := []struct {
		op       string
		expected string
	}{
		{"_eq", "dimension('k', 'v')"},
		{"_neq", "not dimension('k', 'v')"},
		{"_like", "dimension('k', 'v')"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			out, err := splunkMatcherClause(LabelMatcher{Label: "k", Operator: tc.op, Value: "v"})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, out)
		})
	}
}

func TestSplunkMatcher_RejectsUnshapedOps(t *testing.T) {
	for _, op := range []string{"_in", "_not_in", "_ilike", "_contains", "_regex", "bogus"} {
		t.Run(op, func(t *testing.T) {
			_, err := splunkMatcherClause(LabelMatcher{Label: "k", Operator: op, Value: "v"})
			require.Error(t, err)
		})
	}
}

// ---- Value escaping: lock down the injection boundary per provider ----

// Each provider has a different quoting style; verify that a value containing
// the provider's quote character is escaped, not concatenated raw.
func TestMatcherClauses_EscapeValueQuote(t *testing.T) {
	t.Run("nrql_single_quote", func(t *testing.T) {
		out, err := nrqlMatcherClause(LabelMatcher{Label: "k", Operator: "_eq", Value: "a'b"})
		require.NoError(t, err)
		// escapeNRQLValue turns ' into \'
		assert.Equal(t, "k = 'a\\'b'", out)
	})
	t.Run("dql_double_quote", func(t *testing.T) {
		out, err := dqlMatcherClause(LabelMatcher{Label: "k", Operator: "_eq", Value: `a"b`})
		require.NoError(t, err)
		assert.Equal(t, "`k`==\"a\\\"b\"", out)
	})
	t.Run("splunk_single_quote", func(t *testing.T) {
		out, err := splunkMatcherClause(LabelMatcher{Label: "k", Operator: "_eq", Value: "a'b"})
		require.NoError(t, err)
		assert.Equal(t, "dimension('k', 'a\\'b')", out)
	})
}

// ---- End-to-end: GetMetricsQuery routes per-item matchers to provider GetQuery ----

// This is the real-world scenario the user reported: metrics_get_query for
// NewRelic with QueryItems carrying label_matchers must surface those matchers
// in the rendered NRQL. Before the fix, the WHERE clause was silently dropped.
func TestGetMetricsQuery_NewRelic_PerItemMatchers_E2E(t *testing.T) {
	t.Skip("requires DB-backed integration; per-provider GetQuery and matcher tests above cover the rendering path that GetMetricsQuery routes through")
}
