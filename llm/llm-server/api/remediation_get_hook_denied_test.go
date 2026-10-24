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

// Fixtures shaped like the stored rows: the follow-up's message_config as the follow-up writer
// stores it, and the paused turn's message_config with the answer's tool_confirmation_refs.
const deniedKey = "run_step:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef:1:apply"

func deniedRow(id, response, key, turnConfig string) deniedConfirmation {
	return deniedConfirmation{
		MessageId: id,
		Response:  response,
		DeniedAt:  time.Date(2026, 10, 23, 8, 54, 0, 0, time.UTC),
		Config: `{"confirmationKey":"` + key + `","followupOptions":["yes","no"],"followupType":"tool_confirmation",` +
			`"question":"Approve this change on Kubernetes cluster **prod-east** (apply)?\nOption 1 of the plan: Scale up\nCommand - kubectl scale deploy/api --replicas=3 -n demo",` +
			`"target":{"provider":"kubernetes","kind":"Kubernetes cluster","name":"prod-east"},"toolId":"run_step-1","toolName":"run_step"}`,
		TurnConfig: turnConfig,
	}
}

func turnWithRef(key, messageId, approver string) string {
	return `{"original_user_query":"no","tool_confirmations":{"` + key + `":"no"},"tool_confirmation_refs":{"` + key +
		`":{"message_id":"` + messageId + `","approved_by":"` + approver + `"}}}`
}

func TestDeniedApprovalsOf(t *testing.T) {
	const id = "55555555-5555-5555-5555-555555555555"
	got := deniedApprovalsOf("acct", "evt", []deniedConfirmation{deniedRow(id, "no", deniedKey, turnWithRef(deniedKey, id, "person-1"))})
	require.Len(t, got, 1)
	assert.Equal(t, deniedApproval{
		MessageId:   id,
		Tool:        "kubectl",
		Command:     "kubectl scale deploy/api --replicas=3 -n demo",
		Option:      1,
		PlanVersion: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StepKey:     events.ApprovalKey("acct", "evt", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "0:apply", "kubectl scale deploy/api --replicas=3 -n demo"),
		DeniedAt:    time.Date(2026, 10, 23, 8, 54, 0, 0, time.UTC),
		DeniedBy:    "person-1",
	}, got[0])

	raw, err := json.Marshal(got[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"denied_at":"2026-10-23T08:54:00Z"`)
}

// Only a denial counts; denied_by only when the answer recorded for this very follow-up names it.
func TestDeniedApprovalsFilter(t *testing.T) {
	rows := []deniedConfirmation{
		deniedRow("m-yes", "yes", deniedKey, ""),
		deniedRow("m-approved", "approved", deniedKey, ""),
		deniedRow("m-deny", "Deny", deniedKey, turnWithRef(deniedKey, "m-later", "person-2")),
		deniedRow("m-no", " NO ", deniedKey, "not json"),
		deniedRow("m-kubectl", "no", "kubectl_execute", ""),
	}
	got := deniedApprovalsOf("acct", "evt", rows)
	require.Len(t, got, 2)
	assert.Equal(t, "m-deny", got[0].MessageId)
	assert.Empty(t, got[0].DeniedBy, "the recorded answer is another follow-up's")
	assert.Equal(t, "m-no", got[1].MessageId)
	assert.Empty(t, got[1].DeniedBy)
}

// Who denied is named from their display name; a name that cannot be read is left out, and the
// denials are still returned.
func TestDeniedByName(t *testing.T) {
	const person, unknown = "66666666-6666-6666-6666-666666666666", "11111111-2222-3333-4444-555555555555"
	origDenied, origNames := deniedConfirmations, userDisplayNames
	t.Cleanup(func() { deniedConfirmations, userDisplayNames = origDenied, origNames })
	deniedConfirmations = func(*security.RequestContext, string, string, string) ([]deniedConfirmation, error) {
		return []deniedConfirmation{
			deniedRow("m-1", "no", deniedKey, turnWithRef(deniedKey, "m-1", person)),
			deniedRow("m-2", "no", deniedKey, turnWithRef(deniedKey, "m-2", unknown)),
			deniedRow("m-3", "no", deniedKey, turnWithRef(deniedKey, "m-3", "not-a-uuid")),
			deniedRow("m-4", "no", deniedKey, ""),
		}, nil
	}
	var asked [][]string
	userDisplayNames = func(_ *security.RequestContext, ids []string) (map[string]string, error) {
		asked = append(asked, ids)
		return map[string]string{person: "Jane Doe"}, nil
	}
	in := remediationGetInput{AccountId: "acct", EventId: "evt"}
	denials, read := deniedApprovals(nil, in)
	require.True(t, read)
	require.Len(t, denials, 4)
	assert.Equal(t, "Jane Doe", denials[0].DeniedByName)
	assert.Empty(t, denials[1].DeniedByName, "no such person")
	assert.Equal(t, unknown, denials[1].DeniedBy)
	assert.Empty(t, denials[2].DeniedByName)
	assert.Empty(t, denials[3].DeniedByName)
	assert.Equal(t, [][]string{{person, unknown}}, asked, "only ids are looked up, once")

	raw, err := json.Marshal(denials[1])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "denied_by_name")

	userDisplayNames = func(*security.RequestContext, []string) (map[string]string, error) { return nil, errors.New("db down") }
	denials, read = deniedApprovals(nil, in)
	assert.True(t, read, "the denials themselves were read")
	require.Len(t, denials, 4)
	assert.Empty(t, denials[0].DeniedByName)
	assert.Equal(t, person, denials[0].DeniedBy)
}

func TestRunStepConfirmationKey(t *testing.T) {
	pv, option, slot, ok := runStepConfirmationKey("run_step:pv-5:2:rollback")
	assert.True(t, ok)
	assert.Equal(t, "pv-5", pv)
	assert.Equal(t, 2, option)
	assert.Equal(t, "rollback", slot)

	_, _, _, ok = runStepConfirmationKey("kubectl_execute")
	assert.False(t, ok)
	_, _, _, ok = runStepConfirmationKey("run_step")
	assert.False(t, ok, "the legacy per-tool key names no step")
	_, option, _, ok = runStepConfirmationKey("run_step:pv-5:x:apply")
	assert.True(t, ok)
	assert.Zero(t, option)
}

// A read that fails is reported as unread, never as "none denied", and reaches the read's loop.
func TestDeniedApprovalsUnread(t *testing.T) {
	stubLoopOn(t, true)
	origDenied, origWaiting := deniedConfirmations, waitingConfirmations
	t.Cleanup(func() { deniedConfirmations, waitingConfirmations = origDenied, origWaiting })
	waitingConfirmations = func(*security.RequestContext, string, string, string) ([]waitingConfirmation, error) { return nil, nil }

	deniedConfirmations = func(*security.RequestContext, string, string, string) ([]deniedConfirmation, error) {
		return nil, errors.New("context canceled")
	}
	denials, read := deniedApprovals(nil, remediationGetInput{AccountId: "acct", EventId: "evt"})
	assert.False(t, read)
	assert.Nil(t, denials)
	resp, ok := remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, RemediationPlan{}).(remediationGetResponse)
	require.True(t, ok)
	assert.True(t, resp.Loop.DeniedApprovalsUnread)
	assert.False(t, resp.Loop.PendingApprovalsUnread)

	var sessions []string
	deniedConfirmations = func(_ *security.RequestContext, _, session, _ string) ([]deniedConfirmation, error) {
		sessions = append(sessions, session)
		return []deniedConfirmation{deniedRow("m-1", "no", deniedKey, "")}, nil
	}
	resp, ok = remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, RemediationPlan{}).(remediationGetResponse)
	require.True(t, ok)
	assert.False(t, resp.Loop.DeniedApprovalsUnread)
	require.Len(t, resp.Loop.DeniedApprovals, 1)
	assert.Equal(t, []string{"event-fp"}, sessions)
}
