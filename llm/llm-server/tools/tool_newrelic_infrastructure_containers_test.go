package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRelicInfrastructureContainersTool_Name(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}
	assert.Equal(t, ToolNewRelicInfrastructureContainers, tool.Name())
}

func TestNewRelicInfrastructureContainersTool_Description(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}
	desc := tool.Description()
	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "container")
}

func TestNewRelicInfrastructureContainersTool_InputSchema(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}
	schema := tool.InputSchema()

	assert.NotNil(t, schema.Properties)
	assert.Contains(t, schema.Properties, "command")
	assert.Equal(t, 1, len(schema.Required))
	assert.Contains(t, schema.Required, "command")
}

func TestNewRelicInfrastructureContainersTool_CleanupQuery(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain query",
			input:    "name:nginx",
			expected: "name:nginx",
		},
		{
			name:     "Query with backticks",
			input:    "`name:nginx`",
			expected: "name:nginx",
		},
		{
			name:     "Query with spaces",
			input:    "  name:nginx  ",
			expected: "name:nginx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.cleanupQuery(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewRelicInfrastructureContainersTool_SimplifyEntity(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}

	entity := map[string]any{
		"guid":      "CONTAINER123",
		"name":      "nginx-pod-abc",
		"type":      "CONTAINER",
		"domain":    "INFRA",
		"accountId": float64(7284213),
	}

	result := tool.simplifyEntity(entity)

	assert.Equal(t, "CONTAINER123", result["guid"])
	assert.Equal(t, "nginx-pod-abc", result["name"])
	assert.Equal(t, "CONTAINER", result["type"])
	assert.Equal(t, "INFRA", result["domain"])
	assert.Equal(t, 7284213, result["account_id"])
}

func TestNewRelicInfrastructureContainersTool_SimplifyEntity_EdgeCases(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}

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
			},
		},
		{
			name: "accountId is string (wrong type)",
			entity: map[string]any{
				"guid":      "CONTAINER123",
				"accountId": "not-a-number",
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "CONTAINER123", result["guid"])
				assert.NotContains(t, result, "account_id")
			},
		},
		{
			name: "Missing optional fields",
			entity: map[string]any{
				"guid": "CONTAINER123",
			},
			check: func(t *testing.T, result map[string]any) {
				assert.Equal(t, "CONTAINER123", result["guid"])
				assert.Equal(t, "", result["name"])
				assert.Equal(t, "", result["type"])
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

func TestNewRelicInfrastructureContainersTool_ParseEntitySearchResponse(t *testing.T) {
	tool := NewRelicInfrastructureContainersTool{}

	tests := []struct {
		name          string
		response      map[string]any
		expectedCount int
		expectedLen   int
	}{
		{
			name: "Valid response with containers",
			response: map[string]any{
				"data": map[string]any{
					"actor": map[string]any{
						"entitySearch": map[string]any{
							"count": float64(2),
							"results": map[string]any{
								"entities": []any{
									map[string]any{"guid": "CONT1", "name": "nginx-1"},
									map[string]any{"guid": "CONT2", "name": "nginx-2"},
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
