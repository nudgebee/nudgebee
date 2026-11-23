package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/events"
)

// The re-check an investigation newer than the plan asks for: at once with nothing pending; parked behind a
// waiting approval; run when the approval is answered without a run; dropped when the approved step ran.

// recheckFixture has a plan written an hour ago, an investigation completed since, and the alert's chat.
type recheckFixture struct {
	*planningFixture
	chat    string
	waiting string
}

func newRecheckFixture(t *testing.T, approvalWaiting bool) *recheckFixture {
	t.Helper()
	f := &recheckFixture{planningFixture: newPlanningFixture(t), chat: uuid.NewString(), waiting: uuid.NewString()}
	f.storePlan(time.Hour)
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', '{}', 'INV: the image tag is wrong', 'COMPLETED', $2, 'agg', 'investigation')`, f.event, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO llm_conversations VALUES ($1, 'event-fp-1', $2)`, f.chat, f.account)
	require.NoError(t, err)
	status := "COMPLETED"
	if approvalWaiting {
		status = "WAITING"
	}
	_, err = f.db.Exec(`INSERT INTO llm_conversation_messages (id, conversation_id, status) VALUES ($1, $2, $3)`, f.waiting, f.chat, status)
	require.NoError(t, err)
	return f
}

func (f *recheckFixture) answer(t *testing.T) {
	t.Helper()
	_, err := f.db.Exec(`UPDATE llm_conversation_messages SET status = 'COMPLETED' WHERE id = $1`, f.waiting)
	require.NoError(t, err)
}

func (f *recheckFixture) askAfterInvestigation(t *testing.T) events.PlanningRequest {
	t.Helper()
	row, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	require.Empty(t, refusal, "the plan predates the investigation: a re-check is asked")
	return row
}

// Rule 1: nothing pending and nothing running: the re-check runs at once.
func TestRecheckRunsAtOnceWithNothingPending(t *testing.T) {
	f := newRecheckFixture(t, false)
	f.askAfterInvestigation(t)
	require.NoError(t, runDuePlannings())
	assert.Len(t, f.queries, 1, "the turn started")
	assert.Equal(t, events.PlanningRunning, f.latest().State)
}

// Rule 2: parked while the approval waits (and never given up on); denied, it runs.
func TestRecheckParkedBehindAnApprovalThenRunsWhenDenied(t *testing.T) {
	f := newRecheckFixture(t, true)
	f.askAfterInvestigation(t)
	require.NoError(t, runDuePlannings())
	assert.Equal(t, events.PlanningParked, f.latest().State)
	swap(t, &planningGiveUp, time.Duration(0))
	require.NoError(t, runDuePlannings())
	assert.Equal(t, events.PlanningParked, f.latest().State, "parked does not give up")
	assert.Empty(t, f.queries)

	f.answer(t) // denied: no run row
	swap(t, &planningGiveUp, time.Hour)
	require.NoError(t, runDuePlannings())
	got := f.latest()
	assert.Equal(t, events.PlanningRunning, got.State, "the parked re-check runs")
	assert.Equal(t, planTurnAuto, got.Kind)
	assert.Len(t, f.queries, 1)
}

// Rule 2: approved and the step ran: the parked re-check is dropped (ran); the follow-up owns what comes next.
func TestParkedRecheckDroppedWhenTheApprovedStepRan(t *testing.T) {
	f := newRecheckFixture(t, true)
	f.askAfterInvestigation(t)
	require.NoError(t, runDuePlannings())
	require.Equal(t, events.PlanningParked, f.latest().State)

	_, err := f.db.Exec(`INSERT INTO event_resolution (event_id, type, type_reference_id, resolver_type, resolver_id, data, status, created_at)
		VALUES ($1, 'CommandExecution', 'kubectl set resources deploy/api -n shop --limits=memory=1Gi', 'User', 'user-1', '{"source":"chat"}', 'Success', LOCALTIMESTAMP + interval '1 second')`, f.event)
	require.NoError(t, err)
	f.answer(t)
	require.NoError(t, runDuePlannings())
	got := f.latest()
	assert.Equal(t, events.PlanningDropped, got.State)
	assert.Equal(t, "ran", got.Reason)
	assert.False(t, got.Open())
	assert.Empty(t, f.queries, "no turn")
}

// A person's request takes over a parked automatic re-check.
func TestPersonRequestTakesOverAParkedRecheck(t *testing.T) {
	f := newRecheckFixture(t, true)
	parked := f.askAfterInvestigation(t)
	require.NoError(t, runDuePlannings())
	require.Equal(t, events.PlanningParked, f.latest().State)
	row, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnReplan, By: "user-1", Note: "not a restart"})
	require.NoError(t, err)
	require.Empty(t, refusal)
	assert.Equal(t, parked.Id, row.Id)
	assert.Equal(t, planTurnReplan, row.Kind)
	assert.Equal(t, events.PlanningQueued, row.State)
	assert.Equal(t, "not a restart", row.Note)
}

// The read: the investigation newer than the plan, and the parked re-check; a parked request is not "preparing".
func TestReadShowsInvestigationAfterPlanAndParkedRecheck(t *testing.T) {
	f := newRecheckFixture(t, true)
	f.askAfterInvestigation(t)
	require.NoError(t, runDuePlannings())
	in := remediationGetInput{AccountId: f.account, EventId: f.event, PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: readyPlan()}
	facts, err := loadPlanFacts(f.ctx(), in)
	require.NoError(t, err)
	loop := &remediationLoopState{}
	fillPlanLoop(loop, in, facts)
	assert.Equal(t, PlanStatusReady, loop.PlanStatus, "the plan still shows")
	assert.True(t, loop.ParkedRecheck)
	require.NotNil(t, loop.InvestigationAfterPlan)
	assert.True(t, loop.InvestigationAfterPlan.At.After(*facts.Event.PlanAt))
	raw, err := json.Marshal(loop)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"parked_recheck":true`)
	assert.Contains(t, string(raw), `"investigation_after_plan":{"at":`)

	// With the loop on a newer investigation does not void the plan for the chat's cards and decisions.
	stored, ok := loadSavedPlan(f.ctx(), f.account, f.event)
	require.True(t, ok)
	assert.True(t, stored.Stale, "it predates the investigation")
	_, _, current := loadSlackPlan(f.ctx(), f.account, f.event)
	assert.True(t, current, "loop on: still the plan")
	stubLoopOn(t, false)
	_, _, current = loadSlackPlan(f.ctx(), f.account, f.event)
	assert.False(t, current, "loop off: as before")
}

// a: a planning turn's message and the investigation summary carry the note the app shows instead of a person.
func TestPlanningTurnsCarryASystemNote(t *testing.T) {
	assert.Equal(t, "Nubi was asked to prepare fixes after the investigation", planningSystemNote(events.PlanningRequest{Kind: planTurnAuto}))
	assert.Equal(t, "Nubi was asked to re-check the fix", planningSystemNote(events.PlanningRequest{Kind: planTurnRecheck}))
	assert.Equal(t, "A person asked Nubi for other fixes: too risky; not a restart",
		planningSystemNote(events.PlanningRequest{Kind: planTurnReplan, Reasons: []string{"too risky"}, Note: "not a restart"}))
	assert.Equal(t, "A person asked Nubi for other fixes", planningSystemNote(events.PlanningRequest{Kind: planTurnReplan}))
	raw, err := json.Marshal(planningTurnConfig(events.PlanningRequest{Kind: planTurnAsk}))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"system_note":"Nubi was asked to prepare fixes"`)
	assert.Contains(t, string(raw), `"remediation_plan_turn":"ask"`)
	assert.NotEmpty(t, followupConfig.SystemNote, "the follow-up check is a NudgeBee turn too")
}

// b: the heading names the event the investigation was run for, not the plan's when it was reused.
func TestEvidenceHeadingNamesTheInvestigatedEvent(t *testing.T) {
	f := newPlanningFixture(t)
	other := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, starts_at, created_at) VALUES ($1, $2, 'fp-1', LOCALTIMESTAMP - interval '1 day', LOCALTIMESTAMP - interval '1 day')`, other, f.account)
	require.NoError(t, err)
	var analysis string
	require.NoError(t, f.db.Get(&analysis, `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', '{}', 'INV of the other firing', 'COMPLETED', $2, 'agg', 'investigation') RETURNING id::text`, other, f.account))
	_, err = f.db.Exec(`INSERT INTO event_analysis_mapping VALUES ($1, $2, 'investigation')`, f.event, analysis)
	require.NoError(t, err)

	source, at, ok := investigationSource(f.ctx(), f.account, f.event)
	require.True(t, ok)
	assert.Equal(t, other, source, "the event the investigation was run for")
	require.NotNil(t, at)
	query, reason := planningQuery(f.ctx(), events.PlanningRequest{EventId: f.event, AccountId: f.account, Alert: "fp-1", Kind: planTurnAuto})
	require.Empty(t, reason)
	assert.Contains(t, query, "(event "+other+")")
	assert.NotContains(t, query, "(event "+f.event+")")
}
