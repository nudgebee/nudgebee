package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
)

// SolarWinds was the quiet instance of the same bug as #41493: swInValues requires a
// []any, so a scalar `_in` from the single-select Service filter produced no filter
// token at all and collectSwFilterParts skipped it — the request went out unscoped and
// the list came back unchanged, exactly how #41419 presented on Cloud Trace. No 400, no
// log line. The shared rewriter wrapping the scalar is what makes the predicate survive.
func TestBuildSwTraceFilterKeepsScalarInFromServiceFilter(t *testing.T) {
	scalar := convertWhereClauseWithMApping(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"workload_name": {query.In: "qa-payment-s3"}},
	}, map[string]string{})
	assert.Equal(t, "service.name: [qa-payment-s3]", buildSwTraceFilter(scalar))

	// A list was always honoured; it must keep rendering the same way.
	list := convertWhereClauseWithMApping(query.QueryWhereClause{
		Binary: query.BinaryWhereClause{"workload_name": {query.In: []any{"qa-payment-s3", "qa-ledger"}}},
	}, map[string]string{})
	assert.Equal(t, "service.name: [qa-payment-s3,qa-ledger]", buildSwTraceFilter(list))
}
