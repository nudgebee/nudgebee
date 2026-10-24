package api

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentcore "nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

func TestChatWriteOf(t *testing.T) {
	cases := []struct {
		name, tool, input string
		recorded          bool
		label, command    string
		exitCodesReported bool
	}{
		{"kubectl", tools.ToolExecuteKubectlCommand, `{"command":"kubectl rollout undo deploy/api -n demo"}`, true, "kubectl", "kubectl rollout undo deploy/api -n demo", true},
		{"helm", tools.ToolExecuteHelmCommand, `{"command":"helm rollback api 3 -n demo"}`, true, "helm", "helm rollback api 3 -n demo", true},
		{"aws", tools.ToolExecuteAwsCliCommand, `{"command":"aws ecs update-service --cluster c --service s --desired-count 2"}`, true, "aws", "aws ecs update-service --cluster c --service s --desired-count 2", true},
		{"gsutil through gcloud", tools.ToolExecuteGcpCliCommand, `{"command":"gsutil rm gs://b/o"}`, true, "gsutil", "gsutil rm gs://b/o", true},
		{"shell running kubectl", toolcore.ToolExecuteShellCommand, `{"command":"kubectl scale deploy/x --replicas=0"}`, true, "kubectl", "kubectl scale deploy/x --replicas=0", true},
		{"shell", toolcore.ToolExecuteShellCommand, `{"command":"systemctl restart nginx"}`, true, "shell", "systemctl restart nginx", true},
		{"postgres", tools.ToolExecutePostgresQuery, `{"query":"UPDATE jobs SET state='queued' WHERE id=1"}`, true, "postgres", "UPDATE jobs SET state='queued' WHERE id=1", true},
		{"gh workflow run", tools.ToolExecuteGithubCliCommand, `{"command":"gh workflow run \"Image CI\" --repo example/app --ref main"}`, true, "gh", `gh workflow run "Image CI" --repo example/app --ref main`, true},
		{"gh pr merge", tools.ToolExecuteGithubCliCommand, `{"command":"gh pr merge 12 --squash --repo example/app"}`, true, "gh", "gh pr merge 12 --squash --repo example/app", true},
		{"git push", tools.ToolExecuteGithubCliCommand, `{"command":"git push origin fix"}`, true, "git", "git push origin fix", true},
		{"gh api merge", tools.ToolExecuteGithubCliCommand, `{"command":"gh api -X PUT repos/example/app/pulls/12/merge"}`, true, "gh", "gh api -X PUT repos/example/app/pulls/12/merge", true},
		{"gh pr comment is a note", tools.ToolExecuteGithubCliCommand, `{"command":"gh pr comment 12 --body done"}`, false, "", "", false},
		{"gh issue create is a ticket", tools.ToolExecuteGithubCliCommand, `{"command":"gh issue create --title x"}`, false, "", "", false},
		{"gh api comment is a note", tools.ToolExecuteGithubCliCommand, `{"command":"gh api -X POST repos/example/app/issues/1/comments -f body=x"}`, false, "", "", false},
		{"gh read", tools.ToolExecuteGithubCliCommand, `{"command":"gh pr view 12"}`, false, "", "", false},
		{"saved automation", "automation_restart_crashlooping_pods", `{"namespace": "demo", "reason":"r"}`, true, "automation", `automation_restart_crashlooping_pods {"namespace":"demo","reason":"r"}`, false},
		{"workflow trigger", tools.ToolWorkflowTrigger, `{"id":"wf-1"}`, true, "automation", `workflow_trigger {"id":"wf-1"}`, false},
		{"creating an automation", tools.ToolWorkflowCreate, `{"name":"x"}`, false, "", "", false},
		{"ticket", "ticket_master_v2", `{"command":"create a ticket"}`, false, "", "", false},
		{"run_step records its own row", toolcore.ToolRunStep, `{"plan_version":"v","step":1,"action":"apply","command":"kubectl scale deploy/x --replicas=1"}`, false, "", "", false},
		{"memory", "update_notebook", `{"command":"note"}`, false, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			write, ok := chatWriteOf(tc.tool, tc.input)
			assert.Equal(t, tc.recorded, ok)
			if !tc.recorded {
				return
			}
			assert.Equal(t, tc.label, write.Tool)
			assert.Equal(t, tc.command, write.Command)
			assert.Equal(t, tc.exitCodesReported, write.ExitCodes)
		})
	}
}

// fakeChatWriteStore records what recording a chat write asks of the store.
type fakeChatWriteStore struct {
	eventId  string
	eventErr error
	stored   *storedPlan
	conflict *events.DecisionConflict
	claimErr error
	claims   []events.RemediationDecision
	settles  []fakeSettle
}

type fakeSettle struct {
	runId, status, message string
	patch                  map[string]any
}

func (f *fakeChatWriteStore) alertEvent(*security.RequestContext, string, string) (string, error) {
	return f.eventId, f.eventErr
}

func (f *fakeChatWriteStore) plan(*security.RequestContext, string, string) (storedPlan, bool) {
	if f.stored == nil {
		return storedPlan{}, false
	}
	return *f.stored, true
}

func (f *fakeChatWriteStore) claim(_ *security.RequestContext, d events.RemediationDecision) (string, *events.DecisionConflict, error) {
	f.claims = append(f.claims, d)
	if f.claimErr != nil || f.conflict != nil {
		return "", f.conflict, f.claimErr
	}
	return "run-1", nil, nil
}

func (f *fakeChatWriteStore) settle(_ *security.RequestContext, runId, status string, patch map[string]any, message string) (bool, error) {
	f.settles = append(f.settles, fakeSettle{runId: runId, status: status, patch: patch, message: message})
	return true, nil
}

const alertSession = "event-fp-1"

// stubChatWrites makes conversation "alert-conversation" an alert's conversation, every other one
// not (an RCA chat, a Nubi chat), and installs store.
func stubChatWrites(t *testing.T, loopOn bool, store *fakeChatWriteStore) {
	t.Helper()
	stubLoopOn(t, loopOn)
	origSession, origStore := alertSessionOfConversation, chatWriteStore
	alertSessionOfConversation = func(conversationId string) string {
		if conversationId == "alert-conversation" {
			return alertSession
		}
		return ""
	}
	chatWriteStore = store
	t.Cleanup(func() { alertSessionOfConversation, chatWriteStore = origSession, origStore })
}

func approvedKubectl(conversationId string) agentcore.ApprovedWrite {
	return agentcore.ApprovedWrite{
		AccountId:       "acct",
		ConversationId:  conversationId,
		Tool:            tools.ToolExecuteKubectlCommand,
		Input:           `{"command":"kubectl rollout undo deploy/api -n demo"}`,
		ConfirmationKey: tools.ToolExecuteKubectlCommand,
		Ref:             toolcore.ToolConfirmationRef{MessageId: "followup-1", ApprovedBy: "person-1"},
	}
}

func testCtx() *security.RequestContext { return security.NewRequestContextForSuperAdmin() }

// An approved kubectl write in an alert's chat is claimed as C1 before it runs, and its outcome is
// settled on that row.
func TestApprovedChatWriteRecordsC1(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1", stored: &storedPlan{Version: "pv-1", RowId: "row-1", Plan: RemediationPlan{Actions: []RemediationAction{
		{Title: "Scale up", ExecuteCommand: "kubectl scale deploy/api --replicas=3 -n demo"},
		{Title: "Roll back", ExecuteCommand: "kubectl set image deploy/api api=v2 -n demo", RollbackCommand: "kubectl rollout undo deploy/api -n demo"},
	}}}}
	stubChatWrites(t, true, store)

	claim := recordApprovedChatWrite(testCtx(), approvedKubectl("alert-conversation"))
	require.Empty(t, claim.Refusal)
	require.False(t, claim.Reask)
	require.NotNil(t, claim.Settle)
	require.Len(t, store.claims, 1)
	d := store.claims[0]
	assert.Equal(t, "acct", d.AccountId)
	assert.Equal(t, "evt-1", d.EventId)
	assert.Equal(t, "person-1", d.UserId)
	assert.Equal(t, "kubectl rollout undo deploy/api -n demo", d.Ref, "type_reference_id is the command")
	assert.Equal(t, "kubectl_execute:followup-1", d.ApprovalKey)
	assert.Equal(t, events.RunStatusInProgress, d.Status)
	assert.Empty(t, d.PlanRowId, "a chat write is bound to its approval, not to a plan")
	assert.Empty(t, d.PlanVersion)
	assert.Equal(t, map[string]any{
		"source": "chat", "tool": "kubectl", "command": "kubectl rollout undo deploy/api -n demo", "approved_by": "person-1",
		"plan_version": "pv-1", "option": 2, "slot": "rollback", "step": "1:rollback",
	}, d.Data)
	// The plan step's own key goes beside the approval's, so the step does not run again from the
	// panel or Slack under another approval.
	assert.Equal(t, events.ApprovalKey("acct", "evt-1", "pv-1", "1:rollback", "kubectl rollout undo deploy/api -n demo"), d.StepKey)

	claim.Settle(toolcore.NBToolResponse{Data: `{"stdout":"deployment.apps/api rolled back"}`, Status: toolcore.NBToolResponseStatusSuccess}, nil)
	require.Len(t, store.settles, 1)
	s := store.settles[0]
	assert.Equal(t, "run-1", s.runId)
	assert.Equal(t, events.RunStatusSuccess, s.status)
	assert.Equal(t, 0, s.patch["exit_code"])
	assert.Equal(t, true, s.patch["exit_code_reported"])
	assert.Contains(t, s.patch["output"], "rolled back")
}

// A command that is not one of the plan's options is recorded without plan_version or option.
func TestApprovedChatWriteOutsideThePlan(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1"}
	stubChatWrites(t, true, store)
	w := approvedKubectl("alert-conversation")
	w.Tool, w.ConfirmationKey = tools.ToolExecuteGithubCliCommand, tools.ToolExecuteGithubCliCommand
	w.Input = `{"command":"gh workflow run \"Image CI\" --repo example/app --ref main"}`
	claim := recordApprovedChatWrite(testCtx(), w)
	require.NotNil(t, claim.Settle)
	require.Len(t, store.claims, 1)
	assert.Equal(t, "github_execute:followup-1", store.claims[0].ApprovalKey)
	assert.Equal(t, map[string]any{"source": "chat", "tool": "gh", "command": `gh workflow run "Image CI" --repo example/app --ref main`,
		"approved_by": "person-1", "slot": "execute"}, store.claims[0].Data)
	assert.Empty(t, store.claims[0].StepKey, "not a plan step")
}

// An execute command of the plan run from chat decides its step (0-based "<i>:apply").
func TestApprovedChatWriteOfAPlanStepCarriesItsStepKey(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1", stored: &storedPlan{Version: "pv-1", Plan: RemediationPlan{Actions: []RemediationAction{
		{Title: "Scale up", ExecuteCommand: "kubectl scale deploy/api --replicas=3 -n demo"},
	}}}}
	stubChatWrites(t, true, store)
	w := approvedKubectl("alert-conversation")
	w.Input = `{"command":"kubectl scale deploy/api --replicas=3 -n demo"}`
	require.NotNil(t, recordApprovedChatWrite(testCtx(), w).Settle)
	require.Len(t, store.claims, 1)
	assert.Equal(t, "0:apply", store.claims[0].Data["step"])
	assert.Equal(t, 1, store.claims[0].Data["option"])
	assert.Equal(t, events.ApprovalKey("acct", "evt-1", "pv-1", "0:apply", "kubectl scale deploy/api --replicas=3 -n demo"), store.claims[0].StepKey)
}

// A write on another cluster or account is not an attempt on this alert: nothing is recorded on it,
// and it runs as before.
func TestApprovedChatWriteOnAnotherAccountIsNotThisAlertsAttempt(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1"}
	stubChatWrites(t, true, store)
	w := approvedKubectl("alert-conversation")
	w.Input = `{"command":"kubectl --context k8s-other rollout undo deploy/api -n demo"}`
	w.TargetAccountId = "other-acct"
	assert.Equal(t, agentcore.ApprovedWriteClaim{}, recordApprovedChatWrite(testCtx(), w))
	assert.Empty(t, store.claims)

	w.TargetAccountId = "acct"
	assert.NotNil(t, recordApprovedChatWrite(testCtx(), w).Settle, "naming the alert's own account is this alert's")
	assert.Len(t, store.claims, 1)
}

// Flag off, an RCA chat, a non-alert chat, or a write that is not an infra change: nothing is
// recorded and the call runs as before.
func TestChatWritesNotRecorded(t *testing.T) {
	ticket := approvedKubectl("alert-conversation")
	ticket.Tool, ticket.Input = "ticket_master_v2", `{"command":"open a ticket"}`
	for name, tc := range map[string]struct {
		loopOn bool
		write  agentcore.ApprovedWrite
	}{
		"loop off":       {false, approvedKubectl("alert-conversation")},
		"not an alert's": {true, approvedKubectl("rca-conversation")},
		"a ticket":       {true, ticket},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeChatWriteStore{eventId: "evt-1"}
			stubChatWrites(t, tc.loopOn, store)
			assert.Equal(t, agentcore.ApprovedWriteClaim{}, recordApprovedChatWrite(testCtx(), tc.write))
			assert.Empty(t, store.claims)
		})
	}
}

// An approval the write cannot be recorded under is asked for again; a record that cannot be written,
// or a refused claim, never runs the write.
func TestApprovedChatWriteRefusals(t *testing.T) {
	t.Run("unknown approval", func(t *testing.T) {
		store := &fakeChatWriteStore{eventId: "evt-1"}
		stubChatWrites(t, true, store)
		w := approvedKubectl("alert-conversation")
		w.Ref = toolcore.ToolConfirmationRef{}
		assert.True(t, recordApprovedChatWrite(testCtx(), w).Reask)
		assert.Empty(t, store.claims)
	})
	t.Run("approval used by another command", func(t *testing.T) {
		store := &fakeChatWriteStore{eventId: "evt-1", conflict: &events.DecisionConflict{Reason: events.DecisionAlreadyDecided, Ref: "kubectl delete pod api-1"}}
		stubChatWrites(t, true, store)
		claim := recordApprovedChatWrite(testCtx(), approvedKubectl("alert-conversation"))
		assert.True(t, claim.Reask)
		assert.Nil(t, claim.Settle)
	})
	t.Run("same command already ran under this approval", func(t *testing.T) {
		store := &fakeChatWriteStore{eventId: "evt-1", conflict: &events.DecisionConflict{Reason: events.DecisionAlreadyDecided, Ref: "kubectl rollout undo deploy/api -n demo", Status: events.RunStatusSuccess}}
		stubChatWrites(t, true, store)
		claim := recordApprovedChatWrite(testCtx(), approvedKubectl("alert-conversation"))
		assert.False(t, claim.Reask)
		assert.Contains(t, claim.Refusal, "already run or decided for this alert")
		assert.Nil(t, claim.Settle)
	})
	t.Run("unknown outcome", func(t *testing.T) {
		store := &fakeChatWriteStore{eventId: "evt-1", conflict: &events.DecisionConflict{Reason: events.DecisionUnknownOutcome}}
		stubChatWrites(t, true, store)
		claim := recordApprovedChatWrite(testCtx(), approvedKubectl("alert-conversation"))
		assert.Contains(t, claim.Refusal, "unknown outcome")
		assert.Nil(t, claim.Settle)
	})
	t.Run("record not written", func(t *testing.T) {
		store := &fakeChatWriteStore{eventId: "evt-1", claimErr: errors.New("db down")}
		stubChatWrites(t, true, store)
		claim := recordApprovedChatWrite(testCtx(), approvedKubectl("alert-conversation"))
		assert.Equal(t, chatWriteNotRecorded, claim.Refusal)
	})
	t.Run("alert not found", func(t *testing.T) {
		store := &fakeChatWriteStore{eventErr: errors.New("no rows")}
		stubChatWrites(t, true, store)
		assert.Equal(t, chatWriteNotRecorded, recordApprovedChatWrite(testCtx(), approvedKubectl("alert-conversation")).Refusal)
		assert.Empty(t, store.claims)
	})
}

func TestChatWriteOutcome(t *testing.T) {
	cli := chatWrite{Tool: "kubectl", Command: "kubectl scale deploy/x --replicas=0", ExitCodes: true}
	automation := chatWrite{Tool: "automation", Command: "automation_x {}"}
	cases := []struct {
		name     string
		write    chatWrite
		resp     toolcore.NBToolResponse
		err      error
		status   string
		exitCode any
		reported bool
		notSent  bool
	}{
		{"exit 0", cli, toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusSuccess}, nil, events.RunStatusSuccess, 0, true, false},
		{"exit 2", cli, toolcore.NBToolResponse{}, &workspace.CommandFailure{Status: "failed", StdErr: "exit status 2"}, events.RunStatusFailed, 2, true, false},
		{"relay exit", cli, toolcore.NBToolResponse{}, &tools.RelayCommandExitError{ExitCode: 1}, events.RunStatusFailed, 1, true, false},
		{"refused before running", cli, toolcore.NBToolResponse{}, &workspace.CommandFailure{Status: "failed", StdErr: "security validation failed"}, events.RunStatusFailed, nil, false, true},
		{"no result", cli, toolcore.NBToolResponse{}, errors.New("context deadline exceeded"), events.RunStatusUnknown, nil, false, false},
		{"tool failure", cli, toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusError, Data: "forbidden"}, nil, events.RunStatusFailed, nil, false, false},
		{"automation ran", automation, toolcore.NBToolResponse{Status: toolcore.NBToolResponseStatusSuccess}, nil, events.RunStatusSuccess, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, patch, message := chatWriteOutcome(tc.write, tc.resp, tc.err)
			assert.Equal(t, tc.status, status)
			assert.Equal(t, tc.exitCode, patch["exit_code"])
			assert.Equal(t, tc.reported, patch["exit_code_reported"])
			assert.Equal(t, tc.notSent, patch["not_sent"] == true)
			assert.NotEmpty(t, message)
		})
	}
}

// In an alert's chat with the loop on, the approval card names the option, the account and the exact
// command; elsewhere it keeps the default card.
func TestChatWriteCardQuestion(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1", stored: &storedPlan{Version: "pv-1", Plan: RemediationPlan{Actions: []RemediationAction{
		{Title: "Scale up", ExecuteCommand: "kubectl scale deploy/api --replicas=3 -n demo"},
		{Title: "Roll back", ExecuteCommand: "kubectl set image deploy/api api=v2 -n demo", RollbackCommand: "kubectl rollout undo deploy/api -n demo"},
	}}}}
	stubChatWrites(t, true, store)
	w := approvedKubectl("alert-conversation")
	w.Ref, w.Destination = toolcore.ToolConfirmationRef{}, "Kubernetes cluster **prod-east**"
	assert.Equal(t, "Approve this change on Kubernetes cluster **prod-east** (kubectl)?\nOption 2 of the plan: Roll back (undo)\nCommand - kubectl rollout undo deploy/api -n demo",
		chatWriteCardQuestion(testCtx(), w))

	w.Input = `{"command":"kubectl delete pod api-1 -n demo"}`
	assert.Equal(t, "Approve this change on Kubernetes cluster **prod-east** (kubectl)?\nNot one of the plan's options.\nCommand - kubectl delete pod api-1 -n demo",
		chatWriteCardQuestion(testCtx(), w))

	step := w
	step.Tool, step.Input = toolcore.ToolRunStep, `{"plan_version":"pv-1","step":1,"action":"apply","command":"kubectl scale deploy/api --replicas=3 -n demo"}`
	assert.Equal(t, "Approve this change on Kubernetes cluster **prod-east** (apply)?\nOption 1 of the plan: Scale up\nCommand - kubectl scale deploy/api --replicas=3 -n demo",
		chatWriteCardQuestion(testCtx(), step))

	assert.Empty(t, chatWriteCardQuestion(testCtx(), approvedKubectl("rca-conversation")), "not an alert's conversation")
	stubLoopOn(t, false)
	assert.Empty(t, chatWriteCardQuestion(testCtx(), w), "loop off")
}

// A write a tool-approval rule approved is recorded like a person-approved one, in the rule's name and under the
// rule's decision for that call. It is an attempt, not a decided plan step: no person chose the step, so a plan
// command run under a rule carries no step key.
func TestApprovedChatWriteUnderARuleIsRecordedInTheRulesName(t *testing.T) {
	store := &fakeChatWriteStore{eventId: "evt-1", stored: &storedPlan{Version: "pv-1", Plan: RemediationPlan{Actions: []RemediationAction{
		{Title: "Roll back", ExecuteCommand: "kubectl rollout undo deploy/api -n demo"},
	}}}}
	stubChatWrites(t, true, store)
	w := approvedKubectl("alert-conversation")
	w.Ref = toolcore.ToolConfirmationRef{}
	w.MessageId, w.AgentId = "message-1", "agent-1"
	w.Rule = &agentcore.RuleApproval{RuleID: "rule-1", RuleName: "roll back api", Revision: 2, ToolCallID: "call-1"}

	claim := recordApprovedChatWrite(testCtx(), w)
	require.False(t, claim.Reask, "a rule's approval needs no follow-up")
	require.Empty(t, claim.Refusal)
	require.NotNil(t, claim.Settle)
	require.Len(t, store.claims, 1)
	d := store.claims[0]
	assert.Equal(t, "kubectl_execute:rule:rule-1:2:message-1:agent-1:call-1", d.ApprovalKey)
	assert.Equal(t, "rule:rule-1", d.UserId)
	assert.Empty(t, d.StepKey, "not a plan step")
	assert.Equal(t, `approval rule "roll back api"`, d.Data["approved_by"])
	assert.Equal(t, map[string]any{"rule_id": "rule-1", "rule_name": "roll back api", "rule_revision": 2}, d.Data["approved_by_rule"])
	assert.NotContains(t, d.Data, "step")
	assert.NotContains(t, d.Data, "option")
	assert.Equal(t, "execute", d.Data["slot"])

	// run_step under a rule records its own step, as it always did: nothing is asked again.
	step := w
	step.Tool, step.Input = toolcore.ToolRunStep, `{"plan_version":"pv-1","step":1,"action":"apply","command":"kubectl rollout undo deploy/api -n demo"}`
	assert.Equal(t, agentcore.ApprovedWriteClaim{}, recordApprovedChatWrite(testCtx(), step))
}
