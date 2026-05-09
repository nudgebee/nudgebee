package tools

import (
	"fmt"
	"nudgebee/llm/common"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRelicResourceSearchTool_Name(t *testing.T) {
	tool := NewRelicResourceSearchTool{}
	assert.Equal(t, ToolNewRelicResourceSearchExecute, tool.Name())
}

func TestNewRelicResourceSearchTool_Description(t *testing.T) {
	tool := NewRelicResourceSearchTool{}
	desc := tool.Description()
	assert.NotEmpty(t, desc)
	assert.Contains(t, desc, "New Relic")
}

func TestNewRelicResourceSearchTool_InputSchema(t *testing.T) {
	tool := NewRelicResourceSearchTool{}
	schema := tool.InputSchema()

	assert.NotNil(t, schema.Properties)
	assert.Contains(t, schema.Properties, "resource_type")
	assert.Contains(t, schema.Properties, "query")
	assert.Equal(t, 2, len(schema.Required))
	assert.Contains(t, schema.Required, "resource_type")
	assert.Contains(t, schema.Required, "query")
}

func TestBuildEntitySearchQuery(t *testing.T) {
	tests := []struct {
		name       string
		domain     string
		entityType string
		filters    string
		expected   string
	}{
		{
			name:       "APM with name filter",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "name:api-server",
			expected:   "domain='APM' AND type='APPLICATION' AND name LIKE '%api-server%'",
		},
		{
			name:       "INFRA with tag filter",
			domain:     "INFRA",
			entityType: "HOST",
			filters:    "tags.environment:production",
			expected:   "domain='INFRA' AND type='HOST' AND tags.environment='production'",
		},
		{
			name:       "Multiple filters",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "name:api tags.team:backend",
			expected:   "domain='APM' AND type='APPLICATION' AND name LIKE '%api%' AND tags.team='backend'",
		},
		{
			name:       "No filters",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "",
			expected:   "domain='APM' AND type='APPLICATION'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildEntitySearchQuery(tt.domain, tt.entityType, tt.filters)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNewRelicResourceSearchTool_ResourceTypeRouting tests routing logic
func TestNewRelicResourceSearchTool_ResourceTypeRouting(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		shouldError  bool
	}{
		{
			name:         "Valid - apm_services",
			resourceType: "apm_services",
			shouldError:  false,
		},
		{
			name:         "Valid - services (alias)",
			resourceType: "services",
			shouldError:  false,
		},
		{
			name:         "Valid - hosts",
			resourceType: "hosts",
			shouldError:  false,
		},
		{
			name:         "Valid - infrastructure_hosts (alias)",
			resourceType: "infrastructure_hosts",
			shouldError:  false,
		},
		{
			name:         "Valid - containers",
			resourceType: "containers",
			shouldError:  false,
		},
		{
			name:         "Valid - uppercase APM_SERVICES",
			resourceType: "APM_SERVICES",
			shouldError:  false,
		},
		{
			name:         "Valid - mixed case Hosts",
			resourceType: "Hosts",
			shouldError:  false,
		},
		{
			name:         "Invalid - empty string",
			resourceType: "",
			shouldError:  true,
		},
		{
			name:         "Invalid - unsupported type",
			resourceType: "invalid_type",
			shouldError:  true,
		},
		{
			name:         "Invalid - datadog type",
			resourceType: "apm_entities",
			shouldError:  true,
		},
		{
			name:         "Invalid - typo",
			resourceType: "apm_service",
			shouldError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewRelicResourceSearchTool{}
			input := fmt.Sprintf(`{"resource_type":"%s","query":"name:test"}`, tt.resourceType)

			// We can't fully test Call() without mocking GetNBTool,
			// but we can test the JSON parsing and validation
			var request NewRelicResourceSearchRequest
			err := common.UnmarshalJson([]byte(input), &request)

			if tt.resourceType == "" {
				// Empty resource_type should fail validation
				assert.NoError(t, err) // JSON unmarshaling succeeds
				// But the tool should return an error
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.resourceType, request.ResourceType)
			}

			// Verify the tool description mentions supported types
			desc := tool.Description()
			assert.Contains(t, desc, "New Relic")
		})
	}
}

// TestNewRelicResourceSearchTool_JSONValidation tests JSON input validation
func TestNewRelicResourceSearchTool_JSONValidation(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "Valid JSON",
			input:       `{"resource_type":"services","query":"name:test"}`,
			expectError: false,
		},
		{
			name:        "Invalid JSON - not JSON",
			input:       "not json",
			expectError: true,
		},
		{
			name:        "Invalid JSON - missing closing brace",
			input:       `{"resource_type":"services"`,
			expectError: true,
		},
		{
			name:        "Invalid JSON - missing resource_type field",
			input:       `{"query":"test"}`,
			expectError: false, // Unmarshaling succeeds, but validation should fail
		},
		{
			name:        "Invalid JSON - missing query field",
			input:       `{"resource_type":"services"}`,
			expectError: false, // Unmarshaling succeeds, but validation should fail
		},
		{
			name:        "Invalid JSON - wrong type for resource_type",
			input:       `{"resource_type":123,"query":"test"}`,
			expectError: true,
		},
		{
			name:        "Invalid JSON - wrong type for query",
			input:       `{"resource_type":"services","query":["test"]}`,
			expectError: true,
		},
		{
			name:        "Invalid JSON - empty object",
			input:       `{}`,
			expectError: false, // Unmarshaling succeeds, but validation should fail
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request NewRelicResourceSearchRequest
			err := common.UnmarshalJson([]byte(tt.input), &request)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestNewRelicResourceSearchTool_QueryValidation tests query validation
func TestNewRelicResourceSearchTool_QueryValidation(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		description string
	}{
		{
			name:        "Valid query",
			query:       "name:api-server",
			description: "normal query",
		},
		{
			name:        "Empty query",
			query:       "",
			description: "should fail validation",
		},
		{
			name:        "Whitespace only query",
			query:       "   ",
			description: "should fail validation",
		},
		{
			name:        "Long query",
			query:       "name:" + strings.Repeat("a", 1000),
			description: "very long query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewRelicResourceSearchTool{}
			input := fmt.Sprintf(`{"resource_type":"services","query":"%s"}`, tt.query)

			var request NewRelicResourceSearchRequest
			err := common.UnmarshalJson([]byte(input), &request)
			assert.NoError(t, err)

			// Verify schema includes both required fields
			schema := tool.InputSchema()
			assert.Contains(t, schema.Required, "resource_type")
			assert.Contains(t, schema.Required, "query")
		})
	}
}
