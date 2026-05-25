package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresService_Name(t *testing.T) {
	svc := &postgresService{}
	assert.Equal(t, "Microsoft.DBforPostgreSQL/flexibleServers", svc.Name())
}

func TestPostgresService_GetResources(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func() ([]armpostgresqlflexibleservers.Server, error)
		expectedCount  int
		expectedError  bool
		validateResult func(t *testing.T, resources []providers.Resource)
	}{
		{
			name: "successfully retrieve ready postgres servers",
			mockSetup: func() ([]armpostgresqlflexibleservers.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/my-postgres"
				name := "my-postgres"
				typeName := "Microsoft.DBforPostgreSQL/flexibleServers"
				location := "eastus"
				state := armpostgresqlflexibleservers.ServerStateReady

				return []armpostgresqlflexibleservers.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Tags: map[string]*string{
							"env": strPtr("production"),
						},
						Properties: &armpostgresqlflexibleservers.ServerProperties{
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
				assert.Equal(t, "my-postgres", res.Name)
				assert.Equal(t, "Microsoft.DBforPostgreSQL/flexibleServers", res.Type)
				assert.Equal(t, "eastus", res.Region)
				assert.Equal(t, providers.ResourceStatusActive, res.Status)
				assert.Equal(t, "Microsoft.DBforPostgreSQL/flexibleServers", res.ServiceName)
				assert.Contains(t, res.Tags, "env")
			},
		},
		{
			name: "retrieve stopped postgres server",
			mockSetup: func() ([]armpostgresqlflexibleservers.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/stopped-postgres"
				name := "stopped-postgres"
				typeName := "Microsoft.DBforPostgreSQL/flexibleServers"
				location := "westus"
				state := armpostgresqlflexibleservers.ServerStateStopped

				return []armpostgresqlflexibleservers.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armpostgresqlflexibleservers.ServerProperties{
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
			name: "retrieve disabled postgres server",
			mockSetup: func() ([]armpostgresqlflexibleservers.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/disabled-postgres"
				name := "disabled-postgres"
				typeName := "Microsoft.DBforPostgreSQL/flexibleServers"
				location := "eastus"
				state := armpostgresqlflexibleservers.ServerStateDisabled

				return []armpostgresqlflexibleservers.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armpostgresqlflexibleservers.ServerProperties{
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
			name: "retrieve postgres server with unknown state",
			mockSetup: func() ([]armpostgresqlflexibleservers.Server, error) {
				id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/unknown-postgres"
				name := "unknown-postgres"
				typeName := "Microsoft.DBforPostgreSQL/flexibleServers"
				location := "eastus"
				state := armpostgresqlflexibleservers.ServerStateUpdating

				return []armpostgresqlflexibleservers.Server{
					{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armpostgresqlflexibleservers.ServerProperties{
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
			name: "retrieve multiple postgres servers",
			mockSetup: func() ([]armpostgresqlflexibleservers.Server, error) {
				servers := []armpostgresqlflexibleservers.Server{}
				for i := 1; i <= 3; i++ {
					id := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/postgres-" + string(rune(i))
					name := "postgres-" + string(rune(i))
					typeName := "Microsoft.DBforPostgreSQL/flexibleServers"
					location := "eastus"
					state := armpostgresqlflexibleservers.ServerStateReady

					servers = append(servers, armpostgresqlflexibleservers.Server{
						ID:       &id,
						Name:     &name,
						Type:     &typeName,
						Location: &location,
						Properties: &armpostgresqlflexibleservers.ServerProperties{
							State: &state,
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
					if server.Properties != nil && server.Properties.State != nil {
						switch *server.Properties.State {
						case armpostgresqlflexibleservers.ServerStateReady:
							status = providers.ResourceStatusActive
						case armpostgresqlflexibleservers.ServerStateStopped, armpostgresqlflexibleservers.ServerStateDisabled:
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
						ServiceName: "Microsoft.DBforPostgreSQL/flexibleServers",
					})
				}
				tt.validateResult(t, resources)
			}
		})
	}
}

func TestPostgresService_GetRecommendations(t *testing.T) {
	svc := &postgresService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedBackupRule      bool
		expectedStoppedRule     bool
	}{
		{
			name: "no recommendations for properly configured server",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/secure-postgres",
					Name:        "secure-postgres",
					Type:        "Microsoft.DBforPostgreSQL/flexibleServers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforPostgreSQL/flexibleServers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"state": "Ready",
							"backup": map[string]interface{}{
								"backupRetentionDays": float64(7),
							},
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedBackupRule:      false,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for insufficient backup retention",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/low-backup-postgres",
					Name:        "low-backup-postgres",
					Type:        "Microsoft.DBforPostgreSQL/flexibleServers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforPostgreSQL/flexibleServers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"state": "Ready",
							"backup": map[string]interface{}{
								"backupRetentionDays": float64(3),
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedBackupRule:      true,
			expectedStoppedRule:     false,
		},
		{
			name: "recommendation for stopped server",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/stopped-postgres",
					Name:        "stopped-postgres",
					Type:        "Microsoft.DBforPostgreSQL/flexibleServers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforPostgreSQL/flexibleServers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"state": "Stopped",
							"backup": map[string]interface{}{
								"backupRetentionDays": float64(7),
							},
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedBackupRule:      false,
			expectedStoppedRule:     true,
		},
		{
			name: "multiple recommendations for server with issues",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/issues-postgres",
					Name:        "issues-postgres",
					Type:        "Microsoft.DBforPostgreSQL/flexibleServers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforPostgreSQL/flexibleServers",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"state": "Stopped",
							"backup": map[string]interface{}{
								"backupRetentionDays": float64(2),
							},
						},
					},
				},
			},
			expectedRecommendations: 2,
			expectedBackupRule:      true,
			expectedStoppedRule:     true,
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/no-meta-postgres",
					Name:        "no-meta-postgres",
					Type:        "Microsoft.DBforPostgreSQL/flexibleServers",
					Region:      "eastus",
					ServiceName: "Microsoft.DBforPostgreSQL/flexibleServers",
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

			if tt.expectedBackupRule {
				found := false
				for _, rec := range recommendations {
					if rec.RuleName == "azure_postgres_backup_disabled" {
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
					if rec.RuleName == "azure_postgres_server_stopped" {
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

func TestPostgresService_ApplyRecommendation(t *testing.T) {
	svc := &postgresService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_postgres_backup_disabled",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}

func TestPostgresService_ApplyCommand(t *testing.T) {
	svc := &postgresService{}
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
				Command:    "azure_postgres_backup_disabled",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/server-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "valid command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/server-name",
				Command:    "azure_postgres_backup_disabled",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "start_server command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/server-name",
				Command:    "start_server",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "stop_server command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/server-name",
				Command:    "stop_server",
			},
			expectError:   true,
			errorContains: "access secret is not provided",
		},
		{
			name: "restart_server command without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/server-name",
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

func TestPostgresService_QueryMetrices(t *testing.T) {
	svc := &postgresService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "StartDate and EndDate must be provided")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestPostgresService_GetServiceMap(t *testing.T) {
	svc := &postgresService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-postgres-server",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-postgres-server", serviceMap.Id.Name)
	assert.Equal(t, "postgres", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestPostgresService_GetLogGroupName(t *testing.T) {
	svc := &postgresService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/my-postgres"

	_, err := svc.GetLogGroupName(ctx, account, "eastus", resourceID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access secret is not provided")
}
