package api

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"nudgebee/llm/events"
	"nudgebee/llm/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stubLoopOn(t *testing.T, on bool) {
	t.Helper()
	orig, origRead := events.RemediationLoopOn, events.RemediationLoopRead
	events.RemediationLoopOn = func(string) bool { return on }
	events.RemediationLoopRead = func(string) (bool, error) { return on, nil }
	t.Cleanup(func() { events.RemediationLoopOn, events.RemediationLoopRead = orig, origRead })
}

func TestRemediationGetBody_LoopOffKeepsTheResponseUnchanged(t *testing.T) {
	stubLoopOn(t, false)
	plan := RemediationPlan{RootCause: "limit too low", Actions: []RemediationAction{{Title: "raise it"}}}
	got, err := json.Marshal(remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, plan))
	require.NoError(t, err)
	want, err := json.Marshal(plan)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

func TestRemediationGetBody_NoEventInfoKeepsTheResponseUnchanged(t *testing.T) {
	stubLoopOn(t, true)
	plan := RemediationPlan{RootCause: "x"}
	assert.Equal(t, plan, remediationGetBody(nil, "acct", "evt", nil, "", nil, plan))
}

func TestRemediationGetBody_LoopOnAddsLoopAndKeepsPlanFieldsAtTopLevel(t *testing.T) {
	stubLoopOn(t, true)
	plan := RemediationPlan{RootCause: "limit too low", Actions: []RemediationAction{{Title: "raise it"}}}
	raw, err := json.Marshal(remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, plan))
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, "limit too low", body["root_cause"])
	assert.Len(t, body["actions"], 1)
	loop, ok := body["loop"].(map[string]any)
	require.True(t, ok, "loop object present")
	assert.Equal(t, PlanStatusReady, loop["plan_status"])
	assert.NotContains(t, loop, "pending_approvals")
	assert.NotContains(t, loop, "primary_event_id")
}

func TestRemediationGetBody_EmptyPlanHasStatusNone(t *testing.T) {
	stubLoopOn(t, true)
	got := remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, RemediationPlan{})
	resp, ok := got.(remediationGetResponse)
	require.True(t, ok)
	assert.Equal(t, PlanStatusNone, resp.Loop.PlanStatus)
}

func TestRemediationGetBody_HooksReceiveTheEventAndFillTheirFields(t *testing.T) {
	stubLoopOn(t, true)
	origPlan, origPending, origPrimary := planStatusAndRelevance, pendingApprovals, primaryEventOf
	t.Cleanup(func() { planStatusAndRelevance, pendingApprovals, primaryEventOf = origPlan, origPending, origPrimary })

	var mu sync.Mutex // the pending-approvals hook runs alongside the others
	var seen []remediationGetInput
	see := func(in remediationGetInput) { mu.Lock(); defer mu.Unlock(); seen = append(seen, in) }
	planStatusAndRelevance = func(_ *security.RequestContext, in remediationGetInput, loop *remediationLoopState) {
		see(in)
		loop.PlanStatus = PlanStatusPreparing
		loop.Relevance = &planRelevance{AlertState: "firing", Stale: true, StaleReason: "older than 24 h"}
	}
	pendingApprovals = func(_ *security.RequestContext, in remediationGetInput) ([]pendingApproval, bool) {
		see(in)
		return []pendingApproval{{MessageId: "m1", Tool: "kubectl_execute", Command: "kubectl rollout undo deploy/x", Origin: ApprovalOriginWeb}}, true
	}
	primaryEventOf = func(_ *security.RequestContext, in remediationGetInput) string {
		see(in)
		return "main-evt"
	}
	origFp := eventFingerprintOf
	t.Cleanup(func() { eventFingerprintOf = origFp })
	eventFingerprintOf = func(_ *security.RequestContext, accountId, eventId string) string {
		if accountId == "acct" && eventId == "main-evt" {
			return "main-fp"
		}
		return ""
	}

	got := remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, RemediationPlan{})
	resp, ok := got.(remediationGetResponse)
	require.True(t, ok)
	assert.Equal(t, PlanStatusPreparing, resp.Loop.PlanStatus)
	assert.True(t, resp.Loop.Relevance.Stale)
	assert.Equal(t, "main-evt", resp.Loop.PrimaryEventId)
	assert.Equal(t, "main-fp", resp.Loop.PrimaryFingerprint)
	require.Len(t, resp.Loop.PendingApprovals, 1)
	assert.Equal(t, ApprovalOriginWeb, resp.Loop.PendingApprovals[0].Origin)
	assert.False(t, resp.Loop.PendingApprovalsUnread)
	require.Len(t, seen, 3)
	for _, in := range seen {
		assert.Equal(t, remediationGetInput{AccountId: "acct", EventId: "evt", Fingerprint: "fp", PlanEventId: "evt", PlanFingerprint: "fp", Plan: in.Plan}, in)
		assert.NotNil(t, in.Plan)
	}
}

// A failed read of the waiting approvals is reported as unread, not as "none waiting": the tab would
// otherwise drop an approval it is showing whenever the read is cut off.
func TestRemediationGetBody_PendingApprovalsUnread(t *testing.T) {
	stubLoopOn(t, true)
	origPlan, origPending, origPrimary := planStatusAndRelevance, pendingApprovals, primaryEventOf
	t.Cleanup(func() { planStatusAndRelevance, pendingApprovals, primaryEventOf = origPlan, origPending, origPrimary })
	planStatusAndRelevance = func(_ *security.RequestContext, _ remediationGetInput, loop *remediationLoopState) {
		loop.PlanStatus = PlanStatusReady
	}
	pendingApprovals = func(_ *security.RequestContext, _ remediationGetInput) ([]pendingApproval, bool) { return nil, false }
	primaryEventOf = func(_ *security.RequestContext, _ remediationGetInput) string { return "" }

	resp, ok := remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, RemediationPlan{}).(remediationGetResponse)
	require.True(t, ok)
	assert.True(t, resp.Loop.PendingApprovalsUnread)
	assert.Empty(t, resp.Loop.PendingApprovals)
}

// A read that fails is reported as a failure, never as "no plan": a loop-less empty answer makes the tab fall
// back to the one-shot panel, which plans on mount (a Deny was then followed by a re-check nobody
// asked for). Only a missing event is an empty answer.
func TestRemediationReadEventTellsAFailedReadFromAMissingEvent(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	origRead := events.RemediationLoopRead
	t.Cleanup(func() { events.RemediationLoopRead = origRead })
	events.RemediationLoopRead = func(string) (bool, error) { return true, nil }

	mock := useMetastore(t)
	expectEvent(mock)
	_, info, err := remediationReadEvent(ctx, "acc", "evt")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "fp", info.Fingerprint)

	mock.ExpectQuery(`FROM events WHERE id = \$1`).WithArgs("evt", "acc").WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}))
	_, info, err = remediationReadEvent(ctx, "acc", "evt")
	require.NoError(t, err, "a missing event is not a failed read")
	assert.Nil(t, info)

	mock.ExpectQuery(`FROM events WHERE id = \$1`).WithArgs("evt", "acc").WillReturnError(errors.New("connection reset"))
	_, _, err = remediationReadEvent(ctx, "acc", "evt")
	require.ErrorContains(t, err, "connection reset")

	// With the loop off a failed event read answers as before (no plan), not as a failure.
	events.RemediationLoopRead = func(string) (bool, error) { return false, nil }
	mock.ExpectQuery(`FROM events WHERE id = \$1`).WithArgs("evt", "acc").WillReturnError(errors.New("connection reset"))
	_, info, err = remediationReadEvent(ctx, "acc", "evt")
	require.NoError(t, err, "loop off keeps the old answer")
	assert.Nil(t, info)

	events.RemediationLoopRead = func(string) (bool, error) { return false, errors.New("feature flags unavailable") }
	_, _, err = remediationReadEvent(ctx, "acc", "evt")
	require.ErrorContains(t, err, "loop flag not read")
	require.NoError(t, mock.ExpectationsWereMet())
}
