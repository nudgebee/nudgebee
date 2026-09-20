package api

import (
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// Reproduces event a1ffed9c: the debug agent paused on a tool-approval
// followup, the user answered "yes", and stage recovery stored that "yes" as
// the investigation because the followup row is newer than the generation row
// that actually carries the RCA.
func TestLatestAgentGenerationResponseIgnoresFollowupReplies(t *testing.T) {
	agentName := "aws_orchestrator"
	summaryAgent := "events"
	rca := "### 📝 Event Summary\n- **Root Cause:** SSM RunShellScript spawned CPU busy loops"
	messages := []core.ConversationMessage{
		{
			AgentName:   &summaryAgent,
			MessageType: string(core.MessageTypeGeneration),
			Response:    "### 1. Event Details",
			Status:      core.ConversationStatusCompleted,
		},
		{
			AgentName:   &agentName,
			MessageType: string(core.MessageTypeGeneration),
			Response:    rca,
			Status:      core.ConversationStatusCompleted,
		},
		{
			AgentName:   &agentName,
			MessageType: string(core.MessageTypeFollowup),
			Response:    "yes",
			Status:      core.ConversationStatusCompleted,
		},
	}

	got, found := latestAgentGenerationResponse(messages, agentName)
	assert.True(t, found)
	assert.Equal(t, rca, got)
}

func TestLatestAgentGenerationResponseSkipsUnfinishedAndForeignMessages(t *testing.T) {
	agentName := "aws_orchestrator"
	other := "k8s_orchestrator"
	messages := []core.ConversationMessage{
		{AgentName: &other, MessageType: string(core.MessageTypeGeneration), Response: "not mine", Status: core.ConversationStatusCompleted},
		{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Response: "", Status: core.ConversationStatusCompleted},
		{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Response: "still running", Status: core.ConversationStatusInProgress},
		{AgentName: &agentName, MessageType: string(core.MessageTypeFollowup), Response: "yes", Status: core.ConversationStatusCompleted},
	}

	got, found := latestAgentGenerationResponse(messages, agentName)
	assert.False(t, found)
	assert.Empty(t, got)
}

// An approval can complete a followup row while its owning generation is still
// pending (or has failed). Falling back to a previous generation would persist
// obsolete findings as the result of the current event investigation (#37865).
func TestLatestAgentGenerationResponseDoesNotReusePreviousGeneration(t *testing.T) {
	agentName := "aws_orchestrator"
	tests := []struct {
		name     string
		status   core.ConversationStatus
		response string
	}{
		{"waiting for approval", core.ConversationStatusWaiting, "Please approve the tool"},
		{"waiting for client tool", core.ConversationStatusWaitingForClientTool, ""},
		{"resumed on another worker", core.ConversationStatusInProgress, ""},
		{"failed generation", core.ConversationStatusFailed, "failed to execute"},
		{"completed without findings", core.ConversationStatusCompleted, ""},
		{"completed with whitespace", core.ConversationStatusCompleted, " \n\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []core.ConversationMessage{
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusCompleted, Response: "obsolete investigation"},
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: tt.status, Response: tt.response},
				{AgentName: &agentName, MessageType: string(core.MessageTypeFollowup), Status: core.ConversationStatusCompleted, Response: "yes"},
			}
			got, found := latestAgentGenerationResponse(messages, agentName)
			assert.False(t, found, "the newest generation must be usable before its stage can be recovered")
			assert.Empty(t, got)
		})
	}
}

func TestLatestAgentGenerationResponsePreservesCompletedFindingsAfterApproval(t *testing.T) {
	agentName := "aws_orchestrator"
	otherAgent := "events"
	for _, approval := range []string{"yes", "no", "test-pg", ""} {
		t.Run("followup="+approval, func(t *testing.T) {
			want := "  Root cause: database retry loop.\n"
			messages := []core.ConversationMessage{
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusCompleted, Response: "obsolete investigation"},
				{AgentName: &agentName, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusCompleted, Response: want},
				{AgentName: &agentName, MessageType: string(core.MessageTypeFollowup), Status: core.ConversationStatusCompleted, Response: approval},
				{AgentName: &otherAgent, MessageType: string(core.MessageTypeGeneration), Status: core.ConversationStatusWaiting, Response: "unrelated stage"},
			}
			got, found := latestAgentGenerationResponse(messages, agentName)
			assert.True(t, found)
			assert.Equal(t, want, got, "preserve the generation text verbatim; approval wording is irrelevant")
		})
	}
}
