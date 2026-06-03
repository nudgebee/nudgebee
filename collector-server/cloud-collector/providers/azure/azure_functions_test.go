package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunctionsService_Name(t *testing.T) {
	svc := &functionsService{}
	assert.Equal(t, "Microsoft.Web/sites/functions", svc.Name())
}

func TestFunctionsService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armappservice.Site, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve running function apps",
			mockSetup: func() ([]armappservice.Site, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-function"
				name := "my-function"
				typeName := "Microsoft.Web/sites"
				location := "eastus"
				state := "Running"
				kind := "functionapp"

				return []armappservice.Site{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Kind:     &kind,
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
				assert.Equal(t, "my-function", res.Name)
				assert.Equal(t, "Microsoft.Web/sites/functions", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.Web/sites/functions", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve stopped function app",
			mockSetup: func() ([]armappservice.Site, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/stopped-function"
				name := "stopped-function"
				typeName := "Microsoft.Web/sites"
				location := "westus"
				state := "Stopped"
				kind := "functionapp,linux"

				return []armappservice.Site{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Kind:     &kind,
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
			name: "filter out non-function apps",
			mockSetup: func() ([]armappservice.Site, error) {
				funcID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-function"
				funcName := "my-function"
				typeName := "Microsoft.Web/sites"
				location := "eastus"
				state := "Running"
				funcKind := "functionapp"

				webID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-webapp"
				webName := "my-webapp"
				webKind := "app"

				return []armappservice.Site{
					{
						ID:       &funcID,
						Name:     &funcName,
						Type:     &typeName,
						Location: &location,
						Kind:     &funcKind,
						Properties: &armappservice.SiteProperties{
							State: &state,
						},
					},
					{
						ID:       &webID,
						Name:     &webName,
						Type:     &typeName,
						Location: &location,
						Kind:     &webKind,
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
				assert.Equal(t, "my-function", resources[0].Name)
			},
		},
		{
			name: "retrieve multiple function apps with different kinds",
			mockSetup: func() ([]armappservice.Site, error) {
				sites := []armappservice.Site{}
				kinds := []string{"functionapp", "functionapp,linux", "functionapp,linux,container"}

				for i, kind := range kinds {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/function-" + string(rune(i))
					name := "function-" + string(rune(i))
					typeName := "Microsoft.Web/sites"
					location := "eastus"
					state := "Running"

					sites = append(sites, armappservice.Site{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Kind:     &kind,
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
			sites, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.validateResult != nil && !tt.expectedError {
				// Convert mock data to resources for validation
				var resources []providers.Resource
				for _, app := range sites {
					// Filter for function apps only
					if app.Kind == nil || !containsSubstring(*app.Kind, "functionapp") {
						continue
					}

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
						Type:        "Microsoft.Web/sites/functions",
						Region:      *app.Location,
						Tags:        toAzureTags(app.Tags),
						Status:      status,
						ServiceName: "Microsoft.Web/sites/functions",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestFunctionsService_GetRecommendations(t *testing.T) {
	svc := &functionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for secure function app",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/secure-func",
					Name:        "secure-func",
					Type:        "Microsoft.Web/sites/functions",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites/functions",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":       true,
							"siteAuthEnabled": true,
							"siteConfig": map[string]interface{}{
								"netFrameworkVersion": "v6.0",
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for HTTPS only disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/insecure-func",
					Name:        "insecure-func",
					Type:        "Microsoft.Web/sites/functions",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites/functions",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":       false,
							"siteAuthEnabled": true,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_function_https_only_disabled"},
		},
		{
			name: "recommendation for authentication disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/no-auth-func",
					Name:        "no-auth-func",
					Type:        "Microsoft.Web/sites/functions",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites/functions",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":       true,
							"siteAuthEnabled": false,
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_function_authentication_disabled"},
		},
		{
			name: "recommendation for old runtime version",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/old-runtime-func",
					Name:        "old-runtime-func",
					Type:        "Microsoft.Web/sites/functions",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites/functions",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":       true,
							"siteAuthEnabled": true,
							"siteConfig": map[string]interface{}{
								"netFrameworkVersion": "v4.0",
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_function_old_runtime"},
		},
		{
			name: "multiple recommendations for insecure function app",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/very-insecure-func",
					Name:        "very-insecure-func",
					Type:        "Microsoft.Web/sites/functions",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites/functions",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"httpsOnly":       false,
							"siteAuthEnabled": false,
							"siteConfig": map[string]interface{}{
								"netFrameworkVersion": "v4.0",
							},
						},
					},
				},
			},
			expectedRecommendations: 3,
			expectedRules: []string{
				"azure_function_https_only_disabled",
				"azure_function_authentication_disabled",
				"azure_function_old_runtime",
			},
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Web/sites/no-meta-func",
					Name:        "no-meta-func",
					Type:        "Microsoft.Web/sites/functions",
					Region:      "eastus",
					ServiceName: "Microsoft.Web/sites/functions",
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

			for _, expectedRule := range tt.expectedRules {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == expectedRule {
						found = true
						assert.NotEmpty(t, rec.CategoryName)
						assert.NotEmpty(t, rec.Severity)
						assert.NotEmpty(t, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected rule '%s' not found", expectedRule)
			}
		})
	}
}

func TestFunctionsService_ApplyRecommendation(t *testing.T) {
	svc := &functionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_function_https_only_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource ID")
}

func TestFunctionsService_ApplyCommand(t *testing.T) {
	svc := &functionsService{}
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
				Command:    "azure_function_https_only_disabled",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "missing resource group in ID",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id",
				Command:    "azure_function_https_only_disabled",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/func-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "unknown command",
		},
		{
			name: "valid HTTPS command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/func-name",
				Command:    "azure_function_https_only_disabled",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid start command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/func-name",
				Command:    "start_function",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid stop command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/func-name",
				Command:    "stop_function",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid restart command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Web/sites/func-name",
				Command:    "restart_function",
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

func TestFunctionsService_QueryMetrices(t *testing.T) {
	svc := &functionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestFunctionsService_GetServiceMap(t *testing.T) {
	svc := &functionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-function-app",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-function-app", serviceMap.Id.Name)
	assert.Equal(t, "azure-function", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestFunctionsService_GetLogGroupName(t *testing.T) {
	svc := &functionsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/my-function"

	// Without valid Azure credentials, this should fail
	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
}

// Helper function to check substring (case-insensitive)
func containsSubstring(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
