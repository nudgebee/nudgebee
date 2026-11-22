package api

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// With the remediation loop on, an alert has one conversation, event-<fingerprint>, for its investigation and
// everything after it (#40734). These tests run against a real database (REMEDIATION_RUN_TEST_DB_URL) with the
// conversation DAO on it.

type sessionsFixture struct {
	*planningFixture
}

func newSessionsFixture(t *testing.T) *sessionsFixture {
	t.Helper()
	p := newPlanningFixture(t)
	_, err := p.db.Exec(`DROP TABLE llm_conversations; DROP TABLE llm_conversation_messages;
		CREATE TABLE llm_conversations (id uuid PRIMARY KEY, user_id uuid, session_id text, account_id uuid, context text, status text, tenant_id text,
			title text, source text, llm_provider text, llm_model text, llm_tier_overrides jsonb, llm_config_source text, run_profile jsonb,
			created_at timestamp DEFAULT now(), updated_at timestamp DEFAULT now());
		CREATE UNIQUE INDEX llm_conversations_session_account_null_user_uniq ON llm_conversations (session_id, account_id) WHERE user_id IS NULL;
		CREATE TABLE llm_conversation_messages (id uuid PRIMARY KEY, conversation_id uuid, message text, created_at timestamp DEFAULT clock_timestamp(),
			response text, role text, account_id uuid, user_id uuid, message_type text, updated_at timestamp DEFAULT now(), responded_at timestamp,
			status text, worker_name text, agent_name text, parent_agent_id uuid, message_config text, message_context text, suggestions text,
			llm_provider text, llm_model text, metadata jsonb);
		CREATE TABLE llm_conversation_agent (id uuid PRIMARY KEY, conversation_id uuid, message_id uuid, status text);
		CREATE TABLE llm_conversation_token_usage (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), conversation_id uuid, message_id uuid, tenant_id text,
			session_id text, conversation_account_id uuid, prompt_messages text, response_content text, error_message text);
		CREATE TABLE cloud_account_attrs (cloud_account_id uuid, name text, value text)`)
	require.NoError(t, err)
	prev := core.PeekConversationDao()
	core.SetConversationDao(core.NewConversationDao(&common.DatabaseManager{Db: p.db}))
	t.Cleanup(func() { core.SetConversationDao(prev) })
	return &sessionsFixture{planningFixture: p}
}

// conversation adds a conversation in session with status, and returns its id.
func (f *sessionsFixture) conversation(session string, status core.ConversationStatus) string {
	f.t.Helper()
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO llm_conversations (id, session_id, account_id, status, tenant_id, title) VALUES ($1, $2, $3, $4, gen_random_uuid(), 'Event details - old')`,
		id, session, f.account, status)
	require.NoError(f.t, err)
	return id
}

// message adds a message to conversation and returns its id; config is its message_config, runId (when set) its
// metadata.investigation_run_id, ago how long ago it was written.
func (f *sessionsFixture) message(conversation, agent, messageType, response string, status core.ConversationStatus, config, runId string, ago time.Duration) string {
	f.t.Helper()
	id := uuid.NewString()
	var metadata any
	if runId != "" {
		metadata = `{"investigation_run_id":"` + runId + `"}`
	}
	_, err := f.db.Exec(`INSERT INTO llm_conversation_messages (id, conversation_id, message, response, role, account_id, message_type, status, agent_name,
			message_config, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'human', $5, $6, $7, $8, $9, $10::jsonb, LOCALTIMESTAMP - make_interval(secs => $11), LOCALTIMESTAMP - make_interval(secs => $11))`,
		id, conversation, "question for event "+f.event, response, f.account, messageType, status, agent, config, metadata, ago.Seconds())
	require.NoError(f.t, err)
	return id
}

// approval adds a run_step confirmation waiting in conversation, asked ago.
func (f *sessionsFixture) approval(conversation string, ago time.Duration) string {
	return f.message(conversation, "remediation", "followup", "", core.ConversationStatusWaiting,
		`{"followupType":"tool_confirmation","confirmationKey":"run_step:pv-1:1:apply","question":"Approve?"}`, "", ago)
}

// runRow stores eventId's log_analysis row (its run) created ago, mapped to the event, and returns the run.
func (f *sessionsFixture) runRow(eventId string, ago time.Duration) events.EventRun {
	f.t.Helper()
	var run events.EventRun
	require.NoError(f.t, f.db.Get(&run, `INSERT INTO event_log_analysis (event_id, event_fingerprint, status, cloud_account_id, event_aggregation_key, analysis_type, recorded_at, updated_at)
		VALUES ($1, 'fp-1', 'IN_PROGRESS', $2, 'agg', 'log_analysis', LOCALTIMESTAMP - make_interval(secs => $3), LOCALTIMESTAMP - make_interval(secs => $3))
		RETURNING id::text AS id, recorded_at AS started_at`, eventId, f.account, ago.Seconds()))
	_, err := f.db.Exec(`INSERT INTO event_analysis_mapping (event_id, analysis_id, analysis_type) VALUES ($1, $2, 'log_analysis')
		ON CONFLICT (event_id, analysis_type) DO UPDATE SET analysis_id = EXCLUDED.analysis_id`, eventId, run.Id)
	require.NoError(f.t, err)
	return run
}

func stageConfigJSON(runId string) string {
	b, _ := json.Marshal(toolcore.NBQueryConfig{InvestigationRunId: runId})
	return string(b)
}

// AC1: the investigation's stages run in the alert's conversation; there is no other session for them.
func TestLoopOn_StagesRunInTheAlertConversation(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "event_analyzer.go", nil, 0)
	require.NoError(t, err)
	src, err := os.ReadFile("event_analyzer.go")
	require.NoError(t, err)
	assert.Contains(t, string(src), "parentConversationId := events.SessionIdPrefixEvent + eventFingerprint")
	assert.NotContains(t, string(src), "event-inv-")
	// Every stage call in the alert's conversation names its run (request.stageConfig), so its turn is told apart.
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "core", "HandleConversationSessionRequest") {
			return true
		}
		if session, ok := call.Args[4].(*ast.Ident); !ok || session.Name != "parentConversationId" {
			return true
		}
		staged := false
		for _, arg := range call.Args {
			option, ok := arg.(*ast.CallExpr)
			if !ok || !isSelector(option.Fun, "core", "ConversationSessionRequestWithConfig") {
				continue
			}
			if inner, ok := option.Args[0].(*ast.CallExpr); ok {
				if sel, ok := inner.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "stageConfig" {
					staged = true
				}
			}
		}
		assert.True(t, staged, "a stage call at %v does not name its run", call.Pos())
		return true
	})
}

// AC1: the alert's conversation, when NudgeBee creates it, is titled with the event id like an investigation, so
// the Investigations tab lists the event.
func TestEnsureAlertChat_TitleCarriesEventId(t *testing.T) {
	f := newSessionsFixture(t)
	id, err := ensureAlertChat(f.account, f.event, "fp-1")
	require.NoError(t, err)
	var title, session string
	require.NoError(t, f.db.QueryRow(`SELECT title, session_id FROM llm_conversations WHERE id = $1`, id).Scan(&title, &session))
	assert.Equal(t, "Event details - "+f.event, title)
	assert.Equal(t, "event-fp-1", session)
}

// AC2/AC8: with the loop on a regenerate never deletes the alert's conversation (its approvals and runs stay) and
// retitles it to the event now investigated; with it off the conversation is deleted as before.
func TestRegenerate_KeepsTheAlertConversationWithTheLoopOn(t *testing.T) {
	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusCompleted)
	denied := f.message(chat, "remediation", "followup", "deny", core.ConversationStatusCompleted, `{"followupType":"tool_confirmation"}`, "", time.Minute)
	conv, err := core.GetConversationDao().GetConversationBySession(f.account, "event-fp-1")
	require.NoError(t, err)

	resetInvestigationConversation(f.ctx(), EventAnalysisRequest{AccountId: f.account, EventId: f.event, UserId: "user-1", Regenerate: true}, "event-fp-1", conv)
	var title string
	require.NoError(t, f.db.Get(&title, `SELECT title FROM llm_conversations WHERE id = $1`, chat))
	assert.Equal(t, "Event details - "+f.event, title, "titled with the event investigated now")
	var n int
	require.NoError(t, f.db.Get(&n, `SELECT count(*) FROM llm_conversation_messages WHERE id = $1`, denied))
	assert.Equal(t, 1, n, "the denial is still in the conversation")

	stubLoopOn(t, false)
	resetInvestigationConversation(f.ctx(), EventAnalysisRequest{AccountId: f.account, EventId: f.event, UserId: "user-1", Regenerate: true}, "event-fp-1", conv)
	require.NoError(t, f.db.Get(&n, `SELECT count(*) FROM llm_conversations`))
	assert.Zero(t, n, "loop off: the conversation is deleted on regenerate, as before")
}

// A flag that cannot be read is not "off": a regenerate never deletes the alert's conversation then, and the run
// does not start (it is retried, as a stuck analysis is). A run that needs no delete goes ahead.
func TestRegenerate_FlagUnreadNeverDeletes(t *testing.T) {
	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusCompleted)
	conv, err := core.GetConversationDao().GetConversationBySession(f.account, "event-fp-1")
	require.NoError(t, err)
	stubLoopOn(t, false)
	events.RemediationLoopRead = func(string) (bool, error) { return false, errors.New("tenant not found") }

	start := resetInvestigationConversation(f.ctx(), EventAnalysisRequest{AccountId: f.account, EventId: f.event, UserId: "user-1", Regenerate: true}, "event-fp-1", conv)
	assert.False(t, start, "the run waits for the flag")
	var n int
	require.NoError(t, f.db.Get(&n, `SELECT count(*) FROM llm_conversations WHERE id = $1`, chat))
	assert.Equal(t, 1, n, "the conversation is kept")

	assert.True(t, resetInvestigationConversation(f.ctx(), EventAnalysisRequest{AccountId: f.account, EventId: f.event, UserId: "user-1"}, "event-fp-1", conv),
		"nothing to delete: the run goes ahead")
	require.NoError(t, f.db.Get(&n, `SELECT count(*) FROM llm_conversations WHERE id = $1`, chat))
	assert.Equal(t, 1, n)
}

// AC8: with the loop off nothing of this is in play: no run on stage turns, no gate.
func TestLoopOff_NoRunNoGate(t *testing.T) {
	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	f.approval(chat, time.Minute)
	stubLoopOn(t, false)
	reason, err := investigationBlocked(f.ctx().GetContext(), f.db, f.account, "fp-1")
	require.NoError(t, err)
	assert.Empty(t, reason)
	assert.Empty(t, EventAnalysisRequest{}.stageConfig(toolcore.NBQueryConfig{EventId: "e"}).InvestigationRunId)
	assert.False(t, conversationHeld(core.ErrConversationPendingFollowup, false), "loop off: a waiting follow-up is not a reason to wait")
	assert.True(t, conversationHeld(core.ErrConversationInProgress, false))
	key, mayStart := pipelineHoldKey(f.db, f.account, "fp-1", slog.Default())
	assert.Empty(t, key)
	assert.True(t, mayStart)
}

// AC2: the gate's causes, each on its own.
func TestInvestigationBlocked(t *testing.T) {
	f := newSessionsFixture(t)
	ctx := f.ctx().GetContext()
	blocked := func() string {
		t.Helper()
		reason, err := investigationBlocked(ctx, f.db, f.account, "fp-1")
		require.NoError(t, err)
		return reason
	}
	assert.Empty(t, blocked(), "no conversation, no run")

	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	old := f.approval(chat, 13*time.Hour)
	assert.Empty(t, blocked(), "an approval older than 12 h does not hold the investigation back")

	recent := f.approval(chat, time.Hour)
	assert.Equal(t, gateReasonApproval, blocked())
	_, err := f.db.Exec(`UPDATE llm_conversation_messages SET status = 'COMPLETED' WHERE id IN ($1, $2)`, old, recent)
	require.NoError(t, err)

	question := f.message(chat, "events", "followup", "", core.ConversationStatusWaiting, `{"followupType":"ask_user"}`, "", time.Minute)
	assert.Equal(t, gateReasonQuestion, blocked())
	_, err = f.db.Exec(`UPDATE llm_conversation_messages SET status = 'COMPLETED' WHERE id = $1`, question)
	require.NoError(t, err)

	turn := f.message(chat, "remediation", "generation", "", core.ConversationStatusInProgress, `{}`, "", time.Minute)
	assert.Equal(t, gateReasonTurn, blocked(), "someone else's turn under way")
	_, err = f.db.Exec(`UPDATE llm_conversation_messages SET metadata = '{"investigation_run_id":"r"}' WHERE id = $1`, turn)
	require.NoError(t, err)
	assert.Empty(t, blocked(), "the investigation's own stage turn does not hold it back")

	_, err = f.db.Exec(`INSERT INTO event_resolution (event_id, type, type_reference_id, resolver_type, resolver_id, data, status) VALUES ($1, 'CommandExecution', 'kubectl rollout restart deploy/api', 'User', 'user-1', '{}', 'InProgress')`, f.event)
	require.NoError(t, err)
	assert.Equal(t, gateReasonRun, blocked())
}

// An approval whose turn failed (the conversation is FAILED, KILLED or stopped) can never be answered: the resume only
// takes a waiting or running conversation. It neither holds the investigation back nor keeps the tab busy.
func TestInvestigationBlocked_DeadApprovalNotCounted(t *testing.T) {
	f := newSessionsFixture(t)
	ctx := f.ctx().GetContext()
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	f.approval(chat, time.Hour)
	reason, err := investigationBlocked(ctx, f.db, f.account, "fp-1")
	require.NoError(t, err)
	assert.Equal(t, gateReasonApproval, reason, "a live approval holds it back")

	for _, status := range []string{"FAILED", "KILLED", "TERMINATED"} {
		_, err = f.db.Exec(`UPDATE llm_conversations SET status = $1 WHERE id = $2`, status, chat)
		require.NoError(t, err)
		reason, err = investigationBlocked(ctx, f.db, f.account, "fp-1")
		require.NoError(t, err)
		assert.Empty(t, reason, "an approval in a %s conversation is dead", status)
	}
}

// Superseded (product decision 2): an approval asked before the alert cleared and fired again is not counted; one
// asked during the current episode is, even when the alert re-fired without clearing (always-fresh namespaces).
func TestInvestigationBlocked_EarlierEpisodeApprovalNotCounted(t *testing.T) {
	f := newSessionsFixture(t)
	ctx := f.ctx().GetContext()
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	f.approval(chat, 2*time.Hour)
	// The fixture's event is still firing: a re-fire now is the same episode.
	_, err := f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, created_at) VALUES (gen_random_uuid(), $1, 'fp-1', LOCALTIMESTAMP)`, f.account)
	require.NoError(t, err)
	reason, err := investigationBlocked(ctx, f.db, f.account, "fp-1")
	require.NoError(t, err)
	assert.Equal(t, gateReasonApproval, reason, "a re-fire while still firing is the same episode")

	// Every firing cleared an hour ago; a new firing now opens a new episode.
	_, err = f.db.Exec(`UPDATE events SET ends_at = LOCALTIMESTAMP - interval '1 hour', created_at = LOCALTIMESTAMP - interval '3 hours'`)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, created_at) VALUES (gen_random_uuid(), $1, 'fp-1', LOCALTIMESTAMP)`, f.account)
	require.NoError(t, err)
	reason, err = investigationBlocked(ctx, f.db, f.account, "fp-1")
	require.NoError(t, err)
	assert.Empty(t, reason, "the approval is the earlier episode's")
}

// Superseded: the follow-ups tick closes an earlier episode's approval through the no-model close, and leaves the
// current episode's alone.
func TestSupersedeEarlierEpisodeApprovals(t *testing.T) {
	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	f.approval(chat, 2*time.Hour)
	var closed []string
	swap(t, &closeWaitingConversation, func(accountId, conversationId, note, reason string) (bool, error) {
		assert.Equal(t, core.ClosedReasonSuperseded, reason)
		assert.Equal(t, supersededApprovalNote, note)
		closed = append(closed, conversationId)
		return true, nil
	})
	supersedeEarlierEpisodeApprovals(f.db)
	assert.Empty(t, closed, "the alert never cleared: same episode")

	_, err := f.db.Exec(`UPDATE events SET ends_at = LOCALTIMESTAMP - interval '1 hour', created_at = LOCALTIMESTAMP - interval '3 hours'`)
	require.NoError(t, err)
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, created_at) VALUES (gen_random_uuid(), $1, 'fp-1', LOCALTIMESTAMP)`, f.account)
	require.NoError(t, err)
	supersedeEarlierEpisodeApprovals(f.db)
	assert.Equal(t, []string{chat}, closed)

	closed = nil
	stubLoopOn(t, false)
	supersedeEarlierEpisodeApprovals(f.db)
	assert.Empty(t, closed, "loop off: nothing closed")
}

// AC2: a regenerate while an approval waits keeps the conversation, parks the rows IN_PROGRESS with the reason, and
// queues no run; the page reads the reason back.
func TestRegenerateBlockedByApproval_ParksWithReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newSessionsFixture(t)
	for _, stage := range []events.EventAnalysisType{events.AnalysisTypeSummary, events.AnalysisTypeInvestigation, events.AnalysisTypeLog, events.AnalysisTypeDetailedResponse} {
		var id string
		require.NoError(t, f.db.Get(&id, `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
			VALUES ($1, 'fp-1', '{}', 'done', 'COMPLETED', $2, 'agg', $3) RETURNING id::text`, f.event, f.account, stage))
		_, err := f.db.Exec(`INSERT INTO event_analysis_mapping VALUES ($1, $2, $3)`, f.event, id, stage)
		require.NoError(t, err)
	}
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	f.approval(chat, time.Hour)

	respond := func(regenerate bool) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{f.account})
		executeEventInvestigation(ctx, EventAnalysisRequest{AccountId: f.account, EventId: f.event, UserId: "user-1", Regenerate: regenerate}, c)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return body.Data
	}
	got := respond(true)
	assert.Equal(t, "IN_PROGRESS", got["status"])
	assert.Equal(t, gateReasonApproval, got["status_reason"])
	assert.NotContains(t, got, "session_id")

	var rows []struct {
		Status string `db:"status"`
		Reason string `db:"status_reason"`
	}
	require.NoError(t, f.db.Select(&rows, `SELECT a.status, COALESCE(a.status_reason, '') AS status_reason FROM event_analysis_mapping m
		JOIN event_log_analysis a ON a.id = m.analysis_id WHERE m.event_id = $1`, f.event))
	require.Len(t, rows, 4)
	for _, r := range rows {
		assert.Equal(t, "IN_PROGRESS", r.Status)
		assert.Equal(t, gateReasonApproval, r.Reason)
	}
	var conversations int
	require.NoError(t, f.db.Get(&conversations, `SELECT count(*) FROM llm_conversations WHERE id = $1`, chat))
	assert.Equal(t, 1, conversations, "no conversation delete")
	var previous int
	require.NoError(t, f.db.Get(&previous, `SELECT count(*) FROM event_log_analysis WHERE event_id = $1 AND status = 'COMPLETED'`, f.event))
	assert.Equal(t, 4, previous, "the previous run is kept as a version")

	// The page polls: the waiting reason is read back.
	got = respond(false)
	assert.Equal(t, "IN_PROGRESS", got["status"])
	assert.Equal(t, gateReasonApproval, got["status_reason"])
}

// AC2: recovery keeps a waiting investigation parked with a fresh heartbeat, never abandons it, and once nothing
// holds it back restarts it at once (a parked row needs no minimum age).
func TestInvestigationMustWait(t *testing.T) {
	f := newSessionsFixture(t)
	dbm := &common.DatabaseManager{Db: f.db}
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	approval := f.approval(chat, time.Hour)
	run := f.runRow(f.event, 30*time.Hour)
	conv, err := core.GetConversationDao().GetConversationBySession(f.account, "event-fp-1")
	require.NoError(t, err)
	stuck := events.InProgressAnalysis{ID: run.Id, AccountId: f.account, EventId: f.event, EventFingerprint: "fp-1", EventAggregationKey: "agg", AnalysisType: events.AnalysisTypeLog}

	assert.True(t, investigationMustWait(dbm, stuck, conv))
	var reason string
	var age float64
	require.NoError(t, f.db.QueryRow(`SELECT status_reason, EXTRACT(EPOCH FROM LOCALTIMESTAMP - updated_at) FROM event_log_analysis WHERE id = $1`, run.Id).Scan(&reason, &age))
	assert.Equal(t, gateReasonApproval, reason)
	assert.Less(t, age, 60.0, "kept fresh: the 24 h abandon never sees it while it waits")
	assert.True(t, isGateReason(reason), "a parked row restarts without waiting out the minimum age")

	_, err = f.db.Exec(`UPDATE llm_conversation_messages SET status = 'COMPLETED', response = 'yes' WHERE id = $1`, approval)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE llm_conversations SET status = 'COMPLETED'`)
	require.NoError(t, err)
	conv, err = core.GetConversationDao().GetConversationBySession(f.account, "event-fp-1")
	require.NoError(t, err)
	assert.False(t, investigationMustWait(dbm, stuck, conv), "approved: recovery starts it")
	assert.False(t, isGateReason("investigation failed"))
}

// AC5: stage recovery stays inside the current run. The previous run's answer for the same event — even one
// written after the regenerate created the new run (its synthesis tail still finishing) — is never taken; the new
// run's own answer is.
func TestStageRecovery_StaysInsideTheRun(t *testing.T) {
	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusCompleted)
	previous := f.runRow(f.event, 2*time.Hour)
	f.message(chat, "rootcause", "generation", "the previous run's answer", core.ConversationStatusCompleted, stageConfigJSON(previous.Id), previous.Id, 90*time.Minute)
	current := f.runRow(f.event, 10*time.Minute)
	// The previous run's late answer, written after the new run started.
	f.message(chat, "rootcause", "generation", "the previous run's late answer", core.ConversationStatusCompleted, stageConfigJSON(previous.Id), previous.Id, 5*time.Minute)

	_, ok := getAgentResponseFromConversation(f.ctx(), "event-fp-1", f.account, "rootcause", f.event, current)
	assert.False(t, ok, "nothing of the current run yet: the agent runs")

	f.message(chat, "rootcause", "generation", "the current run's answer", core.ConversationStatusCompleted, stageConfigJSON(current.Id), current.Id, time.Minute)
	got, ok := getAgentResponseFromConversation(f.ctx(), "event-fp-1", f.account, "rootcause", f.event, current)
	require.True(t, ok)
	assert.Equal(t, "the current run's answer", got)

	read, err := events.RunOfEvent(f.ctx().GetContext(), f.db, f.account, f.event)
	require.NoError(t, err)
	assert.Equal(t, current.Id, read.Id, "the event's run is its mapped log row")

	// No run (loop off): the conversation is read as before.
	got, ok = getAgentResponseFromConversation(f.ctx(), "event-fp-1", f.account, "rootcause", f.event, events.EventRun{})
	require.True(t, ok)
	assert.Equal(t, "the current run's answer", got)
}

// AC5: the run's own messages, by run id first, then by time.
func TestMessagesOfRun(t *testing.T) {
	start := time.Now()
	before, after := start.Add(-time.Minute), start.Add(time.Minute)
	cfg := func(run string) *string { s := stageConfigJSON(run); return &s }
	msgs := []core.ConversationMessage{
		{Response: "old run", CreatedAt: &after, MessageConfig: cfg("r1")},
		{Response: "before start", CreatedAt: &before, MessageConfig: cfg("r2")},
		{Response: "no run", CreatedAt: &after},
		{Response: "this run", CreatedAt: &after, MessageConfig: cfg("r2")},
	}
	got := messagesOfRun(msgs, events.EventRun{Id: "r2", StartedAt: start})
	require.Len(t, got, 1)
	assert.Equal(t, "this run", got[0].Response)
	assert.Len(t, messagesOfRun(msgs, events.EventRun{}), 4)
}

// AC3: a stage that finds the conversation waiting on someone's answer (an interleaved approval turn) waits, it
// does not fail; every stage error site of the pipeline goes through conversationHeld.
func TestStagesWaitOnAnInterleavedApproval(t *testing.T) {
	assert.True(t, conversationHeld(core.ErrConversationPendingFollowup, true))
	assert.True(t, conversationHeld(core.ErrConversationInProgress, true))
	assert.False(t, conversationHeld(errors.New("model error"), true))
	src, err := os.ReadFile("event_analyzer.go")
	require.NoError(t, err)
	// The one direct check left is the RCA report's, which runs in its own event-rca- session.
	assert.Equal(t, 1, strings.Count(string(src), "errors.Is(err, core.ErrConversationInProgress)"), "every stage site uses conversationHeld")
	assert.GreaterOrEqual(t, strings.Count(string(src), "conversationHeld(err, "), 4)

	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusWaiting)
	f.approval(chat, time.Hour)
	assert.Equal(t, gateReasonApproval, heldReason(f.ctx(), f.account, "fp-1", core.ErrConversationPendingFollowup))
}

// R1: automatic turns do not start while the alert's investigation runs; a lock with no live heartbeat (a dropped
// connection) is not trusted.
func TestPipelineHoldKey(t *testing.T) {
	f := newSessionsFixture(t)
	log := slog.Default()
	key, mayStart := pipelineHoldKey(f.db, f.account, "fp-1", log)
	assert.True(t, mayStart)
	assert.Equal(t, events.EventPipelineLockKey(f.account, "fp-1"), key, "free: start with the key, so a starting pipeline is excluded")

	unlock, acquired, err := tryEventAnalysisLock(f.ctx().GetContext(), f.db, f.account, "fp-1")
	require.NoError(t, err)
	require.True(t, acquired)
	defer unlock()
	_, mayStart = pipelineHoldKey(f.db, f.account, "fp-1", log)
	assert.True(t, mayStart, "lock held but no live heartbeat: not trusted")

	run := f.runRow(f.event, time.Minute)
	require.NoError(t, events.HeartbeatEventRun(f.ctx().GetContext(), f.db, f.account, f.event))
	_, mayStart = pipelineHoldKey(f.db, f.account, "fp-1", log)
	assert.False(t, mayStart, "the investigation runs: planning and follow-up wait")

	_, err = f.db.Exec(`UPDATE event_log_analysis SET updated_at = LOCALTIMESTAMP - interval '10 minutes' WHERE id = $1`, run.Id)
	require.NoError(t, err)
	_, mayStart = pipelineHoldKey(f.db, f.account, "fp-1", log)
	assert.True(t, mayStart, "heartbeat gone stale")
}

// R1: the follow-up never gives up because the alert's own investigation held the conversation.
func TestFollowupNotGivenUpBehindInvestigation(t *testing.T) {
	f := newFollowupFixture(t)
	run := f.insertRun(f.event, "Success", 6*time.Minute, nil)
	f.busy = 100
	f.tick(1)
	f.ageClaim(run)
	swap(t, &followupBehindInvestigation, func(*sqlx.DB, followupRun, *slog.Logger) bool { return true })
	f.tick(2)
	assert.Empty(t, f.followup(run)["outcome"], "waiting behind the investigation")
	f.busy = 0
	swap(t, &followupBehindInvestigation, func(*sqlx.DB, followupRun, *slog.Logger) bool { return false })
	f.tick(2)
	assert.Equal(t, 1, f.turnCount(), "it starts on the first free tick, however long it waited")
	assert.NotEqual(t, "conversation_busy", f.followup(run)["reason"])
}

// AC6: a later turn's history carries the latest investigation's report, bounded; nothing with the loop off or
// outside an alert's conversation.
func TestInvestigationReportForHistory(t *testing.T) {
	f := newSessionsFixture(t)
	chat := f.conversation("event-fp-1", core.ConversationStatusCompleted)
	other := f.conversation("chat-1", core.ConversationStatusCompleted)
	report := "## Root cause\n" + strings.Repeat("the pod ran out of memory. ", 400)
	var id string
	require.NoError(t, f.db.Get(&id, `INSERT INTO event_log_analysis (event_id, event_fingerprint, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', $3, 'COMPLETED', $2, 'agg', 'detailed_response') RETURNING id::text`, f.event, f.account, report))
	_, err := f.db.Exec(`INSERT INTO event_analysis_mapping VALUES ($1, $2, 'detailed_response')`, f.event, id)
	require.NoError(t, err)

	eventId, got := investigationReportForHistory(f.ctx(), f.account, chat)
	assert.Equal(t, f.event, eventId)
	assert.Contains(t, got, "the pod ran out of memory")
	assert.Contains(t, got, "cut at 6000 characters")
	assert.Less(t, len(got), investigationSummaryCap+300)

	_, got = investigationReportForHistory(f.ctx(), f.account, other)
	assert.Empty(t, got, "not an alert's conversation")
	stubLoopOn(t, false)
	_, got = investigationReportForHistory(f.ctx(), f.account, chat)
	assert.Empty(t, got, "loop off: the stages are the history, as before")
}
