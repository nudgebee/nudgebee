package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilesService_Name(t *testing.T) {
	svc := &filesService{}
	assert.Equal(t, "Microsoft.Storage/storageAccounts/fileServices", svc.Name())
}

func TestFilesService_GetRecommendations(t *testing.T) {
	svc := &filesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name                    string
		existingResources       []providers.Resource
		expectedRecommendations int
		expectedRules           []string
	}{
		{
			name: "no recommendations for healthy file share",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/healthy",
					Name:        "healthy",
					Type:        "Microsoft.Storage/storageAccounts/fileServices",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts/fileServices",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"shareQuota":       float64(1024),
							"enabledProtocols": "SMB",
							"lastModifiedTime": time.Now().Format(time.RFC3339),
						},
					},
				},
			},
			expectedRecommendations: 0,
			expectedRules:           []string{},
		},
		{
			name: "recommendation for large quota",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/large-quota",
					Name:        "large-quota",
					Type:        "Microsoft.Storage/storageAccounts/fileServices",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts/fileServices",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"shareQuota":       float64(6144), // 6TB
							"enabledProtocols": "SMB",
							"lastModifiedTime": time.Now().Format(time.RFC3339),
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_files_large_quota"},
		},
		{
			name: "recommendation for SMB not enabled",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/no-smb",
					Name:        "no-smb",
					Type:        "Microsoft.Storage/storageAccounts/fileServices",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts/fileServices",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"shareQuota":       float64(1024),
							"enabledProtocols": "NFS",
							"lastModifiedTime": time.Now().Format(time.RFC3339),
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_files_smb_not_enabled"},
		},
		{
			name: "recommendation for unused file share",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/unused",
					Name:        "unused",
					Type:        "Microsoft.Storage/storageAccounts/fileServices",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts/fileServices",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"shareQuota":       float64(1024),
							"enabledProtocols": "SMB",
							"lastModifiedTime": time.Now().AddDate(0, -7, 0).Format(time.RFC3339), // 7 months ago
						},
					},
				},
			},
			expectedRecommendations: 1,
			expectedRules:           []string{"azure_files_unused_file_share"},
		},
		{
			name: "multiple recommendations for problematic file share",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/problematic",
					Name:        "problematic",
					Type:        "Microsoft.Storage/storageAccounts/fileServices",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts/fileServices",
					Meta: map[string]interface{}{
						"properties": map[string]interface{}{
							"shareQuota":       float64(10240), // 10TB
							"enabledProtocols": "",
							"lastModifiedTime": time.Now().AddDate(-1, 0, 0).Format(time.RFC3339), // 1 year ago
						},
					},
				},
			},
			expectedRecommendations: 3,
			expectedRules: []string{
				"azure_files_large_quota",
				"azure_files_smb_not_enabled",
				"azure_files_unused_file_share",
			},
		},
		{
			name: "no recommendations for resource without meta",
			existingResources: []providers.Resource{
				{
					Id:          "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/no-meta",
					Name:        "no-meta",
					Type:        "Microsoft.Storage/storageAccounts/fileServices",
					Region:      "eastus",
					ServiceName: "Microsoft.Storage/storageAccounts/fileServices",
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

func TestFilesService_ApplyRecommendation(t *testing.T) {
	svc := &filesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	// Test with invalid resource ID
	recommendation := providers.Recommendation{
		ResourceId: "",
		RuleName:   "azure_files_enable_smb_encryption",
	}

	err := svc.ApplyRecommendation(ctx, account, recommendation)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource ID")
}

func TestFilesService_ApplyCommand(t *testing.T) {
	svc := &filesService{}
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
				Command:    "azure_files_enable_smb_encryption",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "missing file share name in ID",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa",
				Command:    "azure_files_enable_smb_encryption",
			},
			expectError:   true,
			errorContains: "invalid resource ID",
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/share-name",
				Command:    "unknown_command",
			},
			expectError:   true,
			errorContains: "unknown command",
		},
		{
			name: "valid SMB encryption command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/share-name",
				Command:    "azure_files_enable_smb_encryption",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid set quota command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/share-name",
				Command:    "azure_files_set_quota",
			},
			expectError:   true,
			errorContains: "failed to create azure credential",
		},
		{
			name: "valid delete command structure without Azure connection",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/share-name",
				Command:    "delete_file_share",
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

func TestFilesService_QueryMetrices(t *testing.T) {
	svc := &filesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.QueryMetricsRequest{}

	resp, err := svc.QueryMetrices(ctx, account, filter)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
	assert.Equal(t, providers.QueryMetricsResponse{}, resp)
}

func TestFilesService_GetServiceMap(t *testing.T) {
	svc := &filesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	resource := providers.Resource{
		Name:   "my-file-share",
		Region: "eastus",
	}

	serviceMap, err := svc.GetServiceMap(ctx, account, resource)
	require.NoError(t, err)
	assert.Equal(t, "my-file-share", serviceMap.Id.Name)
	assert.Equal(t, "azure-files", serviceMap.Id.Kind)
	assert.Equal(t, "eastus", serviceMap.Id.Namespace)
	assert.Equal(t, "Unknown", serviceMap.Status)
	assert.Empty(t, serviceMap.Upstreams)
	assert.Empty(t, serviceMap.Downstreams)
}

func TestFilesService_GetLogGroupName(t *testing.T) {
	svc := &filesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name       string
		resourceID string
		expected   string
	}{
		{
			name:       "extract storage account ID from file share ID",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa/fileServices/default/shares/share-name",
			expected:   "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa",
		},
		{
			name:       "return resource ID if no fileServices found",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa",
			expected:   "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logGroup, err := svc.GetLogGroupName(ctx, account, "eastus", tt.resourceID)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, logGroup)
		})
	}
}
