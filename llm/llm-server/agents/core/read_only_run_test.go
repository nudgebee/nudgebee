package core

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

func TestReadOnlyRunMarksOnlyTheCopy(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	readOnly := WithReadOnlyRun(ctx)
	assert.True(t, IsReadOnlyRun(readOnly))
	assert.False(t, IsReadOnlyRun(ctx), "the caller's own context stays as it was")
	assert.False(t, IsReadOnlyRun(nil))
	assert.Nil(t, WithReadOnlyRun(nil))
}

// readOnlyWriteExecutor returns an executor for a read-only run whose agent
// has one write tool, and an action calling it.
func readOnlyWriteExecutor() (*plannerExecutor, map[string]toolcore.NBTool, NBAgentPlannerToolAction) {
	tool := &MockClassifiedTool{
		MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool"},
		RequestType:              toolcore.ToolRequestTypeCreate,
	}
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc"}
	ctx := WithReadOnlyRun(security.NewRequestContextForSuperAdmin())
	executor := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{tool}}, request, 3)
	action := NBAgentPlannerToolAction{ToolID: "probe", Tool: "write_tool", ToolInput: "redis-cli set test 1"}
	return executor, getNameToTool([]toolcore.NBTool{tool}), action
}

// A write in a read-only run is refused as a step, with no approval prompt,
// and the first refusal says how to get the evidence without a change.
func TestReadOnlyRunRefusesWriteWithoutPrompt(t *testing.T) {
	executor, nameToTool, action := readOnlyWriteExecutor()
	step, finish, err := executor.doAction(nameToTool, action, "")
	require.NoError(t, err)
	assert.Nil(t, finish, "no approval prompt and no end of run")
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "read-only investigation")
	assert.Contains(t, step.Observation, "redis-cli INFO")
	assert.Contains(t, step.Observation, "Not checked (needs write access)")
}

// Later refusals in the same run tell the model to stop trying writes.
func TestReadOnlyRunRepeatedRefusalSaysStop(t *testing.T) {
	executor, nameToTool, action := readOnlyWriteExecutor()
	_, _, err := executor.doAction(nameToTool, action, "")
	require.NoError(t, err)
	action.ToolID = "probe-2"
	step, finish, err := executor.doAction(nameToTool, action, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "Do not retry write actions")
	assert.Contains(t, step.Observation, "1 write actions have already been refused")
}

// A run's own message saved with the read-only flag is recovered read-only,
// whoever started the run; any other message is not.
func TestMessageRanReadOnly(t *testing.T) {
	saved, err := common.MarshalJson(toolcore.NBQueryConfig{EventId: "e1", ReadOnlyRun: true})
	require.NoError(t, err)
	stored := string(saved)
	assert.True(t, messageRanReadOnly(ConversationMessage{MessageConfig: &stored}))

	followup := `{"event_id":"e1"}`
	assert.False(t, messageRanReadOnly(ConversationMessage{MessageConfig: &followup}), "a person's follow-up does not carry the flag")
	assert.False(t, messageRanReadOnly(ConversationMessage{}))
	empty := ""
	assert.False(t, messageRanReadOnly(ConversationMessage{MessageConfig: &empty}))
}

// writeThenFinishPlanner proposes one write, then finishes with whatever the
// steps hold, recording what it was shown.
type writeThenFinishPlanner struct {
	mockNBAgentPlanner
	tools []toolcore.NBTool
	calls int
	steps []NBAgentPlannerToolActionStep
}

func (p *writeThenFinishPlanner) GetTools() []toolcore.NBTool { return p.tools }

func (p *writeThenFinishPlanner) Plan(_ context.Context, steps []NBAgentPlannerToolActionStep, _ string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	p.calls++
	p.steps = append([]NBAgentPlannerToolActionStep(nil), steps...)
	if p.calls == 1 {
		return []NBAgentPlannerToolAction{{ToolID: "probe", Tool: "write_tool", ToolInput: "redis-cli set test 1"}}, nil, nil
	}
	return nil, &NBAgentPlannerFinishAction{Data: "root cause found from read-only evidence", Status: ConversationStatusCompleted}, nil
}

// The whole planner loop of a read-only run: the write is refused, the
// planner sees the refusal, and the run finishes with an answer instead of
// pausing on an approval nobody answers.
func TestReadOnlyRunCompletesInsteadOfWaitingForApproval(t *testing.T) {
	previousDao := GetConversationDao()
	SetConversationDao(readOnlyLoopDao{})
	defer SetConversationDao(previousDao)

	tool := &MockClassifiedTool{
		MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool"},
		RequestType:              toolcore.ToolRequestTypeCreate,
	}
	planner := &writeThenFinishPlanner{tools: []toolcore.NBTool{tool}}
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
	request := readOnlyLoopRequest()
	ctx := WithReadOnlyRun(security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", security.GetSystemUserId(), []string{request.AccountId}))

	response, err := executeAgentPlanner(ctx, planner, agent, request, "")
	require.NoError(t, err)
	assert.Equal(t, AgentExecutionStatusSuccess, response.Status)
	assert.Empty(t, response.Followup, "no approval prompt")
	assert.Contains(t, response.Response, "root cause found")
	require.Len(t, planner.steps, 1, "the planner saw the refused step")
	assert.Equal(t, ToolStatusFailure, planner.steps[0].Status)
	assert.Contains(t, planner.steps[0].Observation, "read-only investigation")
	assert.Empty(t, tool.CapturedCtx, "the write never ran")
}

// readOnlyLoopRequest turns memory off: a finished run otherwise starts a
// background memory extraction that reads the conversation store after the
// test has put the real one back.
func readOnlyLoopRequest() NBAgentRequest {
	return NBAgentRequest{
		AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", AgentId: uuid.NewString(), ConversationId: "conversation", MessageId: "message",
		Query: "why is the cart cache slow", QueryConfig: toolcore.NBQueryConfig{Memory: &toolcore.MemoryOverrides{Read: offPtr()}},
	}
}

// readOnlyLoopDao is a conversation store that accepts the writes a planner
// loop makes and returns nothing.
type readOnlyLoopDao struct{ rejectionContinuationDao }

func (readOnlyLoopDao) SaveCompletedConversationAgentCall(id uuid.UUID, _, _, _, _, _, _, _, _, _, _ string, _ toolcore.NBQueryConfig, _ AgentExecutionStatus, _ string) (uuid.UUID, error) {
	return id, nil
}

// Control: the same loop for a run with no read-only mark stops on an
// approval prompt, as a person's own turn does today.
func TestSameLoopWithoutReadOnlyRunAsksForApproval(t *testing.T) {
	previousDao := GetConversationDao()
	SetConversationDao(readOnlyLoopDao{})
	defer SetConversationDao(previousDao)

	tool := &MockClassifiedTool{
		MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool"},
		RequestType:              toolcore.ToolRequestTypeCreate,
	}
	planner := &writeThenFinishPlanner{tools: []toolcore.NBTool{tool}}
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
	request := readOnlyLoopRequest()
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "dddddddd-dddd-dddd-dddd-dddddddddddd", []string{request.AccountId})

	response, err := executeAgentPlanner(ctx, planner, agent, request, "")
	require.NoError(t, err)
	assert.NotEmpty(t, response.Followup, "the approval prompt is raised")
	assert.Empty(t, tool.CapturedCtx, "the write waits for the approval")
}

func (readOnlyLoopDao) ListConversationAgents(_, _ string) ([]ConversationAgent, error) {
	return nil, nil
}

func (readOnlyLoopDao) SaveConversationMessage(_, _, _, _ string, _ MessageRole, _ MessageType, _, _, _ string, _ uuid.UUID, _ any, _ string, _, _ string, _ ConversationStatus) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (readOnlyLoopDao) UpdateConversationAgentWithFollowup(_, _ string) error { return nil }

func (readOnlyLoopDao) LoadLongTermMemories(_ string, _ string, _ int) ([]string, error) {
	return nil, nil
}

func (readOnlyLoopDao) UpdateConversationMessage(_, _ string, _ ConversationStatus) error { return nil }

func (readOnlyLoopDao) GetConversation(_ string) (Conversation, error) { return Conversation{}, nil }
