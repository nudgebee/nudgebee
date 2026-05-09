package tools

import (
	"encoding/json"
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertESBoolQuery parses the ES DSL JSON and verifies filter/must_not clauses
// order-independently (map iteration in BinaryWhereClause is non-deterministic).
func assertESBoolQuery(t *testing.T, actualJSON string, wantFilter []map[string]any, wantMustNot []map[string]any, wantSize int) {
	t.Helper()

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(actualJSON), &result), "actual should be valid JSON")

	if wantSize > 0 {
		assert.Equal(t, float64(wantSize), result["size"], "size mismatch")
	}

	q, ok := result["query"].(map[string]any)
	require.True(t, ok, "query field must be present")
	boolQ, ok := q["bool"].(map[string]any)
	require.True(t, ok, "query.bool must be present")

	assertESClauses(t, boolQ, "filter", wantFilter)
	assertESClauses(t, boolQ, "must_not", wantMustNot)
}

// assertESClauses compares a bool sub-clause (filter/must_not) as an order-independent set
// by marshalling each element to a canonical JSON string.
func assertESClauses(t *testing.T, boolQ map[string]any, key string, want []map[string]any) {
	t.Helper()
	if len(want) == 0 {
		_, present := boolQ[key]
		assert.False(t, present, "%s should not be present when empty", key)
		return
	}

	arr, ok := boolQ[key].([]any)
	require.True(t, ok, "%s should be a JSON array, got %T", key, boolQ[key])
	require.Len(t, arr, len(want), "%s length mismatch", key)

	toJSONStrs := func(items []any) []string {
		strs := make([]string, len(items))
		for i, item := range items {
			b, _ := json.Marshal(item)
			strs[i] = string(b)
		}
		return strs
	}
	wantAny := make([]any, len(want))
	for i, m := range want {
		wantAny[i] = m
	}
	assert.ElementsMatch(t, toJSONStrs(wantAny), toJSONStrs(arr), "%s clauses mismatch", key)
}

func TestQueryBuilderToESQuery(t *testing.T) {
	tool := &NBLogTool{}
	type clause = map[string]any

	testCases := []struct {
		name        string
		builder     core.QueryBuilder
		wantFilter  []clause
		wantMustNot []clause
		wantSize    int
	}{
		// ── Binary operators ──────────────────────────────────────────────────
		{
			name: "_eq produces match_phrase",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"service": {core.Eq: "myapp"},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"service": "myapp"}},
			},
		},
		{
			name: "_neq produces must_not match_phrase",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"level": {core.Nq: "debug"},
					},
				},
			},
			wantMustNot: []clause{
				{"match_phrase": clause{"level": "debug"}},
			},
		},
		{
			name: "_ilike converts % to * with case_insensitive wildcard",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.pod_name.keyword": {core.ILike: "%api-server%"},
					},
				},
			},
			wantFilter: []clause{
				{"wildcard": clause{"kubernetes.pod_name.keyword": clause{"value": "*api-server*", "case_insensitive": true}}},
			},
		},
		{
			name: "_like converts % to * without case_insensitive",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.pod_name.keyword": {core.Like: "api-%"},
					},
				},
			},
			wantFilter: []clause{
				{"wildcard": clause{"kubernetes.pod_name.keyword": clause{"value": "api-*"}}},
			},
		},
		{
			name: "_ilike without wildcards falls back to match_phrase",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.labels.app_kubernetes_io/name": {core.ILike: "llm-server"},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.labels.app_kubernetes_io/name": "llm-server"}},
			},
		},
		{
			name: "_like without wildcards falls back to match_phrase",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.namespace_name": {core.Like: "nudgebee"},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "nudgebee"}},
			},
		},
		{
			name: "_nlike without wildcards falls back to must_not match_phrase",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"level": {core.NLike: "debug"},
					},
				},
			},
			wantMustNot: []clause{
				{"match_phrase": clause{"level": "debug"}},
			},
		},
		{
			name: "_nlike produces must_not wildcard (case-sensitive, no case_insensitive flag)",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"message": {core.NLike: "%debug%"},
					},
				},
			},
			wantMustNot: []clause{
				{"wildcard": clause{"message": clause{"value": "*debug*"}}},
			},
		},
		{
			name: "_in produces terms",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.namespace_name.keyword": {core.In: []any{"prod", "staging"}},
					},
				},
			},
			wantFilter: []clause{
				{"terms": clause{"kubernetes.namespace_name.keyword": []any{"prod", "staging"}}},
			},
		},
		{
			name: "_nin produces must_not terms",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.namespace_name.keyword": {core.NotIn: []any{"kube-system", "default"}},
					},
				},
			},
			wantMustNot: []clause{
				{"terms": clause{"kubernetes.namespace_name.keyword": []any{"kube-system", "default"}}},
			},
		},
		{
			name: "_gt produces range gt",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"response_time_ms": {core.Gt: 500},
					},
				},
			},
			wantFilter: []clause{
				{"range": clause{"response_time_ms": clause{"gt": 500}}},
			},
		},
		{
			name: "_gte produces range gte",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"status_code": {core.Gte: 400},
					},
				},
			},
			wantFilter: []clause{
				{"range": clause{"status_code": clause{"gte": 400}}},
			},
		},
		{
			name: "_lt produces range lt",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"status_code": {core.Lt: 300},
					},
				},
			},
			wantFilter: []clause{
				{"range": clause{"status_code": clause{"lt": 300}}},
			},
		},
		{
			name: "_lte produces range lte",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"response_time_ms": {core.Lte: 1000},
					},
				},
			},
			wantFilter: []clause{
				{"range": clause{"response_time_ms": clause{"lte": 1000}}},
			},
		},
		{
			name: "_is_null produces must_not exists",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"error_code": {core.IsNull: true},
					},
				},
			},
			wantMustNot: []clause{
				{"exists": clause{"field": "error_code"}},
			},
		},

		// ── _body aliasing ────────────────────────────────────────────────────
		{
			name: "_body _ilike aliases to message field",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"_body": {core.ILike: "%connection refused%"},
					},
				},
			},
			wantFilter: []clause{
				{"wildcard": clause{"message": clause{"value": "*connection refused*", "case_insensitive": true}}},
			},
		},
		{
			name: "_body _eq aliases to message field",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"_body": {core.Eq: "OOMKilled"},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"message": "OOMKilled"}},
			},
		},
		{
			name: "explicit message field passes through unchanged",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"message": {core.ILike: "%timeout%"},
					},
				},
			},
			wantFilter: []clause{
				{"wildcard": clause{"message": clause{"value": "*timeout*", "case_insensitive": true}}},
			},
		},

		// ── Limit / empty ─────────────────────────────────────────────────────
		{
			name: "limit sets size field",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"level": {core.Eq: "error"},
					},
				},
				Limit: 50,
			},
			wantFilter: []clause{
				{"match_phrase": clause{"level": "error"}},
			},
			wantSize: 50,
		},
		{
			name:    "empty where clause produces no filter or must_not",
			builder: core.QueryBuilder{},
		},

		// ── AND / OR / NOT ────────────────────────────────────────────────────
		{
			name: "AND clauses are merged into filter",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"kubernetes.namespace_name.keyword": {core.Eq: "production"}}},
						{Binary: core.BinaryWhereClause{"kubernetes.container_name.keyword": {core.Eq: "nginx"}}},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "production"}},
				{"match_phrase": clause{"kubernetes.container_name": "nginx"}},
			},
		},
		{
			name: "OR clauses produce bool should with minimum_should_match 1",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"message": {core.ILike: "%error%"}}},
						{Binary: core.BinaryWhereClause{"message": {core.ILike: "%warn%"}}},
					},
				},
			},
			wantFilter: []clause{
				{
					"bool": clause{
						"should": []any{
							clause{"wildcard": clause{"message": clause{"value": "*error*", "case_insensitive": true}}},
							clause{"wildcard": clause{"message": clause{"value": "*warn*", "case_insensitive": true}}},
						},
						"minimum_should_match": 1,
					},
				},
			},
		},
		{
			name: "NOT clause inverts filter to must_not",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Not: &core.QueryWhereClause{
						Binary: core.BinaryWhereClause{
							"kubernetes.namespace_name.keyword": {core.Eq: "kube-system"},
						},
					},
				},
			},
			wantMustNot: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "kube-system"}},
			},
		},
		{
			name: "NOT of a must_not becomes filter (double negation)",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Not: &core.QueryWhereClause{
						Binary: core.BinaryWhereClause{
							"level": {core.Nq: "debug"},
						},
					},
				},
			},
			// NOT(_neq) → filter (double negation cancels out)
			wantFilter: []clause{
				{"match_phrase": clause{"level": "debug"}},
			},
		},
		{
			name: "OR with must_not branch wraps it in bool.must_not inside should",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Or: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"level": {core.Eq: "error"}}},
						{Binary: core.BinaryWhereClause{"level": {core.Nq: "info"}}},
					},
				},
			},
			wantFilter: []clause{
				{
					"bool": clause{
						"should": []any{
							clause{"match_phrase": clause{"level": "error"}},
							clause{"bool": clause{"must_not": []any{
								clause{"match_phrase": clause{"level": "info"}},
							}}},
						},
						"minimum_should_match": 1,
					},
				},
			},
		},

		// ── Real-world combinations ───────────────────────────────────────────
		{
			name: "namespace + _body error search via AND",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"kubernetes.namespace_name.keyword": {core.Eq: "prod"}}},
						{Binary: core.BinaryWhereClause{"_body": {core.ILike: "%error%"}}},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "prod"}},
				{"wildcard": clause{"message": clause{"value": "*error*", "case_insensitive": true}}},
			},
		},
		{
			name: "pod prefix match with namespace filter",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"kubernetes.namespace_name.keyword": {core.Eq: "default"}}},
						{Binary: core.BinaryWhereClause{"kubernetes.pod_name.keyword": {core.ILike: "api-server-%"}}},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "default"}},
				{"wildcard": clause{"kubernetes.pod_name.keyword": clause{"value": "api-server-*", "case_insensitive": true}}},
			},
		},
		{
			name: "multiple binary fields all land in filter",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					Binary: core.BinaryWhereClause{
						"kubernetes.namespace_name.keyword": {core.Eq: "prod"},
						"kubernetes.container_name.keyword": {core.Eq: "nginx"},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "prod"}},
				{"match_phrase": clause{"kubernetes.container_name": "nginx"}},
			},
		},
		{
			name: "exclude namespace while matching error body",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"_body": {core.ILike: "%timeout%"}}},
						{
							Not: &core.QueryWhereClause{
								Binary: core.BinaryWhereClause{"kubernetes.namespace_name.keyword": {core.Eq: "kube-system"}},
							},
						},
					},
				},
			},
			wantFilter: []clause{
				{"wildcard": clause{"message": clause{"value": "*timeout*", "case_insensitive": true}}},
			},
			wantMustNot: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "kube-system"}},
			},
		},
		{
			name: "namespace filter with OR on severity levels",
			builder: core.QueryBuilder{
				Where: core.QueryWhereClause{
					And: []core.QueryWhereClause{
						{Binary: core.BinaryWhereClause{"kubernetes.namespace_name.keyword": {core.Eq: "prod"}}},
						{
							Or: []core.QueryWhereClause{
								{Binary: core.BinaryWhereClause{"level": {core.Eq: "error"}}},
								{Binary: core.BinaryWhereClause{"level": {core.Eq: "fatal"}}},
							},
						},
					},
				},
			},
			wantFilter: []clause{
				{"match_phrase": clause{"kubernetes.namespace_name": "prod"}},
				{
					"bool": clause{
						"should": []any{
							clause{"match_phrase": clause{"level": "error"}},
							clause{"match_phrase": clause{"level": "fatal"}},
						},
						"minimum_should_match": 1,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := tool.queryBuilderToESQuery(tc.builder)
			require.NoError(t, err)
			assertESBoolQuery(t, actual, tc.wantFilter, tc.wantMustNot, tc.wantSize)
		})
	}
}
