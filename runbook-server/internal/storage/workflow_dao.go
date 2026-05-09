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

func (s *WorkflowDao) Save(ctx context.Context, tenantID, accountID string, wf model.Workflow) (string, error) {
	id := wf.ID
	if id == "" {
		id = uuid.New().String()
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("panic in Save: %v", p)
			err = fmt.Errorf("failed to save workflow due to an unexpected error")
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()
	wfBytes, err := json.Marshal(wf.Definition)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow: %w", err)
	}

	tagBytes, err := json.Marshal(wf.Tags)
	if err != nil {
		log.Printf("failed to marshal tags: %v", err)
	}

	query := `
		INSERT INTO workflows (id, tenant_id, account_id, name, definition, tags, status, created_by, updated_by, created_from_session_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	var createdFromSessionID sql.NullString
	if wf.CreatedFromSessionID != nil && *wf.CreatedFromSessionID != "" {
		createdFromSessionID = sql.NullString{String: *wf.CreatedFromSessionID, Valid: true}
	}
	_, err = tx.ExecContext(ctx, query, id, tenantID, accountID, wf.Name, wfBytes, tagBytes, wf.Status, wf.CreatedBy, wf.UpdatedBy, createdFromSessionID)
	if err != nil {
		return "", fmt.Errorf("failed to save workflow: %w", err)
	}

	return id, nil
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
			uu.id::text as updated_by_user_id, uu.display_name as updated_by_display_name
		FROM workflows w
		LEFT JOIN users cu ON w.created_by = cu.id
		LEFT JOIN users uu ON w.updated_by = uu.id ` + whereClauseWithAlias + ` order by w.created_at DESC`

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

		if err := rows.Scan(&wfID, &wfName, &wfBytes, &tagBytes, &status, &lastExecutionStatus, &lastExecutionStatusMessage, &lastExecutionTime, &createdBy, &updatedBy, &createdAt, &updatedAt, &createdFromSessionID,
			&createdByUserID, &createdByDisplayName, &updatedByUserID, &updatedByDisplayName); err != nil {
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

	query := `
		SELECT id::text, name, definition, tags, status, last_execution_status, last_execution_status_message, last_execution_time, created_by, updated_by, created_at, updated_at, created_from_session_id FROM workflows
		WHERE tenant_id = $1 AND account_id = $2 AND id = $3
	`
	err := s.db.QueryRowContext(ctx, query, tenantID, accountID, id).Scan(&wfID, &wfName, &wfBytes, &tagBytes, &status, &lastExecutionStatus, &lastExecutionStatusMessage, &lastExecutionTime, &createdBy, &updatedBy, &createdAt, &updatedAt, &createdFromSessionID)
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

	return &wf, nil
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

	query := `
		SELECT id::text, name, definition, tags, status, last_execution_status, last_execution_status_message, last_execution_time, created_by, updated_by, created_at, updated_at, created_from_session_id FROM workflows
		WHERE tenant_id = $1 AND account_id = $2 AND name = $3
	`
	err := s.db.QueryRowContext(ctx, query, tenantID, accountID, name).Scan(&id, &wfName, &wfBytes, &tagBytes, &status, &lastExecutionStatus, &lastExecutionStatusMessage, &lastExecutionTime, &createdBy, &updatedBy, &createdAt, &updatedAt, &createdFromSessionID)
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
			(trigger->'params')::text as filter,
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
		// For optimization triggers, build Jinja filter from structured params
		if rule.TriggerType == model.WorkflowTriggerOptimization {
			rule.Filter = buildOptimizationFilter(rule.Filter)
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

	// Query to find a workflow that has a webhook trigger with the specific integration_name
	// Assuming definition is JSONB
	query := `
		SELECT id::text, name, definition, tags, status, last_execution_status, last_execution_time, created_by, updated_by, created_at, updated_at
		FROM workflows
		WHERE tenant_id = $1 AND account_id = $2
		AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(definition->'triggers') AS trigger
			WHERE trigger->>'type' = 'webhook'
			AND trigger->'params'->>'integration_name' = $3
		)
		LIMIT 1
	`
	err := s.db.QueryRowContext(ctx, query, tenantID, accountID, integrationName).Scan(&id, &wfName, &wfBytes, &tagBytes, &status, &lastExecutionStatus, &lastExecutionTime, &createdBy, &updatedBy, &createdAt, &updatedAt)
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

	return &wf, nil
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
