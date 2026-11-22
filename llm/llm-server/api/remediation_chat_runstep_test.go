package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	toolcore "nudgebee/llm/tools/core"
)

// run_step in an alert's chat, approved by a person: its decision is the C1 run of that approval,
// keyed <confirmation key>:<follow-up message id>, with who approved it, the CLI and the 1-based
// option, and the step's own key beside it so the step is still decided once from any surface.
func TestRunStepApprovalInAlertChatIsRecordedAsC1(t *testing.T) {
	stubLoopOn(t, true)
	stored := storedPlan{Version: "pv-5", RowId: "row-1"}
	approval := agentcore.ApprovedWrite{
		AccountId: "acct", Tool: toolcore.ToolRunStep, ConfirmationKey: "run_step:pv-5:1:apply",
		Ref: toolcore.ToolConfirmationRef{MessageId: "followup-1", ApprovedBy: "person-1"},
	}
	call := toolcore.NbToolContext{AccountId: "acct", Ctx: agentcore.WithCallApproval(testCtx(), approval)}
	request := RemediationExecuteRequest{AccountId: "acct", EventId: "evt-1", PlanVersion: "pv-5", Source: chatSource,
		Slot: RemediationSlotExecute, Command: "kubectl rollout undo deploy/api -n demo"}
	request.ChatApproval = chatStepApprovalOf(call, "person-1")
	require.NotNil(t, request.ChatApproval)

	d := stepDecision(request, "person-1", stored, "0:apply", request.Command, request.Command, events.RunStatusInProgress, "Running",
		map[string]any{"command": request.Command, "slot": RemediationSlotExecute})
	assert.Equal(t, "run_step:pv-5:1:apply:followup-1", d.ApprovalKey)
	assert.Equal(t, events.ApprovalKey("acct", "evt-1", "pv-5", "0:apply", request.Command), d.StepKey)
	assert.Equal(t, "row-1", d.PlanRowId, "a step is still bound to the plan the person saw")
	assert.Equal(t, map[string]any{
		"command": request.Command, "slot": "execute", "step": "0:apply", "plan_version": "pv-5", "source": "chat",
		"approved_by": "person-1", "tool": "kubectl", "option": 1,
	}, d.Data)

	// A rollback of step 2 is option 2, slot rollback.
	d = stepDecision(request, "person-1", stored, "1:rollback", "helm rollback api 3", "helm rollback api 3", events.RunStatusInProgress, "Running",
		map[string]any{"slot": RemediationSlotRollback})
	assert.Equal(t, 2, d.Data["option"])
	assert.Equal(t, "helm", d.Data["tool"])

	// The panel, Slack, or run_step with the loop off: the step's own key, as before.
	request.ChatApproval = nil
	d = stepDecision(request, "person-1", stored, "0:apply", request.Command, request.Command, events.RunStatusInProgress, "Running", map[string]any{})
	assert.Equal(t, events.ApprovalKey("acct", "evt-1", "pv-5", "0:apply", request.Command), d.ApprovalKey)
	assert.Empty(t, d.StepKey)
	assert.NotContains(t, d.Data, "approved_by")
	assert.NotContains(t, d.Data, "option")

	stubLoopOn(t, false)
	assert.Nil(t, chatStepApprovalOf(call, "person-1"), "loop off")
	stubLoopOn(t, true)
	assert.Nil(t, chatStepApprovalOf(toolcore.NbToolContext{AccountId: "acct", Ctx: testCtx()}, "person-1"), "no approval on the call")
}

// run_step records its own decision, so the recorder lets it run; an approval it cannot be recorded
// under is asked for again, in an alert's chat with the loop on only.
func TestRecorderLeavesRunStepToItsOwnRecord(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1"}
	stubChatWrites(t, true, store)
	w := approvedKubectl("alert-conversation")
	w.Tool, w.ConfirmationKey = toolcore.ToolRunStep, "run_step:pv-5:1:apply"
	w.Input = `{"plan_version":"pv-5","step":1,"action":"apply","command":"kubectl rollout undo deploy/api -n demo"}`
	assert.Equal(t, agentcore.ApprovedWriteClaim{}, recordApprovedChatWrite(testCtx(), w))
	assert.Empty(t, store.claims, "one writer: run_step's own claim")

	w.Ref = toolcore.ToolConfirmationRef{}
	assert.True(t, recordApprovedChatWrite(testCtx(), w).Reask)
	w.ConversationId = "rca-conversation"
	assert.Equal(t, agentcore.ApprovedWriteClaim{}, recordApprovedChatWrite(testCtx(), w))
}

func TestStepOption(t *testing.T) {
	assert.Equal(t, 1, stepOption("0:apply"))
	assert.Equal(t, 3, stepOption("2:rollback"))
	assert.Equal(t, 0, stepOption(""))
	assert.Equal(t, 0, stepOption("x:apply"))
}
