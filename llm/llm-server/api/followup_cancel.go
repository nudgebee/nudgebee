package api

import (
	"errors"
	"fmt"
	"net/http"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// resolutionDismiss is the only supported cancel mode for a pending
// follow-up: soft-skip the current question and end the conversation with a
// structured marker. A hard, no-marker terminate already exists as the
// separate ai_cancel_investigation action (POST /v1/completions/chat_stop,
// core.ConversationDao.TerminateConversation) and is wired to the UI's
// existing "Stop conversation" control — duplicating that here would just be
// a second path to the same DB write.
const resolutionDismiss = "dismiss"

// handleFollowupCancel resolves a pending follow-up without answering it,
// by soft-dismissing it. (#27582) Writes a structured marker onto the
// in-flight follow-up message so the UI can show "user skipped this
// question" rather than a bare kill, then terminates the conversation via
// the same path ai_cancel_investigation uses.
//
// Return shape is (responseBody, httpStatus, error). A 200 with an
// "idempotent": true marker is returned when the conversation is already in
// a terminal state so retries from flaky clients are safe.
func handleFollowupCancel(ctx *security.RequestContext, request ConversationApiRequest) (map[string]any, int, error) {
	if !config.Config.FollowupCancelEnabled {
		return nil, http.StatusNotFound, errors.New("api: followup cancel is not enabled")
	}
	if request.ConversationId == "" {
		return nil, http.StatusBadRequest, errors.New("api: conversation_id is required for cancel")
	}
	if request.Resolution != resolutionDismiss {
		return nil, http.StatusBadRequest, fmt.Errorf("api: unsupported resolution %q", request.Resolution)
	}
	if request.AgentId == "" {
		return nil, http.StatusBadRequest, errors.New("api: agent_id is required to dismiss a follow-up")
	}

	dao := core.GetConversationDao()
	conversation, err := dao.GetConversation(request.ConversationId)
	if err != nil {
		return nil, http.StatusNotFound, fmt.Errorf("api: conversation not found: %w", err)
	}
	if conversation.AccountID.String() != request.AccountId {
		return nil, http.StatusForbidden, errors.New(errorUserAccessMessage)
	}

	// Idempotent: already-terminal conversation → report and exit, keeping
	// retries from flaky clients safe.
	if core.IsTerminalConversationStatus(conversation.Status) {
		return map[string]any{
			"status":     string(conversation.Status),
			"message":    "conversation already in terminal state",
			"idempotent": true,
		}, http.StatusOK, nil
	}

	// Try skip-and-continue first (#27582 v1) — for informational follow-up
	// types only, with no sibling still waiting on the same follow-up
	// message, this lets the agent keep working instead of ending the
	// conversation. handled=false means neither condition held; fall
	// through to the existing terminate path below exactly as before.
	handled, resumeResp, skipErr := core.TrySkipAndContinueFollowup(ctx, request.AccountId, request.ConversationId, request.AgentId, request.Reason)
	if skipErr != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("api: skip-and-continue failed: %w", skipErr)
	}
	if handled {
		return map[string]any{
			"status":     string(resumeResp.Status),
			"resolution": request.Resolution,
			"message":    "Follow-up skipped — continuing.",
			"idempotent": false,
		}, http.StatusOK, nil
	}

	// Write the dismissal marker BEFORE terminating so the followup message
	// carries the reason. Best-effort — if this fails we fall through to
	// terminate anyway so the user's cancel still takes effect rather than
	// leaving the conversation stuck in WAITING.
	if dismissErr := core.HandleFollowupDismiss(ctx, request.AccountId, request.ConversationId, request.AgentId, request.Reason); dismissErr != nil {
		ctx.GetLogger().Warn("api: followup dismiss marker failed, falling through to terminate",
			"error", dismissErr,
			"conversation_id", request.ConversationId,
			"agent_id", request.AgentId)
	}

	if err := dao.TerminateConversation(ctx, request.AccountId, request.ConversationId); err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("api: terminate failed: %w", err)
	}

	return map[string]any{
		"status":     string(core.ConversationStatusTerminated),
		"resolution": request.Resolution,
		"message":    "Follow-up dismissed.",
		"idempotent": false,
	}, http.StatusOK, nil
}
