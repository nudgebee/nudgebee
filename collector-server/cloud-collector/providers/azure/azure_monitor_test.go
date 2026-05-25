package azure

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"
)

func TestMonitorService_Name(t *testing.T) {
	service := &monitorService{}
	expected := "microsoft.insights"
	if got := service.Name(); got != expected {
		t.Errorf("Name() = %v, want %v", got, expected)
	}
}

func TestMonitorService_GetRecommendations(t *testing.T) {
	service := &monitorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}
	filter := providers.ListRecommendationsRequest{}

	tests := []struct {
		name          string
		resources     []providers.Resource
		wantRuleCount map[string]int
	}{
		{
			name: "disabled alert rule",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Insights/alertRules/alert1",
					Name:        "alert1",
					Type:        "Microsoft.Insights/alertRules",
					Region:      "eastus",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_monitor_alert_rule_disabled": 1,
			},
		},
		{
			name: "alert rule without action group",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Insights/metricAlerts/alert2",
					Name:        "alert2",
					Type:        "Microsoft.Insights/metricAlerts",
					Region:      "westus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"actions": []interface{}{},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_monitor_alert_no_action_group": 1,
			},
		},
		{
			name: "disabled action group",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Insights/actionGroups/ag1",
					Name:        "ag1",
					Type:        "Microsoft.Insights/actionGroups",
					Region:      "eastus",
					Status:      providers.ResourceStatusInactive,
					ServiceName: service.Name(),
					Meta:        map[string]any{},
				},
			},
			wantRuleCount: map[string]int{
				"azure_monitor_action_group_disabled": 1,
			},
		},
		{
			name: "action group without receivers",
			resources: []providers.Resource{
				{
					Id:          "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Insights/actionGroups/ag2",
					Name:        "ag2",
					Type:        "Microsoft.Insights/actionGroups",
					Region:      "westus",
					Status:      providers.ResourceStatusActive,
					ServiceName: service.Name(),
					Meta: map[string]any{
						"emailReceivers":   []interface{}{},
						"smsReceivers":     []interface{}{},
						"webhookReceivers": []interface{}{},
					},
				},
			},
			wantRuleCount: map[string]int{
				"azure_monitor_action_group_no_receivers": 1,
			},
		},
		{
			name:      "no action groups configured",
			resources: []providers.Resource{},
			wantRuleCount: map[string]int{
				"azure_monitor_no_action_groups": 1,
				"azure_monitor_no_alert_rules":   1,
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

func TestListAzureMonitorMetrics_VirtualMachines(t *testing.T) {
	resp, err := listAzureMonitorMetrics(providers.ListMetricsRequest{
		ServiceName: "microsoft.compute/virtualmachines",
	})
	if err != nil {
		t.Fatalf("listAzureMonitorMetrics() error = %v", err)
	}
	if len(resp.Metrics) == 0 {
		t.Error("expected non-empty metrics for virtual machines")
	}
	for _, m := range resp.Metrics {
		if m.Name == "" {
			t.Error("metric name should not be empty")
		}
		if m.Namespace != "microsoft.compute/virtualmachines" {
			t.Errorf("namespace = %v, want microsoft.compute/virtualmachines", m.Namespace)
		}
	}
}

func TestListAzureMonitorMetrics_SQL(t *testing.T) {
	resp, err := listAzureMonitorMetrics(providers.ListMetricsRequest{
		ServiceName: "microsoft.sql/servers",
	})
	if err != nil {
		t.Fatalf("listAzureMonitorMetrics() error = %v", err)
	}
	if len(resp.Metrics) == 0 {
		t.Error("expected non-empty metrics for SQL servers")
	}
}

func TestListAzureMonitorMetrics_UnknownService(t *testing.T) {
	resp, err := listAzureMonitorMetrics(providers.ListMetricsRequest{
		ServiceName: "nonexistent.service",
	})
	if err != nil {
		t.Fatalf("listAzureMonitorMetrics() error = %v", err)
	}
	if len(resp.Metrics) != 0 {
		t.Errorf("expected empty metrics for unknown service, got %d", len(resp.Metrics))
	}
}

func TestListAzureMonitorMetrics_CaseInsensitive(t *testing.T) {
	resp1, _ := listAzureMonitorMetrics(providers.ListMetricsRequest{ServiceName: "Microsoft.Compute/VirtualMachines"})
	resp2, _ := listAzureMonitorMetrics(providers.ListMetricsRequest{ServiceName: "microsoft.compute/virtualmachines"})
	if len(resp1.Metrics) != len(resp2.Metrics) {
		t.Errorf("case sensitivity issue: got %d vs %d metrics", len(resp1.Metrics), len(resp2.Metrics))
	}
}

func TestListAzureMonitorMetrics_HasStatistics(t *testing.T) {
	resp, _ := listAzureMonitorMetrics(providers.ListMetricsRequest{
		ServiceName: "microsoft.compute/virtualmachines",
	})
	hasStats := false
	for _, m := range resp.Metrics {
		if len(m.Statistics) > 0 {
			hasStats = true
			break
		}
	}
	if !hasStats {
		t.Error("expected at least some VM metrics to have statistics")
	}
}

func TestMonitorService_ApplyCommand(t *testing.T) {
	service := &monitorService{}
	ctx := providers.NewCloudProviderContext(context.Background())
	account := providers.Account{}

	tests := []struct {
		name    string
		command providers.ApplyCommandRequest
		wantErr bool
	}{
		{
			name: "unsupported command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Insights/alertRules/alert1",
				Command:    "azure_monitor_alert_rule_disabled",
			},
			wantErr: true,
		},
		{
			name: "unknown command",
			command: providers.ApplyCommandRequest{
				ResourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Insights/alertRules/alert1",
				Command:    "unknown_command",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := service.ApplyCommand(ctx, account, tt.command)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyCommand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if resp.Success {
				t.Error("ApplyCommand() expected Success = false for unsupported operations")
			}
		})
	}
}
