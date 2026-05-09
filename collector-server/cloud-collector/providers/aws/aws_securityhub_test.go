package aws

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSecurityHubRecommendations(t *testing.T) {
	securityHub, exists := GetAwsService("SecurityHub")
	assert.True(t, exists)
	recommendations, err := securityHub.GetRecommendations(providers.NewCloudProviderContext(context.Background()), providers.Account{
		AccountNumber: "280501305789",
	}, providers.ListRecommendationsRequest{}, []providers.Resource{})
	assert.Nil(t, err)
	assert.NotNil(t, recommendations)
}
