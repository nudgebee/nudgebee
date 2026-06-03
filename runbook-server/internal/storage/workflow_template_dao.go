package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type WorkflowTemplateDao struct {
	db *sqlx.DB
}

func NewWorkflowTemplateDao() (*WorkflowTemplateDao, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	return &WorkflowTemplateDao{db: db.Db}, nil
}

func (s *WorkflowTemplateDao) ListGlobal(ctx context.Context, request model.ListWorkflowTemplateRequest) ([]model.WorkflowTemplate, int, error) {
	whereClauses := []string{"t.tenant_id IS NULL", "t.account_id IS NULL", "t.status = 'ACTIVE'"}
	var args []any
	argID := 1

	if request.Category != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("t.category = $%d", argID))
		args = append(args, request.Category)
		argID++
	}

	if request.Name != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("t.name ILIKE $%d", argID))
		args = append(args, "%"+request.Name+"%")
		argID++
	}

	// Tag-based filtering: match templates by event sources, alert names, and/or subject types.
	// Match if ANY of:
	//   1. alert_name matches (precise hit)
	//   2. subject_type matches AND source matches
	//   3. source matches AND template has empty alert_names AND empty subject_types (universal)
	hasSources := len(request.EventSources) > 0
	hasAlerts := len(request.AlertNames) > 0
	hasSubjects := len(request.SubjectTypes) > 0

	if hasSources || hasAlerts || hasSubjects {
		var orClauses []string

		// Clause 1: alert_name matches
		if hasAlerts {
			orClauses = append(orClauses, fmt.Sprintf("(t.tags->'alert_names') ?| $%d", argID))
			args = append(args, pq.Array(request.AlertNames))
			argID++
		}

		// Clause 2: subject_type matches AND source matches
		if hasSubjects && hasSources {
			orClauses = append(orClauses, fmt.Sprintf(
				"((t.tags->'subject_types') ?| $%d AND (t.tags->'event_sources') ?| $%d)",
				argID, argID+1,
			))
			args = append(args, pq.Array(request.SubjectTypes), pq.Array(request.EventSources))
			argID += 2
		} else if hasSubjects {
			orClauses = append(orClauses, fmt.Sprintf("(t.tags->'subject_types') ?| $%d", argID))
			args = append(args, pq.Array(request.SubjectTypes))
			argID++
		}

		// Clause 3: source matches AND template has no alert_names AND no subject_types (universal)
		if hasSources {
			orClauses = append(orClauses, fmt.Sprintf(
				"((t.tags->'event_sources') ?| $%d AND COALESCE(t.tags->'alert_names', '[]'::jsonb) = '[]'::jsonb AND COALESCE(t.tags->'subject_types', '[]'::jsonb) = '[]'::jsonb)",
				argID,
			))
			args = append(args, pq.Array(request.EventSources))
			argID++
		}

		whereClauses = append(whereClauses, "("+strings.Join(orClauses, " OR ")+")")
	}

	whereClause := "WHERE " + strings.Join(whereClauses, " AND ")

	countQuery := "SELECT COUNT(*) FROM workflow_templates t " + whereClause

	var totalCount int
	err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get global template count: %w", err)
	}

	mainQuery := `
		SELECT t.id::text, t.name, t.description, t.category, t.icon, t.definition, t.template_variables, t.tags, t.is_system, t.status,
			t.created_by, t.updated_by, t.created_at, t.updated_at,
			cu.id::text as created_by_user_id, cu.display_name as created_by_display_name,
			uu.id::text as updated_by_user_id, uu.display_name as updated_by_display_name
		FROM workflow_templates t
		LEFT JOIN users cu ON t.created_by = cu.id
		LEFT JOIN users uu ON t.updated_by = uu.id ` + whereClause + ` ORDER BY t.created_at DESC`

	mainArgs := make([]any, len(args))
	copy(mainArgs, args)
	mainArgID := argID

	if request.Limit > 0 {
		mainQuery += fmt.Sprintf(" LIMIT $%d", mainArgID)
		mainArgs = append(mainArgs, request.Limit)
		mainArgID++
	}

	if request.NextPageToken != "" {
		offset, err := strconv.Atoi(request.NextPageToken)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid next_page_token: %w", err)
		}
		mainQuery += fmt.Sprintf(" OFFSET $%d", mainArgID)
		mainArgs = append(mainArgs, offset)
	}

	rows, err := s.db.QueryContext(ctx, mainQuery, mainArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to retrieve global workflow templates: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("failed to close rows: %v", err)
		}
	}()

	var templates []model.WorkflowTemplate
	for rows.Next() {
		var tmpl model.WorkflowTemplate
		var description, category, icon sql.NullString
		var defBytes, tvBytes, tagBytes []byte
		var createdBy, updatedBy sql.NullString
		var createdAt, updatedAt time.Time
		var createdByUserID, createdByDisplayName sql.NullString
		var updatedByUserID, updatedByDisplayName sql.NullString

		if err := rows.Scan(
			&tmpl.ID, &tmpl.Name, &description, &category, &icon,
			&defBytes, &tvBytes, &tagBytes, &tmpl.IsSystem, &tmpl.Status,
			&createdBy, &updatedBy, &createdAt, &updatedAt,
			&createdByUserID, &createdByDisplayName,
			&updatedByUserID, &updatedByDisplayName,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan global workflow template: %w", err)
		}

		if description.Valid {
			tmpl.Description = description.String
		}
		if category.Valid {
			tmpl.Category = category.String
		}
		if icon.Valid {
			tmpl.Icon = icon.String
		}
		if createdBy.Valid {
			tmpl.CreatedBy = createdBy.String
		}
		if updatedBy.Valid {
			tmpl.UpdatedBy = updatedBy.String
		}

		if err := json.Unmarshal(defBytes, &tmpl.Definition); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal template definition: %w", err)
		}
		if err := json.Unmarshal(tvBytes, &tmpl.TemplateVariables); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal template variables: %w", err)
		}
		if err := json.Unmarshal(tagBytes, &tmpl.Tags); err != nil {
			return nil, 0, fmt.Errorf("failed to unmarshal template tags: %w", err)
		}

		tmpl.CreatedAt = createdAt
		tmpl.UpdatedAt = updatedAt

		if createdByUserID.Valid {
			tmpl.CreatedByUser = &model.WorkflowUser{
				ID:          createdByUserID.String,
				DisplayName: createdByDisplayName.String,
			}
		}
		if updatedByUserID.Valid {
			tmpl.UpdatedByUser = &model.WorkflowUser{
				ID:          updatedByUserID.String,
				DisplayName: updatedByDisplayName.String,
			}
		}

		templates = append(templates, tmpl)
	}

	return templates, totalCount, nil
}

func (s *WorkflowTemplateDao) FindGlobal(ctx context.Context, id string) (*model.WorkflowTemplate, error) {
	var tmpl model.WorkflowTemplate
	var description, category, icon sql.NullString
	var defBytes, tvBytes, tagBytes []byte
	var createdBy, updatedBy sql.NullString
	var createdAt, updatedAt time.Time

	query := `
		SELECT id::text, name, description, category, icon, definition, template_variables, tags, is_system, status,
			created_by, updated_by, created_at, updated_at
		FROM workflow_templates
		WHERE id = $1 AND tenant_id IS NULL AND account_id IS NULL AND status = 'ACTIVE'
	`
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&tmpl.ID, &tmpl.Name, &description, &category, &icon,
		&defBytes, &tvBytes, &tagBytes, &tmpl.IsSystem, &tmpl.Status,
		&createdBy, &updatedBy, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve global workflow template: %w", err)
	}

	if description.Valid {
		tmpl.Description = description.String
	}
	if category.Valid {
		tmpl.Category = category.String
	}
	if icon.Valid {
		tmpl.Icon = icon.String
	}
	if createdBy.Valid {
		tmpl.CreatedBy = createdBy.String
	}
	if updatedBy.Valid {
		tmpl.UpdatedBy = updatedBy.String
	}

	if err := json.Unmarshal(defBytes, &tmpl.Definition); err != nil {
		return nil, fmt.Errorf("failed to unmarshal template definition: %w", err)
	}
	if err := json.Unmarshal(tvBytes, &tmpl.TemplateVariables); err != nil {
		return nil, fmt.Errorf("failed to unmarshal template variables: %w", err)
	}
	if err := json.Unmarshal(tagBytes, &tmpl.Tags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal template tags: %w", err)
	}

	tmpl.CreatedAt = createdAt
	tmpl.UpdatedAt = updatedAt

	return &tmpl, nil
}
