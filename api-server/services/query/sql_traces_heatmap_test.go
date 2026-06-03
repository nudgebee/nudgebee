package query

import (
	"nudgebee/services/internal/database"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracesHeatmapVulnerability_Fixed(t *testing.T) {
	request := QueryRequest{
		Table: "traces_heatmap_v2",
		Columns: []QueryColumn{
			{Name: "trace_id"},
		},
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"trace_id": {
					Eq: "' OR 1=1 --",
				},
			},
		},
	}

	// Mocking traces_heatmap_v2 definition with the FIXED template (no quotes around @traceId)
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.Metastore, // Uses Postgres dialect in tests
		Def:    "SELECT * FROM traces WHERE TraceId = @traceId",
		Name:   "traces_heatmap_v2",
		Columns: map[string]ColumnDefinition{
			"trace_id": {Type: ColumnDefinitionTypeString, Def: "TraceId"},
		},
	}

	// Call the REAL function
	sql, err := GenerateSqlQuery(nil, "account-123", request, tableDef)
	assert.NoError(t, err)

	// We expect the injection to be neutralized.
	// The dialect.QuoteLiteral should escape the single quote.
	// Input: ' OR 1=1 --
	// Escaped (Postgres): '' OR 1=1 --
	// Quoted: ' '' OR 1=1 -- ' (Postgres standard string literal)
	//
	// Expected Result: TraceId = ''' OR 1=1 --'
	// This compares TraceId against the string literal value "' OR 1=1 --", effectively neutralizing the SQL injection.

	// Note: The extra parens come from internal query generation structure
	assert.Contains(t, sql, "TraceId = ''' OR 1=1 --'")
	t.Logf("Generated SQL: %s", sql)
}
