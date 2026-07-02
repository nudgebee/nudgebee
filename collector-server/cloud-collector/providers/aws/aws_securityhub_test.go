package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSecurityHubRecommendations(t *testing.T) {
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Skip("Skipping integration test that requires AWS credentials")
	}
	securityHub, exists := GetAwsService("SecurityHub")
	assert.True(t, exists)
	recommendations, err := securityHub.GetRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: testAWSAccountNumber,
	}, providers.ListRecommendationsRequest{}, []providers.Resource{})
	assert.Nil(t, err)
	assert.NotNil(t, recommendations)
}
