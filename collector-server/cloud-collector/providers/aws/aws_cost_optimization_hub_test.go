package aws

import (
	"context"
	"os"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	cohtypes "github.com/aws/aws-sdk-go-v2/service/costoptimizationhub/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapCOHActionToRuleName(t *testing.T) {
	tests := []struct {
		name       string
		actionType string
		expected   string
	}{
		{"PurchaseSavingsPlans", "PurchaseSavingsPlans", "aws_native_purchase_savings_plans"},
		{"PurchaseReservedInstances", "PurchaseReservedInstances", "aws_native_purchase_reserved_instances"},
		{"Rightsize", "Rightsize", "aws_native_rightsize"},
		{"Stop", "Stop", "aws_native_stop"},
		{"Upgrade", "Upgrade", "aws_native_upgrade"},
		{"Delete", "Delete", "aws_native_delete"},
		{"MigrateToGraviton", "MigrateToGraviton", "aws_native_migrate_graviton"},
		{"ScaleIn (unmapped)", "ScaleIn", "aws_native_coh_ScaleIn"},
		{"empty string", "", "aws_native_coh_"},
		{"unknown", "SomeFutureAction", "aws_native_coh_SomeFutureAction"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapCOHActionToRuleName(tt.actionType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapCOHActionToRecommendationAction(t *testing.T) {
	tests := []struct {
		name       string
		actionType string
		expected   providers.RecommendationAction
	}{
		{"Delete", "Delete", providers.RecommendationActionDelete},
		{"Stop", "Stop", providers.RecommendationActionDelete},
		{"Rightsize", "Rightsize", providers.RecommendationActionModify},
		{"Upgrade", "Upgrade", providers.RecommendationActionModify},
		{"PurchaseSavingsPlans", "PurchaseSavingsPlans", providers.RecommendationActionModify},
		{"empty", "", providers.RecommendationActionModify},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapCOHActionToRecommendationAction(tt.actionType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapSavingsToSeverity(t *testing.T) {
	tests := []struct {
		name     string
		savings  *float64
		expected providers.RecommendationSeverity
	}{
		{"nil savings", nil, providers.RecommendationSeverityLow},
		{"zero savings", aws.Float64(0), providers.RecommendationSeverityLow},
		{"small savings ($5)", aws.Float64(5), providers.RecommendationSeverityLow},
		{"boundary low ($19.99)", aws.Float64(19.99), providers.RecommendationSeverityLow},
		{"boundary medium ($20)", aws.Float64(20), providers.RecommendationSeverityMedium},
		{"medium savings ($50)", aws.Float64(50), providers.RecommendationSeverityMedium},
		{"boundary medium ($99.99)", aws.Float64(99.99), providers.RecommendationSeverityMedium},
		{"boundary high ($100)", aws.Float64(100), providers.RecommendationSeverityHigh},
		{"high savings ($500)", aws.Float64(500), providers.RecommendationSeverityHigh},
		{"negative savings", aws.Float64(-10), providers.RecommendationSeverityLow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapSavingsToSeverity(tt.savings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapCostOptimizationHubRecommendation(t *testing.T) {
	account := providers.Account{AccountNumber: "123456789012"}

	t.Run("full recommendation with all fields", func(t *testing.T) {
		item := cohtypes.Recommendation{
			ActionType:                 aws.String("Rightsize"),
			RecommendationId:           aws.String("rec-123"),
			Region:                     aws.String("us-east-1"),
			CurrentResourceType:        aws.String("Ec2Instance"),
			RecommendedResourceType:    aws.String("Ec2Instance"),
			RecommendedResourceSummary: aws.String("t3.medium -> t3.small"),
			EstimatedMonthlySavings:    aws.Float64(150.0),
			EstimatedSavingsPercentage: aws.Float64(30.0),
			EstimatedMonthlyCost:       aws.Float64(350.0),
			CurrencyCode:               aws.String("USD"),
			AccountId:                  aws.String("123456789012"),
		}

		rec := mapCostOptimizationHubRecommendation(item, account)

		assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)
		assert.Equal(t, "aws_native_rightsize", rec.RuleName)
		assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
		assert.Equal(t, 150.0, rec.Savings)
		assert.Equal(t, providers.RecommendationActionModify, rec.Action)
		assert.Equal(t, "AmazonEC2", rec.ResourceServiceName)
		assert.Equal(t, "rec-123", rec.ResourceId)
		assert.Equal(t, "Ec2Instance", rec.ResourceType)
		assert.Equal(t, "us-east-1", rec.ResourceRegion)

		// Check data fields
		assert.Equal(t, "aws", rec.Data["source"])
		assert.Equal(t, "rec-123", rec.Data["recommendation_id"])
		assert.Equal(t, "Rightsize", rec.Data["action_type"])
		assert.Equal(t, 150.0, rec.Data["estimated_monthly_savings"])
		assert.Equal(t, 30.0, rec.Data["estimated_savings_percentage"])
		assert.Equal(t, "USD", rec.Data["currency_code"])
	})

	t.Run("recommendation with nil fields", func(t *testing.T) {
		item := cohtypes.Recommendation{
			ActionType: nil,
		}

		rec := mapCostOptimizationHubRecommendation(item, account)

		assert.Equal(t, "aws_native_coh_", rec.RuleName)
		assert.Equal(t, providers.RecommendationSeverityLow, rec.Severity)
		assert.Equal(t, 0.0, rec.Savings)
		assert.Equal(t, "global", rec.ResourceRegion)
		assert.Equal(t, "", rec.ResourceId)
		assert.Equal(t, "", rec.ResourceType)
		assert.Equal(t, "aws", rec.Data["source"])
	})

	t.Run("delete action maps correctly", func(t *testing.T) {
		item := cohtypes.Recommendation{
			ActionType:              aws.String("Delete"),
			RecommendationId:        aws.String("rec-del"),
			EstimatedMonthlySavings: aws.Float64(25.0),
		}

		rec := mapCostOptimizationHubRecommendation(item, account)

		assert.Equal(t, "aws_native_delete", rec.RuleName)
		assert.Equal(t, providers.RecommendationActionDelete, rec.Action)
		assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
	})
}

func TestCostOptimizationHubIntegration(t *testing.T) {
	if os.Getenv("AWS_PROFILE") == "" && os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("skipping integration test: set AWS_PROFILE=prod or AWS credentials to run")
	}

	service := &awsCostOptimizationHub{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: "740395098545"}

	recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, nil)
	require.NoError(t, err)

	t.Logf("fetched %d Cost Optimization Hub recommendations", len(recommendations))

	for i, rec := range recommendations {
		assert.Equal(t, "aws", rec.Data["source"])
		assert.NotEmpty(t, rec.RuleName, "recommendation %d should have a rule name", i)
		assert.NotEmpty(t, rec.ResourceServiceName)
		assert.Equal(t, ServiceNameCostOptimizationHub, rec.ResourceServiceName)

		t.Logf("  [%d] rule=%s severity=%s savings=%.2f region=%s type=%s",
			i, rec.RuleName, rec.Severity, rec.Savings, rec.ResourceRegion, rec.ResourceType)
	}
}
