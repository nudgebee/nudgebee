//go:build integration
// +build integration

package azure

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// TestCreateAzureMetricAlert_Integration tests actual Azure metric alert creation
func TestCreateAzureMetricAlert_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	resourceGroup := os.Getenv("AZURE_RESOURCE_GROUP")
	resourceID := os.Getenv("AZURE_TEST_RESOURCE_ID")

	if subscriptionID == "" || resourceGroup == "" || resourceID == "" {
		t.Skip("Azure environment variables not set, skipping integration test")
	}

	// Create test context and account
	ctx := &testCloudProviderContext{
		ctx: context.Background(),
	}

	account := providers.Account{
		ID:            "test-account",
		CloudProvider: "azure",
	}

	alarmName := fmt.Sprintf("test-alarm-integration-%d", time.Now().Unix())

	config := providers.AlarmCreationConfig{
		AlarmName:          alarmName,
		MetricName:         "Percentage CPU",
		Namespace:          "Microsoft.Compute/virtualMachines",
		Period:             300,
		EvaluationPeriods:  3,
		Threshold:          80,
		ComparisonOperator: "GreaterThanThreshold",
		Statistic:          "Average",
	}

	// Create the metric alert
	err := CreateAzureMetricAlert(ctx, account, config, resourceID, "eastus", "Medium")
	if err != nil {
		t.Fatalf("CreateAzureMetricAlert() error = %v", err)
	}

	// Clean up - delete the alert
	defer func() {
		if err := deleteAzureMetricAlert(context.Background(), subscriptionID, resourceGroup, alarmName); err != nil {
			t.Logf("Failed to delete metric alert: %v", err)
		}
	}()

	// Verify the alert exists
	exists, err := verifyAzureAlertExists(context.Background(), subscriptionID, resourceGroup, alarmName)
	if err != nil {
		t.Fatalf("Failed to verify alert: %v", err)
	}
	if !exists {
		t.Error("Metric alert was not created")
	}
}

// TestCreateAzureMetricAlertFromRecommendation_Integration tests alarm creation from recommendation data
func TestCreateAzureMetricAlertFromRecommendation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	resourceGroup := os.Getenv("AZURE_RESOURCE_GROUP")
	resourceID := os.Getenv("AZURE_TEST_RESOURCE_ID")

	if subscriptionID == "" || resourceGroup == "" || resourceID == "" {
		t.Skip("Azure environment variables not set, skipping integration test")
	}

	ctx := &testCloudProviderContext{
		ctx: context.Background(),
	}

	account := providers.Account{
		ID:            "test-account",
		CloudProvider: "azure",
	}

	alarmName := fmt.Sprintf("test-alarm-from-rec-%d", time.Now().Unix())

	recommendation := providers.Recommendation{
		ResourceId:     resourceID,
		ResourceRegion: "eastus",
		Data: map[string]interface{}{
			"alarm_config": map[string]interface{}{
				"alarm_name":          alarmName,
				"metric_name":         "Percentage CPU",
				"namespace":           "Microsoft.Compute/virtualMachines",
				"period":              300,
				"evaluation_periods":  3,
				"threshold":           75.0,
				"comparison_operator": "GreaterThanThreshold",
				"statistic":           "Average",
			},
			"severity": "High",
		},
	}

	// Create alarm from recommendation
	err := CreateAzureMetricAlertFromRecommendation(ctx, account, recommendation)
	if err != nil {
		t.Fatalf("CreateAzureMetricAlertFromRecommendation() error = %v", err)
	}

	// Clean up
	defer func() {
		if err := deleteAzureMetricAlert(context.Background(), subscriptionID, resourceGroup, alarmName); err != nil {
			t.Logf("Failed to delete metric alert: %v", err)
		}
	}()

	// Verify the alert exists
	exists, err := verifyAzureAlertExists(context.Background(), subscriptionID, resourceGroup, alarmName)
	if err != nil {
		t.Fatalf("Failed to verify alert: %v", err)
	}
	if !exists {
		t.Error("Metric alert was not created from recommendation")
	}
}

// Helper function to verify if an alert exists
func verifyAzureAlertExists(ctx context.Context, subscriptionID, resourceGroup, alertName string) (bool, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return false, err
	}

	client, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, nil)
	if err != nil {
		return false, err
	}

	_, err = client.Get(ctx, resourceGroup, alertName, nil)
	if err != nil {
		return false, nil
	}

	return true, nil
}

// Helper function to delete a metric alert
func deleteAzureMetricAlert(ctx context.Context, subscriptionID, resourceGroup, alertName string) error {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return err
	}

	client, err := armmonitor.NewMetricAlertsClient(subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	_, err = client.Delete(ctx, resourceGroup, alertName, nil)
	return err
}

// Test context implementation
type testCloudProviderContext struct {
	ctx context.Context
}

func (t *testCloudProviderContext) GetContext() context.Context {
	return t.ctx
}

func (t *testCloudProviderContext) GetLogger() *slog.Logger {
	return slog.Default()
}

func (t *testCloudProviderContext) GetSecurityContext() *security.SecurityContext {
	// Return nil for tests - integration tests don't need security context
	return nil
}
