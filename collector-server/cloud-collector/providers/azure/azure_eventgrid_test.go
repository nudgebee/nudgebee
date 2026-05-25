package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
)

func TestEventGridService_Name(t *testing.T) {
	service := &eventGridService{}
	expected := "microsoft.eventgrid/topics"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestEventGridService_GetRecommendations(t *testing.T) {
	service := &eventGridService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "topic with public access and no IP filter",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/topics/topic1",
					Name:        "topic1",
					Type:        "Microsoft.EventGrid/topics",
					Region:      "eastus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"publicNetworkAccess": "Enabled",
						"inboundIpRules":      []interface{}{},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_eventgrid_topic_public_access_no_ip_filter": 1,
			},
		},
		{
			name: "topic without managed identity",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/topics/topic2",
					Name:        "topic2",
					Type:        "Microsoft.EventGrid/topics",
					Region:      "westus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_eventgrid_topic_no_managed_identity": 1,
			},
		},
		{
			name: "domain with public access enabled",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/domains/domain1",
					Name:        "domain1",
					Type:        "Microsoft.EventGrid/domains",
					Region:      "centralus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"publicNetworkAccess": "Enabled",
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_eventgrid_domain_public_access_enabled": 1,
			},
		},
		{
			name: "domain with local auth enabled",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/domains/domain2",
					Name:        "domain2",
					Type:        "Microsoft.EventGrid/domains",
					Region:      "eastus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"disableLocalAuth": false,
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_eventgrid_domain_local_auth_enabled": 1,
			},
		},
		{
			name: "resource with failed provisioning",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/topics/topic3",
					Name:        "topic3",
					Type:        "Microsoft.EventGrid/topics",
					Region:      "westus",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_eventgrid_resource_failed_provisioning": 1,
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

func TestEventGridService_ApplyCommand(t *testing.T) {
	service := &eventGridService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	command := providers.ApplyCommandRequest{
		ResourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.EventGrid/topics/topic1",
		Command:    "azure_eventgrid_topic_public_access_no_ip_filter",
	}

	resp, err := service.ApplyCommand(ctx, account, command)
	if err == nil {
		t.Error("ApplyCommand() expected error for unsupported operation")
	}
	if resp.Success {
		t.Error("ApplyCommand() expected Success = false")
	}
}
