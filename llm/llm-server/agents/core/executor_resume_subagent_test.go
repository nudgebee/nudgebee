package core

import (
	"context"
	"errors"
	"testing"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resumeProbeTool stands in for a tool that wraps a sub-agent (delegate_agent):
// it requires a "prompt" argument and records what the planner handed it.
type resumeProbeTool struct {
	name     string
	required []string
	err      error

	calls     int
	gotInput  toolcore.NBToolCallRequest
	gotAnswer string
}

func (t *resumeProbeTool) Name() string                 { return t.name }
func (t *resumeProbeTool) Description() string          { return "probe" }
func (t *resumeProbeTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (t *resumeProbeTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type:       toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{"prompt": {Type: toolcore.ToolSchemaTypeString}},
		Required:   t.required,
	}
}
func (t *resumeProbeTool) Call(ctx toolcore.NbToolContext, input toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	t.calls++
	t.gotInput = input
	t.gotAnswer = ctx.FollowupAnswer
	if t.err != nil {
		return toolcore.NBToolResponse{}, t.err
	}
	return toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusSuccess, Data: "sub-agent finished"}, nil
}

// resumeDao reports the paused tool call as WAITING with a sub-agent that has
// not finished, and accepts the writes the resume makes.
type resumeDao struct{ IConversationDao }

func (d *resumeDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{Status: ConversationStatusInProgress}, nil
}
func (d *resumeDao) GetConversationToolResponse(_, _, _, _ string) (string, toolcore.NBToolResponseStatus, error) {
	return "", toolcore.NBToolResponseStatusWaiting, nil
}
func (d *resumeDao) GetConversationToolCallChildAgentId(_, _, _ string) string { return "" }
func (d *resumeDao) SaveConversationToolCall(_, _, _, _, _, _, _, _, _, _, _ string, _ toolcore.NBToolResponseStatus, _ toolcore.NBToolType, _ *string, _ []toolcore.NBToolResponseReference, _ []byte, _ []byte) error {
	return nil
}
func (d *resumeDao) SaveCompletedConversationAgentCall(_ uuid.UUID, _, _, _, _, _, _, _, _, _, _ string, _ toolcore.NBQueryConfig, _ AgentExecutionStatus, _ string) (uuid.UUID, error) {
	return uuid.New(), nil
}

type resumeProbePlanner struct {
	mockNBAgentPlanner
	tools []toolcore.NBTool
	steps []NBAgentPlannerToolActionStep
}

func (p *resumeProbePlanner) GetTools() []toolcore.NBTool { return p.tools }
func (p *resumeProbePlanner) Plan(_ context.Context, steps []NBAgentPlannerToolActionStep, _ string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	p.steps = append([]NBAgentPlannerToolActionStep(nil), steps...)
	return nil, &NBAgentPlannerFinishAction{Data: "done", Status: ConversationStatusCompleted}, nil
}

const (
	resumeAccountID = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	resumeParentID  = "11111111-1111-1111-1111-111111111111"
	resumeChildID   = "22222222-2222-2222-2222-222222222222"
	resumeToolID    = "delegate-1"
	delegationInput = `{"prompt":"rerun the failed CI job and report the result"}`
)

// resumeWaitingTool builds the saved state of a parent paused on one tool call
// whose followup is fup, then resumes it with request.
func resumeWaitingTool(t *testing.T, tool *resumeProbeTool, fup FollowupRequest, request NBAgentRequest) *resumeProbePlanner {
	t.Helper()
	previousDao := GetConversationDao()
	SetConversationDao(&resumeDao{})
	t.Cleanup(func() { SetConversationDao(previousDao) })

	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{resumeAccountID})
	agent := &MockAgent{SupportedTools: []toolcore.NBTool{tool}}
	planner := &resumeProbePlanner{tools: []toolcore.NBTool{tool}}

	request.AccountId = resumeAccountID
	request.ConversationId = "conversation"
	request.MessageId = "message"
	if request.AgentId == "" {
		request.AgentId = resumeParentID
	}

	action := NBAgentPlannerToolAction{ToolID: resumeToolID, Tool: tool.name, ToolInput: delegationInput}
	paused := newPlannerExecutor(ctx, planner, agent, request, 3)
	paused.currentAction = []NBAgentPlannerToolAction{action}
	paused.steps = []NBAgentPlannerToolActionStep{{Action: action, Observation: fup.Question, Status: ToolStatusWaiting, Followup: &fup}}
	state, err := paused.Marshal()
	require.NoError(t, err)

	_, err = executeAgentPlanner(ctx, planner, agent, request, string(state))
	require.NoError(t, err)
	return planner
}

// The sub-agent asked to approve one of its writes and the user said yes. The
// parent's refinement records the approval under the sub-agent's key and
// resets the query to the parent's original question — which is not the
// delegation's input. The delegation must re-run with its own prompt so the
// sub-agent can pick the approval up.
func TestResume_SubAgentApprovalKeepsDelegationInput(t *testing.T) {
	tool := &resumeProbeTool{name: "delegate_agent", required: []string{"prompt"}}
	fup := FollowupRequest{
		FollowupType: FollowupTypeToolConfirmation,
		Question:     "Tool(github_execute) is trying to update cluster resources. Do you want to continue?",
		AgentId:      uuid.MustParse(resumeChildID),
	}
	request := NBAgentRequest{
		Query:       "rerun the failed CI job",
		QueryConfig: toolcore.NBQueryConfig{ToolConfirmations: map[string]string{"github_execute": "yes"}},
	}

	planner := resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls, "the delegation re-runs so the sub-agent can continue")
	assert.Equal(t, "rerun the failed CI job and report the result", tool.gotInput.Arguments["prompt"])
	assert.Empty(t, tool.gotAnswer, "an approval is not an answer for the sub-agent")
	require.Len(t, planner.steps, 1)
	assert.Equal(t, ToolStatusSuccess, planner.steps[0].Status)
	assert.NotContains(t, planner.steps[0].Observation, "missing required fields")
}

// The sub-agent asked the user a question. The answer is for the sub-agent,
// not a replacement for the delegation's input.
func TestResume_SubAgentQuestionPassesAnswerToSubAgent(t *testing.T) {
	tool := &resumeProbeTool{name: "delegate_agent", required: []string{"prompt"}}
	fup := FollowupRequest{
		FollowupType: FollowupTypeText,
		Question:     "Which namespace should I check?",
		AgentId:      uuid.MustParse(resumeChildID),
	}
	request := NBAgentRequest{Query: "payments"}

	planner := resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	assert.Equal(t, "rerun the failed CI job and report the result", tool.gotInput.Arguments["prompt"])
	assert.Equal(t, "payments", tool.gotAnswer)
	require.Len(t, planner.steps, 1)
	assert.Equal(t, ToolStatusSuccess, planner.steps[0].Status)
}

// Only the case that broke changes. With a config resolved, the old check
// already kept the input, and that stays exactly as it was: no answer is
// handed down.
func TestResume_SubAgentPauseWithResolvedConfigIsUnchanged(t *testing.T) {
	tool := &resumeProbeTool{name: "k8s_debug_agent"}
	fup := FollowupRequest{
		FollowupType: FollowupTypeSingleSelect,
		Question:     "Which deployment?",
		AgentId:      uuid.MustParse(resumeChildID),
	}
	request := NBAgentRequest{
		Query:       "checkout",
		QueryConfig: toolcore.NBQueryConfig{ToolConfigs: map[string]string{"kubectl_execute": "dev-cluster"}},
	}

	resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	assert.Equal(t, "rerun the failed CI job and report the result", tool.gotInput.Arguments["prompt"])
	assert.Empty(t, tool.gotAnswer)
}

// This agent's own tool paused on an approval whose key does not match: the
// old behaviour (the reply replaces the input) is untouched — the fix is
// scoped to pauses that came from a sub-agent.
func TestResume_OwnApprovalWithoutKeyMatchIsUnchanged(t *testing.T) {
	tool := &resumeProbeTool{name: "workflow_trigger"}
	fup := FollowupRequest{
		FollowupType: FollowupTypeToolConfirmation,
		Question:     "Do you want to continue?",
		AgentId:      uuid.MustParse(resumeParentID),
	}
	request := NBAgentRequest{Query: "run it"}

	resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	assert.Equal(t, "run it", tool.gotInput.Command)
	assert.Empty(t, tool.gotAnswer)
}

// A config choice the sub-agent asked for reaches it through the propagated
// configs; the refinement resets the query to the parent's question, which
// must not be handed to the sub-agent as if it were an answer.
func TestResume_SubAgentConfigChoiceKeepsInputWithoutAnswer(t *testing.T) {
	tool := &resumeProbeTool{name: "delegate_agent", required: []string{"prompt"}}
	fup := FollowupRequest{
		FollowupType: FollowupTypeToolConfig,
		Question:     "Which Jira integration?",
		AgentId:      uuid.MustParse(resumeChildID),
	}
	request := NBAgentRequest{Query: "open a ticket for the outage"}

	resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	assert.Equal(t, "rerun the failed CI job and report the result", tool.gotInput.Arguments["prompt"])
	assert.Empty(t, tool.gotAnswer)
}

// A question this agent's own tool asked (ask_clarification-style) is
// unchanged: the answer becomes the tool's input.
func TestResume_OwnQuestionStillReplacesToolInput(t *testing.T) {
	tool := &resumeProbeTool{name: "ask_clarification"}
	fup := FollowupRequest{
		FollowupType: FollowupTypeText,
		Question:     "Which namespace?",
		AgentId:      uuid.MustParse(resumeParentID),
	}
	request := NBAgentRequest{Query: "payments"}

	resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	assert.Equal(t, "payments", tool.gotInput.Command)
	assert.Empty(t, tool.gotAnswer)
}

// When this agent's own id cannot be read, the pause cannot be attributed to a
// sub-agent, so the old behaviour applies: the reply replaces the input.
func TestResume_UnreadableOwnAgentIdIsUnchanged(t *testing.T) {
	tool := &resumeProbeTool{name: "ask_clarification"}
	fup := FollowupRequest{
		FollowupType: FollowupTypeText,
		Question:     "Which namespace?",
		AgentId:      uuid.MustParse(resumeChildID),
	}
	request := NBAgentRequest{Query: "payments", AgentId: "not-a-uuid"}

	resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	assert.Equal(t, "payments", tool.gotInput.Command)
	assert.Empty(t, tool.gotAnswer)
}

// A tool that fails with an error when it runs on resume is recorded like it
// is in the main loop: the planner gets a failed observation instead of
// re-planning as if the call never ran.
func TestResume_ToolErrorIsRecorded(t *testing.T) {
	tool := &resumeProbeTool{name: "delegate_agent", required: []string{"prompt"}, err: errors.New("runbook server returned error (status 500)")}
	fup := FollowupRequest{
		FollowupType: FollowupTypeToolConfirmation,
		Question:     "Do you want to continue?",
		AgentId:      uuid.MustParse(resumeChildID),
	}
	request := NBAgentRequest{
		Query:       "run it",
		QueryConfig: toolcore.NBQueryConfig{ToolConfirmations: map[string]string{"workflow_trigger": "yes"}},
	}

	planner := resumeWaitingTool(t, tool, fup, request)

	require.Equal(t, 1, tool.calls)
	require.Len(t, planner.steps, 1)
	assert.Equal(t, ToolStatusFailure, planner.steps[0].Status)
	assert.Contains(t, planner.steps[0].Observation, "status 500")
}

// Every tool that runs a sub-agent (delegate_agent, agent tools, metrics,
// traces) starts it through ExecuteAgentToolCall, so this is the one place the
// answer to the sub-agent's question has to land.
func TestSubAgentQuery(t *testing.T) {
	query := toolcore.NBToolCallRequest{Command: "rerun the failed CI job and report the result"}
	assert.Equal(t, query.Command, subAgentQuery(toolcore.NbToolContext{}, query), "a fresh or approved run gets the tool's command")
	assert.Equal(t, "payments", subAgentQuery(toolcore.NbToolContext{FollowupAnswer: "payments"}, query), "a sub-agent paused on a question gets the answer")
}
