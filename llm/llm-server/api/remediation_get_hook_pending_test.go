package api

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// Fixtures shaped like the stored follow-ups: message_config as the follow-up writer stores
// it, message_context the request that raised it (a Slack-started one carries reply_ref).
var (
	webConfirmation = waitingConfirmation{
		ConversationId: "c1",
		MessageId:      "11111111-1111-1111-1111-111111111111",
		AgentId:        "22222222-2222-2222-2222-222222222222",
		CreatedAt:      time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC),
		Config:         `{"confirmationKey":"kubectl_execute","followupOptions":["yes","no"],"followupType":"tool_confirmation","question":"Tool(kubectl_execute) is trying to update resources on Kubernetes cluster **prod-east**. Do you want to continue?\nCommand - {\"command\":\"kubectl scale deployment checkout -n demo --replicas=0\"}","target":{"provider":"kubernetes","kind":"Kubernetes cluster","name":"prod-east"},"toolId":"kubectl_execute-1","toolName":"kubectl_execute"}`,
		Context:        `{"query":"scale it down","account_id":"acct","conversation_id":"c1"}`,
	}
	slackConfirmation = waitingConfirmation{
		ConversationId: "c1",
		MessageId:      "33333333-3333-3333-3333-333333333333",
		AgentId:        "44444444-4444-4444-4444-444444444444",
		CreatedAt:      time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC),
		Config:         `{"confirmationKey":"github_execute","followupOptions":["yes","no"],"followupType":"tool_confirmation","question":"Approve this change on Kubernetes cluster **prod-east** (gh)?\nNot one of the plan's options.\nCommand - gh workflow run \"Image CI\" --repo example/app --ref main","toolId":"delegate_agent-1","toolName":"github_execute"}`,
		Context:        `{"query":"run the image build","account_id":"acct","reply_ref":"C0EXAMPLE-1792433577.889749"}`,
	}
	notAConfirmation = waitingConfirmation{
		MessageId: "55555555-5555-5555-5555-555555555555",
		Config:    `{"followupType":"tool_config","question":"Which cluster?","toolName":"kubectl_execute"}`,
	}
)

func TestPendingApprovalsOf(t *testing.T) {
	got := pendingApprovalsOf([]waitingConfirmation{webConfirmation, slackConfirmation, notAConfirmation})
	require.Len(t, got, 2)
	assert.Equal(t, pendingApproval{
		ConversationId: "c1", MessageId: webConfirmation.MessageId, AgentId: webConfirmation.AgentId, Tool: "kubectl",
		Command: "kubectl scale deployment checkout -n demo --replicas=0", CreatedAt: webConfirmation.CreatedAt, Origin: ApprovalOriginWeb,
	}, got[0])
	assert.Equal(t, pendingApproval{
		ConversationId: "c1", MessageId: slackConfirmation.MessageId, AgentId: slackConfirmation.AgentId, Tool: "gh",
		Command: `gh workflow run "Image CI" --repo example/app --ref main`, CreatedAt: slackConfirmation.CreatedAt, Origin: ApprovalOriginSlack,
	}, got[1])
}

// The hook reads the alert's own conversation (event-<fingerprint>, or event-<id> without one) and its
// approvals reach the read as C7's pending_approvals.
func TestPendingApprovalsInTheRemediationRead(t *testing.T) {
	stubLoopOn(t, true)
	orig := waitingConfirmations
	t.Cleanup(func() { waitingConfirmations = orig })
	var sessions []string
	waitingConfirmations = func(_ *security.RequestContext, accountId, session, _ string) ([]waitingConfirmation, error) {
		assert.Equal(t, "acct", accountId)
		sessions = append(sessions, session)
		return []waitingConfirmation{webConfirmation, slackConfirmation}, nil
	}

	raw, err := json.Marshal(remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, RemediationPlan{}))
	require.NoError(t, err)
	var body struct {
		Loop struct {
			PendingApprovals []map[string]any `json:"pending_approvals"`
		} `json:"loop"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Loop.PendingApprovals, 2)
	first := body.Loop.PendingApprovals[0]
	assert.Equal(t, map[string]any{
		"conversation_id": "c1", "message_id": webConfirmation.MessageId, "agent_id": webConfirmation.AgentId, "tool": "kubectl",
		"command": "kubectl scale deployment checkout -n demo --replicas=0", "created_at": "2026-10-22T08:00:00Z", "origin": "web",
	}, first)
	assert.Equal(t, "slack", body.Loop.PendingApprovals[1]["origin"])
	assert.NotContains(t, body.Loop.PendingApprovals[1], "thread_url", "no workspace is known, so no link")

	remediationGetBody(nil, "acct", "evt-2", &events.EventInfo{}, "", nil, RemediationPlan{})
	assert.Equal(t, []string{"event-fp", "event-evt-2"}, sessions)
}

// A Slack-started approval links to its thread only when the workspace is known from a permalink the
// request carries; the reply reference alone names no workspace.
func TestApprovalOrigin(t *testing.T) {
	origin, link := approvalOrigin(`{"query":"x"}`)
	assert.Equal(t, ApprovalOriginWeb, origin)
	assert.Empty(t, link)

	origin, link = approvalOrigin(`{"reply_ref":"C0EXAMPLE-1792433577.889749","channel_context_refs":{"channel_id":"C0EXAMPLE","messages":[{"id":"1","permalink":null}]}}`)
	assert.Equal(t, ApprovalOriginSlack, origin)
	assert.Empty(t, link)

	origin, link = approvalOrigin(`{"reply_ref":"C0EXAMPLE-1792433577.889749","channel_context_refs":{"messages":[{"permalink":"https://example.slack.com/archives/C0OTHER/p1792433500000100"}]}}`)
	assert.Equal(t, ApprovalOriginSlack, origin)
	assert.Equal(t, "https://example.slack.com/archives/C0EXAMPLE/p1792433577889749", link)

	_, link = approvalOrigin(`{"reply_ref":"not a ref","channel_context_refs":{"messages":[{"permalink":"https://example.slack.com/archives/C0OTHER/p1"}]}}`)
	assert.Empty(t, link)
	_, link = approvalOrigin(`{"reply_ref":"C0EXAMPLE-1.2","channel_context_refs":{"messages":[{"permalink":"https://example.com/archives/C0OTHER/p1"}]}}`)
	assert.Empty(t, link, "not a Slack host")
}

// A read that fails is reported as unread, never as "none waiting"; an empty read is read.
func TestPendingApprovalsReadFailureIsNotNoneWaiting(t *testing.T) {
	orig := waitingConfirmations
	t.Cleanup(func() { waitingConfirmations = orig })
	in := remediationGetInput{AccountId: "acct", EventId: "evt", Fingerprint: "fp"}

	waitingConfirmations = func(*security.RequestContext, string, string, string) ([]waitingConfirmation, error) {
		return nil, errors.New("context canceled")
	}
	approvals, read := pendingApprovals(nil, in)
	assert.False(t, read)
	assert.Nil(t, approvals)

	waitingConfirmations = func(*security.RequestContext, string, string, string) ([]waitingConfirmation, error) { return nil, nil }
	approvals, read = pendingApprovals(nil, in)
	assert.True(t, read)
	assert.Empty(t, approvals)

	waitingConfirmations = func(*security.RequestContext, string, string, string) ([]waitingConfirmation, error) {
		return []waitingConfirmation{webConfirmation}, nil
	}
	approvals, read = pendingApprovals(nil, in)
	assert.True(t, read)
	assert.Len(t, approvals, 1)
}

// A duplicate's approvals are asked in its main event's conversation, even when the main event's
// fingerprint differs; both reads look there.
func TestApprovalsOfADuplicateAreReadFromTheMainEventsConversation(t *testing.T) {
	assert.Equal(t, "event-fp", planConversationSession(remediationGetInput{EventId: "evt", Fingerprint: "fp", PlanEventId: "evt", PlanFingerprint: "fp"}))
	assert.Equal(t, "event-fp", planConversationSession(remediationGetInput{EventId: "evt", Fingerprint: "fp"}))
	assert.Equal(t, "event-evt", planConversationSession(remediationGetInput{EventId: "evt"}))
	dup := remediationGetInput{AccountId: "acct", EventId: "dup", Fingerprint: "dup-fp", PlanEventId: "main", PlanFingerprint: "main-fp"}
	assert.Equal(t, "event-main-fp", planConversationSession(dup))
	assert.Equal(t, "event-main", planConversationSession(remediationGetInput{EventId: "dup", Fingerprint: "dup-fp", PlanEventId: "main"}))

	origWaiting, origDenied := waitingConfirmations, deniedConfirmations
	t.Cleanup(func() { waitingConfirmations, deniedConfirmations = origWaiting, origDenied })
	var asked []string
	waitingConfirmations = func(_ *security.RequestContext, _, session, planEventId string) ([]waitingConfirmation, error) {
		asked = append(asked, session+" "+planEventId)
		return nil, nil
	}
	deniedConfirmations = func(_ *security.RequestContext, _, session, planEventId string) ([]deniedConfirmation, error) {
		asked = append(asked, session+" "+planEventId)
		return nil, nil
	}
	pendingApprovals(nil, dup)
	deniedApprovals(nil, dup)
	assert.Equal(t, []string{"event-main-fp main", "event-main-fp main"}, asked)
}

func TestQuestionCommand(t *testing.T) {
	assert.Equal(t, "kubectl get pods", questionCommand(`Tool(x) is trying to update. Command - {"command":"kubectl get pods"}`))
	assert.Equal(t, "SELECT 1", questionCommand(`Tool(x) is trying to update. Command - {"query":"SELECT 1"}`))
	assert.Equal(t, "helm rollback api 3", questionCommand("Approve this change?\nCommand - helm rollback api 3"))
	assert.Equal(t, "Run the automation with ID wf-1?", questionCommand("Run the automation with ID wf-1?"))
}
