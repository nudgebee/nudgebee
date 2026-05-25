package observability

import (
	"nudgebee/services/query"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToOpal_Operators(t *testing.T) {
	s := &ObserveSource{}

	testCases := []struct {
		name           string
		whereClause    query.QueryWhereClause
		expectedFilter string
		expectError    bool
	}{
		// Test Eq operator
		{
			name: "Eq operator with string value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"namespace": {query.Eq: "production"},
				},
			},
			expectedFilter: `filter namespace = "production"`,
			expectError:    false,
		},
		{
			name: "Eq operator with numeric value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"status_code": {query.Eq: 200},
				},
			},
			expectedFilter: `filter status_code = 200`,
			expectError:    false,
		},

		// Test Nq operator
		{
			name: "Nq operator with string value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"environment": {query.Nq: "staging"},
				},
			},
			expectedFilter: `filter environment != "staging"`,
			expectError:    false,
		},
		{
			name: "Nq operator with numeric value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"status_code": {query.Nq: 404},
				},
			},
			expectedFilter: `filter status_code != 404`,
			expectError:    false,
		},
		// Test Like operator
		{
			name: "Like operator with wildcard",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"message": {query.Like: "%error%"},
				},
			},
			expectedFilter: `filter like(message, "%error%")`,
			expectError:    false,
		},
		{
			name: "Like operator with prefix match",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"pod": {query.Like: "nginx-%"},
				},
			},
			expectedFilter: `filter like(pod, "nginx-%")`,
			expectError:    false,
		},
		{
			name: "Contains operator with regex pattern",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"log_level": {query.Contains: "ERROR|FATAL"},
				},
			},
			expectedFilter: `filter log_level ~ "ERROR|FATAL"`,
			expectError:    false,
		},
		{
			name: "Contains operator with simple pattern",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"body": {query.Contains: "exception"},
				},
			},
			expectedFilter: `filter body ~ "exception"`,
			expectError:    false,
		},

		// Test unsupported operators
		{
			name: "Unsupported operator Lt",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"value": {query.Lt: 100},
				},
			},
			expectedFilter: "",
			expectError:    true,
		},
		{
			name: "Unsupported operator Gt",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"value": {query.Gt: 50},
				},
			},
			expectedFilter: "",
			expectError:    true,
		},
		{
			name: "Unsupported operator Between",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"value": {query.Between: []any{10, 20}},
				},
			},
			expectedFilter: "",
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.ToOpal(tc.whereClause)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedFilter, result)
			}
		})
	}
}

func TestToOpal_ComplexQueries(t *testing.T) {
	s := &ObserveSource{}

	testCases := []struct {
		name           string
		whereClause    query.QueryWhereClause
		expectedFilter string
		expectError    bool
	}{
		// Test multiple conditions (AND by default) - Note: order may vary due to map iteration
		{
			name: "Multiple conditions with AND - namespace first",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"namespace": {query.Eq: "production"},
				},
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"pod": {query.Like: "api-%"},
						},
					},
				},
			},
			expectedFilter: `filter namespace = "production" and (like(pod, "api-%"))`,
			expectError:    false,
		},
		{
			name: "Multiple operators on same field - using explicit AND",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"message": {query.Contains: "error"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"message": {query.Like: "%failed%"},
						},
					},
				},
			},
			expectedFilter: `filter (message ~ "error" and like(message, "%failed%"))`,
			expectError:    false,
		},

		// Test explicit AND clause
		{
			name: "Explicit AND clause",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "production"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"container": {query.Eq: "app"},
						},
					},
				},
			},
			expectedFilter: `filter (namespace = "production" and container = "app")`,
			expectError:    false,
		},
		{
			name: "Nested AND clauses",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "prod"},
						},
					},
					{
						And: []query.QueryWhereClause{
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"pod": {query.Like: "api-%"},
								},
							},
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"container": {query.Eq: "main"},
								},
							},
						},
					},
				},
			},
			expectedFilter: `filter (namespace = "prod" and (like(pod, "api-%") and container = "main"))`,
			expectError:    false,
		},

		// Test OR clause
		{
			name: "Simple OR clause",
			whereClause: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"log_level": {query.Eq: "ERROR"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"log_level": {query.Eq: "FATAL"},
						},
					},
				},
			},
			expectedFilter: `filter (log_level = "ERROR" or log_level = "FATAL")`,
			expectError:    false,
		},
		{
			name: "OR with multiple conditions",
			whereClause: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "prod"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "staging"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "dev"},
						},
					},
				},
			},
			expectedFilter: `filter (namespace = "prod" or namespace = "staging" or namespace = "dev")`,
			expectError:    false,
		},

		// Test NOT clause
		{
			name: "Simple NOT clause",
			whereClause: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"container": {query.Eq: "sidecar"},
					},
				},
			},
			expectedFilter: `filter not(container = "sidecar")`,
			expectError:    false,
		},
		{
			name: "NOT with multiple conditions",
			whereClause: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					And: []query.QueryWhereClause{
						{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"namespace": {query.Eq: "test"},
							},
						},
						{
							Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
								"pod": {query.Like: "debug-%"},
							},
						},
					},
				},
			},
			expectedFilter: `filter not((namespace = "test" and like(pod, "debug-%")))`,
			expectError:    false,
		},

		// Test combined AND/OR/NOT
		{
			name: "Combined AND and OR",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "production"},
						},
					},
				},
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"log_level": {query.Eq: "ERROR"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"log_level": {query.Eq: "WARN"},
						},
					},
				},
			},
			expectedFilter: `filter (namespace = "production") and (log_level = "ERROR" or log_level = "WARN")`,
			expectError:    false,
		},
		{
			name: "Combined AND, OR, and NOT",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"namespace": {query.Eq: "production"},
						},
					},
				},
				Or: []query.QueryWhereClause{
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"log_level": {query.Eq: "ERROR"},
						},
					},
					{
						Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
							"log_level": {query.Eq: "WARN"},
						},
					},
				},
				Not: &query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"container": {query.Eq: "sidecar"},
					},
				},
			},
			expectedFilter: `filter (namespace = "production") and (log_level = "ERROR" or log_level = "WARN") and not(container = "sidecar")`,
			expectError:    false,
		},
		{
			name: "Complex nested query",
			whereClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Or: []query.QueryWhereClause{
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"namespace": {query.Eq: "prod"},
								},
							},
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"namespace": {query.Eq: "staging"},
								},
							},
						},
					},
					{
						Or: []query.QueryWhereClause{
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"log_level": {query.Eq: "ERROR"},
								},
							},
							{
								Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
									"message": {query.Contains: "exception"},
								},
							},
						},
					},
				},
				Not: &query.QueryWhereClause{
					Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
						"pod": {query.Like: "test-%"},
					},
				},
			},
			expectedFilter: `filter ((namespace = "prod" or namespace = "staging") and (log_level = "ERROR" or message ~ "exception")) and not(like(pod, "test-%"))`,
			expectError:    false,
		},

		// Test empty query
		{
			name:           "Empty query",
			whereClause:    query.QueryWhereClause{},
			expectedFilter: "",
			expectError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.ToOpal(tc.whereClause)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedFilter, result)
			}
		})
	}
}

func TestToOpal_EdgeCases(t *testing.T) {
	s := &ObserveSource{}

	testCases := []struct {
		name           string
		whereClause    query.QueryWhereClause
		expectedFilter string
		expectError    bool
	}{
		// Test special characters in values
		{
			name: "String with quotes",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"message": {query.Eq: `error with "quotes"`},
				},
			},
			expectedFilter: `filter message = "error with "quotes""`,
			expectError:    false,
		},
		{
			name: "Empty string value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"message": {query.Eq: ""},
				},
			},
			expectedFilter: `filter message = ""`,
			expectError:    false,
		},
		{
			name: "Unsupported In operator with array",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"namespace": {query.In: []any{"prod", "staging"}},
				},
			},
			expectedFilter: "",
			expectError:    true,
		},
		{
			name: "Boolean value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"is_error": {query.Eq: true},
				},
			},
			expectedFilter: `filter is_error = true`,
			expectError:    false,
		},
		{
			name: "Float value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"cpu_usage": {query.Eq: 75.5},
				},
			},
			expectedFilter: `filter cpu_usage = 75.5`,
			expectError:    false,
		},
		{
			name: "Null value",
			whereClause: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"error_code": {query.Eq: nil},
				},
			},
			expectedFilter: `filter error_code = <nil>`,
			expectError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.ToOpal(tc.whereClause)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedFilter, result)
			}
		})
	}
}

func TestOperatorToOpal(t *testing.T) {
	testCases := []struct {
		name           string
		operator       query.BinaryWhereClauseType
		expectedResult string
		expectError    bool
	}{
		// Supported operators
		{name: "Eq operator", operator: query.Eq, expectedResult: "=", expectError: false},
		{name: "Nq operator", operator: query.Nq, expectedResult: "!=", expectError: false},
		{name: "Like operator", operator: query.Like, expectedResult: "like", expectError: false},
		{name: "Contains operator", operator: query.Contains, expectedResult: "~", expectError: false},

		// Unsupported operators
		{name: "Unsupported In operator", operator: query.In, expectedResult: "", expectError: true},
		{name: "Unsupported NotIn operator", operator: query.NotIn, expectedResult: "", expectError: true},
		{name: "Unsupported ILike operator", operator: query.ILike, expectedResult: "", expectError: true},
		{name: "Unsupported Lt operator", operator: query.Lt, expectedResult: "", expectError: true},
		{name: "Unsupported Gt operator", operator: query.Gt, expectedResult: "", expectError: true},
		{name: "Unsupported Lte operator", operator: query.Lte, expectedResult: "", expectError: true},
		{name: "Unsupported Gte operator", operator: query.Gte, expectedResult: "", expectError: true},
		{name: "Unsupported Between operator", operator: query.Between, expectedResult: "", expectError: true},
		{name: "Unsupported HasKey operator", operator: query.HasKey, expectedResult: "", expectError: true},
		{name: "Unsupported IsNull operator", operator: query.IsNull, expectedResult: "", expectError: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, _, err := operatorToOpal(tc.operator)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported operator")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}
