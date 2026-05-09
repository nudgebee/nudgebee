package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
)

func TestPipelinesService_Name(t *testing.T) {
	service := &pipelinesService{}
	expected := "microsoft.devops/pipelines"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestPipelinesService_GetResources_NoConfig(t *testing.T) {
	service := &pipelinesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	resources, err := service.GetResources(ctx, account, "global")
	if err != nil {
		t.Errorf("GetResources() error = %v, expected no error when Pipelines not configured", err)
		return
	}

	if len(resources) != 0 {
		t.Errorf("GetResources() returned %d resources, expected 0 when Pipelines not configured", len(resources))
	}
}

func TestPipelinesService_GetRecommendations(t *testing.T) {
	service := &pipelinesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "failed build",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/project1/_build/results?buildId=123",
					Name:        "CI Build - Build 123",
					Type:        "Microsoft.DevOps/builds",
					Region:      "global",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"project":        "project1",
						"definitionName": "CI Build",
						"buildNumber":    "123",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_pipeline_build_failed": 1,
			},
		},
		{
			name: "build without branch info",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/project1/_build/results?buildId=124",
					Name:        "CI Build - Build 124",
					Type:        "Microsoft.DevOps/builds",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"project":        "project1",
						"definitionName": "CI Build",
						"buildNumber":    "124",
						"sourceBranch":   "",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_pipeline_build_no_branch": 1,
			},
		},
		{
			name: "high failure rate",
			resources: []providers.Resource{
				{
					Id:          "https://dev.azure.com/org1/project1/_build/results?buildId=1",
					Name:        "Build 1",
					Type:        "Microsoft.DevOps/builds",
					Region:      "global",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
				{
					Id:          "https://dev.azure.com/org1/project1/_build/results?buildId=2",
					Name:        "Build 2",
					Type:        "Microsoft.DevOps/builds",
					Region:      "global",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
				{
					Id:          "https://dev.azure.com/org1/project1/_build/results?buildId=3",
					Name:        "Build 3",
					Type:        "Microsoft.DevOps/builds",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_pipeline_build_failed":      2,
				"azure_pipeline_high_failure_rate": 1,
			},
		},
		{
			name:          "no resources",
			resources:     []providers.Resource{},
			wantRuleCount: map[string]int{},
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

func TestPipelinesService_ApplyCommand(t *testing.T) {
	service := &pipelinesService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "https://dev.azure.com/org1/_apis/pipelines/1",
		Command:    "azure_pipeline_build_failed",
	}

	resp, err := service.ApplyCommand(ctx, account, command)
	if err == nil {
		t.Error("ApplyCommand() expected error for unsupported operation")
	}
	if resp.Success {
		t.Error("ApplyCommand() expected Success = false")
	}
}
