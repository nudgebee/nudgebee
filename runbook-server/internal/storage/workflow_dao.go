package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type WorkflowDao struct {
	db *sqlx.DB
}

func NewWorkflowDao() (*WorkflowDao, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	return &WorkflowDao{db: db.Db}, nil
}

func (s *WorkflowDao) Db() *sqlx.DB {
	return s.db
}

// CreateWorkflowWithInitialVersion inserts a new workflow row, snapshots it as
// version 1 (source='create'), and points workflows.live_version_id at that
// version — all inside a single transaction. Doing this atomically guarantees a
// workflow is never left created-but-not-executable (no live version) if a
// later step fails.
//
// Named returns (workflowID, version, err) are required so the deferred
// tx.Commit() error is propagated to the caller.
func (s *WorkflowDao) CreateWorkflowWithInitialVersion(ctx context.Context, tenantID, accountID string, wf model.Workflow) (workflowID string, version *model.WorkflowVersion, err error) {
	id := wf.ID
	if id == "" {
		id = uuid.New().String()
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in CreateWorkflowWithInitialVersion: %v", p)
			err = fmt.Errorf("failed to create workflow due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	wfBytes, err := json.Marshal(wf.Definition)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal workflow: %w", err)
	}
	tagBytes, err := json.Marshal(wf.Tags)
	if err != nil {
		log.Printf("failed to marshal tags: %v", err)
	}

	var createdFromSessionID sql.NullString
	if wf.CreatedFromSessionID != nil && *wf.CreatedFromSessionID != "" {
		createdFromSessionID = sql.NullString{String: *wf.CreatedFromSessionID, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO workflows (id, tenant_id, account_id, name, definition, tags, status, created_by, updated_by, created_from_session_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, id, tenantID, accountID, wf.Name, wfBytes, tagBytes, wf.Status, wf.CreatedBy, wf.UpdatedBy, createdFromSessionID)
	if err != nil {
		return "", nil, fmt.Errorf("failed to save workflow: %w", err)
	}

	var createdByVal sql.NullString
	if wf.CreatedBy != "" {
		createdByVal = sql.NullString{String: wf.CreatedBy, Valid: true}
	}
	var versionID string
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO workflow_versions (workflow_id, version_number, definition, source, created_by)
		VALUES ($1, 1, $2, $3, $4)
		RETURNING id::text, created_at
	`, id, wfBytes, string(model.WorkflowVersionSourceCreate), createdByVal).Scan(&versionID, &createdAt)
	if err != nil {
		return "", nil, fmt.Errorf("failed to insert initial workflow version: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `UPDATE workflows SET live_version_id = $1 WHERE id = $2`, versionID, id); err != nil {
		return "", nil, fmt.Errorf("failed to set initial live version: %w", err)
	}

	version = &model.WorkflowVersion{
		ID:            versionID,
		WorkflowID:    id,
		VersionNumber: 1,
		Definition:    wf.Definition,
		Source:        model.WorkflowVersionSourceCreate,
		IsLive:        true,
		CreatedBy:     wf.CreatedBy,
		CreatedAt:     createdAt,
	}
	return id, version, nil
}

func (s *WorkflowDao) List(ctx context.Context, tenantID, accountID string, request model.ListWorkflowRequest) ([]model.Workflow, int, error) {
	var workflows []model.Workflow

	// Build the base WHERE clause with filters (shared by both queries)
	// Note: Main query uses 'w.' prefix, count query uses no prefix
	whereClause := "WHERE tenant_id = $1 AND account_id = $2"
	whereClauseWithAlias := "WHERE w.tenant_id = $1 AND w.account_id = $2"
	baseArgs := []any{tenantID, accountID}
	argId := 3

	if request.Name != "" {
		whereClause += fmt.Sprintf(" AND name ILIKE $%d", argId)
		whereClauseWithAlias += fmt.Sprintf(" AND w.name ILIKE $%d", argId)
		baseArgs = append(baseArgs, "%"+request.Name+"%")
		argId++
	}

	if request.Status != "" {
		whereClause += fmt.Sprintf(" AND status = $%d", argId)
		whereClauseWithAlias += fmt.Sprintf(" AND w.status = $%d", argId)
		baseArgs = append(baseArgs, request.Status)
		argId++
	}

	if request.LastExecutionStatus != "" {
		whereClause += fmt.Sprintf(" AND last_execution_status = $%d", argId)
		whereClauseWithAlias += fmt.Sprintf(" AND w.last_execution_status = $%d", argId)
		baseArgs = append(baseArgs, request.LastExecutionStatus)
		argId++
	}

	if request.TriggerType != "" {
		// Accepts either a single value or a comma-separated list.
		// Each value becomes a JSON-containment clause OR'd together so the
		// listing can filter by multiple trigger types at once.
		var triggerTypes []string
		for _, t := range strings.Split(request.TriggerType, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				triggerTypes = append(triggerTypes, t)
			}
		}
		if len(triggerTypes) > 0 {
			clauses := make([]string, 0, len(triggerTypes))
			clausesWithAlias := make([]string, 0, len(triggerTypes))
			for _, t := range triggerTypes {
				filter := map[string]any{
					"triggers": []map[string]any{
						{"type": t},
					},
				}
				filterBytes, err := json.Marshal(filter)
				if err != nil {
					return nil, 0, fmt.Errorf("failed to marshal trigger type filter: %w", err)
				}
				clauses = append(clauses, fmt.Sprintf("definition @> $%d", argId))
				clausesWithAlias = append(clausesWithAlias, fmt.Sprintf("w.definition @> $%d", argId))
				baseArgs = append(baseArgs, filterBytes)
				argId++
			}
			whereClause += " AND (" + strings.Join(clauses, " OR ") + ")"
			whereClauseWithAlias += " AND (" + strings.Join(clausesWithAlias, " OR ") + ")"
		}
	}

	if len(request.Tags) > 0 {
		for key, value := range request.Tags {
			if key == "" {
				continue
			}
			// Sanitize the key to prevent SQL injection
			quotedKey := pq.QuoteIdentifier(key)
			// Use ->> to extract value as text for comparison, matching the string input from query params
			whereClause += fmt.Sprintf(" AND tags->>%s = $%d", quotedKey, argId)
			whereClauseWithAlias += fmt.Sprintf(" AND w.tags->>%s = $%d", quotedKey, argId)
			baseArgs = append(baseArgs, value)
			argId++
		}
	}

	if request.CreatedBy != "" {
		whereClause += fmt.Sprintf(" AND created_by IN (SELECT id FROM users WHERE display_name = $%d)", argId)
		whereClauseWithAlias += fmt.Sprintf(" AND w.created_by IN (SELECT id FROM users WHERE display_name = $%d)", argId)
		baseArgs = append(baseArgs, request.CreatedBy)
		argId++
	}

	for _, tag := range request.TagsText {
		// Text-based search on tags column - works for both JSON arrays and objects
		whereClause += fmt.Sprintf(" AND tags::text ILIKE $%d", argId)
		whereClauseWithAlias += fmt.Sprintf(" AND w.tags::text ILIKE $%d", argId)
		baseArgs = append(baseArgs, "%"+tag+"%")
		argId++
	}

	// Build count query (with all filters, no LIMIT/OFFSET)
	countQuery := "SELECT COUNT(*) FROM workflows " + whereClause

	// Get total count
	var totalCount int
	err := s.db.QueryRowContext(ctx, countQuery, baseArgs...).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get workflow count: %w", err)
	}

	// Build main query (with all filters + LIMIT/OFFSET)
	// Join with users table to get user details for created_by and updated_by
	mainQuery := `
		SELECT w.id::text, w.name, w.definition, w.tags, w.status, w.last_execution_status, w.last_execution_status_message, w.last_execution_time, w.created_by, w.updated_by, w.created_at, w.updated_at, w.created_from_session_id,
			cu.id::text as created_by_user_id, cu.display_name as created_by_display_name,
			uu.id::text as updated_by_user_id, uu.display_name as updated_by_display_name,
			w.live_version_id::text, lv.version_number, lv.name
		FROM workflows w
		LEFT JOIN users cu ON w.created_by = cu.id
		LEFT JOIN users uu ON w.updated_by = uu.id
		LEFT JOIN workflow_versions lv ON lv.id = w.live_version_id ` + whereClauseWithAlias + ` order by w.created_at DESC`

	// Copy baseArgs for main query and add LIMIT/OFFSET
	mainArgs := make([]any, len(baseArgs))
	copy(mainArgs, baseArgs)
	mainArgId := argId

	if request.Limit > 0 {
		mainQuery += fmt.Sprintf(" LIMIT $%d", mainArgId)
		mainArgs = append(mainArgs, request.Limit)
		mainArgId++
	}

	if request.NextPageToken != "" {
		offset, err := strconv.Atoi(request.NextPageToken)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid next_page_token: %w", err)
		}
		mainQuery += fmt.Sprintf(" OFFSET $%d", mainArgId)
		mainArgs = append(mainArgs, offset)
	}

	rows, err := s.db.QueryContext(ctx, mainQuery, mainArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to retrieve workflows: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("failed to close rows: %v", err)
		}
	}()

	for rows.Next() {
		var wfID string
		var wfName string
		var wfBytes []byte
		var tagBytes []byte
		var status model.WorkflowStatus
		var lastExecutionStatus sql.NullString
		var lastExecutionStatusMessage sql.NullString
		var lastExecutionTime sql.NullTime
		var createdBy, updatedBy string
		var createdAt, updatedAt time.Time
		var createdFromSessionID sql.NullString
		// User detail fields from JOIN
		var createdByUserID, createdByDisplayName sql.NullString
		var updatedByUserID, updatedByDisplayName sql.NullString
		var liveVersionID sql.NullString
		var liveVersionNumber sql.NullInt64
		var liveVersionName sql.NullString

		if err := rows.Scan(&wfID, &wfName, &wfBytes, &tagBytes, &status, &lastExecutionStatus, &lastExecutionStatusMessage, &lastExecutionTime, &createdBy, &updatedBy, &createdAt, &updatedAt, &createdFromSessionID,
			&createdByUserID, &createdByDisplayName, &updatedByUserID, &updatedByDisplayName,
			&liveVersionID, &liveVersionNumber, &liveVersionName); err != nil {
			return nil, 0, fmt.Errorf("failed to scan workflow: %w", err)
		}

		var wf model.Workflow
		if err := json.Unmarshal(wfBytes, &wf.Definition); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal workflow definition: %w", err)
		}
		if err := json.Unmarshal(tagBytes, &wf.Tags); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal workflow tags: %w", err)
		}
		wf.ID = wfID
		wf.Name = wfName
		wf.Status = status
		if lastExecutionStatus.Valid {
			wf.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecutionStatus.String)
		}
		if lastExecutionStatusMessage.Valid {
			wf.LastExecutionStatusMessage = &lastExecutionStatusMessage.String
		}
		if lastExecutionTime.Valid {
			wf.LastExecutionTime = &lastExecutionTime.Time
		}
		wf.AccountID = accountID
		wf.TenantID = tenantID
		wf.CreatedBy = createdBy
		wf.UpdatedBy = updatedBy
		wf.CreatedAt = createdAt
		wf.UpdatedAt = updatedAt
		if createdFromSessionID.Valid {
			wf.CreatedFromSessionID = &createdFromSessionID.String
		}

		// Build user objects if data exists
		if createdByUserID.Valid {
			wf.CreatedByUser = &model.WorkflowUser{
				ID:          createdByUserID.String,
				DisplayName: createdByDisplayName.String,
			}
		}
		if updatedByUserID.Valid {
			wf.UpdatedByUser = &model.WorkflowUser{
				ID:          updatedByUserID.String,
				DisplayName: updatedByDisplayName.String,
			}
		}
		applyLiveVersion(&wf, liveVersionID, liveVersionNumber, liveVersionName)

		workflows = append(workflows, wf)
	}

	return workflows, totalCount, nil
}

func (s *WorkflowDao) Find(ctx context.Context, tenantID, accountID string, id string) (*model.Workflow, error) {
	var wfID string
	var wfName string
	var wfBytes []byte
	var tagBytes []byte
	var status model.WorkflowStatus
	var lastExecutionStatus sql.NullString
	var lastExecutionStatusMessage sql.NullString
	var lastExecutionTime sql.NullTime
	var createdBy, updatedBy string
	var createdAt, updatedAt time.Time
	var createdFromSessionID sql.NullString
	var liveVersionID sql.NullString
	var liveVersionNumber sql.NullInt64
	var liveVersionName sql.NullString

	query := `
		SELECT w.id::text, w.name, w.definition, w.tags, w.status, w.last_execution_status, w.last_execution_status_message, w.last_execution_time, w.created_by, w.updated_by, w.created_at, w.updated_at, w.created_from_session_id,
		       w.live_version_id::text, lv.version_number, lv.name
		FROM workflows w
		LEFT JOIN workflow_versions lv ON lv.id = w.live_version_id
		WHERE w.tenant_id = $1 AND w.account_id = $2 AND w.id = $3
	`
	err := s.db.QueryRowContext(ctx, query, tenantID, accountID, id).Scan(
		&wfID, &wfName, &wfBytes, &tagBytes, &status,
		&lastExecutionStatus, &lastExecutionStatusMessage, &lastExecutionTime,
		&createdBy, &updatedBy, &createdAt, &updatedAt, &createdFromSessionID,
		&liveVersionID, &liveVersionNumber, &liveVersionName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve workflow: %w", err)
	}

	var wf model.Workflow
	if err := json.Unmarshal(wfBytes, &wf.Definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow definition: %w", err)
	}
	if err := json.Unmarshal(tagBytes, &wf.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow tags: %w", err)
	}
	wf.ID = wfID
	wf.Name = wfName
	wf.Status = status
	if lastExecutionStatus.Valid {
		wf.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecutionStatus.String)
	}
	if lastExecutionStatusMessage.Valid {
		wf.LastExecutionStatusMessage = &lastExecutionStatusMessage.String
	}
	if lastExecutionTime.Valid {
		wf.LastExecutionTime = &lastExecutionTime.Time
	}
	wf.AccountID = accountID
	wf.TenantID = tenantID
	wf.CreatedBy = createdBy
	wf.UpdatedBy = updatedBy
	wf.CreatedAt = createdAt
	wf.UpdatedAt = updatedAt
	if createdFromSessionID.Valid {
		wf.CreatedFromSessionID = &createdFromSessionID.String
	}
	applyLiveVersion(&wf, liveVersionID, liveVersionNumber, liveVersionName)

	return &wf, nil
}

// applyLiveVersion copies the joined live_version columns onto wf if present.
// Centralized so List / Find / FindByName / FindByIntegrationName stay consistent.
func applyLiveVersion(wf *model.Workflow, id sql.NullString, number sql.NullInt64, name sql.NullString) {
	if id.Valid && id.String != "" {
		s := id.String
		wf.LiveVersionID = &s
	}
	if number.Valid {
		n := int(number.Int64)
		wf.LiveVersionNumber = &n
	}
	if name.Valid && name.String != "" {
		s := name.String
		wf.LiveVersionName = &s
	}
}

func (s *WorkflowDao) FindByName(ctx context.Context, tenantID, accountID, name string) (*model.Workflow, error) {
	var id string
	var wfName string
	var wfBytes []byte
	var tagBytes []byte
	var status model.WorkflowStatus
	var lastExecutionStatus sql.NullString
	var lastExecutionStatusMessage sql.NullString
	var lastExecutionTime sql.NullTime
	var createdBy, updatedBy string
	var createdAt, updatedAt time.Time
	var createdFromSessionID sql.NullString
	var liveVersionID sql.NullString
	var liveVersionNumber sql.NullInt64
	var liveVersionName sql.NullString

	query := `
		SELECT w.id::text, w.name, w.definition, w.tags, w.status, w.last_execution_status, w.last_execution_status_message, w.last_execution_time, w.created_by, w.updated_by, w.created_at, w.updated_at, w.created_from_session_id,
		       w.live_version_id::text, lv.version_number, lv.name
		FROM workflows w
		LEFT JOIN workflow_versions lv ON lv.id = w.live_version_id
		WHERE w.tenant_id = $1 AND w.account_id = $2 AND w.name = $3
	`
	err := s.db.QueryRowContext(ctx, query, tenantID, accountID, name).Scan(
		&id, &wfName, &wfBytes, &tagBytes, &status,
		&lastExecutionStatus, &lastExecutionStatusMessage, &lastExecutionTime,
		&createdBy, &updatedBy, &createdAt, &updatedAt, &createdFromSessionID,
		&liveVersionID, &liveVersionNumber, &liveVersionName,
	)
	if err != nil {
		return nil, err // sql.ErrNoRows will be returned here if not found
	}

	var wf model.Workflow
	if err := json.Unmarshal(wfBytes, &wf.Definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow definition: %w", err)
	}
	if err := json.Unmarshal(tagBytes, &wf.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow tags: %w", err)
	}
	wf.ID = id
	wf.Name = wfName
	wf.Status = status
	if lastExecutionStatus.Valid {
		wf.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecutionStatus.String)
	}
	if lastExecutionStatusMessage.Valid {
		wf.LastExecutionStatusMessage = &lastExecutionStatusMessage.String
	}
	if lastExecutionTime.Valid {
		wf.LastExecutionTime = &lastExecutionTime.Time
	}
	wf.AccountID = accountID
	wf.TenantID = tenantID
	wf.CreatedBy = createdBy
	wf.UpdatedBy = updatedBy
	wf.CreatedAt = createdAt
	wf.UpdatedAt = updatedAt
	if createdFromSessionID.Valid {
		wf.CreatedFromSessionID = &createdFromSessionID.String
	}
	applyLiveVersion(&wf, liveVersionID, liveVersionNumber, liveVersionName)

	return &wf, nil
}

func (s *WorkflowDao) FindEventTriggers(ctx context.Context) ([]model.WorkflowEventTriggerRule, error) {
	query := `
		SELECT
			id, tenant_id, account_id,
			COALESCE(event_type_item, '') as event_type,
			COALESCE(trigger->'params'->>'filter', '') as filter,
			'event' as trigger_type
		FROM workflows,
			 jsonb_array_elements(
				CASE
					WHEN jsonb_typeof(definition->'triggers') = 'array' THEN definition->'triggers'
					ELSE '[]'::jsonb
				END
			 ) as trigger,
			 LATERAL jsonb_array_elements_text(
				CASE
					WHEN jsonb_typeof(trigger->'params'->'event_type') = 'array'
						 AND jsonb_array_length(trigger->'params'->'event_type') > 0
						THEN trigger->'params'->'event_type'
					WHEN jsonb_typeof(trigger->'params'->'event_type') = 'string'
						 AND length(trigger->'params'->>'event_type') > 0
						THEN jsonb_build_array(trigger->'params'->>'event_type')
					ELSE '[null]'::jsonb
				END
			 ) as event_type_item
		WHERE status = 'ACTIVE'
		  AND trigger->>'type' = 'event'

		UNION ALL

		SELECT
			id, tenant_id, account_id,
			'optimization.recommendation' as event_type,
			COALESCE((trigger->'params')::text, '') as filter,
			'optimization' as trigger_type
		FROM workflows,
			 jsonb_array_elements(
				CASE
					WHEN jsonb_typeof(definition->'triggers') = 'array' THEN definition->'triggers'
					ELSE '[]'::jsonb
				END
			 ) as trigger
		WHERE status = 'ACTIVE'
		  AND trigger->>'type' = 'optimization'
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query event triggers: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("failed to close rows: %v", err)
		}
	}()

	var rules []model.WorkflowEventTriggerRule
	for rows.Next() {
		var rule model.WorkflowEventTriggerRule
		if err := rows.Scan(&rule.WorkflowID, &rule.TenantID, &rule.AccountID, &rule.EventType, &rule.Filter, &rule.TriggerType); err != nil {
			return nil, fmt.Errorf("failed to scan event trigger rule: %w", err)
		}
		// For optimization triggers, build Jinja filter from structured params.
		// Skip rows that produce no filter (missing/empty params) — registering an
		// unfiltered optimization rule would match every recommendation event.
		if rule.TriggerType == model.WorkflowTriggerOptimization {
			rule.Filter = buildOptimizationFilter(rule.Filter)
			if rule.Filter == "" {
				continue
			}
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating event trigger rows: %w", err)
	}
	return rules, nil
}

// buildOptimizationFilter converts optimization trigger params JSON into a Jinja filter expression.
func buildOptimizationFilter(paramsJSON string) string {
	if paramsJSON == "" {
		return ""
	}

	var params map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		return ""
	}

	var conditions []string

	// Build "in" conditions for array params
	for _, field := range []struct {
		paramKey string
		eventKey string
	}{
		{"categories", "category"},
		{"rule_names", "rule_name"},
		// `clusters` carries cloud_account UUIDs from the frontend (the cluster picker
		// stores account_id as the option value), so match against event.cloud_account_id
		// — already populated by the poller from recommendation.cloud_account_id —
		// instead of resolving UUIDs back to names at filter build time.
		{"clusters", "cloud_account_id"},
	} {
		if val, ok := params[field.paramKey]; ok {
			if arr, arrOk := val.([]any); arrOk && len(arr) > 0 {
				var items []string
				for _, item := range arr {
					if s, sOk := item.(string); sOk {
						// Escape single quotes to prevent Jinja injection
						escaped := strings.ReplaceAll(s, "'", "\\'")
						items = append(items, fmt.Sprintf("'%s'", escaped))
					}
				}
				if len(items) > 0 {
					conditions = append(conditions, fmt.Sprintf("event.%s in [%s]", field.eventKey, strings.Join(items, ", ")))
				}
			}
		}
	}

	// Append explicit filter if present, wrapped in parens to preserve precedence
	if filterVal, ok := params["filter"]; ok {
		if filterStr, strOk := filterVal.(string); strOk && filterStr != "" {
			// Strip template braces if present to combine
			trimmed := strings.TrimSpace(filterStr)
			trimmed = strings.TrimPrefix(trimmed, "{{")
			trimmed = strings.TrimSuffix(trimmed, "}}")
			trimmed = strings.TrimSpace(trimmed)
			if trimmed != "" {
				conditions = append(conditions, "("+trimmed+")")
			}
		}
	}

	if len(conditions) == 0 {
		return ""
	}

	return "{{ " + strings.Join(conditions, " and ") + " }}"
}

func (s *WorkflowDao) GetState(ctx context.Context, workflowID string) ([]model.WorkflowStateItem, error) {
	query := `SELECT key, value, updated_at, expires_at, last_updated_by_execution_id, last_updated_by_task_id FROM workflow_state WHERE workflow_id = $1`
	rows, err := s.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to query workflow state: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("failed to close rows: %v", err)
		}
	}()

	var items []model.WorkflowStateItem
	for rows.Next() {
		var item model.WorkflowStateItem
		var valueBytes []byte
		var expiresAt sql.NullTime
		var lastExecID, lastTaskID sql.NullString

		if err := rows.Scan(&item.Key, &valueBytes, &item.UpdatedAt, &expiresAt, &lastExecID, &lastTaskID); err != nil {
			return nil, fmt.Errorf("failed to scan workflow state: %w", err)
		}

		if err := json.Unmarshal(valueBytes, &item.Value); err != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow state value for key %s: %w", item.Key, err)
		}

		if expiresAt.Valid {
			item.ExpiresAt = &expiresAt.Time
		}
		if lastExecID.Valid {
			item.LastUpdatedByExecutionID = lastExecID.String
		}
		if lastTaskID.Valid {
			item.LastUpdatedByTaskID = lastTaskID.String
		}

		items = append(items, item)
	}
	return items, nil
}

func (s *WorkflowDao) SetState(ctx context.Context, workflowID string, updates []model.WorkflowStateUpdate) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in SetState: %v", p)
			err = fmt.Errorf("failed to set workflow state due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	query := `
		INSERT INTO workflow_state (workflow_id, key, value, updated_at, last_updated_by_execution_id, last_updated_by_task_id, expires_at)
		VALUES ($1, $2, $3, NOW(), $4, $5, $6)
		ON CONFLICT (workflow_id, key)
		DO UPDATE SET 
			value = EXCLUDED.value, 
			updated_at = NOW(),
			last_updated_by_execution_id = EXCLUDED.last_updated_by_execution_id,
			last_updated_by_task_id = EXCLUDED.last_updated_by_task_id,
			expires_at = EXCLUDED.expires_at
	`

	for _, update := range updates {
		valueBytes, err := json.Marshal(update.Value)
		if err != nil {
			return fmt.Errorf("failed to marshal state value for key %s: %w", update.Key, err)
		}
		_, err = tx.ExecContext(ctx, query, workflowID, update.Key, valueBytes, update.ExecutionID, update.TaskID, update.ExpiresAt)
		if err != nil {
			return fmt.Errorf("failed to upsert workflow state for key %s: %w", update.Key, err)
		}
	}

	return nil
}

func (s *WorkflowDao) DeleteExpiredState(ctx context.Context, limit int) (int64, error) {
	query := `
		WITH deleted AS (
			DELETE FROM workflow_state
			WHERE expires_at < NOW()
			AND ctid IN (
				SELECT ctid FROM workflow_state
				WHERE expires_at < NOW()
				LIMIT $1
			)
			RETURNING *
		)
		SELECT count(*) FROM deleted
	`
	var count int64
	err := s.db.GetContext(ctx, &count, query, limit)
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired state: %w", err)
	}
	return count, nil
}

func (s *WorkflowDao) Update(ctx context.Context, tenantID, accountID, id string, wf model.Workflow) error {
	wfBytes, err := json.Marshal(wf.Definition)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}

	tagBytes, err := json.Marshal(wf.Tags)
	if err != nil {
		log.Printf("failed to marshal tags: %v", err)
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in Update: %v", p)
			err = fmt.Errorf("failed to update workflow due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	query := `
		UPDATE workflows
		SET name = $1, definition = $2, tags = $3, status = $4, last_execution_status = $5, updated_by = $6
		WHERE id = $7 AND tenant_id = $8 AND account_id = $9
	`
	_, err = tx.ExecContext(ctx, query, wf.Name, wfBytes, tagBytes, wf.Status, wf.LastExecutionStatus, wf.UpdatedBy, id, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to update workflow: %w", err)
	}

	return nil
}

func (s *WorkflowDao) Delete(ctx context.Context, tenantID, accountID, id string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in Delete: %v", p)
			err = fmt.Errorf("failed to delete workflow due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	query := `
		DELETE FROM workflows
		WHERE id = $1 AND tenant_id = $2 AND account_id = $3
	`
	_, err = tx.ExecContext(ctx, query, id, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to delete workflow: %w", err)
	}

	return nil
}

func (s *WorkflowDao) UpdateWorkflowStatus(ctx context.Context, tenantID, accountID, id string, status model.WorkflowStatus) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in UpdateWorkflowStatus: %v", p)
			err = fmt.Errorf("failed to update workflow status due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	query := `
		UPDATE workflows
		SET status = $1
		WHERE id = $2 AND tenant_id = $3 AND account_id = $4
	`
	_, err = tx.ExecContext(ctx, query, status, id, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to update workflow status: %w", err)
	}

	return nil
}

func (s *WorkflowDao) SetLastExecutionStatus(ctx context.Context, tenantID, accountID, id string, status model.WorkflowExecutionStatus, executionTime time.Time, statusMessage string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in SetLastExecutionStatus: %v", p)
			err = fmt.Errorf("failed to update last execution status due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	nullStatusMessage := sql.NullString{
		String: statusMessage,
		Valid:  statusMessage != "",
	}

	query := `
		UPDATE workflows
		SET last_execution_status = $1, last_execution_time = $2, last_execution_status_message = $3
		WHERE id = $4 AND tenant_id = $5 AND account_id = $6
	`
	_, err = tx.ExecContext(ctx, query, status, executionTime, nullStatusMessage, id, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to update last execution status: %w", err)
	}

	return nil
}

func (s *WorkflowDao) FindByIntegrationName(ctx context.Context, tenantID, accountID, integrationName string) (*model.Workflow, error) {
	var id string
	var wfName string
	var wfBytes []byte
	var tagBytes []byte
	var status model.WorkflowStatus
	var lastExecutionStatus sql.NullString
	var lastExecutionTime sql.NullTime
	var createdBy, updatedBy string
	var createdAt, updatedAt time.Time
	var liveVersionID sql.NullString
	var liveVersionNumber sql.NullInt64
	var liveVersionName sql.NullString

	// Query to find a workflow that has a webhook trigger with the specific integration_name
	// Assuming definition is JSONB
	query := `
		SELECT w.id::text, w.name, w.definition, w.tags, w.status, w.last_execution_status, w.last_execution_time, w.created_by, w.updated_by, w.created_at, w.updated_at,
		       w.live_version_id::text, lv.version_number, lv.name
		FROM workflows w
		LEFT JOIN workflow_versions lv ON lv.id = w.live_version_id
		WHERE w.tenant_id = $1 AND w.account_id = $2
		AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(w.definition->'triggers') AS trigger
			WHERE trigger->>'type' = 'webhook'
			AND trigger->'params'->>'integration_name' = $3
		)
		LIMIT 1
	`
	err := s.db.QueryRowContext(ctx, query, tenantID, accountID, integrationName).Scan(
		&id, &wfName, &wfBytes, &tagBytes, &status,
		&lastExecutionStatus, &lastExecutionTime,
		&createdBy, &updatedBy, &createdAt, &updatedAt,
		&liveVersionID, &liveVersionNumber, &liveVersionName,
	)
	if err != nil {
		return nil, err
	}

	var wf model.Workflow
	if err := json.Unmarshal(wfBytes, &wf.Definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow definition: %w", err)
	}
	if err := json.Unmarshal(tagBytes, &wf.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow tags: %w", err)
	}
	wf.ID = id
	wf.Name = wfName
	wf.Status = status
	if lastExecutionStatus.Valid {
		wf.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecutionStatus.String)
	}
	if lastExecutionTime.Valid {
		wf.LastExecutionTime = &lastExecutionTime.Time
	}
	wf.AccountID = accountID
	wf.TenantID = tenantID
	wf.CreatedBy = createdBy
	wf.UpdatedBy = updatedBy
	wf.CreatedAt = createdAt
	wf.UpdatedAt = updatedAt
	applyLiveVersion(&wf, liveVersionID, liveVersionNumber, liveVersionName)

	return &wf, nil
}

// ListByIntegrationName returns every active workflow whose definition.triggers
// contains a webhook trigger with params.integration_name == integrationName,
// scoped to the tenant only (no account filter). One physical webhook URL can
// route to subscribers across multiple accounts within the same tenant, so the
// query intentionally drops the account_id predicate present on FindByIntegrationName.
//
// Status filter is enforced here so the caller doesn't fan out to paused / draft
// workflows. The JSONB EXISTS subquery mirrors FindByIntegrationName so existing
// integrations bound by either params.integration_name or the equivalent legacy
// path stay matchable without a schema migration.
func (s *WorkflowDao) ListByIntegrationName(ctx context.Context, tenantID, integrationName string) ([]model.Workflow, error) {
	// Match on either trigger.params.integration_name OR trigger.internal.name.
	//
	// normalizeWebhookTriggers (see service.go) stores the api-server integration
	// row's name in trigger.internal.name and the user-visible (un-prefixed)
	// name in trigger.params.integration_name. For "picker flow" workflows the
	// two are identical, but for legacy workflows internal.name carries the
	// `wf-<workflowID>-<name>` prefix while params.integration_name does not.
	//
	// The api-server forwarder always sends the integration row's name on the
	// URL path, so matching only on params.integration_name silently drops
	// every legacy subscriber (fan-out returns 200 with fired=0).
	query := `
		SELECT id::text, account_id::text, name, definition, tags, status, last_execution_status, last_execution_time, created_by, updated_by, created_at, updated_at
		FROM workflows
		WHERE tenant_id = $1
		  AND status = $2
		  AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(definition->'triggers') AS trigger
			WHERE trigger->>'type' = 'webhook'
			  AND (trigger->'params'->>'integration_name' = $3
			       OR trigger->'internal'->>'name' = $3)
		  )
	`
	rows, err := s.db.QueryContext(ctx, query, tenantID, model.WorkflowStatusActive, integrationName)
	if err != nil {
		return nil, fmt.Errorf("failed to query workflows by integration_name: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("workflow_dao: failed to close rows: %v", cerr)
		}
	}()

	var workflows []model.Workflow
	for rows.Next() {
		var id, accountID, wfName string
		var wfBytes, tagBytes []byte
		var status model.WorkflowStatus
		var lastExecutionStatus sql.NullString
		var lastExecutionTime sql.NullTime
		var createdBy, updatedBy string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&id, &accountID, &wfName, &wfBytes, &tagBytes, &status, &lastExecutionStatus, &lastExecutionTime, &createdBy, &updatedBy, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan workflow row: %w", err)
		}

		var wf model.Workflow
		if err := json.Unmarshal(wfBytes, &wf.Definition); err != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow definition (id=%s): %w", id, err)
		}
		if err := json.Unmarshal(tagBytes, &wf.Tags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow tags (id=%s): %w", id, err)
		}
		wf.ID = id
		wf.Name = wfName
		wf.Status = status
		if lastExecutionStatus.Valid {
			wf.LastExecutionStatus = model.WorkflowExecutionStatus(lastExecutionStatus.String)
		}
		if lastExecutionTime.Valid {
			wf.LastExecutionTime = &lastExecutionTime.Time
		}
		wf.AccountID = accountID
		wf.TenantID = tenantID
		wf.CreatedBy = createdBy
		wf.UpdatedBy = updatedBy
		wf.CreatedAt = createdAt
		wf.UpdatedAt = updatedAt

		workflows = append(workflows, wf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow row iteration error: %w", err)
	}
	return workflows, nil
}

// CountWorkflows counts workflows with optional filters for status and trigger type.
func (s *WorkflowDao) CountWorkflows(ctx context.Context, tenantID, accountID string, status model.WorkflowStatus, triggerType string) (int64, error) {
	// Build the query with dynamic filters
	query := `SELECT COUNT(*) FROM workflows WHERE tenant_id = $1 AND account_id = $2`
	args := []any{tenantID, accountID}
	argID := 3

	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", argID)
		args = append(args, status)
		argID++
	}

	if triggerType != "" {
		// Filter by trigger type using JSONB containment
		filter := map[string]any{
			"triggers": []map[string]any{
				{"type": triggerType},
			},
		}
		filterBytes, err := json.Marshal(filter)
		if err != nil {
			return 0, fmt.Errorf("failed to marshal trigger type filter: %w", err)
		}
		query += fmt.Sprintf(" AND definition @> $%d", argID)
		args = append(args, filterBytes)
	}

	var count int64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count workflows: %w", err)
	}

	return count, nil
}

// PublishVersion snapshots the workflow's current draft (workflows.definition)
// as a new immutable workflow_versions row, then prunes back to
// MaxWorkflowVersionsPerWorkflow rows (skipping whichever row is currently
// live).
//
// The workflows row is FOR-UPDATE locked inside the tx so two concurrent
// publishes get sequential version_number values.
//
// Named returns (v, err) are required so the deferred tx.Commit() error is
// propagated — assigning to a non-named err inside the defer would not change
// the returned value, making a commit failure silent.
func (s *WorkflowDao) PublishVersion(ctx context.Context, workflowID, createdBy string, source model.WorkflowVersionSource, name, description *string, restoredFromVersion *int) (v *model.WorkflowVersion, err error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in PublishVersion: %v", p)
			err = fmt.Errorf("failed to publish workflow version due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	// Lock the workflow row and read current draft definition.
	var defBytes []byte
	if err = tx.QueryRowContext(ctx, `SELECT definition FROM workflows WHERE id = $1 FOR UPDATE`, workflowID).Scan(&defBytes); err != nil {
		return nil, fmt.Errorf("failed to lock workflow row: %w", err)
	}

	var next int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_number), 0) + 1 FROM workflow_versions WHERE workflow_id = $1`, workflowID).Scan(&next); err != nil {
		return nil, fmt.Errorf("failed to compute next version_number: %w", err)
	}

	var restoredFrom sql.NullInt64
	if restoredFromVersion != nil {
		restoredFrom = sql.NullInt64{Int64: int64(*restoredFromVersion), Valid: true}
	}
	var createdByVal sql.NullString
	if createdBy != "" {
		createdByVal = sql.NullString{String: createdBy, Valid: true}
	}
	var nameVal, descVal sql.NullString
	if name != nil && *name != "" {
		nameVal = sql.NullString{String: *name, Valid: true}
	}
	if description != nil && *description != "" {
		descVal = sql.NullString{String: *description, Valid: true}
	}

	var versionID string
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO workflow_versions (workflow_id, version_number, definition, source, restored_from_version, created_by, name, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, created_at
	`, workflowID, next, defBytes, string(source), restoredFrom, createdByVal, nameVal, descVal).Scan(&versionID, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert workflow version: %w", err)
	}

	if err = pruneVersionsTx(ctx, tx, workflowID, model.MaxWorkflowVersionsPerWorkflow); err != nil {
		return nil, err
	}

	v = &model.WorkflowVersion{
		ID:                  versionID,
		WorkflowID:          workflowID,
		VersionNumber:       next,
		Source:              source,
		RestoredFromVersion: restoredFromVersion,
		CreatedBy:           createdBy,
		CreatedAt:           createdAt,
		Name:                strPtrIfNonEmpty(name),
		Description:         strPtrIfNonEmpty(description),
	}
	if err = json.Unmarshal(defBytes, &v.Definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal version definition: %w", err)
	}
	return v, nil
}

// SetLiveVersion is a pointer flip. It updates workflows.live_version_id and
// MUST NOT touch workflows.definition (the draft) or workflows.status. This
// is the invariant that protects unpublished edits during a rollback.
//
// The version must already exist for this workflow. Returns sql.ErrNoRows if
// the version isn't found.
func (s *WorkflowDao) SetLiveVersion(ctx context.Context, tenantID, accountID, workflowID, versionID string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM workflow_versions WHERE id = $1 AND workflow_id = $2)
	`, versionID, workflowID).Scan(&exists); err != nil {
		return fmt.Errorf("failed to check version existence: %w", err)
	}
	if !exists {
		return sql.ErrNoRows
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE workflows
		SET live_version_id = $1
		WHERE id = $2 AND tenant_id = $3 AND account_id = $4
	`, versionID, workflowID, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to set live version: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateVersionMetadata patches the mutable metadata fields (name, description)
// on a workflow_versions row. Both fields are independently optional — a nil
// pointer leaves the column untouched; a non-nil pointer overwrites (use "" to
// clear). version_number, definition, source, created_by are immutable here.
func (s *WorkflowDao) UpdateVersionMetadata(ctx context.Context, workflowID string, versionNumber int, name, description *string) (*model.WorkflowVersion, error) {
	sets := []string{}
	args := []any{}
	argID := 1
	if name != nil {
		var v sql.NullString
		if *name != "" {
			v = sql.NullString{String: *name, Valid: true}
		}
		sets = append(sets, fmt.Sprintf("name = $%d", argID))
		args = append(args, v)
		argID++
	}
	if description != nil {
		var v sql.NullString
		if *description != "" {
			v = sql.NullString{String: *description, Valid: true}
		}
		sets = append(sets, fmt.Sprintf("description = $%d", argID))
		args = append(args, v)
		argID++
	}
	if len(sets) == 0 {
		return s.GetWorkflowVersion(ctx, workflowID, versionNumber)
	}
	args = append(args, workflowID, versionNumber)
	query := fmt.Sprintf(
		"UPDATE workflow_versions SET %s WHERE workflow_id = $%d AND version_number = $%d",
		strings.Join(sets, ", "), argID, argID+1,
	)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update version metadata: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetWorkflowVersion(ctx, workflowID, versionNumber)
}

// pruneVersionsTx keeps only the most recent `keep` versions per workflow,
// always preserving whichever row is currently live (workflows.live_version_id).
// The boolean sort key wins for the live row regardless of its version_number,
// so an older live version still survives when N newer non-live versions exist.
func pruneVersionsTx(ctx context.Context, tx *sqlx.Tx, workflowID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		DELETE FROM workflow_versions
		WHERE workflow_id = $1
		  AND id NOT IN (
		    SELECT id FROM workflow_versions
		    WHERE workflow_id = $1
		    ORDER BY (id = (SELECT live_version_id FROM workflows WHERE id = $1)) DESC,
		             version_number DESC
		    LIMIT $2
		  )
	`, workflowID, keep)
	if err != nil {
		return fmt.Errorf("failed to prune workflow versions: %w", err)
	}
	return nil
}

// ListWorkflowVersions returns up to `limit` most-recent versions of a workflow.
// Pass limit <= 0 for the full retained history.
func (s *WorkflowDao) ListWorkflowVersions(ctx context.Context, workflowID string, limit int) ([]model.WorkflowVersion, error) {
	query := `
		SELECT v.id::text, v.workflow_id::text, v.version_number, v.definition,
		       v.source, v.restored_from_version, v.name, v.description,
		       (v.id = w.live_version_id) AS is_live,
		       COALESCE(v.created_by::text, '') AS created_by,
		       u.id::text AS created_by_user_id, u.display_name AS created_by_display_name,
		       v.created_at
		FROM workflow_versions v
		JOIN workflows w ON v.workflow_id = w.id
		LEFT JOIN users u ON v.created_by = u.id
		WHERE v.workflow_id = $1
		ORDER BY v.version_number DESC
	`
	args := []any{workflowID}
	if limit > 0 {
		query += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query workflow versions: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("failed to close rows: %v", err)
		}
	}()

	var versions []model.WorkflowVersion
	for rows.Next() {
		v, err := scanWorkflowVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating workflow versions: %w", err)
	}
	return versions, nil
}

// GetWorkflowVersion fetches a single workflow_versions row by workflow_id + version_number.
func (s *WorkflowDao) GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*model.WorkflowVersion, error) {
	query := `
		SELECT v.id::text, v.workflow_id::text, v.version_number, v.definition,
		       v.source, v.restored_from_version, v.name, v.description,
		       (v.id = w.live_version_id) AS is_live,
		       COALESCE(v.created_by::text, '') AS created_by,
		       u.id::text AS created_by_user_id, u.display_name AS created_by_display_name,
		       v.created_at
		FROM workflow_versions v
		JOIN workflows w ON v.workflow_id = w.id
		LEFT JOIN users u ON v.created_by = u.id
		WHERE v.workflow_id = $1 AND v.version_number = $2
	`
	row := s.db.QueryRowxContext(ctx, query, workflowID, versionNumber)
	return scanWorkflowVersion(row)
}

// GetWorkflowVersionByID fetches a single workflow_versions row by its UUID.
// Used by GetDetailedWorkflowExecution to load the snapshot a run executed
// against, given the version_id stamped in the Temporal Memo.
func (s *WorkflowDao) GetWorkflowVersionByID(ctx context.Context, versionID string) (*model.WorkflowVersion, error) {
	query := `
		SELECT v.id::text, v.workflow_id::text, v.version_number, v.definition,
		       v.source, v.restored_from_version, v.name, v.description,
		       (v.id = w.live_version_id) AS is_live,
		       COALESCE(v.created_by::text, '') AS created_by,
		       u.id::text AS created_by_user_id, u.display_name AS created_by_display_name,
		       v.created_at
		FROM workflow_versions v
		JOIN workflows w ON v.workflow_id = w.id
		LEFT JOIN users u ON v.created_by = u.id
		WHERE v.id = $1
	`
	row := s.db.QueryRowxContext(ctx, query, versionID)
	return scanWorkflowVersion(row)
}

// GetLiveWorkflowVersion returns the workflow_versions row currently pointed at
// by workflows.live_version_id. Returns sql.ErrNoRows when no live version is
// set (which should be transient — CreateWorkflow always auto-publishes v1 and
// marks it live).
func (s *WorkflowDao) GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*model.WorkflowVersion, error) {
	query := `
		SELECT v.id::text, v.workflow_id::text, v.version_number, v.definition,
		       v.source, v.restored_from_version, v.name, v.description,
		       TRUE AS is_live,
		       COALESCE(v.created_by::text, '') AS created_by,
		       u.id::text AS created_by_user_id, u.display_name AS created_by_display_name,
		       v.created_at
		FROM workflows w
		JOIN workflow_versions v ON v.id = w.live_version_id
		LEFT JOIN users u ON v.created_by = u.id
		WHERE w.id = $1
	`
	row := s.db.QueryRowxContext(ctx, query, workflowID)
	return scanWorkflowVersion(row)
}

func scanWorkflowVersion(r rowScanner) (*model.WorkflowVersion, error) {
	var (
		id, workflowID                        string
		versionNumber                         int
		defBytes                              []byte
		source                                string
		restoredFrom                          sql.NullInt64
		name, description                     sql.NullString
		isLive                                bool
		createdBy                             string
		createdByUserID, createdByDisplayName sql.NullString
		createdAt                             time.Time
	)
	if err := r.Scan(&id, &workflowID, &versionNumber, &defBytes, &source, &restoredFrom, &name, &description, &isLive, &createdBy, &createdByUserID, &createdByDisplayName, &createdAt); err != nil {
		return nil, err
	}
	v := model.WorkflowVersion{
		ID:            id,
		WorkflowID:    workflowID,
		VersionNumber: versionNumber,
		Source:        model.WorkflowVersionSource(source),
		IsLive:        isLive,
		CreatedBy:     createdBy,
		CreatedAt:     createdAt,
	}
	if err := json.Unmarshal(defBytes, &v.Definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow version definition: %w", err)
	}
	if restoredFrom.Valid {
		n := int(restoredFrom.Int64)
		v.RestoredFromVersion = &n
	}
	if name.Valid {
		s := name.String
		v.Name = &s
	}
	if description.Valid {
		s := description.String
		v.Description = &s
	}
	if createdByUserID.Valid {
		v.CreatedByUser = &model.WorkflowUser{
			ID:          createdByUserID.String,
			DisplayName: createdByDisplayName.String,
		}
	}
	return &v, nil
}

func strPtrIfNonEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	v := *s
	return &v
}

// RecommendationEvent represents a recommendation record for optimization trigger matching.
type RecommendationEvent struct {
	ID               string    `db:"id"`
	TenantID         string    `db:"tenant_id"`
	CloudAccountID   string    `db:"cloud_account_id"`
	ResourceID       string    `db:"resource_id"`
	Category         string    `db:"category"`
	RuleName         string    `db:"rule_name"`
	Severity         string    `db:"severity"`
	EstimatedSavings float64   `db:"estimated_savings"`
	Status           string    `db:"status"`
	Cluster          string    `db:"cluster"`
	CreatedAt        time.Time `db:"created_at"`
}

// FindNewRecommendations returns recommendations created since the given time.
// Uses created_at (immutable) to avoid re-triggering on edits.
// Returns up to pageSize rows ordered by (created_at, id) for stable cursor-based pagination.
func (s *WorkflowDao) FindNewRecommendations(ctx context.Context, since time.Time) ([]RecommendationEvent, error) {
	const pageSize = 500
	var allResults []RecommendationEvent
	cursorTime := since
	cursorID := ""

	for {
		var page []RecommendationEvent
		var err error

		if cursorID == "" {
			query := `
				SELECT
					r.id::text, r.tenant_id::text, r.cloud_account_id::text, COALESCE(r.resource_id::text, '') as resource_id,
					COALESCE(r.category, '') as category, COALESCE(r.rule_name, '') as rule_name, COALESCE(r.severity, '') as severity,
					COALESCE(r.estimated_savings, 0) as estimated_savings,
					COALESCE(r.status, '') as status,
					COALESCE(r.recommendation->>'cluster_name', '') as cluster,
					r.created_at
				FROM recommendation r
				WHERE r.created_at >= $1
				  AND r.status = 'Open'
				ORDER BY r.created_at ASC, r.id ASC
				LIMIT $2
			`
			err = s.db.SelectContext(ctx, &page, query, cursorTime, pageSize)
		} else {
			query := `
				SELECT
					r.id::text, r.tenant_id::text, r.cloud_account_id::text, COALESCE(r.resource_id::text, '') as resource_id,
					COALESCE(r.category, '') as category, COALESCE(r.rule_name, '') as rule_name, COALESCE(r.severity, '') as severity,
					COALESCE(r.estimated_savings, 0) as estimated_savings,
					COALESCE(r.status, '') as status,
					COALESCE(r.recommendation->>'cluster_name', '') as cluster,
					r.created_at
				FROM recommendation r
				WHERE r.created_at >= $1
				  AND r.status = 'Open'
				  AND (r.created_at, r.id) > ($1, $3::uuid)
				ORDER BY r.created_at ASC, r.id ASC
				LIMIT $2
			`
			err = s.db.SelectContext(ctx, &page, query, cursorTime, pageSize, cursorID)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to query new recommendations: %w", err)
		}

		allResults = append(allResults, page...)

		if len(page) < pageSize {
			break
		}

		// Advance cursor to last row for next page
		last := page[len(page)-1]
		cursorTime = last.CreatedAt
		cursorID = last.ID
	}

	return allResults, nil
}
