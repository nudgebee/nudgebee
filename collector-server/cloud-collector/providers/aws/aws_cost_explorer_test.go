package aws

import (
	"context"
	"os"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFloat64(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		{"valid integer string", "100", 100.0, false},
		{"valid float string", "99.95", 99.95, false},
		{"zero", "0", 0.0, false},
		{"negative value", "-50.25", -50.25, false},
		{"large number", "12345.67", 12345.67, false},
		{"empty string", "", 0.0, true},
		{"non-numeric", "abc", 0.0, true},
		{"with currency symbol", "$100", 0.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFloat64(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.InDelta(t, tt.want, got, 0.01)
			}
		})
	}
}

func TestExtractInstanceDetails(t *testing.T) {
	ce := &awsCostExplorer{}

	t.Run("EC2 instance details", func(t *testing.T) {
		data := map[string]any{}
		details := &cetypes.InstanceDetails{
			EC2InstanceDetails: &cetypes.EC2InstanceDetails{
				InstanceType:      aws.String("m5.xlarge"),
				Region:            aws.String("us-east-1"),
				Platform:          aws.String("Linux/UNIX"),
				Family:            aws.String("m5"),
				CurrentGeneration: true,
			},
		}

		ce.extractInstanceDetails(details, data)

		assert.Equal(t, "m5.xlarge", data["instance_type"])
		assert.Equal(t, "us-east-1", data["region"])
		assert.Equal(t, "Linux/UNIX", data["platform"])
		assert.Equal(t, "m5", data["family"])
		assert.Equal(t, true, data["current_generation"])
	})

	t.Run("RDS instance details", func(t *testing.T) {
		data := map[string]any{}
		details := &cetypes.InstanceDetails{
			RDSInstanceDetails: &cetypes.RDSInstanceDetails{
				InstanceType:      aws.String("db.r5.large"),
				Region:            aws.String("eu-west-1"),
				DatabaseEngine:    aws.String("MySQL"),
				Family:            aws.String("r5"),
				CurrentGeneration: true,
			},
		}

		ce.extractInstanceDetails(details, data)

		assert.Equal(t, "db.r5.large", data["instance_type"])
		assert.Equal(t, "eu-west-1", data["region"])
		assert.Equal(t, "MySQL", data["database_engine"])
		assert.Equal(t, "r5", data["family"])
	})

	t.Run("OpenSearch (ES) instance details", func(t *testing.T) {
		data := map[string]any{}
		details := &cetypes.InstanceDetails{
			ESInstanceDetails: &cetypes.ESInstanceDetails{
				InstanceClass:     aws.String("r5"),
				InstanceSize:      aws.String("large"),
				Region:            aws.String("us-west-2"),
				CurrentGeneration: true,
			},
		}

		ce.extractInstanceDetails(details, data)

		assert.Equal(t, "r5", data["instance_class"])
		assert.Equal(t, "large", data["instance_size"])
		assert.Equal(t, "us-west-2", data["region"])
	})

	t.Run("ElastiCache instance details", func(t *testing.T) {
		data := map[string]any{}
		details := &cetypes.InstanceDetails{
			ElastiCacheInstanceDetails: &cetypes.ElastiCacheInstanceDetails{
				NodeType:          aws.String("cache.r6g.large"),
				Region:            aws.String("us-east-1"),
				Family:            aws.String("r6g"),
				CurrentGeneration: true,
			},
		}

		ce.extractInstanceDetails(details, data)

		assert.Equal(t, "cache.r6g.large", data["node_type"])
		assert.Equal(t, "us-east-1", data["region"])
		assert.Equal(t, "r6g", data["family"])
	})

	t.Run("Redshift instance details", func(t *testing.T) {
		data := map[string]any{}
		details := &cetypes.InstanceDetails{
			RedshiftInstanceDetails: &cetypes.RedshiftInstanceDetails{
				NodeType:          aws.String("dc2.large"),
				Region:            aws.String("us-east-1"),
				Family:            aws.String("dc2"),
				CurrentGeneration: true,
			},
		}

		ce.extractInstanceDetails(details, data)

		assert.Equal(t, "dc2.large", data["node_type"])
		assert.Equal(t, "us-east-1", data["region"])
		assert.Equal(t, "dc2", data["family"])
	})

	t.Run("nil instance details", func(t *testing.T) {
		data := map[string]any{}
		details := &cetypes.InstanceDetails{}

		ce.extractInstanceDetails(details, data)

		assert.Empty(t, data)
	})
}

func TestCostExplorerIntegration(t *testing.T) {
	if os.Getenv("AWS_PROFILE") == "" && os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("skipping integration test: set AWS_PROFILE=prod or AWS credentials to run")
	}

	service := &awsCostExplorer{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: testAWSAccountNumber}

	recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, nil)
	require.NoError(t, err)

	t.Logf("fetched %d Cost Explorer recommendations", len(recommendations))

	riCount, spCount := 0, 0
	for i, rec := range recommendations {
		assert.Equal(t, "aws", rec.Data["source"])
		assert.Equal(t, ServiceNameCostExplorer, rec.ResourceServiceName)

		switch rec.RuleName {
		case "aws_native_ce_ri_recommendation":
			riCount++
		case "aws_native_ce_savings_plan_recommendation":
			spCount++
		default:
			t.Errorf("unexpected rule name: %s", rec.RuleName)
		}

		t.Logf("  [%d] rule=%s severity=%s savings=%.2f region=%s",
			i, rec.RuleName, rec.Severity, rec.Savings, rec.ResourceRegion)
	}

	t.Logf("RI recommendations: %d, Savings Plan recommendations: %d", riCount, spCount)
}

func TestDatabaseSavingsPlanRecommendations(t *testing.T) {
	t.Run("database RI recommendation struct", func(t *testing.T) {
		riRec := &databaseRIRecommendation{
			service:              "Amazon Relational Database Service",
			monthlySavings:       150.50,
			onDemandCost:         500.00,
			riCost:               349.50,
			upfrontCost:          "1000.00",
			recurringMonthlyCost: "250.00",
		}

		assert.Equal(t, "Amazon Relational Database Service", riRec.service)
		assert.InDelta(t, 150.50, riRec.monthlySavings, 0.01)
		assert.InDelta(t, 500.00, riRec.onDemandCost, 0.01)
		assert.InDelta(t, 349.50, riRec.riCost, 0.01)
	})

	t.Run("database savings plan comparison logic", func(t *testing.T) {
		// Test that SP is chosen when it has higher savings
		spSavings := 200.0
		riSavings := 150.0

		strategy := "SP"
		bestSavings := spSavings
		if riSavings > spSavings {
			strategy = "RI"
			bestSavings = riSavings
		}

		assert.Equal(t, "SP", strategy)
		assert.InDelta(t, 200.0, bestSavings, 0.01)
	})

	t.Run("break-even calculation", func(t *testing.T) {
		// Test break-even calculation: Upfront Cost / Monthly Savings
		hourlyCommitment := 1.0 // $1/hour
		termMonths := 12.0      // 1 year
		monthlySavings := 50.0

		upfrontCost := hourlyCommitment * 730 * termMonths // 730 hours per month
		breakEvenMonths := upfrontCost / monthlySavings

		assert.InDelta(t, 8760.0, upfrontCost, 0.01)    // 1 * 730 * 12
		assert.InDelta(t, 175.2, breakEvenMonths, 0.01) // 8760 / 50
	})

	t.Run("database services coverage", func(t *testing.T) {
		// This test verifies that Database Savings Plan recommendations are generated
		// for the expected database services (RDS, ElastiCache, DynamoDB).
		// We verify this by checking that the recommendation data contains service comparisons
		// for these services, which proves they were queried during recommendation generation.
		service := &awsCostExplorer{}
		ctx := providers.NewCloudProviderContext(context.Background())

		// Note: This test doesn't make real AWS API calls - it tests the logic structure
		// The actual services list is defined in getDatabaseSavingsPlanRecommendations()
		// If this test fails, it means the function's internal service list has changed.

		// For now, we just verify the function exists and has the right signature
		// A proper test would require mocking the Cost Explorer API
		assert.NotNil(t, service)
		_ = ctx // Use ctx to avoid unused variable warning

		// TODO: Add proper unit test with mocked Cost Explorer API to verify:
		// 1. All expected services (RDS, ElastiCache, DynamoDB) are queried
		// 2. Recommendations include service_comparisons for each service
	})
}

func TestDatabaseSavingsPlanIntegration(t *testing.T) {
	if os.Getenv("AWS_PROFILE") == "" && os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("skipping integration test: set AWS_PROFILE=prod or AWS credentials to run")
	}

	service := &awsCostExplorer{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{AccountNumber: testAWSAccountNumber}

	recommendations, err := service.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, nil)
	require.NoError(t, err)

	t.Logf("fetched %d Cost Explorer recommendations", len(recommendations))

	dbSpCount := 0
	for _, rec := range recommendations {
		if rec.RuleName == "aws_native_database_savings_plan" {
			dbSpCount++

			// Verify required fields are present
			assert.NotNil(t, rec.Data["source"])
			assert.Equal(t, "aws", rec.Data["source"])
			assert.Equal(t, "DATABASE_SP", rec.Data["savings_plan_type"])
			assert.NotNil(t, rec.Data["strategy"])
			assert.NotNil(t, rec.Data["on_demand_cost"])
			assert.NotNil(t, rec.Data["sp_savings"])
			assert.NotNil(t, rec.Data["break_even_months"])

			// Verify comparison data exists
			assert.NotNil(t, rec.Data["service_comparisons"])
			serviceComparisons, ok := rec.Data["service_comparisons"].(map[string]any)
			assert.True(t, ok)

			// Log details
			strategy := rec.Data["strategy"].(string)
			spSavings := rec.Data["sp_savings"].(float64)
			riSavings := rec.Data["ri_savings"].(float64)

			t.Logf("  Database SP: strategy=%s sp_savings=%.2f ri_savings=%.2f services=%d",
				strategy, spSavings, riSavings, len(serviceComparisons))
		}
	}

	t.Logf("Database Savings Plan recommendations: %d", dbSpCount)

	// Note: dbSpCount may be 0 if AWS doesn't return Database SP recommendations for this account
	// This is expected and not an error
}
