package optimizer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/workflow"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
	"github.com/robfig/cron"
	"go.temporal.io/sdk/client"
)

type Service interface {
	GetRecommendations(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, ruleName *string, status []string, categories []string) (map[string][]string, error)
	GetWorkload(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, status *string, resourceFilters []model.AutoOptimizeResourceFilter) ([]string, error)
	SkipExecution(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, autoOptimizeID uuid.UUID, byMinutes int) (string, error)
	UpdateAutoOptimize(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, req model.AutoOptimizeRequestModel) (*string, error)
	CreateAutoOptimize(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, req model.AutoOptimizeRequestModel) (*string, error)
	ChangeStatus(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, autoOptimizeID uuid.UUID, status model.AutoOptimizeStatus) error
	GetActiveAutoOptimizes(ctx context.Context) ([]model.AutoOptimize, error)
	GenerateTasks(ctx context.Context, autoOptimizeID uuid.UUID) ([]model.AutoOptimizeTask, error)
	CompleteAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) error
	ExecuteAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) error
	SyncSchedules(ctx context.Context) error
}

type optimizerService struct {
	dao            OptimizerRepository
	factory        *ExecutorFactory
	temporalClient client.Client
}

func NewService(dao OptimizerRepository, temporalClient client.Client) Service {
	factory := NewExecutorFactory()
	factory.Register("vertical_rightsize", &VerticalRightsizeGenerator{})
	factory.Register("horizontal_rightsize", &HorizontalRightsizeGenerator{})
	factory.Register("pvc_rightsize", &PVCRightsizeGenerator{})
	factory.Register("continuous_rightsize", &ContinuousRightsizeGenerator{})

	return &optimizerService{
		dao:            dao,
		factory:        factory,
		temporalClient: temporalClient,
	}
}

func (s *optimizerService) SyncSchedules(ctx context.Context) error {
	aos, err := s.dao.GetActiveAutoOptimizes(ctx)
	if err != nil {
		return err
	}

	success := 0
	failed := 0
	skipped := 0
	expired := 0

	now := time.Now().UTC()
	for _, ao := range aos {
		if ao.EndAt != nil && ao.EndAt.Before(now) {
			expired++
			continue
		}

		err := s.createSchedule(ctx, ao)
		if err != nil {
			if strings.Contains(err.Error(), "Schedule already exists") {
				skipped++
			} else {
				failed++
				slog.Error("failed to sync schedule", "auto_optimize_id", ao.ID, "error", err)
			}
		} else {
			success++
			if ao.NextScheduleTime == nil {
				ao.NextScheduleTime = s.calculateNextScheduleTime(ao.ScheduleTime, time.Now().UTC())
				if err := s.dao.SaveAutoOptimize(ctx, ao); err != nil {
					slog.Error("failed to update next_schedule_time during sync", "auto_optimize_id", ao.ID, "error", err)
				}
			}
		}
	}

	if success > 0 || failed > 0 || skipped > 0 || expired > 0 {
		slog.Info("Optimizer Sync Completed", "created", success, "skipped", skipped, "failed", failed, "expired", expired)
	}
	return nil
}

func (s *optimizerService) ExecuteAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) error {
	ao, err := s.dao.GetAutoOptimize(ctx, autoOptimizeID)
	if err != nil {
		return err
	}

	if ao.Status != model.AutoOptimizeStatusActive && ao.Status != model.AutoOptimizeStatusDryrun {
		return fmt.Errorf("auto optimize must be in Active or Dryrun status to execute, current status: %s", ao.Status)
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                       "manual_trigger_" + s.scheduleID(ao.ID) + "_" + uuid.New().String(),
		TaskQueue:                workflow.OptimizerTaskQueue,
		WorkflowExecutionTimeout: 1 * time.Hour,
	}

	workflowInput := workflow.OptimizerWorkflowInput{
		AutoOptimizeID: ao.ID.String(),
	}

	_, err = s.temporalClient.ExecuteWorkflow(ctx, workflowOptions, workflow.OptimizerWorkflow, workflowInput)
	if err != nil {
		return fmt.Errorf("failed to trigger auto optimize workflow: %w", err)
	}

	return nil
}

func (s *optimizerService) CompleteAutoOptimize(ctx context.Context, autoOptimizeID uuid.UUID) error {
	ao, err := s.dao.GetAutoOptimize(ctx, autoOptimizeID)
	if err != nil {
		return err
	}
	ao.ExecutionStatus = string(model.AutopilotExecutionStatusIdle)
	now := time.Now().UTC()
	ao.LastExecutedTime = &now
	return s.dao.SaveAutoOptimize(ctx, *ao)
}

func (s *optimizerService) calculateNextScheduleTime(cronExpr string, from time.Time) *time.Time {
	sched, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return nil
	}
	next := sched.Next(from)
	return &next
}

func (s *optimizerService) GetActiveAutoOptimizes(ctx context.Context) ([]model.AutoOptimize, error) {
	return s.dao.GetActiveAutoOptimizes(ctx)
}

var restrictionMap = map[string][]string{
	"vertical_rightsize":   {"continuous_rightsize"},
	"continuous_rightsize": {"vertical_rightsize"},
}

var categoryFrequencyMap = map[string]string{
	"horizontal_rightsize": "50 * * * *",
	"pvc_rightsize":        "0 * * * *",
	"continuous_rightsize": "*/15 * * * *",
}

func getFrequency(category, frequency string) string {
	if frequency != "" {
		return frequency
	}
	if f, ok := categoryFrequencyMap[category]; ok {
		return f
	}
	return frequency
}

func (s *optimizerService) checkRestrictions(ctx context.Context, accountID, tenantID uuid.UUID, category string, resourceFilters []model.AutoOptimizeResourceFilter, currentID *uuid.UUID) error {
	ids, err := s.dao.GetAutoOptimizeIdsByFilter(ctx, accountID, tenantID, nil, resourceFilters)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if currentID != nil && id == *currentID {
			continue
		}
		ao, err := s.dao.GetAutoOptimize(ctx, id)
		if err != nil {
			continue
		}
		if ao.Status == model.AutoOptimizeStatusActive {
			// Check 1: Same Category Conflict
			if ao.Category == category {
				return fmt.Errorf("restriction failed: Auto optimize already Active for this configuration, please deactivate '%s' first", *ao.Name)
			}

			// Check 2: Explicit Restriction Map
			conflicts := restrictionMap[category]
			for _, conflict := range conflicts {
				if ao.Category == conflict {
					return fmt.Errorf("restriction failed: Auto optimize '%s' (%s) conflicts with new category '%s'", *ao.Name, ao.Category, category)
				}
			}
		}
	}

	return nil
}

func (s *optimizerService) GetRecommendations(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, ruleName *string, status []string, categories []string) (map[string][]string, error) {
	tenantID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("invalid tenant id: %w", err)
	}

	likeFilter, inFilter, err := s.dao.GetResourceFilters(ctx, accountID, tenantID, categories)
	if err != nil {
		return nil, err
	}

	if len(likeFilter) == 0 && len(inFilter) == 0 {
		return map[string][]string{"recommendation": []string{}}, nil
	}

	ids, err := s.dao.GetRecommendations(ctx, accountID, tenantID, ruleName, status, inFilter, likeFilter)
	if err != nil {
		return nil, err
	}

	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}

	return map[string][]string{"recommendation": strIDs}, nil
}

func (s *optimizerService) GetWorkload(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, status *string, resourceFilters []model.AutoOptimizeResourceFilter) ([]string, error) {
	tenantID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("invalid tenant id: %w", err)
	}

	ids, err := s.dao.GetAutoOptimizeIdsByFilter(ctx, accountID, tenantID, status, resourceFilters)
	if err != nil {
		return nil, err
	}

	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}
	return strIDs, nil
}

func (s *optimizerService) SkipExecution(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, autoOptimizeID uuid.UUID, byMinutes int) (string, error) {
	ao, err := s.dao.GetAutoOptimize(ctx, autoOptimizeID)
	if err != nil {
		return "", err
	}

	if ao.AccountID != accountID {
		return "", fmt.Errorf("auto optimize not found in account")
	}

	now := time.Now().UTC()
	var baseTime time.Time
	if ao.NextScheduleTime != nil && ao.NextScheduleTime.After(now) {
		baseTime = *ao.NextScheduleTime
	} else {
		baseTime = now
	}

	newTime := baseTime.Add(time.Duration(byMinutes) * time.Minute)
	ao.NextScheduleTime = &newTime

	err = s.dao.SaveAutoOptimize(ctx, *ao)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("The execution is successfully skipped by %d min, The new time for execution is %s", byMinutes, newTime.Format(time.RFC3339)), nil
}

func (s *optimizerService) UpdateAutoOptimize(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, req model.AutoOptimizeRequestModel) (*string, error) {
	tenantID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("invalid tenant id: %w", err)
	}
	userID, err := uuid.Parse(sc.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}

	if req.ID == nil {
		return nil, fmt.Errorf("id is required for update")
	}

	oldAO, err := s.dao.GetAutoOptimize(ctx, *req.ID)
	if err != nil {
		return nil, err
	}

	if oldAO.TenantID != tenantID {
		return nil, fmt.Errorf("unauthorized: auto optimize does not belong to this tenant")
	}
	if oldAO.AccountID != accountID {
		return nil, fmt.Errorf("unauthorized: auto optimize does not belong to this account")
	}

	if oldAO.Category != req.Category {
		return nil, fmt.Errorf("cannot change category of auto optimize")
	}

	freq := getFrequency(req.Category, req.Schedule.Frequency)
	if _, err := cron.ParseStandard(freq); err != nil {
		return nil, fmt.Errorf("invalid cron frequency: %w", err)
	}

	err = s.checkRestrictions(ctx, accountID, tenantID, req.Category, req.ResourceFilter, req.ID)
	if err != nil {
		return nil, err
	}

	ao := *oldAO
	ao.Rule = req.AutoOptimizeConfig
	ao.ScheduleTime = freq
	ao.EndAt = req.Schedule.EndDate

	notifBytes, _ := json.Marshal(req.Notification)
	var notifMap map[string]interface{}
	if err := json.Unmarshal(notifBytes, &notifMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal notification: %w", err)
	}
	ao.Notification = notifMap

	aoName := req.Name
	if strings.TrimSpace(aoName) == "" {
		aoName = req.GetAutoOptimizeName()
	}
	ao.Name = &aoName
	ao.UpdatedBy = &userID
	ao.UpdateDate = time.Now().UTC()
	ao.NextScheduleTime = s.calculateNextScheduleTime(ao.ScheduleTime, time.Now().UTC())

	if req.DryRun {
		ao.Status = model.AutoOptimizeStatusDryrun
	}
	ao.Attributes.GitOpsConfig = req.GitOps
	ao.Attributes.TicketConfig = req.Ticket

	err = s.dao.SaveAutoOptimize(ctx, ao)
	if err != nil {
		return nil, err
	}

	err = s.dao.DeleteResourceFilters(ctx, ao.ID)
	if err != nil {
		return nil, err
	}

	var filters []model.AutoOptimizeResourceMap
	for _, rf := range req.ResourceFilter {
		filters = append(filters, model.AutoOptimizeResourceMap{
			ID:                 uuid.New(),
			ResourceIdentifier: rf,
			AutoOptimizeID:     ao.ID,
			TenantID:           tenantID,
			AccountID:          accountID,
			AutoOptimizeType:   ao.Category,
		})
	}
	err = s.dao.SaveResourceFilters(ctx, filters)
	if err != nil {
		return nil, err
	}

	if err := s.updateSchedule(ctx, ao); err != nil {
		return nil, fmt.Errorf("failed to update temporal schedule: %w", err)
	}

	idStr := ao.ID.String()
	return &idStr, nil
}

func (s *optimizerService) CreateAutoOptimize(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, req model.AutoOptimizeRequestModel) (*string, error) {
	tenantID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	if err != nil {
		return nil, fmt.Errorf("invalid tenant id: %w", err)
	}
	userID, err := uuid.Parse(sc.GetSecurityContext().GetUserId())
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}

	err = s.checkRestrictions(ctx, accountID, tenantID, req.Category, req.ResourceFilter, nil)
	if err != nil {
		return nil, err
	}

	freq := getFrequency(req.Category, req.Schedule.Frequency)
	if _, err := cron.ParseStandard(freq); err != nil {
		return nil, fmt.Errorf("invalid cron frequency: %w", err)
	}

	startAt := req.Schedule.StartDate
	if startAt.IsZero() {
		startAt = time.Now().UTC()
	}

	notifBytes, _ := json.Marshal(req.Notification)
	var notifMap map[string]interface{}
	if err := json.Unmarshal(notifBytes, &notifMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal notification: %w", err)
	}

	name := req.Name
	if strings.TrimSpace(name) == "" {
		name = req.GetAutoOptimizeName()
	}

	ao := model.AutoOptimize{
		ID:              uuid.New(),
		AccountID:       accountID,
		TenantID:        tenantID,
		Category:        req.Category,
		Name:            &name,
		Rule:            req.AutoOptimizeConfig,
		ScheduleTime:    freq,
		StartAt:         startAt,
		EndAt:           req.Schedule.EndDate,
		Notification:    notifMap,
		CreatedBy:       userID,
		CreationDate:    time.Now().UTC(),
		UpdateDate:      time.Now().UTC(),
		Status:          model.AutoOptimizeStatusActive,
		ExecutionStatus: string(model.AutopilotExecutionStatusIdle),
		Attributes:      model.AutoOptimizeAttributes{},
	}

	ao.NextScheduleTime = s.calculateNextScheduleTime(ao.ScheduleTime, time.Now().UTC())

	if req.DryRun {
		ao.Status = model.AutoOptimizeStatusDryrun
	}
	ao.Attributes.GitOpsConfig = req.GitOps
	ao.Attributes.TicketConfig = req.Ticket

	err = s.dao.SaveAutoOptimize(ctx, ao)
	if err != nil {
		return nil, err
	}

	var filters []model.AutoOptimizeResourceMap
	for _, rf := range req.ResourceFilter {
		filters = append(filters, model.AutoOptimizeResourceMap{
			ID:                 uuid.New(),
			ResourceIdentifier: rf,
			AutoOptimizeID:     ao.ID,
			TenantID:           tenantID,
			AccountID:          accountID,
			AutoOptimizeType:   ao.Category,
		})
	}
	err = s.dao.SaveResourceFilters(ctx, filters)
	if err != nil {
		return nil, err
	}

	if err := s.createSchedule(ctx, ao); err != nil {
		return nil, fmt.Errorf("failed to create temporal schedule: %w", err)
	}

	idStr := ao.ID.String()
	return &idStr, nil
}

func (s *optimizerService) ChangeStatus(ctx context.Context, sc *security.RequestContext, accountID uuid.UUID, autoOptimizeID uuid.UUID, status model.AutoOptimizeStatus) error {
	tenantID, err := uuid.Parse(sc.GetSecurityContext().GetTenantId())
	if err != nil {
		return err
	}

	ao, err := s.dao.GetAutoOptimize(ctx, autoOptimizeID)
	if err != nil {
		return err
	}

	if ao.TenantID != tenantID {
		return fmt.Errorf("unauthorized")
	}
	if ao.AccountID != accountID {
		return fmt.Errorf("account mismatch")
	}

	switch status {
	case model.AutoOptimizeStatusActive:
		filters, err := s.dao.GetFiltersForAutoOptimize(ctx, autoOptimizeID)
		if err != nil {
			return err
		}
		if err := s.checkRestrictions(ctx, accountID, tenantID, ao.Category, filters, &autoOptimizeID); err != nil {
			return err
		}

		if err := s.unpauseOrCreateSchedule(ctx, *ao); err != nil {
			return fmt.Errorf("failed to activate schedule: %w", err)
		}

	case model.AutoOptimizeStatusDisabled:
		if err := s.dao.DeleteScheduledTasks(ctx, autoOptimizeID); err != nil {
			return fmt.Errorf("failed to cleanup scheduled tasks: %w", err)
		}

		err = s.temporalClient.ScheduleClient().GetHandle(ctx, s.scheduleID(ao.ID)).Pause(ctx, client.SchedulePauseOptions{
			Note: "Disabled by user",
		})
		if err != nil {
			return fmt.Errorf("failed to pause schedule: %w", err)
		}
	}

	ao.Status = status
	ao.UpdateDate = time.Now().UTC()

	return s.dao.SaveAutoOptimize(ctx, *ao)
}

func (s *optimizerService) scheduleID(aoID uuid.UUID) string {
	return "auto_optimize_" + aoID.String()
}

func (s *optimizerService) unpauseOrCreateSchedule(ctx context.Context, ao model.AutoOptimize) error {
	handle := s.temporalClient.ScheduleClient().GetHandle(ctx, s.scheduleID(ao.ID))
	err := handle.Unpause(ctx, client.ScheduleUnpauseOptions{})
	if err != nil {
		// If unpause fails, attempt to create (covers deleted/missing schedules)
		return s.createSchedule(ctx, ao)
	}

	if ao.NextScheduleTime == nil {
		ao.NextScheduleTime = s.calculateNextScheduleTime(ao.ScheduleTime, time.Now().UTC())
		return s.dao.SaveAutoOptimize(ctx, ao)
	}
	return nil
}

func (s *optimizerService) createSchedule(ctx context.Context, ao model.AutoOptimize) error {
	spec := client.ScheduleSpec{
		CronExpressions: []string{ao.ScheduleTime},
	}
	action := &client.ScheduleWorkflowAction{
		ID:                       "workflow_" + s.scheduleID(ao.ID),
		Workflow:                 workflow.OptimizerWorkflow,
		Args:                     []interface{}{workflow.OptimizerWorkflowInput{AutoOptimizeID: ao.ID.String()}},
		TaskQueue:                workflow.OptimizerTaskQueue,
		WorkflowExecutionTimeout: 1 * time.Hour,
	}

	_, err := s.temporalClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:     s.scheduleID(ao.ID),
		Spec:   spec,
		Action: action,
	})
	return err
}

func (s *optimizerService) updateSchedule(ctx context.Context, ao model.AutoOptimize) error {
	handle := s.temporalClient.ScheduleClient().GetHandle(ctx, s.scheduleID(ao.ID))
	return handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(schedule client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			schedule.Description.Schedule.Spec.CronExpressions = []string{ao.ScheduleTime}
			action := schedule.Description.Schedule.Action.(*client.ScheduleWorkflowAction)
			action.Args = []interface{}{
				workflow.OptimizerWorkflowInput{AutoOptimizeID: ao.ID.String()},
			}
			action.WorkflowExecutionTimeout = 1 * time.Hour
			return &client.ScheduleUpdate{
					Schedule: &schedule.Description.Schedule,
				},
				nil
		},
	})
}
