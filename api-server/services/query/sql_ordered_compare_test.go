package query

import (
	"testing"

	"nudgebee/services/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ordered operators (< <= > >=) on a string column are rejected on purpose: comparing
// lexicographically against a value the caller means numerically returns plausible wrong
// rows instead of an error. A column opts out per-column via NumericCompareDef, which
// supplies an explicit numeric projection of itself.
func TestOrderedCompareOnStringColumn(t *testing.T) {
	table := TableDefinition{
		Source: database.AgentWarehouse,
		Type:   Normal,
		Columns: map[string]ColumnDefinition{
			"plain_text": {Type: ColumnDefinitionTypeString},
			"numeric_text": {
				Type:              ColumnDefinitionTypeString,
				NumericCompareDef: "toInt32OrZero(numeric_text)",
			},
		},
	}

	t.Run("rejects a plain string column", func(t *testing.T) {
		for _, op := range []BinaryWhereClauseType{"_gt", "_gte", "_lt", "_lte"} {
			_, err := generateWhereClause(QueryWhereClause{
				Binary: map[string]map[BinaryWhereClauseType]any{"plain_text": {op: 500}},
			}, table)
			require.Errorf(t, err, "%s must stay an error, not a lexicographic comparison", op)
			assert.Contains(t, err.Error(), "not supported for string type")
		}
	})

	t.Run("casts a column that opts in", func(t *testing.T) {
		q, err := generateWhereClause(QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{"numeric_text": {"_gte": 500}},
		}, table)
		require.NoError(t, err)
		assert.Equal(t, "(toInt32OrZero(numeric_text) >= 500)", q)
	})

	// A JSON caller may send the number quoted. ClickHouse converts a numeric string
	// literal when comparing against an integer expression -- verified against
	// ClickHouse 24.12: `toInt32(500) >= '500'` returns 1, while a non-numeric literal
	// fails loudly with TYPE_MISMATCH. So quoting here is safe, and the honest error is
	// preserved for genuine garbage.
	t.Run("accepts a quoted numeric value", func(t *testing.T) {
		q, err := generateWhereClause(QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{"numeric_text": {"_gte": "500"}},
		}, table)
		require.NoError(t, err)
		assert.Equal(t, "(toInt32OrZero(numeric_text) >= '500')", q)
	})

	// The opt-in must not leak into equality, or label-values and every string filter
	// built on the column would change behaviour.
	t.Run("equality still compares as a string", func(t *testing.T) {
		q, err := generateWhereClause(QueryWhereClause{
			Binary: map[string]map[BinaryWhereClauseType]any{"numeric_text": {"_eq": "500"}},
		}, table)
		require.NoError(t, err)
		assert.Equal(t, "(numeric_text = '500')", q)
	})
}
