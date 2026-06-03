package aws

import (
	"context"
	"fmt"
	"nudgebee/collector/cloud"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetXrayRecommendations(t *testing.T) {
	cloud.LoadEnvFromFile(t)

	xraySvc, exists := GetAwsService("XRay")
	assert.True(t, exists)
	require.NotNil(t, xraySvc)

	pCtx := providers.NewCloudProviderContext(context.Background())

	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	require.NotEmpty(t, accessKey, "AWS_ACCESS_KEY_ID environment variable must be set for this integration test")
	require.NotEmpty(t, secretKey, "AWS_SECRET_ACCESS_KEY environment variable must be set for this integration test")

	testAWSAccount := os.Getenv("TEST_AWS_ACCOUNT_NUMBER")
	require.NotEmpty(t, testAWSAccount, "TEST_AWS_ACCOUNT_NUMBER environment variable must be set for this integration test")

	encryptedSecret, err := common.Encrypt(secretKey)
	require.NoError(t, err, "Failed to encrypt secret key for test")

	account := providers.Account{
		AccountNumber: testAWSAccount,
		AccessKey:     aws.String(accessKey),
		AccessSecret:  aws.String(encryptedSecret),
	}

	// Note: X-Ray resources are regional.
	existingResources, err := xraySvc.GetResources(pCtx, account, "us-east-1")
	assert.Nil(t, err)
	assert.NotNil(t, existingResources)
	require.NoError(t, err, "GetResources should not fail")

	recommendations, err := xraySvc.GetRecommendations(pCtx, account, providers.ListRecommendationsRequest{}, existingResources)

	assert.Nil(t, err)
	assert.NotNil(t, recommendations)
	fmt.Printf("Found %d X-Ray recommendations.\n", len(recommendations))
}
