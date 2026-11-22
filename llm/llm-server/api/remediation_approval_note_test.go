package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

// Where an answer was made is read from the request: a Slack answer carries its thread's reply reference.
func TestAnsweredFromOf(t *testing.T) {
	assert.Equal(t, ApprovalOriginSlack, answeredFromOf("C0123ABC-1700000000.000100"))
	assert.Equal(t, ApprovalOriginWeb, answeredFromOf(""))
	assert.Equal(t, ApprovalOriginWeb, answeredFromOf("   "))
}

func TestCleanApprovalNote(t *testing.T) {
	assert.Equal(t, "after the freeze", cleanApprovalNote("  after the freeze \n"))
	long := strings.Repeat("é", maxApprovalNote+20)
	assert.Equal(t, maxApprovalNote, len([]rune(cleanApprovalNote(long))), "whole characters")
}

func TestWithApprovalAnswer(t *testing.T) {
	data := map[string]any{}
	withApprovalAnswer(data, approvalAnswer{})
	assert.Empty(t, data, "nothing recorded: nothing added")
	withApprovalAnswer(data, approvalAnswer{Note: " ok after 14:00 ", AnsweredFrom: ApprovalOriginSlack})
	assert.Equal(t, map[string]any{"approval_note": "ok after 14:00", "approved_from": ApprovalOriginSlack}, data)
}

// The note and surface of an answer are recorded only for an approval of a change in an alert's conversation with
// the loop on, while it still waits; a denial of a plan step asks for the re-check; a note that cannot be kept stops
// the answer, a lost surface does not.
func TestRecordApprovalAnswer(t *testing.T) {
	const account = "acct-note"
	stepKey := "run_step:v-hash:1:apply"
	alert := waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "event-fp1", Confirmation: true, Waiting: true,
		FollowupMessageId: "fu-1", ConfirmationKey: stepKey}
	answer := func(query, note, replyRef string) ConversationApiRequest {
		return ConversationApiRequest{AccountId: account, ConversationId: "conv-1", AgentId: "agent-1", Query: query, Reason: note, ReplyRef: replyRef}
	}
	for _, tc := range []struct {
		name     string
		request  ConversationApiRequest
		approval waitingApproval
		loop     bool
		writeErr error
		written  *approvalAnswer
		recheck  bool
		code     int
	}{
		{name: "approve with a note on the tab", request: answer("yes", " after the freeze ", ""), approval: alert, loop: true,
			written: &approvalAnswer{Note: "after the freeze", AnsweredFrom: ApprovalOriginWeb}},
		{name: "deny in Slack, no note", request: answer("no", "", "C1-1700000000.000100"), approval: alert, loop: true,
			written: &approvalAnswer{AnsweredFrom: ApprovalOriginSlack}, recheck: true},
		{name: "deny a chat write: no re-check", request: answer("no", "", ""), loop: true,
			approval: waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "event-fp1", Confirmation: true, Waiting: true, FollowupMessageId: "fu-1", ConfirmationKey: "kubectl_execute:x"},
			written:  &approvalAnswer{AnsweredFrom: ApprovalOriginWeb}},
		{name: "loop off: nothing written", request: answer("yes", "note", ""), approval: alert},
		{name: "not an alert's conversation", request: answer("yes", "note", ""), loop: true,
			approval: waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "chat-1", Confirmation: true, Waiting: true, FollowupMessageId: "fu-1"}},
		{name: "a clarifying question", request: answer("yes", "note", ""), loop: true,
			approval: waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "event-fp1", Waiting: true, FollowupMessageId: "fu-1"}},
		{name: "already answered", request: answer("yes", "note", ""), loop: true,
			approval: waitingApproval{Found: true, ConversationId: "conv-1", SessionId: "event-fp1", Confirmation: true, FollowupMessageId: "fu-1"}},
		{name: "another conversation named", request: ConversationApiRequest{AccountId: account, ConversationId: "conv-2", AgentId: "agent-1", Query: "yes", Reason: "n"}, approval: alert, loop: true},
		{name: "a dismiss is not an answer", request: ConversationApiRequest{AccountId: account, ConversationId: "conv-1", AgentId: "agent-1", Resolution: "dismiss", Reason: "n"}, approval: alert, loop: true},
		{name: "a new message", request: ConversationApiRequest{AccountId: account, Query: "hello"}, approval: alert, loop: true},
		{name: "note not kept: refused", request: answer("yes", "note", ""), approval: alert, loop: true, writeErr: errors.New("db down"),
			written: &approvalAnswer{Note: "note", AnsweredFrom: ApprovalOriginWeb}, code: http.StatusServiceUnavailable},
		{name: "no note, surface not kept: goes on", request: answer("yes", "", ""), approval: alert, loop: true, writeErr: errors.New("db down"),
			written: &approvalAnswer{AnsweredFrom: ApprovalOriginWeb}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubLoopOn(t, tc.loop)
			swap(t, &approvalOfAgent, func(_ context.Context, accountId, agentId string) (waitingApproval, error) {
				return tc.approval, nil
			})
			var written *approvalAnswer
			swap(t, &writeApprovalAnswer, func(_ context.Context, accountId, messageId string, a approvalAnswer) error {
				assert.Equal(t, account, accountId)
				assert.Equal(t, "fu-1", messageId)
				written = &a
				return tc.writeErr
			})
			rechecked := false
			swap(t, &recheckAfterDenial, func(_ *security.RequestContext, accountId, session string) {
				assert.Equal(t, "event-fp1", session)
				rechecked = true
			})
			code, _ := recordApprovalAnswer(security.NewRequestContextForTenantAdminWithUser(context.Background(), "tenant", "user-1"), tc.request)
			assert.Equal(t, tc.code, code)
			assert.Equal(t, tc.written, written)
			assert.Equal(t, tc.recheck, rechecked)
		})
	}
}

// An approved step's record carries what was recorded with its approval.
func TestStepDecisionCarriesTheApprovalNote(t *testing.T) {
	d := stepDecision(RemediationExecuteRequest{AccountId: "a", EventId: "e", Source: chatSource,
		ChatApproval: &chatStepApproval{ApprovalKey: "k:m", ApprovedBy: "user-1", Answer: approvalAnswer{Note: "after the freeze", AnsweredFrom: ApprovalOriginSlack}}},
		"user-1", storedPlan{Version: "v"}, "0:apply", "kubectl rollout restart deploy/api", "ref", "InProgress", "", map[string]any{})
	assert.Equal(t, "after the freeze", d.Data["approval_note"])
	assert.Equal(t, ApprovalOriginSlack, d.Data["approved_from"])

	d = stepDecision(RemediationExecuteRequest{AccountId: "a", EventId: "e", Source: chatSource, ChatApproval: &chatStepApproval{ApprovalKey: "k:m"}},
		"user-1", storedPlan{Version: "v"}, "0:apply", "kubectl rollout restart deploy/api", "ref", "InProgress", "", map[string]any{})
	assert.NotContains(t, d.Data, "approval_note")
}

// A denial shows the note given with it; one NudgeBee closed unanswered has none.
func TestDeniedApprovalsCarryTheNote(t *testing.T) {
	const id = "55555555-5555-5555-5555-555555555555"
	row := deniedRow(id, "no", deniedKey, turnWithRef(deniedKey, id, "person-1"))
	row.Note, row.AnsweredFrom = "too risky before the freeze", ApprovalOriginSlack
	got := deniedApprovalsOf("acct", "evt", []deniedConfirmation{row})
	if assert.Len(t, got, 1) {
		assert.Equal(t, "too risky before the freeze", got[0].Note)
		assert.Equal(t, ApprovalOriginSlack, got[0].AnsweredFrom)
	}
	row.Response, row.ClosedReason = "", "lapsed"
	got = deniedApprovalsOf("acct", "evt", []deniedConfirmation{row})
	if assert.Len(t, got, 1) {
		assert.Empty(t, got[0].Note)
	}
}
