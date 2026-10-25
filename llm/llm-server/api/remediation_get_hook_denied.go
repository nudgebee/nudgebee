package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/samber/lo"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// maxDeniedApprovals is how many denials the read returns.
const maxDeniedApprovals = 10

// deniedApprovals lists the plan steps a person denied in this alert's conversation (run_step
// confirmations answered no), and those NudgeBee closed unanswered (lapsed, superseded), newest first,
// at most 10 from the last 12 h. A denial writes no run record, so without this the tab cannot show
// that an option was turned down. read is false when they could not be read.
var deniedApprovals = func(ctx *security.RequestContext, in remediationGetInput) (denials []deniedApproval, read bool) {
	session := planConversationSession(in)
	rows, err := deniedConfirmations(ctx, in.AccountId, session, firstNonEmpty(in.PlanEventId, in.EventId))
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Warn("remediation_get: denied approvals not read", "error", err)
		}
		return nil, false
	}
	denials = deniedApprovalsOf(in.AccountId, firstNonEmpty(in.PlanEventId, in.EventId), rows)
	nameDeniers(ctx, denials)
	return denials, true
}

// nameDeniers sets each denial's denied_by_name and asked_by_name from the people's display names; a
// name that cannot be read is left out (the denials are still returned).
func nameDeniers(ctx *security.RequestContext, denials []deniedApproval) {
	var ids []string
	for _, d := range denials {
		for _, id := range []string{d.DeniedBy, d.AskedBy} {
			if _, err := uuid.Parse(id); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	names, err := userDisplayNames(ctx, lo.Uniq(ids))
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Warn("remediation_get: names of who denied not read", "error", err)
		}
		return
	}
	for i := range denials {
		denials[i].DeniedByName = strings.TrimSpace(names[denials[i].DeniedBy])
		denials[i].AskedByName = strings.TrimSpace(names[denials[i].AskedBy])
	}
}

// userDisplayNames reads people's display names by id (users.display_name, as the run records'
// resolver_display_name shows them). A var so tests can stub it.
var userDisplayNames = func(ctx *security.RequestContext, ids []string) (map[string]string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("userDisplayNames: %w", err)
	}
	goCtx := context.Background()
	if ctx != nil && ctx.GetContext() != nil {
		goCtx = ctx.GetContext()
	}
	readCtx, cancel := context.WithTimeout(goCtx, decisionDBTimeout)
	defer cancel()
	var rows []struct {
		Id   string `db:"id"`
		Name string `db:"name"`
	}
	if err := dbManager.Db.SelectContext(readCtx, &rows, `SELECT id::text AS id, COALESCE(display_name, '') AS name FROM users WHERE id = ANY($1::uuid[])`,
		pq.Array(ids)); err != nil {
		return nil, fmt.Errorf("userDisplayNames: %w", err)
	}
	names := make(map[string]string, len(rows))
	for _, r := range rows {
		names[r.Id] = r.Name
	}
	return names, nil
}

// deniedConfirmation is a run_step confirmation answered no, as stored, with the message_config of
// the turn it paused (where the answer's approver is recorded, tool_confirmation_refs).
type deniedConfirmation struct {
	MessageId  string    `db:"message_id"`
	Response   string    `db:"response"`
	DeniedAt   time.Time `db:"denied_at"`
	AskedAt    time.Time `db:"asked_at"`
	Config     string    `db:"config"`
	TurnConfig string    `db:"turn_config"`
	// AskedBy is the person whose turn asked it; ClosedReason why NudgeBee closed it unanswered, or "".
	AskedBy      string `db:"asked_by"`
	ClosedReason string `db:"closed_reason"`
	// Note and AnsweredFrom were recorded with the answer (remediation_approval_note.go).
	Note         string `db:"answer_note"`
	AnsweredFrom string `db:"answered_from"`
}

// deniedConfirmations reads the conversation's run_step confirmations answered no or deny within
// pendingApprovalMaxAge, newest first. A var so tests can stub it.
var deniedConfirmations = func(ctx *security.RequestContext, accountId, session, planEventId string) ([]deniedConfirmation, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("deniedConfirmations: %w", err)
	}
	return deniedConfirmationsIn(ctx, dbManager.Db, accountId, session, planEventId)
}

// deniedConfirmationsIn is deniedConfirmations on db: only confirmations asked during the plan event's
// incident (incidentWindow), so a new incident of the alert does not show an earlier one's denials — except
// one NudgeBee closed as superseded during this incident: it was closed because this incident started, so this
// incident's timeline shows it, at the time it was asked (under the earlier firing).
func deniedConfirmationsIn(ctx *security.RequestContext, db *sqlx.DB, accountId, session, planEventId string) ([]deniedConfirmation, error) {
	if _, err := uuid.Parse(planEventId); err != nil {
		return nil, fmt.Errorf("deniedConfirmations: plan event %q: %w", planEventId, err)
	}
	readCtx, cancel := context.WithTimeout(requestGoContext(ctx), decisionDBTimeout)
	defer cancel()
	var rows []deniedConfirmation
	err := db.SelectContext(readCtx, &rows, incidentWindow+`SELECT m.id::text AS message_id, COALESCE(m.response, '') AS response, m.updated_at AS denied_at,
		       m.created_at AS asked_at, COALESCE(m.message_config, '') AS config, COALESCE(m.metadata->>'closed_reason', '') AS closed_reason,
		       COALESCE(m.metadata->>'answer_note', '') AS answer_note, COALESCE(m.metadata->>'answered_from', '') AS answered_from,
		       COALESCE((SELECT t.message_config FROM llm_conversation_agent a JOIN llm_conversation_messages t ON t.id = a.message_id
		                  WHERE a.account_id = c.account_id AND a.followup_message_id = m.id
		                  ORDER BY a.created_at DESC LIMIT 1), '') AS turn_config,
		       COALESCE((SELECT t.user_id::text FROM llm_conversation_agent a JOIN llm_conversation_messages t ON t.id = a.message_id
		                  WHERE a.account_id = c.account_id AND a.followup_message_id = m.id
		                  ORDER BY a.created_at DESC LIMIT 1), '') AS asked_by
		  FROM llm_conversation_messages m
		  JOIN llm_conversations c ON c.id = m.conversation_id
		  CROSS JOIN incident
		 WHERE c.session_id = $1 AND c.account_id = $2::uuid
		   AND m.message_type = 'followup'
		   AND m.updated_at > LOCALTIMESTAMP - $3::interval
		   AND m.message_config LIKE '%"followupType":"tool_confirmation"%'
		   AND m.message_config LIKE '%"confirmationKey":"run_step:%'
		   AND ((m.status = 'COMPLETED' AND lower(btrim(COALESCE(m.response, ''))) IN ('no', 'deny') AND `+askedInIncident+`)
		     OR (m.status = 'TERMINATED' AND m.metadata->>'closed_reason' = 'lapsed' AND `+askedInIncident+`)
		     OR (m.status = 'TERMINATED' AND m.metadata->>'closed_reason' = 'superseded'
		         AND m.updated_at >= incident.starts_at AND (incident.ends_at IS NULL OR m.updated_at < incident.ends_at)))
		 ORDER BY m.updated_at DESC
		 LIMIT $5`,
		session, accountId, fmt.Sprintf("%d seconds", int(pendingApprovalMaxAge.Seconds())), planEventId, maxDeniedApprovals)
	if err != nil {
		return nil, fmt.Errorf("deniedConfirmations: %w", err)
	}
	return rows, nil
}

// deniedApprovalsOf turns the stored denials into the read's denied approvals. accountId and
// planEventId name the plan the steps belong to, for each step's own key.
func deniedApprovalsOf(accountId, planEventId string, rows []deniedConfirmation) []deniedApproval {
	var out []deniedApproval
	for _, r := range rows {
		var config struct {
			FollowupType    string `json:"followupType"`
			ConfirmationKey string `json:"confirmationKey"`
			Question        string `json:"question"`
		}
		if (r.ClosedReason == "" && !isDenial(r.Response)) || json.Unmarshal([]byte(r.Config), &config) != nil || config.FollowupType != "tool_confirmation" {
			continue
		}
		planVersion, option, slot, ok := runStepConfirmationKey(config.ConfirmationKey)
		if !ok {
			continue
		}
		command := questionCommand(config.Question)
		d := deniedApproval{
			MessageId:   r.MessageId,
			Tool:        firstNonEmpty(commandTool(command), toolcore.ToolRunStep),
			Command:     command,
			Option:      option,
			PlanVersion: planVersion,
			DeniedAt:    r.DeniedAt.UTC(),
			DeniedBy:    answeredBy(r.TurnConfig, config.ConfirmationKey, r.MessageId),
			Reason:      r.ClosedReason,
			AskedAt:     r.AskedAt.UTC(),
			AskedBy:     strings.TrimSpace(r.AskedBy),
		}
		if r.ClosedReason == "" {
			d.Note, d.AnsweredFrom = strings.TrimSpace(r.Note), strings.TrimSpace(r.AnsweredFrom)
		}
		if (slot == "apply" || slot == "rollback") && command != "" && option > 0 {
			d.StepKey = events.ApprovalKey(accountId, planEventId, planVersion, fmt.Sprintf("%d:%s", option-1, slot), command)
		}
		out = append(out, d)
	}
	return out
}

// isDenial reports whether a confirmation's answer denied it: no or deny, in any case (the query
// selects the same).
func isDenial(response string) bool {
	switch strings.ToLower(strings.TrimSpace(response)) {
	case "no", "deny":
		return true
	}
	return false
}

// runStepConfirmationKey reads a run_step confirmation key, run_step:<plan version>:<step>:<action>
// (RunStepTool.ConfirmationKey; step is 1-based).
func runStepConfirmationKey(key string) (planVersion string, option int, slot string, ok bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 4 || parts[0] != toolcore.ToolRunStep || parts[1] == "" {
		return "", 0, "", false
	}
	option, err := strconv.Atoi(parts[2])
	if err != nil || option < 1 {
		option = 0
	}
	return parts[1], option, parts[3], true
}

// answeredBy is the person who answered follow-up messageId under key, as the turn it paused recorded
// it (tool_confirmation_refs, written from the answering request's identity); "" when not recorded
// for this very follow-up.
func answeredBy(turnConfig, key, messageId string) string {
	var turn struct {
		Refs map[string]toolcore.ToolConfirmationRef `json:"tool_confirmation_refs"`
	}
	if json.Unmarshal([]byte(turnConfig), &turn) != nil {
		return ""
	}
	ref, ok := turn.Refs[key]
	if !ok || ref.MessageId != messageId {
		return ""
	}
	return strings.TrimSpace(ref.ApprovedBy)
}
