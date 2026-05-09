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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapCOFindingToSeverity(tt.finding)
			assert.Equal(t, tt.expected, result)
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
	account := providers.Account{AccountNumber: "740395098545"}

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
