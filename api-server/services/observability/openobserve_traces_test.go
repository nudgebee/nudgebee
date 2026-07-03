package observability

import (
	"nudgebee/services/query"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenObserveTraceSource_BuildSQLWhere(t *testing.T) {
	where := query.QueryWhereClause{
		And: []query.QueryWhereClause{
			{
				Binary: query.BinaryWhereClause{
					"workload_name": {
						query.Eq: "checkout-service",
					},
				},
			},
			{
				Binary: query.BinaryWhereClause{
					"span_name": {
						query.ILike: "POST /checkout",
					},
				},
			},
		},
	}

	sql, err := buildOpenObserveTraceWhereClause(where)
	require.NoError(t, err)

	expected := "(service.name = 'checkout-service' AND str_match_ignore_case(name, '.*POST /checkout.*'))"
	assert.Equal(t, expected, sql)
}

func TestOpenObserveTraceSource_BuildSQL(t *testing.T) {
	s := &OpenObserveTraceSource{}

	req := TracesV3Request{
		QueryRequest: TracesQueryBuilderRequest{
			Limit: 25,
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status_code": {
						query.Eq: "2",
					},
				},
			},
		},
	}

	sql, err := s.buildSQL(req)
	require.NoError(t, err)

	expected := `SELECT * FROM "default" WHERE otel.status_code = '2' ORDER BY _timestamp DESC LIMIT 25`
	assert.Equal(t, expected, sql)
}
