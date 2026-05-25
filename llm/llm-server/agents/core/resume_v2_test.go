package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsResumableAgentStatus locks in the contract for which agent statuses
// V2's idempotency guard treats as eligible for resume vs already-progressed.
//
// Both `waiting` (user-question followup) and `waiting_for_client_tool`
// (shell_execute / custom client tool resume) must qualify. The original
// implementation only accepted `waiting`, which silently swallowed every
// client-tool-result resume — the conversation stayed IN_PROGRESS until
// the client-side task timeout. See conversations on PR #29933 / #29746
// (terminal-bench harness) for the regression observation.
func TestIsResumableAgentStatus(t *testing.T) {
	cases := []struct {
		name   string
		status AgentExecutionStatus
		want   bool
	}{
		{"waiting → resumable (followup question)", AgentExecutionStatusWaiting, true},
		{"waiting_for_client_tool → resumable (client-tool resume)", AgentExecutionStatusWaitingForClientTool, true},
		{"WAITING uppercased → resumable (case-insensitive)", "WAITING", true},
		{"Waiting_For_Client_Tool mixed case → resumable", "Waiting_For_Client_Tool", true},
		{"success → not resumable (already advanced)", AgentExecutionStatusSuccess, false},
		{"fail → not resumable", AgentExecutionStatusFail, false},
		{"in_progress → not resumable (someone else holds the work)", AgentExecutionStatusInProgress, false},
		{"empty → not resumable", AgentExecutionStatus(""), false},
		{"unknown garbage → not resumable", AgentExecutionStatus("flapdoodle"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isResumableAgentStatus(tc.status))
		})
	}
}
