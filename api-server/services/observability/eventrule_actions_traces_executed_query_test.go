package observability

import (
	"fmt"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/query"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
)

// queryingFakeTraceSource is a fakeTraceSource with a controllable GetQuery, so the
// executed-query resolution can be exercised both against a provider that renders a
// native query string and against one whose GetQuery is unimplemented.
type queryingFakeTraceSource struct {
	fakeTraceSource
	query    string
	queryErr error
}

func (f *queryingFakeTraceSource) GetQuery(*security.RequestContext, TracesV3Request) (string, error) {
	return f.query, f.queryErr
}

func TestAddTraceExecutedQueryInfo(t *testing.T) {
	t.Run("records query and provider", func(t *testing.T) {
		info := addTraceExecutedQueryInfo(
			map[string]any{"action_name": "traces"},
			TracesResult{Query: `SELECT * FROM traces_v2 WHERE workload_name='cart'`, Provider: "clickhouse"},
		)
		assert.Equal(t, `SELECT * FROM traces_v2 WHERE workload_name='cart'`, info["executed_query"])
		assert.Equal(t, "clickhouse", info["provider"])
		assert.Equal(t, "traces", info["action_name"])
	})

	t.Run("records the query on an empty result", func(t *testing.T) {
		// The case a reader most needs: no spans came back, so the query is the only
		// thing that explains the card.
		info := addTraceExecutedQueryInfo(nil, TracesResult{
			Traces:   []common.OpenTelemetryTrace{},
			Query:    `{"_binary":{"workload_name":{"_eq":"cart"}}}`,
			Provider: "datadog",
		})
		assert.Equal(t, `{"_binary":{"workload_name":{"_eq":"cart"}}}`, info["executed_query"])
		assert.Equal(t, "datadog", info["provider"])
	})

	t.Run("skips empty values and tolerates nil map", func(t *testing.T) {
		info := addTraceExecutedQueryInfo(nil, TracesResult{})
		_, hasQuery := info["executed_query"]
		_, hasProvider := info["provider"]
		assert.False(t, hasQuery)
		assert.False(t, hasProvider)
	})
}

func TestResolveExecutedTraceQuery(t *testing.T) {
	whereClause := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"workload_name": {query.Eq: "cart"}},
	}

	t.Run("returns nothing when the caller did not opt in", func(t *testing.T) {
		// The traces UI listing and the service map take this path; neither should pay
		// for the extra GetQuery round trip.
		src := &queryingFakeTraceSource{query: "SELECT 1"}
		got := resolveExecutedTraceQuery(nil, src, TracesV3Request{
			QueryRequest: TracesQueryBuilderRequest{Where: whereClause},
		})
		assert.Empty(t, got)
	})

	t.Run("prefers the caller's raw query", func(t *testing.T) {
		src := &queryingFakeTraceSource{query: "SELECT generated"}
		got := resolveExecutedTraceQuery(nil, src, TracesV3Request{
			Query:                "SELECT raw",
			IncludeExecutedQuery: true,
		})
		assert.Equal(t, "SELECT raw", got)
	})

	t.Run("uses the provider-native query when the source renders one", func(t *testing.T) {
		src := &queryingFakeTraceSource{query: "SELECT * FROM traces_v2 WHERE workload_name='cart'"}
		got := resolveExecutedTraceQuery(nil, src, TracesV3Request{
			IncludeExecutedQuery: true,
			QueryRequest:         TracesQueryBuilderRequest{Where: whereClause},
		})
		assert.Equal(t, "SELECT * FROM traces_v2 WHERE workload_name='cart'", got)
	})

	t.Run("falls back to the canonical clause when GetQuery is unimplemented", func(t *testing.T) {
		// Datadog, Jaeger and Chronosphere consume the where clause natively and return an
		// error here. The clause has already been mapped into provider space, so it is a
		// faithful record of what the source was handed.
		src := &queryingFakeTraceSource{queryErr: fmt.Errorf("Datadog.GetQuery unimplemented")}
		got := resolveExecutedTraceQuery(nil, src, TracesV3Request{
			IncludeExecutedQuery: true,
			QueryRequest:         TracesQueryBuilderRequest{Where: whereClause},
		})
		assert.JSONEq(t, `{"_binary":{"workload_name":{"_eq":"cart"}}}`, got)
	})

	t.Run("returns nothing when there is no query and no clause", func(t *testing.T) {
		src := &queryingFakeTraceSource{queryErr: fmt.Errorf("unimplemented")}
		got := resolveExecutedTraceQuery(nil, src, TracesV3Request{IncludeExecutedQuery: true})
		assert.Empty(t, got)
	})
}
