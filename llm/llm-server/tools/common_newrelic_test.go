package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetNewRelicGraphQLEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		region   string
		expected string
	}{
		{
			name:     "US region",
			region:   "us",
			expected: "https://api.newrelic.com/graphql",
		},
		{
			name:     "EU region",
			region:   "eu",
			expected: "https://api.eu.newrelic.com/graphql",
		},
		{
			name:     "Unknown region defaults to US",
			region:   "invalid",
			expected: "https://api.newrelic.com/graphql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNewRelicGraphQLEndpoint(tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetNewRelicConfigSchema(t *testing.T) {
	schema := getNewRelicConfigSchema()

	assert.NotNil(t, schema.Properties)
	assert.Equal(t, "newrelic", schema.ConfigType)
	assert.Contains(t, schema.Properties, "api_key")
	assert.Contains(t, schema.Properties, "nr_account_id")
	assert.Contains(t, schema.Properties, "region")

	// Check that api_key is encrypted
	apiKeyProp := schema.Properties["api_key"]
	assert.True(t, apiKeyProp.IsEncrypted)

	// Check region has enum values
	regionProp := schema.Properties["region"]
	assert.NotNil(t, regionProp.Enum)
	assert.Contains(t, regionProp.Enum, "us")
	assert.Contains(t, regionProp.Enum, "eu")

	// Check required fields
	assert.Contains(t, schema.Required, "api_key")
	assert.Contains(t, schema.Required, "nr_account_id")
	assert.Contains(t, schema.Required, "region")
}

func TestBuildEntitySearchQuery_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		domain     string
		entityType string
		filters    string
		expected   string
	}{
		{
			name:       "Empty domain",
			domain:     "",
			entityType: "APPLICATION",
			filters:    "name:api",
			expected:   "type='APPLICATION' AND name LIKE '%api%'",
		},
		{
			name:       "Empty entity type",
			domain:     "APM",
			entityType: "",
			filters:    "name:api",
			expected:   "domain='APM' AND name LIKE '%api%'",
		},
		{
			name:       "Empty filters",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "",
			expected:   "domain='APM' AND type='APPLICATION'",
		},
		{
			name:       "Filter with extra spaces",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "  name:api   tags.env:prod  ",
			expected:   "domain='APM' AND type='APPLICATION' AND name LIKE '%api%' AND tags.env='prod'",
		},
		{
			name:       "Generic field filter",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "language:go",
			expected:   "domain='APM' AND type='APPLICATION' AND language='go'",
		},
		{
			name:       "Empty key ignored",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    ":value",
			expected:   "domain='APM' AND type='APPLICATION'",
		},
		{
			name:       "Empty value ignored",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "name:",
			expected:   "domain='APM' AND type='APPLICATION'",
		},
		{
			name:       "Multiple colons in value",
			domain:     "APM",
			entityType: "APPLICATION",
			filters:    "name:api:v2",
			expected:   "domain='APM' AND type='APPLICATION' AND name LIKE '%api:v2%'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildEntitySearchQuery(tt.domain, tt.entityType, tt.filters)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBuildEntitySearchQuery_Security tests protection against injection attacks
func TestBuildEntitySearchQuery_Security(t *testing.T) {
	tests := []struct {
		name             string
		filters          string
		shouldNotContain []string
	}{
		{
			name:             "Single quote injection",
			filters:          "name:api's-server",
			shouldNotContain: []string{"api's-server"}, // Should be escaped
		},
		{
			name:             "Double quote injection",
			filters:          `name:api"server`,
			shouldNotContain: []string{`api"server`}, // Should be escaped
		},
		{
			name:             "GraphQL braces injection",
			filters:          "name:api} {__schema{types{name}}}",
			shouldNotContain: []string{"{__schema", "__schema", "types{name}"},
		},
		{
			name:             "Backslash injection",
			filters:          `name:api\server`,
			shouldNotContain: []string{`api\server`}, // Should be escaped
		},
		{
			name:             "Newline injection",
			filters:          "name:api\nDROP TABLE",
			shouldNotContain: []string{"DROP TABLE"},
		},
		{
			name:             "Invalid field name with special chars",
			filters:          "field{bad}:value",
			shouldNotContain: []string{"field{bad}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildEntitySearchQuery("APM", "APPLICATION", tt.filters)

			// Verify query is not empty (injection didn't break it)
			assert.NotEmpty(t, result)

			// Verify injection attempts are neutralized
			for _, badString := range tt.shouldNotContain {
				assert.NotContains(t, result, badString, "Query should not contain injection attempt: %s", badString)
			}

			// Verify query still has valid structure
			assert.Contains(t, result, "domain='APM'")
			assert.Contains(t, result, "type='APPLICATION'")
		})
	}
}

// TestEscapeGraphQLString tests the escaping function
func TestEscapeGraphQLString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Single quote",
			input:    "api's-server",
			expected: "api\\'s-server",
		},
		{
			name:     "Double quote",
			input:    `api"server`,
			expected: `api\"server`,
		},
		{
			name:     "Backslash",
			input:    `api\server`,
			expected: `api\\server`,
		},
		{
			name:     "Braces removed",
			input:    "api{schema}",
			expected: "apischema",
		},
		{
			name:     "Newline",
			input:    "api\nserver",
			expected: `api\nserver`,
		},
		{
			name:     "Tab",
			input:    "api\tserver",
			expected: `api\tserver`,
		},
		{
			name:     "Multiple special chars",
			input:    `api's\n"server"`,
			expected: `api\\'s\\n\"server\"`,
		},
		{
			name:     "No special chars",
			input:    "api-server",
			expected: "api-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeGraphQLString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsValidFieldName tests field name validation
func TestIsValidFieldName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Valid alphanumeric",
			input:    "serviceName",
			expected: true,
		},
		{
			name:     "Valid with underscore",
			input:    "service_name",
			expected: true,
		},
		{
			name:     "Valid with dot",
			input:    "tags.environment",
			expected: true,
		},
		{
			name:     "Valid with hyphen",
			input:    "service-name",
			expected: true,
		},
		{
			name:     "Invalid with brace",
			input:    "field{bad}",
			expected: false,
		},
		{
			name:     "Invalid with quote",
			input:    "field'bad",
			expected: false,
		},
		{
			name:     "Invalid with space",
			input:    "field bad",
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "Valid numbers",
			input:    "field123",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidFieldName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
