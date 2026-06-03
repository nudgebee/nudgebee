package k8s_upgrade

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func GetAccountDetails(accountID string) (AccountDetails, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return AccountDetails{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	var details AccountDetails
	err = databaseManager.Db.Get(&details, `
		SELECT type, status, COALESCE(k8s_version, '') as k8s_version, COALESCE(k8s_provider, '') as k8s_provider
		FROM agent WHERE cloud_account_id = $1 AND type = 'k8s'`, accountID)
	if err != nil {
		return AccountDetails{}, fmt.Errorf("failed to fetch account details: %w", err)
	}

	return details, nil
}

func StoreUpgradePlan(ctx *security.RequestContext, tenantID string, template UpgradePlanTemplate) error {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	tx, err := databaseManager.Db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				ctx.GetLogger().Error("Failed to rollback transaction", "error", rollbackErr)
			}
		}
	}()

	// Only delete existing plans with the same current_version and target_version
	_, err = tx.Exec(`
		DELETE FROM upgrade_plan
			WHERE tenant_id = $1
			AND account_id = $2
			AND current_version = $3
			AND target_version = $4
	`, tenantID, template.AccountID, template.CurrentVersion, template.TargetVersion)
	if err != nil {
		return fmt.Errorf("failed to delete existing upgrade plan for same version: %w", err)
	}

	ctx.GetLogger().Debug("Deleted existing upgrade plan for same version if any",
		"tenant_id", tenantID,
		"account_id", template.AccountID,
		"current_version", template.CurrentVersion,
		"target_version", template.TargetVersion)

	planID := uuid.New().String()

	_, err = tx.Exec(`
			INSERT INTO upgrade_plan
				(id, tenant_id, account_id, current_version, target_version, k8s_provider, status)
			VALUES 
				($1, $2, $3, $4, $5, $6, $7)
		`, planID, tenantID, template.AccountID, template.CurrentVersion, template.TargetVersion, template.K8sProvider, "Pending")
	if err != nil {
		return fmt.Errorf("failed to insert step: %w", err)
	}

	for _, step := range template.Steps {
		stepId := uuid.New().String()
		_, err := tx.Exec(`
			INSERT INTO upgrade_plan_steps 
				(id, tenant_id, account_id, plan_id, title, sequence, description, status)
			VALUES 
				($1, $2, $3, $4, $5, $6, $7, $8)
		`, stepId, tenantID, template.AccountID, planID, step.Title, step.Sequence, step.Description, step.Status)
		if err != nil {
			return fmt.Errorf("failed to insert step: %w", err)
		}

		for _, task := range step.Tasks {
			action := sql.NullString{String: task.Action, Valid: task.Action != ""}
			resourceType := sql.NullString{String: task.ResourceType, Valid: task.ResourceType != ""}
			_, err := tx.Exec(`
				INSERT INTO upgrade_plan_tasks
					(step_id, sequence, title, description, action, status, is_required, resource_type)
				VALUES
					($1, $2, $3, $4, $5, $6, $7, $8)
			`, stepId, task.Sequence, task.Title, task.Description, action, task.Status, task.IsRequired, resourceType)
			if err != nil {
				return fmt.Errorf("failed to insert task: %w", err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	ctx.GetLogger().Info("Upgrade plan stored successfully", "plan_id", planID, "account_id", template.AccountID)
	return nil
}

func FetchUpgradePlan(ctx *security.RequestContext, tenantId, accountId string) (UpgradePlan, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return UpgradePlan{}, fmt.Errorf("failed to get database manager: %w", err)
	}

	var plan UpgradePlan
	err = databaseManager.Db.QueryRowx(`
		SELECT id, tenant_id, account_id, created_at, updated_at, current_version, target_version, k8s_provider, status
		FROM upgrade_plan
		WHERE tenant_id = $1 AND account_id = $2
	`, tenantId, accountId).Scan(
		&plan.ID,
		&plan.TenantID,
		&plan.AccountID,
		&plan.CreatedAt,
		&plan.UpdatedAt,
		&plan.CurrentVersion,
		&plan.TargetVersion,
		&plan.K8sProvider,
		&plan.Status,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UpgradePlan{}, fmt.Errorf("no upgrade plan found for account_id: %s", accountId)
		}
		return UpgradePlan{}, fmt.Errorf("failed to fetch upgrade plan: %w", err)
	}

	stepRows, err := databaseManager.Db.Queryx(`
		SELECT id, title, sequence, description, status
		FROM upgrade_plan_steps 
		WHERE plan_id = $1
		ORDER BY sequence ASC
	`, plan.ID)
	if err != nil {
		return UpgradePlan{}, fmt.Errorf("failed to fetch upgrade steps: %w", err)
	}
	defer func() {
		if err := stepRows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close step rows", "error", err)
		}
	}()

	var steps []Step
	stepIndexMap := make(map[string]int)

	for stepRows.Next() {
		var step Step
		err := stepRows.Scan(&step.ID, &step.Title, &step.Sequence, &step.Description, &step.Status)
		if err != nil {
			return UpgradePlan{}, fmt.Errorf("failed to scan step: %w", err)
		}

		step.Tasks = []Task{}
		steps = append(steps, step)
		stepIndexMap[step.ID] = len(steps) - 1
	}

	if len(steps) > 0 {
		taskRows, err := databaseManager.Db.Queryx(`
			SELECT id, step_id, sequence, title, description, COALESCE(action, '') as action, status, COALESCE(owner::text, '') as owner, COALESCE(is_required, false) as is_required, COALESCE(resource_type, '') as resource_type
			FROM upgrade_plan_tasks
			WHERE step_id IN (
				SELECT id FROM upgrade_plan_steps
				WHERE plan_id = $1
			)
			ORDER BY step_id, sequence ASC
		`, plan.ID)
		if err != nil {
			return UpgradePlan{}, fmt.Errorf("failed to fetch upgrade tasks: %w", err)
		}
		defer func() {
			if err := taskRows.Close(); err != nil {
				ctx.GetLogger().Error("Failed to close task rows", "error", err)
			}
		}()

		for taskRows.Next() {
			var stepId string
			var task Task
			var owner sql.NullString
			err := taskRows.Scan(&task.ID, &stepId, &task.Sequence, &task.Title, &task.Description, &task.Action, &task.Status, &owner, &task.IsRequired, &task.ResourceType)
			if err != nil {
				return UpgradePlan{}, fmt.Errorf("failed to scan task: %w", err)
			}
			if owner.Valid {
				task.Owner = owner.String
			}
			task.StepID = stepId
			if idx, exists := stepIndexMap[stepId]; exists {
				steps[idx].Tasks = append(steps[idx].Tasks, task)
			}
		}
	}

	plan.Steps = steps
	return plan, nil
}

func FetchAllUpgradePlans(ctx *security.RequestContext, tenantId, accountId string) ([]UpgradePlan, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	// 1. Fetch all plans for the account
	var plans []UpgradePlan
	err = databaseManager.Db.Select(&plans, `
		SELECT id, tenant_id, account_id, created_at, updated_at, current_version, target_version, k8s_provider, status
		FROM upgrade_plan
		WHERE tenant_id = $1 AND account_id = $2
		ORDER BY created_at DESC
	`, tenantId, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch upgrade plans: %w", err)
	}

	if len(plans) == 0 {
		return []UpgradePlan{}, nil
	}

	planMap := make(map[string]*UpgradePlan, len(plans))
	planIDs := make([]string, len(plans))
	for i := range plans {
		planMap[plans[i].ID] = &plans[i]
		planIDs[i] = plans[i].ID
		plans[i].Steps = []Step{} // Initialize steps slice
	}

	// 2. Fetch all steps for all plans
	query, args, err := sqlx.In(`
		SELECT id, plan_id, title, sequence, description, status
		FROM upgrade_plan_steps
		WHERE plan_id IN (?)
		ORDER BY plan_id, sequence ASC
	`, planIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to build query for steps: %w", err)
	}
	query = databaseManager.Db.Rebind(query)

	stepRows, err := databaseManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch upgrade steps: %w", err)
	}
	defer func() {
		if err := stepRows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close step rows", "error", err)
		}
	}()

	stepIndexMap := make(map[string]struct {
		planID    string
		stepIndex int
	})
	var stepIDs []string
	for stepRows.Next() {
		var step Step
		var planID string
		err := stepRows.Scan(&step.ID, &planID, &step.Title, &step.Sequence, &step.Description, &step.Status)
		if err != nil {
			return nil, fmt.Errorf("failed to scan step: %w", err)
		}
		step.Tasks = []Task{}
		stepIDs = append(stepIDs, step.ID)
		if plan, ok := planMap[planID]; ok {
			plan.Steps = append(plan.Steps, step)
			stepIndexMap[step.ID] = struct {
				planID    string
				stepIndex int
			}{planID: planID, stepIndex: len(plan.Steps) - 1}
		}
	}

	if len(stepIDs) == 0 {
		return plans, nil
	}

	// 3. Fetch all tasks for all steps
	query, args, err = sqlx.In(`
		SELECT id, step_id, sequence, title, description, COALESCE(action, '') as action, status, COALESCE(owner::text, '') as owner, COALESCE(is_required, false) as is_required, COALESCE(resource_type, '') as resource_type
		FROM upgrade_plan_tasks
		WHERE step_id IN (?)
		ORDER BY step_id, sequence ASC
	`, stepIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to build query for tasks: %w", err)
	}
	query = databaseManager.Db.Rebind(query)

	taskRows, err := databaseManager.Db.Queryx(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch upgrade tasks: %w", err)
	}
	defer func() {
		if err := taskRows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close task rows", "error", err)
		}
	}()

	for taskRows.Next() {
		var task Task
		var stepId string
		var owner sql.NullString
		err := taskRows.Scan(&task.ID, &stepId, &task.Sequence, &task.Title, &task.Description, &task.Action, &task.Status, &owner, &task.IsRequired, &task.ResourceType)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		if owner.Valid {
			task.Owner = owner.String
		}
		task.StepID = stepId
		if stepInfo, ok := stepIndexMap[stepId]; ok {
			if plan, ok := planMap[stepInfo.planID]; ok {
				plan.Steps[stepInfo.stepIndex].Tasks = append(plan.Steps[stepInfo.stepIndex].Tasks, task)
			}
		}
	}

	return plans, nil
}

// DeleteUpgradePlan removes a plan and its associated steps/tasks (H6).
func DeleteUpgradePlan(ctx *security.RequestContext, tenantID, accountID, planID string) error {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	tx, err := databaseManager.Db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				ctx.GetLogger().Error("Failed to rollback transaction", "error", rollbackErr)
			}
		}
	}()

	// Delete tasks belonging to steps of this plan
	_, err = tx.Exec(`
		DELETE FROM upgrade_plan_tasks
		WHERE step_id IN (
			SELECT id FROM upgrade_plan_steps
			WHERE plan_id = $1 AND tenant_id = $2 AND account_id = $3
		)
	`, planID, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to delete plan tasks: %w", err)
	}

	// Delete steps
	_, err = tx.Exec(`
		DELETE FROM upgrade_plan_steps
		WHERE plan_id = $1 AND tenant_id = $2 AND account_id = $3
	`, planID, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to delete plan steps: %w", err)
	}

	// Delete the plan itself
	result, err := tx.Exec(`
		DELETE FROM upgrade_plan
		WHERE id = $1 AND tenant_id = $2 AND account_id = $3
	`, planID, tenantID, accountID)
	if err != nil {
		return fmt.Errorf("failed to delete plan: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("plan not found")
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	ctx.GetLogger().Info("Upgrade plan deleted successfully", "plan_id", planID, "account_id", accountID)
	return nil
}

func UpsertTask(ctx *security.RequestContext, tenantID string, request TaskUpsertRequest) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	tx, err := dbm.Db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				ctx.GetLogger().Error("Failed to rollback transaction", "error", rollbackErr)
			}
		}
	}()

	var setClauses []string
	var updatedFields []string
	args := []interface{}{request.TaskID}

	addClause := func(field, value string) {
		if value != "" {
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", field, len(args)+1))
			args = append(args, value)
			updatedFields = append(updatedFields, field)
		}
	}

	addClause("title", request.Title)
	addClause("description", request.Description)
	addClause("status", request.Status)

	if request.Owner != "" {
		addClause("owner", request.Owner)
	} else if request.Status == "" {
		setClauses = append(setClauses, "owner = NULL")
		updatedFields = append(updatedFields, "owner")
	}

	var rowsAffected int64
	if len(setClauses) > 0 {
		updateQuery := fmt.Sprintf(`
			UPDATE upgrade_plan_tasks
			SET %s
			WHERE id = $1
		`, strings.Join(setClauses, ", "))

		result, err := tx.Exec(updateQuery, args...)
		if err != nil {
			return fmt.Errorf("failed to update task: %w", err)
		}

		if rowsAffected, err = result.RowsAffected(); err != nil {
			return fmt.Errorf("failed to get rows affected: %w", err)
		}
	}

	var taskID string
	operation := "update"
	if rowsAffected == 0 {
		operation = "insert"
		taskID = request.TaskID
		if taskID == "" {
			taskID = uuid.New().String()
		}

		insertQuery := `
			INSERT INTO upgrade_plan_tasks
				(id, step_id, sequence, title, description, status, owner)
			VALUES
				($1, $2, $3, $4, $5, $6, $7)
		`
		_, err = tx.Exec(insertQuery,
			taskID, request.StepID, request.Sequence,
			request.Title, request.Description,
			request.Status, request.Owner,
		)
		if err != nil {
			return fmt.Errorf("failed to create new task: %w", err)
		}

		ctx.GetLogger().Info("Created new task", "task_id", taskID, "step_id", request.StepID)
	} else {
		taskID = request.TaskID
		ctx.GetLogger().Info("Updated existing task",
			"task_id", taskID, "status", request.Status, "owner", request.Owner)
	}

	err = RecordUpsertActivity(ctx.GetSecurityContext().GetUserId(), tenantID, operation, request, tx, updatedFields)
	if err != nil {
		return fmt.Errorf("failed to record activity: %w", err)
	}

	// Update step status based on required tasks when task status is updated
	if request.Status != "" && request.StepID != "" {
		err = updateStepStatusBasedOnRequiredTasks(ctx, request.StepID, tx)
		if err != nil {
			return fmt.Errorf("failed to update step status: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func RecordUpsertActivity(userID, tenantID, operation string, request TaskUpsertRequest, tx *sqlx.Tx, updates []string) error {
	fieldValues := map[string]string{
		"title":       request.Title,
		"description": request.Description,
		"status":      request.Status,
		"owner":       request.Owner,
		"comment":     request.Comment,
	}

	activities := make([]UpgradePlanAudit, 0, len(updates))

	for _, field := range updates {
		if newVal, ok := fieldValues[field]; ok {
			activities = append(activities, UpgradePlanAudit{
				TenantID:   tenantID,
				AccountID:  request.AccountID,
				PlanID:     request.PlanID,
				StepID:     request.StepID,
				TaskID:     request.TaskID,
				Field:      field,
				Action:     operation,
				OldValue:   "",
				NewValue:   newVal,
				ActionedBy: userID,
				Comment:    request.Comment,
			})
		}
	}

	if len(activities) == 0 {
		return nil
	}

	_, err := tx.NamedExec(`
		INSERT INTO upgrade_plan_audit (
			tenant_id, account_id, plan_id, step_id, task_id, field, action, old_value, new_value, actioned_by, comments
		) VALUES (
			:tenant_id, :account_id, :plan_id, :step_id, :task_id, :field, :action, :old_value, :new_value, :actioned_by, :comments
		)
	`, activities)

	return err
}

func RecordCommandExecution(ctx *security.RequestContext, tenantID string, request ExecuteCommandRequest, output string, success bool) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	userID := ctx.GetSecurityContext().GetUserId()

	auditRecord := UpgradePlanAudit{
		TenantID:   tenantID,
		AccountID:  request.AccountID,
		PlanID:     request.PlanID,
		StepID:     request.StepID,
		TaskID:     request.TaskID,
		Field:      "command",
		Action:     "execute",
		OldValue:   request.Command,
		NewValue:   output,
		ActionedBy: userID,
		Comment:    fmt.Sprintf("Success: %t", success),
	}

	_, err = dbm.Db.NamedExec(`
		INSERT INTO upgrade_plan_audit (
			tenant_id, account_id, plan_id, step_id, task_id, field, action, old_value, new_value, actioned_by, comments
		) VALUES (
			:tenant_id, :account_id, :plan_id, :step_id, :task_id, :field, :action, :old_value, :new_value, :actioned_by, :comments
		)
	`, auditRecord)

	if err != nil {
		return fmt.Errorf("failed to record command execution audit: %w", err)
	}

	ctx.GetLogger().Debug("Command execution recorded in audit",
		"task_id", request.TaskID,
		"command_type", request.CommandType,
		"success", success,
		"user_id", userID)

	return nil
}

func updateStepStatusBasedOnRequiredTasks(ctx *security.RequestContext, stepID string, tx *sqlx.Tx) error {
	var totalRequired, completedOrSkipped int

	err := tx.QueryRow(`
		SELECT 
			COUNT(*) AS total_required,
			COUNT(*) FILTER (WHERE status IN ('Completed', 'Skipped')) AS completed_or_skipped
		FROM upgrade_plan_tasks
		WHERE step_id = $1 AND is_required = true
	`, stepID).Scan(&totalRequired, &completedOrSkipped)

	if err != nil {
		return fmt.Errorf("failed to fetch required task counts for step %s: %w", stepID, err)
	}

	if totalRequired == 0 {
		ctx.GetLogger().Debug("No required tasks found for step, skipping status update", "step_id", stepID)
		return nil
	}

	newStepStatus := "Incomplete"
	if totalRequired == completedOrSkipped {
		newStepStatus = "Completed"
	}

	_, err = tx.Exec(`
		UPDATE upgrade_plan_steps
		SET status = $1
		WHERE id = $2
	`, newStepStatus, stepID)
	if err != nil {
		return fmt.Errorf("failed to update step status: %w", err)
	}

	ctx.GetLogger().Debug("Updated step status based on required tasks",
		"step_id", stepID,
		"new_status", newStepStatus,
		"total_required", totalRequired,
		"completed_or_skipped", completedOrSkipped,
	)

	return nil
}

func getCloudAccountAttributes(ctx *security.RequestContext, accountID string) (map[string]string, error) {
	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	query := `
		select
			name,
			value
		from
			cloud_account_attrs
		where
			cloud_account_id = $1
			and name in (
				'k8s_provider',
				'k8s_provider_account_number',
				'k8s_provider_cluster_name',
				'k8s_provider_region',
				'k8s_provider_zone',
				'k8s_provider_project_id',
				'k8s_provider_resource_group'
			)
	`

	rows, err := databaseManager.Db.Queryx(query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query cloud account attributes: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close task rows", "error", err)
		}
	}()

	attributes := make(map[string]string)
	for rows.Next() {
		var name, value string
		err := rows.Scan(&name, &value)
		if err != nil {
			return nil, fmt.Errorf("failed to scan cloud account attribute: %w", err)
		}
		attributes[name] = value
	}

	if accountNumber, exists := attributes["k8s_provider_account_number"]; exists && accountNumber != "" {
		cloudAccountQuery := `select id from cloud_accounts where account_number = $1`

		var cloudAccountID string
		err := databaseManager.Db.QueryRowx(cloudAccountQuery, accountNumber).Scan(&cloudAccountID)
		if err != nil {
			return nil, fmt.Errorf("failed to query cloud account ID for account_number %s: %w", accountNumber, err)
		}

		attributes["cloud_account_id"] = cloudAccountID
	}

	return attributes, nil
}

// GetHealthCheckHistory retrieves health check history for comparison
func GetHealthCheckHistory(ctx *security.RequestContext, accountID string, checkType ClusterHealthCheckType, limit int) ([]*ClusterHealthSummary, error) {
	query := `
		SELECT id, tenant_id, cloud_account_id, rule_name, recommendation, status, severity, 
		       estimated_savings, note, created_at, updated_at
		FROM public.recommendation
		WHERE tenant_id = $1 AND cloud_account_id = $2 AND category = $3 AND rule_name LIKE $4
		ORDER BY created_at DESC
		LIMIT $5
	`

	category := "InfraUpgrade"
	ruleNamePattern := fmt.Sprintf("%s_%%", checkType)

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	rows, err := databaseManager.Db.Queryx(query,
		ctx.GetSecurityContext().GetTenantId(),
		accountID,
		category,
		ruleNamePattern,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query health check history: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			ctx.GetLogger().Error("Failed to close rows", "error", err)
		}
	}()

	summaries := make([]*ClusterHealthSummary, 0)
	summaryMap := make(map[string]*ClusterHealthSummary)

	for rows.Next() {
		var rec RecommendationRecord
		err := rows.Scan(&rec.ID, &rec.TenantID, &rec.CloudAccountID, &rec.RuleName,
			&rec.Recommendation, &rec.Status, &rec.Severity,
			&rec.EstimatedSavings, &rec.Note, &rec.CreatedAt, &rec.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan recommendation record: %w", err)
		}

		// Parse the stored health check data
		if rec.RuleName == fmt.Sprintf("%s_summary", checkType) {
			// This is a summary record
			summary, err := parseHealthCheckSummary(&rec)
			if err != nil {
				ctx.GetLogger().Error("Failed to parse health check summary", "error", err)
				continue
			}
			summaries = append(summaries, summary)
			summaryMap[rec.CreatedAt.Format(time.RFC3339)] = summary
		} else {
			// This is an individual check record
			check, err := parseHealthCheck(&rec)
			if err != nil {
				ctx.GetLogger().Error("Failed to parse health check", "error", err)
				continue
			}

			// Find or create the summary for this timestamp
			timeKey := rec.CreatedAt.Format(time.RFC3339)
			if summary, exists := summaryMap[timeKey]; exists {
				summary.Checks = append(summary.Checks, *check)
			}
		}
	}

	return summaries, nil
}

// GetLatestHealthCheck retrieves the most recent health check of a specific type
func GetLatestHealthCheck(ctx *security.RequestContext, accountID string, checkType ClusterHealthCheckType) (*ClusterHealthSummary, error) {
	summaries, err := GetHealthCheckHistory(ctx, accountID, checkType, 1)
	if err != nil {
		return nil, err
	}

	if len(summaries) == 0 {
		return nil, fmt.Errorf("no health check found for account %s and type %s", accountID, checkType)
	}

	return summaries[0], nil
}

// DeleteOldHealthChecks removes health check records older than specified duration
func DeleteOldHealthChecks(ctx *security.RequestContext, accountID string, olderThan time.Duration) error {
	cutoffTime := time.Now().Add(-olderThan)

	query := `
		DELETE FROM public.recommendation
		WHERE tenant_id = $1 AND cloud_account_id = $2 AND category = $3 AND created_at < $4
	`

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	result, err := databaseManager.Db.Exec(query,
		ctx.GetSecurityContext().GetTenantId(),
		accountID,
		"ClusterHealthCheck",
		cutoffTime,
	)
	if err != nil {
		return fmt.Errorf("failed to delete old health checks: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	ctx.GetLogger().Info("Deleted old health check records", "rows_deleted", rowsAffected, "cutoff_time", cutoffTime)

	return nil
}

// Helper methods

// parseHealthCheck converts a recommendation record back to a health check
func parseHealthCheck(rec *RecommendationRecord) (*ClusterHealthCheck, error) {
	check := &ClusterHealthCheck{
		TenantID:  rec.TenantID,
		AccountID: rec.CloudAccountID,
		Summary:   rec.Recommendation,
		Status:    HealthCheckStatus(rec.Status),
		CreatedAt: rec.CreatedAt,
	}

	if rec.EstimatedSavings != nil {
		check.EstimatedSavings = rec.EstimatedSavings
	}

	// Parse the note field which contains additional health check data
	if rec.Note != nil {
		var noteData map[string]interface{}
		if err := json.Unmarshal([]byte(*rec.Note), &noteData); err == nil {
			if id, ok := noteData["health_check_id"].(string); ok {
				check.ID = id
			}
			if checkType, ok := noteData["check_type"].(string); ok {
				check.CheckType = ClusterHealthCheckType(checkType)
			}
			if details, ok := noteData["details"].(map[string]interface{}); ok {
				check.Details = details
			}
			if recommendations, ok := noteData["recommendations"].([]interface{}); ok {
				check.Recommendations = make([]string, len(recommendations))
				for i, r := range recommendations {
					if str, ok := r.(string); ok {
						check.Recommendations[i] = str
					}
				}
			}
		}
	}

	// Extract check name from rule name (format: checktype_checkname)
	if len(rec.RuleName) > len(check.CheckType)+1 {
		check.CheckName = rec.RuleName[len(check.CheckType)+1:]
	}

	return check, nil
}

// parseHealthCheckSummary converts a recommendation record back to a health check summary
func parseHealthCheckSummary(rec *RecommendationRecord) (*ClusterHealthSummary, error) {
	summary := &ClusterHealthSummary{
		TenantID:  rec.TenantID,
		AccountID: rec.CloudAccountID,
		CreatedAt: rec.CreatedAt,
		Checks:    make([]ClusterHealthCheck, 0),
	}

	// Parse check type from rule name
	switch rec.RuleName {
	case "pre_flight_summary":
		summary.CheckType = PreFlightCheck
	case "post_flight_summary":
		summary.CheckType = PostFlightCheck
	}

	// Parse summary details from note
	if rec.Note != nil {
		var noteData map[string]interface{}
		if err := json.Unmarshal([]byte(*rec.Note), &noteData); err == nil {
			if details, ok := noteData["details"].(map[string]interface{}); ok {
				if score, ok := details["overall_score"].(float64); ok {
					summary.OverallScore = int(score)
				}
				if totalChecks, ok := details["total_checks"].(float64); ok {
					summary.TotalChecks = int(totalChecks)
				}
				if healthyCount, ok := details["healthy_count"].(float64); ok {
					summary.HealthyCount = int(healthyCount)
				}
				if warningCount, ok := details["warning_count"].(float64); ok {
					summary.WarningCount = int(warningCount)
				}
				if criticalCount, ok := details["critical_count"].(float64); ok {
					summary.CriticalCount = int(criticalCount)
				}
			}
		}
	}

	return summary, nil
}

// StoreHealthCheckWithPlanID stores a health check with plan_id in account_object_id
func StoreHealthCheckWithPlanID(ctx *security.RequestContext, healthCheck *HealthCheck, tenantID, accountID, planID string, checkType ClusterHealthCheckType) error {
	// Convert HealthCheck to JSON
	healthCheckJSON, err := json.Marshal(healthCheck)
	if err != nil {
		return fmt.Errorf("failed to marshal health check: %w", err)
	}

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get database manager: %w", err)
	}

	// Delete previous health check for the same plan_id and check_type to avoid duplicate key constraint
	deleteQuery := `
		DELETE FROM public.recommendation
		WHERE cloud_account_id = $1
			AND account_object_id = $2
			AND category = $3
			AND rule_name = $4
	`

	_, err = databaseManager.Db.Exec(deleteQuery, accountID, planID, "InfraUpgrade", string(checkType))
	if err != nil {
		return fmt.Errorf("failed to delete existing health check: %w", err)
	}

	ctx.GetLogger().Debug("Deleted existing health check if any",
		"account_id", accountID,
		"plan_id", planID,
		"check_type", checkType)

	// Insert new health check
	insertQuery := `
		INSERT INTO public.recommendation (
			tenant_id, cloud_account_id, account_object_id, category, rule_name, recommendation,
			recommendation_action, status, severity, estimated_savings, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id
	`

	var id string
	err = databaseManager.Db.QueryRowx(insertQuery,
		tenantID,
		accountID,
		planID,
		"InfraUpgrade",
		string(checkType),
		string(healthCheckJSON),
		"Modify",
		"Open",
		"Info",
		0.0,
		time.Now(),
		time.Now(),
	).Scan(&id)

	if err != nil {
		return fmt.Errorf("failed to store health check with plan_id: %w", err)
	}

	ctx.GetLogger().Info("Health check stored successfully with plan_id",
		"recommendation_id", id,
		"plan_id", planID,
		"check_type", checkType)

	return nil
}

// GetLatestHealthCheckByPlanID retrieves the most recent health check for a plan
func GetLatestHealthCheckByPlanID(ctx *security.RequestContext, accountID, planID string, checkType ClusterHealthCheckType) (*HealthCheck, error) {
	query := `
		SELECT recommendation, created_at
		FROM public.recommendation
		WHERE cloud_account_id = $1
		  AND account_object_id = $2
		  AND category = 'InfraUpgrade'
		  AND rule_name = $3
		ORDER BY created_at DESC
		LIMIT 1
	`

	databaseManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("failed to get database manager: %w", err)
	}

	var recommendationJSON string
	var createdAt time.Time
	err = databaseManager.Db.QueryRowx(query, accountID, planID, string(checkType)).Scan(&recommendationJSON, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("no %s health check found for account_id: %s, plan_id: %s", checkType, accountID, planID)
		}
		return nil, fmt.Errorf("failed to query health check: %w", err)
	}

	// Unmarshal from recommendation column
	var healthCheck HealthCheck
	if err := json.Unmarshal([]byte(recommendationJSON), &healthCheck); err != nil {
		return nil, fmt.Errorf("failed to unmarshal health check: %w", err)
	}

	ctx.GetLogger().Info("Retrieved health check by plan_id",
		"plan_id", planID,
		"check_type", checkType,
		"created_at", createdAt)

	return &healthCheck, nil
}
