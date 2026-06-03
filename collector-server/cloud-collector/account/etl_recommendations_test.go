package account

import (
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/stretchr/testify/assert"
)

func TestStoreRecommendationsAwsRDS(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonRDS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsEc2(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonEc2",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsLambda(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AWSLambda",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAWsECS(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonECS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsForAwsAllServices(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationAwsIam(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AWSIAM",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsS3(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonS3",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsSecurityHub(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "SecurityHub",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsCloudTrail(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "CloudTrail",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAzureAll(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29")
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsGCloudAll(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationGCloudStorage(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "storage",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}
