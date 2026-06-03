package query

import (
	"nudgebee/services/internal/database"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateWhereClause_SQLInjection(t *testing.T) {
	// Setup a table definition with an integer column
	// We use database.Metastore which maps to postgresDialect (standard SQL quoting)
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.Metastore,
		Name:   "test_table",
		Columns: map[string]ColumnDefinition{
			"id":    {Type: ColumnDefinitionTypeString},
			"count": {Type: ColumnDefinitionTypeInt},
			"score": {Type: ColumnDefinitionTypeFloat},
		},
	}

	tests := []struct {
		name          string
		where         QueryWhereClause
		expectedQuery string
	}{
		{
			name: "Eq: integer column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"count": {
						Eq: "1 OR 1=1",
					},
				},
			},
			// Expecting the string to be quoted, treating it as a string literal value comparison, NOT raw SQL
			// Before fix: count = 1 OR 1=1
			expectedQuery: "(count = '1 OR 1=1')",
		},
		{
			name: "Nq: float column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"score": {
						Nq: "1.0; DROP TABLE users",
					},
				},
			},
			// Before fix: score != 1.0; DROP TABLE users
			expectedQuery: "(score != '1.0; DROP TABLE users')",
		},
		{
			name: "Nq: string column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"id": {
						Nq: "'; DROP TABLE users; --",
					},
				},
			},
			// Expecting the string to be quoted, treating it as a string literal value comparison, NOT raw SQL
			expectedQuery: "(id != '''; DROP TABLE users; --')",
		},
		{
			name: "Gt: integer column with injection payload",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"count": {
						Gt: "10 UNION SELECT * FROM users",
					},
				},
			},
			expectedQuery: "(count > '10 UNION SELECT * FROM users')",
		},
		{
			name: "In: integer column with mixed valid/malicious types",
			where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"count": {
						In: []any{1, "2 OR 1=1"},
					},
				},
			},
			// Mixed types in IN clause. 1 remains 1, the string gets quoted.
			expectedQuery: "(count IN (1,'2 OR 1=1'))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, err := generateWhereClause(tt.where, tableDef)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedQuery, sql)
		})
	}

	t.Run("Between: integer column with malicious bounds", func(t *testing.T) {
		where := QueryWhereClause{
			Binary: BinaryWhereClause{
				"count": {
					Between: map[string]any{
						"_gte": "1 OR 1=1",
						"_lte": 100,
					},
				},
			},
		}
		sql, err := generateWhereClause(where, tableDef)
		assert.NoError(t, err)
		// Map iteration order is random, check for both possibilities
		// Note the double parens from generateWhereClause wrapping: ((cond1 AND cond2))
		option1 := "((count >= '1 OR 1=1' AND count <= 100))"
		option2 := "((count <= 100 AND count >= '1 OR 1=1'))"
		assert.Contains(t, []string{option1, option2}, sql)
	})
}

func TestGenerateWhereClause_NilDialect(t *testing.T) {
	// Setup a table definition with an invalid/unknown source
	tableDef := TableDefinition{
		Type:   Normal,
		Source: database.DatabaseManagerType("unknown_source"),
		Name:   "test_table",
		Columns: map[string]ColumnDefinition{
			"id": {Type: ColumnDefinitionTypeString},
		},
	}

	where := QueryWhereClause{
		Binary: BinaryWhereClause{
			"id": {
				Eq: "some_value",
			},
		},
	}

	// Should return an error, not panic
	sql, err := generateWhereClause(where, tableDef)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dialect not found")
	assert.Empty(t, sql)
}
