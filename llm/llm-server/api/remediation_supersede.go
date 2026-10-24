package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// supersededApprovalNote replaces an approval NudgeBee closes because the alert cleared and fired again after it
// was asked: the step was not run, and a new investigation and plan follow the new firing.
const supersededApprovalNote = "Closed without running: the alert cleared and fired again after this was asked, so this approval was for the earlier firing. Nothing ran; Nubi works from the new firing."

// supersededBatch caps how many alert conversations one tick closes.
const supersededBatch = 20

// waitingAlertApproval is an alert conversation still WAITING on an approval, with when its newest waiting
// approval was asked.
type waitingAlertApproval struct {
	ConversationId string    `db:"conversation_id"`
	AccountId      string    `db:"account_id"`
	Alert          string    `db:"alert"`
	AskedAt        time.Time `db:"asked_at"`
}

// waitingAlertApprovals lists alert conversations (event-<alert>) WAITING on an approval. A var so tests can stub it.
var waitingAlertApprovals = func(ctx context.Context, db *sqlx.DB) ([]waitingAlertApproval, error) {
	var rows []waitingAlertApproval
	err := db.SelectContext(ctx, &rows, `SELECT c.id::text AS conversation_id, c.account_id::text AS account_id,
		       substring(c.session_id FROM 7) AS alert, max(m.created_at) AS asked_at
		  FROM llm_conversations c JOIN llm_conversation_messages m ON m.conversation_id = c.id
		 WHERE c.status = 'WAITING' AND c.session_id LIKE 'event-%' AND c.session_id NOT LIKE 'event-rca-%'
		   AND m.message_type = 'followup' AND m.status = 'WAITING'
		   AND m.message_config LIKE '%"followupType":"tool_confirmation"%'
		 GROUP BY c.id, c.account_id, c.session_id
		 ORDER BY max(m.created_at) LIMIT $1`, supersededBatch)
	return rows, err
}

// closeWaitingConversation ends a WAITING conversation with note, marking why (core CloseWaitingConversation). A
// var so tests can stub it.
var closeWaitingConversation = func(accountId, conversationId, note, reason string) (bool, error) {
	return core.GetConversationDao().CloseWaitingConversation(security.NewRequestContextForSuperAdmin(), accountId, conversationId, note, reason)
}

// supersedeEarlierEpisodeApprovals closes, without running anything or calling a model, every approval still
// waiting in an alert's conversation that was asked before the alert's current episode started — the alert
// cleared and then fired again since (events.AlertEpisodeStart; a re-fire of a still-open incident is the same
// episode). The approval was for the earlier firing; leaving it would hold the new firing's investigation and plan
// back with nothing to approve on the new incident's page. It is the only place this is written; the
// investigation gate only leaves such approvals out. Runs on the remediation_followups tick, loop on only.
func supersedeEarlierEpisodeApprovals(db *sqlx.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	rows, err := waitingAlertApprovals(ctx, db)
	if err != nil {
		slog.Warn("remediation_followups: waiting approvals not read", "error", err)
		return
	}
	for _, r := range rows {
		if !events.RemediationLoopOn(r.AccountId) {
			continue
		}
		start, err := events.AlertEpisodeStart(ctx, db, r.AccountId, r.Alert)
		if err != nil {
			slog.Warn("remediation_followups: alert episode not read", "error", err, "conversation_id", r.ConversationId)
			continue
		}
		if start == nil || !r.AskedAt.Before(*start) {
			continue
		}
		closed, err := closeWaitingConversation(r.AccountId, r.ConversationId, supersededApprovalNote, core.ClosedReasonSuperseded)
		if err != nil {
			slog.Warn("remediation_followups: earlier firing's approval not closed", "error", err, "conversation_id", r.ConversationId)
			continue
		}
		if closed {
			slog.Info("remediation_followups: closed an earlier firing's approval", "conversation_id", r.ConversationId,
				"account_id", r.AccountId, "asked_at", r.AskedAt, "episode_started_at", *start)
		}
	}
}
