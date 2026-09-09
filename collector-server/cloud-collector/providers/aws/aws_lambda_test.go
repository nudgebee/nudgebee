package aws

import (
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Most of these rules fire on essentially every Lambda function in an account,
// so their severity sets the tone of the whole Config category. Each expectation
// below tracks the published rating for the equivalent industry check; raising
// one means disagreeing with that baseline, not just tuning a constant.
func TestLambdaRuleSeveritiesMatchIndustryBaseline(t *testing.T) {
	tests := []struct {
		rule     string
		expected providers.RecommendationSeverity
		baseline string
	}{
		{"aws_lambda_tracing", providers.RecommendationSeverityLow, "AWS FSBP Lambda.7, Checkov CKV_AWS_50"},
		{"aws_lambda_dead_letter_queue", providers.RecommendationSeverityLow, "Checkov CKV_AWS_116; AWS retired this FSBP control"},
		{"aws_lambda_reserved_concurrency", providers.RecommendationSeverityLow, "Checkov CKV_AWS_115"},
		{"aws_lambda_environment_variable_encryption", providers.RecommendationSeverityLow, "Checkov CKV_AWS_173; AWS encrypts env vars by default"},
		{"aws_lambda_provisioned_concurrency", providers.RecommendationSeverityLow, "no industry check; enabling it increases spend"},
		{"aws_lambda_deprecated_runtime", providers.RecommendationSeverityMedium, "AWS FSBP Lambda.2"},
	}

	// Meta deliberately omits TracingConfig, DeadLetterConfig, Concurrency,
	// ProvisionedConcurrency and KMSKeyArn so every rule under test fires, and
	// pins a deprecated runtime so the runtime rule fires too.
	resources := []providers.Resource{{
		Id:          "arn:aws:lambda:us-east-1:123456789012:function:example",
		ServiceName: "AWSLambda",
		Type:        "function",
		Region:      "us-east-1",
		Meta: map[string]any{
			"Runtime": "python3.7",
		},
	}}

	recommendations, err := (&awsLambda{}).GetRecommendations(nil, providers.Account{}, providers.ListRecommendationsRequest{}, resources)
	require.NoError(t, err)

	severityByRule := map[string]providers.RecommendationSeverity{}
	for _, r := range recommendations {
		severityByRule[r.RuleName] = r.Severity
	}

	for _, tc := range tests {
		t.Run(tc.rule, func(t *testing.T) {
			severity, emitted := severityByRule[tc.rule]
			require.True(t, emitted, "expected %s to fire for this function", tc.rule)
			assert.Equal(t, tc.expected, severity, "%s should match %s", tc.rule, tc.baseline)
		})
	}
}
