package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestExtractFilterSQL_FromAndClause_Eq(t *testing.T) {
	// The security layer and the frontend emit filters as _and lists, not _binary.
	request := QueryRequest{
		Where: QueryWhereClause{
			And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"status": {Eq: "Open"}}},
				{Binary: BinaryWhereClause{"account_id": {Eq: "abc-123"}}},
			},
		},
	}
	sql := extractFilterSQL(&request, "account_id", "r.cloud_account_id")

	assert.Equal(t, " AND r.cloud_account_id = 'abc-123'", sql)
	// Removed from the And child it was found in...
	_, exists := request.Where.And[1].Binary["account_id"]
	assert.False(t, exists, "filter should be removed from the And clause after extraction")
	// ...while the sibling And clause is untouched.
	_, statusExists := request.Where.And[0].Binary["status"]
	assert.True(t, statusExists, "unrelated And-clause filters must be preserved")
}

func TestExtractFilterSQL_FromAndClause_In(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"account_id": {In: []any{"a", "b"}}}},
			},
		},
	}
	sql := extractFilterSQL(&request, "account_id", "r.cloud_account_id")

	assert.Equal(t, " AND r.cloud_account_id IN ('a','b')", sql)
	_, exists := request.Where.And[0].Binary["account_id"]
	assert.False(t, exists)
}

func TestExtractFilterSQL_BinaryTakesPriorityOverAnd(t *testing.T) {
	// When present in both, the flat _binary entry is extracted (existing behavior),
	// leaving the And-clause occurrence for the outer WHERE (redundant but correct).
	request := QueryRequest{
		Where: QueryWhereClause{
			Binary: BinaryWhereClause{"status": {Eq: "Open"}},
			And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"status": {Eq: "Closed"}}},
			},
		},
	}
	sql := extractFilterSQL(&request, "status", "r.status")

	assert.Equal(t, " AND r.status = 'Open'", sql)
	_, inBinary := request.Where.Binary["status"]
	assert.False(t, inBinary, "the _binary occurrence should be the one removed")
	_, inAnd := request.Where.And[0].Binary["status"]
	assert.True(t, inAnd, "the And occurrence should remain")
}

func TestExtractFilterSQL_NestedAndClause(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			And: []QueryWhereClause{
				{And: []QueryWhereClause{
					{Binary: BinaryWhereClause{"tenant_id": {Eq: "t-1"}}},
				}},
			},
		},
	}
	sql := extractFilterSQL(&request, "tenant_id", "r.tenant_id")

	assert.Equal(t, " AND r.tenant_id = 't-1'", sql)
	_, exists := request.Where.And[0].And[0].Binary["tenant_id"]
	assert.False(t, exists, "filter nested in a deeper And chain should be extracted")
}

func TestExtractFilterSQL_IgnoresOrClause(t *testing.T) {
	// A filter under an OR branch is conditional; hoisting it as an unconditional
	// pushed-down AND would change the query's meaning, so it must be left in place.
	request := QueryRequest{
		Where: QueryWhereClause{
			Or: []QueryWhereClause{
				{Binary: BinaryWhereClause{"status": {Eq: "Open"}}},
				{Binary: BinaryWhereClause{"status": {Eq: "Closed"}}},
			},
		},
	}
	sql := extractFilterSQL(&request, "status", "r.status")

	assert.Equal(t, "", sql, "filters under Or must not be extracted")
	assert.Len(t, request.Where.Or, 2, "Or branches must be left untouched")
	_, exists := request.Where.Or[0].Binary["status"]
	assert.True(t, exists)
}

func TestExtractFilterSQL_IgnoresNotClause(t *testing.T) {
	request := QueryRequest{
		Where: QueryWhereClause{
			Not: &QueryWhereClause{
				Binary: BinaryWhereClause{"status": {Eq: "Open"}},
			},
		},
	}
	sql := extractFilterSQL(&request, "status", "r.status")

	assert.Equal(t, "", sql, "filters under Not must not be extracted")
	require.NotNil(t, request.Where.Not)
	_, exists := request.Where.Not.Binary["status"]
	assert.True(t, exists)
}

func TestExtractFilterSQL_SecurityShapedTenantAndAccount(t *testing.T) {
	// Mirrors what service.go injects: tenant_id and an account IN restriction as
	// _and clauses, alongside a user-supplied status filter also in _and.
	request := QueryRequest{
		Where: QueryWhereClause{
			And: []QueryWhereClause{
				{Binary: BinaryWhereClause{"status": {In: []any{"Open"}}}},
				{Binary: BinaryWhereClause{"tenant_id": {Eq: "tenant-1"}}},
				{Binary: BinaryWhereClause{"cloud_account_id": {In: []any{"acc-1", "acc-2"}}}},
			},
		},
	}
	statusSQL := extractFilterSQL(&request, "status", "r.status")
	accountSQL := extractFilterSQL(&request, "cloud_account_id", "r.cloud_account_id")

	assert.Equal(t, " AND r.status IN ('Open')", statusSQL)
	assert.Equal(t, " AND r.cloud_account_id IN ('acc-1','acc-2')", accountSQL)
	// tenant_id is left for the outer WHERE (the grouping generator pushes it separately
	// from the security context), and both extracted entries are gone.
	_, hasStatus := request.Where.And[0].Binary["status"]
	_, hasAccount := request.Where.And[2].Binary["cloud_account_id"]
	assert.False(t, hasStatus)
	assert.False(t, hasAccount)
	_, hasTenant := request.Where.And[1].Binary["tenant_id"]
	assert.True(t, hasTenant, "tenant_id is not extracted by these calls and must remain")
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
