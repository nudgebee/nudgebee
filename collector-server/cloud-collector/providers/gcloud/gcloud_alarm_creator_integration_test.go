//go:build integration
// +build integration

package gcloud

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

// alarmCreatorIntegrationCtx is a minimal CloudProviderContext for the
// integration tests in this file. The pre-existing tests were written before
// the CloudProviderContext interface settled, so this adapter brings them up
// to date without changing their behaviour.
type alarmCreatorIntegrationCtx struct{ ctx context.Context }

func (c *alarmCreatorIntegrationCtx) GetContext() context.Context { return c.ctx }
func (c *alarmCreatorIntegrationCtx) GetLogger() *slog.Logger     { return slog.Default() }
func (c *alarmCreatorIntegrationCtx) GetSecurityContext() *security.SecurityContext {
	return nil
}

// TestCreateGCPAlertPolicy_Integration tests actual GCP alert policy creation
func TestCreateGCPAlertPolicy_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	ctx := context.Background()
	account := providers.Account{
		ID:            "test-account",
		CloudProvider: "gcp",
	}

	alarmName := fmt.Sprintf("test-alarm-integration-%d", time.Now().Unix())

	config := providers.AlarmCreationConfig{
		AlarmName:          alarmName,
		MetricName:         "compute.googleapis.com/instance/cpu/utilization",
		Period:             60,
		EvaluationPeriods:  5,
		Threshold:          80,
		ComparisonOperator: "GreaterThanThreshold",
		Statistic:          "Average",
	}

	// Create the alert policy
	err := CreateGCPAlertPolicy(&alarmCreatorIntegrationCtx{ctx: ctx}, account, config, projectID)
	if err != nil {
		t.Fatalf("CreateGCPAlertPolicy() error = %v", err)
	}

	// Clean up - delete the alert policy
	defer func() {
		if err := deleteGCPAlertPolicy(ctx, projectID, alarmName); err != nil {
			t.Logf("Failed to delete alert policy: %v", err)
		}
	}()

	// Verify the alert policy exists
	exists, err := verifyGCPAlertPolicyExists(ctx, projectID, alarmName)
	if err != nil {
		t.Fatalf("Failed to verify alert policy: %v", err)
	}
	if !exists {
		t.Error("Alert policy was not created")
	}
}

// TestCreateGCPAlertPolicyFromRecommendation_Integration tests alarm creation from recommendation data
func TestCreateGCPAlertPolicyFromRecommendation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		t.Skip("GCP_PROJECT_ID not set, skipping integration test")
	}

	ctx := context.Background()
	account := providers.Account{
		ID:            "test-account",
		CloudProvider: "gcp",
	}

	alarmName := fmt.Sprintf("test-alarm-from-rec-%d", time.Now().Unix())

	recommendation := providers.Recommendation{
		ResourceId:     fmt.Sprintf("projects/%s/zones/us-central1-a/instances/test-instance", projectID),
		ResourceRegion: "us-central1",
		Data: map[string]interface{}{
			"alarm_config": map[string]interface{}{
				"alarm_name":          alarmName,
				"metric_name":         "compute.googleapis.com/instance/cpu/utilization",
				"period":              60,
				"evaluation_periods":  5,
				"threshold":           75.0,
				"comparison_operator": "GreaterThanThreshold",
				"statistic":           "Average",
			},
		},
	}

	// Create alarm from recommendation
	err := CreateGCPAlertPolicyFromRecommendation(&alarmCreatorIntegrationCtx{ctx: ctx}, account, recommendation)
	if err != nil {
		t.Fatalf("CreateGCPAlertPolicyFromRecommendation() error = %v", err)
	}

	// Clean up
	defer func() {
		if err := deleteGCPAlertPolicy(ctx, projectID, alarmName); err != nil {
			t.Logf("Failed to delete alert policy: %v", err)
		}
	}()

	// Verify the alert policy exists
	exists, err := verifyGCPAlertPolicyExists(ctx, projectID, alarmName)
	if err != nil {
		t.Fatalf("Failed to verify alert policy: %v", err)
	}
	if !exists {
		t.Error("Alert policy was not created from recommendation")
	}
}

// Helper function to verify if an alert policy exists
func verifyGCPAlertPolicyExists(ctx context.Context, projectID, displayName string) (bool, error) {
	client, err := monitoring.NewAlertPolicyClient(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = client.Close()
	}()

	req := &monitoringpb.ListAlertPoliciesRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}

	it := client.ListAlertPolicies(ctx, req)
	for {
		policy, err := it.Next()
		if err != nil {
			break
		}
		if policy.DisplayName == displayName {
			return true, nil
		}
	}

	return false, nil
}

// Helper function to delete an alert policy
func deleteGCPAlertPolicy(ctx context.Context, projectID, displayName string) error {
	client, err := monitoring.NewAlertPolicyClient(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = client.Close()
	}()

	// First find the policy by display name
	req := &monitoringpb.ListAlertPoliciesRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}

	it := client.ListAlertPolicies(ctx, req)
	for {
		policy, err := it.Next()
		if err != nil {
			break
		}
		if policy.DisplayName == displayName {
			// Delete the policy
			delReq := &monitoringpb.DeleteAlertPolicyRequest{
				Name: policy.Name,
			}
			return client.DeleteAlertPolicy(ctx, delReq)
		}
	}

	return fmt.Errorf("alert policy %s not found", displayName)
}
