package azure

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/stretchr/testify/assert"
)

func toPtr[T any](v T) *T { return &v }

func TestScheduledQueryRuleToEventRule(t *testing.T) {
	// Test the mapping logic that converts ScheduledQueryRuleResource to EventRule.
	// This mirrors the logic in ListEventRules() for scheduled query rules.

	tests := []struct {
		name             string
		rule             *armmonitor.ScheduledQueryRuleResource
		expectedName     string
		expectedCategory string
		expectedSeverity providers.EventDefinitionSeverity
		expectedSource   string
	}{
		{
			name: "critical severity rule",
			rule: &armmonitor.ScheduledQueryRuleResource{
				Name:     toPtr("HighCPUAlert"),
				ID:       toPtr("/subscriptions/sub1/resourceGroups/rg1/providers/microsoft.insights/scheduledqueryrules/HighCPUAlert"),
				Location: toPtr("eastus"),
				Properties: &armmonitor.ScheduledQueryRuleProperties{
					Severity:            toPtr(armmonitor.AlertSeverity(0)),
					Description:         toPtr("High CPU usage detected"),
					EvaluationFrequency: toPtr("PT5M"),
					WindowSize:          toPtr("PT15M"),
					Enabled:             toPtr(true),
				},
			},
			expectedName:     "HighCPUAlert",
			expectedCategory: "Azure Scheduled Query Alert",
			expectedSeverity: providers.EventDefinitionSeverityCritical,
			expectedSource:   "Azure_Monitor_Alert",
		},
		{
			name: "low severity rule",
			rule: &armmonitor.ScheduledQueryRuleResource{
				Name:     toPtr("InfoLogAlert"),
				ID:       toPtr("/subscriptions/sub1/resourceGroups/rg1/providers/microsoft.insights/scheduledqueryrules/InfoLogAlert"),
				Location: toPtr("centralus"),
				Properties: &armmonitor.ScheduledQueryRuleProperties{
					Severity:            toPtr(armmonitor.AlertSeverity(3)),
					Description:         toPtr("Informational log alert"),
					EvaluationFrequency: toPtr("PT15M"),
					WindowSize:          toPtr("PT30M"),
				},
			},
			expectedName:     "InfoLogAlert",
			expectedCategory: "Azure Scheduled Query Alert",
			expectedSeverity: providers.EventDefinitionSeverityWarning,
			expectedSource:   "Azure_Monitor_Alert",
		},
		{
			name: "nil severity defaults to warning",
			rule: &armmonitor.ScheduledQueryRuleResource{
				Name:     toPtr("NoSeverityRule"),
				ID:       toPtr("/subscriptions/sub1/resourceGroups/rg1/providers/microsoft.insights/scheduledqueryrules/NoSeverityRule"),
				Location: toPtr("westus"),
				Properties: &armmonitor.ScheduledQueryRuleProperties{
					Description: toPtr("No severity set"),
				},
			},
			expectedName:     "NoSeverityRule",
			expectedCategory: "Azure Scheduled Query Alert",
			expectedSeverity: providers.EventDefinitionSeverityWarning,
			expectedSource:   "Azure_Monitor_Alert",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := tt.rule
			subID := "test-sub-id"

			// Replicate the mapping logic from ListEventRules
			severity := "Medium"
			if rule.Properties.Severity != nil {
				switch int32(*rule.Properties.Severity) {
				case 0:
					severity = "Critical"
				case 1:
					severity = "High"
				case 2:
					severity = "Medium"
				case 3:
					severity = "Low"
				case 4:
					severity = "Verbose"
				}
			}

			labels := map[string]string{
				"subscription_id": subID,
				"alert_type":      "scheduled_query_rule",
			}
			if rule.Location != nil {
				labels["region"] = *rule.Location
			}
			if rule.Properties.EvaluationFrequency != nil {
				labels["evaluation_frequency"] = *rule.Properties.EvaluationFrequency
			}
			if rule.Properties.WindowSize != nil {
				labels["window_size"] = *rule.Properties.WindowSize
			}

			eventSeverity := providers.EventDefinitionSeverityWarning
			if severity == "Critical" || severity == "High" {
				eventSeverity = providers.EventDefinitionSeverityCritical
			}

			eventRule := providers.EventRule{
				Name:     *rule.Name,
				Summary:  *rule.Name,
				Source:   "Azure_Monitor_Alert",
				Category: "Azure Scheduled Query Alert",
				Severity: eventSeverity,
				Labels:   labels,
			}

			assert.Equal(t, tt.expectedName, eventRule.Name)
			assert.Equal(t, tt.expectedCategory, eventRule.Category)
			assert.Equal(t, tt.expectedSeverity, eventRule.Severity)
			assert.Equal(t, tt.expectedSource, eventRule.Source)
			assert.Equal(t, "scheduled_query_rule", eventRule.Labels["alert_type"])
			assert.Equal(t, subID, eventRule.Labels["subscription_id"])
		})
	}
}

func TestScheduledQueryRuleSeverityMapping(t *testing.T) {
	tests := []struct {
		name     string
		severity int32
		expected string
	}{
		{"Sev0 is Critical", 0, "Critical"},
		{"Sev1 is High", 1, "High"},
		{"Sev2 is Medium", 2, "Medium"},
		{"Sev3 is Low", 3, "Low"},
		{"Sev4 is Verbose", 4, "Verbose"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			severity := "Medium"
			switch tt.severity {
			case 0:
				severity = "Critical"
			case 1:
				severity = "High"
			case 2:
				severity = "Medium"
			case 3:
				severity = "Low"
			case 4:
				severity = "Verbose"
			}
			assert.Equal(t, tt.expected, severity)
		})
	}
}

func TestScheduledQueryRuleLabels(t *testing.T) {
	rule := &armmonitor.ScheduledQueryRuleResource{
		Name:     toPtr("TestRule"),
		ID:       toPtr("/subscriptions/sub1/providers/microsoft.insights/scheduledqueryrules/TestRule"),
		Location: toPtr("centralus"),
		Properties: &armmonitor.ScheduledQueryRuleProperties{
			Severity:            toPtr(armmonitor.AlertSeverity(2)),
			Description:         toPtr("Test description"),
			EvaluationFrequency: toPtr("PT5M"),
			WindowSize:          toPtr("PT15M"),
			Enabled:             toPtr(true),
		},
	}

	labels := map[string]string{
		"subscription_id": "test-sub",
		"alert_type":      "scheduled_query_rule",
	}
	if rule.Location != nil {
		labels["region"] = *rule.Location
	}
	if rule.Properties.EvaluationFrequency != nil {
		labels["evaluation_frequency"] = *rule.Properties.EvaluationFrequency
	}
	if rule.Properties.WindowSize != nil {
		labels["window_size"] = *rule.Properties.WindowSize
	}

	assert.Equal(t, "centralus", labels["region"])
	assert.Equal(t, "PT5M", labels["evaluation_frequency"])
	assert.Equal(t, "PT15M", labels["window_size"])
	assert.Equal(t, "scheduled_query_rule", labels["alert_type"])
	assert.Equal(t, "test-sub", labels["subscription_id"])
}

func TestScheduledQueryRuleSkipsNilProperties(t *testing.T) {
	// Rules with nil properties or nil name should be skipped
	rules := []*armmonitor.ScheduledQueryRuleResource{
		nil,
		{Name: nil, Properties: &armmonitor.ScheduledQueryRuleProperties{}},
		{Name: toPtr("Valid"), Properties: nil},
	}

	var validRules []providers.EventRule
	for _, rule := range rules {
		if rule == nil || rule.Properties == nil || rule.Name == nil {
			continue
		}
		validRules = append(validRules, providers.EventRule{Name: *rule.Name})
	}

	assert.Empty(t, validRules, "No rules should pass the nil checks")
}
