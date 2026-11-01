package tickets

import "testing"

func TestBuildUpdateAdditionalFields_PassesThroughPlatformSpecificKeysOnly(t *testing.T) {
	params := map[string]any{
		"account_id": "acc-1", "integration_id": "int-1", "ticket_tool": "servicenow",
		"ticket_id": "INC1", "project_key": "incident", "status": "Resolved",
		"severity": "High", "assignee": "sys-user-1", "description": "fixed", "labels": []any{"a"},
		"impact": "High", "urgency": "Low",
		"resolution_code": "Solved (Permanently)", "resolution_notes": "root caused", "work_notes": "checked logs",
	}

	got := buildUpdateAdditionalFields(params)

	wantPresent := map[string]any{
		"impact": "High", "urgency": "Low",
		"resolution_code": "Solved (Permanently)", "resolution_notes": "root caused", "work_notes": "checked logs",
	}
	for k, v := range wantPresent {
		if got[k] != v {
			t.Errorf("additionalFields[%q] = %v, want %v", k, got[k], v)
		}
	}

	for _, universal := range []string{
		"account_id", "integration_id", "ticket_tool", "ticket_id", "project_key",
		"status", "severity", "assignee", "description", "labels",
	} {
		if _, ok := got[universal]; ok {
			t.Errorf("universal param %q must not leak into additionalFields, got %v", universal, got)
		}
	}
}

func TestBuildUpdateAdditionalFields_EmptyWhenOnlyUniversalParamsGiven(t *testing.T) {
	params := map[string]any{"ticket_id": "INC1", "integration_id": "int-1", "status": "Resolved"}
	if got := buildUpdateAdditionalFields(params); len(got) != 0 {
		t.Errorf("expected no additionalFields, got %v", got)
	}
}
