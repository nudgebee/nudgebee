package core

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// batchResumeDao serves both phases: the pause (fakeConversationDao) and the
// resume, where the paused client call toolu_B now has its result.
type batchResumeDao struct{ fakeConversationDao }

func (batchResumeDao) SaveConversationToolCall(_, _, _, _, _, _, _, _, _, _, _ string, _ toolcore.NBToolResponseStatus, _ toolcore.NBToolType, _ *string, _ []toolcore.NBToolResponseReference, _, _ []byte) error {
	return nil
}

func (batchResumeDao) GetConversationAgentInput(_, _ string) (string, error) { return "", nil }

func (batchResumeDao) SaveAgentReferences(_, _, _, _ string, _ []AgentReference) error { return nil }

func (batchResumeDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{Status: ConversationStatusInProgress}, nil
}

func (batchResumeDao) GetConversationToolResponse(toolId, _, _, _ string) (string, toolcore.NBToolResponseStatus, error) {
	switch toolId {
	case "toolu_A":
		return "read result", toolcore.NBToolResponseStatusSuccess, nil
	case "toolu_B":
		return "client result", toolcore.NBToolResponseStatusSuccess, nil
	}
	return "", "", sql.ErrNoRows
}

type capturingPlanner struct {
	mockNBAgentPlanner
	steps []NBAgentPlannerToolActionStep
}

func (p *capturingPlanner) Plan(_ context.Context, steps []NBAgentPlannerToolActionStep, _ string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	p.steps = append([]NBAgentPlannerToolActionStep(nil), steps...)
	return nil, &NBAgentPlannerFinishAction{Data: "done", Status: ConversationStatusCompleted}, nil
}

// Reproduces "messages.N.content.2: tool_use ids must be unique" end to end:
// a 2-call batch [A, B] runs sequentially, A completes and B pauses; the
// state is persisted and resumed with B's result. The next prompt must carry
// each tool_use id once — the resumed run used to re-add A from the batch.
func TestResumePausedBatch_NoDuplicateToolIDs(t *testing.T) {
	previousDao := GetConversationDao()
	SetConversationDao(batchResumeDao{})
	defer SetConversationDao(previousDao)

	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	toolOk := &MockContextCapturingTool{NameVal: "TOOL_OK", ReturnOutput: "read result", ReturnStatus: toolcore.NBToolResponseStatusSuccess}
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{toolOk}}
	planner := &capturingPlanner{}
	request := NBAgentRequest{
		AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", UserId: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		ConversationId: "conversation", MessageId: "message", AgentId: "agent", Query: "q",
		ClientTools: []toolcore.NBToolCommand{{Name: "shell_client", Description: "client shell"}},
	}

	a := NBAgentPlannerToolAction{ToolID: "toolu_A", Tool: "TOOL_OK", ToolInput: "ls", TurnID: "turn-3", Log: "thought"}
	b := NBAgentPlannerToolAction{ToolID: "toolu_B", Tool: "shell_client", ToolInput: `{"command":"ls"}`, TurnID: "turn-3", Log: "thought"}

	// Pause through the production path: A runs, B waits for the client.
	paused := newPlannerExecutor(ctx, planner, agent, request, 5)
	steps, finish, err := paused.doIterationSequential(nil, map[string]toolcore.NBTool{"TOOL_OK": toolOk}, []NBAgentPlannerToolAction{a, b})
	require.NoError(t, err)
	require.NotNil(t, finish)
	paused.accumulateSteps(steps)
	state, err := paused.Marshal()
	require.NoError(t, err)

	_, err = executeAgentPlanner(ctx, planner, agent, request, string(state))
	SetConversationDao(previousDao) // rendering below resolves model config via the real DAO
	require.NoError(t, err)

	counts := map[string]int{}
	for _, s := range planner.steps {
		counts[s.Action.ToolID]++
	}
	require.Equal(t, map[string]int{"toolu_A": 1, "toolu_B": 1}, counts, "each call appears once in the history handed to the planner")

	// Render as react_4 does: one assistant turn, each tool_use id once.
	p4 := &NBReActPlanner4{ctx: ctx, request: request, nbAgent: notebookOptOutAgent{}}
	msgs := p4.renderStepsToMessages(planner.steps)
	require.NotEmpty(t, msgs)
	var ids []string
	for _, part := range msgs[0].Parts {
		if tc, ok := part.(llms.ToolCall); ok {
			ids = append(ids, tc.ID)
		}
	}
	require.Equal(t, []string{"toolu_A", "toolu_B"}, ids)
}
