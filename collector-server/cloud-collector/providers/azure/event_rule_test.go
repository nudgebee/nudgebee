package azure

import (
	"testing"
)

// TestGetAzureEventRulesEmbedded verifies that when no config file path is provided
// and the file doesn't exist in default locations, the function falls back to
// embedded default rules instead of throwing an error.
func TestGetAzureEventRulesEmbedded(t *testing.T) {
	// When no path is provided and files may not exist, should use embedded defaults
	rules, err := GetAzureEventRules("")
	if err != nil {
		t.Fatalf("Failed to load event rules: %v", err)
	}

	if len(rules) == 0 {
		t.Fatal("Expected at least one rule from defaults")
	}

	// Verify we have the expected default rules
	expectedRules := []string{
		"azure_vm_state_change",
		"azure_sql_database_operation",
		"azure_storage_account_operation",
		"azure_webapp_state_change",
		"azure_aks_cluster_operation",
		"azure_resource_action_generic",
		"azure_resource_delete",
	}

	if len(rules) != len(expectedRules) {
		t.Errorf("Expected %d rules, got %d", len(expectedRules), len(rules))
	}

	// Verify first rule has required fields
	if rules[0].Name == "" {
		t.Error("Expected rule to have a name")
	}
	if rules[0].Triggers.SourceSystem == "" {
		t.Error("Expected rule to have a trigger source")
	}
}
