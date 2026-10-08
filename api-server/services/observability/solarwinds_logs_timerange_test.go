package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nubi's logs agent sends its window as timestamp _gte/_lte conditions. SolarWinds
// answers "time:>…" with HTTP 500 and the builder refuses range operators, so the
// question failed outright; a top-level time range is the window and folds into it.
func TestSWFoldTimeRange(t *testing.T) {
	const start, end = int64(1791392400000), int64(1791397800000) // 17:00–18:30Z
	req := func(w query.QueryWhereClause) FetchLogRequest {
		return FetchLogRequest{StartTime: start, EndTime: end, QueryRequest: LogsQueryBuilderRequest{Where: w}}
	}

	t.Run("a top-level range narrows the window and leaves the filter", func(t *testing.T) {
		got, err := swFoldTimeRange(req(query.QueryWhereClause{And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"time": {query.Gte: "2026-10-07T17:30:00Z"}}},
			{Binary: query.BinaryWhereClause{"timestamp": {query.Lte: float64(1791397200000)}}}, // 18:20Z, epoch ms
			{Binary: query.BinaryWhereClause{"k8s.namespace.name": {query.Eq: "shop"}}},
		}}))
		require.NoError(t, err)
		assert.Equal(t, int64(1791394200000), got.StartTime)
		assert.Equal(t, int64(1791397200000), got.EndTime)
		require.Len(t, got.QueryRequest.Where.And, 1, "only the non-time condition stays")
		filter, err := (&SolarWindsLogSource{}).buildFilter(got)
		require.NoError(t, err)
		assert.Contains(t, filter, "k8s.namespace.name")
		assert.NotContains(t, filter, "time:")
	})

	t.Run("GetQuery folds the range instead of failing on it", func(t *testing.T) {
		q, err := (&SolarWindsLogSource{}).GetQuery(nil, req(query.QueryWhereClause{And: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"time": {query.Gte: "2026-10-07T17:30:00Z"}}},
			{Binary: query.BinaryWhereClause{"program": {query.Eq: "api"}}},
		}}))
		require.NoError(t, err)
		assert.Equal(t, `(program:"api")`, q)
	})

	t.Run("a range never widens the window; epoch ns is read as ns", func(t *testing.T) {
		got, err := swFoldTimeRange(req(query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"timestamp": {query.Gte: float64(1791300000000) * 1e6, query.Lte: "2026-10-08T00:00:00Z"},
		}}))
		require.NoError(t, err)
		assert.Equal(t, start, got.StartTime)
		assert.Equal(t, end, got.EndTime)
	})

	t.Run("between folds both bounds", func(t *testing.T) {
		got, err := swFoldTimeRange(req(query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"time": {query.Between: map[string]any{"_gte": "2026-10-07T17:10:00Z", "_lte": "2026-10-07T17:20:00Z"}},
		}}))
		require.NoError(t, err)
		assert.Equal(t, int64(1791392400000+10*60000), got.StartTime)
		assert.Equal(t, int64(1791392400000+20*60000), got.EndTime)
	})

	t.Run("conditions that leave no window are an error, not an empty answer", func(t *testing.T) {
		_, err := swFoldTimeRange(req(query.QueryWhereClause{Binary: query.BinaryWhereClause{
			"time": {query.Gte: "2026-10-07T18:20:00Z", query.Lt: "2026-10-07T17:10:00Z"},
		}}))
		require.Error(t, err)
	})

	t.Run("a time range under _or is not a window and is still refused", func(t *testing.T) {
		got, err := swFoldTimeRange(req(query.QueryWhereClause{Or: []query.QueryWhereClause{
			{Binary: query.BinaryWhereClause{"time": {query.Gt: "2026-10-07T17:30:00Z"}}},
			{Binary: query.BinaryWhereClause{"program": {query.Eq: "api"}}},
		}}))
		require.NoError(t, err)
		_, err = (&SolarWindsLogSource{}).buildFilter(got)
		require.Error(t, err)
	})
}
