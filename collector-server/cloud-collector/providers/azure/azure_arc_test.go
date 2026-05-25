package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
	"time"
)

func TestArcService_Name(t *testing.T) {
	service := &arcService{}
	expected := "microsoft.hybridcompute/machines"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestArcService_GetRecommendations(t *testing.T) {
	service := &arcService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "disconnected Arc machine",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.HybridCompute/machines/machine1",
					Name:        "machine1",
					Type:        "Microsoft.HybridCompute/machines",
					Region:      "eastus",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"agentVersion": "1.25.0",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_arc_machine_disconnected": 1,
			},
		},
		{
			name: "outdated Arc agent",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.HybridCompute/machines/machine3",
					Name:        "machine3",
					Type:        "Microsoft.HybridCompute/machines",
					Region:      "centralus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"agentVersion": "1.0.5",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_arc_outdated_agent": 1,
			},
		},
		{
			name: "stale status update",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.HybridCompute/machines/machine4",
					Name:        "machine4",
					Type:        "Microsoft.HybridCompute/machines",
					Region:      "eastus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"agentVersion":     "1.25.0",
						"lastStatusChange": time.Now().Add(-48 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_arc_machine_stale_status": 1,
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

func TestArcService_ApplyCommand(t *testing.T) {
	service := &arcService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.HybridCompute/machines/machine1",
		Command:    "azure_arc_machine_disconnected",
	}

	resp, err := service.ApplyCommand(ctx, account, command)
	if err == nil {
		t.Error("ApplyCommand() expected error for unsupported operation")
	}
	if resp.Success {
		t.Error("ApplyCommand() expected Success = false")
	}
}
