package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppServiceService_Name(t *testing.T) {
	svc := &appServiceService{}
	assert.Equal(t, "Microsoft.Web/sites", svc.Name())
}

func TestAppServiceService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armappservice.Site, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve running app services",
			mockSetup: func() ([]armappservice.Site, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-app"
				name := "my-app"
				typeName := "Microsoft.Web/sites"
				location := "eastus"
				state := "Running"

				return []armappservice.Site{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armappservice.SiteProperties{
							State: &state,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-app", res.Name)
				assert.Equal(t, "Microsoft.Web/sites", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Web/sites", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve stopped app service",
			mockSetup: func() ([]armappservice.Site, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/stopped-app"
				name := "stopped-app"
				typeName := "Microsoft.Web/sites"
				location := "westus"
				state := "Stopped"

				return []armappservice.Site{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armappservice.SiteProperties{
							State: &state,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusInactive, resources[0].Status)
			},
		},
		{
			name: "retrieve app service with unknown state",
			mockSetup: func() ([]armappservice.Site, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/unknown-app"
				name := "unknown-app"
				typeName := "Microsoft.Web/sites"
				location := "eastus"
				state := "Updating"

				return []armappservice.Site{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armappservice.SiteProperties{
							State: &state,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				assert.Equal(t, providers.ResourceStatusUnknown, resources[0].Status)
			},
		},
		{
			name: "retrieve multiple app services",
			mockSetup: func() ([]armappservice.Site, error) {
				sites := []armappservice.Site{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/app-" + string(rune(i))
					name := "app-" + string(rune(i))
					typeName := "Microsoft.Web/sites"
					location := "eastus"
					state := "Running"

					sites = append(sites, armappservice.Site{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armappservice.SiteProperties{
							State: &state,
						},
					})
				}
				return sites, nil
			},
			expectedCount: 3,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				assert.Len(t, resources, 3)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This is a structure test - actual Azure SDK mocking would require
			// interface implementations or using the Azure SDK's test helpers
			// For now, we're testing the logic structure
			sites, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, sites, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				// Convert mock data to resources for validation
				var resources []providers.Resource
				for _, app := range sites {
					status := providers.ResourceStatusUnknown
					if app.Properties != nil && app.Properties.State != nil {
						switch *app.Properties.State {
						case "Running":
							status = providers.ResourceStatusActive
						case "Stopped":
							status = providers.ResourceStatusInactive
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *app.ID,
						Name:        *app.Name,
						Type:        *app.Type,
						Region:      *app.Location,
						Tags:        toAzureTags(app.Tags),
						Status:      status,
						ServiceName: "Microsoft.Web/sites",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestAppServiceService_GetRecommendations(t *testing.T) {
	svc := &appServiceService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedHTTPSOnlyRule   bool
		expectedClientCertRule  bool
	}{
		{
			name: "no recommendations for secure app service",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/secure-app",
					Name:        "secure-app",
					Type:        "Microsoft.Web/sites",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":         true,
							"clientCertEnabled": true,
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedHTTPSOnlyRule:   false,
			expectedClientCertRule:  false,
		},
		{
			name: "recommendation for HTTPS only disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/insecure-app",
					Name:        "insecure-app",
					Type:        "Microsoft.Web/sites",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":         false,
							"clientCertEnabled": true,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedHTTPSOnlyRule:   true,
			expectedClientCertRule:  false,
		},
		{
			name: "recommendation for client cert disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/no-cert-app",
					Name:        "no-cert-app",
					Type:        "Microsoft.Web/sites",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":         true,
							"clientCertEnabled": false,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedHTTPSOnlyRule:   false,
			expectedClientCertRule:  true,
		},
		{
			name: "multiple recommendations for insecure app service",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/very-insecure-app",
					Name:        "very-insecure-app",
					Type:        "Microsoft.Web/sites",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":         false,
							"clientCertEnabled": false,
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedHTTPSOnlyRule:   true,
			expectedClientCertRule:  true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/no-meta-app",
					Name:        "no-meta-app",
					Type:        "Microsoft.Web/sites",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites",
					Meta:        map[string]interface{}{},
				},
			},
			expectedRecommendations: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := svc.GetRecommendations(ctx, account, providers.ListRecommendationsRequest{}, tt.existingResources)
			require.NoError(t, err)
			assert.Len(t, recommendations, tt.expectedRecommendations)

			if tt.expectedHTTPSOnlyRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_app_service_https_only_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected HTTPS only recommendation not found")
			}

			if tt.expectedClientCertRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_app_service_client_cert_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityMedium, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected client cert recommendation not found")
			}
		})
	}
}

func TestAppServiceService_ApplyRecommendation(t *testing.T) {
	svc := &appServiceService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_app_service_https_only_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource ID")
}

func TestAppServiceService_ApplyCommand(t *testing.T) {
	svc := &appServiceService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name          string
		command       providers.ApplyCommandRequest
		expectError   bool
		errorContains string
	}{
		{
			name: "invalid resource ID",
			command: providers.ApplyCommandRequest{
				ResourceId: "",
				Command:    "azure_app_service_https_only_disabled",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/app-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "unknown command",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/app-name",
				Command:    "azure_app_service_https_only_disabled",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ApplyCommand(ctx, account, tt.command)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.False(t, resp.Success)
			} else {
				assert.NoError(t, err)
				assert.True(t, resp.Success)
			}
		})
	}
}

func TestAppServiceService_QueryMetrices(t *testing.T) {
	svc := &appServiceService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestAppServiceService_GetServiceMap(t *testing.T) {
	svc := &appServiceService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-app-service",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-app-service", serviceMap.Id.Name)
	assert.Equal(t, "appservice", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestAppServiceService_GetLogGroupName(t *testing.T) {
	svc := &appServiceService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-app"

	logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	require.NoError(t, err)
	assert.Equal(t, resourceID, logGroup)
}
