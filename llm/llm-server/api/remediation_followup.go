package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// The follow-up (#40734, docs/incident-agent-v1.md): once a run settles (run by NudgeBee or marked run) with the
// loop on (events.RemediationLoopFeature), a read-only remediation-agent turn in the alert's conversation
// (event-<fingerprint>), as tenant admin and only when no other turn is active, checks on it. It may propose_fix
// (a person confirms) or schedule_recheck; the model decides, code only starts, bounds and records. State lives in
// the run row's data.followup:
//
//	claimed_at            the job picked it up (or a re-check came due)
//	session               the alert's conversation
//	message_id            the current turn's message
//	findings, proposed_*  set by propose_fix
//	recheck_request       set by schedule_recheck during a turn; when the turn ends it becomes
//	recheck_at, recheck_reason, rechecks   the follow-up waits, then runs another turn
//	done_at, outcome, reason, summary   how it ended (summary is also the last report while it waits)

// followupDelay is how long after a run settles the follow-up starts: at once. A change that needs time to
// show is the model's to wait for (schedule_recheck). A var so tests can hold claims back.
var followupDelay = 0

const (
	followupReported   = "reported"   // the turn ended with a report and no proposal
	followupProposed   = "proposed"   // the turn proposed a new plan, which waits for a person
	followupSuperseded = "superseded" // a newer run of this alert has its own follow-up
	followupStopped    = "stopped"    // a person stopped the turn
	followupNeedsHuman = "needs_human"
	followupFailed     = "failed"
)

// How long a follow-up may wait for its turn to start or end before it is given up, and how many turns start
// per tick. Vars so tests can change them.
var (
	followupGiveUp        = 2 * time.Hour
	followupStartsPerTick = 3
	// At most followupsPerTenantPerTick follow-ups of one tenant move on per tick, each given followupStepDeadline
	// (its verdict may read the cluster through the relay) before the tick moves on (tickScheduler).
	followupsPerTenantPerTick = 10
	followupStepDeadline      = 45 * time.Second
)

// followupTicks schedules the follow-ups job's steps fairly across tenants.
var followupTicks = newTickScheduler("remediation_followups")

// RegisterRemediationFollowups starts the leader job that runs due follow-ups.
func RegisterRemediationFollowups() error {
	return common.NewLeaderIntervalJob("remediation_followups", runDueFollowups, time.Minute)
}

// followupRun is a run row as the follow-up reads it.
type followupRun = events.FollowupRun

// runDueFollowups claims the runs and re-checks that are due, then moves every open follow-up on: start its
// turn, or record how the turn ended. Claiming never waits on a turn, so hung turns cannot hold claims back.
func runDueFollowups() error {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("remediation_followups: %w", err)
	}
	db := dbManager.Db
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	if _, err := events.ClaimDueFollowups(ctx, db, followupDelay, 20); err != nil {
		return fmt.Errorf("remediation_followups: %w", err)
	}
	if _, err := events.ClaimDueRechecks(ctx, db, 20); err != nil {
		return fmt.Errorf("remediation_followups: %w", err)
	}
	open, err := events.OpenFollowups(ctx, db, 100)
	if err != nil {
		return fmt.Errorf("remediation_followups: %w", err)
	}
	var starts atomic.Int32
	items := make([]tickItem, 0, len(open))
	for _, run := range open {
		items = append(items, tickItem{Key: run.Id, AccountId: run.AccountId, Run: func() {
			if run.MessageId == "" && int(starts.Load()) >= followupStartsPerTick {
				return // started next tick
			}
			if advanceFollowup(db, run) {
				starts.Add(1)
			}
		}})
	}
	followupTicks.each(items, followupsPerTenantPerTick, followupStepDeadline)
	compareRefires(db)
	supersedeEarlierEpisodeApprovals(db)
	syncResolvedThreads(db)
	return nil
}

// advanceFollowup moves one open follow-up on and reports whether it started a turn.
func advanceFollowup(db *sqlx.DB, run followupRun) bool {
	logger := slog.With("run_id", run.Id, "event_id", run.EventId, "account_id", run.AccountId)
	if run.MessageId == "" {
		// The verdict comes first (remediation_verdict.go): code settles a fix that held, or a change that came
		// after the alert cleared, with no model turn, and needs neither the conversation nor the AI budget. A
		// newer run of the alert is the change to judge, so a superseded run gets no verdict.
		if newer, _, err := alertRuns(db, run); err == nil && newer {
			finishFollowup(db, run, map[string]any{"outcome": followupSuperseded}, logger)
			return false
		}
		if settleByVerdict(db, run, logger) {
			return false
		}
		if stop := followupStop(db, run, logger); stop != nil {
			finishFollowup(db, run, stop, logger)
			return false
		}
		if startFollowupTurn(db, run, logger) {
			return true
		}
		// The turn could not start: the conversation is in use. A person's turn there, or the alert's own
		// investigation, is not a reason to give up: the follow-up waits for it to end and starts on the next free
		// tick, whatever the wait cost. Only a conversation busy for followupGiveUp with neither ends it.
		if claimedLongAgo(run, followupGiveUp) && !humanTurnLive(db, run, logger) && !followupBehindInvestigation(db, run, logger) {
			finishFollowup(db, run, map[string]any{"outcome": followupNeedsHuman, "reason": "conversation_busy"}, logger)
		}
		return false
	}
	status, response, found := followupTurnState(run)
	switch {
	case !found:
		// The turn never started (the conversation was busy, or the process stopped first): start it again.
		_, _ = events.SetFollowupTurn(context.Background(), db, run.Id, run.MessageId, "")
	case status == core.ConversationStatusInProgress || status == core.ConversationStatusPending || status == core.ConversationStatusQueued:
		if claimedLongAgo(run, followupGiveUp) {
			finishFollowup(db, run, map[string]any{"outcome": followupFailed, "reason": "turn_stuck"}, logger)
		}
	default:
		endTurn(db, run, status, response, logger)
	}
	return false
}

// humanTurnWindow is how recent a running or waiting turn in the alert's conversation must be for the follow-up
// to keep waiting for it past followupGiveUp: a pending approval stays live this long (C7 lists approvals up to
// 12 h old), and an older one is stale.
const humanTurnWindow = 12 * time.Hour

// humanTurnLive reports whether a person's turn in run's alert conversation is under way, so the follow-up yields
// to it and retries next tick. A var so tests can stub it.
var humanTurnLive = func(db *sqlx.DB, run followupRun, logger *slog.Logger) bool {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	live, err := events.ConversationTurnLive(ctx, db, run.AccountId, followupSession(run), humanTurnWindow)
	if err != nil {
		logger.Warn("remediation_followups: conversation state not read", "error", err)
		return false
	}
	return live
}

// followupBehindInvestigation reports whether the alert's investigation holds its conversation now (a live
// pipeline, events.EventPipelineHoldOf), so the follow-up keeps waiting. A var so tests can stub it.
var followupBehindInvestigation = func(db *sqlx.DB, run followupRun, logger *slog.Logger) bool {
	_, mayStart := pipelineHoldKey(db, run.AccountId, firstNonEmpty(run.Fingerprint, run.EventId), logger)
	return !mayStart
}

// claimedLongAgo reports whether run was claimed at least d ago; an unreadable claim time counts as long ago.
func claimedLongAgo(run followupRun, d time.Duration) bool {
	claimedAt, err := time.Parse(time.RFC3339Nano, run.ClaimedAt)
	return err != nil || time.Since(claimedAt) >= d
}

// startFollowupTurn saves run's turn and claims the alert's conversation in one step, only while no other turn is
// active (else the next tick retries), and reports whether it did. The turn runs in the background; if this process
// stops first, dead-worker recovery runs it, read-only per its saved config.
func startFollowupTurn(db *sqlx.DB, run followupRun, logger *slog.Logger) bool {
	messageId := uuid.New()
	if set, err := events.SetFollowupTurn(context.Background(), db, run.Id, "", messageId.String()); err != nil || !set {
		return false // another leader started it
	}
	conversationId, started, err := startFollowupMessage(run, messageId)
	if err != nil || !started {
		if err != nil {
			logger.Warn("remediation_followups: turn not started", "error", err)
		}
		_, _ = events.SetFollowupTurn(context.Background(), db, run.Id, messageId.String(), "")
		return false
	}
	goFollowupTurn(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("remediation_followups: panic", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := runFollowupTurn(run, conversationId, messageId); err != nil {
			logger.Warn("remediation_followups: turn failed", "error", err)
		}
	})
	return true
}

// goFollowupTurn runs a follow-up turn in the background. A var so tests can run it in place.
var goFollowupTurn = func(turn func()) { go turn() }

// followupSession is the alert's conversation, where the follow-up's turns run.
func followupSession(run followupRun) string {
	return events.SessionIdPrefixEvent + firstNonEmpty(run.Fingerprint, run.EventId)
}

// startFollowupMessage saves run's turn messageId in the alert's conversation and claims the conversation
// for it (core StartIdleTurn); false while the conversation is busy. A var so tests can stub it.
var startFollowupMessage = func(run followupRun, messageId uuid.UUID) (string, bool, error) {
	dao := core.GetConversationDao()
	conversation, err := dao.GetConversationBySession(run.AccountId, followupSession(run))
	if err != nil || conversation.ID == uuid.Nil {
		return "", false, fmt.Errorf("the alert's conversation: %w", cmp.Or(err, errors.New("none")))
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	holdKey, mayStart := "", true
	if dbManager, err := common.GetDatabaseManager(common.Metastore); err == nil {
		holdKey, mayStart = pipelineHoldKey(dbManager.Db, run.AccountId, firstNonEmpty(run.Fingerprint, run.EventId), slog.With("run_id", run.Id))
	}
	if !mayStart {
		return conversation.ID.String(), false, nil
	}
	started, err := dao.StartIdleTurn(ctx, conversation.ID.String(), run.AccountId, messageId.String(), followupQuery(run),
		agents.RemediationAgentName, followupConfig, holdKey)
	return conversation.ID.String(), started, err
}

// followupConfig is the follow-up turn's config, saved with its message so a recovered turn keeps it: read-only,
// its kubectl calls are strict reads of the alert's own cluster, no cloud CLI (tools.readOnlyCheckRefusal).
var followupConfig = toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true, SystemNote: "Nubi checked whether the step worked"}

// followupTurnState is the status and answer of run's current turn, and whether that turn's message exists.
var followupTurnState = func(run followupRun) (core.ConversationStatus, string, bool) {
	dao := core.GetConversationDao()
	conversation, err := dao.GetConversationBySession(run.AccountId, followupSession(run))
	if err != nil || conversation.ID == uuid.Nil {
		return "", "", false
	}
	message, err := dao.GetConversationMessage(run.MessageId, run.AccountId, conversation.ID.String())
	if err != nil {
		return "", "", false
	}
	return message.Status, answerText(message.Response), true
}

// answerText is the report in a turn's stored answer: its <content>, without the model's <thought>.
func answerText(response string) string {
	if _, content, ok := strings.Cut(response, "<content>"); ok {
		response, _, _ = strings.Cut(content, "</content>")
	}
	return strings.TrimSpace(response)
}

// runFollowupTurn runs run's started turn messageId: the remediation agent, read-only, as NudgeBee with only
// the run's account. A var so tests can stub it.
var runFollowupTurn = func(run followupRun, conversationId string, messageId uuid.UUID) error {
	tenant, err := security.GetTenantIdFromAccountId(run.AccountId)
	if err != nil {
		return fmt.Errorf("tenant: %w", err)
	}
	ctx := core.WithReadOnlyRun(security.NewRequestContextForTenantAccountAdmin(tenant, security.GetSystemUserId(), []string{run.AccountId}))
	agent, found := core.GetNBAgent(ctx, agents.RemediationAgentName, run.AccountId, core.AgentStatusEnabled)
	if !found || agent == nil {
		return errors.New("the remediation agent is not available")
	}
	conversation, err := uuid.Parse(conversationId)
	if err != nil {
		return fmt.Errorf("conversation id: %w", err)
	}
	_, err = core.HandleConversationSessionRequest(ctx, agent, "", run.AccountId, followupSession(run), followupQuery(run),
		core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: conversation, Valid: true}),
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		// The message was saved and the conversation claimed for it (StartIdleTurn), as a queue claim does.
		core.ConversationSessionRequestWithQueuedDispatch(),
		core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
		core.ConversationSessionRequestWithConfig(followupConfig))
	return err
}

// followupStop is why a follow-up ends before any model call, or nil: hard limits, not judgement. A newer
// run of the same alert has its own follow-up; past the run cap a person decides; the AI budget is spent.
func followupStop(db *sqlx.DB, run followupRun, logger *slog.Logger) map[string]any {
	newer, runs, err := alertRuns(db, run)
	switch {
	case err != nil:
		logger.Error("remediation_followups: runs of the alert not read", "error", err)
		return map[string]any{"outcome": followupFailed, "reason": "database"}
	case newer:
		return map[string]any{"outcome": followupSuperseded}
	case config.Config.RemediationFollowupMaxRuns > 0 && runs >= config.Config.RemediationFollowupMaxRuns:
		return map[string]any{"outcome": followupNeedsHuman, "reason": "attempt_limit"}
	}
	tenant, err := security.GetTenantIdFromAccountId(run.AccountId)
	if err != nil {
		logger.Error("remediation_followups: tenant not read", "error", err)
		return map[string]any{"outcome": followupFailed, "reason": "tenant"}
	}
	if exceeded, _ := budget.CheckBudgetLimits(tenant, run.AccountId, budget.ModuleInvestigation, logger); exceeded {
		return map[string]any{"outcome": followupFailed, "reason": "budget"}
	}
	return nil
}

// alertRuns reports whether a newer run of the same alert (account + fingerprint) exists, and how many other plan
// versions of it got a follow-up turn in the 24 h up to run: the cap counts plans, so every step of one plan is
// checked, and only checks the model ran, so a superseded follow-up doesn't use it up. Declined and not_sent rows
// are not runs.
func alertRuns(db *sqlx.DB, run followupRun) (newer bool, runs int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	var counts struct {
		Newer int `db:"newer"`
		Runs  int `db:"runs"`
	}
	// A write approved in the alert's chat that is no step of the plan (planStep) never supersedes a step's run.
	err = db.GetContext(ctx, &counts, `SELECT count(*) FILTER (WHERE er.created_at > $3 AND NOT (COALESCE(er.data->>'source', '') = '`+chatSource+`'
		       AND er.data->'option' IS NULL AND COALESCE(er.data->>'step', '') = '')) AS newer,
		       count(DISTINCT COALESCE(er.data->>'plan_version', er.id::text)) FILTER (WHERE er.created_at <= $3
		           AND er.created_at > $3 - interval '24 hours' AND er.id::text <> $4
		           AND COALESCE(er.data->>'plan_version', er.id::text) <> $5
		           AND COALESCE(er.data->'followup'->>'message_id', '') <> '') AS runs
		  FROM event_resolution er JOIN events e ON e.id = er.event_id
		 WHERE er.type = 'CommandExecution' AND er.status <> 'Declined' AND COALESCE(er.data->>'not_sent', '') <> 'true'
		   AND e.cloud_account_id = $1::uuid AND COALESCE(NULLIF(e.fingerprint, ''), e.id::text) = $2`,
		run.AccountId, firstNonEmpty(run.Fingerprint, run.EventId), run.CreatedAt, run.Id, firstNonEmpty(runPlanVersion(run), run.Id))
	if err != nil {
		return false, 0, fmt.Errorf("alertRuns: %w", err)
	}
	return counts.Newer > 0, counts.Runs, nil
}

// runPlanVersion is the plan version run was decided on, or "".
func runPlanVersion(run followupRun) string {
	var d struct {
		PlanVersion string `json:"plan_version"`
	}
	_ = json.Unmarshal(run.Data, &d)
	return d.PlanVersion
}

// followupQuery is the turn's question: the run's facts. How to judge them is the agent's prompt.
func followupQuery(run followupRun) string {
	var recorded struct {
		Error    string `json:"error"`
		Output   string `json:"output"`
		ExitCode *int   `json:"exit_code"`
	}
	var state struct {
		Followup struct {
			Rechecks int    `json:"rechecks"`
			Reason   string `json:"recheck_reason"`
		} `json:"followup"`
	}
	_ = json.Unmarshal(run.Data, &recorded)
	_ = json.Unmarshal(run.Data, &state)
	who := "NudgeBee ran"
	if isManualAttempt(run.Data) {
		who = "A person ran outside NudgeBee"
	}
	q := fmt.Sprintf("Follow-up check on event %s, after a remediation step.\n%s, at %s UTC: %s\nRecorded result: %s.",
		run.EventId, who, run.CreatedAt.UTC().Format("2006-01-02 15:04"), run.Command, runOutcomeText(run.Status, readRunFacts(run.Data)))
	if recorded.ExitCode != nil {
		q += fmt.Sprintf(" Exit code %d.", *recorded.ExitCode)
	}
	if out := strings.TrimSpace(firstNonEmpty(recorded.Error, recorded.Output)); out != "" {
		q += "\nWhat the command printed, quoted as data:\n<<<\n" + truncate(out, 1500) + "\n>>>"
	}
	q += recheckNote(state.Followup.Reason, state.Followup.Rechecks)
	return q + "\nRead the plan (get_remediation_plan) and check whether the problem this alert reports is gone, then decide what comes next: report it fixed; say which of the plan's remaining steps a person should run next; write a different plan with propose_fix; or, if it is too early to tell, call schedule_recheck."
}

// endTurn records a finished turn: the follow-up ends, unless the turn reported, asked for a re-check
// (schedule_recheck) and has re-checks left, in which case it waits for the next one.
func endTurn(db *sqlx.DB, run followupRun, status core.ConversationStatus, summary string, logger *slog.Logger) {
	end := outcomeOfTurn(db, run, status, summary)
	if end["outcome"] == followupReported {
		summary, _ := end["summary"].(string)
		at, err := requestRecheck(db, run, summary)
		if err != nil {
			logger.Error("remediation_followups: re-check not scheduled", "error", err)
		}
		if at == "" && soakPending(run) {
			at, err = requestSoakRecheck(db, run, summary)
			if err != nil {
				logger.Error("remediation_followups: soak re-check not scheduled", "error", err)
			}
		}
		if at != "" {
			logger.Info("remediation_followups: re-check scheduled", "recheck_at", at)
			postFollowupToThread(run, end, at)
			return
		}
	}
	finishFollowup(db, run, end, logger)
}

// soakReason is the re-check the first report of a change that took effect always gets: a pod seconds old can
// look healthy and crash again, so its report stands only after a second look (review of #41596).
const soakReason = "the first check ran right after the change; confirm the fix held"

// soakPending reports whether run's change took effect and its follow-up has not looked again yet.
func soakPending(run followupRun) bool {
	var d struct {
		Followup struct {
			Rechecks int `json:"rechecks"`
		} `json:"followup"`
	}
	_ = json.Unmarshal(run.Data, &d)
	return run.Status == events.RunStatusSuccess && d.Followup.Rechecks == 0
}

// requestSoakRecheck schedules the soak re-check at the shortest wait, as if the turn had asked for it.
func requestSoakRecheck(db *sqlx.DB, run followupRun, summary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	if _, err := events.RequestFollowupRecheck(ctx, db, run.Id, recheckMinMinutes, soakReason, followupMaxRechecks); err != nil {
		return "", err
	}
	return events.ScheduleFollowupRecheck(ctx, db, run.Id, summary, recheckMinMinutes, recheckMaxMinutes, followupMaxRechecks)
}

// requestRecheck turns the turn's recheck_request into the time to look again, and returns it; empty when
// the turn asked for none, proposed, or the re-checks are used up.
func requestRecheck(db *sqlx.DB, run followupRun, summary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	return events.ScheduleFollowupRecheck(ctx, db, run.Id, summary, recheckMinMinutes, recheckMaxMinutes, followupMaxRechecks)
}

// outcomeOfTurn is how a finished turn ends the follow-up. A proposal is read from the row, where
// propose_fix recorded it.
func outcomeOfTurn(db *sqlx.DB, run followupRun, status core.ConversationStatus, summary string) map[string]any {
	out := map[string]any{}
	if summary = strings.TrimSpace(summary); summary != "" {
		out["summary"] = truncate(summary, 2000) // a turn recorded after a crash has none; keep the last one
	}
	switch status {
	case core.ConversationStatusTerminated:
		out["outcome"] = followupStopped
	case core.ConversationStatusCompleted:
		out["outcome"] = followupReported
		if proposed, _ := events.FollowupProposedVersion(context.Background(), db, run.Id); proposed != "" {
			out["outcome"] = followupProposed
		}
	default:
		out["outcome"], out["reason"] = followupNeedsHuman, strings.ToLower(string(status))
	}
	return out
}

// finishFollowup records how a follow-up ended, once.
func finishFollowup(db *sqlx.DB, run followupRun, end map[string]any, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	patch, _ := json.Marshal(end)
	n, err := events.FinishFollowup(ctx, db, run.Id, string(patch))
	if err != nil {
		logger.Error("remediation_followups: not recorded", "error", err, "end", end["outcome"])
		return
	}
	if n == 1 {
		logger.Info("remediation_followups: done", "outcome", end["outcome"], "reason", end["reason"])
		postFollowupToThread(run, end, "")
	}
}

// planRunsAfter is, for each of the plan's steps, the index of the step it runs after (its After names
// that step's action), or -1.
func planRunsAfter(plan RemediationPlan) []int {
	key := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	byName := map[string]int{}
	for i, a := range plan.Actions {
		if k := key(a.Action); k != "" {
			byName[k] = i
		}
	}
	out := make([]int, len(plan.Actions))
	for i, a := range plan.Actions {
		out[i] = -1
		if p, ok := byName[key(a.After)]; ok && key(a.After) != "" && p != i {
			out[i] = p
		}
	}
	return out
}
