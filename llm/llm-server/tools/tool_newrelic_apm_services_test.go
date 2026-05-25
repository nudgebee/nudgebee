package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRelicAPMServicesTool_Name(t *testing.T) {
	tool := NewRelicAPMServicesTool{}
	assert.Equal(t, ToolNewRelicAPMServices, tool.Name())
}

func TestNewRelicAPMServicesTool_Description(t *testing.T) {
	tool := NewRelicAPMServicesTool{}
	desc := tool.Description()
	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "APM")
}

func TestNewRelicAPMServicesTool_InputSchema(t *testing.T) {
	tool := NewRelicAPMServicesTool{}
	schema := tool.InputSchema()

	assert.NotNil(t, schema.Properties)
	assert.Contains(t, schema.Properties, "command")
	assert.Equal(t, 1, len(schema.Required))
	assert.Contains(t, schema.Required, "command")
}

func TestNewRelicAPMServicesTool_CleanupQuery(t *testing.T) {
	tool := NewRelicAPMServicesTool{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain query",
			input:    "name:api-server",
			expected: "name:api-server",
		},
		{
			name:     "Query with backticks",
			input:    "`name:api-server`",
			expected: "name:api-server",
		},
		{
			name:     "Query with spaces",
			input:    "  name:api-server  ",
			expected: "name:api-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.cleanupQuery(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewRelicAPMServicesTool_SimplifyEntity(t *testing.T) {
	tool := NewRelicAPMServicesTool{}

	entity := map[string]any{
		"guid":          "MTIzNDU2fEFQTXxBUFBMSUNBVElPTnwxMjM0NTY",
		"name":          "api-server",
		"type":          "APPLICATION",
		"domain":        "APM",
		"accountId":     float64(7284213),
		"language":      "go",
		"applicationId": float64(123456),
		"alertSeverity": "NOT_CONFIGURED",
		"apmSummary": map[string]any{
			"errorRate":           0.02,
			"responseTimeAverage": 125.5,
			"throughput":          450.2,
			"apdexScore":          0.95,
		},
	}

	result := tool.simplifyEntity(entity)

	assert.Equal(t, "MTIzNDU2fEFQTXxBUFBMSUNBVElPTnwxMjM0NTY", result["guid"])
	assert.Equal(t, "api-server", result["name"])
	assert.Equal(t, "APPLICATION", result["type"])
	assert.Equal(t, "APM", result["domain"])
	assert.Equal(t, 7284213, result["account_id"])
	assert.Equal(t, "go", result["language"])
	assert.Equal(t, 123456, result["application_id"])
	assert.Equal(t, "NOT_CONFIGURED", result["alert_severity"])
	assert.Equal(t, 0.02, result["error_rate"])
	assert.Equal(t, 125.5, result["response_time_avg"])
	assert.Equal(t, 450.2, result["throughput"])
	assert.Equal(t, 0.95, result["apdex_score"])
}

func TestGetStringValue(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		key      string
		expected string
	}{
		{
			name:     "String value exists",
			input:    map[string]any{"name": "api-server"},
			key:      "name",
			expected: "api-server",
		},
		{
			name:     "Key does not exist",
			input:    map[string]any{"name": "api-server"},
			key:      "missing",
			expected: "",
		},
		{
			name:     "Value is not string",
			input:    map[string]any{"count": 123},
			key:      "count",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringValue(tt.input, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNewRelicAPMServicesTool_SimplifyEntity_EdgeCases tests edge cases in entity simplification
func TestNewRelicAPMServicesTool_SimplifyEntity_EdgeCases(t *testing.T) {
	tool := NewRelicAPMServicesTool{}

	tests := []struct {
		name   string
		entity map[string]any
		check  func(t *testing.T, result map[string]any)
	}{
		{
			name:   "Empty entity",
			entity: map[string]any{},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "", result["guid"])
				assert.Equal(t, "", result["name"])
				assert.NotContains(t, result, "account_id")
				assert.NotContains(t, result, "error_rate")
			},
		},
		{
			name: "accountId is string (wrong type)",
			entity: map[string]any{
				"guid":      "ABC123",
				"accountId": "not-a-number",
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "ABC123", result["guid"])
				assert.NotContains(t, result, "account_id")
			},
		},
		{
			name: "Partial apmSummary",
			entity: map[string]any{
				"guid": "ABC123",
				"apmSummary": map[string]any{
					"errorRate": 0.02,
					// Missing other fields
				},
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, 0.02, result["error_rate"])
				assert.NotContains(t, result, "throughput")
				assert.NotContains(t, result, "apdex_score")
			},
		},
		{
			name: "Wrong type in apmSummary",
			entity: map[string]any{
				"guid": "ABC123",
				"apmSummary": map[string]any{
					"errorRate": "high", // String instead of float
				},
			},
			check: func(t *testing.T, result map[string]any) {
				assert.NotContains(t, result, "error_rate")
			},
		},
		{
			name: "apmSummary is null",
			entity: map[string]any{
				"guid":       "ABC123",
				"apmSummary": nil,
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "ABC123", result["guid"])
				assert.NotContains(t, result, "error_rate")
			},
		},
		{
			name: "All optional fields present",
			entity: map[string]any{
				"guid":          "ABC123",
				"name":          "api-server",
				"type":          "APPLICATION",
				"domain":        "APM",
				"accountId":     float64(12345),
				"language":      "go",
				"applicationId": float64(67890),
				"alertSeverity": "CRITICAL",
				"apmSummary": map[string]any{
					"errorRate":           0.05,
					"responseTimeAverage": 200.0,
					"throughput":          1000.0,
					"apdexScore":          0.85,
				},
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "ABC123", result["guid"])
				assert.Equal(t, "api-server", result["name"])
				assert.Equal(t, 12345, result["account_id"])
				assert.Equal(t, "go", result["language"])
				assert.Equal(t, 67890, result["application_id"])
				assert.Equal(t, "CRITICAL", result["alert_severity"])
				assert.Equal(t, 0.05, result["error_rate"])
				assert.Equal(t, 200.0, result["response_time_avg"])
				assert.Equal(t, 1000.0, result["throughput"])
				assert.Equal(t, 0.85, result["apdex_score"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.simplifyEntity(tt.entity)
			tt.check(t, result)
		})
	}
}

// TestNewRelicAPMServicesTool_ParseEntitySearchResponse tests response parsing edge cases
func TestNewRelicAPMServicesTool_ParseEntitySearchResponse(t *testing.T) {
	tool := NewRelicAPMServicesTool{}

	tests := []struct {
		name          string
		response      map[string]any
		expectedCount int
		expectedLen   int
	}{
		{
			name: "Valid response with entities",
			response: map[string]any{
				"data": map[string]any{
					"actor": map[string]any{
						"entitySearch": map[string]any{
							"count": float64(2),
							"results": map[string]any{
								"entities": []any{
									map[string]any{"guid": "ABC", "name": "api-server"},
									map[string]any{"guid": "DEF", "name": "web-server"},
								},
							},
						},
					},
				},
			},
			expectedCount: 2,
			expectedLen:   2,
		},
		{
			name:          "Empty response",
			response:      map[string]any{},
			expectedCount: 0,
			expectedLen:   0,
		},
		{
			name: "Missing entities array",
			response: map[string]any{
				"data": map[string]any{
					"actor": map[string]any{
						"entitySearch": map[string]any{
							"count": float64(0),
						},
					},
				},
			},
			expectedCount: 0,
			expectedLen:   0,
		},
		{
			name: "Missing entitySearch",
			response: map[string]any{
				"data": map[string]any{
					"actor": map[string]any{},
				},
			},
			expectedCount: 0,
			expectedLen:   0,
		},
		{
			name: "Count as integer instead of float",
			response: map[string]any{
				"data": map[string]any{
					"actor": map[string]any{
						"entitySearch": map[string]any{
							"count": 1, // int instead of float64
							"results": map[string]any{
								"entities": []any{
									map[string]any{"guid": "ABC", "name": "api-server"},
								},
							},
						},
					},
				},
			},
			expectedCount: 0, // Won't match int type
			expectedLen:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entities, count := tool.parseEntitySearchResponse(tt.response)
			assert.Equal(t, tt.expectedCount, count)
			assert.Equal(t, tt.expectedLen, len(entities))
		})
	}
}
