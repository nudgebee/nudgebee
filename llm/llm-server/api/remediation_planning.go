package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/llm/agents"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/budget"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// The background planner (#40734): with the loop on, Nubi writes every alert's remediation plan itself, in a
// read-only turn of the alert's conversation (event-<fingerprint>), after each completed investigation or when
// a person asks (for a plan, a re-check, or a different plan). The turn writes the plan with propose_fix; code
// only starts, bounds and records it. A request waits while the conversation is busy (one active message at a
// time; a pending approval keeps it WAITING) and is retried every minute; it ends with no plan, and a reason,
// when it waits too long or its turn fails. Requests are rows of events.AnalysisTypeRemediationPlanning.

// Planning turn kinds: what the turn answers. The kind rides in the turn's config (NBQueryConfig.RemediationPlanTurn).
const (
	planTurnAuto    = "auto"    // after an investigation
	planTurnAsk     = "ask"     // a person asked Nubi for a plan
	planTurnRecheck = "recheck" // a person asked Nubi to re-check a plan that may no longer apply
	planTurnReplan  = "replan"  // a person asked for a different plan, with reasons and a note
)

// Bounds on a request. Vars so tests can change them.
var (
	planningGiveUp        = time.Hour        // how long a request waits for the alert's conversation
	planningTurnTimeout   = 30 * time.Minute // how long a started turn may run
	planningMaxAttempts   = 2                // turns started for one request when one fails
	planningStartsPerTick = 3
	// At most planningPerTenantPerTick requests of one tenant move on per tick, each given planningStepDeadline
	// before the tick moves on (tickScheduler).
	planningPerTenantPerTick = 10
	planningStepDeadline     = 45 * time.Second
)

// planningTicks schedules the planning job's steps fairly across tenants.
var planningTicks = newTickScheduler("remediation_planning")

// RegisterRemediationPlanning starts the leader job that starts planning turns and records how they ended.
func RegisterRemediationPlanning() error {
	return common.NewLeaderIntervalJob("remediation_planning", runDuePlannings, time.Minute)
}

// planRequestInput is a request for a planning turn.
type planRequestInput struct {
	Kind    string
	By      string // the person who asked; "" for NudgeBee
	Reasons []string
	Note    string
	// OutOfDate: the caller found the event's plan no longer applies (planRelevance), so an automatic request is
	// not refused for the plan it has (queueChainPlannings).
	OutOfDate bool
}

// planningEvent is the event as a planning request reads it.
type planningEvent struct {
	Alert string `db:"alert"`
}

// planEventOf is the event whose plan eventId reads and acts on (events.PlanEventOf). A var so tests can stub it.
var planEventOf = func(ctx context.Context, db sqlx.QueryerContext, accountId, eventId string) (string, error) {
	return events.PlanEventOf(ctx, db, accountId, eventId)
}

// ownPlan reports whether eventId gets a plan of its own: not while it is a duplicate of a live main event whose
// plan applies (planEventOf).
func ownPlan(ctx context.Context, db sqlx.QueryerContext, accountId, eventId string) (bool, error) {
	planEvent, err := planEventOf(ctx, db, accountId, eventId)
	return planEvent == eventId, err
}

// incidentClosedAt is when the alert closed, or nil while it fires or cannot be read: the incident's state as
// the remediation read reports it (alertStateOf), decided by its newest firing, not by the plan's event alone.
func incidentClosedAt(inc *planEventFacts) *time.Time {
	if inc == nil {
		return nil
	}
	if state, at := alertStateOf(*inc); state == "closed" {
		return at
	}
	return nil
}

// planningIncident reads the request's incident (the plan's event and its later firings), or nil.
func planningIncident(row events.PlanningRequest) *planEventFacts {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil
	}
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	inc, err := readIncidentFacts(readCtx, dbManager.Db, row.AccountId, row.EventId)
	if err != nil {
		slog.Warn("remediation_planning: incident not read", "error", err, "event_id", row.EventId)
		return nil
	}
	return inc
}

// investigatedFiring is the incident's newest firing with a completed investigation, and when it fired; found is
// false when none has one. A var so tests can stub it.
var investigatedFiring = func(ctx context.Context, db *sqlx.DB, accountId, eventId string) (string, *time.Time, bool, error) {
	var firing struct {
		Id string     `db:"id"`
		At *time.Time `db:"at"`
	}
	err := db.GetContext(ctx, &firing, `SELECT d.id::text AS id, COALESCE(d.starts_at, d.created_at) AS at
		  FROM events e JOIN events d ON `+incidentFiringsOf+`
		 WHERE e.id = $1 AND e.cloud_account_id = $2::uuid
		   AND (EXISTS (SELECT 1 FROM event_log_analysis a WHERE a.event_id = d.id AND a.analysis_type = 'investigation' AND a.status = 'COMPLETED')
		     OR EXISTS (SELECT 1 FROM event_analysis_mapping m JOIN event_log_analysis a ON a.id = m.analysis_id
		                 WHERE m.event_id = d.id AND m.analysis_type = 'investigation' AND a.status = 'COMPLETED'))
		 ORDER BY d.created_at DESC LIMIT 1`, eventId, accountId)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("investigatedFiring: %w", err)
	}
	return firing.Id, firing.At, true, nil
}

// readPlanningEvent reads the event on accountId; found is false when there is none. A var so tests can stub it.
var readPlanningEvent = func(ctx context.Context, db *sqlx.DB, accountId, eventId string) (planningEvent, bool, error) {
	var e planningEvent
	err := db.GetContext(ctx, &e, `SELECT COALESCE(NULLIF(fingerprint, ''), id::text) AS alert
		  FROM events WHERE id = $1 AND cloud_account_id = $2::uuid`, eventId, accountId)
	if errors.Is(err, sql.ErrNoRows) {
		return e, false, nil
	}
	if err != nil {
		return e, false, fmt.Errorf("readPlanningEvent: %w", err)
	}
	return e, true, nil
}

// noPlanAskedReason is why an event has no plan when Nubi was never asked for one.
const noPlanAskedReason = "Nubi has not prepared a fix for this event yet"

// notOwnPlanReason is why a duplicate has no plan of its own.
const notOwnPlanReason = "this event is a duplicate of a live main event; the main event's fixes apply"

// requestBackgroundPlan asks Nubi for the event's plan and returns at once: the request is recorded and its turn
// started now if the conversation is free, else by the job. refusal, when set, is why nothing was asked (the
// person reads it). An open request for the event is returned instead of a second one.
func requestBackgroundPlan(ctx *security.RequestContext, accountId, eventId string, in planRequestInput) (events.PlanningRequest, string, error) {
	if !events.RemediationLoopOn(accountId) {
		return events.PlanningRequest{}, "the remediation loop is off for this account", nil
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return events.PlanningRequest{}, "", fmt.Errorf("requestBackgroundPlan: %w", err)
	}
	db := dbManager.Db
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	ev, found, err := readPlanningEvent(readCtx, db, accountId, eventId)
	if err != nil {
		return events.PlanningRequest{}, "", err
	}
	if !found {
		return events.PlanningRequest{}, "event not found", nil
	}
	if own, err := ownPlan(readCtx, db, accountId, eventId); err != nil {
		return events.PlanningRequest{}, "", fmt.Errorf("requestBackgroundPlan: %w", err)
	} else if !own {
		return events.PlanningRequest{}, notOwnPlanReason, nil
	}
	stored, hasPlan := loadSavedPlan(ctx, accountId, eventId)
	if in.Kind == planTurnAuto && hasPlan && !stored.Stale && !in.OutOfDate {
		return events.PlanningRequest{}, "the alert already has fixes prepared from this investigation", nil
	}
	if in.Kind == planTurnRecheck && !hasPlan {
		in.Kind = planTurnAsk
	}
	if in.Kind == planTurnReplan && !hasPlan {
		return events.PlanningRequest{}, "this event has no fixes to replace; ask Nubi to prepare a fix", nil
	}
	if in.Kind != planTurnAuto {
		busy, err := events.AlertRunInFlight(readCtx, db, accountId, ev.Alert)
		if err != nil {
			return events.PlanningRequest{}, "", fmt.Errorf("requestBackgroundPlan: %w", err)
		}
		if busy {
			return events.PlanningRequest{}, "a step of this alert is running or its follow-up check has not finished; ask again when the check reports", nil
		}
	}
	if in.Kind == planTurnReplan {
		if refusal, err := replanCapRefusal(readCtx, db, accountId, ev.Alert); err != nil || refusal != "" {
			return events.PlanningRequest{}, refusal, err
		}
	}
	req := events.PlanningRequest{Kind: in.Kind, RequestedBy: in.By, Reasons: in.Reasons, Note: truncate(strings.TrimSpace(in.Note), 2000)}
	if hasPlan {
		req.BaseVersion = stored.Version
	}
	row, queued, err := events.EnqueuePlanning(readCtx, db, accountId, eventId, req)
	if err != nil {
		return events.PlanningRequest{}, "", fmt.Errorf("requestBackgroundPlan: %w", err)
	}
	// A person's request takes over an automatic re-check parked behind an approval: theirs is what they asked for.
	if !queued && row.State == events.PlanningParked && in.Kind != planTurnAuto {
		if moved, err := events.TakeOverParkedPlanning(readCtx, db, row.Id, req); err != nil {
			return events.PlanningRequest{}, "", fmt.Errorf("requestBackgroundPlan: %w", err)
		} else if moved {
			if latest, err := events.LatestPlanning(readCtx, db, accountId, eventId); err == nil && latest != nil {
				row = *latest
			}
			queued = true
		}
	}
	if !queued && in.Kind == planTurnReplan && row.Kind != planTurnReplan {
		return row, "Nubi is already writing this alert's plan; ask for a different one when it is ready", nil
	}
	if queued {
		kickPlanning(db, row)
	}
	return row, "", nil
}

// kickPlanning tries to start a new request's turn now rather than at the next tick. A var so tests can skip it.
var kickPlanning = func(db *sqlx.DB, row events.PlanningRequest) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("remediation_planning: panic", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		advancePlanning(db, row)
	}()
}

// replanCapRefusal is why a replan is refused by the alert's 24-hour cap on plans proposed in its chat, or "": a
// replan is a person's chat turn, so it counts toward the same cap.
func replanCapRefusal(ctx context.Context, db *sqlx.DB, accountId, alert string) (string, error) {
	max := config.Config.RemediationFollowupMaxRuns
	if max <= 0 {
		return "", nil
	}
	proposed, err := chatProposalCount(ctx, db, events.SessionIdPrefixEvent+alert, accountId)
	if err != nil {
		return "", err
	}
	if proposed >= max {
		return fmt.Sprintf("This alert already has %d plans proposed in its chat in the last 24 hours, the limit, and a replan counts toward it", proposed), nil
	}
	return "", nil
}

// runDuePlannings moves every open request on: start its turn, or record how its turn ended.
func runDuePlannings() error {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("remediation_planning: %w", err)
	}
	// Recurring alerts run no investigation of their own, so nothing else asks for their plan.
	queueChainPlannings(dbManager.Db)
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	open, err := events.OpenPlannings(ctx, dbManager.Db, 100)
	if err != nil {
		return fmt.Errorf("remediation_planning: %w", err)
	}
	var starts atomic.Int32
	items := make([]tickItem, 0, len(open))
	for _, row := range open {
		items = append(items, tickItem{Key: row.Id, AccountId: row.AccountId, Run: func() {
			if row.State == events.PlanningQueued && int(starts.Load()) >= planningStartsPerTick {
				return // started next tick
			}
			if advancePlanning(dbManager.Db, row) {
				starts.Add(1)
			}
		}})
	}
	planningTicks.each(items, planningPerTenantPerTick, planningStepDeadline)
	return nil
}

// advancePlanning moves one open request on and reports whether it started a turn.
func advancePlanning(db *sqlx.DB, row events.PlanningRequest) bool {
	logger := slog.With("planning_id", row.Id, "event_id", row.EventId, "account_id", row.AccountId, "kind", row.Kind)
	if row.Kind == planTurnAuto && (row.State == events.PlanningQueued || row.State == events.PlanningParked) {
		if hold := holdAutomaticRecheck(db, &row, logger); hold {
			return false
		}
	}
	if row.State == events.PlanningQueued {
		if time.Since(row.RequestedAt) >= planningGiveUp {
			endPlanning(db, row, fmt.Sprintf("it could not start: the alert's conversation stayed busy for %s", planningGiveUp), logger)
			return false
		}
		stop, wait := planningStop(db, row, logger)
		switch {
		case stop != "":
			endPlanning(db, row, stop, logger)
			return false
		case wait:
			return false
		}
		return startPlanningTurn(db, row, logger)
	}
	status, _, found := planningTurnState(row)
	switch {
	case !found:
		// The turn never started (the conversation was busy, or the process stopped first): start it again.
		_, _ = events.SetPlanningTurn(context.Background(), db, row.Id, row.MessageId, "")
	case status == core.ConversationStatusInProgress || status == core.ConversationStatusPending ||
		status == core.ConversationStatusQueued || status == core.ConversationStatusWaiting || status == core.ConversationStatusWaitingForClientTool:
		if row.StartedAt == nil || time.Since(*row.StartedAt) >= planningTurnTimeout {
			endPlanning(db, row, fmt.Sprintf("its turn did not finish in %s", planningTurnTimeout), logger)
		}
	case status == core.ConversationStatusFailed || status == core.ConversationStatusKilled:
		if row.Attempts < planningMaxAttempts {
			_, _ = events.SetPlanningTurn(context.Background(), db, row.Id, row.MessageId, "") // started again
			return false
		}
		endPlanning(db, row, "its turn failed", logger)
	case status == core.ConversationStatusTerminated:
		endPlanning(db, row, "a person stopped its turn", logger)
	default:
		// propose_fix ends the request when it stores the plan, so a finished turn with the request open wrote none.
		// A model outage can end a turn that way (the planner summarizes what it has), so it gets another turn too.
		// Its answer can still read as if the plan changed, so the turn's message says no plan was stored.
		reason := noPlanReason(planningTurnRefusal(db, row))
		retry := row.Attempts < planningMaxAttempts
		noteNoPlanStored(db, row, reason, retry, logger)
		if retry {
			_, _ = events.SetPlanningTurn(context.Background(), db, row.Id, row.MessageId, "") // started again
			return false
		}
		endPlanning(db, row, reason, logger)
	}
	return false
}

// holdAutomaticRecheck applies the rule for an automatic re-check (after an investigation) that has not started: a
// step that ran since it was asked drops it (dropped, ran), since the run's follow-up owns what comes next; an
// approval waiting in the alert's chat parks it, without the give-up clock; once that approval is answered without a
// run (denied, or lapsed) it is queued again and runs. It reports whether the request is held (or ended) this tick.
func holdAutomaticRecheck(db *sqlx.DB, row *events.PlanningRequest, logger *slog.Logger) bool {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	ran, err := events.PlanningRanSince(ctx, db, row.Id)
	if err != nil {
		logger.Warn("remediation_planning: runs since the request not read", "error", err)
		return true
	}
	if ran {
		if ended, err := events.FinishPlanning(ctx, db, row.Id, events.PlanningDropped, "", "ran"); err == nil && ended {
			logger.Info("remediation_planning: automatic re-check dropped, a step ran since it was asked")
		}
		return true
	}
	waiting := alertApprovalWaiting(*row)
	switch {
	case waiting && row.State == events.PlanningQueued:
		if parked, err := events.ParkPlanning(ctx, db, row.Id); err == nil && parked {
			logger.Info("remediation_planning: automatic re-check parked behind a waiting approval")
		}
		return true
	case waiting:
		return true
	case row.State == events.PlanningParked:
		if moved, err := events.UnparkPlanning(ctx, db, row.Id); err != nil || !moved {
			return true
		}
		row.State, row.RequestedAt = events.PlanningQueued, time.Now().UTC()
		logger.Info("remediation_planning: approval answered without a run, the parked re-check runs")
	}
	return false
}

// alertApprovalWaiting reports whether a turn in the alert's chat (event-<alert>) waits on a person: an approval.
// A var so tests can stub it.
var alertApprovalWaiting = func(row events.PlanningRequest) bool {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	var waiting bool
	if err := dbManager.Db.GetContext(ctx, &waiting, `SELECT EXISTS (SELECT 1 FROM llm_conversations c
		  JOIN llm_conversation_messages m ON m.conversation_id = c.id
		 WHERE c.session_id = $1 AND c.account_id = $2::uuid AND m.status IN ('WAITING', 'WAITING_FOR_CLIENT_TOOL')
		   AND `+approvalConversationLive+`)`,
		events.SessionIdPrefixEvent+row.Alert, row.AccountId); err != nil {
		return false
	}
	return waiting
}

// noPlanReason says why a turn that finished wrote no plan: propose_fix refused its last plan (refusal), or it
// answered without calling propose_fix. The answer itself is not the reason: it can describe a plan as if stored.
// Like every stored reason it is the bare cause; noFixReason words it for the person.
func noPlanReason(refusal string) string {
	if refusal = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(refusal), "propose_fix:")); refusal != "" {
		return "propose_fix refused it: " + truncate(refusal, 300)
	}
	return "it answered without proposing one"
}

// noFixReason is a request's stored reason as the person reads it ("Nubi prepared no fix: <cause>"). Rows stored
// before the wording changed carry their own "Nubi wrote no plan" prefix, which it replaces.
func noFixReason(reason string) string {
	const prefix = "Nubi prepared no fix"
	reason = strings.TrimSpace(reason)
	for _, legacy := range []string{"Nubi wrote no plan", prefix} {
		if rest, ok := strings.CutPrefix(reason, legacy); ok {
			reason = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
		}
	}
	if reason == "" {
		return prefix
	}
	return prefix + ": " + reason
}

// planningTurnRefusal is the refusal of the turn's last propose_fix call when that call failed, or "" when the turn
// did not call it or the last call succeeded.
func planningTurnRefusal(db *sqlx.DB, row events.PlanningRequest) string {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	var last struct {
		Status   string `db:"status"`
		Response string `db:"response"`
	}
	if err := db.GetContext(ctx, &last, `SELECT COALESCE(status, '') AS status, COALESCE(response, '') AS response FROM llm_conversation_tool_calls
		 WHERE message_id = $1::uuid AND tool_name = $2 ORDER BY created_at DESC LIMIT 1`, row.MessageId, toolcore.ToolProposeFix); err != nil {
		return ""
	}
	if strings.EqualFold(last.Status, "success") {
		return ""
	}
	if i := strings.Index(last.Response, "\n"); i > 0 {
		last.Response = last.Response[:i] // the first line says why; the rest is the live state it read
	}
	return last.Response
}

// noPlanStoredMarker opens the note a planning turn's message gets when the turn stored no plan.
const noPlanStoredMarker = "**No new fixes were stored.**"

// noteNoPlanStored appends to the turn's message that no plan was stored and why, once, so an answer that reads as
// if the plan changed is not taken for one.
func noteNoPlanStored(db *sqlx.DB, row events.PlanningRequest, reason string, retry bool, logger *slog.Logger) {
	next := "The alert's fixes are unchanged."
	if row.BaseVersion == "" {
		next = "The alert has no fixes from this request."
	}
	if retry {
		next += " Nubi tries once more."
	}
	note := "\n\n---\n" + noPlanStoredMarker + " " + noFixReason(reason) + ". " + next
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, `UPDATE llm_conversation_messages SET response = COALESCE(response, '') || $2
		 WHERE id = $1::uuid AND position($3 in COALESCE(response, '')) = 0`, row.MessageId, note, noPlanStoredMarker); err != nil {
		logger.Warn("remediation_planning: no-plan note not added", "error", err, "message_id", row.MessageId)
	}
}

// endPlanning ends an open request with no plan.
func endPlanning(db *sqlx.DB, row events.PlanningRequest, reason string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	ended, err := events.FinishPlanning(ctx, db, row.Id, events.PlanningNone, "", reason)
	if err != nil {
		logger.Error("remediation_planning: end not recorded", "error", err, "reason", reason)
		return
	}
	if ended {
		logger.Info("remediation_planning: ended with no plan", "reason", reason)
	}
}

// planningStop is why a queued request ends before its turn starts, or wait while it must wait for the alert:
// hard limits, not judgement.
func planningStop(db *sqlx.DB, row events.PlanningRequest, logger *slog.Logger) (stop string, wait bool) {
	if !events.RemediationLoopOn(row.AccountId) {
		return "the remediation loop is off for this account", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	ev, found, err := readPlanningEvent(ctx, db, row.AccountId, row.EventId)
	switch {
	case err != nil:
		logger.Warn("remediation_planning: event not read", "error", err)
		return "", true
	case !found:
		return "event not found", false
	}
	if own, err := ownPlan(ctx, db, row.AccountId, row.EventId); err != nil {
		logger.Warn("remediation_planning: plan event not read", "error", err)
		return "", true
	} else if !own {
		return notOwnPlanReason, false
	}
	busy, err := events.AlertRunInFlight(ctx, db, row.AccountId, ev.Alert)
	if err != nil || busy {
		return "", true // a run or its follow-up owns the alert's plan until it reports
	}
	tenant, err := security.GetTenantIdFromAccountId(row.AccountId)
	if err != nil {
		logger.Warn("remediation_planning: tenant not read", "error", err)
		return "", true
	}
	if exceeded, _ := budget.CheckBudgetLimits(tenant, row.AccountId, budget.ModuleInvestigation, logger); exceeded {
		return "the AI budget is used up", false
	}
	return "", false
}

// personAskedAt is when a person asked for row's turn, nil when NudgeBee asked for it. A person's request made after
// someone stopped the alert's conversation may start there (core automaticTurnIdle); NudgeBee's own may not.
func personAskedAt(row events.PlanningRequest) *time.Time {
	if planRequester(row) == "" || row.RequestedAt.IsZero() {
		return nil
	}
	at := row.RequestedAt
	return &at
}

// planningConversationIdle reports whether the alert's conversation can take a turn now, or has none yet: no turn
// is active there, it is not stopped by a person unless that person asked after (core AutomaticTurnIdle), and the alert's investigation does not
// hold it. Read before the turn's evidence is built, so a busy conversation costs a read or two per tick. A var so
// tests can stub it.
var planningConversationIdle = func(row events.PlanningRequest) bool {
	dao := core.GetConversationDao()
	conversation, err := dao.GetConversationBySession(row.AccountId, events.SessionIdPrefixEvent+row.Alert)
	if err != nil {
		return false
	}
	if conversation.ID != uuid.Nil {
		ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
		defer cancel()
		if idle, err := dao.AutomaticTurnIdle(ctx, conversation.ID.String(), row.AccountId, personAskedAt(row)); err != nil || !idle {
			return false
		}
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false
	}
	_, mayStart := pipelineHoldKey(dbManager.Db, row.AccountId, row.Alert, slog.With("planning_id", row.Id, "event_id", row.EventId))
	return mayStart
}

// startPlanningTurn saves row's turn and claims the alert's conversation in one step, only while no other turn is
// active (else the next tick retries), and reports whether it did. The turn runs in the background.
func startPlanningTurn(db *sqlx.DB, row events.PlanningRequest, logger *slog.Logger) bool {
	if !planningConversationIdle(row) {
		return false
	}
	ctx, refusal := planningContext(row)
	if refusal != "" {
		endPlanning(db, row, refusal, logger)
		return false
	}
	if ctx == nil {
		return false
	}
	query, reason := planningQuery(ctx, row)
	if reason != "" {
		endPlanning(db, row, reason, logger)
		return false
	}
	messageId := uuid.New()
	if set, err := events.SetPlanningTurn(context.Background(), db, row.Id, "", messageId.String()); err != nil || !set {
		return false // another worker started it
	}
	conversationId, started, err := startPlanningMessage(row, messageId, query)
	if err != nil || !started {
		if err != nil {
			logger.Warn("remediation_planning: turn not started", "error", err)
		}
		_, _ = events.SetPlanningTurn(context.Background(), db, row.Id, messageId.String(), "")
		return false
	}
	logger.Info("remediation_planning: turn started", "message_id", messageId)
	goPlanningTurn(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("remediation_planning: panic", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		if err := runPlanningTurn(row, conversationId, messageId, query); err != nil {
			logger.Warn("remediation_planning: turn failed", "error", err)
		}
	})
	return true
}

// goPlanningTurn runs a planning turn in the background. A var so tests can run it in place.
var goPlanningTurn = func(turn func()) { go turn() }

// planningContext is who row's turn runs as, read-only: the person who asked for it, with their own access, when
// a person did (refusal says why it may not run: they no longer have write access to the account); NudgeBee on the
// account alone for an automatic one. nil, with no refusal, when it cannot be built now (tenant or the person's
// access unreadable): the next tick tries again.
func planningContext(row events.PlanningRequest) (ctx *security.RequestContext, refusal string) {
	tenant, err := security.GetTenantIdFromAccountId(row.AccountId)
	if err != nil || tenant == "" {
		return nil, ""
	}
	person := planRequester(row)
	if person == "" {
		return core.WithReadOnlyRun(security.NewRequestContextForTenantAccountAdmin(tenant, security.GetSystemUserId(), []string{row.AccountId})), ""
	}
	sc, err := loadPersonSecurityContext(tenant, person)
	if err != nil || sc == nil {
		slog.Warn("remediation_planning: the requester's access not read", "error", err, "planning_id", row.Id)
		return nil, ""
	}
	if refusal := planWriteRefusal(sc, row.AccountId); refusal != "" {
		return nil, refusal
	}
	return core.WithReadOnlyRun(security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)), ""
}

// planRequester is the person who asked for row's turn, or "" for an automatic one (after an investigation, or
// by NudgeBee).
func planRequester(row events.PlanningRequest) string {
	by := strings.TrimSpace(row.RequestedBy)
	if row.Kind == planTurnAuto || by == "" || by == security.GetSystemUserId() {
		return ""
	}
	return by
}

// loadPersonSecurityContext is a person's access, as the services server reports it. A var so tests can stub it.
var loadPersonSecurityContext = security.NewSecurityContext

// planNoWriteAccess is why a person without write access to the account cannot ask for fixes.
const planNoWriteAccess = "asking Nubi for fixes needs write access to this account"

// planWriteRefusal is why sc may not ask for the account's fixes, or "": a plan is a set of changes to run there,
// so asking for one needs write access (a read-only role, or a run-AI grant alone, does not do).
func planWriteRefusal(sc *security.SecurityContext, accountId string) string {
	if sc == nil || !sc.HasAccountAccess(accountId, security.SecurityAccessTypeUpdate) {
		return planNoWriteAccess
	}
	return ""
}

// planningConfig is a planning turn's config, saved with its message so a recovered turn keeps it: read-only with
// strict reads, like the follow-up check; only the current firing's evidence (the conversation spans earlier
// firings); and the marker propose_fix writes the request's plan by.
// planningTurnConfig is row's turn config: planningConfig with the one-line note the app shows for the turn's message
// instead of a person's bubble, and what the turn's knowledge search is told about the event (planningKnowledgeHints).
func planningTurnConfig(row events.PlanningRequest) toolcore.NBQueryConfig {
	config := planningConfig(row.Kind)
	config.SystemNote = planningSystemNote(row)
	config.KnowledgeHints = planningKnowledgeHints(row)
	return config
}

// planningKnowledgeHints names the kind of row's event for the turn's knowledge search, as the investigation's hints
// do. Without them the search runs on the planning instruction alone, which never names the alert, and a team's SOP
// for it is not found (#42793). Only the hints: a workload in the config would also make the turn's metric and log
// tools refuse to pick a target on their own. Empty when the event cannot be read. A var so tests can stub it.
var planningKnowledgeHints = func(row events.PlanningRequest) []string {
	if row.EventId == "" {
		return nil
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	var e events.Event
	if err := dbManager.Db.QueryRowContext(ctx, `SELECT COALESCE(category, ''), COALESCE(finding_type, ''), COALESCE(aggregation_key, '')
		FROM events WHERE id = $1 AND cloud_account_id = $2`, row.EventId, row.AccountId).Scan(&e.Category, &e.FindingType, &e.AggregationKey); err != nil {
		slog.Warn("remediation_planning: the event's knowledge hints not read", "error", err, "planning_id", row.Id, "event_id", row.EventId)
		return nil
	}
	return eventKnowledgeHints(e)
}

// planningSystemNote is the one-line note for a planning turn's message.
func planningSystemNote(row events.PlanningRequest) string {
	switch row.Kind {
	case planTurnRecheck:
		return "Nubi was asked to re-check the fix"
	case planTurnReplan:
		note := "A person asked Nubi for other fixes"
		if why := strings.TrimSpace(strings.Join(append(append([]string{}, row.Reasons...), row.Note), "; ")); strings.Trim(why, "; ") != "" {
			note += ": " + truncate(strings.Trim(why, "; "), 200)
		}
		return note
	case planTurnAsk:
		return "Nubi was asked to prepare fixes"
	}
	return "Nubi was asked to prepare fixes after the investigation"
}

func planningConfig(kind string) toolcore.NBQueryConfig {
	return toolcore.NBQueryConfig{ReadOnlyRun: true, StrictReadOnly: true, SkipConversationHistory: true, RemediationPlanTurn: kind}
}

// startPlanningMessage saves the turn's message in the alert's conversation, creating the conversation when the
// alert has none, and claims it for the turn (core StartIdleTurn); false while it is busy. A replan's message is
// the person's. A var so tests can stub it.
var startPlanningMessage = func(row events.PlanningRequest, messageId uuid.UUID, query string) (string, bool, error) {
	dao := core.GetConversationDao()
	conversationId, err := ensureAlertChat(row.AccountId, row.EventId, row.Alert)
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	holdKey, mayStart := "", true
	if dbManager, err := common.GetDatabaseManager(common.Metastore); err == nil {
		holdKey, mayStart = pipelineHoldKey(dbManager.Db, row.AccountId, row.Alert, slog.With("planning_id", row.Id, "event_id", row.EventId))
	}
	if !mayStart {
		return conversationId.String(), false, nil
	}
	started, err := dao.StartIdleTurn(ctx, conversationId.String(), row.AccountId, messageId.String(), query, agents.RemediationAgentName, planningTurnConfig(row), holdKey, personAskedAt(row))
	if err != nil || !started {
		return conversationId.String(), started, err
	}
	if row.Kind == planTurnReplan && row.RequestedBy != "" {
		if dbManager, err := common.GetDatabaseManager(common.Metastore); err == nil {
			if _, err := dbManager.Db.ExecContext(ctx, `UPDATE llm_conversation_messages SET user_id = $2 WHERE id = $1`, messageId, row.RequestedBy); err != nil {
				slog.Warn("remediation_planning: replan message not attributed to the person", "error", err, "message_id", messageId)
			}
		}
	}
	return conversationId.String(), true, nil
}

// planningTurnState is the status and answer of row's turn, and whether its message exists. A var so tests can stub it.
var planningTurnState = func(row events.PlanningRequest) (core.ConversationStatus, string, bool) {
	return followupTurnState(followupRun{AccountId: row.AccountId, EventId: row.EventId, Fingerprint: row.Alert, MessageId: row.MessageId})
}

// runPlanningTurn runs row's started turn messageId: the remediation agent, read-only, as NudgeBee with only the
// account. A var so tests can stub it.
var runPlanningTurn = func(row events.PlanningRequest, conversationId string, messageId uuid.UUID, query string) error {
	ctx, refusal := planningContext(row)
	if ctx == nil {
		return errors.New(firstNonEmpty(refusal, "the turn's identity could not be read"))
	}
	agent, found := core.GetNBAgent(ctx, agents.RemediationAgentName, row.AccountId, core.AgentStatusEnabled)
	if !found || agent == nil {
		return errors.New("the remediation agent is not available")
	}
	conversation, err := uuid.Parse(conversationId)
	if err != nil {
		return fmt.Errorf("conversation id: %w", err)
	}
	_, err = core.HandleConversationSessionRequest(ctx, agent, "", row.AccountId, events.SessionIdPrefixEvent+row.Alert, query,
		core.ConversationSessionRequestWithConversationId(uuid.NullUUID{UUID: conversation, Valid: true}),
		core.ConversationSessionRequestWithMessageId(uuid.NullUUID{UUID: messageId, Valid: true}),
		// The message was saved and the conversation claimed for it (StartIdleTurn), as a queue claim does.
		core.ConversationSessionRequestWithQueuedDispatch(),
		core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
		core.ConversationSessionRequestWithoutConversationHistory(),
		core.ConversationSessionRequestWithConfig(planningTurnConfig(row)))
	return err
}

// planningEvidence is what the turn plans from: the same facts the one-shot planner was given, of the plan's
// event, and the stored investigation of evidenceEventId (the incident's newest investigated firing). A var so
// tests can stub it.
var planningEvidence = func(ctx *security.RequestContext, accountId, eventId, evidenceEventId string) (string, error) {
	evidence, _, err := remediationEvidence(ctx, accountId, evidenceEventId, "")
	if err != nil || strings.TrimSpace(evidence) == "" {
		return "", err
	}
	parts := []string{}
	if account := describeRemediationAccount(accountId, eventId); account != "" {
		parts = append(parts, account)
	}
	if facts, _ := buildRemediationFacts(ctx, accountId, eventId); facts != "" {
		parts = append(parts, facts)
	}
	return strings.Join(append(parts, evidence), "\n\n"), nil
}

// planningQuery is the turn's message: what to write, then the evidence. reason is set when there is nothing to
// plan from.
func planningQuery(ctx *security.RequestContext, row events.PlanningRequest) (query, reason string) {
	inc := planningIncident(row)
	evidenceEvent, evidenceAt := row.EventId, (*time.Time)(nil)
	if inc != nil {
		created := inc.CreatedAt
		evidenceAt = firstTime(inc.StartsAt, &created)
	}
	if dbManager, err := common.GetDatabaseManager(common.Metastore); err == nil {
		readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
		if id, at, found, err := investigatedFiring(readCtx, dbManager.Db, row.AccountId, row.EventId); err == nil && found {
			evidenceEvent, evidenceAt = id, at
		}
		cancel()
	}
	evidence, err := planningEvidence(ctx, row.AccountId, row.EventId, evidenceEvent)
	if err != nil {
		return "", "the investigation could not be read: " + truncate(err.Error(), 200)
	}
	if evidence == "" {
		return "", "the event has no completed investigation to plan from"
	}
	// The heading names the event the investigation was run for, which can be another firing's (reused).
	if source, at, ok := investigationSource(ctx, row.AccountId, evidenceEvent); ok {
		evidenceEvent, evidenceAt = source, at
	}
	firing := false
	if inc != nil {
		state, _ := alertStateOf(*inc)
		firing = state == "firing"
	}
	query = planningRequestText(row, incidentClosedAt(inc), firing)
	// The automations a step may run (#42459); none listed, none offered.
	if catalog := automationCatalogText(planAutomations(row.AccountId)); catalog != "" {
		query += "\n\n" + catalog
	}
	return query + "\n\n" + evidenceHeading(inc, row.EventId, evidenceEvent, evidenceAt) + "\n" + evidence, ""
}

// investigationSource is the event the investigation read for eventId was run for (an investigation is reused
// across the alert's firings) and when that event fired; false when it cannot be read. A var so tests can stub it.
var investigationSource = func(ctx *security.RequestContext, accountId, eventId string) (string, *time.Time, bool) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", nil, false
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return "", nil, false
	}
	analysis, err := repo.GetEventAnalysis(ctx, eventId, firstNonEmpty(info.Fingerprint, eventId), info.AggregationKey, accountId, events.AnalysisTypeInvestigation)
	if err != nil || analysis == nil || strings.TrimSpace(analysis.RelatedEventId) == "" {
		return "", nil, false
	}
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	var at time.Time
	if err := dbManager.Db.GetContext(readCtx, &at, `SELECT COALESCE(starts_at, created_at) FROM events WHERE id = $1 AND cloud_account_id = $2::uuid`,
		analysis.RelatedEventId, accountId); err != nil {
		return analysis.RelatedEventId, nil, true
	}
	return analysis.RelatedEventId, &at, true
}

// evidenceHeading names the firing the investigation is of: the alert's current one, or an earlier one with when
// it fired and one line on the latest firing as stored (no new reads).
func evidenceHeading(inc *planEventFacts, planEventId, evidenceEventId string, evidenceAt *time.Time) string {
	if inc == nil {
		return "## Investigation of the alert's current firing"
	}
	latestId, latestAt, latestState := latestFiring(*inc, planEventId)
	if latestId == evidenceEventId {
		return "## Investigation of the alert's current firing" + firedAtText(evidenceAt)
	}
	return fmt.Sprintf("## Investigation of an earlier firing (event %s)%s\nLatest firing%s: %s.", evidenceEventId, firedAtText(evidenceAt),
		strings.TrimPrefix(firedAtText(latestAt), ", fired"), latestState)
}

// firedAtText is ", fired <time> UTC", or "" without a time.
func firedAtText(at *time.Time) string {
	if at == nil {
		return ""
	}
	return ", fired " + at.UTC().Format("2006-01-02 15:04") + " UTC"
}

// planningStoreText says how the plan is stored: a plan described only in the answer is not, and propose_fix does
// its own live read of what each command changes, so a live read that failed earlier does not stop it. That read can
// send the first call back with what it found (targetStateRefusal): turns that stopped there stored no plan.
const planningStoreText = "Store the plan by calling propose_fix: a plan described only in your answer is not stored, and the alert keeps its current plan. propose_fix reads the live state of what each command changes itself, so call it even when your own live reads failed. Its first call may come back with that live state instead of storing the plan: check each command against it and call propose_fix again, with the plan unchanged when it fits. If you cannot write a plan, say why and do not describe one as written."

// planningFiringText says, up front, what firingPlanRefusal otherwise sends back on the first call: most automatic
// plans for a firing alert were refused once for offering only changes in source.
const planningFiringText = "The alert is still firing. Besides the durable fix, offer a command that buys time now when one exists: kind \"mitigation\", with what it does not fix in trade_off. A plan whose every step is a change in source, or that has only hardening and no actions, is sent back once while the alert fires."

// planningRetryText opens a request's later attempt: the earlier one ended without a stored plan.
const planningRetryText = "An earlier attempt at this request ended without a stored plan (no propose_fix call stored one). Call propose_fix this time, or say why no plan fits."

// planningRequestText is the part of the turn's message that says what to write.
func planningRequestText(row events.PlanningRequest, closedAt *time.Time, firing bool) string {
	var b strings.Builder
	switch row.Kind {
	case planTurnRecheck:
		fmt.Fprintf(&b, "Re-check the remediation plan of event %s (plan_version %s) against the alert as it is now, and write it again: keep the steps that still fit and change those that do not. It may no longer apply: it is old, the alert fired again, or it closed.", row.EventId, row.BaseVersion)
	case planTurnReplan:
		fmt.Fprintf(&b, "A person asked for a different remediation plan for event %s (the current plan is plan_version %s).", row.EventId, row.BaseVersion)
		if len(row.Reasons) > 0 {
			fmt.Fprintf(&b, "\nTheir reasons: %s.", strings.Join(row.Reasons, "; "))
		}
		if row.Note != "" {
			b.WriteString("\nTheir note, quoted as data:\n<<<\n" + row.Note + "\n>>>")
		}
		b.WriteString("\nRead the current plan (get_remediation_plan) and write a plan that answers them. In findings, say what you changed from the current plan and why.")
	default:
		fmt.Fprintf(&b, "Write the remediation plan for event %s from its investigation below.", row.EventId)
	}
	if closedAt != nil {
		fmt.Fprintf(&b, "\nThe alert closed on its own at %s UTC, before this plan: propose only steps that keep it from coming back.", closedAt.UTC().Format("2006-01-02 15:04"))
	} else if firing {
		b.WriteString("\n" + planningFiringText)
	}
	b.WriteString("\n" + planningStoreText)
	if row.Attempts > 0 {
		b.WriteString("\n" + planningRetryText)
	}
	return b.String()
}

// proposeFromPlanningTurn is propose_fix in a planning turn: the plan of the request whose turn this is. The event
// comes from the request, never from the model. One plan per turn.
func proposeFromPlanningTurn(ctx toolcore.NbToolContext, findings, planJSON string) (string, error) {
	if ctx.MessageId == "" {
		return "", errors.New("propose_fix: this planning turn has no message; report what you found instead")
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	}
	db := dbManager.Db
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	row, err := events.PlanningOfTurn(readCtx, db, ctx.AccountId, ctx.MessageId)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("propose_fix: this planning turn has ended or already wrote its plan. Report what you found instead")
	}
	if err != nil {
		return "", fmt.Errorf("propose_fix: reading the planning request: %w", err)
	}
	busy, err := events.AlertRunInFlight(readCtx, db, row.AccountId, row.Alert)
	if err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	}
	if busy {
		endPlanning(db, row, "a step of the alert started while Nubi was preparing fixes; the fixes being run were kept", ctx.Ctx.GetLogger())
		return "", errors.New("propose_fix: a step of this alert started running while you wrote the plan, so the plan being run is kept. Report what you found instead")
	}
	if row.Kind == planTurnReplan {
		if refusal, err := replanCapRefusal(readCtx, db, row.AccountId, row.Alert); err != nil {
			return "", fmt.Errorf("propose_fix: %w", err)
		} else if refusal != "" {
			endPlanning(db, row, refusal, ctx.Ctx.GetLogger())
			return "", errors.New("propose_fix: " + refusal)
		}
	}
	claimed, err := events.ClaimPlanningProposal(readCtx, db, row.Id)
	if err != nil {
		return "", fmt.Errorf("propose_fix: %w", err)
	}
	if !claimed {
		return "", errors.New("propose_fix: this planning turn is already writing its plan; it writes one")
	}
	plan, version, err := proposePlanningPlan(ctx.Ctx, row, turnOf(ctx), findings, planJSON)
	if err != nil {
		_ = events.ReleasePlanningProposal(context.Background(), db, row.Id)
		return "", fmt.Errorf("propose_fix: the plan was not stored (%v). Correct it and call propose_fix again", err)
	}
	if _, err := events.FinishPlanning(context.Background(), db, row.Id, events.PlanningDone, version, ""); err != nil {
		ctx.Ctx.GetLogger().Error("remediation_planning: plan stored but the request not ended", "error", err, "planning_id", row.Id)
	}
	return proposalText(plan), nil
}

// proposePlanningPlan stores planJSON as the request's plan and returns it with its version. A plan with no
// actions is stored (Nubi found nothing to run); one the alert closed before gets "Closed on its own at ...".
// A var so tests can stub it.
var proposePlanningPlan = func(ctx *security.RequestContext, row events.PlanningRequest, turn planTurn, findings, planJSON string) (RemediationPlan, string, error) {
	var written struct {
		Actions []json.RawMessage `json:"actions"`
	}
	_ = json.Unmarshal([]byte(planJSON), &written)
	closedAt := incidentClosedAt(planningIncident(row))
	return storeNewPlanWith(ctx, row.AccountId, row.EventId, writtenPlan(planJSON), len(written.Actions), planStoreOptions{
		allowEmpty: true,
		revision:   &PlanRevision{Kind: row.Kind, Reasons: row.Reasons, Note: row.Note, Findings: truncate(strings.TrimSpace(findings), 2000)},
		adjust:     func(plan *RemediationPlan) { markClosedOnItsOwn(plan, closedAt) },
		turn:       turn,
	})
}

// markClosedOnItsOwn starts the summary of a plan written after the alert closed with when it closed.
func markClosedOnItsOwn(plan *RemediationPlan, closedAt *time.Time) {
	if closedAt == nil {
		return
	}
	prefix := fmt.Sprintf("Closed on its own at %s UTC.", closedAt.UTC().Format("2006-01-02 15:04"))
	if !strings.HasPrefix(plan.Summary, prefix) {
		plan.Summary = strings.TrimSpace(prefix + " " + plan.Summary)
	}
}

// PlanRevision is which version of the event's plan a plan is, and what changed from the one before (#40734).
type PlanRevision struct {
	// PreviousVersion is the plan_version (hash) of the plan this one replaced.
	PreviousVersion string `json:"previous_version,omitempty"`
	// Kind is the planning request that wrote it (auto, ask, recheck, replan); empty for a plan proposed in chat
	// or by a follow-up check.
	Kind     string   `json:"kind,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
	Note     string   `json:"note,omitempty"`
	Findings string   `json:"findings,omitempty"`
	// Changes lists, in words, what differs from the previous version: steps added, removed or changed, and the cause.
	Changes []string `json:"changes,omitempty"`
}

// stampLoopPlan sets what every plan written with the loop on carries: its normalized cause key (empty fields
// when unclear), when it was written, its version number (from 1, one more than the event's previous plan), and
// its revision against that plan.
func stampLoopPlan(plan *RemediationPlan, prev *RemediationPlan, prevVersion string, req *PlanRevision, now time.Time) {
	key := events.CauseKey{}
	if plan.CauseKey != nil {
		key = events.NormalizeCauseKey(*plan.CauseKey)
	}
	plan.CauseKey = &key
	plan.Goal = strings.TrimSpace(plan.Goal)
	at := now.UTC()
	plan.GeneratedAt = &at
	plan.Version = 1
	rev := PlanRevision{}
	if req != nil {
		rev.Kind, rev.Reasons, rev.Note, rev.Findings = req.Kind, req.Reasons, req.Note, req.Findings
	}
	if prev != nil {
		plan.Version = 2 // a plan from before versions were kept was the first
		if prev.Version > 0 {
			plan.Version = prev.Version + 1
		}
		rev.PreviousVersion = prevVersion
		rev.Changes = planChanges(*prev, *plan)
	}
	plan.Revision = &rev
}

// planChanges says what changed from prev to next: the cause, and each step added, removed or with a different
// command (steps are matched by name).
func planChanges(prev, next RemediationPlan) []string {
	key := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	name := func(a RemediationAction) string { return firstNonEmpty(a.Action, a.Title) }
	var changes []string
	if key(prev.RootCause) != key(next.RootCause) {
		changes = append(changes, "Cause: "+truncate(next.RootCause, 300))
	}
	before := map[string]RemediationAction{}
	for _, a := range prev.Actions {
		before[key(name(a))] = a
	}
	after := map[string]bool{}
	for _, a := range next.Actions {
		k := key(name(a))
		after[k] = true
		old, ok := before[k]
		switch {
		case !ok:
			changes = append(changes, "Added: "+name(a))
		case stepRef(old) != stepRef(a) || automationInputsText(automationInputsOf(old)) != automationInputsText(automationInputsOf(a)) ||
			strings.TrimSpace(old.RollbackCommand) != strings.TrimSpace(a.RollbackCommand) || !sameRequiredChange(old.RequiredChange, a.RequiredChange):
			changes = append(changes, "Changed: "+name(a))
		}
	}
	for _, a := range prev.Actions {
		if !after[key(name(a))] {
			changes = append(changes, "Removed: "+name(a))
		}
	}
	if len(changes) == 0 {
		changes = append(changes, "No change to the cause or the steps")
	}
	return changes
}

func sameRequiredChange(a, b *RequiredChange) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// The repair pass for a plan an agent wrote (#40734): the one-shot planner reads the live state of what a draft
// changes and rewrites the draft against it. For an agent's plan the agent is the one to rewrite it, so the
// first proposal whose reads found something is refused with that state; the same targets proposed again are
// stored. Per process: a turn's tool calls run where the turn runs.
var shownTargetState = &targetStateMemo{seen: map[string]time.Time{}}

// targetStateShownFor is how long a shown state lets the same targets through.
const targetStateShownFor = 30 * time.Minute

type targetStateMemo struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// firstShowing records key and reports whether it was not shown in the last targetStateShownFor.
func (m *targetStateMemo) firstShowing(key string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, at := range m.seen {
		if now.Sub(at) >= targetStateShownFor {
			delete(m.seen, k)
		}
	}
	if _, ok := m.seen[key]; ok {
		return false
	}
	m.seen[key] = now
	return true
}

// targetStateRefusal refuses an agent's plan the first time the reads of its targets found something, with what
// they found, so the agent checks each command against it. nil when nothing was read or it was shown already.
func targetStateRefusal(eventId string, trace RemediationTrace) error {
	if !slices.ContainsFunc(trace.TargetReads, func(r targetRead) bool { return r.Content != "" }) {
		return nil
	}
	h := sha256.New()
	h.Write([]byte(eventId))
	for _, r := range trace.TargetReads {
		h.Write([]byte("\x00" + r.Target + "\x00" + r.Source))
	}
	if !shownTargetState.firstShowing(hex.EncodeToString(h.Sum(nil)), time.Now()) {
		return nil
	}
	return fmt.Errorf("first check each command against the live state of what it changes, read just now and shown below; correct any command it shows is wrong (a value already set, a name or namespace that differs, a document a patch must carry whole), or, if the plan already fits, call propose_fix again with it unchanged\n\n%s",
		renderTargetState(trace.TargetReads, remediationNow()))
}

// planRequestResponse is the answer to a planning request that was recorded.
type planRequestResponse struct {
	Queued         bool       `json:"queued"`
	RequestId      string     `json:"request_id,omitempty"`
	Kind           string     `json:"kind,omitempty"`
	PlanStatus     string     `json:"plan_status,omitempty"`
	PreparingSince *time.Time `json:"preparing_since,omitempty"`
}

// remediationPlanRequest is the payload of ai_generate_remediation_plan.
type remediationPlanRequest struct {
	AccountId string   `json:"account_id"`
	EventId   string   `json:"event_id"`
	Mode      string   `json:"mode"` // plan | recheck | replan
	Reasons   []string `json:"reasons"`
	Note      string   `json:"note"`
}

// planModes maps the request's mode to the planning turn's kind.
var planModes = map[string]string{"plan": planTurnAsk, "recheck": planTurnRecheck, "replan": planTurnReplan}

// processRemediationPlanRequest is ai_generate_remediation_plan: "Ask Nubi for a plan" (plan), "Ask Nubi to
// re-check" (recheck) and Replan (replan, with reason chips and a note). It records the request and returns at
// once; the plan arrives through ai_remediation_get. A refusal (the 24 h cap, a run in flight, a duplicate or
// suppressed event, ...) is a 409 whose error message is one sentence the person reads as is. RBAC: read, as
// plan generation.
func processRemediationPlanRequest(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	var request remediationPlanRequest
	var actionRequest ActionRequest
	if err := c.ShouldBindJSON(&actionRequest); err != nil {
		slog.Error(errorBindingMessage, "error", err)
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	payload := actionRequest.Input
	if v, ok := payload["request"].(map[string]any); ok {
		payload = v
	}
	if err := common.DecodeMapToStruct(payload, &request); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	logger := slog.With("account_id", request.AccountId, "event_id", request.EventId, "mode", request.Mode)
	ctx, err := BuildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, logger)
	if err != nil {
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	sc := ctx.GetSecurityContext()
	if sc == nil || (!sc.HasAccountAccess(request.AccountId, security.SecurityAccessTypeRead) &&
		!grantedRun(sc, request.AccountId, moduleAiMisc)) {
		c.JSON(403, BuildApiResponse(nil, []error{errors.New(errorUserAccessMessage)}))
		return
	}
	if _, err := uuid.Parse(strings.TrimSpace(request.EventId)); err != nil {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "event_id must be an event's id"}}))
		return
	}
	kind, ok := planModes[strings.ToLower(strings.TrimSpace(request.Mode))]
	if !ok {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "mode is plan, recheck or replan"}}))
		return
	}
	refuse := func(sentence string) {
		c.JSON(409, BuildApiResponse(nil, []error{common.Error{Message: sentence}}))
	}
	by := strings.TrimSpace(sc.GetUserId())
	if kind == planTurnReplan && (by == "" || by == security.GetSystemUserId()) {
		refuse("Only a person can ask for other fixes.")
		return
	}
	// A person's request runs as them (planningContext), so it needs their write access.
	if by != "" && by != security.GetSystemUserId() {
		if refusal := planWriteRefusal(sc, request.AccountId); refusal != "" {
			c.JSON(403, BuildApiResponse(nil, []error{common.Error{Message: sentenceOf(refusal)}}))
			return
		}
	}
	if kind == planTurnReplan && len(request.Reasons) == 0 && strings.TrimSpace(request.Note) == "" {
		c.JSON(400, BuildApiResponse(nil, []error{common.Error{Message: "a replan needs a reason or a note"}}))
		return
	}
	row, refusal, err := requestBackgroundPlan(ctx, request.AccountId, request.EventId, planRequestInput{Kind: kind, By: by, Reasons: cleanReasons(request.Reasons), Note: request.Note})
	if err != nil {
		ctx.GetLogger().Error("remediation_plan: request not recorded", "error", err)
		c.JSON(500, BuildApiResponse(nil, []error{common.Error{Message: "failed to ask Nubi for a fix"}}))
		return
	}
	if refusal != "" {
		refuse(sentenceOf(refusal))
		return
	}
	out := planRequestResponse{Queued: true}
	if row.Id != "" {
		out.RequestId, out.Kind = row.Id, row.Kind
		if row.Open() {
			since := row.RequestedAt
			out.PlanStatus, out.PreparingSince = PlanStatusPreparing, &since
		}
	}
	c.JSON(200, BuildApiResponse(out, nil))
}

// sentenceOf is a refusal as a sentence: capitalised, with a full stop.
func sentenceOf(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return reason
	}
	reason = strings.ToUpper(reason[:1]) + reason[1:]
	if !strings.HasSuffix(reason, ".") {
		reason += "."
	}
	return reason
}

// cleanReasons trims the reason chips, drops empty ones and bounds them.
func cleanReasons(in []string) []string {
	var out []string
	for _, r := range in {
		if r = truncate(strings.TrimSpace(r), 200); r != "" && len(out) < 10 {
			out = append(out, r)
		}
	}
	return out
}

// respondGenerateWithLoop is ai_remediation_generate with the loop on: the stored plan with its loop state, as
// ai_remediation_get returns it, and nothing else. It neither calls the one-shot planner nor asks Nubi for a plan:
// its callers (the legacy panel) fire it on mount and on refresh, not when a person asks. With the loop on a planning
// turn starts only from the planner's own triggers or a person's request (ai_generate_remediation_plan).
func respondGenerateWithLoop(c *gin.Context, ctx *security.RequestContext, accountId, eventId string) {
	stored, hasPlan := loadSavedPlan(ctx, accountId, eventId)
	var plan RemediationPlan
	if hasPlan {
		plan = stored.Plan
		plan.PlanVersion = stored.Version
		plan.Target = targetOrNil(ctx, accountId)
		assignDelivery(ctx, accountId, &plan)
	}
	var info *events.EventInfo
	if dbManager, err := common.GetDatabaseManager(common.Metastore); err == nil {
		info, _ = events.NewEventAnalysisRepository(dbManager).GetEventInfo(ctx, eventId, accountId)
	}
	c.JSON(200, BuildApiResponse(remediationGetBody(ctx, accountId, eventId, info, eventId, info, plan), nil))
}
