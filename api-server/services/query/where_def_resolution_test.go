package query

import (
	"strings"
	"testing"

	"nudgebee/services/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A column defined only by WhereDef (the trace attribute columns registered from
// request filter keys) must be written as its WhereDef in every operator. Several
// branches used to fall back to the raw column name, so a crafted key became SQL.
func TestGenerateWhereClause_WhereDefOnlyColumnNeverWritesRawKey(t *testing.T) {
	const rawKey = "x') OR 1=1 --"
	const whereDef = "attr_expr"
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.AgentWarehouse,
		Name:   "traces_test",
		Columns: map[string]ColumnDefinition{
			rawKey: {Type: ColumnDefinitionTypeString, WhereDef: whereDef},
		},
	}

	ops := map[BinaryWhereClauseType]any{
		Eq:       "v",
		Nq:       "v",
		In:       []any{"a", "b"},
		NotIn:    []any{"a", "b"},
		Like:     "v%",
		NLike:    "v%",
		ILike:    "v%",
		IsNull:   true,
		HasKey:   "k",
		Contains: `{"a":1}`,
		Regex:    "^v",
		NRegex:   "^v",
	}
	for op, value := range ops {
		t.Run(string(op), func(t *testing.T) {
			where := QueryWhereClause{Binary: BinaryWhereClause{rawKey: {op: value}}}
			sql, err := generateWhereClause(where, tableDef)
			require.NoError(t, err)
			assert.Contains(t, sql, whereDef)
			assert.False(t, strings.Contains(sql, "OR 1=1"), "raw key reached SQL: %s", sql)
		})
	}
}
