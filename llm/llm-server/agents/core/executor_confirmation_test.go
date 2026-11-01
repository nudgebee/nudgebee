package core

import (
	"context"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteConfirmationRequired: write tools (create/update/delete) prompt, except
// watch_resource which is exempt; watch_cancel is intentionally NOT exempt.
func TestWriteConfirmationRequired(t *testing.T) {
	rt := func(t toolcore.ToolRequestType) *toolcore.ToolRequestType { return &t }

	cases := []struct {
		name        string
		requestType *toolcore.ToolRequestType
		toolName    string
		want        bool
	}{
		{"nil request type", nil, "kubectl_execute", false},
		{"empty request type", rt(""), "kubectl_execute", false},
		{"read is never confirmed", rt(toolcore.ToolRequestTypeRead), "kubectl_execute", false},
		{"create write tool confirms", rt(toolcore.ToolRequestTypeCreate), "kubectl_execute", true},
		{"update write tool confirms", rt(toolcore.ToolRequestTypeUpdate), "aws_execute", true},
		{"delete write tool confirms", rt(toolcore.ToolRequestTypeDelete), "shell_execute", true},
		{"watch_resource create is exempt", rt(toolcore.ToolRequestTypeCreate), tools.ToolWatchResource, false},
		{"watch_resource exempt case-insensitive", rt(toolcore.ToolRequestTypeCreate), "Watch_Resource", false},
		{"watch_cancel update still confirms", rt(toolcore.ToolRequestTypeUpdate), tools.ToolWatchCancel, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action := NBAgentPlannerToolAction{Tool: tc.toolName}
			assert.Equal(t, tc.want, writeConfirmationRequired(confirmStubTool{name: tc.toolName}, tc.requestType, action))
		})
	}
}

// TestWriteConfirmationRequired_Exempt: a tool implementing
// ToolConfirmationExempt suppresses the card only when it says so, and only for
// write request types — a read was never confirmed in the first place.
func TestWriteConfirmationRequired_Exempt(t *testing.T) {
	rt := func(t toolcore.ToolRequestType) *toolcore.ToolRequestType { return &t }
	action := NBAgentPlannerToolAction{Tool: "automation_restart_pods", ToolInput: `{"reason":"x"}`}

	exempt := confirmExemptStubTool{confirmStubTool{name: "automation_restart_pods"}, true}
	notExempt := confirmExemptStubTool{confirmStubTool{name: "automation_restart_pods"}, false}

	assert.False(t, writeConfirmationRequired(exempt, rt(toolcore.ToolRequestTypeCreate), action))
	assert.True(t, writeConfirmationRequired(notExempt, rt(toolcore.ToolRequestTypeCreate), action))
	// Still no confirmation for reads, exempt or not.
	assert.False(t, writeConfirmationRequired(notExempt, rt(toolcore.ToolRequestTypeRead), action))
}

// TestWriteConfirmationRequired_RecommendationWriteTools: the typed resolution
// write tools must never slip into the watch_resource exemption.
func TestWriteConfirmationRequired_RecommendationWriteTools(t *testing.T) {
	rt := func(t toolcore.ToolRequestType) *toolcore.ToolRequestType { return &t }
	confirms := func(requestType *toolcore.ToolRequestType, name string) bool {
		return writeConfirmationRequired(confirmStubTool{name: name}, requestType, NBAgentPlannerToolAction{Tool: name})
	}
	assert.True(t, confirms(rt(toolcore.ToolRequestTypeUpdate), tools.ToolRecommendationApply))
	assert.True(t, confirms(rt(toolcore.ToolRequestTypeUpdate), tools.ToolRecommendationExecuteCli))
	assert.True(t, confirms(rt(toolcore.ToolRequestTypeCreate), tools.ToolRecommendationRecordTicketResolution))
	assert.True(t, confirms(rt(toolcore.ToolRequestTypeUpdate), tools.ToolRecommendationSetDismissal))
}

// confirmStubTool / confirmScopedStubTool exercise confirmationKeyForAction's two paths.
type confirmStubTool struct{ name string }

func (s confirmStubTool) Name() string                     { return s.name }
func (s confirmStubTool) GetType() toolcore.NBToolType     { return toolcore.NBToolTypeTool }
func (s confirmStubTool) Description() string              { return "" }
func (s confirmStubTool) InputSchema() toolcore.ToolSchema { return toolcore.ToolSchema{} }
func (s confirmStubTool) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}

type confirmExemptStubTool struct {
	confirmStubTool
	skip bool
}

func (s confirmExemptStubTool) SkipWriteConfirmation(string) bool { return s.skip }

type confirmScopedStubTool struct{ confirmStubTool }

func (s confirmScopedStubTool) ConfirmationKey(toolInput string) string {
	return s.name + ":" + toolInput
}

func TestConfirmationKeyForAction(t *testing.T) {
	plain := confirmStubTool{name: "plain_tool"}
	scoped := confirmScopedStubTool{confirmStubTool{name: "scoped_tool"}}

	// Unscoped tools keep the historical per-tool key.
	key := confirmationKeyForAction(plain, NBAgentPlannerToolAction{Tool: "plain_tool", ToolInput: "a"})
	assert.Equal(t, "plain_tool", key)

	// Scoped tools key per action: same input → same key, different input → different key.
	k1 := confirmationKeyForAction(scoped, NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "a"})
	k2 := confirmationKeyForAction(scoped, NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "b"})
	assert.Equal(t, "scoped_tool:a", k1)
	assert.NotEqual(t, k1, k2)
}

func TestConfirmationApprovedForAction(t *testing.T) {
	scoped := confirmScopedStubTool{confirmStubTool{name: "scoped_tool"}}
	nameToTool := map[string]toolcore.NBTool{"SCOPED_TOOL": scoped}
	action := NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "a"}

	// Approval recorded under the per-action key is found; an approval under a
	// different action's key is not.
	e := &plannerExecutor{agentRequest: NBAgentRequest{AccountId: "account"}}
	approved := func(confirmations map[string]string, action NBAgentPlannerToolAction) bool {
		return isToolConfirmationApproved(confirmations, e.confirmationKeyOf(nameToTool, action))
	}
	assert.True(t, approved(map[string]string{"scoped_tool:a": "yes"}, action))
	assert.False(t, approved(map[string]string{"scoped_tool:b": "yes"}, action))
	// A bare per-tool approval no longer unlocks a scoped tool's other actions.
	assert.False(t, approved(map[string]string{"scoped_tool": "yes"}, action))
	// Rejection under the right key stays a rejection.
	assert.False(t, approved(map[string]string{"scoped_tool:a": "no"}, action))

	// Tools absent from the map fall back to the per-tool key.
	unknown := NBAgentPlannerToolAction{Tool: "other_tool", ToolInput: "x"}
	assert.True(t, approved(map[string]string{"other_tool": "yes"}, unknown))
}

func TestConfirmationRejectedForAction(t *testing.T) {
	scoped := confirmScopedStubTool{confirmStubTool{name: "scoped_tool"}}
	nameToTool := map[string]toolcore.NBTool{"SCOPED_TOOL": scoped}
	action := NBAgentPlannerToolAction{Tool: "scoped_tool", ToolInput: "a"}

	e := &plannerExecutor{agentRequest: NBAgentRequest{AccountId: "account"}}
	rejected := func(confirmations map[string]string) bool {
		return confirmationRejected(confirmations, e.confirmationKeyOf(nameToTool, action))
	}
	assert.True(t, rejected(map[string]string{"scoped_tool:a": "no"}))
	assert.True(t, rejected(map[string]string{"scoped_tool:a": ""}))
	assert.False(t, rejected(map[string]string{"scoped_tool:a": "yes"}))
	assert.False(t, rejected(map[string]string{"scoped_tool:b": "no"}))
	assert.False(t, rejected(nil))
}

type rejectionContinuationPlanner struct {
	mockNBAgentPlanner
	steps []NBAgentPlannerToolActionStep
}

func (p *rejectionContinuationPlanner) Plan(_ context.Context, steps []NBAgentPlannerToolActionStep, _ string) ([]NBAgentPlannerToolAction, *NBAgentPlannerFinishAction, error) {
	p.steps = append([]NBAgentPlannerToolActionStep(nil), steps...)
	return nil, &NBAgentPlannerFinishAction{Data: "planner continued", Status: ConversationStatusCompleted}, nil
}

type rejectionContinuationDao struct{ IConversationDao }

func (rejectionContinuationDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{Status: ConversationStatusInProgress}, nil
}

func TestRejectedConfirmationContinuesResumedPlannerTurn(t *testing.T) {
	previousDao := GetConversationDao()
	SetConversationDao(rejectionContinuationDao{})
	defer SetConversationDao(previousDao)

	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"})
	agent := &MockAgent{}
	planner := &rejectionContinuationPlanner{}
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", ConversationId: "conversation", MessageId: "message", Query: "run the command"}
	executor := newPlannerExecutor(ctx, planner, agent, request, 3)
	executor.currentAction = []NBAgentPlannerToolAction{{ToolID: "pending-write", Tool: "write_tool", ToolInput: "touch /tmp/probe"}}
	state, err := executor.Marshal()
	require.NoError(t, err)

	request.QueryConfig.ToolConfirmations = map[string]string{"write_tool": "no"}
	response, err := executeAgentPlanner(ctx, planner, agent, request, string(state))
	require.NoError(t, err)
	assert.Equal(t, AgentExecutionStatusSuccess, response.Status)
	assert.Contains(t, response.Response, "planner continued")
	assert.Empty(t, response.Followup)
	require.Len(t, planner.steps, 1)
	assert.Equal(t, ToolStatusFailure, planner.steps[0].Status)
	assert.Contains(t, planner.steps[0].Observation, "User rejected approval")
	assert.Contains(t, planner.steps[0].Observation, "not executed")
}

func TestRejectedWriteRetryRemainsToolFailure(t *testing.T) {
	tool := &MockClassifiedTool{
		MockContextCapturingTool: MockContextCapturingTool{NameVal: "write_tool"},
		RequestType:              toolcore.ToolRequestTypeCreate,
	}
	request := NBAgentRequest{
		AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		QueryConfig: toolcore.NBQueryConfig{
			ToolConfirmations: map[string]string{"write_tool": "no"},
		},
	}
	executor := newPlannerExecutor(security.NewRequestContextForSuperAdmin(), &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{tool}}, request, 3)
	action := NBAgentPlannerToolAction{ToolID: "denied-write", Tool: "write_tool", ToolInput: "touch /tmp/probe"}
	step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), action, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "not executed")
}

// A bare-string `commands` must still produce the full card, DESTRUCTIVE warning included.
func TestConfirmationQuestionForAction_UsesRepairedInput(t *testing.T) {
	tool := tools.RecommendationCliTool{}
	cmd := "aws ec2 terminate-instances --instance-ids i-aaa"

	asString := NBAgentPlannerToolAction{Tool: tools.ToolRecommendationExecuteCli, ToolInput: `{"commands":"` + cmd + `"}`}
	asArray := NBAgentPlannerToolAction{Tool: tools.ToolRecommendationExecuteCli, ToolInput: `{"commands":["` + cmd + `"]}`}

	assert.Empty(t, tool.ConfirmationQuestion(asString.ToolInput),
		"precondition: the tool cannot read a bare string, which is what made the card empty")

	got := confirmationQuestionForAction(tool, asString)
	assert.Contains(t, got, cmd)
	assert.Contains(t, got, "DESTRUCTIVE")
	assert.Equal(t, confirmationQuestionForAction(tool, asArray), got,
		"a bare string and the array the model should have sent must produce the same card")

	// The approval key stays on the original input (resume re-derives it from that).
	assert.Equal(t, tool.ConfirmationKey(asString.ToolInput), confirmationKeyForAction(tool, asString))

	// Tools without a custom question keep the default rendering.
	assert.Empty(t, confirmationQuestionForAction(confirmStubTool{name: "plain_tool"}, asString))
}
