package insight

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInsightFiltersToWhereClause(t *testing.T) {
	tests := []struct {
		name          string
		filters       []InsightFilters
		colName       string
		ids           []string
		expectedWhere string
		expectedArgs  []interface{}
	}{
		{
			name:          "No filters, no IDs",
			filters:       []InsightFilters{},
			colName:       "",
			ids:           []string{},
			expectedWhere: "1 = 1",
			expectedArgs:  []interface{}{},
		},
		{
			name:          "Only IDs",
			filters:       []InsightFilters{},
			colName:       "account_id",
			ids:           []string{"acc1", "acc2"},
			expectedWhere: "1 = 1 AND account_id in (?)",
			expectedArgs:  []interface{}{[]string{"acc1", "acc2"}},
		},
		{
			name: "Simple filter",
			filters: []InsightFilters{
				{Column: "status", Operator: "=", Value: "active"},
			},
			colName:       "",
			ids:           []string{},
			expectedWhere: "1 = 1 AND status = ?",
			expectedArgs:  []interface{}{"active"},
		},
		{
			name: "IN filter",
			filters: []InsightFilters{
				{Column: "role", Operator: "in", Value: []string{"admin", "user"}},
			},
			colName:       "",
			ids:           []string{},
			expectedWhere: "1 = 1 AND role in (?)",
			expectedArgs:  []interface{}{[]string{"admin", "user"}},
		},
		{
			name: "Mixed IDs and filters",
			filters: []InsightFilters{
				{Column: "status", Operator: "=", Value: "active"},
			},
			colName:       "account_id",
			ids:           []string{"acc1"},
			expectedWhere: "1 = 1 AND account_id in (?) AND status = ?",
			expectedArgs:  []interface{}{[]string{"acc1"}, "active"},
		},
		{
			name: "SQL Injection Attempt in Value",
			filters: []InsightFilters{
				{Column: "name", Operator: "=", Value: "'; DROP TABLE users; --"},
			},
			colName:       "",
			ids:           []string{},
			expectedWhere: "1 = 1 AND name = ?",
			expectedArgs:  []interface{}{"'; DROP TABLE users; --"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWhere, gotArgs, err := insightFiltersToWhereClause(tt.filters, tt.colName, tt.ids)
			assert.Nil(t, err)
			assert.Equal(t, tt.expectedWhere, gotWhere)
			assert.Equal(t, tt.expectedArgs, gotArgs)
		})
	}
}
