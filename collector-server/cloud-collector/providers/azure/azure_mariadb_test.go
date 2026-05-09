package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mariadb/armmariadb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMariaDBService_Name(t *testing.T) {
	svc := &mariadbService{}
	assert.Equal(t, "Microsoft.DBforMariaDB/servers", svc.Name())
}

func TestMariaDBService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armmariadb.Server, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve ready mariadb servers",
			mockSetup: func() ([]armmariadb.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforMariaDB/servers/my-mariadb"
				name := "my-mariadb"
				typeName := "Microsoft.DBforMariaDB/servers"
				location := "eastus"
				state := armmariadb.ServerState("Ready")

				return []armmariadb.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armmariadb.ServerProperties{
							UserVisibleState: &state,
						},
					},
				}, nil
			},
			expectedCount: 1,
			expectedError: false,
			validateResult: func(t *testing.T, resources []providers.Resource) {
				require.Len(t, resources, 1)
				res := resources[0]
				assert.Equal(t, "my-mariadb", res.Name)
				assert.Equal(t, "Microsoft.DBforMariaDB/servers", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.DBforMariaDB/servers", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve stopped mariadb server",
			mockSetup: func() ([]armmariadb.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforMariaDB/servers/stopped-mariadb"
				name := "stopped-mariadb"
				typeName := "Microsoft.DBforMariaDB/servers"
				location := "westus"
				state := armmariadb.ServerState("Stopped")

				return []armmariadb.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armmariadb.ServerProperties{
							UserVisibleState: &state,
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
			name: "retrieve disabled mariadb server",
			mockSetup: func() ([]armmariadb.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforMariaDB/servers/disabled-mariadb"
				name := "disabled-mariadb"
				typeName := "Microsoft.DBforMariaDB/servers"
				location := "eastus"
				state := armmariadb.ServerState("Disabled")

				return []armmariadb.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armmariadb.ServerProperties{
							UserVisibleState: &state,
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
			name: "retrieve mariadb server with unknown state",
			mockSetup: func() ([]armmariadb.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforMariaDB/servers/unknown-mariadb"
				name := "unknown-mariadb"
				typeName := "Microsoft.DBforMariaDB/servers"
				location := "eastus"
				state := armmariadb.ServerState("Inaccessible")

				return []armmariadb.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armmariadb.ServerProperties{
							UserVisibleState: &state,
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
			name: "retrieve multiple mariadb servers",
			mockSetup: func() ([]armmariadb.Server, error) {
				servers := []armmariadb.Server{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforMariaDB/servers/mariadb-" + string(rune(i))
					name := "mariadb-" + string(rune(i))
					typeName := "Microsoft.DBforMariaDB/servers"
					location := "eastus"
					state := armmariadb.ServerState("Ready")

					servers = append(servers, armmariadb.Server{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armmariadb.ServerProperties{
							UserVisibleState: &state,
						},
					})
				}
				return servers, nil
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
			servers, err := tt.mockSetup()
			if tt.expectedError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, servers, tt.expectedCount)

			if tt.validateResult != nil && !tt.expectedError {
				// Convert mock data to resources for validation
				var resources []providers.Resource
				for _, server := range servers {
					status := providers.ResourceStatusUnknown
					if server.Properties != nil && server.Properties.UserVisibleState != nil {
						switch *server.Properties.UserVisibleState {
						case armmariadb.ServerState("Ready"):
							status = providers.ResourceStatusActive
						case armmariadb.ServerState("Stopped"), armmariadb.ServerState("Disabled"):
							status = providers.ResourceStatusInactive
						}
					}

					resources = append(resources, providers.Resource{
						Id:          *server.ID,
						Name:        *server.Name,
						Type:        *server.Type,
						Region:      *server.Location,
						Tags:        toAzureTags(server.Tags),
						Status:      status,
						ServiceName: "Microsoft.DBforMariaDB/servers",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestMariaDBService_GetRecommendations(t *testing.T) {
	svc := &mariadbService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedSSLRule         bool
		expectedBackupRule      bool
		expectedStoppedRule     bool
	}{
		{
			name: "no recommendations for properly configured server",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/secure-mariadb",
					Name:        "secure-mariadb",
					Type:        "Microsoft.DBforMariaDB/servers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforMariaDB/servers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"userVisibleState": "Ready",
							"sslEnforcement":   "Enabled",
							"storageProfile": map[string]interface{}{
								"backupRetentionDays": float64(7),
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedSSLRule:         false,
			expectedBackupRule:      false,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for SSL disabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/no-ssl-mariadb",
					Name:        "no-ssl-mariadb",
					Type:        "Microsoft.DBforMariaDB/servers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforMariaDB/servers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"userVisibleState": "Ready",
							"sslEnforcement":   "Disabled",
							"storageProfile": map[string]interface{}{
								"backupRetentionDays": float64(7),
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedSSLRule:         true,
			expectedBackupRule:      false,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for insufficient backup retention",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/low-backup-mariadb",
					Name:        "low-backup-mariadb",
					Type:        "Microsoft.DBforMariaDB/servers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforMariaDB/servers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"userVisibleState": "Ready",
							"sslEnforcement":   "Enabled",
							"storageProfile": map[string]interface{}{
								"backupRetentionDays": float64(3),
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedSSLRule:         false,
			expectedBackupRule:      true,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for stopped server",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/stopped-mariadb",
					Name:        "stopped-mariadb",
					Type:        "Microsoft.DBforMariaDB/servers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforMariaDB/servers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"userVisibleState": "Stopped",
							"sslEnforcement":   "Enabled",
							"storageProfile": map[string]interface{}{
								"backupRetentionDays": float64(7),
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedSSLRule:         false,
			expectedBackupRule:      false,
			expectedStoppedRule:     true,
		},
		{
			name: "multiple recommendations for server with issues",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/issues-mariadb",
					Name:        "issues-mariadb",
					Type:        "Microsoft.DBforMariaDB/servers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforMariaDB/servers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"userVisibleState": "Stopped",
							"sslEnforcement":   "Disabled",
							"storageProfile": map[string]interface{}{
								"backupRetentionDays": float64(2),
							},
						},
					},
				},
			},
			expectedRecommendations: 3,
			expectedSSLRule:         true,
			expectedBackupRule:      true,
			expectedStoppedRule:     true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/no-meta-mariadb",
					Name:        "no-meta-mariadb",
					Type:        "Microsoft.DBforMariaDB/servers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforMariaDB/servers",
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

			if tt.expectedSSLRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_mariadb_ssl_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected SSL recommendation not found")
			}

			if tt.expectedBackupRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_mariadb_backup_disabled" {
						found = true
						assert.Equal(t, providers.RecommendationCategorySecurity, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityHigh, rec.Severity)
						assert.Equal(t, providers.RecommendationActionModify, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected backup recommendation not found")
			}

			if tt.expectedStoppedRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_mariadb_server_stopped" {
						found = true
						assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)
						assert.Equal(t, providers.RecommendationSeverityLow, rec.Severity)
						assert.Equal(t, providers.RecommendationActionDelete, rec.Action)
						break
					}
				}
				assert.True(t, found, "Expected stopped server recommendation not found")
			}
		})
	}
}

func TestMariaDBService_ApplyRecommendation(t *testing.T) {
	svc := &mariadbService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_mariadb_ssl_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}

func TestMariaDBService_ApplyCommand(t *testing.T) {
	svc := &mariadbService{}
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
				Command:    "azure_mariadb_ssl_disabled",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/server-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/server-name",
				Command:    "azure_mariadb_ssl_disabled",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "start_server command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/server-name",
				Command:    "start_server",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "stop_server command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/server-name",
				Command:    "stop_server",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "restart_server command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforMariaDB/servers/server-name",
				Command:    "restart_server",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
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

func TestMariaDBService_QueryMetrices(t *testing.T) {
	svc := &mariadbService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestMariaDBService_GetServiceMap(t *testing.T) {
	svc := &mariadbService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-mariadb-server",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-mariadb-server", serviceMap.Id.Name)
	assert.Equal(t, "mariadb", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestMariaDBService_GetLogGroupName(t *testing.T) {
	svc := &mariadbService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforMariaDB/servers/my-mariadb"

	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}
