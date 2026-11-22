package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// The answer to an approval in an alert's conversation, with the remediation loop on (#40734): the person may add an
// optional note (the request's reason) to Approve or Deny, on the Remediation tab or in Slack. The server records it on
// the follow-up message it answers, with the surface it was answered from, derived from the request (a Slack answer
// carries the thread's reply reference, approvalOrigin), never taken from the client. The approved run carries both on
// its record (data.approval_note, data.approved_from, stepDecision / recordApprovedChatWrite) and a denial shows them
// (deniedApprovalsOf). With the loop off, or outside an alert's conversation, an answer is recorded as before: nothing
// is written here.
//
// A denial of a plan step also asks Nubi to re-check the fixes when the alert's investigation finished after they
// were written ("Deny and re-check"), so neither surface has to ask for it.

// maxApprovalNote caps an approval note kept on the record, in characters (as a decline's reason).
const maxApprovalNote = maxDeclineReason

// Metadata keys of an answered follow-up message.
const (
	answerNoteKey = "answer_note"
	answerFromKey = "answered_from"
)

// approvalAnswer is what the server records with an answer to an approval.
type approvalAnswer struct {
	Note         string
	AnsweredFrom string
}

// answeredFromOf is the surface an answer was made on, from the request itself: Slack when it carries a thread's
// reply reference, the web otherwise (the rule approvalOrigin applies to where an approval was asked).
func answeredFromOf(replyRef string) string {
	context, _ := json.Marshal(map[string]string{"reply_ref": strings.TrimSpace(replyRef)})
	origin, _ := approvalOrigin(string(context))
	return origin
}

// cleanApprovalNote is a note as it is kept: trimmed, at most maxApprovalNote whole characters.
func cleanApprovalNote(note string) string {
	note = strings.TrimSpace(note)
	if r := []rune(note); len(r) > maxApprovalNote {
		note = strings.TrimSpace(string(r[:maxApprovalNote]))
	}
	return note
}

// writeApprovalAnswer records answer on the follow-up message messageId of accountId while it still waits for its
// answer. A var so tests can stub it.
var writeApprovalAnswer = func(ctx context.Context, accountId, messageId string, answer approvalAnswer) error {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("writeApprovalAnswer: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, decisionDBTimeout)
	defer cancel()
	_, err = dbManager.Db.ExecContext(writeCtx, `UPDATE llm_conversation_messages
		   SET metadata = COALESCE(NULLIF(metadata, 'null'::jsonb), '{}'::jsonb) || jsonb_build_object($3::text, $4::text, $5::text, $6::text)
		 WHERE id = $1::uuid AND account_id = $2::uuid AND message_type = 'followup' AND status = 'WAITING'`,
		messageId, accountId, answerNoteKey, answer.Note, answerFromKey, answer.AnsweredFrom)
	if err != nil {
		return fmt.Errorf("writeApprovalAnswer: %w", err)
	}
	return nil
}

// approvalAnswerOf reads what was recorded with the answer to the follow-up message messageId of accountId; empty
// when nothing was. A var so tests can stub it.
var approvalAnswerOf = func(ctx context.Context, accountId, messageId string) approvalAnswer {
	var out approvalAnswer
	if strings.TrimSpace(messageId) == "" {
		return out
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return out
	}
	readCtx, cancel := context.WithTimeout(ctx, decisionDBTimeout)
	defer cancel()
	var row struct {
		Note string `db:"note"`
		From string `db:"answered_from"`
	}
	if err := dbManager.Db.GetContext(readCtx, &row, `SELECT COALESCE(metadata->>$3, '') AS note, COALESCE(metadata->>$4, '') AS answered_from
		  FROM llm_conversation_messages WHERE id = $1::uuid AND account_id = $2::uuid`, messageId, accountId, answerNoteKey, answerFromKey); err != nil {
		return out
	}
	return approvalAnswer{Note: row.Note, AnsweredFrom: row.From}
}

// withApprovalAnswer adds what was recorded with an approval to a run's data: approval_note when there is one,
// approved_from (web or slack) when it is known.
func withApprovalAnswer(data map[string]any, answer approvalAnswer) {
	if note := strings.TrimSpace(answer.Note); note != "" {
		data["approval_note"] = note
	}
	if from := strings.TrimSpace(answer.AnsweredFrom); from != "" {
		data["approved_from"] = from
	}
}

// recordApprovalAnswer records the note and surface of an answer to an approval in an alert's conversation, with the
// loop on, before the answer is taken. It returns the HTTP status and message refusing the answer, or 0: an answer
// whose note cannot be kept is refused rather than taken without it; one with no note goes on even when its surface
// cannot be written. Called by the chat handler after the access, approval and budget checks.
func recordApprovalAnswer(ctx *security.RequestContext, request ConversationApiRequest) (int, string) {
	if !followupAnswer(request) || strings.TrimSpace(request.Resolution) != "" {
		return 0, ""
	}
	approval, err := approvalOfAgent(ctx.GetContext(), request.AccountId, request.AgentId)
	if err != nil || !approval.Found || !approval.Confirmation || !approval.Waiting || approval.ConversationId != strings.TrimSpace(request.ConversationId) {
		return 0, ""
	}
	if _, alert := events.AlertFingerprintOfSession(approval.SessionId); !alert {
		return 0, ""
	}
	on, err := events.RemediationLoopRead(request.AccountId)
	if err != nil || !on {
		return 0, ""
	}
	answer := approvalAnswer{Note: cleanApprovalNote(request.Reason), AnsweredFrom: answeredFromOf(request.ReplyRef)}
	if err := writeApprovalAnswer(ctx.GetContext(), request.AccountId, approval.FollowupMessageId, answer); err != nil {
		ctx.GetLogger().Error("remediation: approval answer not recorded", "error", err, "message_id", approval.FollowupMessageId)
		if answer.Note != "" {
			return http.StatusServiceUnavailable, "remediation: your note could not be recorded, so the answer was not taken; try again"
		}
	}
	if isDenial(request.Query) && strings.HasPrefix(approval.ConfirmationKey, runStepKeyPrefix) {
		recheckAfterDenial(ctx, request.AccountId, approval.SessionId)
	}
	return 0, ""
}

// runStepKeyPrefix starts a plan step's confirmation key (RunStepTool.ConfirmationKey).
const runStepKeyPrefix = "run_step:"

// recheckAfterDenial asks Nubi to re-check the alert's fixes, as the person who denied the step, when its
// investigation finished after they were written. The request waits behind the approval being answered and starts
// once it is; an automatic re-check already parked there is taken over (requestBackgroundPlan). Best effort: a
// failure is logged and the denial goes on. A var so tests can stub it.
var recheckAfterDenial = func(ctx *security.RequestContext, accountId, session string) {
	logger := ctx.GetLogger().With("account_id", accountId, "session_id", session)
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		logger.Warn("remediation: re-check after a denial: database unavailable", "error", err)
		return
	}
	eventId, _, err := alertOfSession(dbManager.Db, accountId, session)
	if err != nil {
		logger.Warn("remediation: re-check after a denial: alert not found", "error", err)
		return
	}
	readCtx, cancel := context.WithTimeout(requestGoContext(ctx), decisionDBTimeout)
	ev, err := readIncidentFacts(readCtx, dbManager.Db, accountId, eventId)
	cancel()
	if err != nil || ev == nil {
		logger.Warn("remediation: re-check after a denial: facts not read", "error", err, "event_id", eventId)
		return
	}
	if !investigatedAfterPlan(ev) {
		return
	}
	by := ""
	if sc := ctx.GetSecurityContext(); sc != nil {
		by = strings.TrimSpace(sc.GetUserId())
	}
	row, refusal, err := requestBackgroundPlan(ctx, accountId, eventId, planRequestInput{Kind: planTurnRecheck, By: by})
	if err != nil || refusal != "" {
		logger.Info("remediation: re-check after a denial not asked", "event_id", eventId, "reason", refusal, "error", err)
		return
	}
	logger.Info("remediation: re-check asked after a denial", "event_id", eventId, "request_id", row.Id)
}

// investigatedAfterPlan reports whether the alert's newest completed investigation finished after its plan was
// written: the read's loop.investigation_after_plan.
func investigatedAfterPlan(ev *planEventFacts) bool {
	return ev != nil && ev.PlanAt != nil && ev.InvestigatedAt != nil && ev.InvestigatedAt.After(*ev.PlanAt)
}
