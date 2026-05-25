package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
)

func TestPolicyService_Name(t *testing.T) {
	service := &policyService{}
	expected := "microsoft.authorization/policyassignments"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestPolicyService_GetRecommendations(t *testing.T) {
	service := &policyService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "policy assignment not enforced",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/providers/Microsoft.Authorization/policyAssignments/pa1",
					Name:        "pa1",
					Type:        "Microsoft.Authorization/policyAssignments",
					Region:      "global",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_policy_assignment_not_enforced": 1,
			},
		},
		{
			name: "policy assignment without description",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/providers/Microsoft.Authorization/policyAssignments/pa2",
					Name:        "pa2",
					Type:        "Microsoft.Authorization/policyAssignments",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"description": "",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_policy_assignment_no_description": 1,
			},
		},
		{
			name: "custom policy definition without metadata",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/providers/Microsoft.Authorization/policyDefinitions/pd1",
					Name:        "pd1",
					Type:        "Microsoft.Authorization/policyDefinitions",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"policyType": "Custom",
						"metadata":   map[string]interface{}{},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_policy_custom_definition_no_metadata": 1,
			},
		},
		{
			name: "policy definition without category",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/providers/Microsoft.Authorization/policyDefinitions/pd2",
					Name:        "pd2",
					Type:        "Microsoft.Authorization/policyDefinitions",
					Region:      "global",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"policyType": "Custom",
						"metadata": map[string]interface{}{
							"version": "1.0.0",
						},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_policy_definition_no_category": 1,
			},
		},
		{
			name:      "no policy assignments",
			resources: []providers.Resource{},
			wantRuleCount: map[string]int{
				"azure_policy_no_assignments": 1,
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

func TestPolicyService_ApplyCommand(t *testing.T) {
	service := &policyService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub1/providers/Microsoft.Authorization/policyAssignments/pa1",
		Command:    "azure_policy_assignment_not_enforced",
	}

	resp, err := service.ApplyCommand(ctx, account, command)
	if err == nil {
		t.Error("ApplyCommand() expected error for unsupported operation")
	}
	if resp.Success {
		t.Error("ApplyCommand() expected Success = false")
	}
}
