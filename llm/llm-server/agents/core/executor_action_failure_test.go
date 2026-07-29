package core

import (
	"context"
	"testing"
	"time"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConversationDao embeds IConversationDao so it satisfies the interface
// while only stubbing the single method recordFailedActionStep persists
// through (SaveCompletedConversationAgentCall) — the rest are never called.
type fakeConversationDao struct {
	IConversationDao
}

func (fakeConversationDao) SaveCompletedConversationAgentCall(
	_ uuid.UUID,
	_, _, _, _, _, _, _, _, _, _ string,
	_ toolcore.NBQueryConfig,
	_ AgentExecutionStatus,
	_ string,
) (uuid.UUID, error) {
	return uuid.Nil, nil
}

// These tests pin the fix for the "action <toolId> failed" loop break: a
// pre-execution action error (here: IsAgentToolAuthorizedToProcessRequest
// rejecting a tool that is dispatchable via nameToTool but absent from the
// agent's supported tools — the exact production failure behind
// "action kubectl_execute-* failed") must be recorded as a ToolStatusFailure
// step and must NOT abort the iteration. Aborting discarded sibling results
// and surfaced the bare error string verbatim as the chat response.

func newActionFailureTestExecutor(agent NBAgent) *plannerExecutor {
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	return &plannerExecutor{
		ctx:   ctx,
		agent: agent,
		agentRequest: NBAgentRequest{
			AccountId:      "cccccccc-cccc-cccc-cccc-cccccccccccc",
			UserId:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			ConversationId: "conv_id",
			MessageId:      "msg_id",
			AgentId:        "agent_id",
		},
		toolCallCache: turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)},
	}
}

func TestDoIterationParallel_ActionFailureDoesNotAbortIteration(t *testing.T) {
	prevDao := conversationDao
	SetConversationDao(fakeConversationDao{})
	t.Cleanup(func() { conversationDao = prevDao })

	toolOk := &MockContextCapturingTool{NameVal: "TOOL_OK", ReturnOutput: "ok-output", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	toolUnauthorized := &MockContextCapturingTool{NameVal: "TOOL_UNAUTH", ReturnOutput: "never", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	nameToTool := map[string]toolcore.NBTool{
		"TOOL_OK":     toolOk,
		"TOOL_UNAUTH": toolUnauthorized,
	}

	// TOOL_UNAUTH is dispatchable via nameToTool but NOT in the agent's
	// supported tools, so IsAgentToolAuthorizedToProcessRequest returns an
	// error ("auth: tool not found - ...") — a pre-execution action failure.
	executor := newActionFailureTestExecutor(&MockAgent{SupportedTools: []toolcore.NBTool{toolOk}})

	actions := []NBAgentPlannerToolAction{
		{ToolID: "Plan1", Tool: "TOOL_UNAUTH", ToolInput: "input1"},
		{ToolID: "Plan2", Tool: "TOOL_OK", ToolInput: "input2"},
	}

	var steps []NBAgentPlannerToolActionStep
	var finish *NBAgentPlannerFinishAction
	var err error
	done := make(chan struct{})
	go func() {
		steps, finish, err = executor.doIterationParallel(context.Background(), nil, nameToTool, actions)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("doIterationParallel timed out")
	}

	assert.NoError(t, err, "an action failure must not abort the iteration with an error")
	assert.Nil(t, finish)
	require.Len(t, steps, 2, "both the failed and the sibling successful step must be returned")

	byToolID := map[string]NBAgentPlannerToolActionStep{}
	for _, s := range steps {
		byToolID[s.Action.ToolID] = s
	}

	failedStep, ok := byToolID["Plan1"]
	require.True(t, ok, "failed action must be present as a step")
	assert.Equal(t, ToolStatusFailure, failedStep.Status)
	assert.Contains(t, failedStep.Observation, "action execution failed", "observation must carry the failure reason")
	assert.Contains(t, failedStep.Observation, "tool not found", "observation must carry the underlying error, not be discarded")

	okStep, ok := byToolID["Plan2"]
	require.True(t, ok, "sibling successful action must not be cancelled by the failure")
	assert.Equal(t, ToolStatusSuccess, okStep.Status)
	assert.Equal(t, "ok-output", okStep.Observation)
}

func TestDoIterationSequential_ActionFailureRecordsFailedStep(t *testing.T) {
	prevDao := conversationDao
	SetConversationDao(fakeConversationDao{})
	t.Cleanup(func() { conversationDao = prevDao })

	toolUnauthorized := &MockContextCapturingTool{NameVal: "TOOL_UNAUTH", ReturnOutput: "never", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	toolOk := &MockContextCapturingTool{NameVal: "TOOL_OK", ReturnOutput: "ok-output", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	nameToTool := map[string]toolcore.NBTool{
		"TOOL_OK":     toolOk,
		"TOOL_UNAUTH": toolUnauthorized,
	}

	executor := newActionFailureTestExecutor(&MockAgent{SupportedTools: []toolcore.NBTool{toolOk}})

	actions := []NBAgentPlannerToolAction{
		{ToolID: "Plan1", Tool: "TOOL_UNAUTH", ToolInput: "input1"},
		{ToolID: "Plan2", Tool: "TOOL_OK", ToolInput: "input2"},
	}

	steps, finish, err := executor.doIterationSequential(nil, nameToTool, actions)

	assert.NoError(t, err, "an action failure must not abort the iteration with an error")
	assert.Nil(t, finish)
	require.Len(t, steps, 2, "the action after the failed one must still execute")
	assert.Equal(t, ToolStatusFailure, steps[0].Status)
	assert.Contains(t, steps[0].Observation, "action execution failed")
	assert.Contains(t, steps[0].Observation, "tool not found")
	assert.Equal(t, ToolStatusSuccess, steps[1].Status)
	assert.Equal(t, "ok-output", steps[1].Observation)
}
