package api

import (
	"context"
	"log/slog"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// primaryEventOf returns the event a duplicate's remediation runs on, or "" when the event reads its own plan.
// The shared rule (planEventOf, events.PlanEventOf) decides: a duplicate follows its main event only while the
// main is live, and never when the main has no plan and the duplicate has one.
var primaryEventOf = func(ctx *security.RequestContext, in remediationGetInput) string {
	if in.PlanEventId != "" {
		if in.PlanEventId == in.EventId {
			return ""
		}
		return in.PlanEventId // the read already applied the rule (planEventAndInfo)
	}
	return primaryEventId(ctx, in.AccountId, in.EventId)
}

// primaryEventId is the event whose plan eventId reads and acts on when that is not eventId itself, else "". A
// read error reads as not a duplicate: the event then shows its own state, as before the loop.
var primaryEventId = func(ctx *security.RequestContext, accountId, eventId string) string {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	base, logger := context.Background(), slog.Default()
	if ctx != nil {
		base, logger = ctx.GetContext(), ctx.GetLogger()
	}
	readCtx, cancel := context.WithTimeout(base, decisionDBTimeout)
	defer cancel()
	planEvent, err := planEventOf(readCtx, dbManager.Db, accountId, eventId)
	if err != nil {
		logger.Warn("remediation: main event of a duplicate not read", "error", err, "event_id", eventId)
		return ""
	}
	if planEvent == eventId {
		return ""
	}
	return planEvent
}
