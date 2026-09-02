package observability

import (
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traceFilterClause is the standing clause an operator configuring
// `service_name = checkout` on the trace integration ends up with.
func traceFilterClause(rows ...defaultFilterRow) query.QueryWhereClause {
	return buildDefaultFilterClause(rows)
}

// TestApplyDefaultTraceFiltersNoConfig pins the contract the issue calls out: an
// account with nothing configured must produce a byte-identical request to today.
// Resolution fails open to an empty clause, so this is the shape every unconfigured
// account hits on every trace query.
func TestApplyDefaultTraceFiltersNoConfig(t *testing.T) {
	original := TracesV3Request{
		AccountId:    "acc-1",
		ProviderType: "otel_clickhouse",
		StartTime:    1700996400000,
		EndTime:      1701000000000,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "checkout"}}},
			Limit: 100,
		},
	}

	t.Run("empty defaults leave the request untouched", func(t *testing.T) {
		req := original
		require.NoError(t, applyDefaultTraceFilters(&req, query.QueryWhereClause{}))
		assert.Equal(t, original, req)
	})

	t.Run("no account short-circuits before any lookup", func(t *testing.T) {
		req := original
		req.AccountId = ""
		before := req
		require.NoError(t, ApplyDefaultTraceFilters(security.NewRequestContextForSuperAdmin(nil, nil, nil), &req))
		assert.Equal(t, before, req)
	})

	t.Run("a raw query is not refused when no filter is configured", func(t *testing.T) {
		req := original
		req.Query = "SELECT * FROM traces_view"
		before := req
		require.NoError(t, applyDefaultTraceFilters(&req, query.QueryWhereClause{}))
		assert.Equal(t, before, req)
	})
}

// TestApplyDefaultTraceFiltersANDsIntoWhere covers the injection itself, including
// the case where the caller supplied no where clause of its own.
func TestApplyDefaultTraceFiltersANDsIntoWhere(t *testing.T) {
	defaults := traceFilterClause(defaultFilterRow{Key: "service_name", Value: "checkout"})
	require.False(t, isEmptyWhereClause(defaults))

	t.Run("existing clause is AND-ed with the standing filter", func(t *testing.T) {
		req := TracesV3Request{AccountId: "acc-1", QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "cart"}}},
		}}
		require.NoError(t, applyDefaultTraceFilters(&req, defaults))

		require.Len(t, req.QueryRequest.Where.And, 2)
		assert.Equal(t, "cart", req.QueryRequest.Where.And[0].Binary["workload_name"][query.Eq])
		assert.Equal(t, "checkout", req.QueryRequest.Where.And[1].Binary["service_name"][query.Eq])
	})

	t.Run("no existing clause yields the standing filter alone", func(t *testing.T) {
		req := TracesV3Request{AccountId: "acc-1"}
		require.NoError(t, applyDefaultTraceFilters(&req, defaults))
		assert.Equal(t, defaults, req.QueryRequest.Where)
	})
}

// TestApplyDefaultTraceFiltersFailsClosedOnRawQuery covers the raw-provider-query
// paths (raw ClickHouse SQL, a Datadog query string, Chronosphere JSON). The string
// runs verbatim, so there is no clause to scope — running it would silently return
// spans from outside the operator's scope.
func TestApplyDefaultTraceFiltersFailsClosedOnRawQuery(t *testing.T) {
	defaults := traceFilterClause(
		defaultFilterRow{Key: "service_name", Value: "checkout"},
		defaultFilterRow{Key: "workload_namespace", Value: "prod"},
	)

	req := TracesV3Request{AccountId: "acc-1", Query: "SELECT * FROM traces_view LIMIT 10"}
	err := applyDefaultTraceFilters(&req, defaults)

	require.Error(t, err)
	// The message must name the filter, so the reader can tell an unexpectedly
	// refused query from a broken one.
	assert.Contains(t, err.Error(), "service_name=checkout")
	assert.Contains(t, err.Error(), "workload_namespace=prod")
	assert.Contains(t, err.Error(), "traces_execute_v2")
	// The request is left alone — nothing half-applied.
	assert.Equal(t, "SELECT * FROM traces_view LIMIT 10", req.Query)
	assert.True(t, isEmptyWhereClause(req.QueryRequest.Where))
}

// TestDescribeTraceFilterClauseIsStable guards the error message against Go's
// randomised map iteration: an error that reads differently on every call is not
// something support can match against.
func TestDescribeTraceFilterClauseIsStable(t *testing.T) {
	clause := traceFilterClause(
		defaultFilterRow{Key: "workload_namespace", Value: "prod"},
		defaultFilterRow{Key: "service_name", Value: "checkout"},
	)
	first := describeTraceFilterClause(clause)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, describeTraceFilterClause(clause))
	}
	assert.Equal(t, "service_name=checkout, workload_namespace=prod", first)
}

// TestDefaultTraceFiltersAreCanonical is the reason trace filters are injected
// BEFORE convertWhereClauseWithMApping while log filters are injected after: they
// are stored as canonical field names, so they must be translated per provider like
// every other clause. Storing provider-native names instead would break the moment
// the account switched backends.
func TestDefaultTraceFiltersAreCanonical(t *testing.T) {
	req := TracesV3Request{AccountId: "acc-1"}
	require.NoError(t, applyDefaultTraceFilters(&req, traceFilterClause(
		defaultFilterRow{Key: "workload_name", Value: "checkout"},
	)))

	mapped := convertWhereClauseWithMApping(req.QueryRequest.Where, dynatraceTraceLabelMapping)
	assert.Equal(t, "checkout", mapped.Binary["k8s.workload.name"][query.Eq],
		"canonical workload_name should have been translated to Dynatrace's field")
	assert.NotContains(t, mapped.Binary, "workload_name")
}

// TestDefaultTraceFilterReachesProviderQuery follows one configured filter all the
// way to the SQL a provider actually runs — the step that would silently do nothing
// if the clause were injected after the mapping or dropped by the query builder.
func TestDefaultTraceFilterReachesProviderQuery(t *testing.T) {
	req := TracesV3Request{
		AccountId: "acc-1",
		StartTime: 1700996400000,
		EndTime:   1701000000000,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "cart"}}},
			Limit: 10,
		},
	}
	require.NoError(t, applyDefaultTraceFilters(&req, traceFilterClause(
		defaultFilterRow{Key: "service_name", Value: "checkout"},
	)))

	source := &OtelClickhouseTraceSource{}
	req.QueryRequest.Where = convertWhereClauseWithMApping(req.QueryRequest.Where, source.GetLabelMapping())

	sql, err := source.buildTraceSQL(security.NewRequestContextForSuperAdmin(nil, nil, nil), req)
	require.NoError(t, err)
	assert.Contains(t, sql, "checkout", "the standing filter must reach the provider SQL")
	assert.Contains(t, sql, "cart", "the caller's own filter must survive alongside it")
}

func TestDefaultTraceFiltersCacheKeyDiffersByProviderAndSource(t *testing.T) {
	// Same account, different explicit provider/source override, must not collide —
	// otherwise querying a non-default trace integration would read (and cache)
	// another integration's standing filters.
	assert.NotEqual(t,
		defaultTraceFiltersCacheKey("acc-1", "otel_clickhouse", ""),
		defaultTraceFiltersCacheKey("acc-1", "datadog", ""))
	assert.NotEqual(t,
		defaultTraceFiltersCacheKey("acc-1", "ES", "agent"),
		defaultTraceFiltersCacheKey("acc-1", "ES", "user"))
}

// TestDefaultTraceFiltersCacheIsSeparateFromLogs guards the config-key decision: a
// shared cache namespace would let a log filter answer a trace lookup even though
// the two configs are distinct.
func TestDefaultTraceFiltersCacheIsSeparateFromLogs(t *testing.T) {
	assert.NotEqual(t, logDefaultFiltersCacheNamespace, traceDefaultFiltersCacheNamespace)
	assert.NotEqual(t, defaultFiltersConfigName, defaultTraceFiltersConfigName)
}

// TestInvalidateDefaultTraceFiltersCache mirrors the integration-save path: entries
// cached under different provider overrides for the same account must all be dropped
// by a single invalidation call keyed only on accountId, so a saved filter takes
// effect immediately instead of waiting out the 10 minute TTL.
func TestInvalidateDefaultTraceFiltersCache(t *testing.T) {
	accountId := "acc-trace-invalidate-test"
	keyA := defaultTraceFiltersCacheKey(accountId, "otel_clickhouse", "agent")
	keyB := defaultTraceFiltersCacheKey(accountId, "datadog", "user")
	tag := defaultTraceFiltersAccountTag(accountId)

	require.NoError(t, common.CacheSet(traceDefaultFiltersCacheNamespace, keyA, []byte("{}"), common.CacheSetWithTags(tag)))
	require.NoError(t, common.CacheSet(traceDefaultFiltersCacheNamespace, keyB, []byte("{}"), common.CacheSetWithTags(tag)))

	_, okA := common.CacheGet(traceDefaultFiltersCacheNamespace, keyA)
	_, okB := common.CacheGet(traceDefaultFiltersCacheNamespace, keyB)
	require.True(t, okA)
	require.True(t, okB)

	InvalidateDefaultTraceFiltersCache(accountId)

	_, okA = common.CacheGet(traceDefaultFiltersCacheNamespace, keyA)
	_, okB = common.CacheGet(traceDefaultFiltersCacheNamespace, keyB)
	assert.False(t, okA, "entry cached under one provider override should be invalidated")
	assert.False(t, okB, "entry cached under a different provider override should also be invalidated")
}

// TestInvalidateDefaultTraceFiltersCacheLeavesLogsAlone: the two caches are
// invalidated side by side from invalidateIntegrationCaches, so a namespace mix-up
// would be invisible there — pin it here instead.
func TestInvalidateDefaultTraceFiltersCacheLeavesLogsAlone(t *testing.T) {
	accountId := "acc-trace-vs-log-invalidate"
	logKey := defaultLogFiltersCacheKey(accountId, "pinot", "")
	traceKey := defaultTraceFiltersCacheKey(accountId, "otel_clickhouse", "agent")

	require.NoError(t, common.CacheSet(logDefaultFiltersCacheNamespace, logKey, []byte("{}"),
		common.CacheSetWithTags(defaultLogFiltersAccountTag(accountId))))
	require.NoError(t, common.CacheSet(traceDefaultFiltersCacheNamespace, traceKey, []byte("{}"),
		common.CacheSetWithTags(defaultTraceFiltersAccountTag(accountId))))

	InvalidateDefaultTraceFiltersCache(accountId)

	_, logStillCached := common.CacheGet(logDefaultFiltersCacheNamespace, logKey)
	_, traceGone := common.CacheGet(traceDefaultFiltersCacheNamespace, traceKey)
	assert.True(t, logStillCached, "invalidating trace filters must not drop the log filter cache")
	assert.False(t, traceGone)
}
