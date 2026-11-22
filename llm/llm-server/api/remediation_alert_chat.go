package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// With the remediation loop on, event-<fingerprint> is the alert's one conversation: its investigation's stages,
// the person's questions, Nubi's plans, its approvals and runs (#40734). The stages' turns are not history for the
// later turns there (core LoadConversationMessages leaves them out); the latest investigation's report, bounded,
// stands in for them (investigationReportForHistory).

func init() {
	core.InvestigationReportForHistory = investigationReportForHistory
}

// investigationSummaryCap bounds the investigation report a later turn in the alert's conversation gets as
// history, in characters.
const investigationSummaryCap = 6000

// ensureAlertChat is the alert's conversation (event-<alert>), created when the alert has none. It is titled like
// an investigation's (investigationConversationTitle): the Investigations tab and the investigation counts find
// the event by the id in the title, and the investigation runs in this conversation.
func ensureAlertChat(accountId, eventId, alert string) (uuid.UUID, error) {
	dao := core.GetConversationDao()
	if dao == nil {
		return uuid.Nil, errors.New("conversations are unavailable")
	}
	session := events.SessionIdPrefixEvent + alert
	conversation, err := dao.GetConversationBySession(accountId, session)
	if err != nil {
		return uuid.Nil, fmt.Errorf("the alert's conversation: %w", err)
	}
	if conversation.ID != uuid.Nil {
		return conversation.ID, nil
	}
	tenant, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant: %w", err)
	}
	id, err := dao.SaveConversation("", session, tenant, accountId, "", "", investigationConversationTitle(eventId),
		core.ConversationStatusCompleted, core.ConversationSourceInvestigation, "", "", nil, "", nil)
	if err != nil {
		return uuid.Nil, fmt.Errorf("creating the alert's conversation: %w", err)
	}
	return id, nil
}

// investigationSummaryText is what a later turn gets from a completed investigation: its final report, else its
// investigation, bounded by investigationSummaryCap; "" when neither has content.
func investigationSummaryText(report, investigation string) string {
	text := strings.TrimSpace(report)
	if text == "" {
		text = strings.TrimSpace(investigation)
	}
	if text == "" {
		return ""
	}
	if len(text) > investigationSummaryCap {
		text = truncate(text, investigationSummaryCap) + "\n\n[investigation summary cut at " + fmt.Sprint(investigationSummaryCap) + " characters]"
	}
	return text
}

// investigationReportForHistory is core.InvestigationReportForHistory: with the loop on, for an alert's
// conversation, the newest completed investigation of the alert (its report, else its investigation, bounded) and
// its event id. "" otherwise, and on any read error (the turn then runs without it).
var investigationReportForHistory = func(ctx *security.RequestContext, accountId, conversationId string) (string, string) {
	if conversationId == "" || !events.RemediationLoopOn(accountId) {
		return "", ""
	}
	dao := core.GetConversationDao()
	if dao == nil {
		return "", ""
	}
	conversation, err := dao.GetConversation(conversationId)
	if err != nil {
		return "", ""
	}
	alert, ok := events.AlertFingerprintOfSession(conversation.SessionID)
	if !ok {
		return "", ""
	}
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", ""
	}
	readCtx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	eventId, report, err := latestInvestigationReport(readCtx, dbManager, accountId, alert)
	if err != nil {
		if ctx != nil {
			ctx.GetLogger().Warn("remediation: investigation report not read for the history", "error", err)
		}
		return "", ""
	}
	return eventId, report
}

// latestInvestigationReport is the newest completed investigation of the alert (account and fingerprint, or the
// event id for an alert without one): its event and investigationSummaryText of its report and investigation.
func latestInvestigationReport(ctx context.Context, dbManager *common.DatabaseManager, accountId, alert string) (string, string, error) {
	var row struct {
		EventId       string `db:"event_id"`
		Report        string `db:"report"`
		Investigation string `db:"investigation"`
	}
	err := dbManager.Db.GetContext(ctx, &row, `SELECT m.event_id::text AS event_id, COALESCE(a.summary, '') AS report,
		       COALESCE((SELECT i.summary FROM event_analysis_mapping im JOIN event_log_analysis i ON i.id = im.analysis_id
		                  WHERE im.event_id = m.event_id AND im.analysis_type = $3 AND i.status = 'COMPLETED'), '') AS investigation
		  FROM event_analysis_mapping m
		  JOIN event_log_analysis a ON a.id = m.analysis_id
		  JOIN events e ON e.id = m.event_id
		 WHERE e.cloud_account_id = $1::uuid AND COALESCE(NULLIF(e.fingerprint, ''), e.id::text) = $2
		   AND m.analysis_type = $4 AND a.status = 'COMPLETED'
		 ORDER BY COALESCE(a.updated_at, a.recorded_at) DESC LIMIT 1`,
		accountId, alert, string(events.AnalysisTypeInvestigation), string(events.AnalysisTypeDetailedResponse))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("latestInvestigationReport: %w", err)
	}
	text := investigationSummaryText(row.Report, row.Investigation)
	if text == "" {
		return "", "", nil
	}
	return row.EventId, text, nil
}
