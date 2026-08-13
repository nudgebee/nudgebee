package observability

import (
	"testing"

	"nudgebee/services/common"
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

// TestAndWhereClause covers the shared AND-wrap used by both FetchLogs and
// GetLogsQuery (the SQL-preview endpoint that seeds the log builder's raw-query
// editor) — regressions here would silently drop the default filter from
// whichever path stops calling it.
func TestAndWhereClause(t *testing.T) {
	existing := query.QueryWhereClause{Binary: query.BinaryWhereClause{"namespace": {query.Eq: "shop"}}}
	defaults := buildDefaultFilterClause([]defaultFilterRow{{Key: "cluster_id", Value: "nudgebee"}})
	require.False(t, isEmptyWhereClause(defaults))

	t.Run("both empty yields empty", func(t *testing.T) {
		assert.True(t, isEmptyWhereClause(andWhereClause(query.QueryWhereClause{}, query.QueryWhereClause{})))
	})

	t.Run("no existing where, defaults present -> defaults only", func(t *testing.T) {
		result := andWhereClause(query.QueryWhereClause{}, defaults)
		assert.Equal(t, defaults, result)
	})

	t.Run("existing where, no defaults -> existing untouched", func(t *testing.T) {
		result := andWhereClause(existing, query.QueryWhereClause{})
		assert.Equal(t, existing, result)
	})

	t.Run("both present -> AND-ed, both predicates reach the SQL", func(t *testing.T) {
		result := andWhereClause(existing, defaults)
		sql, err := buildPinotWhereClause(result)
		require.NoError(t, err)
		assert.Contains(t, sql, `"namespace" = 'shop'`)
		assert.Contains(t, sql, `"cluster_id" = 'nudgebee'`)
		assert.Contains(t, sql, " AND ")
	})
}

func TestDefaultLogFiltersCacheKeyDiffersByProvider(t *testing.T) {
	// Same account, different explicit provider override, must not collide —
	// otherwise querying a non-default log integration would read (and cache)
	// another integration's default filters.
	a := defaultLogFiltersCacheKey("acc-1", "pinot", "")
	b := defaultLogFiltersCacheKey("acc-1", "loki", "")
	assert.NotEqual(t, a, b)
}

// TestInvalidateDefaultLogFiltersCache mirrors the save-config path: entries
// cached under different provider overrides for the same account must all be
// dropped by a single invalidation call keyed only on accountId, so a saved
// filter takes effect immediately instead of waiting out the cache TTL.
func TestInvalidateDefaultLogFiltersCache(t *testing.T) {
	accountId := "acc-invalidate-test"
	keyA := defaultLogFiltersCacheKey(accountId, "pinot", "")
	keyB := defaultLogFiltersCacheKey(accountId, "", "agent")
	tag := defaultLogFiltersAccountTag(accountId)

	require.NoError(t, common.CacheSet(logDefaultFiltersCacheNamespace, keyA, []byte("{}"), common.CacheSetWithTags(tag)))
	require.NoError(t, common.CacheSet(logDefaultFiltersCacheNamespace, keyB, []byte("{}"), common.CacheSetWithTags(tag)))

	_, okA := common.CacheGet(logDefaultFiltersCacheNamespace, keyA)
	_, okB := common.CacheGet(logDefaultFiltersCacheNamespace, keyB)
	require.True(t, okA)
	require.True(t, okB)

	InvalidateDefaultLogFiltersCache(accountId)

	_, okA = common.CacheGet(logDefaultFiltersCacheNamespace, keyA)
	_, okB = common.CacheGet(logDefaultFiltersCacheNamespace, keyB)
	assert.False(t, okA, "entry cached under provider override should be invalidated")
	assert.False(t, okB, "entry cached under a different provider override should also be invalidated")
}
