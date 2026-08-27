package observability

import (
	"testing"

	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The trace label mapping is used twice: to advertise label_mappings in the provider
// capabilities, and to rewrite every incoming where clause via convertWhereClauseWithMApping
// -- including the ones the frontend trace query builder sends. A key that shadows a real
// trace column would therefore silently rewrite queries that work today.
func TestOtelClickhouseTraceLabelMapping(t *testing.T) {
	mapping := (&OtelClickhouseTraceSource{}).GetLabelMapping()

	// Acceptance criterion of the canonical-traces work: capabilities must advertise a
	// non-empty merged mapping so the agent can emit canonical field names.
	require.NotEmpty(t, mapping, "an empty mapping sends canonical names to ClickHouse verbatim")
	assert.Equal(t, "workload_namespace", mapping["namespace"])

	columns := ClickhouseTraceTableDefinition
	for canonical, target := range mapping {
		_, shadows := columns[canonical]
		assert.Falsef(t, shadows, "alias %q shadows a real trace column; it would rewrite queries that already work", canonical)
		_, resolves := columns[target]
		assert.Truef(t, resolves, "alias %q maps to %q, which is not a trace column", canonical, target)
	}
}

// Regression: a canonical `endpoint` filter used to fail with "Unknown expression or
// function identifier `endpoint`" because the trace column set had no such column, even
// though llm-server's traces_view defines it and the agent prompt documents it.
func TestClickhouseTraceTableHasEndpoint(t *testing.T) {
	col, ok := ClickhouseTraceTableDefinition["endpoint"]
	require.True(t, ok, "endpoint must be queryable server-side")
	assert.Equal(t, query.ColumnDefinitionTypeString, col.Type)
	assert.Contains(t, col.Def, "spanattributes['http.route']", "must mirror traces_view's projection")
	assert.Contains(t, col.Def, "span_name", "must end on the same span_name fallback as traces_view")
}

// Regression: canonical `http_status_code {_gte: 500}` used to 400 with "binary clause type
// _gte not supported for string type", while the identical predicate worked on the agent's
// raw-SQL path because its view casts the column.
func TestClickhouseTraceStatusCodeComparesNumerically(t *testing.T) {
	col := ClickhouseTraceTableDefinition["http_status_code"]
	assert.Equal(t, query.ColumnDefinitionTypeString, col.Type,
		"type must stay string so _eq and label-values keep comparing strings")
	assert.Equal(t, "toInt32OrZero(http_status_code)", col.NumericCompareDef)
}
