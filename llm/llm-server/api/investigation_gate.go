package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// With the remediation loop on, an alert has one conversation, event-<fingerprint>: its investigation, report,
// plan, questions, approvals and runs (#40734, docs/architecture-decisions.md). A re-investigation never deletes it;
// it waits (investigationBlocked) while something in it waits on a person or a step runs, with its stage rows kept
// IN_PROGRESS and the reason recorded (parkInvestigation), and the recovery job starts it once nothing holds it back.

// Why an investigation waits (its stage rows' status_reason). Shown on the event page.
const (
	gateReasonApproval = "waiting on an approval"
	gateReasonQuestion = "waiting on an answer in the alert's conversation"
	gateReasonRun      = "waiting for a step that is running, or for its follow-up check"
	gateReasonTurn     = "waiting for the current turn in the alert's conversation"
	gateReasonUnread   = "waiting: the alert's conversation could not be checked"
)

// isGateReason reports whether reason is one investigationBlocked or the pipeline wrote when it parked the run, so
// no live worker holds the row (the recovery job need not wait out its minimum age).
func isGateReason(reason string) bool {
	switch reason {
	case gateReasonApproval, gateReasonQuestion, gateReasonRun, gateReasonTurn, gateReasonUnread:
		return true
	}
	return false
}

// investigationBlocked is why an investigation of the alert (account and fingerprint, or the event id for an alert
// without one) must wait now, or "" when it may run. Read only. With the loop off it is always "".
//
//   - an approval waiting in the alert's conversation, asked less than pendingApprovalMaxAge ago and during the
//     alert's current episode (an approval from before the alert cleared and fired again does not count: the
//     follow-ups job closes it as superseded, supersedeEarlierEpisodeApprovals);
//   - another question to a person waiting there, as recent;
//   - a step of the alert running, or its follow-up check not finished (events.AlertRunInFlight);
//   - a turn under way there that is not one of the investigation's own stages.
//
// A var so tests can stub it.
var investigationBlocked = func(ctx context.Context, db *sqlx.DB, accountId, alert string) (string, error) {
	if !events.RemediationLoopOn(accountId) {
		return "", nil
	}
	episode, err := events.AlertEpisodeStart(ctx, db, accountId, alert)
	if err != nil {
		return gateReasonUnread, fmt.Errorf("investigationBlocked: %w", err)
	}
	var state struct {
		Approval bool `db:"approval"`
		Question bool `db:"question"`
		Turn     bool `db:"turn"`
	}
	if err := db.GetContext(ctx, &state, `WITH c AS (SELECT id, status FROM llm_conversations WHERE session_id = $1 AND account_id = $2::uuid),
		waiting AS (SELECT m.message_config LIKE '%"followupType":"tool_confirmation"%' AS approval
		  FROM llm_conversation_messages m JOIN c ON c.id = m.conversation_id
		 WHERE m.message_type = 'followup' AND m.status IN ('WAITING', 'WAITING_FOR_CLIENT_TOOL') AND `+approvalConversationLive+`
		   AND m.created_at > LOCALTIMESTAMP - make_interval(secs => $3)
		   AND ($4::timestamp IS NULL OR m.created_at >= $4::timestamp))
		SELECT COALESCE(bool_or(approval), false) AS approval, COALESCE(bool_or(NOT approval), false) AS question,
		       EXISTS (SELECT 1 FROM llm_conversation_messages m JOIN c ON c.id = m.conversation_id
		         WHERE m.status IN ('IN_PROGRESS', 'QUEUED', 'PENDING') AND `+conversationNotEnded+`
		           AND COALESCE(m.metadata->>'investigation_run_id', '') = '') AS turn
		  FROM waiting`,
		events.SessionIdPrefixEvent+alert, accountId, pendingApprovalMaxAge.Seconds(), episode); err != nil {
		return gateReasonUnread, fmt.Errorf("investigationBlocked: conversation: %w", err)
	}
	switch {
	case state.Approval:
		return gateReasonApproval, nil
	case state.Question:
		return gateReasonQuestion, nil
	}
	busy, err := events.AlertRunInFlight(ctx, db, accountId, alert)
	if err != nil {
		return gateReasonUnread, fmt.Errorf("investigationBlocked: %w", err)
	}
	if busy {
		return gateReasonRun, nil
	}
	if state.Turn {
		return gateReasonTurn, nil
	}
	return "", nil
}

// parkInvestigation keeps the event's investigation waiting with reason (events.ParkEventAnalysis) and is the
// pipeline's answer while it waits: IN_PROGRESS with the reason, never FAILED.
func parkInvestigation(ctx *security.RequestContext, repo *events.EventAnalysisRepository, eventId, fingerprint, accountId, aggKey, reason string) EventAnalysisResponse {
	ctx.GetLogger().Info("analyzer: investigation waits", "event_id", eventId, "fingerprint", fingerprint, "reason", reason)
	if err := repo.ParkEventAnalysis(ctx, eventId, fingerprint, accountId, aggKey, reason); err != nil {
		ctx.GetLogger().Warn("analyzer: waiting reason not recorded", "error", err, "event_id", eventId)
	}
	return EventAnalysisResponse{Status: string(events.AnalysisStatusInProgress), StatusReason: reason}
}

// conversationHeld reports whether a stage's turn could not start because the alert's conversation is in use (a
// turn running, or one waiting on a person's answer). With the loop on both mean the investigation waits; with it
// off only a running turn does, as before.
func conversationHeld(err error, loopOn bool) bool {
	return errors.Is(err, core.ErrConversationInProgress) || (loopOn && errors.Is(err, core.ErrConversationPendingFollowup))
}

// heldReason is why the investigation waits when a stage found the conversation in use: what investigationBlocked
// names, else (an approval past its age, waiting for the hourly close; a turn that just started) by the
// conversation's state.
func heldReason(ctx *security.RequestContext, accountId, alert string, err error) string {
	if dbManager, dbErr := common.GetDatabaseManager(common.Metastore); dbErr == nil {
		if reason, _ := investigationBlocked(ctx.GetContext(), dbManager.Db, accountId, alert); reason != "" {
			return reason
		}
	}
	if errors.Is(err, core.ErrConversationPendingFollowup) {
		return gateReasonApproval
	}
	return gateReasonTurn
}

// stageConfig is cfg for one of the investigation's stage turns: with the loop on it names the run
// (InvestigationRunId), which the turn's message (and any follow-up it raises) is saved with.
func (r EventAnalysisRequest) stageConfig(cfg toolcore.NBQueryConfig) toolcore.NBQueryConfig {
	cfg.InvestigationRunId = r.run.Id
	return cfg
}

// startPipelineHeartbeat marks the event's IN_PROGRESS stage rows live every events.PipelineHeartbeatInterval
// while the pipeline holds the alert's conversation (events.EventPipelineHoldOf reads it), until the returned stop.
// A var so tests can stub it.
var startPipelineHeartbeat = func(db *sqlx.DB, accountId, eventId string) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(events.PipelineHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				beatCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
				if err := events.HeartbeatEventRun(beatCtx, db, accountId, eventId); err != nil {
					slog.Warn("analyzer: pipeline heartbeat not written", "error", err, "event_id", eventId)
				}
				cancel()
			}
		}
	}()
	return func() { close(done) }
}

// pipelineHoldKey is the hold key an automatic turn in the alert's conversation starts with (core StartIdleTurn),
// and whether it may start at all: "" with true when the loop is off; false while the alert's investigation runs.
// A lock with no live heartbeat (a dropped connection Postgres has not reaped yet) is not trusted: the turn starts
// without the key rather than wait on it. A var so tests can stub it.
var pipelineHoldKey = func(db *sqlx.DB, accountId, alert string, logger *slog.Logger) (key string, mayStart bool) {
	if !events.RemediationLoopOn(accountId) {
		return "", true
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	hold, err := events.EventPipelineHoldOf(ctx, db, accountId, alert)
	switch {
	case err != nil:
		logger.Warn("remediation: investigation hold not read", "error", err)
		return events.EventPipelineLockKey(accountId, alert), true
	case hold == events.PipelineLive:
		logger.Info("remediation: alert conversation held by its investigation")
		return "", false
	case hold == events.PipelineStale:
		logger.Warn("remediation: investigation lock held with no live run; starting without it")
		return "", true
	}
	return events.EventPipelineLockKey(accountId, alert), true
}

// messagesOfRun keeps the messages of the investigation run (their message_config names it,
// toolcore.NBQueryConfig.InvestigationRunId) written since the run's row was created. With no run (loop off, an
// RCA session) it keeps every message.
func messagesOfRun(messages []core.ConversationMessage, run events.EventRun) []core.ConversationMessage {
	if run.Id == "" {
		return messages
	}
	var out []core.ConversationMessage
	for _, m := range messages {
		if m.CreatedAt != nil && !run.StartedAt.IsZero() && m.CreatedAt.Before(run.StartedAt) {
			continue
		}
		if investigationRunIdOf(m.MessageConfig) != run.Id {
			continue
		}
		out = append(out, m)
	}
	return out
}

// investigationRunIdOf is the run a stored message_config names, or "".
func investigationRunIdOf(messageConfig *string) string {
	if messageConfig == nil || !strings.Contains(*messageConfig, "investigation_run_id") {
		return ""
	}
	var cfg struct {
		RunId string `json:"investigation_run_id"`
	}
	if json.Unmarshal([]byte(*messageConfig), &cfg) != nil {
		return ""
	}
	return cfg.RunId
}
