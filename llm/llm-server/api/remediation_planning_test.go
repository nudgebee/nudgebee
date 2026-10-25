package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// realProposePlanningPlan is the store path the fixture stubs, for a test that runs it.
var realProposePlanningPlan = proposePlanningPlan

// ─── Plan status and relevance (C7) ───

func at(s string) *time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return &t
}

func readyPlan() *RemediationPlan {
	return &RemediationPlan{RootCause: "512Mi limit", Actions: []RemediationAction{{Action: "Raise limit"}}}
}

func TestFillPlanLoop_Status(t *testing.T) {
	in := remediationGetInput{AccountId: "a", EventId: "e1", Plan: readyPlan()}
	firing := &planEventFacts{Status: "FIRING", NbStatus: "OPEN", PlanAt: at("2026-10-22 10:00"), Now: *at("2026-10-22 11:00")}

	loop := &remediationLoopState{}
	fillPlanLoop(loop, in, planFacts{Event: firing})
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	require.NotNil(t, loop.Relevance)
	assert.False(t, loop.Relevance.Stale)
	assert.Equal(t, "firing", loop.Relevance.AlertState)

	// A request still open: preparing since it was asked, even over an older plan.
	asked := time.Date(2026, 10, 22, 10, 30, 0, 0, time.UTC)
	loop = &remediationLoopState{}
	fillPlanLoop(loop, in, planFacts{Event: firing, Planning: &events.PlanningRequest{State: events.PlanningQueued, RequestedAt: asked}})
	assert.Equal(t, PlanStatusPreparing, loop.PlanStatus)
	assert.Equal(t, asked, *loop.PreparingSince)

	// No plan and the last request gave up: none, with its reason.
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "e1", Plan: &RemediationPlan{}}, planFacts{Event: firing,
		Planning: &events.PlanningRequest{State: events.PlanningNone, Reason: "it could not start: the alert's conversation stayed busy for 1h0m0s"}})
	assert.Equal(t, PlanStatusNone, loop.PlanStatus)
	assert.Equal(t, "Nubi prepared no fix: it could not start: the alert's conversation stayed busy for 1h0m0s", loop.PlanStatusReason)
	require.NotNil(t, loop.Relevance, "the alert's state is read without a plan")
	assert.False(t, loop.Relevance.Stale)
	assert.Equal(t, "firing", loop.Relevance.AlertState)
	// No plan and Nubi was never asked; or the last request was dropped.
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "e1", Plan: &RemediationPlan{}}, planFacts{Event: firing, PlanEventOf: "e1"})
	assert.Equal(t, PlanStatusNone, loop.PlanStatus)
	assert.Equal(t, noPlanAskedReason, loop.PlanStatusReason)
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "e1", Plan: &RemediationPlan{}}, planFacts{Event: firing,
		Planning: &events.PlanningRequest{State: events.PlanningDropped, Reason: "a step ran since it was asked"}})
	assert.Equal(t, "Nubi prepared no fix: the request was dropped (a step ran since it was asked)", loop.PlanStatusReason)
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "e1", Plan: &RemediationPlan{}}, planFacts{Event: firing,
		Planning: &events.PlanningRequest{State: events.PlanningDropped, Reason: "ran"}})
	assert.Equal(t, "Nubi prepared no fix: the re-check was not needed, a step ran since it was asked", loop.PlanStatusReason)
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "e1", Plan: &RemediationPlan{}}, planFacts{Event: firing,
		Planning: &events.PlanningRequest{State: events.PlanningDone}})
	assert.Equal(t, "Nubi prepared no fix", loop.PlanStatusReason)

	// A plan is shown and the newest request ended with no plan: ready, with that request's reason and when it was.
	loop = &remediationLoopState{}
	fillPlanLoop(loop, in, planFacts{Event: firing, ConversationBusy: true,
		Planning: &events.PlanningRequest{State: events.PlanningNone, Kind: planTurnRecheck, RequestedAt: asked, Reason: "Nubi wrote no plan: the model was unavailable"}}) // stored before the rewording
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	assert.Equal(t, "Nubi prepared no fix: the model was unavailable", loop.PlanStatusReason)
	assert.Equal(t, asked, *loop.LastRequestAt)
	assert.Equal(t, planTurnRecheck, loop.LastRequestMode)
	assert.True(t, loop.ConversationBusy)
	// The newest request wrote the plan: no reason.
	loop = &remediationLoopState{}
	fillPlanLoop(loop, in, planFacts{Event: firing, Planning: &events.PlanningRequest{State: events.PlanningDone, Kind: planTurnAuto, RequestedAt: asked}})
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	assert.Empty(t, loop.PlanStatusReason)
	assert.Equal(t, planTurnAuto, loop.LastRequestMode)
	assert.False(t, loop.ConversationBusy)

	// A duplicate's read returns its main event's plan: status and relevance are the main event's.
	dup := remediationGetInput{AccountId: "a", EventId: "dup", PlanEventId: "main", Plan: readyPlan()}
	loop = &remediationLoopState{PrimaryEventId: "main"}
	fillPlanLoop(loop, dup, planFacts{Event: firing})
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	assert.Empty(t, loop.PlanStatusReason)
	require.NotNil(t, loop.Relevance)
	// The main event has no plan: none, with its last request's reason.
	loop = &remediationLoopState{PrimaryEventId: "main"}
	fillPlanLoop(loop, remediationGetInput{EventId: "dup", PlanEventId: "main", Plan: &RemediationPlan{}},
		planFacts{Event: firing, Planning: &events.PlanningRequest{State: events.PlanningNone, Reason: "Nubi wrote no plan"}})
	assert.Equal(t, PlanStatusNone, loop.PlanStatus)
	assert.Equal(t, "Nubi prepared no fix", loop.PlanStatusReason)
	// A duplicate of a live main event the read did not map to it never has a plan of its own.
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "dup", Plan: &RemediationPlan{}}, planFacts{Event: firing, PlanEventOf: "main"})
	assert.Equal(t, PlanStatusNone, loop.PlanStatus)
	assert.Equal(t, notOwnPlanReason, loop.PlanStatusReason)
	// A duplicate whose main event is over heads its own chain: no plan yet, because none was asked for.
	loop = &remediationLoopState{}
	fillPlanLoop(loop, remediationGetInput{EventId: "dup", Plan: &RemediationPlan{}}, planFacts{Event: &planEventFacts{NbStatus: "DUPLICATE"}, PlanEventOf: "dup"})
	assert.Equal(t, noPlanAskedReason, loop.PlanStatusReason)
}

// The alert's state is the incident's, decided by its newest firing: the plan's event, or a later one with its
// fingerprint or marked its duplicate.
func TestAlertStateCoversTheIncident(t *testing.T) {
	plan := at("2026-10-20 10:00")
	mainClosedDupFiring := planEventFacts{Status: "CLOSED", EndsAt: at("2026-10-20 09:30"), StartsAt: at("2026-10-20 08:00"), PlanAt: plan,
		Now: *at("2026-10-20 12:00"), NewestId: "dup", NewestStatus: "FIRING", NewestStartsAt: at("2026-10-20 11:00")}
	state, since := alertStateOf(mainClosedDupFiring)
	assert.Equal(t, "firing", state, "the new firing is a duplicate of the closed main event")
	assert.Equal(t, *at("2026-10-20 11:00"), *since)
	assert.False(t, planRelevanceOf(mainClosedDupFiring, planFacts{}).Stale, "firing, and the main event closed before the plan")
	assert.Nil(t, incidentClosedAt(&mainClosedDupFiring), "the planner does not call it closed either")
	id, latestAt, latest := latestFiring(mainClosedDupFiring, "main")
	assert.Equal(t, "dup", id)
	assert.Equal(t, *at("2026-10-20 11:00"), *latestAt)
	assert.Equal(t, "firing", latest)

	resolvedDupFiring := mainClosedDupFiring
	resolvedDupFiring.NbStatus = "RESOLVED"
	state, _ = alertStateOf(resolvedDupFiring)
	assert.Equal(t, "firing", state)

	// Every firing closed: closed when the newest one closed.
	allClosed := planEventFacts{Status: "CLOSED", EndsAt: at("2026-10-20 09:30"), PlanAt: plan, Now: *at("2026-10-20 12:00"),
		NewestId: "dup", NewestStatus: "CLOSED", NewestEndsAt: at("2026-10-20 11:30")}
	state, since = alertStateOf(allClosed)
	assert.Equal(t, "closed", state)
	assert.Equal(t, *at("2026-10-20 11:30"), *since, "when the newest firing closed")
	assert.Equal(t, *at("2026-10-20 11:30"), *incidentClosedAt(&allClosed))
	assert.Equal(t, "the alert closed after the plan was written", planRelevanceOf(allClosed, planFacts{}).StaleReason)
	_, _, latest = latestFiring(allClosed, "main")
	assert.Equal(t, "closed at 2026-10-20 11:30 UTC", latest)

	// No later firing: the plan's event alone.
	alone := planEventFacts{Status: "CLOSED", EndsAt: at("2026-10-20 09:30"), PlanAt: plan, Now: *at("2026-10-20 12:00")}
	assert.Equal(t, *at("2026-10-20 09:30"), *incidentClosedAt(&alone))
	assert.Nil(t, incidentClosedAt(&planEventFacts{Status: "FIRING"}))
	assert.Nil(t, incidentClosedAt(nil))
}

func TestEvidenceHeading(t *testing.T) {
	inc := planEventFacts{Status: "CLOSED", StartsAt: at("2026-10-22 09:00"), EndsAt: at("2026-10-22 09:30"),
		NewestId: "dup", NewestStatus: "FIRING", NewestStartsAt: at("2026-10-23 04:00")}
	assert.Equal(t, "## Investigation of the alert's current firing, fired 2026-10-23 04:00 UTC",
		evidenceHeading(&inc, "main", "dup", at("2026-10-23 04:00")))
	assert.Equal(t, "## Investigation of an earlier firing (event main), fired 2026-10-22 09:00 UTC\nLatest firing 2026-10-23 04:00 UTC: firing.",
		evidenceHeading(&inc, "main", "main", at("2026-10-22 09:00")))
	assert.Equal(t, "## Investigation of the alert's current firing", evidenceHeading(nil, "main", "main", nil))
}

func TestPlanRelevance_Stale(t *testing.T) {
	plan := at("2026-10-20 10:00")
	old := planEventFacts{Status: "FIRING", PlanAt: plan, Now: *at("2026-10-21 10:01")}
	rel := planRelevanceOf(old, planFacts{})
	assert.True(t, rel.Stale)
	assert.Equal(t, "written more than 24 h ago", rel.StaleReason)

	fresh := planEventFacts{Status: "FIRING", PlanAt: plan, Now: *at("2026-10-20 12:00")}
	// Firings while the problem stays present and nothing was tried: the same incident, still shown.
	rel = planRelevanceOf(fresh, planFacts{Firings: 2, NewerEventId: "e9", Runs: 1})
	assert.False(t, rel.Stale)
	assert.Equal(t, 2, rel.FiringsSincePlan)
	assert.Equal(t, 1, rel.RunsSincePlan)
	assert.Equal(t, "e9", rel.NewerEventId)
	rel = planRelevanceOf(fresh, planFacts{Firings: 2, Runs: 1, FiredAfterRun: true})
	assert.True(t, rel.Stale)
	assert.Equal(t, "the alert fired again after a step was run", rel.StaleReason)
	rel = planRelevanceOf(fresh, planFacts{Firings: 1, FiredAfterClear: true})
	assert.True(t, rel.Stale)
	assert.Equal(t, "the alert cleared and fired again since the plan", rel.StaleReason)

	closedAfter := planEventFacts{Status: "CLOSED", EndsAt: at("2026-10-20 11:00"), PlanAt: plan, Now: *at("2026-10-20 12:00")}
	rel = planRelevanceOf(closedAfter, planFacts{})
	assert.True(t, rel.Stale)
	assert.Equal(t, "closed", rel.AlertState)
	assert.Equal(t, "the alert closed after the plan was written", rel.StaleReason)

	// "Closed on its own": the plan came after the close, so the close does not make it stale.
	closedBefore := planEventFacts{Status: "CLOSED", EndsAt: at("2026-10-20 09:00"), PlanAt: plan, Now: *at("2026-10-20 12:00")}
	assert.False(t, planRelevanceOf(closedBefore, planFacts{}).Stale)

	resolved := planEventFacts{Status: "CLOSED", NbStatus: "RESOLVED", NbChangedAt: at("2026-10-20 11:30"), PlanAt: plan, Now: *at("2026-10-20 12:00")}
	rel = planRelevanceOf(resolved, planFacts{})
	assert.Equal(t, "resolved", rel.AlertState)
	assert.Equal(t, *at("2026-10-20 11:30"), *rel.AlertStateAt)
}

// The read's loop object, shaped exactly as C7 has it.
func TestRemediationGetBody_PlanStatusAndRelevanceShape(t *testing.T) {
	stubLoopOn(t, true)
	swap(t, &loadPlanFacts, func(*security.RequestContext, remediationGetInput) (planFacts, error) {
		return planFacts{Event: &planEventFacts{Status: "FIRING", StartsAt: at("2026-10-20 09:00"), PlanAt: at("2026-10-20 10:00"), Now: *at("2026-10-21 11:00")},
			Firings: 1, NewerEventId: "e2", Runs: 3}, nil
	})
	raw, err := json.Marshal(remediationGetBody(nil, "acct", "evt", &events.EventInfo{Fingerprint: "fp"}, "", nil, *readyPlan()))
	require.NoError(t, err)
	var body struct {
		Loop map[string]any `json:"loop"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, PlanStatusReady, body.Loop["plan_status"])
	rel, ok := body.Loop["relevance"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "firing", rel["alert_state"])
	assert.Equal(t, "2026-10-20T09:00:00Z", rel["alert_state_at"])
	assert.EqualValues(t, 1, rel["firings_since_plan"])
	assert.EqualValues(t, 3, rel["runs_since_plan"])
	assert.Equal(t, "e2", rel["newer_event_id"])
	assert.Equal(t, true, rel["stale"])
	assert.Equal(t, "written more than 24 h ago", rel["stale_reason"], "a firing with nothing tried and no clear is the same incident")
}

// ─── What every plan written with the loop on carries (C8, version, revision) ───

func TestStampLoopPlan(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	first := RemediationPlan{RootCause: "512Mi limit", Goal: " The pod stays up with no OOM kill for 30 minutes. ",
		CauseKey: &events.CauseKey{What: "Deployment/api", How: "OOMKilled", Against: "Memory_Limit"},
		Actions:  []RemediationAction{{Action: "Raise limit", ExecuteCommand: "kubectl set resources deploy/api --limits=memory=1Gi"}}}
	stampLoopPlan(&first, nil, "", &PlanRevision{Kind: planTurnAuto}, now)
	assert.Equal(t, 1, first.Version)
	assert.Equal(t, now, *first.GeneratedAt)
	assert.Equal(t, events.CauseKey{What: "deploy api", How: "oom", Against: "memory limit"}, *first.CauseKey)
	assert.Equal(t, "The pod stays up with no OOM kill for 30 minutes.", first.Goal)
	assert.Equal(t, planTurnAuto, first.Revision.Kind)
	assert.Empty(t, first.Revision.Changes)

	second := RemediationPlan{RootCause: "512Mi limit", Actions: []RemediationAction{
		{Action: "Raise limit", ExecuteCommand: "kubectl set resources deploy/api --limits=memory=2Gi"},
		{Action: "Fix the leak", RequiredChange: &RequiredChange{Target: "cache.go", Change: "bound the cache"}}}}
	stampLoopPlan(&second, &first, "pv-1", &PlanRevision{Kind: planTurnReplan, Reasons: []string{"too small"}, Note: "go to 2Gi"}, now.Add(time.Hour))
	assert.Equal(t, 2, second.Version)
	assert.Equal(t, "pv-1", second.Revision.PreviousVersion)
	assert.Equal(t, []string{"Changed: Raise limit", "Added: Fix the leak"}, second.Revision.Changes)
	assert.Equal(t, []string{"too small"}, second.Revision.Reasons)
	assert.Equal(t, events.CauseKey{}, *second.CauseKey, "no key: stored empty, not comparable")

	// The stored shape: top-level generated_at, version, goal and cause_key.
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored))
	assert.Equal(t, "2026-10-22T10:00:00Z", stored["generated_at"])
	assert.EqualValues(t, 1, stored["version"])
	assert.Equal(t, map[string]any{"what": "deploy api", "how": "oom", "against": "memory limit"}, stored["cause_key"])
	assert.NotEmpty(t, stored["goal"])

	// A plan from before versions were kept counts as the first.
	third := RemediationPlan{}
	stampLoopPlan(&third, &RemediationPlan{RootCause: "x"}, "pv-old", nil, now)
	assert.Equal(t, 2, third.Version)
}

func TestPlanChanges(t *testing.T) {
	prev := RemediationPlan{RootCause: "a", Actions: []RemediationAction{{Action: "Restart"}, {Action: "Raise limit", ExecuteCommand: "x"}}}
	assert.Equal(t, []string{"No change to the cause or the steps"}, planChanges(prev, prev))
	next := RemediationPlan{RootCause: "b", Actions: []RemediationAction{{Action: "raise limit ", ExecuteCommand: "x"}}}
	assert.Equal(t, []string{"Cause: b", "Removed: Restart"}, planChanges(prev, next))
}

func TestMarkClosedOnItsOwn(t *testing.T) {
	plan := RemediationPlan{Summary: "Add a readiness probe."}
	markClosedOnItsOwn(&plan, nil)
	assert.Equal(t, "Add a readiness probe.", plan.Summary)
	closed := at("2026-10-22 09:15")
	markClosedOnItsOwn(&plan, closed)
	markClosedOnItsOwn(&plan, closed)
	assert.Equal(t, "Closed on its own at 2026-10-22 09:15 UTC. Add a readiness probe.", plan.Summary)
}

func TestPlanningRequestText(t *testing.T) {
	text := planningRequestText(events.PlanningRequest{EventId: "e1", Kind: planTurnReplan, BaseVersion: "pv-1",
		Reasons: []string{"too risky", "wrong target"}, Note: "do not restart the database"}, nil)
	assert.Contains(t, text, "plan_version pv-1")
	assert.Contains(t, text, "Their reasons: too risky; wrong target.")
	assert.Contains(t, text, "<<<\ndo not restart the database\n>>>")
	assert.Contains(t, text, "In findings, say what you changed")

	assert.Contains(t, planningRequestText(events.PlanningRequest{EventId: "e1", Kind: planTurnRecheck, BaseVersion: "pv-1"}, nil), "Re-check the remediation plan")
	auto := planningRequestText(events.PlanningRequest{EventId: "e1", Kind: planTurnAuto}, at("2026-10-22 09:15"))
	assert.Contains(t, auto, "Write the remediation plan for event e1")
	assert.Contains(t, auto, "closed on its own at 2026-10-22 09:15 UTC")
}

func TestSentenceOf(t *testing.T) {
	assert.Equal(t, "This event has no fixes to replace; ask Nubi to prepare a fix.", sentenceOf("this event has no fixes to replace; ask Nubi to prepare a fix"))
	assert.Equal(t, "Done.", sentenceOf("Done."))
}

// The repair pass for an agent's plan: the first proposal whose reads found something is refused with that state;
// the same targets again are stored; other targets are shown again.
func TestTargetStateRefusal(t *testing.T) {
	swap(t, &shownTargetState, &targetStateMemo{seen: map[string]time.Time{}})
	assert.NoError(t, targetStateRefusal("e1", RemediationTrace{}), "nothing read")
	assert.NoError(t, targetStateRefusal("e1", RemediationTrace{TargetReads: []targetRead{{Target: "deploy/api", Unknown: "forbidden"}}}), "nothing found")

	trace := RemediationTrace{TargetReads: []targetRead{{Target: "deployment api in shop", Source: "kubectl get deployment api -n shop -o yaml", Content: "limits:\n  memory: 1Gi"}}}
	err := targetStateRefusal("e1", trace)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory: 1Gi")
	assert.Contains(t, err.Error(), "call propose_fix again with it unchanged")
	assert.NoError(t, targetStateRefusal("e1", trace), "shown once")
	other := RemediationTrace{TargetReads: []targetRead{{Target: "configmap app in shop", Source: "kubectl get cm app -n shop -o yaml", Content: "data: {}"}}}
	assert.Error(t, targetStateRefusal("e1", other))
	assert.Error(t, targetStateRefusal("e2", trace), "per event")
}

// ─── The planner against a database ───

type planningFixture struct {
	t       *testing.T
	db      *sqlx.DB
	account string
	event   string
	mu      sync.Mutex
	kicks   int
	idle    bool
	queries []string
	// turn is what the planning turn does with its tool context; turnEnd how it ends.
	turn     func(toolcore.NbToolContext)
	turnEnd  core.ConversationStatus
	answer   string
	messages map[string]core.ConversationStatus
	stored   []events.PlanningRequest
	// evidenceOf is the event each turn's investigation was read from.
	evidenceOf []string
}

func newPlanningFixture(t *testing.T) *planningFixture {
	t.Helper()
	db := remediationScratchDB(t, "planning", `CREATE TABLE event_resolution (LIKE public.event_resolution INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, tenant text NOT NULL DEFAULT 'tenant-1', fingerprint text,
			aggregation_key text DEFAULT 'agg', status text DEFAULT 'FIRING', nb_status text DEFAULT 'OPEN', starts_at timestamp, ends_at timestamp,
			nb_status_changed_at timestamp, created_at timestamp NOT NULL DEFAULT LOCALTIMESTAMP, labels jsonb);
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, tenant text);
		CREATE TABLE feature_flag (feature_id text, tenant_id text, account_id uuid, status text);
		INSERT INTO feature_flag VALUES ('`+events.RemediationLoopFeature+`', 'tenant-1', NULL, 'enabled');
		CREATE TABLE event_log_analysis (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid NOT NULL, event_fingerprint text,
			analysis text, summary text, status text NOT NULL DEFAULT 'COMPLETED', status_reason text, cloud_account_id uuid,
			event_aggregation_key text, analysis_type varchar NOT NULL, recorded_at timestamp NOT NULL DEFAULT LOCALTIMESTAMP, updated_at timestamp DEFAULT LOCALTIMESTAMP);
		CREATE TABLE event_analysis_mapping (event_id uuid, analysis_id uuid, analysis_type text, UNIQUE (event_id, analysis_type));
		CREATE TABLE event_duplicates (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid, cloud_account_id uuid, first_event_id uuid, occurrence_number int);
		CREATE TABLE llm_conversations (id uuid PRIMARY KEY, session_id text, account_id uuid, status text NOT NULL DEFAULT 'WAITING');
		CREATE TABLE llm_conversation_messages (id uuid PRIMARY KEY, message_config text, conversation_id uuid, status text, response text);
		CREATE TABLE llm_conversation_tool_calls (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), conversation_id uuid, message_id uuid,
			tool_name text, status text, response text, created_at timestamp DEFAULT now())`)
	f := &planningFixture{t: t, db: db, account: uuid.NewString(), event: uuid.NewString(), idle: true, turnEnd: core.ConversationStatusCompleted,
		messages: map[string]core.ConversationStatus{}}
	_, err := db.Exec(`INSERT INTO cloud_accounts VALUES ($1, 'tenant-1')`, f.account)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint) VALUES ($1, $2, 'fp-1')`, f.event, f.account)
	require.NoError(t, err)
	withMetastore(t, db)
	stubLoopOn(t, true)
	swap(t, &config.Config.RemediationFollowupMaxRuns, 3)
	swap(t, &kickPlanning, func(*sqlx.DB, events.PlanningRequest) { f.mu.Lock(); f.kicks++; f.mu.Unlock() })
	swap(t, &goPlanningTurn, func(turn func()) { turn() })
	// A person's request runs as them; here every person has write access to the account.
	swap(t, &loadPersonSecurityContext, func(tenant, userId string) (*security.SecurityContext, error) {
		return security.NewSecurityContextForTenantAccountAdmin(tenant, userId, []string{f.account}), nil
	})
	swap(t, &planningConversationIdle, func(events.PlanningRequest) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.idle })
	swap(t, &planningEvidence, func(_ *security.RequestContext, _, _, evidenceEventId string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.evidenceOf = append(f.evidenceOf, evidenceEventId)
		return "the pod was OOM-killed at 512Mi", nil
	})
	swap(t, &startPlanningMessage, func(row events.PlanningRequest, messageId uuid.UUID, query string) (string, bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.queries = append(f.queries, query)
		f.messages[messageId.String()] = core.ConversationStatusInProgress
		_, err := f.db.Exec(`INSERT INTO llm_conversation_messages (id, status) VALUES ($1, 'IN_PROGRESS')`, messageId)
		require.NoError(t, err)
		return "conversation-1", true, nil
	})
	swap(t, &runPlanningTurn, func(row events.PlanningRequest, _ string, messageId uuid.UUID, _ string) error {
		if f.turn != nil {
			f.turn(f.planningToolCtx(row.Kind, messageId.String()))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.messages[messageId.String()] = f.turnEnd
		_, err := f.db.Exec(`UPDATE llm_conversation_messages SET response = $2 WHERE id = $1`, messageId, f.answer)
		require.NoError(t, err)
		return nil
	})
	swap(t, &planningTurnState, func(row events.PlanningRequest) (core.ConversationStatus, string, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		s, ok := f.messages[row.MessageId]
		return s, f.answer, ok
	})
	swap(t, &proposePlanningPlan, func(_ *security.RequestContext, row events.PlanningRequest, _, _ string) (RemediationPlan, string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stored = append(f.stored, row)
		return RemediationPlan{Summary: "Raise the memory limit", Actions: []RemediationAction{{Action: "Raise limit"}}}, "pv-2", nil
	})
	return f
}

// planningToolCtx is a tool call's context in a planning turn, as runPlanningTurn runs it.
func (f *planningFixture) planningToolCtx(kind, messageId string) toolcore.NbToolContext {
	ctx := core.WithReadOnlyRun(security.NewRequestContextForTenantAccountAdmin("tenant-1", security.GetSystemUserId(), []string{f.account}))
	return toolcore.NbToolContext{Ctx: ctx, AccountId: f.account, MessageId: messageId, QueryConfig: planningConfig(kind)}
}

func (f *planningFixture) ctx() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{f.account})
}

// storePlan stores a plan for the event, written ago.
func (f *planningFixture) storePlan(ago time.Duration) {
	f.t.Helper()
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, status, cloud_account_id, event_aggregation_key, analysis_type, updated_at)
		VALUES ($1, 'fp-1', $3, 'COMPLETED', $2, 'agg', 'remediation', LOCALTIMESTAMP - make_interval(secs => $4))`, f.event, f.account, testPlanJSON, ago.Seconds())
	require.NoError(f.t, err)
}

func (f *planningFixture) latest() *events.PlanningRequest {
	f.t.Helper()
	row, err := events.LatestPlanning(f.ctx().GetContext(), f.db, f.account, f.event)
	require.NoError(f.t, err)
	require.NotNil(f.t, row)
	return row
}

func (f *planningFixture) proposeCalls(n int, config string) {
	f.t.Helper()
	conversation := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO llm_conversations VALUES ($1, 'event-fp-1', $2)`, conversation, f.account)
	require.NoError(f.t, err)
	for i := 0; i < n; i++ {
		message := uuid.NewString()
		_, err = f.db.Exec(`INSERT INTO llm_conversation_messages (id, message_config) VALUES ($1, $2)`, message, config)
		require.NoError(f.t, err)
		_, err = f.db.Exec(`INSERT INTO llm_conversation_tool_calls (conversation_id, message_id, tool_name, status) VALUES ($1, $2, $3, 'success')`,
			conversation, message, toolcore.ToolProposeFix)
		require.NoError(f.t, err)
	}
}

func TestRequestBackgroundPlan(t *testing.T) {
	f := newPlanningFixture(t)
	row, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAsk, By: "user-1"})
	require.NoError(t, err)
	require.Empty(t, refusal)
	assert.Equal(t, planTurnAsk, row.Kind)
	assert.Equal(t, 1, f.kicks, "its turn is tried at once")
	again, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	assert.Empty(t, refusal)
	assert.Equal(t, row.Id, again.Id, "the open request, not a second one")
	assert.Equal(t, 1, f.kicks)

	_, refusal, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnReplan, By: "user-1", Note: "x"})
	require.NoError(t, err)
	assert.Contains(t, refusal, "no fixes to replace")

	_, err = events.FinishPlanning(f.ctx().GetContext(), f.db, row.Id, events.PlanningNone, "", "test")
	require.NoError(t, err)
	f.storePlan(time.Hour)
	_, refusal, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	assert.Contains(t, refusal, "already has fixes prepared", "a repeated completion keeps the plan of this investigation")

	// The 24 h cap counts chat proposals and replans, not the planner's own turns.
	f.proposeCalls(2, `{"read_only_run":true,"remediation_plan_turn":"auto"}`)
	f.proposeCalls(2, `{}`)
	replan, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnReplan, By: "user-1", Reasons: []string{"too risky"}})
	require.NoError(t, err)
	require.Empty(t, refusal)
	assert.NotEmpty(t, replan.BaseVersion, "a replan names the plan it replaces")
	_, err = events.FinishPlanning(f.ctx().GetContext(), f.db, replan.Id, events.PlanningNone, "", "test")
	require.NoError(t, err)
	f.proposeCalls(1, `{"remediation_plan_turn":"replan"}`)
	_, refusal, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnReplan, By: "user-1", Note: "x"})
	require.NoError(t, err)
	assert.Contains(t, refusal, "already has 3 plans")
	assert.Contains(t, refusal, "a replan counts toward it")

	// A run in flight: a person's request waits for its check.
	_, err = f.db.Exec(`INSERT INTO event_resolution (event_id, type, type_reference_id, resolver_type, resolver_id, data, status) VALUES ($1, 'CommandExecution', 'kubectl set resources deploy/api -n shop --limits=memory=1Gi', 'User', 'user-1', '{}', 'InProgress')`, f.event)
	require.NoError(t, err)
	_, refusal, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnRecheck})
	require.NoError(t, err)
	assert.Contains(t, refusal, "is running")

	// A duplicate of a live main event with a plan: the main event's plan applies.
	main := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint) VALUES ($1, $2, 'fp-main')`, main, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO event_log_analysis (event_id, analysis, cloud_account_id, analysis_type) VALUES ($1, $2, $3, 'remediation')`, main, testPlanJSON, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE events SET nb_status = 'DUPLICATE', labels = jsonb_build_object('nb_duplicate_of', $2::text) WHERE id = $1`, f.event, main)
	require.NoError(t, err)
	_, refusal, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAsk})
	require.NoError(t, err)
	assert.Equal(t, notOwnPlanReason, refusal, "a duplicate of a live main event with a plan gets no plan of its own")

	stubLoopOn(t, false)
	_, refusal, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAsk})
	require.NoError(t, err)
	assert.Contains(t, refusal, "loop is off")
}

// A request's turn starts when the conversation is free, writes the plan with propose_fix (without a follow-up
// run), and ends the request with the plan's version. A second proposal in the turn is refused.
func TestPlanningTurnWritesThePlan(t *testing.T) {
	f := newPlanningFixture(t)
	var second error
	f.turn = func(tc toolcore.NbToolContext) {
		out, err := proposeFix(tc, "the limit is 512Mi and the pod peaks at 700Mi", testPlanJSON)
		require.NoError(t, err)
		assert.Contains(t, out, "Stored a new version of the alert's fixes, with 1 fix option")
		assert.Contains(t, out, "Your final answer is one sentence that the fixes were updated")
		_, second = proposeFix(tc, "again", testPlanJSON)
	}
	f.idle = false
	row, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	require.Empty(t, refusal)
	assert.False(t, advancePlanning(f.db, row), "busy: waits")
	assert.Equal(t, events.PlanningQueued, f.latest().State)

	f.idle = true
	require.NoError(t, runDuePlannings())
	got := f.latest()
	assert.Equal(t, events.PlanningDone, got.State)
	assert.Equal(t, "pv-2", got.PlanVersion)
	require.Len(t, f.stored, 1)
	assert.Equal(t, f.event, f.stored[0].EventId, "the request's event, never the model's")
	require.Error(t, second)
	assert.Contains(t, second.Error(), "already wrote its plan")
	require.Len(t, f.queries, 1)
	assert.Contains(t, f.queries[0], "Write the remediation plan for event "+f.event)
	assert.Contains(t, f.queries[0], "the pod was OOM-killed at 512Mi")
}

// A turn that ends without a plan, one that fails, and a request that waits too long: each ends with a reason,
// never preparing forever.
func TestPlanningEndsWithAReason(t *testing.T) {
	f := newPlanningFixture(t)
	f.answer = "The investigation found no fault: the alert threshold is too low."
	_, _, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	// A turn that completes without propose_fix (a model outage ends a turn that way) is started again while
	// attempts remain, and the request ends with no plan only after the last.
	require.NoError(t, runDuePlannings()) // starts and runs the turn, which writes nothing
	assert.Equal(t, events.PlanningRunning, f.latest().State)
	require.NoError(t, runDuePlannings()) // sees it ended with attempts left: queued again
	got := f.latest()
	assert.Equal(t, events.PlanningQueued, got.State, "retried, not ended")
	assert.Equal(t, 1, got.Attempts)
	require.NoError(t, runDuePlannings()) // the second turn, which writes nothing either
	assert.Equal(t, events.PlanningRunning, f.latest().State)
	require.NoError(t, runDuePlannings()) // the last attempt: ends
	got = f.latest()
	assert.Equal(t, events.PlanningNone, got.State)
	assert.Equal(t, "it answered without proposing one", got.Reason)
	assert.Equal(t, 2, got.Attempts)
	assert.Len(t, f.queries, 2)

	// A failed turn is started again once, then the request ends.
	f.turnEnd = core.ConversationStatusFailed
	_, _, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAsk})
	require.NoError(t, err)
	for i := 0; i < 4; i++ {
		require.NoError(t, runDuePlannings())
	}
	got = f.latest()
	assert.Equal(t, events.PlanningNone, got.State)
	assert.Equal(t, "its turn failed", got.Reason)
	assert.Equal(t, 2, got.Attempts)
	assert.Len(t, f.queries, 4)

	// Busy past the give-up: none, with the reason.
	f.idle = false
	_, _, err = requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAsk})
	require.NoError(t, err)
	swap(t, &planningGiveUp, time.Duration(0))
	require.NoError(t, runDuePlannings())
	got = f.latest()
	assert.Equal(t, events.PlanningNone, got.State)
	assert.Contains(t, got.Reason, "stayed busy")
}

// The hook's facts from the database: a day-old plan of an alert that fired again and had a run since.
func TestLoadPlanFacts(t *testing.T) {
	f := newPlanningFixture(t)
	f.storePlan(25 * time.Hour)
	newer := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint) VALUES ($1, $2, 'fp-1')`, newer, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO event_resolution (event_id, type, type_reference_id, resolver_type, resolver_id, data, status, created_at) VALUES ($1, 'CommandExecution', 'kubectl set resources deploy/api -n shop --limits=memory=1Gi', 'User', 'user-1', '{}', 'Success', LOCALTIMESTAMP),
		($1, 'CommandExecution', 'kubectl set resources deploy/api -n shop --limits=memory=1Gi', 'User', 'user-1', '{}', 'Declined', LOCALTIMESTAMP)`, newer)
	require.NoError(t, err)
	in := remediationGetInput{AccountId: f.account, EventId: f.event, Fingerprint: "fp-1", Plan: readyPlan()}
	facts, err := loadPlanFacts(f.ctx(), in)
	require.NoError(t, err)
	assert.Equal(t, 1, facts.Firings)
	assert.Equal(t, newer, facts.NewerEventId)
	assert.Equal(t, 1, facts.Runs, "a declined step is not a run")
	loop := &remediationLoopState{}
	fillPlanLoop(loop, in, facts)
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	assert.True(t, loop.Relevance.Stale)
	assert.Equal(t, "written more than 24 h ago", loop.Relevance.StaleReason, "the run came after the firing")
}

// A duplicate's read (a new firing with another fingerprint, marked a duplicate of the closed main event) shows
// the main event's plan as ready, the incident as firing, and the duplicate as a firing since the plan.
func TestLoadPlanFacts_DuplicateShowsTheMainEventsPlan(t *testing.T) {
	f := newPlanningFixture(t)
	f.storePlan(time.Hour)
	_, err := f.db.Exec(`UPDATE events SET status = 'CLOSED', ends_at = LOCALTIMESTAMP - interval '2 hours' WHERE id = $1`, f.event)
	require.NoError(t, err)
	dup := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, starts_at, labels) VALUES ($1, $2, 'fp-other', 'DUPLICATE',
		LOCALTIMESTAMP - interval '10 minutes', jsonb_build_object('nb_duplicate_of', $3::text))`, dup, f.account, f.event)
	require.NoError(t, err)

	in := remediationGetInput{AccountId: f.account, EventId: dup, Fingerprint: "fp-other", PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: readyPlan()}
	facts, err := loadPlanFacts(f.ctx(), in)
	require.NoError(t, err)
	require.NotNil(t, facts.Event)
	assert.Equal(t, dup, facts.Event.NewestId)
	assert.Equal(t, 1, facts.Firings, "the duplicate is a firing since the plan")
	assert.Equal(t, dup, facts.NewerEventId)
	loop := &remediationLoopState{PrimaryEventId: f.event}
	fillPlanLoop(loop, in, facts)
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	assert.Equal(t, "firing", loop.Relevance.AlertState, "the main event closed, its duplicate fires")
	assert.False(t, loop.Relevance.Stale, "the main event closed before the plan, nothing was tried since: the same incident")
}

// conversation_busy: the alert's conversation has a turn in progress or waiting; read for the plan's fingerprint.
func TestLoadPlanFacts_ConversationBusy(t *testing.T) {
	f := newPlanningFixture(t)
	conversation, message := uuid.NewString(), uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO llm_conversations VALUES ($1, 'event-fp-1', $2)`, conversation, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO llm_conversation_messages (id, conversation_id, status) VALUES ($1, $2, 'COMPLETED')`, message, conversation)
	require.NoError(t, err)
	in := remediationGetInput{AccountId: f.account, EventId: f.event, PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: &RemediationPlan{}}
	busy := func() bool {
		t.Helper()
		facts, err := loadPlanFacts(f.ctx(), in)
		require.NoError(t, err)
		return facts.ConversationBusy
	}
	assert.False(t, busy())
	for _, status := range []string{"IN_PROGRESS", "PENDING", "QUEUED", "WAITING"} {
		_, err = f.db.Exec(`UPDATE llm_conversation_messages SET status = $2 WHERE id = $1`, message, status)
		require.NoError(t, err)
		assert.True(t, busy(), status)
	}
	other := in
	other.AccountId = uuid.NewString()
	facts, err := loadPlanFacts(f.ctx(), other)
	require.NoError(t, err)
	assert.False(t, facts.ConversationBusy, "another account's conversation")
}

// On an alert that keeps firing, a new firing makes the plan stale only after a step was run since the plan, or
// after the alert cleared since the plan; firings while it stayed broken and nothing was tried do not.
func TestLoadPlanFacts_StaleOnlyAfterARunOrAClear(t *testing.T) {
	f := newPlanningFixture(t)
	f.storePlan(time.Hour)
	_, err := f.db.Exec(`UPDATE events SET created_at = LOCALTIMESTAMP - interval '2 hours', starts_at = LOCALTIMESTAMP - interval '2 hours' WHERE id = $1`, f.event)
	require.NoError(t, err)
	fire := func(ago string, closedAgo any) {
		t.Helper()
		_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, status, starts_at, created_at, ends_at, labels)
			VALUES ($1, $2, 'fp-1', 'DUPLICATE', CASE WHEN $4::text IS NULL THEN 'FIRING' ELSE 'CLOSED' END, LOCALTIMESTAMP - $3::interval,
			        LOCALTIMESTAMP - $3::interval, LOCALTIMESTAMP - $4::interval, jsonb_build_object('nb_duplicate_of', $5::text))`,
			uuid.NewString(), f.account, ago, closedAgo, f.event)
		require.NoError(t, err)
	}
	in := remediationGetInput{AccountId: f.account, EventId: f.event, Fingerprint: "fp-1", Plan: readyPlan()}
	relevance := func() *planRelevance {
		t.Helper()
		facts, err := loadPlanFacts(f.ctx(), in)
		require.NoError(t, err)
		loop := &remediationLoopState{}
		fillPlanLoop(loop, in, facts)
		require.NotNil(t, loop.Relevance)
		return loop.Relevance
	}

	// The main event still fires; three more firings since the plan, one of which closed while the main event
	// still fired (not a clear), and nothing tried.
	fire("50 minutes", "45 minutes")
	fire("30 minutes", nil)
	fire("10 minutes", nil)
	rel := relevance()
	assert.False(t, rel.Stale, rel.StaleReason)
	assert.Equal(t, 3, rel.FiringsSincePlan)

	// A step ran 20 minutes ago and the alert fired after it.
	_, err = f.db.Exec(`INSERT INTO event_resolution (event_id, type, type_reference_id, resolver_type, resolver_id, data, status, created_at)
		VALUES ($1, 'CommandExecution', 'kubectl set resources deploy/api -n shop --limits=memory=1Gi', 'User', 'user-1', '{}', 'Success', LOCALTIMESTAMP - interval '20 minutes')`, f.event)
	require.NoError(t, err)
	rel = relevance()
	assert.True(t, rel.Stale)
	assert.Equal(t, "the alert fired again after a step was run", rel.StaleReason)

	// Cleared, then fired again: every firing closed 40 minutes ago, a new one 15 minutes ago, nothing tried.
	g := newPlanningFixture(t)
	g.storePlan(time.Hour)
	_, err = g.db.Exec(`UPDATE events SET created_at = LOCALTIMESTAMP - interval '2 hours', status = 'CLOSED', ends_at = LOCALTIMESTAMP - interval '40 minutes' WHERE id = $1`, g.event)
	require.NoError(t, err)
	f = g
	in = remediationGetInput{AccountId: g.account, EventId: g.event, Fingerprint: "fp-1", Plan: readyPlan()}
	fire("15 minutes", nil)
	rel = relevance()
	assert.True(t, rel.Stale)
	assert.Equal(t, "the alert cleared and fired again since the plan", rel.StaleReason)
}

func TestProposeFixOutsideAPlanningRequestIsRefused(t *testing.T) {
	f := newPlanningFixture(t)
	_, err := proposeFix(f.planningToolCtx(planTurnAuto, uuid.NewString()), "findings", testPlanJSON)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "has ended or already wrote its plan"), err.Error())
}

// The planner judges the alert by its incident: a firing duplicate after a closed main event is a firing alert
// (no "closed on its own"), and its investigation is read from the newest firing that has one.
func TestPlanningQueryJudgesTheIncident(t *testing.T) {
	f := newPlanningFixture(t)
	_, err := f.db.Exec(`UPDATE events SET status = 'CLOSED', starts_at = LOCALTIMESTAMP - interval '1 day', ends_at = LOCALTIMESTAMP - interval '20 hours',
		created_at = LOCALTIMESTAMP - interval '1 day' WHERE id = $1`, f.event)
	require.NoError(t, err)
	investigate := func(event string) {
		t.Helper()
		_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, status, cloud_account_id, event_aggregation_key, analysis_type)
			VALUES ($1, 'fp-1', 'investigation', 'COMPLETED', $2, 'agg', 'investigation')`, event, f.account)
		require.NoError(t, err)
	}
	firing := func(status string, ago string) string {
		t.Helper()
		id := uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, status, starts_at, ends_at, created_at, labels)
			VALUES ($1, $2, 'fp-1', 'DUPLICATE', $3, LOCALTIMESTAMP - $4::interval,
			        CASE WHEN $3 = 'CLOSED' THEN LOCALTIMESTAMP - $4::interval + interval '5 minutes' END, LOCALTIMESTAMP - $4::interval,
			        jsonb_build_object('nb_duplicate_of', $5::text))`, id, f.account, status, ago, f.event)
		require.NoError(t, err)
		return id
	}
	investigate(f.event)
	row := events.PlanningRequest{EventId: f.event, AccountId: f.account, Alert: "fp-1", Kind: planTurnRecheck}

	// The main event closed; a later duplicate fires now. Only the main event was investigated.
	firing("CLOSED", "10 hours")
	latest := firing("FIRING", "30 minutes")
	query, reason := planningQuery(f.ctx(), row)
	require.Empty(t, reason)
	assert.NotContains(t, query, "closed on its own")
	assert.Contains(t, query, "## Investigation of an earlier firing (event "+f.event+")")
	assert.Contains(t, query, ": firing.")
	assert.Equal(t, []string{f.event}, f.evidenceOf)
	inc := planningIncident(row)
	require.NotNil(t, inc)
	state, _ := alertStateOf(*inc)
	assert.Equal(t, "firing", state)

	// The newest firing was investigated: the evidence is its.
	investigate(latest)
	query, _ = planningQuery(f.ctx(), row)
	assert.Equal(t, latest, f.evidenceOf[1])
	assert.Contains(t, query, "## Investigation of the alert's current firing")

	// Every firing closed: closed on its own, at the newest firing's close.
	_, err = f.db.Exec(`UPDATE events SET status = 'CLOSED', ends_at = LOCALTIMESTAMP - interval '10 minutes' WHERE id = $1`, latest)
	require.NoError(t, err)
	inc = planningIncident(row)
	require.NotNil(t, inc)
	closed := incidentClosedAt(inc)
	require.NotNil(t, closed)
	assert.Equal(t, *inc.NewestEndsAt, *closed)
	query, _ = planningQuery(f.ctx(), row)
	assert.Contains(t, query, "closed on its own at "+closed.UTC().Format("2006-01-02 15:04"))
}

// propose_fix keeps the plan's cause_key (normalized) and optional_hardening through validation and storage; a plan
// without them is stored too, with an empty cause_key.
func TestProposedPlanKeepsCauseKeyAndHardening(t *testing.T) {
	f := newPlanningFixture(t)
	swap(t, &targetReader, func(*security.RequestContext, string, []targetSpec) []targetRead { return nil })
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', '{}', 'INV: the api pod is OOMKilled at 512Mi', 'COMPLETED', $2, 'agg', 'investigation')`, f.event, f.account)
	require.NoError(t, err)
	stored := func() map[string]any {
		t.Helper()
		var analysis string
		require.NoError(t, f.db.Get(&analysis, `SELECT analysis FROM event_log_analysis WHERE event_id = $1 AND analysis_type = 'remediation'`, f.event))
		var out map[string]any
		require.NoError(t, json.Unmarshal([]byte(analysis), &out))
		return out
	}

	withBoth := `{"root_cause":"512Mi limit","summary":"Raise the memory limit","actions":[{"action":"Raise limit","kind":"fix",
		"execute_command":"kubectl set resources deployment api -n shop --limits=memory=1Gi"}],
		"cause_key":{"what":"Deployment/api","how":"OOMKilled","against":"Memory_Limit"},
		"optional_hardening":[{"target":"deployment api","change":"add a memory alert at 80% of the limit","reason":"warn before the next kill"}]}`
	plan, version, err := proposePlan(f.ctx(), f.account, f.event, withBoth)
	require.NoError(t, err)
	require.NotEmpty(t, version)
	require.NotNil(t, plan.CauseKey)
	assert.Equal(t, events.CauseKey{What: "deploy api", How: "oom", Against: "memory limit"}, *plan.CauseKey)
	require.Len(t, plan.OptionalHardening, 1)
	got := stored()
	assert.Equal(t, map[string]any{"what": "deploy api", "how": "oom", "against": "memory limit"}, got["cause_key"])
	assert.Equal(t, []any{map[string]any{"target": "deployment api", "change": "add a memory alert at 80% of the limit", "reason": "warn before the next kill"}},
		got["optional_hardening"])

	plan, _, err = proposePlan(f.ctx(), f.account, f.event, testPlanJSON)
	require.NoError(t, err)
	assert.Equal(t, events.CauseKey{}, *plan.CauseKey)
	assert.Empty(t, plan.OptionalHardening)
	got = stored()
	assert.Equal(t, map[string]any{"what": "", "how": "", "against": ""}, got["cause_key"], "stored empty, not null")
	assert.NotContains(t, got, "optional_hardening")
}

// A plan written by a planning turn is recorded by the turn's own answer: storing it adds no "Suggest a
// remediation" message to the alert's conversation, so the turn is the only new message there.
func TestPlanningTurnAddsOneMessage(t *testing.T) {
	f := newPlanningFixture(t)
	swap(t, &proposePlanningPlan, realProposePlanningPlan)
	swap(t, &targetReader, func(*security.RequestContext, string, []targetSpec) []targetRead { return nil })
	var appended int
	swap(t, &appendPlanToConversation, func(*security.RequestContext, string, RemediationGenerateRequest, RemediationPlan) { appended++ })
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', '{}', 'INV: the api pod is OOMKilled at 512Mi', 'COMPLETED', $2, 'agg', 'investigation')`, f.event, f.account)
	require.NoError(t, err)
	f.turn = func(tc toolcore.NbToolContext) {
		_, err := proposeFix(tc, "the limit is 512Mi", testPlanJSON)
		require.NoError(t, err)
	}
	_, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnAuto})
	require.NoError(t, err)
	require.Empty(t, refusal)
	require.NoError(t, runDuePlannings())
	assert.Equal(t, events.PlanningDone, f.latest().State, "the turn stored its plan")
	assert.Len(t, f.queries, 1, "one message: the planning turn's")
	assert.Zero(t, appended, "no synthetic plan message beside the turn's answer")

	// The planner model's plan has no turn in the conversation: it is still added there.
	_, err = storeRemediationPlan(f.ctx(), "conversation-1", RemediationGenerateRequest{AccountId: f.account, EventId: f.event},
		RemediationPlan{Summary: "x"}, RemediationTrace{})
	require.NoError(t, err)
	assert.Equal(t, 1, appended)
}

// loop.firings: the alert's newest firings from the plan's event on, oldest first, bounded. refire_after_run: the
// first firing after the newest run's check ended, only once it ended.
func TestLoadPlanFacts_FiringsAndRefire(t *testing.T) {
	f := newPlanningFixture(t)
	f.storePlan(2 * time.Hour)
	_, err := f.db.Exec(`UPDATE events SET created_at = LOCALTIMESTAMP - interval '3 hours', starts_at = LOCALTIMESTAMP - interval '3 hours' WHERE id = $1`, f.event)
	require.NoError(t, err)
	byAgo := map[int]string{}
	for ago := 125; ago >= 5; ago -= 5 { // 25 firings after the main event
		id := uuid.NewString()
		byAgo[ago] = id
		status, ends := "CLOSED", "LOCALTIMESTAMP - make_interval(mins => $3) + interval '2 minutes'"
		if ago <= 10 {
			status, ends = "FIRING", "NULL"
		}
		_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, status, starts_at, created_at, ends_at, labels)
			VALUES ($1, $2, 'fp-1', 'DUPLICATE', $4, LOCALTIMESTAMP - make_interval(mins => $3), LOCALTIMESTAMP - make_interval(mins => $3), `+ends+`,
			        jsonb_build_object('nb_duplicate_of', $5::text))`, id, f.account, ago, status, f.event)
		require.NoError(t, err)
	}
	in := remediationGetInput{AccountId: f.account, EventId: f.event, PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: readyPlan()}
	load := func() *remediationLoopState {
		t.Helper()
		facts, err := loadPlanFacts(f.ctx(), in)
		require.NoError(t, err)
		loop := &remediationLoopState{}
		fillPlanLoop(loop, in, facts)
		return loop
	}

	loop := load()
	require.Len(t, loop.Firings, loopFiringsShown, "bounded")
	assert.Equal(t, 26, loop.FiringsTotal, "the main event and its 25 firings")
	for i := 1; i < len(loop.Firings); i++ {
		assert.True(t, loop.Firings[i].StartsAt.After(loop.Firings[i-1].StartsAt), "oldest first")
	}
	last := loop.Firings[len(loop.Firings)-1]
	assert.Equal(t, byAgo[5], last.EventId, "the newest is last")
	assert.Equal(t, "FIRING", last.Status)
	assert.Equal(t, "DUPLICATE", last.NbStatus)
	assert.Nil(t, last.EndsAt, "still firing")
	// The main event never closed, so all of it is one episode: its start, then the newest.
	assert.Equal(t, []string{f.event, byAgo[125], byAgo[120], byAgo[85]}, []string{loop.Firings[0].EventId, loop.Firings[1].EventId,
		loop.Firings[2].EventId, loop.Firings[3].EventId}, "the first episode's oldest three, then the newest 17")
	assert.Nil(t, loop.RefireAfterRun, "no run")

	// A run 90 minutes ago whose check has not ended: nothing to say yet.
	var run string
	require.NoError(t, f.db.Get(&run, `INSERT INTO event_resolution (event_id, type, type_reference_id, resolver_type, resolver_id, data, status, created_at)
		VALUES ($1, 'CommandExecution', 'kubectl set resources deploy/api -n shop --limits=memory=1Gi', 'User', 'user-1', '{"followup":{"claimed_at":"x"}}', 'Success', LOCALTIMESTAMP - interval '90 minutes') RETURNING id::text`, f.event))
	assert.Nil(t, load().RefireAfterRun, "the check has not ended")

	// Its check ended 80 minutes ago; the alert fired again 75 minutes ago, labelled as the same cause.
	_, err = f.db.Exec(`UPDATE event_resolution SET data = jsonb_build_object('followup', jsonb_build_object('done_at', (LOCALTIMESTAMP - interval '80 minutes')::timestamptz))
		WHERE id = $1`, run)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE events SET labels = labels || '{"nb_refire":{"kind":"fix_did_not_hold"}}'::jsonb WHERE id = $1`, byAgo[75])
	require.NoError(t, err)
	refire := load().RefireAfterRun
	require.NotNil(t, refire)
	assert.Equal(t, run, refire.RunId)
	assert.Equal(t, byAgo[75], refire.EventId, "the first firing after the check")
	assert.Equal(t, 15, refire.MinutesAfter, "minutes after the run")
	assert.Equal(t, "same_cause", refire.Cause)

	// A check that ended after every firing: no re-fire.
	_, err = f.db.Exec(`UPDATE event_resolution SET data = jsonb_build_object('followup', jsonb_build_object('verdict_at', (LOCALTIMESTAMP + interval '1 minute')::timestamptz))
		WHERE id = $1`, run)
	require.NoError(t, err)
	assert.Nil(t, load().RefireAfterRun)
}

func TestRefireCause(t *testing.T) {
	assert.Equal(t, "same_cause", refireCause("fix_did_not_hold"))
	assert.Equal(t, "different", refireCause("different_cause"))
	assert.Equal(t, "unknown", refireCause("unknown"))
	assert.Equal(t, "unknown", refireCause("something new"))
	assert.Empty(t, refireCause(""))
}

// With the loop on, ai_remediation_generate (the legacy panel's generate, fired on mount and refresh) returns the
// stored plan and starts no planning turn, with a current plan or without one.
func TestGenerateWithTheLoopOnRequestsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newPlanningFixture(t)
	requests := func() int {
		t.Helper()
		var n int
		require.NoError(t, f.db.Get(&n, `SELECT count(*) FROM event_log_analysis WHERE analysis_type = $1`, events.AnalysisTypeRemediationPlanning))
		return n
	}
	generate := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		respondGenerateWithLoop(c, f.ctx(), f.account, f.event)
		require.Equal(t, http.StatusOK, rec.Code)
		var body struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.Data
	}

	generate()
	assert.Zero(t, requests(), "no plan: still no planning turn")
	f.storePlan(time.Hour)
	body := generate()
	assert.Zero(t, requests(), "a current plan: no re-check nobody asked for")
	assert.Equal(t, "512Mi limit", body["root_cause"], "the stored plan comes back")
	assert.Zero(t, f.kicks)
}

// A duplicate follows its main event only while the main event is live: not resolved, not suppressed, and still
// firing or worked on. Otherwise it heads its own chain. A plan stored on the duplicate itself is never hidden
// behind a main event with none. Both duplicate links count: labels.nb_duplicate_of and event_duplicates.
func TestPlanEventOf_DuplicateFollowsOnlyALiveMain(t *testing.T) {
	f := newPlanningFixture(t)
	main := f.event
	dup := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status, labels) VALUES ($1, $2, 'fp-dup', 'DUPLICATE',
		jsonb_build_object('nb_duplicate_of', $3::text))`, dup, f.account, main)
	require.NoError(t, err)
	planEvent := func(msg string) string {
		t.Helper()
		got, err := events.PlanEventOf(f.ctx().GetContext(), f.db, f.account, dup)
		require.NoError(t, err, msg)
		return got
	}
	setMain := func(status, nbStatus string) {
		t.Helper()
		_, err := f.db.Exec(`UPDATE events SET status = $2, nb_status = $3 WHERE id = $1`, main, status, nbStatus)
		require.NoError(t, err)
	}

	assert.Equal(t, main, planEvent("live"), "a firing main event: follow it, as before")
	setMain("FIRING", "SUPPRESSED")
	assert.Equal(t, dup, planEvent("suppressed"), "a suppressed main event is over")
	setMain("CLOSED", "RESOLVED")
	assert.Equal(t, dup, planEvent("resolved"), "a resolved main event is over")
	setMain("CLOSED", "OPEN")
	assert.Equal(t, dup, planEvent("closed, never analysed"), "a closed main event nobody worked on is over")
	investigation := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO event_log_analysis (id, event_id, analysis, cloud_account_id, analysis_type) VALUES ($1, $2, '{}', $3, 'investigation')`,
		investigation, uuid.NewString(), f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO event_analysis_mapping VALUES ($1, $2, 'investigation')`, main, investigation)
	require.NoError(t, err)
	assert.Equal(t, main, planEvent("closed, investigated"), "a closed main event with an investigation still carries the chain")

	// The duplicate has a plan of its own and the main event none: the duplicate's plan; once the main event has
	// one, the main event's.
	_, err = f.db.Exec(`INSERT INTO event_log_analysis (event_id, analysis, cloud_account_id, analysis_type) VALUES ($1, $2, $3, 'remediation')`, dup, testPlanJSON, f.account)
	require.NoError(t, err)
	assert.Equal(t, dup, planEvent("own plan"), "the duplicate's own plan is never hidden")
	planned, refusal, err := requestBackgroundPlan(f.ctx(), f.account, dup, planRequestInput{Kind: planTurnRecheck})
	require.NoError(t, err)
	assert.Empty(t, refusal, "the planner plans for the duplicate")
	assert.Equal(t, dup, planned.EventId)
	f.storePlan(time.Minute)
	assert.Equal(t, main, planEvent("both plans"))

	// event_duplicates alone (a later occurrence triage suppressed, no label).
	other := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, nb_status) VALUES ($1, $2, 'fp-1', 'SUPPRESSED')`, other, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO event_duplicates (event_id, cloud_account_id, first_event_id, occurrence_number) VALUES ($1, $2, $3, 2)`, other, f.account, main)
	require.NoError(t, err)
	got, err := events.PlanEventOf(f.ctx().GetContext(), f.db, f.account, other)
	require.NoError(t, err)
	assert.Equal(t, main, got, "a live main event through event_duplicates")
	setMain("FIRING", "SUPPRESSED")
	got, err = events.PlanEventOf(f.ctx().GetContext(), f.db, f.account, other)
	require.NoError(t, err)
	assert.Equal(t, other, got, "a suppressed main event through event_duplicates is over")
	got, err = events.PlanEventOf(f.ctx().GetContext(), f.db, f.account, uuid.NewString())
	require.NoError(t, err)
	assert.NotEmpty(t, got, "an unknown event is its own")
}

// Without a plan, the read still lists the alert's firings and its state, and says why there is no plan.
func TestLoadPlanFacts_FiringsWithoutAPlan(t *testing.T) {
	f := newPlanningFixture(t)
	_, err := f.db.Exec(`UPDATE events SET created_at = LOCALTIMESTAMP - interval '1 hour' WHERE id = $1`, f.event)
	require.NoError(t, err)
	later := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint) VALUES ($1, $2, 'fp-1')`, later, f.account)
	require.NoError(t, err)
	in := remediationGetInput{AccountId: f.account, EventId: f.event, Fingerprint: "fp-1", PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: &RemediationPlan{}}
	facts, err := loadPlanFacts(f.ctx(), in)
	require.NoError(t, err)
	assert.Equal(t, f.event, facts.PlanEventOf)
	require.Len(t, facts.List, 2)
	assert.Equal(t, later, facts.List[1].EventId)
	loop := &remediationLoopState{}
	fillPlanLoop(loop, in, facts)
	assert.Equal(t, PlanStatusNone, loop.PlanStatus)
	assert.Equal(t, noPlanAskedReason, loop.PlanStatusReason)
	assert.Len(t, loop.Firings, 2)
	require.NotNil(t, loop.Relevance)
	assert.Equal(t, "firing", loop.Relevance.AlertState)
	assert.Equal(t, 1, loop.Relevance.FiringsSincePlan, "counted from the event when there is no plan")
	assert.Equal(t, later, loop.Relevance.NewerEventId)
	assert.False(t, loop.Relevance.Stale)
}

// A planning turn that answers without storing a plan never leaves an answer that reads as if the plan changed: its
// message says no plan was stored and why, the next attempt is told the earlier one stored none, and the request's
// reason is the real one (no propose_fix call, or propose_fix's refusal).
func TestPlanningTurnWithoutAStoredPlanSaysSo(t *testing.T) {
	f := newPlanningFixture(t)
	f.storePlan(time.Hour)
	f.answer = "The remediation plan has been updated to target the deployment's memory limit."
	_, refusal, err := requestBackgroundPlan(f.ctx(), f.account, f.event, planRequestInput{Kind: planTurnReplan, By: "user-1",
		Reasons: []string{"Different cause"}, Note: "The fix is the deployment's memory limit."})
	require.NoError(t, err)
	require.Empty(t, refusal)
	responses := func() []string {
		t.Helper()
		var got []string
		require.NoError(t, f.db.Select(&got, `SELECT COALESCE(response, '') FROM llm_conversation_messages ORDER BY response`))
		return got
	}

	require.NoError(t, runDuePlannings()) // the first turn answers in prose
	require.NoError(t, runDuePlannings()) // ended without a plan: noted, queued again
	got := responses()
	require.Len(t, got, 1)
	assert.Contains(t, got[0], f.answer)
	assert.Contains(t, got[0], "**No new fixes were stored.** Nubi prepared no fix: it answered without proposing one. The alert's fixes are unchanged. Nubi tries once more.")
	require.Len(t, f.queries, 1)
	assert.Contains(t, f.queries[0], planningStoreText)
	assert.NotContains(t, f.queries[0], planningRetryText)

	// The second attempt is told the first stored nothing; this time propose_fix refused its plan.
	f.turn = func(tc toolcore.NbToolContext) {
		_, err := f.db.Exec(`INSERT INTO llm_conversation_tool_calls (message_id, tool_name, status, response) VALUES ($1, 'propose_fix', 'error', $2)`,
			tc.MessageId, "propose_fix: the plan was not stored (a step has no command). Correct it and call propose_fix again\nlive state ...")
		require.NoError(t, err)
	}
	require.NoError(t, runDuePlannings())
	require.Len(t, f.queries, 2)
	assert.Contains(t, f.queries[1], planningRetryText)
	require.NoError(t, runDuePlannings())
	last := f.latest()
	assert.Equal(t, events.PlanningNone, last.State)
	assert.Equal(t, "propose_fix refused it: the plan was not stored (a step has no command). Correct it and call propose_fix again", last.Reason)
	got = responses()
	require.Len(t, got, 2)
	for _, response := range got {
		assert.Equal(t, 1, strings.Count(response, noPlanStoredMarker), "noted once: %s", response)
	}
	assert.Contains(t, strings.Join(got, "\n"), "Nubi prepared no fix: propose_fix refused it: the plan was not stored (a step has no command). Correct it and call propose_fix again. The alert's fixes are unchanged.")

	// The read carries the reason.
	in := remediationGetInput{AccountId: f.account, EventId: f.event, PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: readyPlan()}
	facts, err := loadPlanFacts(f.ctx(), in)
	require.NoError(t, err)
	loop := &remediationLoopState{}
	fillPlanLoop(loop, in, facts)
	assert.Equal(t, PlanStatusReady, loop.PlanStatus)
	assert.Equal(t, "Nubi prepared no fix: "+last.Reason, loop.PlanStatusReason)
}

// More firings than the tab shows: the first episode's start and the firing that closed it (the first clear) stay
// in, then the newest; firings_total counts them all. 30 firings in two episodes: the first (60-45 minutes ago,
// closed 40 minutes ago), then the alert fired again 30 minutes ago and kept firing.
func TestLoadPlanFacts_FiringsKeepTheFirstEpisode(t *testing.T) {
	f := newPlanningFixture(t)
	_, err := f.db.Exec(`UPDATE events SET created_at = LOCALTIMESTAMP - interval '61 minutes', starts_at = LOCALTIMESTAMP - interval '61 minutes',
		status = 'CLOSED', ends_at = LOCALTIMESTAMP - interval '50 minutes' WHERE id = $1`, f.event)
	require.NoError(t, err)
	fire := func(agoSecs int, endsAgoSecs any) string {
		t.Helper()
		id := uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, status, starts_at, created_at, ends_at)
			VALUES ($1, $2, 'fp-1', CASE WHEN $4::int IS NULL THEN 'FIRING' ELSE 'CLOSED' END, LOCALTIMESTAMP - make_interval(secs => $3),
			        LOCALTIMESTAMP - make_interval(secs => $3), LOCALTIMESTAMP - make_interval(secs => $4::int))`, id, f.account, agoSecs, endsAgoSecs)
		require.NoError(t, err)
		return id
	}
	// The first episode: the main event and 4 more, the last of them closing 40 minutes ago.
	episode := []string{f.event, fire(58*60, 48*60), fire(55*60, 47*60), fire(50*60, 44*60)}
	closing := fire(45*60, 40*60)
	// The second: 25 firings from 30 minutes ago, every minute or so, still firing.
	var newest []string
	for i := 0; i < 25; i++ {
		newest = append(newest, fire(30*60-i*70, nil))
	}
	in := remediationGetInput{AccountId: f.account, EventId: f.event, PlanEventId: f.event, PlanFingerprint: "fp-1", Plan: &RemediationPlan{}}
	facts, err := loadPlanFacts(f.ctx(), in)
	require.NoError(t, err)
	loop := &remediationLoopState{}
	fillPlanLoop(loop, in, facts)

	assert.Equal(t, 30, loop.FiringsTotal)
	require.Len(t, loop.Firings, loopFiringsShown)
	var ids []string
	for _, firing := range loop.Firings {
		ids = append(ids, firing.EventId)
	}
	want := append(append(append([]string{}, episode[:loopFirstEpisodeShown]...), closing), newest[len(newest)-(loopFiringsShown-loopFirstEpisodeShown-1):]...)
	assert.Equal(t, want, ids, "the first episode's oldest three and the firing that cleared it, then the newest 16")
	require.NotNil(t, loop.Firings[loopFirstEpisodeShown].EndsAt, "the first clear is when that firing closed")

	// Firings that fit are all shown.
	g := newPlanningFixture(t)
	in.AccountId, in.EventId, in.PlanEventId = g.account, g.event, g.event
	facts, err = loadPlanFacts(g.ctx(), in)
	require.NoError(t, err)
	assert.Equal(t, 1, facts.ListTotal)
	assert.Len(t, facts.List, 1)
}

// The alert's state reads NudgeBee's own status before the source's: a newest firing resolved (by a person or by a
// run's verdict) while its source still says FIRING is resolved, since it was resolved; the plan's event resolved
// while the source says FIRING, likewise. Suppressed is not resolved: the problem can still be there.
func TestAlertStateResolvedBeforeSourceFiring(t *testing.T) {
	resolvedAt, started := at("2026-10-23 14:40"), at("2026-10-23 14:29")
	newestResolved := planEventFacts{Status: "CLOSED", NbStatus: "OPEN", EndsAt: at("2026-10-23 14:23"), CreatedAt: *at("2026-10-23 10:22"),
		NewestId: "a1", NewestStatus: "FIRING", NewestNbStatus: "RESOLVED", NewestNbChanged: resolvedAt, NewestStartsAt: started, NewestCreatedAt: started}
	state, since := alertStateOf(newestResolved)
	assert.Equal(t, "resolved", state)
	assert.Equal(t, *resolvedAt, *since)

	alone := planEventFacts{Status: "FIRING", NbStatus: "RESOLVED", NbChangedAt: resolvedAt, StartsAt: started, CreatedAt: *started}
	state, since = alertStateOf(alone)
	assert.Equal(t, "resolved", state)
	assert.Equal(t, *resolvedAt, *since)

	suppressed := alone
	suppressed.NbStatus = "SUPPRESSED"
	state, _ = alertStateOf(suppressed)
	assert.Equal(t, "firing", state)

	// A plan's event resolved earlier, a newer firing still open: firing, as before.
	refired := alone
	refired.NewestId, refired.NewestStatus, refired.NewestNbStatus, refired.NewestStartsAt, refired.NewestCreatedAt = "a2", "FIRING", "OPEN", resolvedAt, resolvedAt
	state, _ = alertStateOf(refired)
	assert.Equal(t, "firing", state)
}

// With the loop off an agent-written plan (propose_fix in the alert's chat) is added to the alert's conversation as
// its own message, as before the loop; with it on the turn's answer is the record there.
func TestAgentWrittenPlanIsAddedToTheChatWithTheLoopOff(t *testing.T) {
	f := newPlanningFixture(t)
	var appended int
	swap(t, &appendPlanToConversation, func(*security.RequestContext, string, RemediationGenerateRequest, RemediationPlan) { appended++ })
	swap(t, &targetReader, func(*security.RequestContext, string, []targetSpec) []targetRead { return nil })
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', '{}', 'INV: the api pod is OOMKilled at 512Mi', 'COMPLETED', $2, 'agg', 'investigation')`, f.event, f.account)
	require.NoError(t, err)
	stubLoopOn(t, false)
	_, version, err := proposePlan(f.ctx(), f.account, f.event, testPlanJSON)
	require.NoError(t, err)
	require.NotEmpty(t, version)
	assert.Equal(t, 1, appended, "loop off: the plan is in the alert's chat")
}
