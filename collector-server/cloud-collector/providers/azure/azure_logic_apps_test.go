package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"
)

func TestLogicAppsService_Name(t *testing.T) {
	service := &logicAppsService{}
	expected := "microsoft.logic/workflows"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestLogicAppsService_GetRecommendations(t *testing.T) {
	service := &logicAppsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "disabled workflow",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/workflow1",
					Name:        "workflow1",
					Type:        "Microsoft.Logic/workflows",
					Region:      "eastus",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_logic_app_workflow_disabled": 1,
			},
		},
		{
			name: "workflow without triggers",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/workflow2",
					Name:        "workflow2",
					Type:        "Microsoft.Logic/workflows",
					Region:      "westus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"triggers": map[string]interface{}{},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_logic_app_no_triggers": 1,
			},
		},
		{
			name: "workflow without actions",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/workflow3",
					Name:        "workflow3",
					Type:        "Microsoft.Logic/workflows",
					Region:      "centralus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"actions": map[string]interface{}{},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_logic_app_no_actions": 1,
			},
		},
		{
			name: "outdated workflow",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/workflow4",
					Name:        "workflow4",
					Type:        "Microsoft.Logic/workflows",
					Region:      "eastus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"changedTime": time.Now().Add(-400 * 24 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_logic_app_outdated_workflow": 1,
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

func TestLogicAppsService_ApplyCommand(t *testing.T) {
	service := &logicAppsService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Logic/workflows/workflow1",
		Command:    "azure_logic_app_workflow_disabled",
	}

	resp, err := service.ApplyCommand(ctx, account, command)
	if err == nil {
		t.Error("ApplyCommand() expected error for unsupported operation")
	}
	if resp.Success {
		t.Error("ApplyCommand() expected Success = false")
	}
}
