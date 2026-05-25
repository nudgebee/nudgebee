package system

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	CronWebhookActivityName = "CronWebhookActivity"
)

// CronWebhookWorkflowInput contains the parameters for a cron webhook execution.
type CronWebhookWorkflowInput struct {
	Name                 string         `json:"name"`
	Webhook              string         `json:"webhook"`
	Payload              map[string]any `json:"payload"`
	Headers              []CronHeader   `json:"headers"`
	TimeoutSeconds       int            `json:"timeout_seconds"`
	NumRetries           int            `json:"num_retries"`
	RetryIntervalSeconds int            `json:"retry_interval_seconds"`
}

// CronWebhookWorkflow executes a webhook call as a Temporal workflow.
// Returns the parsed JSON response body so it appears as the workflow result in Temporal UI.
func CronWebhookWorkflow(ctx workflow.Context, input CronWebhookWorkflowInput) (map[string]any, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting cron webhook workflow", "name", input.Name)

	timeout := 10 * time.Minute
	if input.TimeoutSeconds > 0 {
		timeout = time.Duration(input.TimeoutSeconds) * time.Second
	}

	// MaximumAttempts = num_retries + 1 (first attempt + retries). Defaults to 1 (no retry).
	maxAttempts := int32(input.NumRetries + 1)
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	retryInterval := 10 * time.Second
	if input.RetryIntervalSeconds > 0 {
		retryInterval = time.Duration(input.RetryIntervalSeconds) * time.Second
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval: retryInterval,
			MaximumAttempts: maxAttempts,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result map[string]any
	err := workflow.ExecuteActivity(ctx, CronWebhookActivityName, input).Get(ctx, &result)
	if err != nil {
		logger.Error("Cron webhook activity failed", "name", input.Name, "error", err)
		return nil, err
	}

	logger.Info("Cron webhook workflow completed", "name", input.Name, "response", result)
	return result, nil
}
