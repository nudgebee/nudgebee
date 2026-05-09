package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type OptimizerDao struct {
	db *sqlx.DB
}

func NewOptimizerDao() (*OptimizerDao, error) {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	return &OptimizerDao{db: db.Db}, nil
}

func (d *OptimizerDao) Db() *sqlx.DB {
	return d.db
}

// AutoOptimize operations

func (d *OptimizerDao) SaveAutoOptimize(ctx context.Context, ao model.AutoOptimize) error {
	ruleBytes, err := json.Marshal(ao.Rule)
	if err != nil {
		return fmt.Errorf("failed to marshal rule: %w", err)
	}
	notificationBytes, err := json.Marshal(ao.Notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}
	attributesBytes, err := json.Marshal(ao.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	query := `
		INSERT INTO auto_pilot (
			id, name, account_id, source, rule, creation_date, update_date, created_by, update_by,
			schedule_time, last_schedule_time, last_executed_time, status, execution_status,
			tenant_id, category, start_at, end_at, notification, next_schedule_time, attributes
		) VALUES (
			:id, :name, :account_id, :source, :rule, :creation_date, :update_date, :created_by, :update_by,
			:schedule_time, :last_schedule_time, :last_executed_time, :status, :execution_status,
			:tenant_id, :category, :start_at, :end_at, :notification, :next_schedule_time, :attributes
		) ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			execution_status = EXCLUDED.execution_status,
			last_schedule_time = EXCLUDED.last_schedule_time,
			last_executed_time = EXCLUDED.last_executed_time,
			next_schedule_time = EXCLUDED.next_schedule_time,
			attributes = EXCLUDED.attributes,
			end_at = EXCLUDED.end_at,
			rule = EXCLUDED.rule,
			schedule_time = EXCLUDED.schedule_time,
			notification = EXCLUDED.notification,
			name = EXCLUDED.name,
			update_date = EXCLUDED.update_date,
			update_by = EXCLUDED.update_by
	`

	params := map[string]interface{}{
		"id":                 ao.ID,
		"name":               ao.Name,
		"account_id":         ao.AccountID,
		"source":             ao.Source,
		"rule":               string(ruleBytes), // CAST TO STRING
		"creation_date":      ao.CreationDate,
		"update_date":        ao.UpdateDate,
		"created_by":         ao.CreatedBy,
		"update_by":          ao.UpdatedBy,
		"schedule_time":      ao.ScheduleTime,
		"last_schedule_time": ao.LastScheduleTime,
		"last_executed_time": ao.LastExecutedTime,
		"status":             ao.Status,
		"execution_status":   ao.ExecutionStatus,
		"tenant_id":          ao.TenantID,
		"category":           ao.Category,
		"start_at":           ao.StartAt,
		"end_at":             ao.EndAt,
		"notification":       string(notificationBytes), // CAST TO STRING
		"next_schedule_time": ao.NextScheduleTime,
		"attributes":         string(attributesBytes), // CAST TO STRING
	}

	_, err = d.db.NamedExecContext(ctx, query, params)
	if err != nil {
		return fmt.Errorf("failed to save auto_optimize: %w", err)
	}
	return nil
}

func (d *OptimizerDao) GetAutoOptimize(ctx context.Context, id uuid.UUID) (*model.AutoOptimize, error) {
	query := `SELECT * FROM auto_pilot WHERE id = $1`
	var ao model.AutoOptimize

	// Create a helper struct to scan into
	var dbModel struct {
		ID               uuid.UUID  `db:"id"`
		Name             *string    `db:"name"`
		AccountID        uuid.UUID  `db:"account_id"`
		Source           *string    `db:"source"`
		Rule             []byte     `db:"rule"`
		CreationDate     time.Time  `db:"creation_date"`
		UpdateDate       time.Time  `db:"update_date"`
		CreatedBy        uuid.UUID  `db:"created_by"`
		UpdatedBy        *uuid.UUID `db:"update_by"`
		ScheduleTime     string     `db:"schedule_time"`
		LastScheduleTime *time.Time `db:"last_schedule_time"`
		LastExecutedTime *time.Time `db:"last_executed_time"`
		Status           string     `db:"status"`
		ExecutionStatus  string     `db:"execution_status"`
		TenantID         uuid.UUID  `db:"tenant_id"`
		Category         string     `db:"category"`
		StartAt          time.Time  `db:"start_at"`
		EndAt            *time.Time `db:"end_at"`
		Notification     []byte     `db:"notification"`
		NextScheduleTime *time.Time `db:"next_schedule_time"`
		Attributes       []byte     `db:"attributes"`
	}

	err := d.db.GetContext(ctx, &dbModel, query, id)
	if err != nil {
		return nil, err
	}

	ao.ID = dbModel.ID
	ao.Name = dbModel.Name
	ao.AccountID = dbModel.AccountID
	ao.Source = dbModel.Source
	ao.CreationDate = dbModel.CreationDate
	ao.UpdateDate = dbModel.UpdateDate
	ao.CreatedBy = dbModel.CreatedBy
	ao.UpdatedBy = dbModel.UpdatedBy
	ao.ScheduleTime = dbModel.ScheduleTime
	ao.LastScheduleTime = dbModel.LastScheduleTime
	ao.LastExecutedTime = dbModel.LastExecutedTime
	ao.Status = model.AutoOptimizeStatus(dbModel.Status)
	ao.ExecutionStatus = dbModel.ExecutionStatus
	ao.TenantID = dbModel.TenantID
	ao.Category = dbModel.Category
	ao.StartAt = dbModel.StartAt
	ao.EndAt = dbModel.EndAt
	ao.NextScheduleTime = dbModel.NextScheduleTime

	if len(dbModel.Rule) > 0 {
		if err := json.Unmarshal(dbModel.Rule, &ao.Rule); err != nil {
			return nil, fmt.Errorf("failed to unmarshal rule: %w", err)
		}
	}
	if len(dbModel.Notification) > 0 {
		if err := json.Unmarshal(dbModel.Notification, &ao.Notification); err != nil {
			return nil, fmt.Errorf("failed to unmarshal notification: %w", err)
		}
	}
	if len(dbModel.Attributes) > 0 {
		if err := json.Unmarshal(dbModel.Attributes, &ao.Attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}
	}

	filters, err := d.GetFiltersForAutoOptimize(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch filters for auto optimize %s: %w", id, err)
	}
	ao.ResourceFilters = filters

	return &ao, nil
}

func (d *OptimizerDao) GetActiveAutoOptimizes(ctx context.Context) ([]model.AutoOptimize, error) {
	query := `SELECT * FROM auto_pilot WHERE status IN ('Active', 'Dryrun') AND execution_status = 'Idle'`

	var dbModels []struct {
		ID               uuid.UUID  `db:"id"`
		Name             *string    `db:"name"`
		AccountID        uuid.UUID  `db:"account_id"`
		Source           *string    `db:"source"`
		Rule             []byte     `db:"rule"`
		CreationDate     time.Time  `db:"creation_date"`
		UpdateDate       time.Time  `db:"update_date"`
		CreatedBy        uuid.UUID  `db:"created_by"`
		UpdatedBy        *uuid.UUID `db:"update_by"`
		ScheduleTime     string     `db:"schedule_time"`
		LastScheduleTime *time.Time `db:"last_schedule_time"`
		LastExecutedTime *time.Time `db:"last_executed_time"`
		Status           string     `db:"status"`
		ExecutionStatus  string     `db:"execution_status"`
		TenantID         uuid.UUID  `db:"tenant_id"`
		Category         string     `db:"category"`
		StartAt          time.Time  `db:"start_at"`
		EndAt            *time.Time `db:"end_at"`
		Notification     []byte     `db:"notification"`
		NextScheduleTime *time.Time `db:"next_schedule_time"`
		Attributes       []byte     `db:"attributes"`
	}

	if err := d.db.SelectContext(ctx, &dbModels, query); err != nil {
		return nil, err
	}

	var aos []model.AutoOptimize
	var ids []uuid.UUID
	for _, dbModel := range dbModels {
		ao := model.AutoOptimize{
			ID:               dbModel.ID,
			Name:             dbModel.Name,
			AccountID:        dbModel.AccountID,
			Source:           dbModel.Source,
			CreationDate:     dbModel.CreationDate,
			UpdateDate:       dbModel.UpdateDate,
			CreatedBy:        dbModel.CreatedBy,
			UpdatedBy:        dbModel.UpdatedBy,
			ScheduleTime:     dbModel.ScheduleTime,
			LastScheduleTime: dbModel.LastScheduleTime,
			LastExecutedTime: dbModel.LastExecutedTime,
			Status:           model.AutoOptimizeStatus(dbModel.Status),
			ExecutionStatus:  dbModel.ExecutionStatus,
			TenantID:         dbModel.TenantID,
			Category:         dbModel.Category,
			StartAt:          dbModel.StartAt,
			EndAt:            dbModel.EndAt,
			NextScheduleTime: dbModel.NextScheduleTime,
		}

		if len(dbModel.Rule) > 0 {
			if err := json.Unmarshal(dbModel.Rule, &ao.Rule); err != nil {
				slog.Error("Failed to unmarshal rule for auto pilot", "id", ao.ID, "error", err)
			}
		}
		if len(dbModel.Notification) > 0 {
			if err := json.Unmarshal(dbModel.Notification, &ao.Notification); err != nil {
				slog.Error("Failed to unmarshal notification for auto pilot", "id", ao.ID, "error", err)
			}
		}
		if len(dbModel.Attributes) > 0 {
			if err := json.Unmarshal(dbModel.Attributes, &ao.Attributes); err != nil {
				slog.Error("Failed to unmarshal attributes for auto pilot", "id", ao.ID, "error", err)
			}
		}

		aos = append(aos, ao)
		ids = append(ids, ao.ID)
	}

	filtersMap, err := d.GetFiltersForAutoOptimizes(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch filters for active auto optimizes: %w", err)
	}

	for i := range aos {
		if filters, ok := filtersMap[aos[i].ID]; ok {
			aos[i].ResourceFilters = filters
		}
	}

	return aos, nil
}

// Resource Map operations

func (d *OptimizerDao) DeleteScheduledTasks(ctx context.Context, autoOptimizeID uuid.UUID) error {
	query := `DELETE FROM auto_pilot_task WHERE auto_pilot_id = $1 AND status = 'Scheduled'`
	_, err := d.db.ExecContext(ctx, query, autoOptimizeID)
	return err
}

func (d *OptimizerDao) DeleteResourceFilters(ctx context.Context, autoOptimizeID uuid.UUID) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM auto_optimize_resource_map WHERE auto_optimize_id = $1", autoOptimizeID)
	return err
}

func (d *OptimizerDao) SaveResourceFilters(ctx context.Context, filters []model.AutoOptimizeResourceMap) error {
	if len(filters) == 0 {
		return nil
	}
	query := `
		INSERT INTO auto_optimize_resource_map (
			id, resource_identifier, auto_optimize_id, tenant_id, account_id, auto_optimize_type
		) VALUES (
			:id, :resource_identifier, :auto_optimize_id, :tenant_id, :account_id, :auto_optimize_type
		)
	`
	// We need to marshal resource_identifier to JSONB
	var dbFilters []map[string]interface{}
	for _, f := range filters {
		riBytes, err := json.Marshal(f.ResourceIdentifier)
		if err != nil {
			return err
		}
		dbFilters = append(dbFilters, map[string]interface{}{
			"id":                  f.ID,
			"resource_identifier": string(riBytes), // CAST TO STRING
			"auto_optimize_id":    f.AutoOptimizeID,
			"tenant_id":           f.TenantID,
			"account_id":          f.AccountID,
			"auto_optimize_type":  f.AutoOptimizeType,
		})
	}

	_, err := d.db.NamedExecContext(ctx, query, dbFilters)
	return err
}

func (d *OptimizerDao) GetFiltersForAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeResourceFilter, error) {
	query := `SELECT resource_identifier FROM auto_optimize_resource_map WHERE auto_optimize_id = $1`
	var filters []model.AutoOptimizeResourceFilter
	rows, err := d.db.QueryContext(ctx, query, autoOptimizeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var riBytes []byte
		if err := rows.Scan(&riBytes); err != nil {
			return nil, err
		}
		var ri model.AutoOptimizeResourceFilter
		if err := json.Unmarshal(riBytes, &ri); err != nil {
			continue
		}
		filters = append(filters, ri)
	}
	return filters, nil
}

func (d *OptimizerDao) GetFiltersForAutoOptimizes(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]model.AutoOptimizeResourceFilter, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := `SELECT auto_optimize_id, resource_identifier FROM auto_optimize_resource_map WHERE auto_optimize_id = ANY($1)`
	rows, err := d.db.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	res := make(map[uuid.UUID][]model.AutoOptimizeResourceFilter)
	for rows.Next() {
		var id uuid.UUID
		var riBytes []byte
		if err := rows.Scan(&id, &riBytes); err != nil {
			return nil, err
		}
		var ri model.AutoOptimizeResourceFilter
		if err := json.Unmarshal(riBytes, &ri); err != nil {
			continue
		}
		res[id] = append(res[id], ri)
	}
	return res, nil
}

func (d *OptimizerDao) GetResourceFilters(ctx context.Context, accountID, tenantID uuid.UUID, categories []string) ([]string, []string, error) {
	// Equivalent to get_auto_pilots_resource_filters in controller
	// returns like_filter, in_filter
	query := `SELECT resource_identifier FROM auto_optimize_resource_map WHERE account_id = $1 AND tenant_id = $2`
	args := []interface{}{accountID, tenantID}

	if len(categories) > 0 {
		query += " AND auto_optimize_type = ANY($3)"
		args = append(args, pq.Array(categories))
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var likeFilters []string
	var inFilters []string

	for rows.Next() {
		var riBytes []byte
		if err := rows.Scan(&riBytes); err != nil {
			return nil, nil, err
		}
		var ri model.AutoOptimizeResourceFilter
		if err := json.Unmarshal(riBytes, &ri); err != nil {
			continue
		}

		if ri.OnlyNamespace() {
			if ri.Namespace != nil {
				// Escape % and _ for LIKE pattern
				escaped := sqlEscapeLike(*ri.Namespace)
				likeFilters = append(likeFilters, fmt.Sprintf("%s/%%", escaped))
			}
		} else {
			inFilters = append(inFilters, ri.String())
		}
	}
	return likeFilters, inFilters, nil
}

func sqlEscapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "_", "\\_")
	s = strings.ReplaceAll(s, "%", "\\%")
	return s
}

func (d *OptimizerDao) GetAutoOptimizeIdsByFilter(ctx context.Context, accountID, tenantID uuid.UUID, status *string, resourceFilters []model.AutoOptimizeResourceFilter) ([]uuid.UUID, error) {
	// Equivalent to get_auto_optimizer_get
	query := `
		SELECT m.auto_optimize_id, m.resource_identifier
		FROM auto_optimize_resource_map m
		JOIN auto_pilot a ON m.auto_optimize_id = a.id
		WHERE m.account_id = $1 AND m.tenant_id = $2
	`
	args := []interface{}{accountID, tenantID}
	argIdx := 3

	if status != nil {
		query += fmt.Sprintf(" AND a.status = $%d", argIdx)
		args = append(args, *status)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []uuid.UUID

	for rows.Next() {
		var id uuid.UUID
		var riBytes []byte
		if err := rows.Scan(&id, &riBytes); err != nil {
			return nil, err
		}

		if len(resourceFilters) > 0 {
			var ri model.AutoOptimizeResourceFilter
			if err := json.Unmarshal(riBytes, &ri); err != nil {
				continue
			}

			match := false
			for _, trf := range resourceFilters {
				// Check if the DB rule (ri) covers the input query (trf)
				// Logic: Rule is a "Pattern". Query is a "Target".
				// 1. Namespace must always match (no global wildcard allowed usually, or handled same way)
				// 2. Name/Type in Rule can be nil/empty (Wildcard) to match any Query value.

				if matches(ri.Namespace, trf.Namespace) &&
					matches(ri.Type, trf.Type) &&
					matches(ri.Name, trf.Name) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// matches returns true if the scopes intersect (one is a subset of the other, or they are equal).
// If ruleVal is empty, it's a wildcard matching any queryVal.
// If queryVal is empty, it's a filter that accepts any ruleVal.
func matches(ruleVal, queryVal *string) bool {
	r := ""
	if ruleVal != nil {
		r = *ruleVal
	}
	q := ""
	if queryVal != nil {
		q = *queryVal
	}

	// If query has no constraint, it matches anything (User filtering down to a Namespace)
	if q == "" {
		return true
	}

	// If rule has no constraint, it matches any query (Wildcard rule covering a resource)
	if r == "" {
		return true
	}

	// Both have specific constraints, must match exactly
	return r == q
}

// Recommendations

func (d *OptimizerDao) GetRecommendations(ctx context.Context, accountID, tenantID uuid.UUID, ruleName *string, status []string, inFilter, likeFilter []string) ([]uuid.UUID, error) {
	// Equivalent to get_auto_optimizer_recommendation query

	// mainArgs starts with $1=accountID, $2=tenantID
	allArgs := []interface{}{accountID, tenantID}
	currIdx := 3
	mainQuery := `SELECT id FROM recommendation WHERE cloud_account_id = $1 AND tenant_id = $2`

	if ruleName != nil && *ruleName != "" {
		mainQuery += fmt.Sprintf(" AND rule_name = $%d", currIdx)
		allArgs = append(allArgs, *ruleName)
		currIdx++
	}

	if len(status) > 0 {
		mainQuery += fmt.Sprintf(" AND status = ANY($%d)", currIdx)
		allArgs = append(allArgs, pq.Array(status))
		currIdx++
	}

	// Now build the subquery for cloud_resourses
	// We need to pass account and tenant again for the subquery
	resQuery := fmt.Sprintf("SELECT id FROM cloud_resourses WHERE account = $%d AND tenant = $%d AND status = 'Active'", currIdx, currIdx+1)
	allArgs = append(allArgs, accountID, tenantID)
	currIdx += 2

	var shiftedConditions []string
	if len(inFilter) > 0 {
		shiftedConditions = append(shiftedConditions, fmt.Sprintf("resourse_id = ANY($%d)", currIdx))
		allArgs = append(allArgs, pq.Array(inFilter))
		currIdx++
	}
	if len(likeFilter) > 0 {
		shiftedConditions = append(shiftedConditions, fmt.Sprintf("resourse_id LIKE ANY($%d) ESCAPE '\\'", currIdx))
		allArgs = append(allArgs, pq.Array(likeFilter))
	}

	if len(shiftedConditions) > 0 {
		resQuery += " AND (" + joinOR(shiftedConditions) + ")"
	}

	finalQuery := mainQuery + " AND resource_id IN (" + resQuery + ")"

	rows, err := d.db.QueryContext(ctx, finalQuery, allArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var recIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		recIDs = append(recIDs, id)
	}
	return recIDs, nil
}

func (d *OptimizerDao) GetFullRecommendationsForOptimizerCategory(ctx context.Context, accountID uuid.UUID, category string) ([]model.RecommendationWithResource, error) {

	recommendationRuleName := category
	recommendationCategory := category
	switch category {
	case "vertical_rightsize":
		recommendationRuleName = "pod_right_sizing"
		recommendationCategory = "RightSizing"
	case "horizontal_rightsize":
		recommendationRuleName = "replica_right_sizing"
		recommendationCategory = "RightSizing"
	case "pvc_rightsize":
		recommendationRuleName = "pv_rightsize"
		recommendationCategory = "RightSizing"
	case "continuous_rightsize":
		recommendationRuleName = "continuous_rightsize"
		recommendationCategory = "RightSizing"
	}

	query := `
			SELECT r.*, cr.resourse_id as resource_identifier, cr.meta as resource_metadata
			FROM recommendation r
			LEFT JOIN cloud_resourses cr ON r.resource_id = cr.id
			WHERE r.cloud_account_id = $1 
			AND cr.status = 'Active'
			AND r.category = $2 
			AND r.rule_name = $3 
			AND r.status = 'Open' 
			AND r.is_dismissed = false
		`
	var dbRecs []struct {
		ID                   uuid.UUID  `db:"id"`
		CreatedAt            time.Time  `db:"created_at"`
		UpdatedAt            time.Time  `db:"updated_at"`
		TenantID             uuid.UUID  `db:"tenant_id"`
		CloudAccountID       uuid.UUID  `db:"cloud_account_id"`
		ResourceID           *uuid.UUID `db:"resource_id"`
		Recommendation       []byte     `db:"recommendation"`
		RecommendationAction *string    `db:"recommendation_action"`
		Severity             *string    `db:"severity"`
		RuleName             *string    `db:"rule_name"`
		EstimatedSavings     *float64   `db:"estimated_savings"`
		Status               string     `db:"status"`
		Category             *string    `db:"category"`
		IsDismissed          bool       `db:"is_dismissed"`
		DismissedReason      *string    `db:"dismissed_reason"`
		ResourceIdentifier   *string    `db:"resource_identifier"`
		ResourceMetadata     []byte     `db:"resource_metadata"`
		Note                 *string    `db:"note"`
		AccountObjectID      *string    `db:"account_object_id"`
		UpdatedBy            *uuid.UUID `db:"updated_by"`
	}

	if err := d.db.SelectContext(ctx, &dbRecs, query, accountID, recommendationCategory, recommendationRuleName); err != nil {
		return nil, err
	}

	var recs []model.RecommendationWithResource
	for _, r := range dbRecs {
		savings := 0.0
		if r.EstimatedSavings != nil {
			savings = *r.EstimatedSavings
		}
		recAction := ""
		if r.RecommendationAction != nil {
			recAction = *r.RecommendationAction
		}
		severity := ""
		if r.Severity != nil {
			severity = *r.Severity
		}
		ruleName := ""
		if r.RuleName != nil {
			ruleName = *r.RuleName
		}
		category := ""
		if r.Category != nil {
			category = *r.Category
		}

		rec := model.Recommendation{
			ID:                   r.ID,
			CreatedAt:            r.CreatedAt,
			UpdatedAt:            r.UpdatedAt,
			TenantID:             r.TenantID,
			CloudAccountID:       r.CloudAccountID,
			ResourceID:           r.ResourceID,
			RecommendationAction: recAction,
			Severity:             severity,
			RuleName:             ruleName,
			EstimatedSavings:     savings,
			Status:               r.Status,
			Category:             category,
			IsDismissed:          r.IsDismissed,
			DismissedReason:      r.DismissedReason,
			AccountObjectID:      r.AccountObjectID,
			UpdatedBy:            r.UpdatedBy,
		}
		if len(r.Recommendation) > 0 {
			if err := json.Unmarshal(r.Recommendation, &rec.Recommendation); err != nil {
				slog.Error("Failed to unmarshal recommendation blob", "id", rec.ID, "error", err)
			}
		}

		resIdent := ""
		if r.ResourceIdentifier != nil {
			resIdent = *r.ResourceIdentifier
		}
		resMeta := map[string]any{}
		if len(r.ResourceMetadata) > 0 {
			err := common.UnmarshalJson(r.ResourceMetadata, &resMeta)
			if err != nil {
				slog.Error("Failed to unmarshal resource metadata", "error", err)
			}
		}

		recs = append(recs, model.RecommendationWithResource{
			Recommendation:     rec,
			ResourceIdentifier: resIdent,
			ResourceMetadata:   resMeta,
		})
	}
	return recs, nil
}

func (d *OptimizerDao) SaveAutoOptimizeTasks(ctx context.Context, tasks []model.AutoOptimizeTask) error {
	if len(tasks) == 0 {
		return nil
	}
	query := `
		INSERT INTO auto_pilot_task (
			id, auto_pilot_id, tenant_id, account_id, recommendation_id, 
			status, created_at, updated_at, meta, attributes, resource_filter, scheduled_time, name,
			reason, error, command, task_id, skipped_by
		) VALUES (
			:id, :auto_pilot_id, :tenant_id, :account_id, :recommendation_id, 
			:status, :created_at, :updated_at, :meta, :attributes, :resource_filter, :scheduled_time, :name,
			:reason, :error, :command, :task_id, :skipped_by
		) ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at,
			reason = EXCLUDED.reason,
			error = EXCLUDED.error,
			command = EXCLUDED.command,
			task_id = EXCLUDED.task_id,
			skipped_by = EXCLUDED.skipped_by,
			meta = EXCLUDED.meta,
			attributes = EXCLUDED.attributes
	`
	// Helper struct for NamedExec
	var dbTasks []map[string]interface{}
	for _, t := range tasks {
		metaBytes, _ := json.Marshal(t.Meta)
		attrBytes, _ := json.Marshal(t.Attributes)
		rfBytes, _ := json.Marshal(t.ResourceFilter)

		dbTasks = append(dbTasks, map[string]interface{}{
			"id":                t.ID,
			"auto_pilot_id":     t.AutoPilotID,
			"tenant_id":         t.TenantID,
			"account_id":        t.AccountID,
			"recommendation_id": t.RecommendationID,
			"status":            t.Status,
			"created_at":        t.CreatedAt,
			"updated_at":        t.UpdatedAt,
			"meta":              string(metaBytes), // CAST TO STRING
			"attributes":        string(attrBytes), // CAST TO STRING
			"resource_filter":   string(rfBytes),   // CAST TO STRING
			"scheduled_time":    t.ScheduledTime,
			"name":              t.Name,
			"reason":            t.Reason,
			"error":             t.Error,
			"command":           t.Command,
			"task_id":           t.TaskID,
			"skipped_by":        t.SkippedBy,
		})
	}

	_, err := d.db.NamedExecContext(ctx, query, dbTasks)
	return err
}

func (d *OptimizerDao) SaveAutoOptimizeTask(ctx context.Context, task model.AutoOptimizeTask) error {
	return d.SaveAutoOptimizeTasks(ctx, []model.AutoOptimizeTask{task})
}

func (d *OptimizerDao) GetAutoOptimizeTask(ctx context.Context, id uuid.UUID) (*model.AutoOptimizeTask, error) {
	query := `SELECT * FROM auto_pilot_task WHERE id = $1`
	var dbTask struct {
		ID               uuid.UUID  `db:"id"`
		AutoPilotID      uuid.UUID  `db:"auto_pilot_id"`
		TenantID         uuid.UUID  `db:"tenant_id"`
		AccountID        uuid.UUID  `db:"account_id"`
		RecommendationID *uuid.UUID `db:"recommendation_id"`
		Status           string     `db:"status"`
		CreatedAt        time.Time  `db:"created_at"`
		UpdatedAt        time.Time  `db:"updated_at"`
		Meta             []byte     `db:"meta"`
		Attributes       []byte     `db:"attributes"`
		ResourceFilter   []byte     `db:"resource_filter"`
		ScheduledTime    time.Time  `db:"scheduled_time"`
		Name             string     `db:"name"`
		Reason           *string    `db:"reason"`
		Error            *string    `db:"error"`
		Command          *string    `db:"command"`
		TaskID           *uuid.UUID `db:"task_id"`
		SkippedBy        *uuid.UUID `db:"skipped_by"`
	}

	if err := d.db.GetContext(ctx, &dbTask, query, id); err != nil {
		return nil, err
	}

	task := model.AutoOptimizeTask{
		ID:               dbTask.ID,
		AutoPilotID:      dbTask.AutoPilotID,
		TenantID:         dbTask.TenantID,
		AccountID:        dbTask.AccountID,
		RecommendationID: dbTask.RecommendationID,
		Status:           dbTask.Status,
		CreatedAt:        dbTask.CreatedAt,
		UpdatedAt:        dbTask.UpdatedAt,
		ScheduledTime:    dbTask.ScheduledTime,
		Name:             dbTask.Name,
		Reason:           dbTask.Reason,
		Error:            dbTask.Error,
		Command:          dbTask.Command,
		TaskID:           dbTask.TaskID,
		SkippedBy:        dbTask.SkippedBy,
	}

	if len(dbTask.Meta) > 0 {
		if err := json.Unmarshal(dbTask.Meta, &task.Meta); err != nil {
			slog.Error("Failed to unmarshal meta for task", "id", task.ID, "error", err)
		}
	}
	if len(dbTask.Attributes) > 0 {
		if err := json.Unmarshal(dbTask.Attributes, &task.Attributes); err != nil {
			slog.Error("Failed to unmarshal attributes for task", "id", task.ID, "error", err)
		}
	}
	if len(dbTask.ResourceFilter) > 0 {
		if err := json.Unmarshal(dbTask.ResourceFilter, &task.ResourceFilter); err != nil {
			slog.Error("Failed to unmarshal resource filter for task", "id", task.ID, "error", err)
		}
	}

	return &task, nil
}

func (d *OptimizerDao) GetAgent(ctx context.Context, accountID uuid.UUID) (*model.Agent, error) {
	query := `
		SELECT 
			id, created_at, updated_at, tenant, cloud_account_id, type, status, 
			last_connected_at, last_synced_at, version, k8s_version
		FROM agent WHERE cloud_account_id = $1
	`
	var agent model.Agent
	if err := d.db.GetContext(ctx, &agent, query, accountID); err != nil {
		return nil, err
	}
	return &agent, nil
}

func (d *OptimizerDao) GetWorkloadFiltersForNamespace(ctx context.Context, accountID, tenantID uuid.UUID, namespace string, category string) ([]model.AutoOptimizeResourceFilter, error) {
	query := `
		SELECT resource_identifier 
		FROM auto_optimize_resource_map 
		WHERE account_id = $1 AND tenant_id = $2 AND auto_optimize_type = $3
		AND resource_identifier->>'namespace' = $4
		AND resource_identifier->>'name' IS NOT NULL
	`
	rows, err := d.db.QueryContext(ctx, query, accountID, tenantID, category, namespace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var filters []model.AutoOptimizeResourceFilter
	for rows.Next() {
		var riBytes []byte
		if err := rows.Scan(&riBytes); err != nil {
			return nil, err
		}
		var ri model.AutoOptimizeResourceFilter
		if err := json.Unmarshal(riBytes, &ri); err != nil {
			continue
		}
		filters = append(filters, ri)
	}
	return filters, nil
}

func (d *OptimizerDao) UpdateRecommendationStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := d.db.ExecContext(ctx, "UPDATE recommendation SET status = $1, updated_at = NOW() WHERE id = $2", status, id)
	return err
}

func (d *OptimizerDao) GetActiveTasksForRecommendations(ctx context.Context, recommendationIDs []uuid.UUID) (map[uuid.UUID]model.AutoOptimizeTask, error) {
	if len(recommendationIDs) == 0 {
		return nil, nil
	}
	query := `SELECT * FROM auto_pilot_task WHERE recommendation_id = ANY($1) AND status = $2`

	var dbTasks []struct {
		ID               uuid.UUID  `db:"id"`
		AutoPilotID      uuid.UUID  `db:"auto_pilot_id"`
		TenantID         uuid.UUID  `db:"tenant_id"`
		AccountID        uuid.UUID  `db:"account_id"`
		RecommendationID *uuid.UUID `db:"recommendation_id"`
		Status           string     `db:"status"`
		CreatedAt        time.Time  `db:"created_at"`
		UpdatedAt        time.Time  `db:"updated_at"`
		Meta             []byte     `db:"meta"`
		Attributes       []byte     `db:"attributes"`
		ResourceFilter   []byte     `db:"resource_filter"`
		ScheduledTime    time.Time  `db:"scheduled_time"`
		Name             string     `db:"name"`
		Reason           *string    `db:"reason"`
		Error            *string    `db:"error"`
		Command          *string    `db:"command"`
		TaskID           *uuid.UUID `db:"task_id"`
		SkippedBy        *uuid.UUID `db:"skipped_by"`
	}

	if err := d.db.SelectContext(ctx, &dbTasks, query, pq.Array(recommendationIDs), model.AutopilotTaskStatusScheduled); err != nil {
		return nil, err
	}

	res := make(map[uuid.UUID]model.AutoOptimizeTask)
	for _, dt := range dbTasks {
		task := model.AutoOptimizeTask{
			ID:               dt.ID,
			AutoPilotID:      dt.AutoPilotID,
			TenantID:         dt.TenantID,
			AccountID:        dt.AccountID,
			RecommendationID: dt.RecommendationID,
			Status:           dt.Status,
			CreatedAt:        dt.CreatedAt,
			UpdatedAt:        dt.UpdatedAt,
			ScheduledTime:    dt.ScheduledTime,
			Name:             dt.Name,
			Reason:           dt.Reason,
			Error:            dt.Error,
			Command:          dt.Command,
			TaskID:           dt.TaskID,
			SkippedBy:        dt.SkippedBy,
		}
		if len(dt.Meta) > 0 {
			if err := json.Unmarshal(dt.Meta, &task.Meta); err != nil {
				slog.Error("Failed to unmarshal meta for task in batch", "id", task.ID, "error", err)
			}
		}
		if len(dt.Attributes) > 0 {
			if err := json.Unmarshal(dt.Attributes, &task.Attributes); err != nil {
				slog.Error("Failed to unmarshal attributes for task in batch", "id", task.ID, "error", err)
			}
		}
		if len(dt.ResourceFilter) > 0 {
			if err := json.Unmarshal(dt.ResourceFilter, &task.ResourceFilter); err != nil {
				slog.Error("Failed to unmarshal resource filter for task in batch", "id", task.ID, "error", err)
			}
		}
		if dt.RecommendationID != nil {
			res[*dt.RecommendationID] = task
		}
	}
	return res, nil
}

func (d *OptimizerDao) GetActiveResolutionsForRecommendations(ctx context.Context, recommendationIDs []uuid.UUID) (map[uuid.UUID][]model.RecommendationResolution, error) {
	if len(recommendationIDs) == 0 {
		return nil, nil
	}
	query := `SELECT * FROM recommendation_resolution WHERE recommendation_id = ANY($1) AND status = $2`

	var dbResolutions []struct {
		ID               uuid.UUID  `db:"id"`
		RecommendationID uuid.UUID  `db:"recommendation_id"`
		Type             string     `db:"type"`
		Data             []byte     `db:"data"`
		Status           string     `db:"status"`
		TypeReferenceID  string     `db:"type_reference_id"`
		ResolverType     string     `db:"resolver_type"`
		ResolverID       uuid.UUID  `db:"resolver_id"`
		CreatedAt        time.Time  `db:"created_at"`
		UpdatedAt        *time.Time `db:"updated_at"`
		StatusMessage    *string    `db:"status_message"`
	}

	if err := d.db.SelectContext(ctx, &dbResolutions, query, pq.Array(recommendationIDs), model.RecommendationResolutionStatusInProgress); err != nil {
		return nil, err
	}

	res := make(map[uuid.UUID][]model.RecommendationResolution)
	for _, dr := range dbResolutions {
		resolution := model.RecommendationResolution{
			ID:               dr.ID,
			RecommendationID: dr.RecommendationID,
			Type:             dr.Type,
			Status:           dr.Status,
			TypeReferenceID:  dr.TypeReferenceID,
			ResolverType:     dr.ResolverType,
			ResolverID:       dr.ResolverID,
			CreatedAt:        dr.CreatedAt,
			StatusMessage:    dr.StatusMessage,
		}
		if dr.UpdatedAt != nil {
			resolution.UpdatedAt = *dr.UpdatedAt
		}
		if len(dr.Data) > 0 {
			if err := json.Unmarshal(dr.Data, &resolution.Data); err != nil {
				slog.Error("Failed to unmarshal resolution data", "id", dr.ID, "error", err)
			}
		}
		res[dr.RecommendationID] = append(res[dr.RecommendationID], resolution)
	}
	return res, nil
}

func joinOR(conds []string) string {
	res := ""
	for i, c := range conds {
		if i > 0 {
			res += " OR "
		}
		res += c
	}
	return res
}
