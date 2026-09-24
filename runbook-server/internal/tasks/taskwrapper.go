package tasks

import (
	"context" // Add this import
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// TaskWrapper adapts a Task to the Temporal activity signature.
type TaskWrapper struct {
	Task           types.Task
	TemporalClient client.Client
	Store          model.WorkflowStore
	Converter      converter.DataConverter
}

const (
	// Internal param keys for workflow metadata
	ParamTenantID        = "_tenant_id"
	ParamAccountID       = "_account_id"
	ParamWorkflowID      = "_workflow_id"
	ParamEventID         = "_event_id"
	ParamUserID          = "_user_id"
	ParamVars            = "_vars" // New: Current state of global vars map
	ParamDryRun          = "_dry_run"
	ParamWorkflowName    = "_workflow_name"
	ParamUserDisplayName = "_user_display_name"
	ParamExpectedOutput  = "_expected_output"
)

// Execute is the method registered with Temporal. It extracts workflow metadata from params,
// injects them into context, builds TaskContext, and calls the real Task.Execute.
func (tw *TaskWrapper) Execute(ctx context.Context, params map[string]any) (any, error) {
	if !activity.IsActivity(ctx) {
		return nil, fmt.Errorf("TaskWrapper.Execute must be called within a Temporal Activity context")
	}

	// Extract workflow metadata from params if present
	tenantID, _ := params[ParamTenantID].(string)
	accountID, _ := params[ParamAccountID].(string)
	workflowID, _ := params[ParamWorkflowID].(string)
	eventID, _ := params[ParamEventID].(string)
	userID, _ := params[ParamUserID].(string)
	isDryRun, _ := params[ParamDryRun].(bool)
	workflowName, _ := params[ParamWorkflowName].(string)
	userDisplayName, _ := params[ParamUserDisplayName].(string)

	var expectedOutput *model.TaskExpectedOutput
	if eoVal, ok := params[ParamExpectedOutput]; ok && eoVal != nil {
		switch eo := eoVal.(type) {
		case *model.TaskExpectedOutput:
			expectedOutput = eo
		case model.TaskExpectedOutput:
			expectedOutput = &eo
		case map[string]any:
			expectedOutput = model.ParseTaskExpectedOutput(eo)
		}
	}

	// Build TaskContext (includes logger, metadata, etc.)
	taskCtx := types.NewTemporalTaskContextFromActivity(ctx, tenantID, accountID, workflowID, eventID, userID, workflowName, userDisplayName, tw.TemporalClient, tw.Converter, tw.Store, isDryRun)

	// Remove internal metadata keys from params before passing to real task
	delete(params, ParamTenantID)
	delete(params, ParamAccountID)
	delete(params, ParamWorkflowID)
	delete(params, ParamEventID)
	delete(params, ParamUserID)
	delete(params, ParamVars) // Defensive: strip legacy ParamVars from in-flight (pre-fix) activity inputs; no longer attached by the executor
	delete(params, ParamDryRun)
	delete(params, ParamWorkflowName)
	delete(params, ParamUserDisplayName)
	delete(params, ParamExpectedOutput)

	if schema := tw.Task.InputSchema(); schema != nil {
		if err := schema.Process(params); err != nil {
			return nil, err
		}
	}

	rawResult, err := tw.Task.Execute(taskCtx, params)
	if err != nil {
		return nil, err
	}

	if expectedOutput != nil {
		start := time.Now()
		validatedResult, valErr := ValidateTaskOutput(rawResult, expectedOutput, tw.Task.GetName())
		duration := time.Since(start)
		if valErr != nil {
			taskCtx.GetLogger().Warn("Task output validation failed",
				"task_type", tw.Task.GetName(),
				"validation_code", valErr.Code,
				"reason", valErr.Reason,
				"retryable", valErr.IsRetryable(),
				"duration_ms", duration.Milliseconds(),
			)
			if span := trace.SpanFromContext(ctx); span.IsRecording() {
				span.SetAttributes(
					attribute.String("task.validation.status", "failed"),
					attribute.String("task.validation.code", valErr.Code),
					attribute.Bool("task.validation.retryable", valErr.IsRetryable()),
					attribute.Int64("task.validation.duration_ms", duration.Milliseconds()),
				)
			}
			return nil, valErr.ToTemporalError()
		}
		taskCtx.GetLogger().Debug("Task output validation succeeded",
			"task_type", tw.Task.GetName(),
			"duration_ms", duration.Milliseconds(),
		)
		if span := trace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(
				attribute.String("task.validation.status", "passed"),
				attribute.Int64("task.validation.duration_ms", duration.Milliseconds()),
			)
		}
		return validatedResult, nil
	}

	return rawResult, nil
}
