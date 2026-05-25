package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryConfidence(t *testing.T) {
	tests := []struct {
		name     string
		memType  MemoryType
		expected float64
	}{
		{"user_preference highest", MemoryTypeUserPreference, 0.9},
		{"pattern", MemoryTypePattern, 0.8},
		{"workflow", MemoryTypeWorkflow, 0.8},
		{"architectural_fact", MemoryTypeArchitecturalFact, 0.7},
		{"config_insight", MemoryTypeConfigInsight, 0.7},
		{"dependency_mapping", MemoryTypeDependencyMapping, 0.7},
		{"troubleshooting", MemoryTypeTroubleshooting, 0.7},
		{"investigation_result default", MemoryTypeInvestigationResult, 0.5},
		{"unknown type falls to default", MemoryType("unknown"), 0.5},
		{"empty string falls to default", MemoryType(""), 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, memoryConfidence(tc.memType))
		})
	}
}

func TestIsRedundantWithConversationContext(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		convFacts []string
		expected  bool
	}{
		{
			name:      "exact match is redundant",
			content:   "ECR pulls need imagePullSecrets",
			convFacts: []string{"ECR pulls need imagePullSecrets"},
			expected:  true,
		},
		{
			name:      "case-insensitive match",
			content:   "ECR Pulls Need ImagePullSecrets",
			convFacts: []string{"ecr pulls need imagepullsecrets"},
			expected:  true,
		},
		{
			name:      "minor edit within 15% threshold",
			content:   "ECR pulls needs imagePullSecrets",
			convFacts: []string{"ECR pulls need imagePullSecrets"},
			expected:  true,
		},
		{
			name:      "larger edit exceeds 15% threshold",
			content:   "ECR pulls require imagePullSecrets",
			convFacts: []string{"ECR pulls need imagePullSecrets"},
			expected:  false, // "require" vs "need" = 6 edits / 34 chars = 17.6% > 15%
		},
		{
			name:      "different content is not redundant",
			content:   "services-server OOM caused by cache growth",
			convFacts: []string{"ECR pulls need imagePullSecrets"},
			expected:  false,
		},
		{
			name:      "empty convFacts returns false",
			content:   "anything",
			convFacts: nil,
			expected:  false,
		},
		{
			name:      "empty content is redundant with empty convFact",
			content:   "",
			convFacts: []string{""},
			expected:  false, // maxLen == 0 → division guard prevents match
		},
		{
			name:      "whitespace trimmed before comparison",
			content:   "  ECR pulls need imagePullSecrets  ",
			convFacts: []string{"ECR pulls need imagePullSecrets"},
			expected:  true,
		},
		{
			name:      "matches any one convFact",
			content:   "k8s-collector depends on relay-server",
			convFacts: []string{"completely different fact", "k8s-collector depends on relay-server"},
			expected:  true,
		},
		{
			name:      "significantly different wording is not redundant",
			content:   "beehive-dev-pg slow queries caused by autovacuum lag",
			convFacts: []string{"services-server pods getting OOM killed"},
			expected:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isRedundantWithConversationContext(tc.content, tc.convFacts)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestMemoryTypeValidate(t *testing.T) {
	tests := []struct {
		input    string
		expected MemoryType
	}{
		{"user_preference", MemoryTypeUserPreference},
		{"pattern", MemoryTypePattern},
		{"workflow", MemoryTypeWorkflow},
		{"investigation_result", MemoryTypeInvestigationResult},
		{"architectural_fact", MemoryTypeArchitecturalFact},
		{"dependency_mapping", MemoryTypeDependencyMapping},
		{"troubleshooting_guide", MemoryTypeTroubleshooting},
		{"configuration_insight", MemoryTypeConfigInsight},
		{"unknown_type", MemoryTypeInvestigationResult},
		{"", MemoryTypeInvestigationResult},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := MemoryType(tc.input).Validate()
			assert.Equal(t, tc.expected, result)
		})
	}
}
