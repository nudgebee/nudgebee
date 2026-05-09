package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
)

func TestDevOpsService_Name(t *testing.T) {
	service := &devopsService{}
	expected := "microsoft.devops/projects"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestDevOpsService_GetRecommendations(t *testing.T) {
	service := &devopsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "project without description",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/projects/project1",
					Name:        "project1",
					Type:        "Microsoft.DevOps/projects",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"description": "",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_devops_project_no_description": 1,
			},
		},
		{
			name: "public project",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/projects/project2",
					Name:        "project2",
					Type:        "Microsoft.DevOps/projects",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"visibility": "public",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_devops_project_public_visibility": 1,
			},
		},
		{
			name: "disabled repository",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/projects/project1/repos/repo1",
					Name:        "repo1",
					Type:        "Microsoft.DevOps/repositories",
					Region:      "global",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_devops_repository_disabled": 1,
			},
		},
		{
			name: "large repository",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/projects/project1/repos/repo2",
					Name:        "repo2",
					Type:        "Microsoft.DevOps/repositories",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"size": int64(2000000000), // 2GB
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_devops_repository_large_size": 1,
			},
		},
		{
			name: "project without pipelines",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/projects/project1",
					Name:        "project1",
					Type:        "Microsoft.DevOps/projects",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"description": "Test project",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_devops_no_pipelines": 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations, err := service.GetRecommendations(ctx, account, filter, tt.resources)
			if err != nil {
				t.Errorf("GetRecommendations() error = %v", err)
				return
			}

			ruleCount := make(map[string]int)
			for _, rec := range recommendations {
				ruleCount[rec.RuleName]++
			}

			for rule, expectedCount := range tt.wantRuleCount {
				if gotCount := ruleCount[rule]; gotCount != expectedCount {
					t.Errorf("GetRecommendations() rule %s count = %v, want %v", rule, gotCount, expectedCount)
				}
			}
		})
	}
}

func TestDevOpsService_ApplyCommand(t *testing.T) {
	service := &devopsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "https://dev.azure.com/org1/projects/project1",
		Command:    "azure_devops_project_no_description",
	}

	resp, err := service.ApplyCommand(ctx, account, command)
	if err == nil {
		t.Error("ApplyCommand() expected error for unsupported operation")
	}
	if resp.Success {
		t.Error("ApplyCommand() expected Success = false")
	}
}

func TestDevOpsService_GetResources_NoConfig(t *testing.T) {
	service := &devopsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	resources, err := service.GetResources(ctx, account, "global")
	if err != nil {
		t.Errorf("GetResources() error = %v, expected no error when DevOps not configured", err)
		return
	}

	if len(resources) != 0 {
		t.Errorf("GetResources() returned %d resources, expected 0 when DevOps not configured", len(resources))
	}
}
