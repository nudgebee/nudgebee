package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractFilterSQL_Eq(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"account_id": {Eq: "abc-123"},
			},
		},
	}
	sql := extractFilterSQL(&request, "account_id", "r.cloud_account_id")

	assert.Equal(t, " AND r.cloud_account_id = 'abc-123'", sql)
	// Filter should be removed from request
	_, exists := request.Where.Binary["account_id"]
	assert.False(t, exists, "filter should be removed from request after extraction")
}

func TestExtractFilterSQL_In(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"status": {In: []any{"Open", "Assigned"}},
			},
		},
	}
	sql := extractFilterSQL(&request, "status", "r.status")

	assert.Equal(t, " AND r.status IN ('Open','Assigned')", sql)
	_, exists := request.Where.Binary["status"]
	assert.False(t, exists, "filter should be removed from request after extraction")
}

func TestExtractFilterSQL_InSingleValue(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"status": {In: []any{"Open"}},
			},
		},
	}
	sql := extractFilterSQL(&request, "status", "r.status")

	assert.Equal(t, " AND r.status IN ('Open')", sql)
}

func TestExtractFilterSQL_InEmpty(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"status": {In: []any{}},
			},
		},
	}
	sql := extractFilterSQL(&request, "status", "r.status")

	assert.Equal(t, "", sql, "empty IN list should produce no SQL")
	// Filter should NOT be removed since no SQL was generated
	_, exists := request.Where.Binary["status"]
	assert.True(t, exists, "filter should remain when no SQL was generated")
}

func TestExtractFilterSQL_Missing(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"other_field": {Eq: "value"},
			},
		},
	}
	sql := extractFilterSQL(&request, "account_id", "r.cloud_account_id")

	assert.Equal(t, "", sql)
	// other_field should remain untouched
	_, exists := request.Where.Binary["other_field"]
	assert.True(t, exists)
}

func TestExtractFilterSQL_NilBinary(t *testing.T) {
	request := QueryRequest{}
	sql := extractFilterSQL(&request, "account_id", "r.cloud_account_id")

	assert.Equal(t, "", sql)
}

func TestExtractFilterSQL_SQLInjection(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{
			name:     "single quote in eq value",
			value:    "abc'; DROP TABLE recommendation; --",
			expected: " AND r.cloud_account_id = 'abc''; DROP TABLE recommendation; --'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := QueryRequest{
				Where: QueryWhereClause{
					Binary: BinaryWhereClause{
						"account_id": {Eq: tt.value},
					},
				},
			}
			sql := extractFilterSQL(&request, "account_id", "r.cloud_account_id")
			assert.Equal(t, tt.expected, sql)
		})
	}

	t.Run("single quote in IN values", func(t *testing.T) {
		request := QueryRequest{
			Where: QueryWhereClause{
				Binary: BinaryWhereClause{
					"status": {In: []any{"Open", "'; DROP TABLE x; --"}},
				},
			},
		}
		sql := extractFilterSQL(&request, "status", "r.status")
		assert.Equal(t, " AND r.status IN ('Open','''; DROP TABLE x; --')", sql)
	})
}

func TestExtractFilterSQL_PreservesOtherFilters(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{
				"account_id": {Eq: "abc-123"},
				"status":     {In: []any{"Open", "Assigned"}},
				"category":   {Eq: "RightSizing"},
			},
		},
	}

	extractFilterSQL(&request, "account_id", "r.cloud_account_id")
	extractFilterSQL(&request, "status", "r.status")

	// account_id and status should be removed
	_, hasAccount := request.Where.Binary["account_id"]
	_, hasStatus := request.Where.Binary["status"]
	assert.False(t, hasAccount)
	assert.False(t, hasStatus)

	// category should remain
	_, hasCategory := request.Where.Binary["category"]
	assert.True(t, hasCategory)
}
