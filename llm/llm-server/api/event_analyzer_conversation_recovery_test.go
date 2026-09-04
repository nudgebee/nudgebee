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
