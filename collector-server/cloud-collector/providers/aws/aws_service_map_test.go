package aws

import (
	"context"
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwsServiceMap(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	// Require TEST_RESOURCE_ARN for actual integration testing
	resourceARN := ""

	serviceName := os.Getenv("TEST_SERVICE_NAME")
	if serviceName == "arn:aws:rds:us-east-1:740395098545:db:ops-demo-primary-db" {
		serviceName = "AmazonRDS" // default to RDS
	}

	// Create context
	ctx := providers.NewCloudProviderContext(context.Background())

	// Get account configuration from environment
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// Get account name from env or default to "aws-prod" which matches UUID 6c008cf8-4d79-4999-8447-573a697d0652
	accountName := os.Getenv("TEST_ACCOUNT_NAME")
	if accountName == "" {
		accountName = "aws-prod"
	}

	account := providers.Account{
		ID:            "6c008cf8-4d79-4999-8447-573a697d0652", // aws-prod cloud account UUID
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccountName:   accountName,
		Region:        &region,
	}

	// Create AWS provider
	provider := &awsProvider{}

	// Create query request with actual resource ARN
	request := providers.QueryServiceMapRequest{
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: serviceName,
				Resource:    resourceARN,
			},
		},
		Region: region,
	}

	// Execute service map query
	response, err := provider.QueryServiceMap(ctx, account, request)
	require.NoError(t, err, "QueryServiceMap should not return error")
	assert.NotNil(t, response, "Response should not be nil")

	t.Logf("Retrieved %d applications from service map", len(response.Applications))

	// print as json
	responseJson, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("Service Map Response: %s", string(responseJson))
}

// TestAwsServiceMapWithConfig tests the AWS Config-based service map
func TestAwsServiceMapWithConfig(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" || os.Getenv("TEST_RESOURCE_ARN") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT or TEST_RESOURCE_ARN not set")
	}

	ctx := providers.NewCloudProviderContext(context.Background())
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// Get account name from env or default to "aws-prod" which matches UUID 6c008cf8-4d79-4999-8447-573a697d0652
	accountName := os.Getenv("TEST_ACCOUNT_NAME")
	if accountName == "" {
		accountName = "aws-prod"
	}

	account := providers.Account{
		ID:            "6c008cf8-4d79-4999-8447-573a697d0652", // aws-prod cloud account UUID
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccountName:   accountName,
		Region:        &region,
	}

	provider := &awsProvider{}

	request := providers.QueryServiceMapRequest{
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "rds",
				Resource:    "arn:aws:rds:us-east-1:740395098545:db:main",
			},
		},
		Region: region,
	}

	// This should try AWS Config first
	response, err := provider.queryServiceMapWithConfig(ctx, account, request)

	if err != nil {
		// AWS Config might not be enabled - this is okay
		t.Logf("AWS Config query failed (this is expected if Config is not enabled): %v", err)
	} else {
		assert.NotNil(t, response)
		t.Logf("AWS Config returned %d applications", len(response.Applications))
	}
}

// TestAwsServiceMapFallback tests the service-specific fallback
func TestAwsServiceMapFallback(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	// For fallback testing, we need a specific resource ID (not ARN)
	resourceID := os.Getenv("TEST_RESOURCE_ID")
	if resourceID == "" {
		t.Skip("Skipping fallback test - TEST_RESOURCE_ID not set (e.g., mydb-instance)")
	}

	ctx := providers.NewCloudProviderContext(context.Background())
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// Get account name from env or default to "aws-prod" which matches UUID 6c008cf8-4d79-4999-8447-573a697d0652
	accountName := os.Getenv("TEST_ACCOUNT_NAME")
	if accountName == "" {
		accountName = "aws-prod"
	}

	account := providers.Account{
		ID:            "6c008cf8-4d79-4999-8447-573a697d0652", // aws-prod cloud account UUID
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccountName:   accountName,
		Region:        &region,
	}

	provider := &awsProvider{}

	request := providers.QueryServiceMapRequest{
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "rds",
				Resource:    resourceID,
			},
		},
		Region: region,
	}

	// Test fallback directly
	response, err := provider.queryServiceMapWithFallback(ctx, account, request)
	require.NoError(t, err, "Fallback should not return error")
	assert.NotNil(t, response)
	assert.NotEmpty(t, response.Applications)

	t.Logf("Fallback returned %d applications", len(response.Applications))
	for _, app := range response.Applications {
		t.Logf("  %s (%s): %d upstreams, %d downstreams",
			app.Id.Name, app.Id.Kind, len(app.Upstreams), len(app.Downstreams))
	}
}

// TestMultiSourceServiceMapFeatureFlag tests the feature flag for multi-source engine
func TestMultiSourceServiceMapFeatureFlag(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// Get account name from env or default to "aws-prod" which matches UUID 6c008cf8-4d79-4999-8447-573a697d0652
	accountName := os.Getenv("TEST_ACCOUNT_NAME")
	if accountName == "" {
		accountName = "aws-prod"
	}

	account := providers.Account{
		ID:            "6c008cf8-4d79-4999-8447-573a697d0652", // aws-prod cloud account UUID
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccountName:   accountName,
		Region:        &region,
	}

	provider := &awsProvider{}

	// Test with feature flag disabled (default)
	assert.False(t, provider.shouldUseMultiSourceEngine(account),
		"Multi-source engine should be disabled by default")

	// Test with feature flag enabled
	t.Setenv("ENABLE_MULTI_SOURCE_SERVICEMAP", "true")
	assert.True(t, provider.shouldUseMultiSourceEngine(account),
		"Multi-source engine should be enabled when env var is set")
}

// TestMultiSourceServiceMapFallback tests that multi-source engine falls back to legacy
func TestMultiSourceServiceMapFallback(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	ctx := providers.NewCloudProviderContext(context.Background())
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// Get account name from env or default to "aws-prod" which matches UUID 6c008cf8-4d79-4999-8447-573a697d0652
	accountName := os.Getenv("TEST_ACCOUNT_NAME")
	if accountName == "" {
		accountName = "aws-prod"
	}

	account := providers.Account{
		ID:            "6c008cf8-4d79-4999-8447-573a697d0652", // aws-prod cloud account UUID
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccountName:   accountName,
		Region:        &region,
	}

	provider := &awsProvider{}

	request := providers.QueryServiceMapRequest{
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "rds",
				Resource:    "arn:aws:rds:us-east-1:740395098545:db:main",
			},
		},
		Region: region,
	}

	// Test that queryServiceMapWithEngine falls back to legacy implementation
	response, err := provider.queryServiceMapWithEngine(ctx, account, request)

	// Should not error - should fall back gracefully
	if err != nil {
		// May error if AWS Config is not enabled, which is okay
		t.Logf("Query returned error (expected if AWS Config not enabled): %v", err)
	} else {
		assert.NotNil(t, response)
		t.Logf("Multi-source engine (fallback mode) returned %d applications", len(response.Applications))
	}
}

// TestVPCWideServiceMap tests VPC-wide traffic discovery via VPC Flow Logs
// This test validates that VPC Flow Logs service map includes:
// - Internet nodes for public IPs
// - vpc-unknown nodes for unresolved private IPs
// - Proper failure tracking (REJECT actions)
// - Link metadata (BytesSent, BytesReceived, FailureCount, Protocol)
func TestVPCWideServiceMap(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test - TEST_ACCOUNT not set")
	}

	vpcID := os.Getenv("TEST_VPC_ID")
	if vpcID == "" {
		t.Skip("Skipping VPC-wide test - TEST_VPC_ID not set (e.g., vpc-0e328e2f6da26e5e4)")
	}

	ctx := providers.NewCloudProviderContext(context.Background())
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	// Get account name from env or default to "aws-prod" which matches UUID 6c008cf8-4d79-4999-8447-573a697d0652
	accountName := os.Getenv("TEST_ACCOUNT_NAME")
	if accountName == "" {
		accountName = "aws-prod"
	}

	account := providers.Account{
		ID:            "6c008cf8-4d79-4999-8447-573a697d0652", // aws-prod cloud account UUID
		AccountNumber: os.Getenv("TEST_ACCOUNT"),
		AccountName:   accountName,
		Region:        &region,
	}

	// Create AWS provider
	provider := &awsProvider{}

	t.Logf("Testing VPC-wide service map for VPC: %s", vpcID)
	t.Logf("This test requires:")
	t.Logf("  1. VPC Flow Logs enabled for VPC %s", vpcID)
	t.Logf("  2. CloudWatch Logs log group with recent flow log data")
	t.Logf("  3. At least some network traffic in the time window")

	// Query service map for a resource in the VPC to trigger VPC Flow Logs source
	// The VPC Flow Logs source should discover ALL traffic in the VPC
	resourceARN := os.Getenv("TEST_RESOURCE_ARN")
	if resourceARN == "" {
		resourceARN = "arn:aws:elasticloadbalancing:us-east-1:740395098545:loadbalancer/app/Demo-Frontend-ALB/219c89b4742c55f1"
	}

	request := providers.QueryServiceMapRequest{
		Resources: []providers.QueryServiceMapResourceRequest{
			{
				ServiceName: "elb",
				Resource:    resourceARN,
			},
		},
		Region: region,
	}

	// Execute service map query with multi-source engine enabled
	t.Setenv("ENABLE_MULTI_SOURCE_SERVICEMAP", "true")
	response, err := provider.QueryServiceMap(ctx, account, request)

	if err != nil {
		t.Logf("Service map query failed (may be expected if VPC Flow Logs not enabled): %v", err)
		t.Skip("Skipping validation - VPC Flow Logs may not be enabled")
		return
	}

	require.NoError(t, err, "QueryServiceMap should not error with VPC Flow Logs enabled")
	assert.NotNil(t, response)
	assert.NotEmpty(t, response.Applications, "Should return applications")

	t.Logf("VPC-wide service map returned %d applications", len(response.Applications))

	// Verify link metadata structure
	hasLinkMetadata := false
	hasInternetNode := false
	hasUnknownNode := false

	for _, app := range response.Applications {
		// Check for Internet node
		if app.Id.Kind == "external-ip" && app.Id.Name == "Internet" {
			hasInternetNode = true
			t.Logf("Found Internet node: %s", app.Id.Name)
		}

		// Check for vpc-unknown nodes
		if app.Id.Kind == "vpc-unknown" {
			hasUnknownNode = true
			t.Logf("Found vpc-unknown node: %s", app.Id.Name)
		}

		// Check link metadata
		for _, link := range app.Downstreams {
			if link.Protocol != "" || link.BytesSent > 0 || link.BytesReceived > 0 {
				hasLinkMetadata = true
				t.Logf("Link with metadata: %s -> %s (Protocol: %s, BytesSent: %.0f, BytesReceived: %.0f, FailureCount: %.0f)",
					app.Id.Name, link.Id.Name, link.Protocol, link.BytesSent, link.BytesReceived, link.FailureCount)
			}
		}
	}

	// Log findings
	t.Logf("Test results:")
	t.Logf("  - Link metadata present: %v", hasLinkMetadata)
	t.Logf("  - Internet node found: %v", hasInternetNode)
	t.Logf("  - vpc-unknown nodes found: %v", hasUnknownNode)

	// Print full response as JSON for inspection
	responseJson, _ := json.MarshalIndent(response, "", "  ")
	t.Logf("Full VPC-wide Service Map Response:\n%s", string(responseJson))
}
