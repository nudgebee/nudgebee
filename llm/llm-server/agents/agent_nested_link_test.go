package agents

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/agents/core"
	toolcore "nudgebee/llm/tools/core"
)

func TestNestedAgentParentID(t *testing.T) {
	assert.Equal(t, "current-agent", nestedAgentParentID(core.NBAgentRequest{
		AgentId:       "current-agent",
		ParentAgentId: "root-agent",
	}))
	assert.Equal(t, "root-agent", nestedAgentParentID(core.NBAgentRequest{ParentAgentId: "root-agent"}))
}

func TestNestedAgentAdditionalDetails(t *testing.T) {
	details := nestedAgentAdditionalDetails(core.NBAgentResponse{
		AgentId:   "child-agent",
		MessageId: "message-1",
	})

	assert.Equal(t, "child-agent", details[core.NBToolCallAdditionalDetailsAgentId])
	assert.Equal(t, "message-1", details[core.NBToolCallAdditionalDetailsMessageId])
}

func TestAddNestedAgentWaitingDetails(t *testing.T) {
	followup := core.FollowupRequest{Question: "Approve?"}
	details := nestedAgentAdditionalDetails(core.NBAgentResponse{})
	addNestedAgentWaitingDetails(details, core.NBAgentResponse{
		Query:           "restart pod",
		FollowupRequest: followup,
	})

	assert.Equal(t, "restart pod", details[core.NBToolCallAdditionalDetailsQuery])
	assert.Equal(t, followup, details[core.NBToolCallAdditionalDetailsFollowupRequest])
}

func TestHandleNestedAgentCallPreambleError(t *testing.T) {
	expectedErr := errors.New("nested failure")
	reference := toolcore.NBToolResponseReference{Text: "evidence"}

	response, err, handled := handleNestedAgentCallPreamble(toolcore.NbToolContext{}, core.NBAgentResponse{
		AgentId:   "child-agent",
		MessageId: "message-1",
		References: []toolcore.NBToolResponseReference{
			reference,
		},
	}, expectedErr, MetricsAgentName)

	assert.True(t, handled)
	assert.ErrorIs(t, err, expectedErr)
	assert.Equal(t, "child-agent", response.AdditionalDetails[core.NBToolCallAdditionalDetailsAgentId])
	assert.Equal(t, []toolcore.NBToolResponseReference{reference}, response.References)
}

func TestHandleNestedAgentCallPreambleWaiting(t *testing.T) {
	followup := core.FollowupRequest{Question: "Approve?"}

	response, err, handled := handleNestedAgentCallPreamble(toolcore.NbToolContext{}, core.NBAgentResponse{
		AgentId:         "child-agent",
		MessageId:       "message-1",
		Query:           "restart pod",
		FollowupRequest: followup,
		Response:        []string{"Please approve."},
		Status:          core.ConversationStatusWaiting,
		IsTerminal:      true,
	}, nil, MetricsAgentName)

	assert.True(t, handled)
	assert.NoError(t, err)
	assert.Equal(t, toolcore.NBToolResponseStatusWaiting, response.Status)
	assert.Equal(t, "Please approve.", response.Data)
	assert.True(t, response.IsTerminal)
	assert.Equal(t, "restart pod", response.AdditionalDetails[core.NBToolCallAdditionalDetailsQuery])
	assert.Equal(t, followup, response.AdditionalDetails[core.NBToolCallAdditionalDetailsFollowupRequest])
}

func TestHandleNestedAgentCallPreambleCompleted(t *testing.T) {
	response, err, handled := handleNestedAgentCallPreamble(toolcore.NbToolContext{}, core.NBAgentResponse{
		AgentId:   "child-agent",
		MessageId: "message-1",
		Status:    core.ConversationStatusCompleted,
	}, nil, MetricsAgentName)

	assert.False(t, handled)
	assert.NoError(t, err)
	assert.Equal(t, "child-agent", response.AdditionalDetails[core.NBToolCallAdditionalDetailsAgentId])
}
