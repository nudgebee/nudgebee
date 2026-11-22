package core

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/security/toolapproval"
	toolcore "nudgebee/llm/tools/core"
)

// countingWriteTool is a write tool that counts its runs and returns err.
type countingWriteTool struct {
	MockClassifiedTool
	runs int
	err  error
}

func (c *countingWriteTool) Call(ctx toolcore.NbToolContext, request toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	c.runs++
	if approvedWriteOf(ctx.Ctx, c) != nil {
		panic("the tool's own context must not carry the approval mark")
	}
	return toolcore.NBToolResponse{Data: "deployment.apps/x scaled", Status: toolcore.NBToolResponseStatusSuccess}, c.err
}

func newCountingWriteTool() *countingWriteTool {
	return &countingWriteTool{MockClassifiedTool: MockClassifiedTool{
		MockContextCapturingTool: MockContextCapturingTool{NameVal: "kubectl_execute"},
		RequestType:              toolcore.ToolRequestTypeUpdate,
	}}
}

// stubRecorder installs recorder as the ApprovedWriteRecorder for one test.
func stubRecorder(t *testing.T, recorder func(*security.RequestContext, ApprovedWrite) ApprovedWriteClaim) {
	t.Helper()
	previous := ApprovedWriteRecorder
	ApprovedWriteRecorder = recorder
	t.Cleanup(func() { ApprovedWriteRecorder = previous })
}

const approvedWritePerson = "dddddddd-dddd-dddd-dddd-dddddddddddd"

// approvedKubectlExecutor is an executor whose request carries a person's "yes" for kubectl_execute,
// answered in follow-up message "followup-1".
func approvedKubectlExecutor(tool toolcore.NBTool, refs map[string]toolcore.ToolConfirmationRef) (*plannerExecutor, NBAgentPlannerToolAction) {
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", approvedWritePerson, []string{approvalTestAccount})
	request := NBAgentRequest{
		AccountId:      approvalTestAccount,
		ConversationId: "conversation-1",
		MessageId:      "message-1",
		AgentId:        uuid.NewString(),
		QueryConfig: toolcore.NBQueryConfig{
			ToolConfirmations:    map[string]string{"kubectl_execute": "yes"},
			ToolConfirmationRefs: refs,
		},
	}
	executor := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{tool}}, request, 3)
	executor.toolCallbackHandler = nil
	return executor, NBAgentPlannerToolAction{ToolID: "w-1", Tool: "kubectl_execute", ToolInput: `{"command":"kubectl scale deploy/x --replicas=0 -n demo"}`}
}

// A person's approved write reaches the recorder once, with the approval it was given under, before
// it runs; the tool runs once and its outcome is settled once.
func TestApprovedWriteIsClaimedBeforeItRunsAndSettledOnce(t *testing.T) {
	var claims []ApprovedWrite
	var settled []error
	stubRecorder(t, func(_ *security.RequestContext, w ApprovedWrite) ApprovedWriteClaim {
		claims = append(claims, w)
		return ApprovedWriteClaim{Settle: func(_ toolcore.NBToolResponse, err error) { settled = append(settled, err) }}
	})
	tool := newCountingWriteTool()
	tool.err = errors.New("exit status 1")
	refs := map[string]toolcore.ToolConfirmationRef{"kubectl_execute": {MessageId: "followup-1", ApprovedBy: approvedWritePerson}}
	executor, action := approvedKubectlExecutor(tool, refs)

	_, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), action, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, 1, tool.runs)
	require.Len(t, claims, 1)
	assert.Equal(t, "kubectl_execute", claims[0].ConfirmationKey)
	assert.Equal(t, toolcore.ToolConfirmationRef{MessageId: "followup-1", ApprovedBy: approvedWritePerson}, claims[0].Ref)
	assert.Equal(t, "kubectl_execute", claims[0].Tool)
	assert.Equal(t, approvalTestAccount, claims[0].AccountId)
	assert.Equal(t, "conversation-1", claims[0].ConversationId)
	assert.Contains(t, claims[0].Input, "kubectl scale deploy/x")
	require.Len(t, settled, 1)
	assert.EqualError(t, settled[0], "exit status 1", "the settle sees the tool's own error")
}

// A refused claim never runs the tool; its refusal is what the model reads.
func TestRefusedApprovedWriteDoesNotRun(t *testing.T) {
	stubRecorder(t, func(*security.RequestContext, ApprovedWrite) ApprovedWriteClaim {
		return ApprovedWriteClaim{Refusal: "This change already ran under this approval, so it was not run again."}
	})
	tool := newCountingWriteTool()
	executor, action := approvedKubectlExecutor(tool, map[string]toolcore.ToolConfirmationRef{"kubectl_execute": {MessageId: "followup-1"}})
	step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), action, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Zero(t, tool.runs)
	assert.Equal(t, ToolStatusFailure, step.Status)
	assert.Contains(t, step.Observation, "already ran under this approval")
}

// reaskDao is the conversation store a re-asked approval writes its follow-up to: the agent already
// has an active follow-up, which is updated in place.
type reaskDao struct {
	IConversationDao
	followup uuid.UUID
	updated  []FollowupRequest
}

func (d *reaskDao) ListConversationAgents(_, agentId string) ([]ConversationAgent, error) {
	return []ConversationAgent{{ID: uuid.MustParse(agentId), FollowupMessageID: d.followup}}, nil
}

func (d *reaskDao) GetConversationMessage(_, _, _ string) (ConversationMessage, error) {
	return ConversationMessage{ID: d.followup, Status: ConversationStatusWaiting}, nil
}

func (d *reaskDao) UpdateConversationMessageFollowupConfig(_ string, request FollowupRequest) error {
	d.updated = append(d.updated, request)
	return nil
}

// An approval the recorder cannot use (another write already used it) is asked for again, with the
// same key and the alert's card, and the tool does not run.
func TestApprovedWriteWhoseApprovalIsUsedIsAskedAgain(t *testing.T) {
	dao := &reaskDao{followup: uuid.New()}
	previousDao := GetConversationDao()
	SetConversationDao(dao)
	t.Cleanup(func() { SetConversationDao(previousDao) })
	stubRecorder(t, func(*security.RequestContext, ApprovedWrite) ApprovedWriteClaim {
		return ApprovedWriteClaim{Reask: true}
	})
	previousCard := ApprovalCardQuestion
	ApprovalCardQuestion = func(_ *security.RequestContext, w ApprovedWrite) string {
		assert.Empty(t, w.Ref.MessageId, "the card is built before anything is approved")
		return "Approve this change?\nCommand - kubectl scale deploy/x --replicas=0 -n demo"
	}
	t.Cleanup(func() { ApprovalCardQuestion = previousCard })

	tool := newCountingWriteTool()
	executor, action := approvedKubectlExecutor(tool, map[string]toolcore.ToolConfirmationRef{"kubectl_execute": {MessageId: "followup-1"}})
	step, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), action, "")
	require.NoError(t, err)
	assert.Zero(t, tool.runs)
	require.NotNil(t, finish)
	assert.Equal(t, ConversationStatusWaiting, finish.Status)
	assert.Equal(t, "kubectl_execute", finish.Followup.ConfirmationKey)
	assert.Equal(t, "Approve this change?\nCommand - kubectl scale deploy/x --replicas=0 -n demo", finish.Followup.Question)
	assert.Equal(t, ToolStatusWaiting, step.Status)
}

// Without a recorder (or for a call no approval covers) the tool runs as before.
func TestCallWithoutApprovalMarkRunsAsBefore(t *testing.T) {
	calls := 0
	stubRecorder(t, func(*security.RequestContext, ApprovedWrite) ApprovedWriteClaim { calls++; return ApprovedWriteClaim{} })
	tool := newCountingWriteTool()
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", approvedWritePerson, []string{approvalTestAccount})
	request := NBAgentRequest{AccountId: approvalTestAccount, UserId: "u", ConversationId: "c", MessageId: "m", AgentId: "a"}

	_, err := callNbTool(ctx, request, "agent", tool, `{"command":"kubectl get pods"}`, nil, "", "t-1", "", toolcore.ToolRequestTypeRead)
	require.NoError(t, err)
	assert.Zero(t, calls)

	// A mark for another tool is not this call's.
	marked := withApprovedWrite(ctx, &ApprovedWrite{Tool: "helm_execute"})
	_, err = callNbTool(marked, request, "agent", tool, `{"command":"kubectl get pods"}`, nil, "", "t-2", "", toolcore.ToolRequestTypeRead)
	require.NoError(t, err)
	assert.Zero(t, calls)
	assert.Equal(t, 2, tool.runs)

	// A marked call reaches the recorder once; a zero claim runs it unrecorded.
	marked = withApprovedWrite(ctx, &ApprovedWrite{Tool: "kubectl_execute"})
	_, err = callNbTool(marked, request, "agent", tool, `{"command":"kubectl scale deploy/x --replicas=0"}`, nil, "", "t-3", "", toolcore.ToolRequestTypeUpdate)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 3, tool.runs)
}

// approvalSeeingTool records the approval its call runs under.
type approvalSeeingTool struct {
	countingWriteTool
	seen     ApprovedWrite
	approved bool
}

func (a *approvalSeeingTool) Call(ctx toolcore.NbToolContext, request toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	a.seen, a.approved = CallApproval(ctx.Ctx)
	return a.countingWriteTool.Call(ctx, request)
}

// A tool that records its own decision (run_step) sees the approval its call runs under; a call
// nobody approved sees none.
func TestApprovedToolSeesItsApproval(t *testing.T) {
	stubRecorder(t, func(*security.RequestContext, ApprovedWrite) ApprovedWriteClaim { return ApprovedWriteClaim{} })
	tool := &approvalSeeingTool{countingWriteTool: *newCountingWriteTool()}
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", approvedWritePerson, []string{approvalTestAccount})
	request := NBAgentRequest{AccountId: approvalTestAccount, UserId: "u", ConversationId: "c", MessageId: "m", AgentId: "a"}

	_, err := callNbTool(ctx, request, "agent", tool, `{"command":"kubectl get pods"}`, nil, "", "t-1", "", toolcore.ToolRequestTypeRead)
	require.NoError(t, err)
	assert.False(t, tool.approved)

	approval := ApprovedWrite{Tool: "kubectl_execute", ConfirmationKey: "kubectl_execute", Ref: toolcore.ToolConfirmationRef{MessageId: "followup-1", ApprovedBy: approvedWritePerson}}
	_, err = callNbTool(withApprovedWrite(ctx, &approval), request, "agent", tool, `{"command":"kubectl scale deploy/x --replicas=0"}`, nil, "", "t-2", "", toolcore.ToolRequestTypeUpdate)
	require.NoError(t, err)
	require.True(t, tool.approved)
	assert.Equal(t, approval.Ref, tool.seen.Ref)
	assert.Equal(t, "kubectl_execute", tool.seen.ConfirmationKey)
}

// A write that lands on another account says so, so the alert does not count it as its own attempt.
func TestApprovedWriteNamesAnotherTargetAccount(t *testing.T) {
	tool := newCountingWriteTool()
	executor, action := approvedKubectlExecutor(tool, nil)
	own := executor.approvedWriteFor(tool, action, "kubectl_execute", nil, nil)
	assert.Empty(t, own.TargetAccountId)

	other := &writeTarget{Target: toolcore.ExecutionTarget{Name: "k8s-other", AccountID: "dddddddd-0000-0000-0000-000000000000"}}
	w := executor.approvedWriteFor(tool, action, "kubectl_execute@dddddddd-0000-0000-0000-000000000000", other, other)
	assert.Equal(t, "dddddddd-0000-0000-0000-000000000000", w.TargetAccountId)
	assert.Equal(t, approvalTestAccount, w.AccountId)

	same := &writeTarget{Target: toolcore.ExecutionTarget{Name: "prod-east", AccountID: approvalTestAccount}}
	assert.Empty(t, executor.approvedWriteFor(tool, action, "kubectl_execute", same, same).TargetAccountId)
}

func TestRecordConfirmationRef(t *testing.T) {
	assert.Nil(t, recordConfirmationRef(nil, "k", toolcore.ToolConfirmationRef{}), "no message, nothing recorded")
	refs := recordConfirmationRef(nil, "k", toolcore.ToolConfirmationRef{MessageId: "m1", ApprovedBy: "u"})
	assert.Equal(t, map[string]toolcore.ToolConfirmationRef{"k": {MessageId: "m1", ApprovedBy: "u"}}, refs)
	refs = recordConfirmationRef(refs, "k", toolcore.ToolConfirmationRef{MessageId: "m2", ApprovedBy: "u"})
	assert.Equal(t, "m2", refs["k"].MessageId, "a newer answer replaces the older one")
}

// A new message does not inherit approvals, nor which follow-up they were answered in.
func TestInheritableMessageConfigDropsConfirmationRefs(t *testing.T) {
	got := inheritableMessageConfig(toolcore.NBQueryConfig{
		ToolConfirmations:    map[string]string{"kubectl_execute": "yes"},
		ToolConfirmationRefs: map[string]toolcore.ToolConfirmationRef{"kubectl_execute": {MessageId: "m1"}},
	})
	assert.Nil(t, got.ToolConfirmations)
	assert.Nil(t, got.ToolConfirmationRefs)
}

// A write a tool-approval rule approved is recorded like a person's, in the rule's name and under the
// rule's decision for this call; nobody's follow-up answer is named.
func TestWriteApprovedByARuleIsRecorded(t *testing.T) {
	previousPolicy := config.Config.LlmServerToolApprovalPolicyEnabled
	config.Config.LlmServerToolApprovalPolicyEnabled = true
	t.Cleanup(func() { config.Config.LlmServerToolApprovalPolicyEnabled = previousPolicy })
	ruleID := uuid.New()
	previousMatch := matchToolApprovalRule
	matchToolApprovalRule = func(*plannerExecutor, NBAgentPlannerToolAction, string, string, toolApprovalTarget) *toolapproval.Match {
		return &toolapproval.Match{RuleID: ruleID, RuleName: "restart api", Revision: 3}
	}
	t.Cleanup(func() { matchToolApprovalRule = previousMatch })
	var claims []ApprovedWrite
	stubRecorder(t, func(_ *security.RequestContext, w ApprovedWrite) ApprovedWriteClaim {
		claims = append(claims, w)
		return ApprovedWriteClaim{}
	})
	tool := newCountingWriteTool()
	executor, action := approvedKubectlExecutor(tool, nil)
	executor.agentRequest.QueryConfig.ToolConfirmations = nil

	_, finish, err := executor.doAction(getNameToTool([]toolcore.NBTool{tool}), action, "")
	require.NoError(t, err)
	assert.Nil(t, finish)
	assert.Equal(t, 1, tool.runs)
	require.Len(t, claims, 1)
	assert.Equal(t, &RuleApproval{RuleID: ruleID.String(), RuleName: "restart api", Revision: 3, ToolCallID: "w-1"}, claims[0].Rule)
	assert.Empty(t, claims[0].Ref.MessageId)
	assert.Equal(t, "kubectl_execute", claims[0].ConfirmationKey)
}
