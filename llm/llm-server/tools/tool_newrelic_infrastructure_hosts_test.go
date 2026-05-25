package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRelicInfrastructureHostsTool_Name(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}
	assert.Equal(t, ToolNewRelicInfrastructureHosts, tool.Name())
}

func TestNewRelicInfrastructureHostsTool_Description(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}
	desc := tool.Description()
	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "infrastructure")
	assert.Contains(t, desc, "host")
}

func TestNewRelicInfrastructureHostsTool_InputSchema(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}
	schema := tool.InputSchema()

	assert.NotNil(t, schema.Properties)
	assert.Contains(t, schema.Properties, "command")
	assert.Equal(t, 1, len(schema.Required))
	assert.Contains(t, schema.Required, "command")
}

func TestNewRelicInfrastructureHostsTool_CleanupQuery(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain query",
			input:    "name:prod-web-01",
			expected: "name:prod-web-01",
		},
		{
			name:     "Query with backticks",
			input:    "`name:prod-web-01`",
			expected: "name:prod-web-01",
		},
		{
			name:     "Query with spaces",
			input:    "  name:prod-web-01  ",
			expected: "name:prod-web-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.cleanupQuery(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewRelicInfrastructureHostsTool_SimplifyEntity(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}

	entity := map[string]any{
		"guid":      "XYZ123",
		"name":      "prod-web-01",
		"type":      "HOST",
		"domain":    "INFRA",
		"accountId": float64(7284213),
		"hostSummary": map[string]any{
			"cpuUtilizationPercent": 75.5,
			"memoryUsedPercent":     82.3,
			"diskUsedPercent":       45.1,
		},
	}

	result := tool.simplifyEntity(entity)

	assert.Equal(t, "XYZ123", result["guid"])
	assert.Equal(t, "prod-web-01", result["name"])
	assert.Equal(t, "HOST", result["type"])
	assert.Equal(t, "INFRA", result["domain"])
	assert.Equal(t, 7284213, result["account_id"])
	assert.Equal(t, 75.5, result["cpu_utilization_percent"])
	assert.Equal(t, 82.3, result["memory_used_percent"])
	assert.Equal(t, 45.1, result["disk_used_percent"])
}

func TestNewRelicInfrastructureHostsTool_SimplifyEntity_EdgeCases(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}

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
				assert.NotContains(t, result, "cpu_utilization_percent")
			},
		},
		{
			name: "Partial hostSummary",
			entity: map[string]any{
				"guid": "XYZ123",
				"hostSummary": map[string]any{
					"cpuUtilizationPercent": 75.5,
					// Missing memory and disk
				},
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, 75.5, result["cpu_utilization_percent"])
				assert.NotContains(t, result, "memory_used_percent")
				assert.NotContains(t, result, "disk_used_percent")
			},
		},
		{
			name: "Wrong type in hostSummary",
			entity: map[string]any{
				"guid": "XYZ123",
				"hostSummary": map[string]any{
					"cpuUtilizationPercent": "high", // String instead of float
				},
			},
			check: func(t *testing.T, result map[string]any) {
				assert.NotContains(t, result, "cpu_utilization_percent")
			},
		},
		{
			name: "hostSummary is null",
			entity: map[string]any{
				"guid":        "XYZ123",
				"hostSummary": nil,
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "XYZ123", result["guid"])
				assert.NotContains(t, result, "cpu_utilization_percent")
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

func TestNewRelicInfrastructureHostsTool_ParseEntitySearchResponse(t *testing.T) {
	tool := NewRelicInfrastructureHostsTool{}

	tests := []struct {
		name          string
		response      map[string]any
		expectedCount int
		expectedLen   int
	}{
		{
			name: "Valid response with hosts",
			response: map[string]any{
				"data": map[string]any{
					"actor": map[string]any{
						"entitySearch": map[string]any{
							"count": float64(2),
							"results": map[string]any{
								"entities": []any{
									map[string]any{"guid": "HOST1", "name": "prod-web-01"},
									map[string]any{"guid": "HOST2", "name": "prod-web-02"},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entities, count := tool.parseEntitySearchResponse(tt.response)
			assert.Equal(t, tt.expectedCount, count)
			assert.Equal(t, tt.expectedLen, len(entities))
		})
	}
}
