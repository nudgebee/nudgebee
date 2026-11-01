package observability

import (
	"encoding/json"
	"testing"

	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traceProviders is every provider getTraceSource dispatches on. getTraceSource is a
// switch, so this list is hand-maintained: add a provider here when you add a case there.
var traceProviders = []string{
	"datadog", "azure_app_insights", "chronosphere", "otel_clickhouse", "jaeger", "newrelic",
	"splunk_observability_platform", "splunk_enterprise", "grafana_tempo", "cubeapm", "ES",
	"dynatrace", "solarwinds", "gcp", "openobserve",
}

// Every trace provider must say what a trace_source filter means for it. A provider that
// declares neither silently sends the field to a backend that may not have it — the bug
// that left every Elasticsearch service map empty.
func TestEveryTraceProviderDeclaresTraceSource(t *testing.T) {
	for _, p := range traceProviders {
		_, userErr := getTraceSource(p, "user")
		_, agentErr := getTraceSource(p, "agent")
		require.True(t, userErr == nil || agentErr == nil, "%s is listed but getTraceSource has no reader for it", p)

		caps, ok := allProviderCaps[p]
		require.True(t, ok, "%s has no allProviderCaps entry", p)
		assert.True(t, (caps.TraceSource != "") != caps.TraceSourcePassthrough,
			"%s must set exactly one of TraceSource and TraceSourcePassthrough", p)
	}
	// The reverse direction: a capability entry that getTraceSource serves must be listed.
	listed := map[string]bool{}
	for _, p := range traceProviders {
		listed[p] = true
	}
	for p := range allProviderCaps {
		_, userErr := getTraceSource(p, "user")
		_, agentErr := getTraceSource(p, "agent")
		if userErr == nil || agentErr == nil {
			assert.True(t, listed[p], "getTraceSource serves %s; add it to traceProviders", p)
		}
	}
}

func whereJSON(t *testing.T, raw string) query.QueryWhereClause {
	t.Helper()
	var w query.QueryWhereClause
	require.NoError(t, json.Unmarshal([]byte(raw), &w))
	return w
}

func toJSON(t *testing.T, w query.QueryWhereClause) string {
	t.Helper()
	b, err := json.Marshal(w)
	require.NoError(t, err)
	return string(b)
}

// The predicate semantics, against the constant every span of a declaring provider carries.
// These are the cases OpenObserve, Tempo and Splunk Enterprise each tested on their own copy.
func TestResolveTraceSourceFilter_Predicates(t *testing.T) {
	holds := []map[query.BinaryWhereClauseType]any{
		{query.Eq: "otel"},
		{query.Eq: "OTel"},
		{query.Nq: "ebpf"},
		{query.In: []any{"ebpf", "otel"}},
		{query.In: "ebpf,otel"},
		{query.NotIn: []any{"ebpf"}},
	}
	never := []map[query.BinaryWhereClauseType]any{
		{query.Eq: "ebpf"},
		{query.Nq: "otel"},
		{query.In: []any{"ebpf"}},
		{query.NotIn: []any{"otel"}},
	}
	for _, provider := range []string{"ES", "datadog", "newrelic", "openobserve", "grafana_tempo", "splunk_enterprise", "cubeapm"} {
		for _, ops := range holds {
			where := query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": ops, "workload_name": {query.Eq: "cart"}}}
			got, matchesNothing, err := resolveTraceSourceFilter(provider, where)
			require.NoError(t, err)
			assert.False(t, matchesNothing, "%s %v", provider, ops)
			assert.Equal(t, query.BinaryWhereClause{"workload_name": {query.Eq: "cart"}}, got.Binary,
				"%s: %v always holds, so only the other filter remains", provider, ops)
		}
		for _, ops := range never {
			_, matchesNothing, err := resolveTraceSourceFilter(provider, query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": ops}})
			require.NoError(t, err)
			assert.True(t, matchesNothing, "%s: %v can never match", provider, ops)
		}
	}

	// A nil value names no source: equality can never hold, exclusion always does.
	_, matchesNothing, err := resolveTraceSourceFilter("ES", query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": {query.Eq: nil}}})
	require.NoError(t, err)
	assert.True(t, matchesNothing)
	_, matchesNothing, err = resolveTraceSourceFilter("ES", query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": {query.NotIn: nil}}})
	require.NoError(t, err)
	assert.False(t, matchesNothing)

	_, _, err = resolveTraceSourceFilter("ES", query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": {query.Like: "o%"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trace_source supports")
}

func TestResolveTraceSourceFilter_Tree(t *testing.T) {
	cases := []struct {
		name, where, want string
		matchesNothing    bool
	}{
		{"service map's top-level otel", `{"_binary":{"trace_source":{"_eq":"otel"},"workload_name":{"_eq":"a"}}}`,
			`{"_binary":{"workload_name":{"_eq":"a"}}}`, false},
		{"otel alone leaves no restriction", `{"_binary":{"trace_source":{"_eq":"otel"}}}`, `{}`, false},
		{"default filters wrap the request in _and", `{"_and":[{"_binary":{"trace_source":{"_eq":"otel"},"workload_name":{"_eq":"a"}}},{"_binary":{"workload_namespace":{"_eq":"demo"}}}]}`,
			`{"_and":[{"_binary":{"workload_name":{"_eq":"a"}}},{"_binary":{"workload_namespace":{"_eq":"demo"}}}]}`, false},
		{"never inside _and rules out everything", `{"_and":[{"_binary":{"trace_source":{"_eq":"ebpf"}}},{"_binary":{"x":{"_eq":"1"}}}]}`, `{}`, true},
		{"never branch of _or is dropped", `{"_or":[{"_binary":{"trace_source":{"_eq":"ebpf"}}},{"_binary":{"x":{"_eq":"1"}}}]}`,
			`{"_or":[{"_binary":{"x":{"_eq":"1"}}}]}`, false},
		{"every _or branch never", `{"_or":[{"_binary":{"trace_source":{"_eq":"ebpf"}}},{"_binary":{"trace_source":{"_in":["ebpf"]}}}]}`, `{}`, true},
		{"an always branch makes the _or always", `{"_or":[{"_binary":{"trace_source":{"_eq":"otel"}}},{"_binary":{"x":{"_eq":"1"}}}],"_binary":{"y":{"_eq":"2"}}}`,
			`{"_binary":{"y":{"_eq":"2"}}}`, false},
		// ¬(false ∧ x=1) is true for every span, not ¬(x=1).
		{"_not of a never group is always", `{"_not":{"_binary":{"trace_source":{"_eq":"ebpf"},"x":{"_eq":"1"}}}}`, `{}`, false},
		{"_not keeps the rest of an otel group", `{"_not":{"_binary":{"trace_source":{"_eq":"otel"},"x":{"_eq":"1"}}}}`,
			`{"_not":{"_binary":{"x":{"_eq":"1"}}}}`, false},
		{"_not of an always group is never", `{"_not":{"_binary":{"trace_source":{"_eq":"otel"}}}}`, `{}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, matchesNothing, err := resolveTraceSourceFilter("ES", whereJSON(t, tc.where))
			require.NoError(t, err)
			assert.Equal(t, tc.matchesNothing, matchesNothing)
			if !matchesNothing {
				assert.JSONEq(t, tc.want, toJSON(t, got))
			}
		})
	}
}

// Providers that evaluate trace_source themselves (ClickHouse) or ignore it today
// (Chronosphere, Jaeger, GCP) receive the clause unchanged — including eBPF filters.
func TestResolveTraceSourceFilter_PassthroughAndAbsent(t *testing.T) {
	where := query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": {query.Eq: "ebpf"}}}
	for _, provider := range []string{"otel_clickhouse", "chronosphere", "jaeger", "gcp", "not-a-provider"} {
		got, matchesNothing, err := resolveTraceSourceFilter(provider, where)
		require.NoError(t, err)
		assert.False(t, matchesNothing, provider)
		assert.Equal(t, where, got, provider)
	}

	// A mapping that renamed trace_source to a real field: no longer the synthetic key.
	renamed := query.QueryWhereClause{Binary: query.BinaryWhereClause{"resource.attributes.otel@scope@name": {query.Eq: "nudgebee-node-agent"}}}
	got, matchesNothing, err := resolveTraceSourceFilter("ES", renamed)
	require.NoError(t, err)
	assert.False(t, matchesNothing)
	assert.Equal(t, renamed, got)
}

func TestApplyTraceSourceFilter_ExplainsAnImpossibleFilter(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("", nil, nil, nil)

	where := query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": {query.Eq: "ebpf"}}}
	msg, err := applyTraceSourceFilter(ctx, "acct", "datadog", &where)
	require.NoError(t, err)
	assert.Contains(t, msg, "datadog holds no eBPF spans")

	where = query.QueryWhereClause{Binary: query.BinaryWhereClause{"trace_source": {query.Eq: "otel"}, "workload_name": {query.Eq: "a"}}}
	msg, err = applyTraceSourceFilter(ctx, "acct", "ES", &where)
	require.NoError(t, err)
	assert.Empty(t, msg)
	assert.Equal(t, query.BinaryWhereClause{"workload_name": {query.Eq: "a"}}, where.Binary, "the filter is resolved in place")
}
