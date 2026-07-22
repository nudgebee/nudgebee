package templatesource

import (
	"context"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// ScheduleID is the Temporal Schedule that drives periodic template sync.
	ScheduleID = "template-sync"
	// scheduleWorkflowID is the per-fire workflow id prefix.
	scheduleWorkflowID = "template-sync-scheduled"
	// TemplateSyncActivityName is the registered activity name.
	TemplateSyncActivityName = "TemplateSyncActivity"
)

// TemplateSyncWorkflow runs one reconcile via the TemplateSyncActivity. Kept trivial
// (one activity) — all the work and IO happen in the activity.
func TemplateSyncWorkflow(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	return workflow.ExecuteActivity(ctx, TemplateSyncActivityName).Get(ctx, nil)
}

// SyncActivities adapts the Syncer to a Temporal activity.
type SyncActivities struct {
	syncer *Syncer
}

func NewSyncActivities(s *Syncer) *SyncActivities {
	return &SyncActivities{syncer: s}
}

// TemplateSyncActivity performs one reconcile pass.
func (a *SyncActivities) TemplateSyncActivity(ctx context.Context) error {
	_, err := a.syncer.SyncOnce(ctx)
	return err
}

// EnsureSchedule creates or updates the Temporal Schedule that triggers
// TemplateSyncWorkflow on the given cron expression and task queue. Idempotent:
// describe → update if present, else create (mirrors internal/system/manager.go).
func EnsureSchedule(ctx context.Context, c client.Client, cron, taskQueue string) error {
	sc := c.ScheduleClient()
	action := &client.ScheduleWorkflowAction{
		ID:        scheduleWorkflowID,
		Workflow:  TemplateSyncWorkflow,
		TaskQueue: taskQueue,
	}
	spec := client.ScheduleSpec{CronExpressions: []string{cron}}

	handle := sc.GetHandle(ctx, ScheduleID)
	if _, err := handle.Describe(ctx); err == nil {
		return handle.Update(ctx, client.ScheduleUpdateOptions{
			DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
				in.Description.Schedule.Spec = &spec
				in.Description.Schedule.Action = action
				return &client.ScheduleUpdate{Schedule: &in.Description.Schedule}, nil
			},
		})
	}

	_, err := sc.Create(ctx, client.ScheduleOptions{
		ID:     ScheduleID,
		Spec:   spec,
		Action: action,
	})
	return err
}
