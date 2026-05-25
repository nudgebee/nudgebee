package core

import (
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"time"

	"go.temporal.io/sdk/activity"
)

// WaitTask implements a task that pauses execution for a specified duration.
type WaitTask struct{}

func (t *WaitTask) GetName() string {
	return "core.wait"
}

// GetDescription returns a brief description of the task.
func (t *WaitTask) GetDescription() string {
	return "Wait for a specified amount of time before continuing."
}

// GetDisplayName returns a human-readable name for the task.
func (t *WaitTask) GetDisplayName() string {
	return "Wait"
}

func (t *WaitTask) Execute(taskCtx types.TaskContext, params map[string]any) (result any, err error) {
	if !activity.IsActivity(taskCtx.GetContext()) {
		return nil, fmt.Errorf("core.wait task is not allowed to execute directly. It must be part of a workflow")
	}

	durationStr, ok := params["duration"].(string)
	if !ok || durationStr == "" {
		return nil, fmt.Errorf("missing or invalid 'duration' parameter for core.wait task")
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse duration '%s': %w", durationStr, err)
	}

	taskCtx.GetLogger().Info("Pausing for duration", "duration", duration)

	// In an activity, time.Sleep is safe. In a workflow, workflow.Sleep should be used.
	// Since this is designed as an Activity, time.Sleep is appropriate.
	time.Sleep(duration)

	taskCtx.GetLogger().Info("Resuming after duration", "duration", duration)

	return map[string]any{
		"message":  fmt.Sprintf("Waited for %s", duration),
		"duration": durationStr,
	}, nil
}

func (t *WaitTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"duration": {
				Type:        "string",
				Description: "The duration to wait (e.g., '10s', '5m', '1h').",
				Required:    true,
			},
		},
	}
}

func (t *WaitTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"message": {
				Type:        "string",
				Description: "Confirmation message.",
				Required:    true,
			},
			"duration": {
				Type:        "string",
				Description: "The duration that was waited.",
				Required:    true,
			},
		},
	}
}
