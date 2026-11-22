package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// approvalConversationLive: an approval can be answered only while its conversation (c) waits or runs, the states an
// answer resumes (FollowupResumeV2). In a conversation that failed, was killed or was stopped its row stays WAITING but
// nothing can answer it: it is not offered, keeps nothing busy and blocks no investigation.
const approvalConversationLive = `c.status IN ('WAITING', 'IN_PROGRESS', 'WAITING_FOR_CLIENT_TOOL')`

// conversationNotEnded: the conversation (c) did not fail, was not killed and was not stopped; a row left live in one
// that did is dead.
const conversationNotEnded = `c.status NOT IN ('FAILED', 'KILLED', 'TERMINATED')`

// pendingApprovalMaxAge: an approval older than this is not offered, and no longer holds the alert's
// investigation back (investigationBlocked). The hourly waiting-expiry job closes it soon after, marked
// lapsed (core CloseWaitingConversation); one from before the alert cleared and fired again is closed
// as superseded on the next remediation_followups tick (supersedeEarlierEpisodeApprovals).
const pendingApprovalMaxAge = 12 * time.Hour

// pendingApprovals lists the writes Nubi is waiting on a person to approve for this incident: the
// tool confirmations of the alert's conversation still WAITING, asked during the incident the read is
// for (incidentWindow) and less than 12 h ago, newest first. The conversation spans every incident of
// the alert, so one asked during an earlier incident stays on that incident's page. Each says where it
// was asked: an approval asked in a Slack thread is answered there, because answering it elsewhere
// would not post the outcome back to the thread. read is false when they could not be read, which the
// tab must not take for "none are waiting".
var pendingApprovals = func(ctx *security.RequestContext, in remediationGetInput) (approvals []pendingApproval, read bool) {
	session := planConversationSession(in)
	rows, err := waitingConfirmations(ctx, in.AccountId, session, firstNonEmpty(in.PlanEventId, in.EventId))
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Warn("remediation_get: pending approvals not read", "error", err)
		}
		return nil, false
	}
	return pendingApprovalsOf(rows), true
}

// planConversationSession is the alert conversation the read's approvals are asked in: the plan event's
// (event-<its fingerprint>, or event-<its id> without one), which for a duplicate is its main event's
// even when the two fingerprints differ.
func planConversationSession(in remediationGetInput) string {
	if in.PlanEventId != "" && in.PlanEventId != in.EventId {
		return events.SessionIdPrefixEvent + firstNonEmpty(in.PlanFingerprint, in.PlanEventId)
	}
	return events.SessionIdPrefixEvent + firstNonEmpty(in.PlanFingerprint, in.Fingerprint, in.EventId)
}

// waitingConfirmation is a WAITING tool-confirmation follow-up message of a conversation, as stored.
type waitingConfirmation struct {
	ConversationId string    `db:"conversation_id"`
	MessageId      string    `db:"message_id"`
	AgentId        string    `db:"agent_id"`
	CreatedAt      time.Time `db:"created_at"`
	// Config is the follow-up's message_config (agentcore.FollowupRequest as stored); Context its
	// message_context (the request that raised it).
	Config  string `db:"config"`
	Context string `db:"context"`
}

// incidentWindow is the incident a remediation read is for, as a CTE over its plan event ($4, on
// account $2): from that event's creation until the next incident of its alert starts (the next event
// of the fingerprint that is not a duplicate or suppressed), or open while none has. An approval
// belongs to the incident it was asked in.
const incidentWindow = `WITH incident AS (
	SELECT p.created_at AS starts_at,
	       (SELECT min(n.created_at) FROM events n
	         WHERE n.cloud_account_id = p.cloud_account_id AND n.fingerprint = p.fingerprint AND n.id <> p.id
	           AND n.created_at > p.created_at AND COALESCE(n.nb_status, '') NOT IN ('DUPLICATE', 'SUPPRESSED')) AS ends_at
	  FROM events p WHERE p.id = $4::uuid AND p.cloud_account_id = $2::uuid)
	`

// askedInIncident limits follow-up messages m to those asked during incidentWindow.
const askedInIncident = `m.created_at >= incident.starts_at AND (incident.ends_at IS NULL OR m.created_at < incident.ends_at)`

// waitingConfirmations reads the conversation's WAITING tool confirmations asked during the plan
// event's incident and within pendingApprovalMaxAge, newest first, each with the agent waiting on it.
// A var so tests can stub it.
var waitingConfirmations = func(ctx *security.RequestContext, accountId, session, planEventId string) ([]waitingConfirmation, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, fmt.Errorf("waitingConfirmations: %w", err)
	}
	return waitingConfirmationsIn(ctx, dbManager.Db, accountId, session, planEventId)
}

func waitingConfirmationsIn(ctx *security.RequestContext, db *sqlx.DB, accountId, session, planEventId string) ([]waitingConfirmation, error) {
	if _, err := uuid.Parse(planEventId); err != nil {
		return nil, fmt.Errorf("waitingConfirmations: plan event %q: %w", planEventId, err)
	}
	readCtx, cancel := context.WithTimeout(requestGoContext(ctx), decisionDBTimeout)
	defer cancel()
	var rows []waitingConfirmation
	// One row per message: a delegated write's follow-up is pointed at by the agent that asked and its
	// parent; the waiting one, then the newest, is the one an answer resumes.
	err := db.SelectContext(readCtx, &rows, incidentWindow+`SELECT conversation_id, message_id, agent_id, created_at, config, context FROM (
		SELECT DISTINCT ON (m.id) m.conversation_id::text AS conversation_id, m.id::text AS message_id, COALESCE(a.id::text, '') AS agent_id, m.created_at,
		       COALESCE(m.message_config, '') AS config, COALESCE(m.message_context, '') AS context
		  FROM llm_conversation_messages m
		  JOIN llm_conversations c ON c.id = m.conversation_id
		  CROSS JOIN incident
		  LEFT JOIN llm_conversation_agent a ON a.account_id = c.account_id AND a.followup_message_id = m.id
		 WHERE c.session_id = $1 AND c.account_id = $2::uuid
		   AND m.message_type = 'followup' AND m.status = 'WAITING' AND `+approvalConversationLive+`
		   AND m.created_at > LOCALTIMESTAMP - $3::interval
		   AND `+askedInIncident+`
		   AND m.message_config LIKE '%"followupType":"tool_confirmation"%'
		 ORDER BY m.id, (COALESCE(a.status, '') = 'waiting') DESC, a.created_at DESC NULLS LAST) waiting
		 ORDER BY created_at DESC`,
		session, accountId, fmt.Sprintf("%d seconds", int(pendingApprovalMaxAge.Seconds())), planEventId)
	if err != nil {
		return nil, fmt.Errorf("waitingConfirmations: %w", err)
	}
	return rows, nil
}

// requestGoContext is ctx's context, or a background one without a request.
func requestGoContext(ctx *security.RequestContext) context.Context {
	if ctx != nil && ctx.GetContext() != nil {
		return ctx.GetContext()
	}
	return context.Background()
}

// pendingApprovalsOf turns the stored follow-ups into the read's pending approvals.
func pendingApprovalsOf(rows []waitingConfirmation) []pendingApproval {
	var out []pendingApproval
	for _, r := range rows {
		var config struct {
			FollowupType string `json:"followupType"`
			ToolName     string `json:"toolName"`
			Question     string `json:"question"`
		}
		if json.Unmarshal([]byte(r.Config), &config) != nil || config.FollowupType != "tool_confirmation" {
			continue
		}
		command := questionCommand(config.Question)
		tool := config.ToolName
		if write, ok := chatWriteOf("", config.ToolName, `{"command":`+jsonString(command)+`}`); ok {
			tool = write.Tool
		}
		if _, ok := automationRefID(command); ok {
			tool = chatWriteToolAutomation
		}
		origin, threadUrl := approvalOrigin(r.Context)
		out = append(out, pendingApproval{
			ConversationId: r.ConversationId,
			MessageId:      r.MessageId,
			AgentId:        r.AgentId,
			ThreadUrl:      threadUrl,
			Tool:           tool,
			Command:        command,
			Automation:     questionAutomation(config.Question),
			CreatedAt:      r.CreatedAt,
			Origin:         origin,
		})
	}
	return out
}

// questionCommand is the command an approval card asks about: the text after its last "Command - ",
// unwrapped from a {"command": …} or {"query": …} input.
// automationCardPrefix opens the approval card's line naming the automation a step runs and its inputs.
const automationCardPrefix = "Automation - "

// questionAutomation is the automation an approval card names (its automationCardPrefix line), or "".
func questionAutomation(question string) string {
	for _, line := range strings.Split(question, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), automationCardPrefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func questionCommand(question string) string {
	command := question
	if i := strings.LastIndex(command, "Command - "); i >= 0 {
		command = command[i+len("Command - "):]
	}
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, "{") {
		var input map[string]any
		if json.Unmarshal([]byte(command), &input) == nil {
			for _, k := range []string{"command", "query"} {
				if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
		if parsed := strings.TrimSpace(toolcore.ParseNBToolCallRequestFromInput(command).Command); parsed != "" {
			return parsed
		}
	}
	return command
}

// approvalOrigin is where the request that raised an approval was made: a Slack thread when it
// carries the notification's reply reference (<channel>-<thread ts>), else the web. For Slack, the
// thread's link when the workspace is known from a permalink the request carries; "" otherwise.
func approvalOrigin(messageContext string) (origin, threadUrl string) {
	var request struct {
		ReplyRef           string `json:"reply_ref"`
		ChannelContextRefs struct {
			Messages []struct {
				Permalink *string `json:"permalink"`
			} `json:"messages"`
		} `json:"channel_context_refs"`
	}
	if json.Unmarshal([]byte(messageContext), &request) != nil || strings.TrimSpace(request.ReplyRef) == "" {
		return ApprovalOriginWeb, ""
	}
	for _, m := range request.ChannelContextRefs.Messages {
		if m.Permalink == nil {
			continue
		}
		if host := slackWorkspaceHost(*m.Permalink); host != "" {
			return ApprovalOriginSlack, slackThreadUrl(host, request.ReplyRef)
		}
	}
	return ApprovalOriginSlack, ""
}

// slackWorkspaceHost is the workspace host of a Slack permalink (https://<workspace>.slack.com/…), or "".
func slackWorkspaceHost(permalink string) string {
	u, err := url.Parse(strings.TrimSpace(permalink))
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Host, ".slack.com") {
		return ""
	}
	return u.Host
}

// slackThreadUrl links to the thread a reply reference names (<channel>-<thread ts>) in the
// workspace at host, or "" when the reference is not one.
func slackThreadUrl(host, replyRef string) string {
	channel, ts, ok := strings.Cut(strings.TrimSpace(replyRef), "-")
	if !ok || channel == "" || ts == "" || strings.Contains(ts, "-") {
		return ""
	}
	digits := strings.ReplaceAll(ts, ".", "")
	for _, r := range channel + digits {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return ""
		}
	}
	return "https://" + host + "/archives/" + channel + "/p" + digits
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
