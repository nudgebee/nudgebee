package observability

import (
	"strings"
	"testing"

	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOtelClickhouseBuildTraceSQL_CarriesTheTimeFilter pins the fidelity of the query we
// report to a reader. The SQL build used to live only inside QueryTraces, so GetQuery
// rendered the clause WITHOUT the injected time filter — meaning the traces UI "show query"
// surface, and now the `executed_query` stamped onto trace evidence, would display an
// unbounded full-table scan that no backend had ever run. Both callers go through
// buildTraceSQL precisely so that cannot drift apart again.
func TestOtelClickhouseBuildTraceSQL_CarriesTheTimeFilter(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	src := &OtelClickhouseTraceSource{}

	req := TracesV3Request{
		AccountId: "acct-1",
		StartTime: 1756000000000,
		EndTime:   1756000600000,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "cart"}},
			},
			Limit: 50,
		},
	}

	sql, err := src.buildTraceSQL(ctx, req)
	require.NoError(t, err)

	assert.Contains(t, sql, "workload_name", "the caller's filter must survive into the SQL")
	assert.Contains(t, strings.ToLower(sql), "timestamp",
		"the injected time filter must be part of the query we report, not just the one we run")
}

// TestOtelClickhouseBuildTraceSQL_DoesNotMutateCallersClause pins that building SQL leaves the
// caller's where clause alone. GetTraces calls this twice per query — once through QueryTraces
// to execute, once through GetQuery to record what ran — so a shared map would make the second
// call depend on the first's leftovers, and would be a concurrent map write if a request object
// were ever shared across goroutines.
func TestOtelClickhouseBuildTraceSQL_DoesNotMutateCallersClause(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	src := &OtelClickhouseTraceSource{}

	where := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"spanattributes": {query.Eq: map[string]interface{}{"service.name": "cart", "http.method": "GET"}},
		},
	}
	req := TracesV3Request{
		AccountId:    "acct-1",
		StartTime:    1756000000000,
		EndTime:      1756000600000,
		QueryRequest: TracesQueryBuilderRequest{Where: where},
	}

	first, err := src.buildTraceSQL(ctx, req)
	require.NoError(t, err)

	// The caller's clause still has exactly what it started with: no injected time filter,
	// and service.name still inside the spanattributes value map.
	_, injectedTime := where.Binary["timestamp"]
	assert.False(t, injectedTime, "the time filter must land on the copy, not the caller's clause")
	valueMap, ok := where.Binary["spanattributes"][query.Eq].(map[string]interface{})
	require.True(t, ok, "spanattributes entry should be intact")
	assert.Contains(t, valueMap, "service.name", "the strip must not reach the caller's nested map")

	// And because nothing leaked, building again from the same request filters identically —
	// the second call does not inherit the first's injected time filter or stripped attribute.
	// Compared on the WHERE clause alone: GenerateSqlQuery renders the SELECT list from a Go
	// map, so its column order is randomised per call and is not a stable equality target.
	second, err := src.buildTraceSQL(ctx, req)
	require.NoError(t, err)
	_, firstWhere, ok := strings.Cut(first, " WHERE ")
	require.True(t, ok)
	_, secondWhere, ok := strings.Cut(second, " WHERE ")
	require.True(t, ok)
	assert.Equal(t, firstWhere, secondWhere, "a second build from the same request must filter identically")
}

// TestOtelClickhouseBuildTraceSQL_StripsServiceNameSpanAttribute covers the second half of
// the normalisation QueryTraces performs: `service.name` is lifted out of the spanattributes
// filter. GetQuery skipped this too, so a reader was shown a predicate the backend dropped.
func TestOtelClickhouseBuildTraceSQL_StripsServiceNameSpanAttribute(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin(nil, nil, nil)
	src := &OtelClickhouseTraceSource{}

	req := TracesV3Request{
		AccountId: "acct-1",
		StartTime: 1756000000000,
		EndTime:   1756000600000,
		QueryRequest: TracesQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"spanattributes": {query.Eq: map[string]interface{}{"service.name": "cart"}},
				},
			},
		},
	}

	sql, err := src.buildTraceSQL(ctx, req)
	require.NoError(t, err)

	// Assert on the WHERE clause specifically: `service.name` legitimately appears in the
	// base SELECT's attribute-resolution CASEs, so a whole-query check would pass for the
	// wrong reason.
	_, where, found := strings.Cut(sql, " WHERE ")
	require.True(t, found, "generated SQL should carry a WHERE clause")
	assert.NotContains(t, where, "spanattributes",
		"the reported query must not filter on a predicate QueryTraces strips before executing")
}
