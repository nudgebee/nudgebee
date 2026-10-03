package account

import (
	"errors"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRecommendationsRecordsProviderFailure(t *testing.T) {
	providerErr := errors.New("provider permission denied")
	var recorded map[string]any
	deps := storeRecommendationsDeps{
		getDB: func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
			return &common.DatabaseManager{}, nil
		},
		fetch: func(*security.RequestContext, string, providers.ListRecommendationsRequest) (providers.ListRecommendationsResponse, providers.Account, error) {
			return providers.ListRecommendationsResponse{}, providers.Account{AccountNumber: "123"}, providerErr
		},
		update: func(_ *security.RequestContext, accountID string, status AgentStatus, message string, synced bool, connectionStatus map[string]any) error {
			assert.Equal(t, "acc-1", accountID)
			assert.Equal(t, AgentStatusConnected, status)
			assert.Equal(t, providerErr.Error(), message)
			assert.True(t, synced)
			recorded = connectionStatus
			return nil
		},
	}

	ctx := security.NewRequestContextForTenantAdmin("tenant-1")
	_, err := storeRecommendations(ctx, "acc-1", providers.ListRecommendationsRequest{ServiceName: "AmazonEC2"}, deps)
	require.ErrorIs(t, err, providerErr)
	recommendations := recorded["recommendations"].(map[string]any)
	assert.Equal(t, providerErr.Error(), recommendations["err"])
	assert.NotEmpty(t, recommendations["updated_at"])
}

func TestStoreRecommendationsAwsRDS(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonRDS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsEc2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonEc2",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsLambda(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AWSLambda",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAWsECS(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonECS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsForAwsAllServices(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationAwsIam(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AWSIAM",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsS3(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonS3",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsSecurityHub(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "SecurityHub",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsCloudTrail(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "CloudTrail",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAzureAll(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29")
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsGCloudAll(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationGCloudStorage(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "storage",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
