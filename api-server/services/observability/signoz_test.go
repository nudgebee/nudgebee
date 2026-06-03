package observability

import (
	"encoding/json"
	"nudgebee/services/query"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Helper function to check if a clause exists in a slice
func containsClause(clauses []FlattenedClause, key, op string, value any) bool {
	for _, c := range clauses {
		if c.Key["key"] == key && c.Op == op {
			// For slice values, do deep comparison
			switch v := value.(type) {
			case []any:
				actualSlice, ok := c.Value.([]any)
				if !ok {
					return false
				}
				if len(v) != len(actualSlice) {
					return false
				}
				for i := range v {
					if v[i] != actualSlice[i] {
						return false
					}
				}
				return true
			default:
				return c.Value == value
			}
		}
	}
	return false
}

// Test FlattenSignozQuery with all supported operators
func TestFlattenSignozQuery(t *testing.T) {
	testCases := []struct {
		name            string
		queryClause     query.QueryWhereClause
		expectedClauses []FlattenedClause
		expectError     bool
		errorContains   string
	}{
		{
			name: "Eq operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service": {Eq: "api-service"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "service"},
					Value: "api-service",
					Op:    "=",
				},
			},
			expectError: false,
		},
		{
			name: "EqF operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"count": {EqF: 100},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "count"},
					Value: 100,
					Op:    "=",
				},
			},
			expectError: false,
		},
		{
			name: "Nq operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"environment": {Nq: "staging"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "environment"},
					Value: "staging",
					Op:    "!=",
				},
			},
			expectError: false,
		},
		{
			name: "NqF operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status_code": {NqF: 404},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "status_code"},
					Value: 404,
					Op:    "!=",
				},
			},
			expectError: false,
		},
		{
			name: "In operator with array",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status": {In: []any{"ok", "error", "warning"}},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "status"},
					Value: []any{"ok", "error", "warning"},
					Op:    "in",
				},
			},
			expectError: false,
		},
		{
			name: "In operator with non-array value should error",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"status": {In: "not-an-array"},
				},
			},
			expectedClauses: nil,
			expectError:     true,
			errorContains:   "value for 'in' operator must be an array",
		},
		{
			name: "NotIn operator with array",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"env": {NotIn: []any{"dev", "test"}},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "env"},
					Value: []any{"dev", "test"},
					Op:    "nin",
				},
			},
			expectError: false,
		},
		{
			name: "NotIn operator with non-array value should error",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"env": {NotIn: "not-an-array"},
				},
			},
			expectedClauses: nil,
			expectError:     true,
			errorContains:   "value for 'nin' operator must be an array",
		},
		{
			name: "Like operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {Like: "%error%"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "message"},
					Value: "%error%",
					Op:    "like",
				},
			},
			expectError: false,
		},
		{
			name: "NLike operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {NLike: "%debug%"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "message"},
					Value: "%debug%",
					Op:    "nlike",
				},
			},
			expectError: false,
		},
		{
			name: "Contains operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {Contains: "exception"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "message"},
					Value: "exception",
					Op:    "contains",
				},
			},
			expectError: false,
		},
		{
			name: "NotContains operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {NotContains: "debug"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "message"},
					Value: "debug",
					Op:    "ncontains",
				},
			},
			expectError: false,
		},
		{
			name: "Exist operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"trace_id": {Exist: true},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "trace_id"},
					Value: true,
					Op:    "exists",
				},
			},
			expectError: false,
		},
		{
			name: "NotExist operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"error_field": {NotExist: true},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "error_field"},
					Value: true,
					Op:    "nexists",
				},
			},
			expectError: false,
		},
		{
			name: "RegEx operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {RegEx: ".*error.*"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "message"},
					Value: ".*error.*",
					Op:    "regex",
				},
			},
			expectError: false,
		},
		{
			name: "NotRegEx operator",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"message": {NotRegEx: ".*debug.*"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "message"},
					Value: ".*debug.*",
					Op:    "nregex",
				},
			},
			expectError: false,
		},
		{
			name: "Single field Eq operator for deterministic test",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service": {Eq: "api"},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "service"},
					Value: "api",
					Op:    "=",
				},
			},
			expectError: false,
		},
		{
			name: "AND clause with multiple conditions",
			queryClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: query.BinaryWhereClause{
							"service": {Eq: "api"},
						},
					},
					{
						Binary: query.BinaryWhereClause{
							"env": {Eq: "prod"},
						},
					},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "service"},
					Value: "api",
					Op:    "=",
				},
				{
					Key:   map[string]string{"key": "env"},
					Value: "prod",
					Op:    "=",
				},
			},
			expectError: false,
		},
		{
			name: "Nested AND clauses",
			queryClause: query.QueryWhereClause{
				And: []query.QueryWhereClause{
					{
						Binary: query.BinaryWhereClause{
							"service": {Eq: "api"},
						},
					},
					{
						And: []query.QueryWhereClause{
							{
								Binary: query.BinaryWhereClause{
									"env": {Eq: "prod"},
								},
							},
							{
								Binary: query.BinaryWhereClause{
									"region": {Eq: "us-east-1"},
								},
							},
						},
					},
				},
			},
			expectedClauses: []FlattenedClause{
				{
					Key:   map[string]string{"key": "service"},
					Value: "api",
					Op:    "=",
				},
				{
					Key:   map[string]string{"key": "env"},
					Value: "prod",
					Op:    "=",
				},
				{
					Key:   map[string]string{"key": "region"},
					Value: "us-east-1",
					Op:    "=",
				},
			},
			expectError: false,
		},
		{
			name: "OR clause should return error",
			queryClause: query.QueryWhereClause{
				Or: []query.QueryWhereClause{
					{
						Binary: query.BinaryWhereClause{
							"status": {Eq: "error"},
						},
					},
					{
						Binary: query.BinaryWhereClause{
							"status": {Eq: "warning"},
						},
					},
				},
			},
			expectedClauses: nil,
			expectError:     true,
			errorContains:   "OR operator is not supported",
		},
		{
			name: "NOT clause should return error",
			queryClause: query.QueryWhereClause{
				Not: &query.QueryWhereClause{
					Binary: query.BinaryWhereClause{
						"service": {Eq: "api"},
					},
				},
			},
			expectedClauses: nil,
			expectError:     true,
			errorContains:   "NOT operator is not supported",
		},
		{
			name:            "Empty query clause",
			queryClause:     query.QueryWhereClause{},
			expectedClauses: []FlattenedClause{},
			expectError:     false,
		},
		{
			name: "Unsupported operator should error",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"field": {Lt: 100},
				},
			},
			expectedClauses: nil,
			expectError:     true,
			errorContains:   "unsupported operator",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := FlattenSignozQuery(tc.queryClause)

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedClauses), len(result))

				// Compare each clause
				for i, expected := range tc.expectedClauses {
					assert.Equal(t, expected.Key, result[i].Key)
					assert.Equal(t, expected.Value, result[i].Value)
					assert.Equal(t, expected.Op, result[i].Op)
				}
			}
		})
	}
}

// Test SignozSource.mapSeverity
func TestSignozSource_MapSeverity(t *testing.T) {
	s := &SignozSource{}

	testCases := []struct {
		name           string
		severityText   string
		severityNumber int
		expectedLevel  string
	}{
		// Test severity text mapping
		{
			name:           "Severity text: trace",
			severityText:   "trace",
			severityNumber: 1,
			expectedLevel:  "debug",
		},
		{
			name:           "Severity text: debug",
			severityText:   "debug",
			severityNumber: 5,
			expectedLevel:  "debug",
		},
		{
			name:           "Severity text: information",
			severityText:   "information",
			severityNumber: 9,
			expectedLevel:  "info",
		},
		{
			name:           "Severity text: info",
			severityText:   "info",
			severityNumber: 9,
			expectedLevel:  "info",
		},
		{
			name:           "Severity text: warning",
			severityText:   "warning",
			severityNumber: 13,
			expectedLevel:  "warn",
		},
		{
			name:           "Severity text: warn",
			severityText:   "warn",
			severityNumber: 13,
			expectedLevel:  "warn",
		},
		{
			name:           "Severity text: error",
			severityText:   "error",
			severityNumber: 17,
			expectedLevel:  "error",
		},
		{
			name:           "Severity text: fatal",
			severityText:   "fatal",
			severityNumber: 21,
			expectedLevel:  "fatal",
		},
		{
			name:           "Severity text: critical",
			severityText:   "critical",
			severityNumber: 21,
			expectedLevel:  "fatal",
		},
		// Test case insensitivity
		{
			name:           "Severity text: INFO (uppercase)",
			severityText:   "INFO",
			severityNumber: 9,
			expectedLevel:  "info",
		},
		{
			name:           "Severity text: Error (mixed case)",
			severityText:   "Error",
			severityNumber: 17,
			expectedLevel:  "error",
		},
		// Test fallback to severity number
		{
			name:           "Unknown text, number <= 8 (TRACE/DEBUG)",
			severityText:   "unknown",
			severityNumber: 5,
			expectedLevel:  "debug",
		},
		{
			name:           "Unknown text, number <= 12 (INFO)",
			severityText:   "unknown",
			severityNumber: 10,
			expectedLevel:  "info",
		},
		{
			name:           "Unknown text, number <= 16 (WARN)",
			severityText:   "unknown",
			severityNumber: 14,
			expectedLevel:  "warn",
		},
		{
			name:           "Unknown text, number <= 20 (ERROR)",
			severityText:   "unknown",
			severityNumber: 18,
			expectedLevel:  "error",
		},
		{
			name:           "Unknown text, number > 20 (FATAL)",
			severityText:   "unknown",
			severityNumber: 22,
			expectedLevel:  "fatal",
		},
		// Edge cases
		{
			name:           "Empty severity text, low number",
			severityText:   "",
			severityNumber: 1,
			expectedLevel:  "debug",
		},
		{
			name:           "Empty severity text, high number",
			severityText:   "",
			severityNumber: 25,
			expectedLevel:  "fatal",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := s.mapSeverity(tc.severityText, tc.severityNumber)
			assert.Equal(t, tc.expectedLevel, result)
		})
	}
}

// Test SignozSource.convertSigNozLogs
func TestSignozSource_ConvertSigNozLogs(t *testing.T) {
	s := &SignozSource{}

	testCases := []struct {
		name           string
		inputLogs      []SigNozLog
		expectedOutput []OutputLog
	}{
		{
			name: "Single log with all attributes",
			inputLogs: []SigNozLog{
				{
					Timestamp: "2023-01-01T12:00:00Z",
					Data: struct {
						AttributesBool    map[string]bool    `json:"attributes_bool"`
						AttributesFloat64 map[string]float64 `json:"attributes_float64"`
						AttributesInt64   map[string]int64   `json:"attributes_int64"`
						AttributesString  map[string]string  `json:"attributes_string"`
						Body              string             `json:"body"`
						ID                string             `json:"id"`
						ResourcesString   map[string]string  `json:"resources_string"`
						SeverityNumber    int                `json:"severity_number"`
						SeverityText      string             `json:"severity_text"`
						SpanID            string             `json:"span_id"`
						TraceFlags        int                `json:"trace_flags"`
						TraceID           string             `json:"trace_id"`
					}{
						Body:           "Application started",
						ID:             "log-123",
						SeverityText:   "info",
						SeverityNumber: 9,
						TraceID:        "trace-abc",
						SpanID:         "span-xyz",
						AttributesString: map[string]string{
							"service": "api",
							"env":     "prod",
						},
						AttributesBool: map[string]bool{
							"is_production": true,
						},
						AttributesFloat64: map[string]float64{
							"duration": 123.45,
						},
						AttributesInt64: map[string]int64{
							"status_code": 200,
						},
						ResourcesString: map[string]string{
							"host": "server-01",
						},
					},
				},
			},
			expectedOutput: []OutputLog{
				{
					Timestamp: "2023-01-01T12:00:00Z",
					Message:   "Application started",
					Severity:  "info",
					Labels: map[string]interface{}{
						"service":       "api",
						"env":           "prod",
						"is_production": "true",
						"duration":      "123.45",
						"status_code":   "200",
						"host":          "server-01",
						"trace_id":      "trace-abc",
						"span_id":       "span-xyz",
					},
				},
			},
		},
		{
			name: "Log without trace information",
			inputLogs: []SigNozLog{
				{
					Timestamp: "2023-01-01T13:00:00Z",
					Data: struct {
						AttributesBool    map[string]bool    `json:"attributes_bool"`
						AttributesFloat64 map[string]float64 `json:"attributes_float64"`
						AttributesInt64   map[string]int64   `json:"attributes_int64"`
						AttributesString  map[string]string  `json:"attributes_string"`
						Body              string             `json:"body"`
						ID                string             `json:"id"`
						ResourcesString   map[string]string  `json:"resources_string"`
						SeverityNumber    int                `json:"severity_number"`
						SeverityText      string             `json:"severity_text"`
						SpanID            string             `json:"span_id"`
						TraceFlags        int                `json:"trace_flags"`
						TraceID           string             `json:"trace_id"`
					}{
						Body:           "Error occurred",
						SeverityText:   "error",
						SeverityNumber: 17,
						AttributesString: map[string]string{
							"message": "Failed to process request",
						},
					},
				},
			},
			expectedOutput: []OutputLog{
				{
					Timestamp: "2023-01-01T13:00:00Z",
					Message:   "Error occurred",
					Severity:  "error",
					Labels: map[string]interface{}{
						"message": "Failed to process request",
					},
				},
			},
		},
		{
			name: "Multiple logs",
			inputLogs: []SigNozLog{
				{
					Timestamp: "2023-01-01T10:00:00Z",
					Data: struct {
						AttributesBool    map[string]bool    `json:"attributes_bool"`
						AttributesFloat64 map[string]float64 `json:"attributes_float64"`
						AttributesInt64   map[string]int64   `json:"attributes_int64"`
						AttributesString  map[string]string  `json:"attributes_string"`
						Body              string             `json:"body"`
						ID                string             `json:"id"`
						ResourcesString   map[string]string  `json:"resources_string"`
						SeverityNumber    int                `json:"severity_number"`
						SeverityText      string             `json:"severity_text"`
						SpanID            string             `json:"span_id"`
						TraceFlags        int                `json:"trace_flags"`
						TraceID           string             `json:"trace_id"`
					}{
						Body:           "Log 1",
						SeverityText:   "debug",
						SeverityNumber: 5,
					},
				},
				{
					Timestamp: "2023-01-01T11:00:00Z",
					Data: struct {
						AttributesBool    map[string]bool    `json:"attributes_bool"`
						AttributesFloat64 map[string]float64 `json:"attributes_float64"`
						AttributesInt64   map[string]int64   `json:"attributes_int64"`
						AttributesString  map[string]string  `json:"attributes_string"`
						Body              string             `json:"body"`
						ID                string             `json:"id"`
						ResourcesString   map[string]string  `json:"resources_string"`
						SeverityNumber    int                `json:"severity_number"`
						SeverityText      string             `json:"severity_text"`
						SpanID            string             `json:"span_id"`
						TraceFlags        int                `json:"trace_flags"`
						TraceID           string             `json:"trace_id"`
					}{
						Body:           "Log 2",
						SeverityText:   "warn",
						SeverityNumber: 13,
					},
				},
			},
			expectedOutput: []OutputLog{
				{
					Timestamp: "2023-01-01T10:00:00Z",
					Message:   "Log 1",
					Severity:  "debug",
					Labels:    map[string]interface{}{},
				},
				{
					Timestamp: "2023-01-01T11:00:00Z",
					Message:   "Log 2",
					Severity:  "warn",
					Labels:    map[string]interface{}{},
				},
			},
		},
		{
			name:           "Empty log list",
			inputLogs:      []SigNozLog{},
			expectedOutput: []OutputLog{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := s.convertSigNozLogs(tc.inputLogs)
			assert.Equal(t, len(tc.expectedOutput), len(result))

			for i := range result {
				assert.Equal(t, tc.expectedOutput[i].Timestamp, result[i].Timestamp)
				assert.Equal(t, tc.expectedOutput[i].Message, result[i].Message)
				assert.Equal(t, tc.expectedOutput[i].Severity, result[i].Severity)
				assert.Equal(t, tc.expectedOutput[i].Labels, result[i].Labels)
			}
		})
	}
}

// Test SignozSource.convertSigNozLogLabels
func TestSignozSource_ConvertSigNozLogLabels(t *testing.T) {
	s := &SignozSource{}

	testCases := []struct {
		name           string
		inputJSON      string
		expectedOutput []OutputLogLabel
		expectError    bool
	}{
		{
			name: "Single label",
			inputJSON: `[
				{
					"key": "service",
					"dataType": "string",
					"type": "tag",
					"isColumn": true,
					"isJSON": false
				}
			]`,
			expectedOutput: []OutputLogLabel{
				{
					Label: "service",
					Attributes: map[string]interface{}{
						"dataType": "string",
						"type":     "tag",
						"isColumn": true,
						"isJSON":   false,
					},
				},
			},
			expectError: false,
		},
		{
			name: "Multiple labels with different types",
			inputJSON: `[
				{
					"key": "host",
					"dataType": "string",
					"type": "resource",
					"isColumn": true,
					"isJSON": false
				},
				{
					"key": "status_code",
					"dataType": "number",
					"type": "tag",
					"isColumn": false,
					"isJSON": true
				},
				{
					"key": "is_error",
					"dataType": "bool",
					"type": "attribute",
					"isColumn": true,
					"isJSON": false
				}
			]`,
			expectedOutput: []OutputLogLabel{
				{
					Label: "host",
					Attributes: map[string]interface{}{
						"dataType": "string",
						"type":     "resource",
						"isColumn": true,
						"isJSON":   false,
					},
				},
				{
					Label: "status_code",
					Attributes: map[string]interface{}{
						"dataType": "number",
						"type":     "tag",
						"isColumn": false,
						"isJSON":   true,
					},
				},
				{
					Label: "is_error",
					Attributes: map[string]interface{}{
						"dataType": "bool",
						"type":     "attribute",
						"isColumn": true,
						"isJSON":   false,
					},
				},
			},
			expectError: false,
		},
		{
			name:           "Empty array",
			inputJSON:      `[]`,
			expectedOutput: []OutputLogLabel{},
			expectError:    false,
		},
		{
			name:           "Invalid JSON",
			inputJSON:      `{invalid json}`,
			expectedOutput: nil,
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.convertSigNozLogLabels(tc.inputJSON)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedOutput), len(result))

				for i := range result {
					assert.Equal(t, tc.expectedOutput[i].Label, result[i].Label)
					assert.Equal(t, tc.expectedOutput[i].Attributes, result[i].Attributes)
				}
			}
		})
	}
}

// Test ConvertSigNozLogLabelValues
func TestConvertSigNozLogLabelValues(t *testing.T) {
	testCases := []struct {
		name           string
		inputJSON      []byte
		expectedOutput []OutputLogLabelValue
		expectError    bool
	}{
		{
			name:      "Single value",
			inputJSON: []byte(`["value1"]`),
			expectedOutput: []OutputLogLabelValue{
				{
					Value:      "value1",
					Attributes: map[string]interface{}{},
				},
			},
			expectError: false,
		},
		{
			name:      "Multiple values",
			inputJSON: []byte(`["prod", "staging", "dev"]`),
			expectedOutput: []OutputLogLabelValue{
				{
					Value:      "prod",
					Attributes: map[string]interface{}{},
				},
				{
					Value:      "staging",
					Attributes: map[string]interface{}{},
				},
				{
					Value:      "dev",
					Attributes: map[string]interface{}{},
				},
			},
			expectError: false,
		},
		{
			name:           "Empty array",
			inputJSON:      []byte(`[]`),
			expectedOutput: []OutputLogLabelValue{},
			expectError:    false,
		},
		{
			name:           "Invalid JSON",
			inputJSON:      []byte(`{invalid}`),
			expectedOutput: nil,
			expectError:    true,
		},
		{
			name:           "Non-array JSON",
			inputJSON:      []byte(`{"key": "value"}`),
			expectedOutput: nil,
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertSigNozLogLabelValues(tc.inputJSON)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedOutput), len(result))

				for i := range result {
					assert.Equal(t, tc.expectedOutput[i].Value, result[i].Value)
					assert.Equal(t, tc.expectedOutput[i].Attributes, result[i].Attributes)
				}
			}
		})
	}
}

// Test SignozSource.GetLabelMapping
func TestSignozSource_GetLabelMapping(t *testing.T) {
	s := &SignozSource{}
	mapping := s.GetLabelMapping()

	// SignozSource returns an empty map
	assert.NotNil(t, mapping)
	assert.Equal(t, 0, len(mapping))
	assert.Equal(t, map[string]string{}, mapping)
}

// Test FlattenSignozQuery with multiple fields (order-independent)
func TestFlattenSignozQuery_MultipleFields(t *testing.T) {
	queryClause := query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			"service":  {Eq: "api"},
			"env":      {Eq: "prod"},
			"status":   {In: []any{"ok", "error"}},
			"trace_id": {Exist: true},
		},
	}

	result, err := FlattenSignozQuery(queryClause)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(result), "Expected 4 clauses")

	// Check each expected clause exists (order-independent)
	assert.True(t, containsClause(result, "service", "=", "api"), "Should contain service=api clause")
	assert.True(t, containsClause(result, "env", "=", "prod"), "Should contain env=prod clause")
	assert.True(t, containsClause(result, "status", "in", []any{"ok", "error"}), "Should contain status in clause")
	assert.True(t, containsClause(result, "trace_id", "exists", true), "Should contain trace_id exists clause")
}

// Test edge cases for FlattenSignozQuery
func TestFlattenSignozQuery_EdgeCases(t *testing.T) {
	testCases := []struct {
		name        string
		queryClause query.QueryWhereClause
		expectError bool
	}{
		{
			name: "Field with multiple operators",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"field": {
						Eq:   "value1",
						Like: "value2",
					},
				},
			},
			expectError: false, // Should create multiple clauses
		},
		{
			name: "Mix of supported and unsupported operators",
			queryClause: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"field1": {Eq: "value"},
					"field2": {Gt: 100}, // Unsupported
				},
			},
			expectError: true, // Should error on unsupported operator
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FlattenSignozQuery(tc.queryClause)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test FlattenedClause JSON marshaling
func TestFlattenedClause_JSONMarshaling(t *testing.T) {
	clause := FlattenedClause{
		Key:   map[string]string{"key": "service"},
		Value: "api-service",
		Op:    "=",
	}

	// Test marshaling
	data, err := json.Marshal(clause)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"key":{"key":"service"}`)
	assert.Contains(t, string(data), `"value":"api-service"`)
	assert.Contains(t, string(data), `"op":"="`)

	// Test unmarshaling
	var unmarshaled FlattenedClause
	err = json.Unmarshal(data, &unmarshaled)
	assert.NoError(t, err)
	assert.Equal(t, clause.Key, unmarshaled.Key)
	assert.Equal(t, clause.Value, unmarshaled.Value)
	assert.Equal(t, clause.Op, unmarshaled.Op)
}
