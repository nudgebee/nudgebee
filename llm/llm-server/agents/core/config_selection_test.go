package core

import (
	toolcore "nudgebee/llm/tools/core"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFindConfigInQuery(t *testing.T) {
	configs := []toolcore.ToolConfig{
		{
			Name: "gcp-dev",
			Values: []toolcore.ToolConfigValue{
				{Name: "id", Value: "project-123"},
				{Name: "cluster_id", Value: "g"}, // for short identifier test
			},
		},
		{
			Name: "gcp-dev - nudgebee-dev",
			Values: []toolcore.ToolConfigValue{
				{Name: "id", Value: "nudgebee-dev"},
				{Name: "project_id", Value: "gcp-dev"}, // conflicting value for Name > Value test
			},
		},
		{Name: "aws-prod"},
	}

	testCases := []struct {
		name     string
		query    string
		expected string // Expected config name, or "" if nil
	}{
		{
			name:     "Exact match - gcp-dev",
			query:    "use 'gcp-dev' account",
			expected: "gcp-dev",
		},
		{
			name:     "Value match - nudgebee-dev",
			query:    "Get metrics for project nudgebee-dev",
			expected: "gcp-dev - nudgebee-dev",
		},
		{
			name:     "Case insensitive match",
			query:    "USE GCP-DEV ACCOUNT",
			expected: "gcp-dev",
		},
		{
			name:     "Ambiguous match - unrelated configs",
			query:    "use gcp-dev and aws-prod",
			expected: "", // Should return nil
		},
		{
			name:     "Partial match should NOT match if not explicitly a config name or value",
			query:    "use nudgebee",
			expected: "", // "nudgebee" is not a whole config name or value
		},
		{
			name:     "Short identifier should be ignored",
			query:    "Get pods for project g",
			expected: "", // "g" is < 3 chars, should not auto-match to avoid false positives
		},
		{
			name:     "Name takes priority over Value",
			query:    "use gcp-dev",
			expected: "gcp-dev", // Should match gcp-dev name even if another config has "gcp-dev" as a value
		},
		{
			name:     "Gcloud verbatim example",
			query:    `gcloud logging read ... --project=nudgebee-dev using 'gcp-dev - nudgebee-dev' account`,
			expected: "gcp-dev - nudgebee-dev",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			matched := findConfigInQuery(tc.query, configs)
			if tc.expected == "" {
				assert.Nil(t, matched)
			} else {
				assert.NotNil(t, matched)
				assert.Equal(t, tc.expected, matched.Name)
			}
		})
	}
}
