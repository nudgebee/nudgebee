package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

func stubRunRecord(t *testing.T, calls []ToolCallTrace, err error) *[]string {
	t.Helper()
	var asked []string
	origList, origName := listRunToolCalls, automationRunName
	listRunToolCalls = func(messageId string) ([]ToolCallTrace, error) {
		asked = append(asked, messageId)
		return calls, err
	}
	automationRunName = func(_, toolName, _ string) string {
		switch toolName {
		case "automation_restart_pods", "workflow_trigger":
			return `"Restart Pods"`
		}
		return `"` + toolName + `"`
	}
	t.Cleanup(func() { listRunToolCalls, automationRunName = origList, origName })
	return &asked
}

func receiptCtx() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", nil)
}

// A reply that shows a COMPLETED run with an execution id no tool returned:
// the trigger was refused because the automation was paused, and the record
// above the reply must say so whatever the reply says.
func TestRunReceiptShowsARefusedRunAsNotRun(t *testing.T) {
	stubRunRecord(t, []ToolCallTrace{{
		ToolName: "workflow_trigger", Parameters: `{"id":"wf-1"}`, Status: "error",
		Response: `NO RUN STARTED: "Restart Pods" was not run: it is not ACTIVE (it is paused or disabled), and the assistant may only run ACTIVE automations. No execution was created, so there is no execution ID, status or result to report. Do not retry it or change the automation to get around this; tell the user it did not run and why.`,
	}}, nil)

	response := []string{"```json\n{\"execution_id\": \"01a0e7f5\", \"status\": \"COMPLETED\"}\n```"}
	receipt := applyRunReceipt(receiptCtx(), "acct", "msg-1", ConversationSourceUserInvestigation, response)

	assert.Equal(t, runReceiptHeading+"\n"+
		`- "Restart Pods" was not run: it is not ACTIVE (it is paused or disabled), and the assistant may only run ACTIVE automations.`, receipt)
	assert.Equal(t, receipt+"\n\n```json\n{\"execution_id\": \"01a0e7f5\", \"status\": \"COMPLETED\"}\n```", response[0])
}

func TestRunReceiptShowsAStartedRunFromItsResult(t *testing.T) {
	stubRunRecord(t, []ToolCallTrace{{
		ToolName: "automation_restart_pods", Status: "success",
		Response: `{"workflow_id":"wf-1","execution_id":"exec-1","status":"COMPLETED","url":"https://app.example.com/automation/wf-1","result":{"ok":true}}`,
	}, {
		ToolName: "automation_restart_pods", Status: "success",
		Response: `{"workflow_id":"wf-1","execution_id":"exec-2","status":"RUNNING","note":"paused waiting for an approval; it will continue once someone approves it"}`,
	}}, nil)

	receipt := renderRunReceipt("acct", mustCalls(listRunToolCalls("msg-1")))
	assert.Equal(t, runReceiptHeading+"\n"+
		"- \"Restart Pods\" ran — execution `exec-1`, status COMPLETED. [Open the automation](https://app.example.com/automation/wf-1)\n"+
		"- \"Restart Pods\" ran — execution `exec-2`, status RUNNING (paused waiting for an approval; it will continue once someone approves it).",
		receipt)
}

// An approval nobody gave leaves the row WAITING with the approval question as
// its response. The record says the run did not happen and never shows the
// question as if it were an outcome.
func TestRunReceiptShowsAnUnansweredApprovalAsNotRun(t *testing.T) {
	stubRunRecord(t, []ToolCallTrace{{
		ToolName: "automation_restart_pods", Status: "waiting",
		Response: `Run the automation "Restart Pods"?` + "\nInputs:\n- namespace: demo",
	}}, nil)

	receipt := renderRunReceipt("acct", mustCalls(listRunToolCalls("msg-1")))
	assert.Equal(t, runReceiptHeading+"\n"+`- "Restart Pods" was not run: it was waiting for approval, which was not given.`, receipt)
	assert.NotContains(t, receipt, "Inputs:")
}

// The model usually corrects a bad call and retries. A plain failure followed
// by a run of the same automation is not worth a line; a refusal always is.
func TestRunReceiptDropsAFailureTheTurnRecoveredFrom(t *testing.T) {
	stubRunRecord(t, []ToolCallTrace{
		{ToolName: "automation_restart_pods", Status: "error", Response: "automation_restart_pods: unknown parameter(s) svc — this automation accepts: service"},
		{ToolName: "automation_restart_pods", Status: "success", Response: `{"workflow_id":"wf-1","execution_id":"exec-1","status":"COMPLETED"}`},
		{ToolName: "automation_other", Status: "error", Response: "action execution failed: failed to run automation \"Other\": failed to send request: connection refused"},
	}, nil)

	receipt := renderRunReceipt("acct", mustCalls(listRunToolCalls("msg-1")))
	assert.Equal(t, runReceiptHeading+"\n"+
		"- \"Restart Pods\" ran — execution `exec-1`, status COMPLETED.\n"+
		`- "automation_other": the run request failed (failed to run automation "Other": failed to send request: connection refused), and no run was confirmed.`,
		receipt)
}

// The same refusal retried three times is one line.
func TestRunReceiptListsARepeatedRefusalOnce(t *testing.T) {
	refused := ToolCallTrace{ToolName: "workflow_trigger", Parameters: `{"id":"wf-1"}`, Status: "error",
		Response: `NO RUN STARTED: "Restart Pods" was not run: the automation was not found. No execution was created, so there is no execution ID, status or result to report. Do not retry it or change the automation to get around this; tell the user it did not run and why.`}
	stubRunRecord(t, []ToolCallTrace{refused, refused, refused}, nil)

	receipt := renderRunReceipt("acct", mustCalls(listRunToolCalls("msg-1")))
	assert.Equal(t, runReceiptHeading+"\n"+`- "Restart Pods" was not run: the automation was not found.`, receipt)
}

func TestRunReceiptIsOnlyForChats(t *testing.T) {
	asked := stubRunRecord(t, []ToolCallTrace{{ToolName: "automation_restart_pods", Status: "success",
		Response: `{"execution_id":"exec-1","status":"COMPLETED"}`}}, nil)

	for _, source := range []ConversationSource{ConversationSourceInvestigation, ConversationSourceAutomation, ConversationSourceWorkflowBuilder, ""} {
		response := []string{"answer"}
		assert.Empty(t, applyRunReceipt(receiptCtx(), "acct", "msg-1", source, response), source)
		assert.Equal(t, "answer", response[0], source)
	}
	assert.Empty(t, *asked, "a non-chat turn must not even read the record")

	for _, source := range []ConversationSource{ConversationSourceUserInvestigation, ConversationSourceUserInvestigationCLI, ConversationSourceInstantNotification} {
		response := []string{"answer"}
		assert.NotEmpty(t, applyRunReceipt(receiptCtx(), "acct", "msg-1", source, response), source)
	}
}

func TestRunReceiptLeavesATurnWithoutRunsAlone(t *testing.T) {
	stubRunRecord(t, nil, nil)
	response := []string{"answer"}
	assert.Empty(t, applyRunReceipt(receiptCtx(), "acct", "msg-1", ConversationSourceUserInvestigation, response))
	assert.Equal(t, "answer", response[0])
}

// Applied on the normal save and again by the resume path's save: the second
// must not add another record.
func TestRunReceiptIsAppliedOnce(t *testing.T) {
	stubRunRecord(t, []ToolCallTrace{{ToolName: "automation_restart_pods", Status: "success",
		Response: `{"execution_id":"exec-1","status":"COMPLETED"}`}}, nil)
	response := []string{"answer"}
	first := applyRunReceipt(receiptCtx(), "acct", "msg-1", ConversationSourceUserInvestigation, response)
	require.NotEmpty(t, first)
	withRecord := response[0]
	assert.Empty(t, applyRunReceipt(receiptCtx(), "acct", "msg-1", ConversationSourceUserInvestigation, response))
	assert.Equal(t, withRecord, response[0])
}

func TestRunReceiptKeepsTheAnswerWhenTheRecordCannotBeRead(t *testing.T) {
	stubRunRecord(t, nil, errors.New("db down"))
	response := []string{"answer"}
	assert.Empty(t, applyRunReceipt(receiptCtx(), "acct", "msg-1", ConversationSourceUserInvestigation, response))
	assert.Equal(t, "answer", response[0])
}

// The Slack rewrite is given the answer without the record and the record goes
// back on unchanged.
func TestWithoutRunReceiptRoundTrips(t *testing.T) {
	receipt := runReceiptHeading + "\n- \"Restart Pods\" ran — execution `exec-1`, status COMPLETED."
	joined := joinRunReceipt(receipt, "the answer")
	assert.Equal(t, "the answer", withoutRunReceipt(receipt, joined))
	assert.Equal(t, joined, withoutRunReceipt("", joined))
	assert.Equal(t, receipt, joinRunReceipt(receipt, "  "))
}

func mustCalls(calls []ToolCallTrace, err error) []ToolCallTrace {
	if err != nil {
		panic(err)
	}
	return calls
}

// automation_builder is an agent called as a tool and shares the per-automation
// prefix the query matches on. Its calls are not runs.
func TestRunReceiptSkipsAgentsThatShareThePrefix(t *testing.T) {
	toolcore.RegisterNBToolFactory("automation_builder_receipt_test", func(string) (toolcore.NBTool, error) { return nil, nil })
	stubRunRecord(t, []ToolCallTrace{{ToolName: "automation_builder_receipt_test", Status: "waiting",
		Response: "Tool exited successfully with no output."}}, nil)

	response := []string{"answer"}
	assert.Empty(t, applyRunReceipt(receiptCtx(), "acct", "msg-1", ConversationSourceUserInvestigation, response))
	assert.Equal(t, "answer", response[0])
}

type sourceFakeDao struct {
	IConversationDao
	source    *ConversationSource
	persisted string
}

func (f *sourceFakeDao) GetConversation(string) (Conversation, error) {
	return Conversation{Source: f.source}, nil
}

func (f *sourceFakeDao) UpdateConversationMessage(_, response string, _ ConversationStatus) error {
	f.persisted = response
	return nil
}

// An approval answered from the UI arrives as a chat request; the record
// follows the conversation's stored source, not the request's.
func TestPersistFinalMessageUsesTheStoredSourceForTheRunRecord(t *testing.T) {
	stubRunRecord(t, []ToolCallTrace{{ToolName: "automation_restart_pods", Status: "success",
		Response: `{"execution_id":"exec-1","status":"COMPLETED"}`}}, nil)
	req := NBAgentRequest{ConversationId: "conv-1", MessageId: "msg-1", AccountId: "acct",
		ConversationSource: ConversationSourceUserInvestigation}
	original := GetConversationDao()
	t.Cleanup(func() { SetConversationDao(original) })

	investigation := ConversationSourceInvestigation
	fake := &sourceFakeDao{source: &investigation}
	SetConversationDao(fake)
	persistFinalMessage(receiptCtx(), req, NBAgentResponse{Response: []string{"answer"}})
	assert.Equal(t, "answer", fake.persisted, "a machine-read conversation gets no record")

	chat := ConversationSourceInstantNotification
	fake = &sourceFakeDao{source: &chat}
	SetConversationDao(fake)
	resp := NBAgentResponse{Response: []string{"answer"}}
	persistFinalMessage(receiptCtx(), NBAgentRequest{ConversationId: "conv-1", MessageId: "msg-1", AccountId: "acct"}, resp)
	assert.Contains(t, fake.persisted, runReceiptHeading)
	assert.Contains(t, resp.Response[0], runReceiptHeading, "the returned response carries the record too")
}
