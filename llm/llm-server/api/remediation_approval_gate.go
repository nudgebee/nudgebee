package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// An answer to a tool confirmation in an alert's conversation, with the remediation loop on, decides a change on the
// alert: approving it runs the write, denying or dismissing it decides the plan step. The alert's conversation is
// shared by everyone on the tenant who can read the account, and its approvals are shown on the Remediation tab, so
// the answer needs the same write access as running a remediation command (remediation.go). Other follow-ups (a
// clarifying question) and other conversations are answered as before.

// errAlertApprovalNeedsWrite is the refusal a read-only person gets.
var errAlertApprovalNeedsWrite = errors.New("remediation: write access to this account is required to answer an approval for a change on this alert")

// followupAnswer reports whether request answers (or dismisses) a pending follow-up: the agent waiting on it is
// named. Which follow-up and which conversation it is, is read from the server's own rows (approvalOfAgent).
func followupAnswer(request ConversationApiRequest) bool {
	return strings.TrimSpace(request.AgentId) != "" || strings.TrimSpace(request.Resolution) != ""
}

// canWriteAlert is the remediation command gate: write (create) access to the account, or an Execute grant.
func canWriteAlert(sc *security.SecurityContext, accountId string) bool {
	return sc != nil && (sc.HasAccountAccess(accountId, security.SecurityAccessTypeCreate) || grantedRun(sc, accountId, moduleAiMisc))
}

// waitingApproval is the follow-up an agent waits on, as the server recorded it.
type waitingApproval struct {
	Found          bool
	ConversationId string
	SessionId      string
	// Confirmation: the follow-up is a tool confirmation (an approval of a write).
	Confirmation bool
	// FollowupMessageId is the follow-up message the agent waits on; Waiting whether that message still waits for
	// its answer; ConfirmationKey the key its answer is recorded under (run_step:<plan version>:<step>:<action> for a
	// plan step).
	FollowupMessageId string
	Waiting           bool
	ConfirmationKey   string
}

// approvalOfAgent reads the follow-up agentId of accountId waits on: its conversation, that conversation's session
// and whether it is a tool confirmation. Found is false when there is no such agent. A var so tests can stub it.
var approvalOfAgent = func(ctx context.Context, accountId, agentId string) (waitingApproval, error) {
	if _, err := uuid.Parse(strings.TrimSpace(agentId)); err != nil {
		return waitingApproval{}, nil
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return waitingApproval{}, fmt.Errorf("approvalOfAgent: %w", err)
	}
	readCtx, cancel := context.WithTimeout(ctx, decisionDBTimeout)
	defer cancel()
	var row struct {
		ConversationId    string `db:"conversation_id"`
		SessionId         string `db:"session_id"`
		Confirmation      bool   `db:"confirmation"`
		FollowupMessageId string `db:"followup_message_id"`
		Waiting           bool   `db:"waiting"`
		Config            string `db:"config"`
	}
	err = dbManager.Db.GetContext(readCtx, &row, `SELECT a.conversation_id::text AS conversation_id, COALESCE(c.session_id, '') AS session_id,
		       COALESCE(m.message_config, '') LIKE '%"followupType":"tool_confirmation"%' AS confirmation,
		       COALESCE(m.id::text, '') AS followup_message_id, COALESCE(m.status::text, '') = 'WAITING' AS waiting,
		       COALESCE(m.message_config, '') AS config
		  FROM llm_conversation_agent a
		  JOIN llm_conversations c ON c.id = a.conversation_id
		  LEFT JOIN llm_conversation_messages m ON m.id = a.followup_message_id AND m.message_type = 'followup'
		 WHERE a.id = $1::uuid AND c.account_id = $2::uuid`, strings.TrimSpace(agentId), accountId)
	if errors.Is(err, sql.ErrNoRows) {
		return waitingApproval{}, nil
	}
	if err != nil {
		return waitingApproval{}, fmt.Errorf("approvalOfAgent: %w", err)
	}
	var config struct {
		ConfirmationKey string `json:"confirmationKey"`
	}
	_ = json.Unmarshal([]byte(row.Config), &config)
	return waitingApproval{Found: true, ConversationId: row.ConversationId, SessionId: row.SessionId, Confirmation: row.Confirmation,
		FollowupMessageId: row.FollowupMessageId, Waiting: row.Waiting, ConfirmationKey: config.ConfirmationKey}, nil
}

// refuseReadOnlyAlertApproval answers 403 and reports true when request answers a tool confirmation in an alert's
// conversation with the remediation loop on (or not known to be off) and the caller has no write access to the
// account. Everything it decides on is read from the server's rows for the agent the request names, never from the
// request: an answer naming another conversation than the agent's is refused, and so is one whose follow-up cannot
// be read. Called by the chat handler (/v1/completions/chat, the ai_get_followup_response action) after the account
// read check.
func refuseReadOnlyAlertApproval(c *gin.Context, ctx *security.RequestContext, request ConversationApiRequest) bool {
	if !followupAnswer(request) || canWriteAlert(ctx.GetSecurityContext(), request.AccountId) {
		return false
	}
	refuse := func(reason string, err error) bool {
		ctx.GetLogger().Warn("remediation: read-only answer to an approval refused", "reason", reason, "error", err, "agent_id", request.AgentId)
		c.JSON(403, BuildApiResponse(nil, []error{errAlertApprovalNeedsWrite}))
		return true
	}
	approval, err := approvalOfAgent(ctx.GetContext(), request.AccountId, request.AgentId)
	if err != nil {
		return refuse("follow-up not read", err)
	}
	if !approval.Found {
		return false // nothing waits under this agent: the answer resolves nothing
	}
	if approval.ConversationId != strings.TrimSpace(request.ConversationId) {
		return refuse("the answer names another conversation than the follow-up's", nil)
	}
	if _, alert := events.AlertFingerprintOfSession(approval.SessionId); !alert || !approval.Confirmation {
		return false
	}
	on, err := events.RemediationLoopRead(request.AccountId)
	if err != nil {
		return refuse("loop flag not read", err)
	}
	if !on {
		return false
	}
	return refuse("approval of a change on the alert", nil)
}
