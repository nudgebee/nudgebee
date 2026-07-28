package aws

import (
	"context"
	"os"
	"testing"

	"nudgebee/collector/cloud/providers"

	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapCOFindingToSeverity(t *testing.T) {
	tests := []struct {
		name     string
		finding  cotypes.Finding
		expected providers.RecommendationSeverity
	}{
		{"OverProvisioned", cotypes.FindingOverProvisioned, providers.RecommendationSeverityMedium},
		{"UnderProvisioned", cotypes.FindingUnderProvisioned, providers.RecommendationSeverityHigh},
		{"Optimized", cotypes.FindingOptimized, providers.RecommendationSeverityLow},
		{"NotOptimized", cotypes.FindingNotOptimized, providers.RecommendationSeverityLow},
		{"empty finding", cotypes.Finding(""), providers.RecommendationSeverityLow},
		{"unknown finding", cotypes.Finding("SomeFutureFinding"), providers.RecommendationSeverityLow},
		// The GetEC2InstanceRecommendations API returns findings in
		// SCREAMING_SNAKE_CASE, not the SDK's mixed-case enum values. These
		// wire-format cases lock in the case-insensitive mapping.
		{"wire OVER_PROVISIONED", cotypes.Finding("OVER_PROVISIONED"), providers.RecommendationSeverityMedium},
		{"wire UNDER_PROVISIONED", cotypes.Finding("UNDER_PROVISIONED"), providers.RecommendationSeverityHigh},
		{"wire OPTIMIZED", cotypes.Finding("OPTIMIZED"), providers.RecommendationSeverityLow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapCOFindingToSeverity(tt.finding)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormCOFinding(t *testing.T) {
	// The skip-guard in getEC2Recommendations relies on the wire-format
	// "OPTIMIZED" normalizing to the same value as the SDK constant
	// cotypes.FindingOptimized ("Optimized"). Without this, already-optimized
	// instances leak through as bogus rightsize recommendations where the
	// recommended instance type equals the current one.
	tests := []struct {
		name     string
		finding  cotypes.Finding
		optimize bool
	}{
		{"wire OPTIMIZED is skipped", cotypes.Finding("OPTIMIZED"), true},
		{"sdk Optimized is skipped", cotypes.FindingOptimized, true},
		{"wire OVER_PROVISIONED is kept", cotypes.Finding("OVER_PROVISIONED"), false},
		{"wire UNDER_PROVISIONED is kept", cotypes.Finding("UNDER_PROVISIONED"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipped := normCOFinding(tt.finding) == normCOFinding(cotypes.FindingOptimized)
			assert.Equal(t, tt.optimize, skipped)
		})
	}
}

func TestExtractRegionFromARN(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		expected string
	}{
		{
			"EC2 instance ARN",
			"arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0",
			"us-east-1",
		},
		{
			"Lambda function ARN",
			"arn:aws:lambda:eu-west-1:123456789012:function:my-function",
			"eu-west-1",
		},
		{
			"EBS volume ARN",
			"arn:aws:ec2:ap-southeast-1:123456789012:volume/vol-049df61146c4d7901",
			"ap-southeast-1",
		},
		{
			"ECS service ARN",
			"arn:aws:ecs:us-west-2:123456789012:service/my-cluster/my-service",
			"us-west-2",
		},
		{
			"invalid ARN (too few parts)",
			"arn:aws",
			"",
		},
		{
			"empty string",
			"",
			"",
		},
		{
			"ARN with empty region",
			"arn:aws:iam::123456789012:root",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRegionFromARN(tt.arn)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeOptimizerIntegration(t *testing.T) {
	if os.Getenv("AWS_PROFILE") == "" && os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("skipping integration test: set AWS_PROFILE=prod or AWS credentials to run")
	}

	service := &awsComputeOptimizer{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: testAWSAccountNumber}

	recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, nil)
	require.NoError(t, err)

	t.Logf("fetched %d Compute Optimizer recommendations", len(recommendations))

	ec2Count, lambdaCount, ebsCount, ecsCount := 0, 0, 0, 0
	for i, rec := range recommendations {
		assert.Equal(t, "aws", rec.Data["source"])
		assert.Equal(t, ServiceNameComputeOptimizer, rec.ResourceServiceName)
		assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)

		switch rec.RuleName {
		case "aws_native_rightsize":
			ec2Count++
		case "aws_native_co_lambda_rightsize":
			lambdaCount++
		case "aws_native_co_ebs_rightsize":
			ebsCount++
		case "aws_native_co_ecs_rightsize":
			ecsCount++
		default:
			t.Errorf("unexpected rule name: %s", rec.RuleName)
		}

		t.Logf("  [%d] rule=%s severity=%s savings=%.2f region=%s resource=%s",
			i, rec.RuleName, rec.Severity, rec.Savings, rec.ResourceRegion, rec.ResourceType)
	}

	t.Logf("EC2: %d, Lambda: %d, EBS: %d, ECS: %d", ec2Count, lambdaCount, ebsCount, ecsCount)
}
