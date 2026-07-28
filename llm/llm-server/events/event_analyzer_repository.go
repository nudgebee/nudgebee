package events

import (
	"database/sql"
	"encoding/json"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// EventAnalysisRepository handles database operations for event analysis.
type EventAnalysisRepository struct {
	dbManager *common.DatabaseManager
}

// NewEventAnalysisRepository creates a new repository for event analysis.
func NewEventAnalysisRepository(dbManager *common.DatabaseManager) *EventAnalysisRepository {
	return &EventAnalysisRepository{dbManager: dbManager}
}

// EventAnalysisType defines the type of analysis being performed.
type EventAnalysisType string

const (
	AnalysisTypeSummary          EventAnalysisType = "summary"
	AnalysisTypeInvestigation    EventAnalysisType = "investigation"
	AnalysisTypeLog              EventAnalysisType = "log_analysis"
	AnalysisTypeRCA              EventAnalysisType = "rca_analysis"
	AnalysisTypeDetailedResponse EventAnalysisType = "detailed_response"
)

const (
	SessionIdPrefixEvent    = "event-"
	SessionIdPrefixEventRCA = "event-rca-"
)

// AnalysisStatus defines the status of an analysis.
type AnalysisStatus string

const (
	AnalysisStatusInProgress AnalysisStatus = "IN_PROGRESS"
	AnalysisStatusCompleted  AnalysisStatus = "COMPLETED"
	AnalysisStatusFailed     AnalysisStatus = "FAILED"
	AnalysisStatusCreated    AnalysisStatus = "CREATED"
)

// EventInfo holds basic information about an event fetched from the database.
type EventInfo struct {
	ID             string
	Fingerprint    string
	AggregationKey string
}

// EventAnalysis represents an event analysis record from the database.
type EventAnalysis struct {
	Analysis       string
	Status         string
	Summary        string
	RelatedEventId string
	// StatusReason carries the failure detail emitted by the agent / pipeline
	// when ``Status == FAILED``. Populated by ``UpdateEventAnalysisStatus`` and
	// surfaced to the UI so users see *why* a run failed instead of an opaque
	// "Failed" badge.
	StatusReason string
}

// GetEventInfo fetches basic event details (ID, fingerprint, aggregation key) from the database.
func (r *EventAnalysisRepository) GetEventInfo(ctx *security.RequestContext, eventId string, accountId string) (*EventInfo, error) {
	eventSqlQuery := `SELECT id, fingerprint, aggregation_key FROM events WHERE id = $1 and cloud_account_id = $2;`
	rows, err := r.dbManager.Db.Queryx(eventSqlQuery, eventId, accountId)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to get event from database", "error", err, "event_id", eventId)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getEventInfo", "error", err, "event_id", eventId)
		}
	}()

	var dbEventId, dbEventFingerprint, dbEventAggregationKey sql.NullString
	if rows.Next() {
		if err := rows.Scan(&dbEventId, &dbEventFingerprint, &dbEventAggregationKey); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to scan event from database", "error", err, "event_id", eventId)
			return nil, err
		}
		if dbEventId.String == "" {
			return nil, common.Error{Message: "analyzer: event not found - " + eventId}
		}
		return &EventInfo{
			ID:             dbEventId.String,
			Fingerprint:    dbEventFingerprint.String,
			AggregationKey: dbEventAggregationKey.String,
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return nil, common.Error{Message: "analyzer: event not found - " + eventId}
}

// GetEventAnalysis fetches an existing analysis from the database.
func (r *EventAnalysisRepository) GetEventAnalysis(ctx *security.RequestContext, fingerprint, aggKey, accountId string, analysisType EventAnalysisType) (*EventAnalysis, error) {
	sqlQuery := `SELECT analysis, status, event_id, summary, status_reason FROM event_log_analysis WHERE event_fingerprint = $1 and cloud_account_id = $2 and event_aggregation_key = $3 and analysis_type = $4;`
	rows, err := r.dbManager.Db.Queryx(sqlQuery, fingerprint, accountId, aggKey, analysisType)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to get log_analysis from database", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getAnalysis", "error", err, "event_fingerprint", fingerprint, "event_aggregation_key", aggKey)
		}
	}()

	var analysis, status, relatedEventId, summary, statusReason sql.NullString
	if rows.Next() {
		if err := rows.Scan(&analysis, &status, &relatedEventId, &summary, &statusReason); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to scan analysis from database", "error", err)
			return nil, err
		}
		return &EventAnalysis{
			Analysis:       analysis.String,
			Status:         status.String,
			Summary:        summary.String,
			RelatedEventId: relatedEventId.String,
			StatusReason:   statusReason.String,
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Return nil, nil if no analysis is found
	return nil, nil
}

// UpsertEventAnalysisInProgress inserts or updates an analysis entry to 'IN_PROGRESS'.
// The DO UPDATE must bump updated_at so downstream heuristics (stuck-analysis
// cleanup, recovery back-off) see an accurate last-touch timestamp. Without it
// updated_at stays frozen at the first INSERT time even as the row transitions
// through status changes, silently breaking any time-based reasoning on it.
func (r *EventAnalysisRepository) UpsertEventAnalysisInProgress(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey string, analysisType EventAnalysisType) error {
	updateQuery := `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (cloud_account_id, event_fingerprint, event_aggregation_key, analysis_type) DO UPDATE SET analysis = EXCLUDED.analysis, status = EXCLUDED.status, event_id = EXCLUDED.event_id, updated_at = NOW();`
	status := AnalysisStatusInProgress

	_, err := r.dbManager.Db.Exec(updateQuery, eventId, fingerprint, "", "", status, accountId, aggKey, analysisType)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to update analysis in database", "error", err, "event_id", eventId)
		return err
	}
	return nil
}

// ClaimEventAnalysis atomically claims an analysis row for processing,
// transitioning it to IN_PROGRESS and reporting whether THIS caller won the
// claim (claimed == true when exactly one row was affected).
//
// This is the atomic gate that replaces the check-then-write pattern the
// live dispatchers used (read status, then UpsertEventAnalysisInProgress). That
// pattern raced across the entry points that fire for one event on ingestion —
// the two llm-server MQ consumers and the HTTP investigation endpoint — letting
// two of them both read "nothing in progress" before either wrote, and each then
// run the full summary → investigation → log → detailed-response pipeline (the
// observed duplicate-task bug). The INSERT ... ON CONFLICT ... WHERE makes
// claim-or-skip a single statement, so at most one dispatcher proceeds. On a
// concurrent fresh INSERT the conflict serialises on the row: the loser's
// DO UPDATE re-evaluates its WHERE against the winner's committed IN_PROGRESS
// and affects zero rows.
//
// This method is used by the MQ and HTTP dispatch gates. The sync-recovery job
// (syncStuckEventAnalyses) does NOT go through it — it deliberately re-drives
// rows this claim would refuse (already IN_PROGRESS but stale), and is instead
// serialised by leader election plus its own age / conversation-status guards.
//
// The claim never steals a row another worker is actively running (IN_PROGRESS
// is always excluded). The force flag controls COMPLETED handling:
//   - force == false (auto dispatch): a COMPLETED row is NOT re-claimed. Callers
//     use this so a finished analysis keeps its cached result.
//   - force == true (regenerate): a COMPLETED row IS re-claimed, so an explicit
//     regenerate re-runs the pipeline.
func (r *EventAnalysisRepository) ClaimEventAnalysis(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey string, analysisType EventAnalysisType, force bool) (bool, error) {
	// Always exclude IN_PROGRESS (never steal an active run). Additionally
	// exclude COMPLETED unless forcing a regenerate.
	claimQuery := `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
VALUES ($1, $2, '', '', $3, $4, $5, $6)
ON CONFLICT (cloud_account_id, event_fingerprint, event_aggregation_key, analysis_type)
DO UPDATE SET status = EXCLUDED.status, event_id = EXCLUDED.event_id, updated_at = NOW()
WHERE event_log_analysis.status <> $7`
	args := []any{eventId, fingerprint, AnalysisStatusInProgress, accountId, aggKey, analysisType, AnalysisStatusInProgress}
	if !force {
		claimQuery += ` AND event_log_analysis.status <> $8`
		args = append(args, AnalysisStatusCompleted)
	}
	claimQuery += `;`

	result, err := r.dbManager.Db.Exec(claimQuery, args...)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to claim analysis in database", "error", err, "event_id", eventId, "analysis_type", analysisType)
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to read rows affected on claim", "error", err, "event_id", eventId, "analysis_type", analysisType)
		return false, err
	}
	return affected > 0, nil
}

// SaveEventRCAAnalysis saves the final RCA analysis result.
// Bumps updated_at so stuck-RCA detection works — see UpsertEventAnalysisInProgress.
func (r *EventAnalysisRepository) SaveEventRCAAnalysis(ctx *security.RequestContext, eventId, fingerprint, accountId, aggKey, analysisResult string) error {
	updateQuery := `INSERT INTO event_log_analysis (event_id, analysis, status, event_fingerprint, cloud_account_id, event_aggregation_key, analysis_type) VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (event_fingerprint, event_aggregation_key, cloud_account_id, analysis_type) DO UPDATE SET analysis = EXCLUDED.analysis, event_id = EXCLUDED.event_id, status = EXCLUDED.status, updated_at = NOW();`
	// Match parameter order to columns:
	// 1: event_id ($1) -> eventId
	// 2: analysis ($2) -> analysisResult
	// 3: status ($3) -> AnalysisStatusCompleted
	// 4: event_fingerprint ($4) -> fingerprint
	// 5: cloud_account_id ($5) -> accountId
	// 6: event_aggregation_key ($6) -> aggKey
	// 7: analysis_type ($7) -> AnalysisTypeRCA
	_, err := r.dbManager.Db.Exec(updateQuery, eventId, analysisResult, AnalysisStatusCompleted, fingerprint, accountId, aggKey, AnalysisTypeRCA)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to insert rca analysis into database", "error", err, "event_id", eventId)
		return err
	}
	return nil
}

// GetEventRuleDefinition fetches the rule definition and annotations for a given aggregation key.
func (r *EventAnalysisRepository) GetEventRuleDefinition(ctx *security.RequestContext, accountId string, aggregationKey string) (string, map[string]any, error) {
	rows, err := r.dbManager.Db.Query("select expr, annotations::jsonb from event_rules er where er.account_id = $1 and er.alert  = $2", accountId, aggregationKey)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getEventRuleDefinition", "error", err, "rule", aggregationKey)
		}
	}()

	var expr string
	var annotations []byte
	if rows.Next() {
		if err := rows.Scan(&expr, &annotations); err != nil {
			return "", nil, err
		}
		annotationMap := map[string]any{}
		if err := json.Unmarshal(annotations, &annotationMap); err != nil {
			return expr, nil, err
		}
		return expr, annotationMap, nil
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return "", nil, nil
}

// GetEventRuleActionDefinitions fetches action definitions for a given alert rule.
func (r *EventAnalysisRepository) GetEventRuleActionDefinitions(ctx *security.RequestContext, accountId string, aggregationKey string) ([]map[string]any, error) {
	rows, err := r.dbManager.Db.Query(`select action_params
		from agent_playbook
		where alert_name = $2 and cloud_account_id = $1`, accountId, aggregationKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getAlertRuleActionDefinitions", "error", err)
		}
	}()

	alertRuleActionDefinitions := []map[string]any{}
	for rows.Next() {
		var actionParams []byte
		err = rows.Scan(&actionParams)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to scan rows in getAlertRuleActionDefinitions", "error", err)
			continue
		}
		err := common.UnmarshalJson(actionParams, &alertRuleActionDefinitions)
		if err != nil {
			ctx.GetLogger().Error("analyzer: unable to unmarshal rows in getAlertRuleActionDefinitions", "error", err)
			continue
		}
		break
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return alertRuleActionDefinitions, nil
}

// UpdateEventAnalysisStatus updates the status and status reason for a log analysis entry.
func (r *EventAnalysisRepository) UpdateEventAnalysisStatus(ctx *security.RequestContext, eventFingerprint string, cloudAccountId string, aggregationKey string, status string, statusReason string, analysisType EventAnalysisType) error {
	ctx.GetLogger().Info("analyzer: updating event analysis entry", "event_fingerprint", eventFingerprint, "account_id", cloudAccountId, "event_aggregation_key", aggregationKey, "analysis_type", analysisType, "status", status)
	updateQuery := `UPDATE event_log_analysis set  status=$2, status_reason=$3, updated_at=NOW() WHERE event_fingerprint = $1 and event_aggregation_key = $4 and cloud_account_id = $5 and analysis_type = $6;`
	_, err := r.dbManager.Db.Exec(updateQuery, eventFingerprint, status, statusReason, aggregationKey, cloudAccountId, analysisType)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to update analysis status in database", "error", err, "event_id", eventFingerprint)
		return err
	}
	return nil
}

// UpsertEventAnalysis inserts or updates an analysis entry.
// Bumps updated_at so downstream reasoning about staleness works correctly —
// see UpsertEventAnalysisInProgress for the full rationale.
func (r *EventAnalysisRepository) UpsertEventAnalysis(ctx *security.RequestContext, eventId, analysis, summary, status, fingerprint, accountId, aggKey string, analysisType EventAnalysisType) error {
	updateQuery := `INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (cloud_account_id, event_fingerprint, event_aggregation_key, analysis_type)
		DO UPDATE SET summary = EXCLUDED.summary, status = EXCLUDED.status, event_id = EXCLUDED.event_id, analysis = EXCLUDED.analysis, updated_at = NOW();`
	_, err := r.dbManager.Db.Exec(updateQuery, eventId, fingerprint, analysis, summary, status, accountId, aggKey, analysisType)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to update analysis in database", "error", err, "event_id", eventId)
		return err
	}
	return nil
}

// InProgressAnalysis holds minimal data to identify and restart a stuck analysis.
type InProgressAnalysis struct {
	EventId             string
	AccountId           string
	EventFingerprint    string
	EventAggregationKey string
	AnalysisType        EventAnalysisType
	UpdatedAt           time.Time
}

// ListInProgressAnalysis returns all analysis entries that are currently 'IN_PROGRESS'.
func (r *EventAnalysisRepository) ListInProgressAnalysis(ctx *security.RequestContext) ([]InProgressAnalysis, error) {
	sqlQuery := `SELECT event_id, cloud_account_id, event_fingerprint, event_aggregation_key, analysis_type, COALESCE(updated_at, recorded_at) as updated_at FROM event_log_analysis WHERE status = 'IN_PROGRESS';`
	rows, err := r.dbManager.Db.Queryx(sqlQuery)
	if err != nil {
		ctx.GetLogger().Warn("analyzer: failed to list in-progress analyses", "error", err)
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in ListInProgressAnalysis", "error", err)
		}
	}()

	var results []InProgressAnalysis
	for rows.Next() {
		var a InProgressAnalysis
		if err := rows.Scan(&a.EventId, &a.AccountId, &a.EventFingerprint, &a.EventAggregationKey, &a.AnalysisType, &a.UpdatedAt); err != nil {
			ctx.GetLogger().Warn("analyzer: failed to scan in-progress analysis", "error", err)
			continue
		}
		results = append(results, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

type EventKnowledgebase struct {
	Description string `json:"description"`
	Impact      string `json:"impact"`
	Diagnosis   string `json:"diagnosis"`
	Mitigation  string `json:"mitigation"`
}

// GetKnowledgebase inserts or updates an analysis entry.
func (r *EventAnalysisRepository) GetKnowledgebase(ctx *security.RequestContext, rulename string) (EventKnowledgebase, bool) {
	knowledgeQuery := `select description, impact, diagnosis, mitigation from knowledge_base where lower(rule_name) = lower($1)`
	rows, err := r.dbManager.Db.Queryx(knowledgeQuery, rulename)
	if err != nil {
		ctx.GetLogger().Debug("analyzer: failed to get knowledge_base from database", "error", err, "rule_name", rulename)
		return EventKnowledgebase{}, false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("analyzer: unable to close rows in getKnowledgebase", "error", err, "rule_name", rulename)
		}
	}()

	var description, impact, diagnosis, mitigation sql.NullString
	if rows.Next() {
		if err := rows.Scan(&description, &impact, &diagnosis, &mitigation); err != nil {
			ctx.GetLogger().Debug("analyzer: failed to scan knowledgebase from database", "error", err, "rule_name", rulename)
			return EventKnowledgebase{}, false
		}
		kb := EventKnowledgebase{
			Description: description.String,
			Impact:      impact.String,
			Diagnosis:   diagnosis.String,
			Mitigation:  mitigation.String,
		}
		return kb, true
	}
	if err := rows.Err(); err != nil {
		ctx.GetLogger().Debug("analyzer: rows error in getKnowledgebase", "error", err, "rule_name", rulename)
		return EventKnowledgebase{}, false
	}
	return EventKnowledgebase{}, false
}

func (r *EventAnalysisRepository) InsertEventRecommendationResolution(ctx *security.RequestContext, eventId string, parentConversationId string, prUrl string) error {
	_, err := r.dbManager.Db.Exec(`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		common.GenerateUUID(),
		eventId,
		"PullRequest",
		"{}",
		"Success",
		prUrl,
		"NBLLM",
		parentConversationId,
		"PR raised successfully")
	return err
}

// GetAccountRCAFormat fetches the custom RCA format for a given account.
func (r *EventAnalysisRepository) GetAccountRCAFormat(ctx *security.RequestContext, accountId string) (string, error) {
	var format string
	query := `SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'rca_report_format'`
	err := r.dbManager.Db.Get(&format, query, accountId)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // No custom format exists
		}
		ctx.GetLogger().Error("analyzer: failed to get account RCA format", "error", err, "account_id", accountId)
		return "", err
	}
	return format, nil
}

// SetAccountRCAFormat sets the custom RCA format for a given account.
func (r *EventAnalysisRepository) SetAccountRCAFormat(ctx *security.RequestContext, accountId string, format string) error {
	if format == "" {
		// Delete the format if empty
		_, err := r.dbManager.Db.Exec(`DELETE FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = 'rca_report_format'`, accountId)
		return err
	}

	query := `
		INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
		VALUES ($1, 'rca_report_format', $2, NOW(), NOW())
		ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
	`
	_, err := r.dbManager.Db.Exec(query, accountId, format)
	return err
}
