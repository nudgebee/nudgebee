package agents

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCompanionOwnershipGuidance(t *testing.T) {
	var response map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"automated_fix_pr_info":{"url":"https://github.com/acme/repo/pull/1","ownership":{"provider":"github","repository":"github.com/acme/repo","handle":"alice","status":"assigned"}}}`), &response))
	addCompanionIssueGuidance(response)
	require.Equal(t, codeOwnershipInstructions, response["companion_issue_assignment_guidance"])
	pr := response["automated_fix_pr_info"].(map[string]any)
	require.Equal(t, "alice", pr["ownership"].(map[string]any)["handle"])
	for _, clause := range []string{"already created earlier in this chat", "being created now", "explicit user-supplied assignee", "existing non-empty issue assignment", "different repository", "never transfer identities", "keep the issue and PR open", "Do not create an issue"} {
		require.Contains(t, codeOwnershipInstructions, clause)
	}
}

func TestNoCompanionGuidanceWithoutOwnership(t *testing.T) {
	response := map[string]any{"answer": "no fix requested"}
	addCompanionIssueGuidance(response)
	require.NotContains(t, response, "companion_issue_assignment_guidance")
}
