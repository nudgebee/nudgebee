package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
)

// TestConvertWhereClauseWithMapping_PreservesNot guards the provider-independent fix that
// the Not branch is copied/recursed (it used to be silently dropped, so a `_not` clause
// lost its negation for every provider before any native query builder ran).
func TestConvertWhereClauseWithMapping_PreservesNot(t *testing.T) {
	mapping := map[string]string{"namespace": "kube_namespace"}

	t.Run("top-level Not is preserved and its labels are mapped", func(t *testing.T) {
		where := query.QueryWhereClause{
			Not: &query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"namespace": {Eq: "kube-system"},
				},
			},
		}
		got := convertWhereClauseWithMApping(where, mapping)
		assert.NotNil(t, got.Not, "Not branch must survive the conversion")
		_, mapped := got.Not.Binary["kube_namespace"]
		assert.True(t, mapped, "label inside Not must be mapped namespace -> kube_namespace")
	})

	t.Run("Not nested inside And is preserved", func(t *testing.T) {
		where := query.QueryWhereClause{
			And: []query.QueryWhereClause{
				{Binary: query.BinaryWhereClause{"service": {Eq: "api"}}},
				{Not: &query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {Eq: "kube-system"}}}},
			},
		}
		got := convertWhereClauseWithMApping(where, mapping)
		assert.Len(t, got.And, 2)
		assert.NotNil(t, got.And[1].Not, "Not nested in And must survive")
	})

	t.Run("nil Not stays nil", func(t *testing.T) {
		where := query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"service": {Eq: "api"}},
		}
		got := convertWhereClauseWithMApping(where, mapping)
		assert.Nil(t, got.Not)
	})
}

// A single-select UI filter sends `_in` a bare scalar. Every logs and traces request
// passes through convertWhereClauseWithMApping, so normalising it here is what keeps
// each provider from having to grow its own scalar arm (#41493, #41419).
func TestConvertWhereClauseWithMapping_WrapsScalarInOperand(t *testing.T) {
	mapping := map[string]string{"workload_name": "service.name"}

	got := convertWhereClauseWithMApping(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"workload_name": {query.In: "qa-payment-s3"}},
		And: []query.QueryWhereClause{{
			Binary: query.BinaryWhereClause{"workload_name": {query.NotIn: "qa-ledger"}},
		}},
		Not: &query.QueryWhereClause{
			Binary: query.BinaryWhereClause{"workload_name": {query.In: 7}},
		},
	}, mapping)

	assert.Equal(t, []any{"qa-payment-s3"}, got.Binary["service.name"][query.In])
	assert.Equal(t, []any{"qa-ledger"}, got.And[0].Binary["service.name"][query.NotIn])
	// Non-string scalars travel the same way; each builder renders the element.
	assert.Equal(t, []any{7}, got.Not.Binary["service.name"][query.In])
}

// What the normalisation must NOT touch: a nil operand (its "no values" refusal belongs to
// the builder) and every other operator. A []any list is returned as-is; a []string list is
// retyped so the operand of an _in is always a []any past this point.
func TestConvertWhereClauseWithMapping_LeavesOtherOperandsAlone(t *testing.T) {
	got := convertWhereClauseWithMApping(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"a": {query.In: []any{"x", "y"}},
			"b": {query.In: []string{"x"}},
			"c": {query.In: nil},
			"d": {query.Eq: "x"},
			"e": {query.Like: "%x%"},
			"f": {query.Gte: 300},
		},
	}, map[string]string{})

	assert.Equal(t, []any{"x", "y"}, got.Binary["a"][query.In])
	// Retyped, not passed through: swInValues asserts []any alone and drops anything else.
	assert.Equal(t, []any{"x"}, got.Binary["b"][query.In])
	assert.Nil(t, got.Binary["c"][query.In])
	assert.Equal(t, "x", got.Binary["d"][query.Eq])
	assert.Equal(t, "%x%", got.Binary["e"][query.Like])
	assert.Equal(t, 300, got.Binary["f"][query.Gte])
}
