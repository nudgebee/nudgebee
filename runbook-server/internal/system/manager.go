package system

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
)

const (
	SystemTaskQueue = "runbook-system-tasks"
)

type SystemJobManager struct {
	client client.Client
	logger *slog.Logger
}

func NewSystemJobManager(c client.Client, logger *slog.Logger) *SystemJobManager {
	return &SystemJobManager{
		client: c,
		logger: logger,
	}
}

// EnsureSchedules makes sure all required system schedules exist and are up to date.
// It registers both hardcoded system schedules and cron triggers loaded from YAML.
func (m *SystemJobManager) EnsureSchedules(ctx context.Context, cronTriggers []CronTrigger) error {
	m.logger.Info("Ensuring system schedules are up to date...")

	// System cleanup schedule (hardcoded — internal to runbook-server)
	if err := m.ensureSchedule(ctx, scheduleConfig{
		scheduleID: "system-state-cleanup",
		workflowID: "system-state-cleanup-run",
		workflow:   SystemCleanupWorkflow,
		cron:       "*/15 * * * *",
		taskQueue:  SystemTaskQueue,
	}); err != nil {
		return err
	}

	// Cron triggers from YAML config
	for _, trigger := range cronTriggers {
		scheduleID := cronScheduleID(trigger.Name)
		workflowID := cronWorkflowID(trigger.Name)

		timeoutSeconds := 600 // default 10 min
		numRetries := 0
		retryIntervalSeconds := 0
		var catchupWindow time.Duration
		if trigger.RetryConf != nil {
			if trigger.RetryConf.TimeoutSeconds > 0 {
				timeoutSeconds = trigger.RetryConf.TimeoutSeconds
			}
			numRetries = trigger.RetryConf.NumRetries
			retryIntervalSeconds = trigger.RetryConf.RetryIntervalSeconds
			if trigger.RetryConf.ToleranceSeconds > 0 {
				catchupWindow = time.Duration(trigger.RetryConf.ToleranceSeconds) * time.Second
			}
		}

		input := CronWebhookWorkflowInput{
			Name:                 trigger.Name,
			Webhook:              trigger.Webhook,
			Payload:              trigger.Payload,
			Headers:              trigger.Headers,
			TimeoutSeconds:       timeoutSeconds,
			NumRetries:           numRetries,
			RetryIntervalSeconds: retryIntervalSeconds,
		}

		if err := m.ensureSchedule(ctx, scheduleConfig{
			scheduleID:    scheduleID,
			workflowID:    workflowID,
			workflow:      CronWebhookWorkflow,
			workflowArgs:  []any{input},
			cron:          trigger.Schedule,
			taskQueue:     SystemTaskQueue,
			catchupWindow: catchupWindow,
		}); err != nil {
			return err
		}
		m.logger.Info("Registered cron trigger", "name", trigger.Name, "schedule", trigger.Schedule, "webhook", trigger.Webhook)
	}

	return nil
}

type scheduleConfig struct {
	scheduleID    string
	workflowID    string
	workflow      any
	workflowArgs  []any
	cron          string
	taskQueue     string
	catchupWindow time.Duration
}

func (m *SystemJobManager) ensureSchedule(ctx context.Context, cfg scheduleConfig) error {
	scheduleClient := m.client.ScheduleClient()

	action := &client.ScheduleWorkflowAction{
		ID:        cfg.workflowID,
		Workflow:  cfg.workflow,
		Args:      cfg.workflowArgs,
		TaskQueue: cfg.taskQueue,
	}

	spec := client.ScheduleSpec{
		CronExpressions: []string{cfg.cron},
	}

	handle := scheduleClient.GetHandle(ctx, cfg.scheduleID)
	_, err := handle.Describe(ctx)
	if err == nil {
		m.logger.Info("Updating existing schedule", "scheduleID", cfg.scheduleID)
		err = handle.Update(ctx, client.ScheduleUpdateOptions{
			DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				input.Description.Schedule.Spec = &spec
				input.Description.Schedule.Action = action
				if cfg.catchupWindow > 0 {
					input.Description.Schedule.Policy.CatchupWindow = cfg.catchupWindow
				}
				return &client.ScheduleUpdate{
					Schedule: &input.Description.Schedule,
				}, nil
			},
		})
		if err != nil {
			return fmt.Errorf("failed to update schedule %s: %w", cfg.scheduleID, err)
		}
	} else {
		m.logger.Info("Creating schedule", "scheduleID", cfg.scheduleID)
		_, err = scheduleClient.Create(ctx, client.ScheduleOptions{
			ID:            cfg.scheduleID,
			Spec:          spec,
			Action:        action,
			CatchupWindow: cfg.catchupWindow,
		})
		if err != nil {
			return fmt.Errorf("failed to create schedule %s: %w", cfg.scheduleID, err)
		}
	}

	return nil
}

// cronScheduleID converts a trigger name like "SLO Execute" to "cron-slo-execute".
func cronScheduleID(name string) string {
	return "cron-" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
}

// cronWorkflowID converts a trigger name to a workflow run ID.
func cronWorkflowID(name string) string {
	return cronScheduleID(name) + "-run"
}
