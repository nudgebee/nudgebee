package observability

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exact `query` variable the Logs page sent for the AI-generated query that
// failed in production: the canonical payload, JSON-encoded TWICE (the page runs
// JSON.stringify over a value that is already a string). Replayed against live
// SigNoz v0.145, the body this used to produce answered 400 "cannot unmarshal
// string into Go struct field ...filters.items of type []v3.FilterItem".
const uiDoubleEncodedQuery = "\"{\\n  \\\"where\\\": {\\n    \\\"app\\\": {\\n      \\\"_eq\\\": \\\"services-server\\\"\\n    },\\n" +
	"    \\\"namespace\\\": {\\n      \\\"_eq\\\": \\\"nudgebee\\\"\\n    }\\n  },\\n  \\\"time_range\\\": \\\"1h\\\",\\n  \\\"limit\\\": 1000\\n}\""

// binaryOps renders a where clause's top-level _binary conditions.
func binaryOps(t *testing.T, where query.QueryWhereClause) []string {
	t.Helper()
	var out []string
	for field, ops := range where.Binary {
		for op, v := range ops {
			b, err := json.Marshal(v)
			require.NoError(t, err)
			out = append(out, field+" "+string(op)+" "+string(b))
		}
	}
	return out
}

func TestShiftCanonicalQueryToWhere(t *testing.T) {
	t.Run("a canonical query moves to QueryRequest", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": {"namespace": {"_eq": "nb-e2e"}}, "time_range": "1h", "limit": 1000}`, Limit: 100}
		require.NoError(t, shiftCanonicalQueryToWhere(&req))
		assert.Empty(t, req.Query, "Query must be cleared so the provider takes the where-clause path")
		assert.Equal(t, []string{`namespace _eq "nb-e2e"`}, binaryOps(t, req.QueryRequest.Where))
		assert.Equal(t, 1000, req.Limit, "the query's own limit wins")
	})

	// The regression that motivated the fix.
	t.Run("the double-encoded payload the Logs page sends", func(t *testing.T) {
		req := FetchLogRequest{Query: uiDoubleEncodedQuery, Limit: 100}
		require.NoError(t, shiftCanonicalQueryToWhere(&req))
		assert.Empty(t, req.Query)
		assert.ElementsMatch(t,
			[]string{`app _eq "services-server"`, `namespace _eq "nudgebee"`},
			binaryOps(t, req.QueryRequest.Where))
		assert.Equal(t, 1000, req.Limit)
	})

	t.Run("_or survives the shift", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": {"_or": [{"ns": {"_eq": "a"}}, {"ns": {"_eq": "b"}}]}}`}
		require.NoError(t, shiftCanonicalQueryToWhere(&req))
		require.Len(t, req.QueryRequest.Where.Or, 2)
		assert.Equal(t, []string{`ns _eq "a"`}, binaryOps(t, req.QueryRequest.Where.Or[0]))
		assert.Equal(t, []string{`ns _eq "b"`}, binaryOps(t, req.QueryRequest.Where.Or[1]))
	})

	t.Run("_not survives the shift", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": {"_not": {"ns": {"_eq": "noisy"}}}}`}
		require.NoError(t, shiftCanonicalQueryToWhere(&req))
		require.NotNil(t, req.QueryRequest.Where.Not)
		assert.Equal(t, []string{`ns _eq "noisy"`}, binaryOps(t, *req.QueryRequest.Where.Not))
	})
}

// Why this runs at the service level: FetchLogs expands canonical label keys and
// applies the account's label mapping to QueryRequest.Where. A shift done inside
// a provider happens after both, so the generator's canonical names would reach
// the backend unmapped — accepted by SigNoz and matching nothing. This is the
// composition a provider-side shift silently skipped.
func TestShiftCanonicalQueryToWhere_CanonicalNamesAreMappedAfterTheShift(t *testing.T) {
	mapping := map[string]string{"namespace": "k8s.namespace.name", "pod": "k8s.pod.name"}
	req := FetchLogRequest{Query: `{"where": {"namespace": {"_eq": "nudgebee"}}}`}
	require.NoError(t, shiftCanonicalQueryToWhere(&req))

	// Exactly what FetchLogs does next, in the same order.
	where, _ := expandCanonicalLabelKeys(req.QueryRequest.Where, &SignozSaasSource{}, mapping)
	where = convertWhereClauseWithMApping(where, mapping)

	assert.Equal(t, []string{`k8s.namespace.name _eq "nudgebee"`}, binaryOps(t, where))
}

func TestShiftCanonicalQueryToWhere_Window(t *testing.T) {
	req := FetchLogRequest{
		Query:     `{"where": {"ns": {"_eq": "x"}}, "time_range": "24h", "limit": 5000}`,
		StartTime: time.Now().Add(-2 * time.Hour).UnixMilli(),
		EndTime:   time.Now().UnixMilli(),
	}
	require.NoError(t, shiftCanonicalQueryToWhere(&req))
	window := time.Duration(req.EndTime-req.StartTime) * time.Millisecond
	assert.InDelta(t, (24 * time.Hour).Seconds(), window.Seconds(), 5, "24h must replace the caller's 2h window")
	assert.Equal(t, 5000, req.Limit)

	t.Run("day suffix", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": {"a": {"_eq": "1"}}, "time_range": "7d"}`}
		require.NoError(t, shiftCanonicalQueryToWhere(&req))
		window := time.Duration(req.EndTime-req.StartTime) * time.Millisecond
		assert.InDelta(t, (7 * 24 * time.Hour).Seconds(), window.Seconds(), 5)
	})

	t.Run("absent or unusable time_range keeps the caller's window", func(t *testing.T) {
		for _, tr := range []string{"", "last tuesday", "0h", "-1h", "200000d"} {
			req := FetchLogRequest{Query: `{"where": {"a": {"_eq": "1"}}, "time_range": "` + tr + `"}`, StartTime: 111, EndTime: 222}
			require.NoError(t, shiftCanonicalQueryToWhere(&req), tr)
			assert.Equal(t, int64(111), req.StartTime, tr)
			assert.Equal(t, int64(222), req.EndTime, tr)
		}
	})
}

// A where clause that was written but decoded to no condition must fail: an
// empty clause means "no filter", so the query would widen to every log in the
// window and report success.
func TestShiftCanonicalQueryToWhere_Rejections(t *testing.T) {
	for name, q := range map[string]string{
		"a composite with no usable clause": `{"where": {"_or": ["not-a-clause"]}}`,
		"an empty composite":                `{"where": {"_and": []}}`,
	} {
		req := FetchLogRequest{Query: q}
		assert.ErrorContains(t, shiftCanonicalQueryToWhere(&req), "matched no supported condition", name)
	}

	t.Run("a where of the wrong type", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": "namespace=x"}`}
		assert.ErrorContains(t, shiftCanonicalQueryToWhere(&req), "not an object")
	})

	t.Run("a composite of the wrong type", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": {"_and": {"a": {"_eq": "1"}}}}`}
		assert.ErrorContains(t, shiftCanonicalQueryToWhere(&req), "must be a list of clauses")
	})

	t.Run("an explicitly empty where means no filter", func(t *testing.T) {
		req := FetchLogRequest{Query: `{"where": {}}`}
		require.NoError(t, shiftCanonicalQueryToWhere(&req))
		assert.Empty(t, req.Query)
		assert.False(t, hasWhereData(req.QueryRequest.Where))
	})
}

// Query is provider-native space — for SigNoz, an array of filter items.
// Anything that is not unambiguously a canonical query must come through
// untouched. The other providers' languages are listed because this helper is
// one `if` away from seeing them if the call-site gate is ever widened.
func TestShiftCanonicalQueryToWhere_LeavesProviderNativeQueriesAlone(t *testing.T) {
	native := map[string]FetchLogRequest{
		"LogQL":                     {Query: `{app="checkout"} |= "error"`},
		"SQL":                       {Query: `SELECT * FROM logs WHERE level = 'ERROR'`},
		"KQL":                       {Query: `kubernetes.namespace:"default"`},
		"SigNoz filter items":       {Query: `[{"key":{"key":"a"},"op":"=","value":"1"}]`},
		"ES where-clause array":     {Query: `[{"_binary":{"k":{"_eq":"v"}}}]`},
		"an object without where":   {Query: `{"time_range": "1h", "limit": 10}`},
		"empty":                     {Query: ``},
		"a JSON string of non-JSON": {Query: `"services-server"`},
	}
	for name, req := range native {
		t.Run(name, func(t *testing.T) {
			before := req.Query
			require.NoError(t, shiftCanonicalQueryToWhere(&req))
			assert.Equal(t, before, req.Query, "Query must be untouched")
			assert.False(t, hasWhereData(req.QueryRequest.Where))
		})
	}
}
