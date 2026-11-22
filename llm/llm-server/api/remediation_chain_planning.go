package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// Fix options for recurring alerts (#40734). A firing that joins a chain (a DUPLICATE or SUPPRESSED repeat of the
// alert) reuses the chain's analysis, so no investigation completes for it and planAfterInvestigation never asks for
// a plan: its thread shows no fix options, however old or missing the chain's plan is. The planning job's tick
// therefore looks at the chain firings of the last chainPlanWindow that a person was told about (posted to Slack or
// Teams, or P1/P2), and when their plan event (events.PlanEventOf) has no current plan — none, or one that no
// longer applies (the tab's planRelevance) — asks for one automatic plan for it. It is an automatic request like
// the one after an investigation, so the job's rules for those apply: it parks behind a waiting approval and is
// dropped once a step ran. At most one per plan event per chainPlanEvery: any planning request for the plan event
// in that time (open or ended, whoever asked) covers it, so a chain firing every few minutes, or one whose
// planning ends with no plan, is not asked again on every firing.

// Bounds on the scan. Vars so tests can change them.
var (
	chainPlanWindow = 15 * time.Minute
	chainPlanEvery  = planStaleAfter
	chainPlanMax    = 50
)

// readChainFirings reads the recent chain firings a person was told about. A var so tests can stub it.
var readChainFirings = func(ctx context.Context, db *sqlx.DB) ([]events.ChainFiring, error) {
	return events.RecentChainFirings(ctx, db, chainPlanWindow, chainPlanMax)
}

// latestPlanningOf is the plan event's newest planning request, or nil. A var so tests can stub it.
var latestPlanningOf = func(ctx context.Context, db *sqlx.DB, accountId, eventId string) (*events.PlanningRequest, error) {
	return events.LatestPlanning(ctx, db, accountId, eventId)
}

// chainPlanNeeded reports whether planEvent has no current plan, and why: it has none, its plan was built before an
// analysis it read was re-run, or the plan no longer applies by the tab's rule (planRelevanceOf: older than 24 h,
// fired again after a run or after it cleared, closed after the plan). A plan whose state cannot be read is taken
// as current. A var so tests can stub it.
var chainPlanNeeded = func(ctx *security.RequestContext, accountId, planEvent string) (bool, string) {
	stored, hasPlan := loadSavedPlan(ctx, accountId, planEvent)
	if !hasPlan || !planHasContent(&stored.Plan) {
		return true, "no fixes"
	}
	if stored.Stale {
		return true, "the fixes predate the analysis"
	}
	facts, err := loadPlanFacts(ctx, remediationGetInput{AccountId: accountId, EventId: planEvent, PlanEventId: planEvent, Plan: &stored.Plan})
	if err != nil || facts.Event == nil || facts.Event.PlanAt == nil {
		return false, ""
	}
	rel := planRelevanceOf(*facts.Event, facts)
	return rel.Stale, rel.StaleReason
}

// requestChainPlan is requestBackgroundPlan; a var so tests can capture it.
var requestChainPlan = requestBackgroundPlan

// queueChainPlannings asks for a plan for each recent notified chain firing whose plan event has no current plan,
// once per plan event per chainPlanEvery. Run by the planning job's tick.
func queueChainPlannings(db *sqlx.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*decisionDBTimeout)
	defer cancel()
	firings, err := readChainFirings(ctx, db)
	if err != nil {
		slog.Warn("remediation_planning: recent chain firings not read", "error", err)
		return
	}
	seen := map[string]bool{}
	for _, f := range firings {
		if !events.RemediationLoopOn(f.AccountId) {
			continue
		}
		planEvent, err := planEventOf(ctx, db, f.AccountId, f.EventId)
		if err != nil || planEvent == "" || seen[f.AccountId+"/"+planEvent] {
			continue
		}
		seen[f.AccountId+"/"+planEvent] = true
		logger := slog.With("account_id", f.AccountId, "event_id", planEvent, "firing_id", f.EventId)
		latest, err := latestPlanningOf(ctx, db, f.AccountId, planEvent)
		if err != nil {
			logger.Warn("remediation_planning: the chain's planning not read", "error", err)
			continue
		}
		if latest != nil && (latest.Open() || time.Since(latest.RequestedAt) < chainPlanEvery) {
			continue
		}
		rctx := security.NewRequestContextForSuperAdmin()
		need, why := chainPlanNeeded(rctx, f.AccountId, planEvent)
		if !need {
			continue
		}
		row, refusal, err := requestChainPlan(rctx, f.AccountId, planEvent, planRequestInput{Kind: planTurnAuto, OutOfDate: true})
		switch {
		case err != nil:
			logger.Warn("remediation_planning: plan for the recurring alert not asked", "error", err)
		case refusal != "":
			logger.Info("remediation_planning: no plan for the recurring alert", "reason", refusal)
		default:
			logger.Info("remediation_planning: plan asked for the recurring alert", "planning_id", row.Id, "why", why)
		}
	}
}
