package optimizer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/storage"
	"nudgebee/runbook/internal/tasks/k8s"
	"nudgebee/runbook/services/security"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

type Activities struct {
	Service Service
	Dao     *storage.OptimizerDao
}

func NewActivities(s Service, d *storage.OptimizerDao) *Activities {
	return &Activities{Service: s, Dao: d}
}

func (a *Activities) GenerateTasksActivity(ctx context.Context, autoOptimizeID string) ([]string, error) {
	aoID, err := uuid.Parse(autoOptimizeID)
	if err != nil {
		return nil, err
	}

	tasks, err := a.Service.GenerateTasks(ctx, aoID)
	if err != nil {
		return nil, err
	}

	ids := []string{}
	for _, t := range tasks {
		ids = append(ids, t.ID.String())
	}
	return ids, nil
}

func (a *Activities) CompleteAutoOptimizeActivity(ctx context.Context, autoOptimizeID string) error {
	aoID, err := uuid.Parse(autoOptimizeID)
	if err != nil {
		return err
	}
	return a.Service.CompleteAutoOptimize(ctx, aoID)
}

func (a *Activities) ExecuteTaskActivity(ctx context.Context, taskID string) error {
	tID, err := uuid.Parse(taskID)
	if err != nil {
		return err
	}

	task, err := a.Dao.GetAutoOptimizeTask(ctx, tID)
	if err != nil {
		return err
	}

	if task.Status == string(model.AutopilotTaskStatusSkipped) {
		slog.Info("Task already skipped, skipping execution", "task_id", taskID)
		return nil
	}

	sc := security.NewRequestContextForTenantAdmin(task.TenantID.String())

	// Create Temporal Logger wrapping slog if possible, else Default
	var tLogger log.Logger
	if sc.GetLogger() != nil {
		tLogger = log.NewStructuredLogger(sc.GetLogger())
	} else {
		tLogger = log.NewStructuredLogger(slog.Default())
	}

	taskCtx := &SimpleTaskContext{
		Context: ctx,
		Logger:  tLogger,
		ReqCtx:  sc,
		Account: task.AccountID.String(),
		DryRun:  task.Status == string(model.AutoOptimizeStatusDryrun),
		ExecID:  taskID,
	}

	var taskOutput any
	var taskErr error

	if task.Meta != nil {
		if task.RecommendationID != nil {
			task.Meta["recommendation_id"] = task.RecommendationID.String()
		}
		task.Meta["recommendation_task_id"] = task.ID.String()
		task.Meta["recommendation_optimizer_id"] = task.AutoPilotID.String()
	}

	if contains(task.Name, "Vertical Rightsize") {
		t := &k8s.VerticalRightsizeTask{}
		taskOutput, taskErr = t.Execute(taskCtx, task.Meta)
	} else if contains(task.Name, "Horizontal Rightsize") {
		t := &k8s.HorizontalRightsizeTask{}
		taskOutput, taskErr = t.Execute(taskCtx, task.Meta)
	} else if contains(task.Name, "PVC rightsize") {
		t := &k8s.PVRightsizeTask{}
		taskOutput, taskErr = t.Execute(taskCtx, task.Meta)
	} else if contains(task.Name, "Continuous Rightsize") {
		t := &k8s.ContinuousRightsizeTask{}
		taskOutput, taskErr = t.Execute(taskCtx, task.Meta)
	} else {
		return fmt.Errorf("unknown task type for task %s: %s", taskID, task.Name)
	}

	// Update DB Status
	now := time.Now().UTC()
	task.UpdatedAt = now
	if taskErr != nil {
		slog.Error("Task failed", "task_id", taskID, "error", taskErr)
		task.Status = string(model.AutopilotTaskStatusFailed)
		errMsg := taskErr.Error()
		task.Error = &errMsg
		task.Reason = &errMsg
	} else {
		slog.Info("Task completed", "task_id", taskID)
		task.Status = string(model.AutopilotTaskStatusComplete)
		if outMap, ok := taskOutput.(map[string]any); ok {
			if stat, ok := outMap["status"].(string); ok {
				switch stat {
				case "skipped":
					task.Status = string(model.AutopilotTaskStatusSkipped)
				case "dry_run":
					task.Status = string(model.AutoOptimizeStatusDryrun)
				}
			}
			if desc, ok := outMap["description"].(string); ok {
				task.Reason = &desc
			} else if reason, ok := outMap["reason"].(string); ok {
				task.Reason = &reason
			}

			// Extract resolution_id and save it
			if resIDStr, ok := outMap["resolution_id"].(string); ok && resIDStr != "" {
				if resID, err := uuid.Parse(resIDStr); err == nil {
					task.TaskID = &resID
					task.Attributes.ResolutionID = &resID
				}
			}

			// Store ticket URL in task attributes
			if ticketURL, ok := outMap["ticket_url"].(string); ok && ticketURL != "" {
				task.Attributes.TicketLink = &ticketURL
			}
		}

		if task.RecommendationID != nil {
			if err := a.Dao.UpdateRecommendationStatus(ctx, *task.RecommendationID, string(model.RecommendationStatusClosed)); err != nil {
				slog.Error("Failed to update recommendation status", "rec_id", *task.RecommendationID, "error", err)
			}
		}
	}

	if err := a.Dao.SaveAutoOptimizeTask(ctx, *task); err != nil {
		slog.Error("Failed to save task status", "task_id", taskID, "error", err)
		if taskErr == nil {
			return err
		}
	}

	return taskErr
}

type SimpleTaskContext struct {
	Context context.Context
	Logger  log.Logger
	ReqCtx  *security.RequestContext
	Account string
	DryRun  bool
	ExecID  string
}

func (c *SimpleTaskContext) GetContext() context.Context                    { return c.Context }
func (c *SimpleTaskContext) GetLogger() log.Logger                          { return c.Logger }
func (c *SimpleTaskContext) GetAccountID() string                           { return c.Account }
func (c *SimpleTaskContext) GetTenantID() string                            { return c.ReqCtx.GetSecurityContext().GetTenantId() }
func (c *SimpleTaskContext) GetUserID() string                              { return c.ReqCtx.GetSecurityContext().GetUserId() }
func (c *SimpleTaskContext) GetWorkflowID() string                          { return "" }
func (c *SimpleTaskContext) GetWorkflowName() string                        { return "" }
func (c *SimpleTaskContext) GetUserDisplayName() string                     { return "" }
func (c *SimpleTaskContext) GetWorkflowRunID() string                       { return c.ExecID }
func (c *SimpleTaskContext) GetTaskID() string                              { return c.ExecID }
func (c *SimpleTaskContext) GetNewRequestContext() *security.RequestContext { return c.ReqCtx }
func (c *SimpleTaskContext) GetNewRequestContextForAccount(accountID string) *security.RequestContext {
	return c.ReqCtx
}
func (c *SimpleTaskContext) IsDryRun() bool {
	return c.DryRun
}
func (c *SimpleTaskContext) SetOutput(name string, value any) {}
func (c *SimpleTaskContext) GetInput(name string) any         { return nil }
func (c *SimpleTaskContext) GetTemporalClient() client.Client { return nil }
func (c *SimpleTaskContext) GetStore() model.WorkflowStore    { return nil }
func (c *SimpleTaskContext) GetDataConverter() converter.DataConverter {
	return converter.GetDefaultDataConverter()
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
