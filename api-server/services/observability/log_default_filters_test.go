package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildDefaultFilterClause(t *testing.T) {
	t.Run("single equality row becomes a Pinot predicate", func(t *testing.T) {
		clause := buildDefaultFilterClause([]defaultFilterRow{{Key: "cluster_id", Value: "nudgebee"}})
		sql, err := buildPinotWhereClause(clause)
		require.NoError(t, err)
		assert.Contains(t, sql, `"cluster_id" = 'nudgebee'`)
	})

	t.Run("multiple rows are AND-ed", func(t *testing.T) {
		clause := buildDefaultFilterClause([]defaultFilterRow{
			{Key: "cluster_id", Value: "nudgebee"},
			{Key: "env", Value: "prod"},
		})
		sql, err := buildPinotWhereClause(clause)
		require.NoError(t, err)
		assert.Contains(t, sql, `"cluster_id" = 'nudgebee'`)
		assert.Contains(t, sql, `"env" = 'prod'`)
		assert.Contains(t, sql, " AND ")
	})

	t.Run("op is normalized to equality for now", func(t *testing.T) {
		clause := buildDefaultFilterClause([]defaultFilterRow{{Key: "cluster_id", Op: "_in", Value: "nudgebee"}})
		sql, err := buildPinotWhereClause(clause)
		require.NoError(t, err)
		assert.Contains(t, sql, `"cluster_id" = 'nudgebee'`)
	})

	t.Run("non-string values (array/number) are skipped without panicking", func(t *testing.T) {
		clause := buildDefaultFilterClause([]defaultFilterRow{
			{Key: "x", Value: []any{"a", "b"}},
			{Key: "y", Value: 42.0},
			{Key: "cluster_id", Value: "nudgebee"},
		})
		sql, err := buildPinotWhereClause(clause)
		require.NoError(t, err)
		assert.Contains(t, sql, `"cluster_id" = 'nudgebee'`)
		assert.NotContains(t, sql, `"x"`)
		assert.NotContains(t, sql, `"y"`)
	})

	t.Run("empty/blank rows are skipped, yielding an empty clause", func(t *testing.T) {
		clause := buildDefaultFilterClause([]defaultFilterRow{
			{Key: "", Value: "x"},
			{Key: "cluster_id", Value: ""},
			{Key: "ns", Value: nil},
		})
		assert.True(t, isEmptyWhereClause(clause))
	})

	t.Run("nil input yields an empty clause", func(t *testing.T) {
		assert.True(t, isEmptyWhereClause(buildDefaultFilterClause(nil)))
	})
}

// TestDefaultFilterAndWrap mirrors the FetchLogs injection: AND the default clause
// into an existing user where-clause and confirm both predicates reach the SQL.
func TestDefaultFilterAndWrap(t *testing.T) {
	existing := query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "shop"}}}
	defaults := buildDefaultFilterClause([]defaultFilterRow{{Key: "cluster_id", Value: "nudgebee"}})
	require.False(t, isEmptyWhereClause(defaults))

	wrapped := query.QueryWhereClause{And: []query.QueryWhereClause{existing, defaults}}
	sql, err := buildPinotWhereClause(wrapped)
	require.NoError(t, err)
	assert.Contains(t, sql, `"namespace" = 'shop'`)
	assert.Contains(t, sql, `"cluster_id" = 'nudgebee'`)
	assert.Contains(t, sql, " AND ")
}
