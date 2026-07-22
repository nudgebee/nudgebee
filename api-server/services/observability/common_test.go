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
