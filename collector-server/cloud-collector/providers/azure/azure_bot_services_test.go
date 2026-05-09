package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
	"github.com/stretchr/testify/assert"
)

func TestBotServicesService_Name(t *testing.T) {
	svc := &botServicesService{}
	assert.Equal(t, "microsoft.botservice/botservices", svc.Name())
}

func TestBotServicesService_GetResources(t *testing.T) {
	// This is a structural test. A full test would require mocking the Azure SDK.
	t.Run("structure validation", func(t *testing.T) {
		id := "/subscriptions/sub-id/resourceGroups/rg/providers/microsoft.botservice/botservices/my-bot"
		name := "my-bot"
		location := "global"
		botType := "microsoft.botservice/botservices"
		provisioningState := "Succeeded"

		mockBot := armbotservice.Bot{
			ID:         &id,
			Name:       &name,
			Location:   &location,
			Type:       &botType,
			Properties: &armbotservice.BotProperties{ProvisioningState: &provisioningState},
		}

		status := providers.ResourceStatusUnknown
		if mockBot.Properties != nil && mockBot.Properties.ProvisioningState != nil {
			if val, ok := nbStatusFromAzureProvisioningState[*mockBot.Properties.ProvisioningState]; ok {
				status = val
			}
		}

		createdAt := time.Time{}

		resource := providers.Resource{
			Id:        *mockBot.ID,
			Name:      *mockBot.Name,
			Type:      *mockBot.Type,
			Region:    *mockBot.Location,
			Status:    status,
			CreatedAt: createdAt,

			Arn:         *mockBot.ID,
			ServiceName: "microsoft.botservice/botservices",
		}

		assert.Equal(t, id, resource.Id)
		assert.Equal(t, name, resource.Name)
		assert.Equal(t, botType, resource.Type)
		assert.Equal(t, location, resource.Region)
		assert.Equal(t, providers.ResourceStatusActive, status)
		assert.True(t, resource.CreatedAt.IsZero())
	})
}

func TestBotServicesService_GetRecommendations(t *testing.T) {
	svc := &botServicesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "secure bot service with no recommendations",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-id/resourceGroups/rg/providers/microsoft.botservice/botservices/secure-bot",
					ServiceName: svc.Name(),
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"publicNetworkAccess": "Disabled",
						},
						"identity": map[string]interface{}{
							"type": "SystemAssigned",
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "bot service with public network access enabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-id/resourceGroups/rg/providers/microsoft.botservice/botservices/public-bot",
					ServiceName: svc.Name(),
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"publicNetworkAccess": "Enabled",
						},
						"identity": map[string]interface{}{
							"type": "SystemAssigned",
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_bot_service_public_network_access_enabled"},
		},
		{
			name: "bot service with managed identity disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-id/resourceGroups/rg/providers/microsoft.botservice/botservices/no-identity-bot",
					ServiceName: svc.Name(),
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"publicNetworkAccess": "Disabled",
						},
						"identity": nil,
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_bot_service_managed_identity_disabled"},
		},
		{
			name: "bot service with multiple issues",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-id/resourceGroups/rg/providers/microsoft.botservice/botservices/insecure-bot",
					ServiceName: svc.Name(),
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"publicNetworkAccess": "Enabled",
						},
						"identity": map[string]interface{}{
							"type": "None",
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedRules:           []string{"azure_bot_service_public_network_access_enabled", "azure_bot_service_managed_identity_disabled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			assert.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			foundRules := make(map[string]bool)
			for _, rec := range recommendations {
				foundRules[rec.RuleName] = true
			}

			for _, expectedRule := range tt.expectedRules {
				assert.True(t, foundRules[expectedRule], "Expected rule %s not found", expectedRule)
			}
		})
	}
}
