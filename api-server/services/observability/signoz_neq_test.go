package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SigNoz's `!=` is a SUBSTRING match on resource attributes while `=` is exact,
// so it must never reach the backend. Measured on v0.145 against a 15-row set:
//
//	service.name != "e2e"        -> 0 of 15   (excluded all three e2e-* services)
//	k8s.pod.name != "checkout"   -> 10 of 15  (excluded the checkout-* pod)
//	namespace    != "nb-e2e"     -> 0 of 5    (excluded nb-e2e-edge too)
//
// A user filtering `namespace != prod` would silently also lose `prod-east`.
// `nin` with one element measured exact on every field class — resource
// attribute, top-level column and log attribute — so signozLeaf emits that.
//
// This test asserts the RULE rather than one expected output: no clause built
// from any supported operator, through any composition, may carry "!=".
func TestSignozLeaf_NeverEmitsNotEquals(t *testing.T) {
	cases := map[string]query.QueryWhereClause{
		"direct !=":            bin("k8s.namespace.name", Nq, "nb-e2e"),
		"NOT over =":           {Not: &query.QueryWhereClause{Binary: query.BinaryWhereClause{"service.name": {Eq: "api"}}}},
		"NOT over an OR":       {Not: &query.QueryWhereClause{Or: []query.QueryWhereClause{bin("a", Eq, "1"), bin("b", Eq, "2")}}},
		"NOT over an AND":      {Not: &query.QueryWhereClause{And: []query.QueryWhereClause{bin("a", Eq, "1"), bin("b", Eq, "2")}}},
		"!= on severity":       bin(signozSeverityKey, Nq, "ERROR"),
		"!= inside a compound": {And: []query.QueryWhereClause{bin("ns", Eq, "x"), bin("pod", Nq, "y")}},
		"!= with a list value": bin("ns", Nq, []any{"a", "b"}),
	}
	for name, where := range cases {
		t.Run(name, func(t *testing.T) {
			terms, err := signozDNF(where)
			require.NoError(t, err)
			require.NotEmpty(t, terms)
			for _, term := range terms {
				for _, clause := range term {
					assert.NotEqual(t, "!=", clause.Op,
						"%s produced a != filter; SigNoz matches that as a substring on resource attributes", name)
				}
			}
		})
	}
}

// The rewrite must preserve everything except the operator: same field, and the
// value carried as the single-element list `nin` expects.
func TestSignozLeaf_NeqBecomesSingleValueNotIn(t *testing.T) {
	clause, err := signozLeaf("k8s.namespace.name", Nq, "nb-e2e")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"key": "k8s.namespace.name", "type": "resource", "dataType": "string"}, clause.Key)
	assert.Equal(t, "nin", clause.Op)
	assert.Equal(t, []any{"nb-e2e"}, clause.Value)

	t.Run("a list value is not nested, and still becomes nin", func(t *testing.T) {
		// A caller may write _neq with a list ({"ns": {"_neq": ["a","b"]}}). The
		// list must not be wrapped again, and the operator must still be rewritten:
		// making the whole rewrite conditional on a non-list value left "!=" reachable
		// for exactly this input.
		clause, err := signozLeaf("some.field", Nq, []any{"a", "b"})
		require.NoError(t, err)
		assert.Equal(t, "nin", clause.Op)
		assert.Equal(t, []any{"a", "b"}, clause.Value)
	})

	t.Run("_not_in is unchanged", func(t *testing.T) {
		clause, err := signozLeaf("ns", NotIn, []any{"x"})
		require.NoError(t, err)
		assert.Equal(t, "nin", clause.Op)
		assert.Equal(t, []any{"x"}, clause.Value)
	})
}

// Keys our own mapping produces are OTel resource attributes and must be sent
// typed: an untyped key that SigNoz also knows as a log attribute (k8s event logs
// carry k8s.namespace.name) resolves to the log attribute and misses the rows
// that hold it as a resource attribute. Columns and unmapped keys stay untyped.
func TestSignozLeaf_TypesResourceKeys(t *testing.T) {
	typed := map[string]string{"type": "resource", "dataType": "string"}
	resourceKeys := append([]string{signozNamespaceKey, signozPodKey, "service.name", "k8s.container.name",
		"k8s.node.name", "k8s.cluster.name", "host.name"}, signozWorkloadKeys...)
	for _, key := range resourceKeys {
		clause, err := signozLeaf(key, Eq, "x")
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"key": key, "type": typed["type"], "dataType": typed["dataType"]}, clause.Key, key)
	}
	for _, key := range []string{"body", signozSeverityKey, "user.tier", "trace_id"} {
		value := any("x")
		if key == signozSeverityKey {
			value = "ERROR"
		}
		clause, err := signozLeaf(key, Eq, value)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"key": key}, clause.Key, key)
	}

	// The workload query the logs action sends reaches SigNoz with both keys typed.
	terms, err := signozDNF(query.QueryWhereClause{And: []query.QueryWhereClause{
		bin("k8s.deployment.name", Eq, "checkout"), bin(signozNamespaceKey, Eq, "shop"),
	}})
	require.NoError(t, err)
	require.Len(t, terms, 1)
	require.Len(t, terms[0], 2)
	for _, clause := range terms[0] {
		assert.Equal(t, "resource", clause.Key["type"], clause.Key["key"])
		assert.Equal(t, "string", clause.Key["dataType"], clause.Key["key"])
	}
}
