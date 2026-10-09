package observability

import (
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/query"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dashboard traces panel sends its filters as a where clause over the trace
// table's canonical columns. This drives the REAL column definitions through the
// SQL generator, so a regex filter is proven to reach ClickHouse as match() on
// the column the definition maps to — not just on a made-up test table.
func TestOtelClickhouse_RegexFilterCompilesToMatch(t *testing.T) {
	tableDef := query.TableDefinition{
		Type:                query.Normal,
		Source:              database.AgentWarehouse,
		Def:                 "otel_traces",
		Name:                "traces_v2",
		Columns:             ClickhouseTraceTableDefinition,
		AccountIdColumnName: "account_id",
		TenantIdColumnName:  "tenant_id",
	}
	for _, column := range []string{"resource", "span_name", "workload_name"} {
		def, ok := ClickhouseTraceTableDefinition[column]
		require.True(t, ok, column)
		expr := def.Def
		if def.WhereDef != "" {
			expr = def.WhereDef
		}
		if expr == "" {
			expr = column
		}

		sql, err := query.GenerateSqlQuery(nil, "account-123", query.QueryRequest{
			Table:   "traces_v2",
			Columns: []query.QueryColumn{{Name: column}},
			Where:   query.QueryWhereClause{Binary: query.BinaryWhereClause{column: {query.Regex: "registry-(central|edge)"}}},
		}, tableDef)
		require.NoError(t, err, column)
		assert.Contains(t, sql, "match("+expr+", '(?s)registry-(central|edge)')", column)
	}
}
