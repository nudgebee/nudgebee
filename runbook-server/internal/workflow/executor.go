package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/internal/tasks/types"
	configSvc "nudgebee/runbook/services/config"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	temporalLog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type WorkflowExecutor struct {
	temporalClient client.Client
	temporalWorker worker.Worker
	dataConverter  converter.DataConverter
	workflowStore  model.WorkflowStore
}

// buildRoutingDef converts the activity result returned by a case-routing task
// (e.g. core.switch) back into the WorkflowDefinition the executor needs to
// run the matched branch as an inline coroutine. The result is read as
// map[string]any because Temporal serialises through JSON — a typed struct
// would round-trip to the same shape.
//
// Expected keys:
//   - selected_case  string  (matched case value or "default")
//   - routed_to      []any   (task IDs to run; resolved against fullWorkflowTaskDefinitions)
//   - embedded_tasks []any   (legacy mode 2/3: full task bodies; hydrated directly)
func buildRoutingDef(raw any) *model.WorkflowDefinition {
	def := &model.WorkflowDefinition{Inputs: []model.Input{}}
	m, ok := raw.(map[string]any)
	if !ok {
		return def
	}
	if sc, ok := m["selected_case"].(string); ok {
		def.Output = map[string]any{"selected_case": sc}
	}
	// Legacy switch modes embed full task definitions inline. JSON-round-trip
	// them into []model.Task so hydration matches the inline path.
	if embeddedRaw, ok := m["embedded_tasks"].([]any); ok && len(embeddedRaw) > 0 {
		if bytes, err := json.Marshal(embeddedRaw); err == nil {
			_ = json.Unmarshal(bytes, &def.Tasks)
		}
		return def
	}
	if routed, ok := m["routed_to"].([]any); ok {
		for _, id := range routed {
			if s, ok := id.(string); ok {
				def.Tasks = append(def.Tasks, model.Task{ID: s})
			}
		}
	}
	return def
}

// getUserDisplayName returns the display name to attribute the workflow run to.
// Prefers the actual triggerer (manual / retrigger), falls back to the last
// updater for scheduled/webhook/event-rule triggers.
func getUserDisplayName(wf *model.Workflow) string {
	if wf.TriggeredByUser != nil && wf.TriggeredByUser.DisplayName != "" {
		return wf.TriggeredByUser.DisplayName
	}
	if wf.UpdatedByUser != nil {
		return wf.UpdatedByUser.DisplayName
	}
	return ""
}

const (
	Internal_UpdateLastExecutionStatusActivity   = "Internal_UpdateLastExecutionStatusActivity"
	FetchWorkflowDefinitionActivity              = "FetchWorkflowDefinitionActivity"
	FetchWorkflowStateActivity                   = "FetchWorkflowStateActivity"
	UpdateWorkflowStateActivity                  = "UpdateWorkflowStateActivity"
	FetchWorkflowConfigsActivity                 = "FetchWorkflowConfigsActivity"
	Internal_UpdateEventResolutionStatusActivity = "Internal_UpdateEventResolutionStatusActivity"
)

func NewWorkflowExecutor(store model.WorkflowStore, configService *configSvc.Service, c client.Client, dc converter.DataConverter) (*WorkflowExecutor, error) {
	w := worker.New(c, config.Config.RunbookServerTemporalQueue, worker.Options{
		DisableRegistrationAliasing: true,
	})

	// Register workflow-specific activities
	workflowActivities := &WorkflowActivities{Store: store, ConfigService: configService}
	w.RegisterActivityWithOptions(workflowActivities.Internal_UpdateLastExecutionStatus, activity.RegisterOptions{
		Name: Internal_UpdateLastExecutionStatusActivity,
	})
	w.RegisterActivityWithOptions(workflowActivities.FetchWorkflowDefinitionActivity, activity.RegisterOptions{
		Name: FetchWorkflowDefinitionActivity,
	})
	w.RegisterActivityWithOptions(workflowActivities.FetchWorkflowStateActivity, activity.RegisterOptions{
		Name: FetchWorkflowStateActivity,
	})
	w.RegisterActivityWithOptions(workflowActivities.UpdateWorkflowStateActivity, activity.RegisterOptions{
		Name: UpdateWorkflowStateActivity,
	})
	w.RegisterActivityWithOptions(workflowActivities.FetchWorkflowConfigsActivity, activity.RegisterOptions{
		Name: FetchWorkflowConfigsActivity,
	})
	w.RegisterActivityWithOptions(workflowActivities.Internal_UpdateEventResolutionStatus, activity.RegisterOptions{
		Name: Internal_UpdateEventResolutionStatusActivity,
	})

	executor := &WorkflowExecutor{
		temporalClient: c,
		temporalWorker: w,
		dataConverter:  dc,
		workflowStore:  store,
	}

	// Register workflow types
	w.RegisterWorkflow(executor.ExecuteWorkflowInternal)

	return executor, nil
}

func (e *WorkflowExecutor) GetClient() client.Client {
	return e.temporalClient
}

func (e *WorkflowExecutor) GetWorker() worker.Worker {
	return e.temporalWorker
}

func (e *WorkflowExecutor) GetConverter() converter.DataConverter {
	return e.dataConverter
}

func (e *WorkflowExecutor) GetStore() model.WorkflowStore {
	return e.workflowStore
}

func (e *WorkflowExecutor) Start() error {
	err := e.temporalWorker.Run(worker.InterruptCh())
	if err != nil {
		return fmt.Errorf("unable to start Worker: %w", err)
	}
	return nil
}

func (e *WorkflowExecutor) Stop() {
	if e.temporalClient != nil {
		e.temporalClient.Close()
	}
	if e.temporalWorker != nil {
		e.temporalWorker.Stop()
	}
}

func evaluateTaskCondition(ctx workflow.Context, task model.Task, tplCtx *TemplateContext) (bool, error) {
	if task.If == "" {
		return true, nil
	}
	renderedIf, err := Render(task.If, tplCtx)
	if err != nil {
		logger := workflow.GetLogger(ctx)
		logger.Error("Failed to render 'if' expression", "taskID", task.ID, "if", task.If, "error", err)
		return false, fmt.Errorf("failed to render 'if' expression for task %s: %w", task.ID, err)
	}
	return strings.ToLower(strings.TrimSpace(renderedIf)) == "true", nil
}

func createActivityOptionsForTask(task model.Task, wfDef *model.WorkflowDefinition) workflow.ActivityOptions {
	// Start with workflow-level defaults if present
	timeout := 10 * time.Minute
	retryPolicy := &temporal.RetryPolicy{
		InitialInterval:    time.Second,
		BackoffCoefficient: 2.0,
		MaximumInterval:    time.Minute,
		MaximumAttempts:    1,
	}
	if wfDef != nil {
		if wfDef.Timeout != "" {
			if t, err := time.ParseDuration(wfDef.Timeout); err == nil {
				timeout = t
			}
		}
		if wfDef.RetryPolicy != nil {
			p := wfDef.RetryPolicy
			if p.InitialInterval != "" {
				if d, err := time.ParseDuration(p.InitialInterval); err == nil {
					retryPolicy.InitialInterval = d
				}
			}
			if p.BackoffCoefficient > 0 {
				retryPolicy.BackoffCoefficient = p.BackoffCoefficient
			}
			if p.MaximumInterval != "" {
				if d, err := time.ParseDuration(p.MaximumInterval); err == nil {
					retryPolicy.MaximumInterval = d
				}
			}
			if p.MaximumAttempts > 0 {
				retryPolicy.MaximumAttempts = p.MaximumAttempts
			}
			if len(p.NonRetryableErrorTypes) > 0 {
				retryPolicy.NonRetryableErrorTypes = p.NonRetryableErrorTypes
			}
		}
	}
	// Task-level overrides
	if task.Timeout != "" {
		if t, err := time.ParseDuration(task.Timeout); err == nil {
			timeout = t
		}
	}
	if task.FailurePolicy != nil && task.FailurePolicy.Retry != nil {
		p := task.FailurePolicy.Retry
		if p.InitialInterval != "" {
			if d, err := time.ParseDuration(p.InitialInterval); err == nil {
				retryPolicy.InitialInterval = d
			}
		}
		if p.BackoffCoefficient > 0 {
			retryPolicy.BackoffCoefficient = p.BackoffCoefficient
		}
		if p.MaximumInterval != "" {
			if d, err := time.ParseDuration(p.MaximumInterval); err == nil {
				retryPolicy.MaximumInterval = d
			}
		}
		if p.MaximumAttempts > 0 {
			retryPolicy.MaximumAttempts = p.MaximumAttempts
		}
		if len(p.NonRetryableErrorTypes) > 0 {
			retryPolicy.NonRetryableErrorTypes = p.NonRetryableErrorTypes
		}
	}
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy:         retryPolicy,
	}
}

func createActivityCtxForAction(ctx workflow.Context) workflow.Context {
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second, // Short timeout for status update
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	}
	ctxWithOptions := workflow.WithActivityOptions(ctx, activityOptions)
	return ctxWithOptions
}

func handleTaskCompletion(ctx workflow.Context, workflowID string, taskID string, future workflow.Future, globalContext *TemplateContext, taskMap map[string]model.Task, isDryRun bool) error {
	logger := workflow.GetLogger(ctx)
	var result any
	err := future.Get(ctx, &result)

	// New logic to handle JSON string results from child workflows
	if resultStr, ok := result.(string); ok {
		var resultMap map[string]any
		if err := json.Unmarshal([]byte(resultStr), &resultMap); err == nil {
			result = resultMap
		}
	}

	currentTask, ok := taskMap[taskID]
	if !ok {
		logger.Error("Could not find task definition for completed task", "taskID", taskID)
		// If we can't find the task, we can't process hooks, so we just return the original error if it exists.
		return err
	}

	// New logic to handle JSON string results from child workflows
	if resultStr, ok := result.(string); ok {
		var resultMap map[string]any
		if err := json.Unmarshal([]byte(resultStr), &resultMap); err == nil {
			result = resultMap
		}
	}

	if err != nil {
		logger.Error("Activity failed", "taskID", taskID, "error", err, "params", globalContext.Tasks[taskID])
		// find task to run on_failure
		if currentTask.Hooks != nil && len(currentTask.Hooks.Failure) > 0 {
			onFailureCtx := createActivityCtxForAction(ctx)
			hookErr := executeSimpleActions(onFailureCtx, currentTask.Hooks.Failure, globalContext)
			if hookErr != nil {
				logger.Error("OnFailure hook failed", "taskID", taskID, "error", hookErr)
				return hookErr
			}
		}

		// Check failure policy action
		if currentTask.FailurePolicy != nil && currentTask.FailurePolicy.Action == "continue" {
			logger.Warn("Task failed but continue_on_error is set", "taskID", taskID, "error", err)
			// Update context with FAILED status but continue workflow
			globalContext.Tasks[taskID] = map[string]any{
				"status": model.TaskStatusFailed,
				"error":  err.Error(),
			}
			return nil
		}

		return err
	}

	logger.Info("Task completed", "taskID", taskID, "result", result) // ENABLED LOGGING
	globalContext.Tasks[taskID] = map[string]any{
		"status": string(model.TaskStatusCompleted),
		"output": result,
	}

	// Process task.SetVars and merge into globalContext.Vars
	if len(currentTask.SetVars) > 0 {
		// Create a temporary context for rendering self-referential outputs.
		selfRenderContext := globalContext.Clone()
		selfRenderContext.Self = globalContext.Tasks[taskID]

		for outputName, outputTemplate := range currentTask.SetVars {
			processedVal, err := ProcessValue(outputTemplate, selfRenderContext)
			if err != nil {
				logger.Error("Failed to process task output template", "taskID", taskID, "outputName", outputName, "template", outputTemplate, "error", err)
				// Decide how to handle this error: continue, log, or return.
				// For now, we'll log and continue to avoid failing the workflow if an output template is malformed.
			} else {
				var finalValue any
				if valMap, ok := processedVal.(map[string]any); ok {
					if v, hasValue := valMap["value"]; hasValue {
						finalValue = v
					} else {
						finalValue = processedVal
					}
				} else {
					finalValue = processedVal
				}
				globalContext.Vars[outputName] = finalValue
			}
		}
	}

	// Process task.SetState and update persistent state
	if len(currentTask.SetState) > 0 {
		selfRenderContext := globalContext.Clone()
		selfRenderContext.Self = globalContext.Tasks[taskID]
		stateUpdates := make(map[string]model.StateUpdateDTO)

		for key, valueTemplate := range currentTask.SetState {
			// Render the key to support dynamic keys (e.g. "github_run_{{ job_id }}")
			renderedKey, err := Render(key, selfRenderContext)
			if err != nil {
				logger.Error("Failed to render task state key", "taskID", taskID, "key", key, "error", err)
				// Fallback to original key if rendering fails, but this might lead to unexpected behavior if dynamic keys are intended.
				// For now, we log and proceed with the literal key.
				renderedKey = key
			}

			processedVal, err := ProcessValue(valueTemplate, selfRenderContext)
			if err != nil {
				logger.Error("Failed to process task state template", "taskID", taskID, "key", renderedKey, "template", valueTemplate, "error", err)
				continue
			}

			var dto model.StateUpdateDTO
			if valMap, ok := processedVal.(map[string]any); ok {
				// Check if it's a polymorphic map with "value" key
				if v, hasValue := valMap["value"]; hasValue {
					dto.Value = v
					if ttl, hasTTL := valMap["ttl"]; hasTTL {
						if ttlStr, ok := ttl.(string); ok {
							dto.TTL = ttlStr
						}
					}
				} else {
					// It's just a map value
					dto.Value = processedVal
				}
			} else {
				// Simple value
				dto.Value = processedVal
			}

			stateUpdates[renderedKey] = dto
			// Update local context immediately so subsequent tasks can see it
			// For local context, we just store the value, ignoring TTL
			globalContext.State[renderedKey] = dto.Value
		}

		if len(stateUpdates) > 0 { // Execute activity to persist state
			// Skip persistent state update for inline/ephemeral workflows or dry-runs
			if strings.HasPrefix(workflowID, "inline-") || isDryRun {
				logger.Warn("Skipping persistent state update; persistent state is not supported for ephemeral or dry-run workflows", "workflowID", workflowID)
			} else {
				ao := workflow.ActivityOptions{
					StartToCloseTimeout: 10 * time.Second,
					RetryPolicy: &temporal.RetryPolicy{
						InitialInterval:    1 * time.Second,
						BackoffCoefficient: 2.0,
						MaximumInterval:    10 * time.Second,
						MaximumAttempts:    3,
					},
				}
				ctxWithAO := workflow.WithActivityOptions(ctx, ao)

				executionID := workflow.GetInfo(ctx).WorkflowExecution.RunID

				// We fire and forget the activity future here because we don't want to block strict execution flow
				// for state persistence if it's not critical for the *immediate* next step (though ideally it should be consistent).
				// However, to ensure consistency, we SHOULD wait.
				if err := workflow.ExecuteActivity(ctxWithAO, UpdateWorkflowStateActivity, workflowID, stateUpdates, executionID, taskID).Get(ctxWithAO, nil); err != nil {
					logger.Error("Failed to persist workflow state", "taskID", taskID, "error", err)
					// Decide if this is fatal. For now, log error.
				}
			}
		}
	}

	// find task to run on_success
	if currentTask.Hooks != nil && len(currentTask.Hooks.Success) > 0 {
		onSuccessCtx := createActivityCtxForAction(ctx)
		hookErr := executeSimpleActions(onSuccessCtx, currentTask.Hooks.Success, globalContext)
		if hookErr != nil {
			logger.Error("OnSuccess hook failed", "taskID", taskID, "error", hookErr)
			return hookErr
		}
	}

	// find task to run finally
	if currentTask.Hooks != nil && len(currentTask.Hooks.Always) > 0 {
		onFinalCtx := createActivityCtxForAction(ctx)
		hookErr := executeSimpleActions(onFinalCtx, currentTask.Hooks.Always, globalContext)
		if hookErr != nil {
			logger.Error("Finally hook failed", "taskID", taskID, "error", hookErr)
			return hookErr
		}
	}
	return nil
}

func (e *WorkflowExecutor) ExecuteWorkflowInternal(ctx workflow.Context, wf *model.Workflow, inputs map[string]any) (result string, err error) {
	if inputs == nil {
		inputs = make(map[string]any)
	}
	logger := workflow.GetLogger(ctx)
	info := workflow.GetInfo(ctx)
	isChildWorkflow := info.ParentWorkflowExecution != nil

	logger.Info("Starting workflow", "workflowId", wf.ID, "tenantID", wf.TenantID, "accountID", wf.AccountID, "isChild", isChildWorkflow)

	// Explicitly upsert critical system search attributes for visibility
	var systemSearchAttrs []temporal.SearchAttributeUpdate
	systemSearchAttrs = append(systemSearchAttrs, temporal.NewSearchAttributeKeyKeyword(model.SearchAttrTenantID).ValueSet(wf.TenantID))
	systemSearchAttrs = append(systemSearchAttrs, temporal.NewSearchAttributeKeyKeyword(model.SearchAttrAccountID).ValueSet(wf.AccountID))
	systemSearchAttrs = append(systemSearchAttrs, temporal.NewSearchAttributeKeyKeyword(model.SearchAttrWorkflowID).ValueSet(wf.ID))

	// Add workflow trigger type if available from info.SearchAttributes
	searchAttributes := workflow.GetTypedSearchAttributes(ctx)
	if searchAttributes.Size() > 0 {
		if triggerType, ok := searchAttributes.GetKeyword(temporal.NewSearchAttributeKeyKeyword(model.SearchAttrWorkflowTrigger)); ok {
			systemSearchAttrs = append(systemSearchAttrs, temporal.NewSearchAttributeKeyKeyword(model.SearchAttrWorkflowTrigger).ValueSet(triggerType))
		}
	}

	if len(systemSearchAttrs) > 0 {
		if err := workflow.UpsertTypedSearchAttributes(ctx, systemSearchAttrs...); err != nil {
			logger.Error("Failed to upsert system search attributes", "error", err)
		}
	}
	pendingApprovalTokens := make(map[string]string)
	approvalTokenSignalChan := workflow.GetSignalChannel(ctx, "approval-token")

	// Panic recovery for the workflow function
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Panic recovered in workflow execution", "panic", r)
			panicMessage := fmt.Sprintf("%v", r)
			// Pass the panic message to the final status update
			updateFinalWorkflowStatusAndExecuteHooks(ctx, wf, inputs, logger, temporal.NewApplicationError("workflow panic", "panic", panicMessage))
			// Set the error for the workflow function to return, causing the Temporal workflow to fail.
			err = temporal.NewApplicationError("workflow panic: non-deterministic", "panic", panicMessage)
		}
	}()

	// Set up query handler for approval tokens
	err = workflow.SetQueryHandler(ctx, "getApprovalToken", func(taskID string) (string, error) {
		if token, ok := pendingApprovalTokens[taskID]; ok {
			return token, nil
		}
		return "", fmt.Errorf("no pending approval found for task ID: %s", taskID)
	})
	if err != nil {
		logger.Error("Failed to set query handler", "error", err)
		return "", err
	}

	// Update workflow status to RUNNING with retry logic, only for parent workflows
	if !isChildWorkflow && !wf.DryRun {
		updateInprogressWorkflowStatus(ctx, wf, logger)
	}

	// Upsert static Search Attributes from wf.Tags
	if len(wf.Tags) > 0 {
		logger.Info("Upserting static search attributes", "tags", wf.Tags)
		var tags []string
		for k, v := range wf.Tags {
			if strVal, ok := v.(string); ok {
				tags = append(tags, fmt.Sprintf("%s:%s", k, strVal))
			}
		}
		if len(tags) > 0 {
			key := temporal.NewSearchAttributeKeyKeywordList(model.SearchAttrExecutionTags)
			if err := workflow.UpsertTypedSearchAttributes(ctx, key.ValueSet(tags)); err != nil {
				logger.Error("Failed to upsert static search attributes", "error", err)
			}
		}
	}

	// Evaluate and upsert Execution Tags
	if len(wf.Definition.SetExecutionTags) > 0 {
		initialTplCtx := NewTemplateContext(wf.Definition.Inputs, inputs)
		for _, input := range wf.Definition.Inputs {
			initialTplCtx.Inputs[input.ID] = input.Default
		}

		var tags []string
		for _, tagTemplate := range wf.Definition.SetExecutionTags {
			renderedTag, err := Render(tagTemplate, initialTplCtx)
			if err != nil {
				logger.Error("Failed to render execution tag", "template", tagTemplate, "error", err)
				continue
			}
			tags = append(tags, renderedTag)
		}

		if len(tags) > 0 {
			taggedSearchAttr := temporal.NewSearchAttributeKeyKeywordList(model.SearchAttrExecutionTags)
			if err := workflow.UpsertTypedSearchAttributes(ctx, taggedSearchAttr.ValueSet(tags)); err != nil {
				logger.Error("Failed to upsert search attributes for tags", "error", err)
			}
		}
	}

	// Auto-tag Event details if present
	var eventData any
	for _, input := range wf.Definition.Inputs {
		if input.ID == "event" {
			eventData = input.Default
			break
		}
	}
	if val, ok := inputs["event"]; ok {
		eventData = val
	}

	if eventData != nil {
		if eventMap, ok := eventData.(map[string]any); ok {
			eventAttrs := []temporal.SearchAttributeUpdate{}
			if val, ok := eventMap["event_type"].(string); ok {
				attr := temporal.NewSearchAttributeKeyKeyword(model.SearchAttrEventType)
				eventAttrs = append(eventAttrs, attr.ValueSet(val))
			}
			if val, ok := eventMap["id"].(string); ok {
				attr := temporal.NewSearchAttributeKeyKeyword(model.SearchAttrEventID)
				eventAttrs = append(eventAttrs, attr.ValueSet(val))
			} else if val, ok := eventMap["event_id"].(string); ok {
				attr := temporal.NewSearchAttributeKeyKeyword(model.SearchAttrEventID)
				eventAttrs = append(eventAttrs, attr.ValueSet(val))
			}

			if len(eventAttrs) > 0 {
				logger.Info("Upserting system event attributes", "attributes", eventAttrs)
				if err := workflow.UpsertTypedSearchAttributes(ctx, eventAttrs...); err != nil {
					logger.Error("Failed to upsert system event attributes", "error", err)
				}
			}
		}
	}

	// Fetch persistent workflow state
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctxWithAO := workflow.WithActivityOptions(ctx, ao)
	var workflowState map[string]any

	// Skip persistent state fetch for inline/ephemeral workflows (non-UUID IDs) or dry-runs
	if !strings.HasPrefix(wf.ID, "inline-") && !wf.DryRun {
		if err := workflow.ExecuteActivity(ctxWithAO, FetchWorkflowStateActivity, wf.ID).Get(ctxWithAO, &workflowState); err != nil {
			logger.Error("Failed to fetch workflow state", "workflowId", wf.ID, "error", err)
			// We might choose to continue with empty state or fail. failing seems safer for consistency.
			return "", fmt.Errorf("failed to fetch workflow state: %w", err)
		}
	} else {
		workflowState = make(map[string]any)
	}

	var configsResponse FetchConfigsResponse
	if err := workflow.ExecuteActivity(ctxWithAO, FetchWorkflowConfigsActivity, wf.TenantID, wf.AccountID).Get(ctxWithAO, &configsResponse); err != nil {
		logger.Error("Failed to fetch workflow configs", "workflowId", wf.ID, "error", err)
		return "", fmt.Errorf("failed to fetch workflow configs: %w", err)
	}

	// --- Inject Standard Context Variables ---
	currentExecTime := workflow.Now(ctx)
	inputs["workflow_execution_time"] = currentExecTime.Format(time.RFC3339)
	inputs["workflow_execution_id"] = info.WorkflowExecution.RunID
	inputs["workflow_id"] = wf.ID
	inputs["workflow_name"] = wf.Name

	// Determine Scheduled Time (Logical Time)
	var workflowScheduledTime time.Time
	if searchAttributes.Size() > 0 {
		if val, ok := searchAttributes.GetTime(temporal.NewSearchAttributeKeyTime("TemporalScheduledStartTime")); ok {
			workflowScheduledTime = val
		}
	}
	if workflowScheduledTime.IsZero() {
		workflowScheduledTime = currentExecTime
	}
	inputs["workflow_scheduled_time"] = workflowScheduledTime.Format(time.RFC3339)

	// Inject last_execution_time from state
	if workflowState != nil {
		if val, ok := workflowState["workflow_last_execution_time"]; ok {
			inputs["workflow_last_execution_time"] = val
		}
	}

	// Initialize Template Context
	templateContext := NewTemplateContext(wf.Definition.Inputs, inputs)
	if workflowState != nil {
		templateContext.State = workflowState
	}
	if configsResponse.Configs != nil {
		templateContext.Configs = configsResponse.Configs
	}
	for k, v := range configsResponse.Secrets {
		templateContext.Secrets[k] = v
	}

	// Run Tasks using the recursive loop function
	taskRegistry := tasks.NewInitializedTaskRegistry()

	var executionTrace []model.TaskExecutionDetails
	err = workflow.SetQueryHandler(ctx, "getDryRunTrace", func() ([]model.TaskExecutionDetails, error) {
		return executionTrace, nil
	})
	if err != nil {
		logger.Error("Failed to set query handler for dry run trace", "error", err)
	}

	_, executionTrace, workflowError := processTaskLoop(ctx, wf.Definition.Tasks, templateContext, logger, pendingApprovalTokens, approvalTokenSignalChan, workflow.GetSignalChannel(ctx, "update-workflow-inputs"), wf, info.WorkflowExecution.ID, taskRegistry, e.GetClient(), e.GetConverter(), e.GetStore(), e.ExecuteWorkflowInternal)

	if workflowError != nil {
		logger.Info("Workflow execution failed because of error", "workflowId", wf.ID, "error", workflowError)
		if !isChildWorkflow {
			updateFinalWorkflowStatusAndExecuteHooks(ctx, wf, inputs, logger, workflowError)
		}
		return "", workflowError
	}

	var workflowResult any

	// Evaluate workflow output if defined
	if wf != nil && len(wf.Definition.Output) > 0 {
		logger.Info("Rendering workflow output", "outputTemplate", wf.Definition.Output)
		if templateContext != nil {
			renderedOutput, err := templateContext.RenderMap(wf.Definition.Output)
			if err != nil {
				logger.Error("Failed to render workflow output", "error", err)
			} else {
				workflowResult = renderedOutput
			}
		} else {
			logger.Warn("globalContext is nil, cannot render workflow output")
		}
	}

	if !isChildWorkflow {
		// --- Persist workflow_last_execution_time state (Success Only) ---
		if !wf.DryRun {
			stateUpdates := map[string]model.StateUpdateDTO{
				"workflow_last_execution_time": {Value: currentExecTime.Format(time.RFC3339)},
			}
			aoState := workflow.ActivityOptions{
				StartToCloseTimeout: 5 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					MaximumAttempts: 3,
				},
			}
			ctxState := workflow.WithActivityOptions(ctx, aoState)
			// Use a dummy task ID for system update
			if err := workflow.ExecuteActivity(ctxState, UpdateWorkflowStateActivity, wf.ID, stateUpdates, info.WorkflowExecution.RunID, "system-final-update").Get(ctxState, nil); err != nil {
				logger.Warn("Failed to persist workflow_last_execution_time", "error", err)
			}
		}

		// --- Re-evaluate and Upsert Search Attributes at End ---
		if templateContext != nil {
			var tags []string
			if len(wf.Definition.SetExecutionTags) > 0 {
				for _, tagTemplate := range wf.Definition.SetExecutionTags {
					renderedTag, err := Render(tagTemplate, templateContext)
					if err != nil {
						logger.Error("Failed to render execution tag (final pass)", "template", tagTemplate, "error", err)
						continue
					}
					tags = append(tags, renderedTag)
				}
			}

			if eventVal, ok := templateContext.Inputs["event"]; ok {
				if eventMap, ok := eventVal.(map[string]any); ok {
					if val, ok := eventMap["source"].(string); ok {
						tags = append(tags, fmt.Sprintf("%s:%s", "nb_event_source", val))
					}
					if val, ok := eventMap["id"].(string); ok {
						tags = append(tags, fmt.Sprintf("%s:%s", "nb_event_id", val))
					} else if val, ok := eventMap["event_id"].(string); ok {
						tags = append(tags, fmt.Sprintf("%s:%s", "nb_event_id", val))
					}
				}
			}

			if len(tags) > 0 {
				key := temporal.NewSearchAttributeKeyKeywordList(model.SearchAttrExecutionTags)
				logger.Info("Upserting final execution tags", "tags", tags)
				if err := workflow.UpsertTypedSearchAttributes(ctx, key.ValueSet(tags)); err != nil {
					logger.Error("Failed to upsert final search attributes for tags", "error", err)
				}
			}
		}
		updateFinalWorkflowStatusAndExecuteHooks(ctx, wf, inputs, logger, nil)
	}

	logger.Info("Workflow execution completed", "workflowId", wf.ID, "result", workflowResult)
	resultBytes, err := json.Marshal(workflowResult)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow result: %w", err)
	}
	return string(resultBytes), nil
}

func updateInprogressWorkflowStatus(ctx workflow.Context, wf *model.Workflow, logger temporalLog.Logger) {
	logger.Info("updating workflow status to in-progress", "id", wf.ID, "tenant", wf.TenantID, "account", wf.AccountID)
	// Update workflow status to RUNNING with retry logic
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second, // Short timeout for status update
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	}
	statusUpdateCtx := workflow.WithActivityOptions(ctx, activityOptions)
	statusFuture := workflow.ExecuteActivity(statusUpdateCtx, Internal_UpdateLastExecutionStatusActivity, wf, model.WorkflowExecutionStatusRunning, "")
	if err := statusFuture.Get(statusUpdateCtx, nil); err != nil {
		logger.Error("Failed to update workflow status to RUNNING", "error", err)
		// Optionally: surface alert here if critical
	}
}

type internalTaskResult struct {
	Output interface{}
	Trace  []model.TaskExecutionDetails
}

// processTaskLoop implements the core task execution logic, capable of recursion for sub-tasks (e.g. switch branches).
func processTaskLoop(
	ctx workflow.Context,
	tasksToRun []model.Task,
	templateContext *TemplateContext,
	logger temporalLog.Logger,
	pendingApprovalTokens map[string]string,
	approvalTokenSignalChan workflow.ReceiveChannel,
	updateSignalChan workflow.ReceiveChannel,
	wf *model.Workflow, // Passed for defaults logic
	temporalId string,
	taskRegistry *tasks.TaskRegistry,
	temporalClient client.Client,
	dataConverter converter.DataConverter,
	workflowStore model.WorkflowStore,
	childWorkflowFunc any,
) (map[string]bool, []model.TaskExecutionDetails, error) {

	initialSortedTasks, err := TopologicalSort(tasksToRun)
	if err != nil {
		logger.Error("TopologicalSort failed", "error", err, "tasks", tasksToRun)
		return nil, nil, err
	}

	// Validate that all dependencies exist in the task list
	taskIDSet := make(map[string]struct{})
	for _, t := range initialSortedTasks {
		taskIDSet[t.ID] = struct{}{}
	}
	missingDeps := make(map[string][]string) // taskID -> missing deps
	for _, t := range initialSortedTasks {
		for _, dep := range t.DependsOn {
			if _, ok := taskIDSet[dep]; !ok {
				missingDeps[t.ID] = append(missingDeps[t.ID], dep)
			}
		}
	}
	if len(missingDeps) > 0 {
		for taskID, deps := range missingDeps {
			logger.Error("Task has missing dependencies", "taskID", taskID, "missingDependencies", deps)
		}
		return nil, nil, fmt.Errorf("workflow definition error: missing dependencies detected: %v", missingDeps)
	}

	// Build a lookup map for all tasks defined in the top-level workflow.
	// This is crucial for resolving switch branch tasks by their ID.
	fullWorkflowTaskDefinitions := make(map[string]model.Task)
	if wf != nil {
		for _, t := range wf.Definition.Tasks {
			fullWorkflowTaskDefinitions[t.ID] = t
		}
	}

	// Tasks reachable only via case routing (cases[].next / default_next) are
	// filtered out of the main scheduling loop — they run only inside the
	// switch's inline child workflow when their case matches. See
	// switch_routing.go for the detection rule.
	switchTargetTaskIDs := collectSwitchTargetTaskIDs(initialSortedTasks)

	// Filter out switch-exclusive tasks from the execution list.
	// They remain in fullWorkflowTaskDefinitions for hydration by child workflows.
	var sortedTasks []model.Task
	for _, t := range initialSortedTasks {
		if switchTargetTaskIDs[t.ID] {
			logger.Debug("Skipping switch target task from main loop", "taskID", t.ID)
			continue
		}
		sortedTasks = append(sortedTasks, t)
	}

	taskMap := make(map[string]model.Task)
	for _, task := range sortedTasks {
		taskMap[task.ID] = task
	}

	taskFutures := make(map[string]workflow.Future)
	completedTasks := make(map[string]bool)
	executionTrace := make([]model.TaskExecutionDetails, 0)
	matrixReplacements := make(map[string][]string) // Track matrix task expansions
	reverseMatrixMap := make(map[string]struct {
		ParentID string
		Index    int
	})
	switchWaits := make(map[string]bool) // Track tasks that are waiting for sub-execution (e.g. switch branches)
	// switchBranchDone tracks task IDs resolved by a `core.switch`'s routing
	// decision (selected branches hoisted from child context, unselected
	// stamped SKIPPED). Kept separate from `completedTasks` so the loop's
	// count-based stop condition stays intact and only top-level sortedTasks
	// drive termination.
	switchBranchDone := make(map[string]bool)
	// Trace returned by the inline sub-routine, captured before settlement so we
	// retain it on f.Get errors. Temporal's Future.Get does not populate the
	// value pointer when the future resolves with an error, so a failed branch
	// would otherwise lose its already-executed children.
	inlineTraces := make(map[string][]model.TaskExecutionDetails)
	var workflowError error

	for len(completedTasks) < len(sortedTasks) {
		selector := workflow.NewSelector(ctx)

		// Signal handlers
		selector.AddReceive(approvalTokenSignalChan, func(c workflow.ReceiveChannel, more bool) {
			var signalData map[string]string
			c.Receive(ctx, &signalData)
			if taskID, ok := signalData["task_id"]; ok {
				if taskToken, ok := signalData["task_token"]; ok {
					pendingApprovalTokens[taskID] = taskToken
					logger.Info("Received approval token for task", "taskID", taskID)
				}
			}
		})

		selector.AddReceive(updateSignalChan, func(c workflow.ReceiveChannel, more bool) {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("Panic recovered in signal handler", "panic", r)
				}
			}()
			var signal model.UpdateWorkflowExecutionSignal
			c.Receive(ctx, &signal)
			if templateContext != nil {
				for k, v := range signal.Inputs {
					templateContext.Inputs[k] = v
				}
				logger.Info("Workflow inputs updated via signal", "newInputs", signal.Inputs)
			}
		})

		for i := 0; i < len(sortedTasks); i++ {
			task := sortedTasks[i]
			if _, exists := taskFutures[task.ID]; exists {
				continue
			}
			if completedTasks[task.ID] {
				continue
			}

			// A task with Disabled=true is intentionally muted by the user. The frontend
			// also strips its incoming/outgoing edges so dependents naturally resolve
			// without it, but we still need this guard: the task itself has no deps left,
			// which the executor would otherwise treat as a root and run on workflow
			// start. Mark it skipped before any matrix expansion / type check / dependency
			// resolution, then propagate Skipped status so any downstream that *does*
			// still reference it (e.g. user manually edited JSON) skips via the existing
			// path at the dependency-check loop below.
			if task.Disabled {
				logger.Info("Skipping disabled task", "taskID", task.ID, "type", task.Type)
				completedTasks[task.ID] = true
				if templateContext.Tasks[task.ID] == nil {
					templateContext.Tasks[task.ID] = make(map[string]any)
				}
				templateContext.Tasks[task.ID]["status"] = model.TaskStatusSkipped
				executionTrace = append(executionTrace, model.TaskExecutionDetails{
					ID:     task.ID,
					Type:   task.Type,
					Status: model.TaskStatusSkipped,
				})
				continue
			}

			if task.Type == "" {
				return completedTasks, nil, fmt.Errorf("task type is missing for task ID: %s", task.ID)
			}

			// Update dependencies on the fly if a parent was a matrix task that has been expanded
			updatedDependsOn := false
			for j, depID := range task.DependsOn {
				if replacements, ok := matrixReplacements[depID]; ok {
					// replace this dependency with all its expanded counterparts
					newDeps := append(task.DependsOn[:j], replacements...)
					newDeps = append(newDeps, task.DependsOn[j+1:]...)
					task.DependsOn = newDeps
					updatedDependsOn = true
					break // restart dependency check for this task
				}
			}
			if updatedDependsOn {
				sortedTasks[i] = task
				i-- // re-process this task with updated dependencies
				continue
			}

			dependenciesMet := true
			skippedDependency := false
			plainSkippedDep := false
			skippedDepCount := 0
			for _, depID := range task.DependsOn {
				// Switch-routed deps are resolved by the switch's goroutine
				// rather than the main loop. Their readiness is implicit
				// (they don't enter completedTasks). For skip-propagation:
				// fan-in joins (≥1 selected branch + others skipped) must
				// still run, so a switch-skipped dep does NOT by itself
				// trigger skip. But when EVERY dep is skipped — e.g. a
				// downstream task whose only ancestor is an unselected
				// branch — the task should be SKIPPED.
				if switchBranchDone[depID] {
					if taskData, ok := templateContext.Tasks[depID]; ok {
						if status, ok := taskData["status"].(model.TaskStatus); ok && status == model.TaskStatusSkipped {
							skippedDepCount++
						} else if statusStr, ok := taskData["status"].(string); ok && statusStr == string(model.TaskStatusSkipped) {
							skippedDepCount++
						}
					}
					continue
				}
				if !completedTasks[depID] {
					dependenciesMet = false
					break
				}
				if taskData, ok := templateContext.Tasks[depID]; ok {
					if status, ok := taskData["status"].(model.TaskStatus); ok && status == model.TaskStatusSkipped {
						plainSkippedDep = true
						skippedDepCount++
					} else if statusStr, ok := taskData["status"].(string); ok && statusStr == string(model.TaskStatusSkipped) {
						plainSkippedDep = true
						skippedDepCount++
					}
				}
			}
			// Skip-propagation:
			//   - Any plain (non-switch) skipped dep → propagate (preserves
			//     prod's any-skipped behavior for normal joins).
			//   - All deps skipped → propagate (covers all-switch-unselected).
			if dependenciesMet && len(task.DependsOn) > 0 {
				if plainSkippedDep || skippedDepCount == len(task.DependsOn) {
					skippedDependency = true
				}
			}

			if dependenciesMet {
				// Matrix expansion logic - only expand when dependencies are met
				if len(task.Matrix) > 0 {
					resolvedMatrix := make(map[string]any)
					for k, v := range task.Matrix {
						processedVal, err := ProcessValue(v, templateContext)
						if err != nil {
							logger.Error("Failed to process matrix parameter", "taskID", task.ID, "key", k, "error", err)
							return completedTasks, nil, fmt.Errorf("failed to process matrix parameter %s for task %s: %w", k, task.ID, err)
						}
						resolvedMatrix[k] = processedVal
					}

					combinations, err := generateMatrixCombinations(resolvedMatrix)
					if err != nil {
						logger.Error("Failed to generate matrix combinations", "taskID", task.ID, "error", err)
						return completedTasks, nil, fmt.Errorf("failed to generate matrix combinations for task %s: %w", task.ID, err)
					}

					if _, ok := templateContext.Tasks[task.ID]; !ok {
						templateContext.Tasks[task.ID] = make(map[string]any)
					}
					templateContext.Tasks[task.ID]["output"] = make([]any, len(combinations))

					var expandedTaskIDs []string
					for j, combo := range combinations {
						expandedTask := task
						expandedTask.ID = fmt.Sprintf("%s-%d", task.ID, j)
						expandedTask.Matrix = nil

						tempTplCtx := templateContext.Clone()
						tempTplCtx.Matrix = combo

						processedParams, err := ProcessValue(expandedTask.Params, tempTplCtx)
						if err != nil {
							logger.Error("Failed to process params for expanded matrix task", "taskID", expandedTask.ID, "error", err)
							return completedTasks, nil, fmt.Errorf("failed to process params for expanded matrix task %s: %w", expandedTask.ID, err)
						}
						expandedTask.Params = processedParams.(map[string]any)

						sortedTasks = append(sortedTasks, expandedTask)
						taskMap[expandedTask.ID] = expandedTask
						expandedTaskIDs = append(expandedTaskIDs, expandedTask.ID)
						reverseMatrixMap[expandedTask.ID] = struct {
							ParentID string
							Index    int
						}{ParentID: task.ID, Index: j}
					}
					matrixReplacements[task.ID] = expandedTaskIDs
					completedTasks[task.ID] = true // The original matrix task is now considered "completed" (expanded)
					continue
				}

				if skippedDependency && task.If == "" {
					completedTasks[task.ID] = true
					templateContext.Tasks[task.ID] = map[string]any{"status": string(model.TaskStatusSkipped)}
					executionTrace = append(executionTrace, model.TaskExecutionDetails{
						ID:     task.ID,
						Type:   task.Type,
						Status: model.TaskStatusSkipped,
					})
					continue
				}

				tplCtx := templateContext.Clone()
				if len(task.Matrix) > 0 {
					tplCtx.Matrix = task.Matrix
				}

				shouldExecute, err := evaluateTaskCondition(ctx, task, tplCtx)
				if err != nil {
					logger.Error("Failed to evaluate task condition", "taskID", task.ID, "error", err)
					return completedTasks, nil, err
				}

				if !shouldExecute {
					completedTasks[task.ID] = true
					templateContext.Tasks[task.ID] = map[string]any{"status": string(model.TaskStatusSkipped)}
					executionTrace = append(executionTrace, model.TaskExecutionDetails{
						ID:     task.ID,
						Type:   task.Type,
						Status: model.TaskStatusSkipped,
					})
					continue
				}

				ao := createActivityOptionsForTask(task, &wf.Definition)
				ao.ActivityID = task.ID
				ctxWithAO := workflow.WithActivityOptions(ctx, ao)

				// Special handling: Do not render "tasks" param for Loop tasks as they contain templates
				// that depend on loop variables not yet available in the current context.
				paramsToProcess := task.Params
				preservedParams := make(map[string]any)

				if impl, err := taskRegistry.GetTask(task.Type); err == nil {
					if _, isLoop := impl.(types.TaskLoop); isLoop {
						pCopy := make(map[string]any)
						for k, v := range task.Params {
							pCopy[k] = v
						}
						if t, ok := pCopy["tasks"]; ok {
							preservedParams["tasks"] = t
							delete(pCopy, "tasks")
						}
						if o, ok := pCopy["output"]; ok {
							preservedParams["output"] = o
							delete(pCopy, "output")
						}
						paramsToProcess = pCopy
					}
				}

				processedParams, err := ProcessValue(paramsToProcess, tplCtx)
				if err != nil {
					logger.Error("Failed to process params for task", "taskID", task.ID, "error", err)
					return completedTasks, nil, fmt.Errorf("failed to process params for task %s: %w", task.ID, err)
				}

				paramMap, ok := processedParams.(map[string]any)
				if !ok {
					if processedParams != nil {
						paramMap = map[string]any{"_raw": processedParams}
					} else {
						paramMap = make(map[string]any)
					}
				}
				for k, v := range preservedParams {
					paramMap[k] = v
				}

				paramMap[tasks.ParamTenantID] = wf.TenantID
				paramMap[tasks.ParamAccountID] = wf.AccountID
				paramMap[tasks.ParamWorkflowID] = wf.ID
				paramMap[tasks.ParamUserID] = wf.UpdatedBy
				paramMap[tasks.ParamVars] = tplCtx.Vars
				paramMap[tasks.ParamWorkflowName] = wf.Name
				if displayName := getUserDisplayName(wf); displayName != "" {
					paramMap[tasks.ParamUserDisplayName] = displayName
				}

				// Check if task is an inline workflow (e.g. group, switch)
				taskImpl, err := taskRegistry.GetTask(task.Type)
				if err == nil {
					if inlineTask, ok := taskImpl.(types.TaskInlineWorkflow); ok {
						// Determine execution strategy
						executeAsChild := false
						if strategy, ok := taskImpl.(types.TaskExecutionStrategy); ok {
							executeAsChild = strategy.ShouldExecuteAsChildWorkflow()
						}

						// Force inline execution for DryRun to capture trace
						if wf.DryRun {
							executeAsChild = false
						}

						if executeAsChild {
							// Execute as Child Workflow
							logger.Info("Executing task as Child Workflow", "taskID", task.ID, "taskType", task.Type)

							if len(task.Tasks) > 0 {
								paramMap["tasks"] = task.Tasks
							}

							// Create a TaskContext for the inline task
							taskContext := types.NewTemporalTaskContext(context.TODO(), wf.TenantID, wf.AccountID, wf.ID, wf.UpdatedBy, wf.Name, getUserDisplayName(wf), temporalClient, dataConverter, workflowStore, temporalId, uuid.Nil.String(), logger, wf.DryRun)

							childWfDef, err := inlineTask.GetChildWorkflowDefinition(taskContext, paramMap)
							if err != nil {
								logger.Error("Failed to get child workflow definition", "taskID", task.ID, "error", err)
								return completedTasks, nil, err
							}

							// Construct a synthetic Workflow object
							// We use "inline-" prefix so GetDetailedWorkflowExecution reconstructs definition from history input
							childWfID := fmt.Sprintf("inline-%s-%s", task.ID, uuid.New().String())
							childWf := &model.Workflow{
								ID:                  childWfID,
								TenantID:            wf.TenantID,
								AccountID:           wf.AccountID,
								Name:                fmt.Sprintf("child-of-%s", wf.Name),
								Definition:          *childWfDef,
								Status:              model.WorkflowStatusActive,
								CreatedBy:           wf.UpdatedBy,
								UpdatedBy:           wf.UpdatedBy,
								LastExecutionStatus: model.WorkflowExecutionStatusRunning,
							}

							cwo := workflow.ChildWorkflowOptions{
								WorkflowID: fmt.Sprintf("%s-%s-%s", wf.ID, task.ID, uuid.New().String()), // Unique Run ID
								TaskQueue:  config.Config.RunbookServerTemporalQueue,
								Memo: map[string]interface{}{
									"parent_task_id":      task.ID,
									"child_definition_id": childWfID,
								},
								SearchAttributes: map[string]interface{}{
									model.SearchAttrTenantID:         wf.TenantID,
									model.SearchAttrAccountID:        wf.AccountID,
									model.SearchAttrParentWorkflowID: wf.ID, // Or SearchAttrParentWorkflowID constant if available
								},
							}

							ctxWithCWO := workflow.WithChildOptions(ctx, cwo)
							childFuture := workflow.ExecuteChildWorkflow(ctxWithCWO, childWorkflowFunc, childWf, nil)
							taskFutures[task.ID] = childFuture
							continue

						} else {
							// Execute as inline workflow (Coroutine)
							logger.Info("Executing inline workflow task", "taskID", task.ID, "taskType", task.Type)

							if len(task.Tasks) > 0 {
								paramMap["tasks"] = task.Tasks
							}

							// Resolve the child workflow definition. Tasks whose params carry a
							// `cases` array (routing tasks like core.switch) dispatch resolution
							// as a regular Temporal activity so the rendered input, selected case,
							// status, and timings show up in workflow_get_execution like any
							// other task. Container-shaped inline tasks (group, foreach) keep the
							// cheaper in-coroutine resolution.
							var childWfDef *model.WorkflowDefinition
							if _, isCaseRouting := task.Params["cases"]; isCaseRouting {
								ao := workflow.ActivityOptions{
									ActivityID:          task.ID,
									StartToCloseTimeout: 1 * time.Minute,
									RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
								}
								var rawResult any
								if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, ao), task.Type, paramMap).Get(ctx, &rawResult); err != nil {
									logger.Error("Activity-based child workflow resolution failed", "taskID", task.ID, "error", err)
									return completedTasks, nil, err
								}
								childWfDef = buildRoutingDef(rawResult)
							} else {
								// Create a TaskContext for the inline task
								taskContext := types.NewTemporalTaskContext(context.TODO(), wf.TenantID, wf.AccountID, wf.ID, wf.UpdatedBy, wf.Name, getUserDisplayName(wf), temporalClient, dataConverter, workflowStore, temporalId, uuid.Nil.String(), logger, wf.DryRun)

								def, err := inlineTask.GetChildWorkflowDefinition(taskContext, paramMap)
								if err != nil {
									logger.Error("Failed to get child workflow definition", "taskID", task.ID, "error", err)
									return completedTasks, nil, err
								}
								childWfDef = def
							}

							var hydratedTasks []model.Task
							originalToRenamedID := make(map[string]string)

							for _, t := range childWfDef.Tasks {
								// If task is incomplete (missing Type and Uses) but has ID, try to find it in full definitions
								if t.Type == "" && t.ID != "" {
									if fullTask, found := fullWorkflowTaskDefinitions[t.ID]; found {
										// Create a copy to avoid mutating the original definition
										taskCopy := fullTask
										// Uniquify ID to avoid duplicate activity scheduling if the task is also in the main loop
										taskCopy.ID = fmt.Sprintf("%s-%s", task.ID, fullTask.ID)
										hydratedTasks = append(hydratedTasks, taskCopy)
										originalToRenamedID[t.ID] = taskCopy.ID
									} else {
										// If not found, keep as is (might be a bug or intended empty task)
										logger.Warn("Inline task references unknown task ID, or task definition is incomplete", "refID", t.ID, "parentTaskID", task.ID)
										hydratedTasks = append(hydratedTasks, t)
										originalToRenamedID[t.ID] = t.ID
									}
								} else {
									// Also uniquify IDs for explicitly defined inline tasks to ensure uniqueness in this execution scope
									tCopy := t
									tCopy.ID = fmt.Sprintf("%s-%s", task.ID, t.ID)
									hydratedTasks = append(hydratedTasks, tCopy)
									originalToRenamedID[t.ID] = tCopy.ID
								}
							}

							// Update dependencies to point to new unique IDs
							for i := range hydratedTasks {
								if len(hydratedTasks[i].DependsOn) > 0 {
									var newDependsOn []string
									for _, dep := range hydratedTasks[i].DependsOn {
										if newID, ok := originalToRenamedID[dep]; ok {
											newDependsOn = append(newDependsOn, newID)
										} else {
											newDependsOn = append(newDependsOn, dep)
										}
									}
									hydratedTasks[i].DependsOn = newDependsOn
								}
							}

							// Start sub-routine
							subFuture, settlement := workflow.NewFuture(ctx)
							switchWaits[task.ID] = true // Mark as waiting for sub-execution

							workflow.Go(ctx, func(gCtx workflow.Context) {
								defer func() {
									if r := recover(); r != nil {
										logger.Error("Panic in inline workflow coroutine", "panic", r)
										settlement.Set(nil, fmt.Errorf("panic in inline workflow: %v", r))
									}
								}()

								// Create a new template context for the child execution
								childTemplateContext := templateContext.Clone()

								// Populate child inputs if definition exists and has inputs
								if childWfDef != nil && len(childWfDef.Inputs) > 0 {
									// Reset Inputs for the child scope to match child definition
									childTemplateContext.Inputs = make(map[string]any)
									for _, input := range childWfDef.Inputs {
										childTemplateContext.Inputs[input.ID] = input.Default
									}
									// Note: We might want to keep parent vars/state? Clone() does that.
									// But Inputs must be scoped to the child.
								}

								// Recursive call with child context
								_, innerTrace, runErr := processTaskLoop(gCtx, hydratedTasks, childTemplateContext, logger, pendingApprovalTokens, approvalTokenSignalChan, updateSignalChan, wf, temporalId, taskRegistry, temporalClient, dataConverter, workflowStore, childWorkflowFunc)

								var output any
								// Debug logging
								logger.Info("Inline workflow execution finished", "taskID", task.ID, "runErr", runErr, "hasDef", childWfDef != nil)
								if childWfDef != nil {
									logger.Info("Child definition output", "output", childWfDef.Output)
								}

								if runErr == nil && childWfDef != nil && len(childWfDef.Output) > 0 {
									// Construct a context for evaluating the child workflow output
									// We need to expose the child tasks as if they were local (using their original IDs).
									childOutputContext := templateContext.Clone()
									childOutputContext.Tasks = make(map[string]map[string]any)

									logger.Info("Mapping child tasks output", "originalToRenamedID", originalToRenamedID)

									// Map the execution results from renamed IDs back to original IDs for output evaluation
									for originalID, renamedID := range originalToRenamedID {
										if val, ok := childTemplateContext.Tasks[renamedID]; ok {
											childOutputContext.Tasks[originalID] = val
											logger.Info("Mapped child task output", "originalID", originalID, "renamedID", renamedID, "val", val)
										} else {
											logger.Warn("Child task output missing in childTemplateContext", "renamedID", renamedID)
										}
									}

									// Evaluate the output map
									rendered, err := childOutputContext.RenderMap(childWfDef.Output)
									if err != nil {
										logger.Error("Failed to render child workflow output", "taskID", task.ID, "error", err)
										// We don't fail the workflow here, but the output will be nil/missing
									} else {
										output = rendered
										logger.Info("Child workflow output rendered successfully", "taskID", task.ID, "output", output)
									}
								} else {
									logger.Info("Child workflow output processing skipped", "taskID", task.ID, "runErr", runErr, "hasDef", childWfDef != nil, "outputLen", len(childWfDef.Output))
								}

								// Switch fan-in support: mirror per-branch state into the
								// parent so a downstream join can resolve its dep-check.
								// See switch_routing.go for the rationale.
								if runErr == nil && task.Type == "core.switch" {
									propagateSwitchBranchState(task, output, originalToRenamedID, childTemplateContext, templateContext, switchBranchDone)
								}

								// Stash the trace before settling so the settlement handler
								// can recover children when runErr != nil — Future.Get does
								// not populate the value pointer on error.
								inlineTraces[task.ID] = innerTrace
								settlement.Set(internalTaskResult{Output: output, Trace: innerTrace}, runErr)
							})

							taskFutures[task.ID] = subFuture
							continue
						}
					} else if loopTask, ok := taskImpl.(types.TaskLoop); ok {
						if len(task.Tasks) > 0 {
							paramMap["tasks"] = task.Tasks
						}
						taskContext := types.NewTemporalTaskContext(context.TODO(), wf.TenantID, wf.AccountID, wf.ID, wf.UpdatedBy, wf.Name, getUserDisplayName(wf), temporalClient, dataConverter, workflowStore, temporalId, uuid.Nil.String(), logger, wf.DryRun)

						loopConfig, err := loopTask.GetLoopConfig(taskContext, paramMap)
						if err != nil {
							logger.Error("Failed to get loop config", "taskID", task.ID, "error", err)
							return completedTasks, nil, err
						}

						if len(loopConfig.Items) == 0 {
							logger.Warn("Loop items list is empty, skipping loop execution", "taskID", task.ID)
							completedTasks[task.ID] = true
							templateContext.Tasks[task.ID] = map[string]any{"status": string(model.TaskStatusSkipped), "output": []any{}}
							executionTrace = append(executionTrace, model.TaskExecutionDetails{
								ID:     task.ID,
								Type:   task.Type,
								Status: model.TaskStatusSkipped,
							})
							continue
						}
						if len(loopConfig.Body.Tasks) == 0 {
							logger.Warn("Loop body is empty, skipping loop execution", "taskID", task.ID)
							completedTasks[task.ID] = true
							templateContext.Tasks[task.ID] = map[string]any{"status": string(model.TaskStatusSkipped), "output": []any{}}
							executionTrace = append(executionTrace, model.TaskExecutionDetails{
								ID:     task.ID,
								Type:   task.Type,
								Status: model.TaskStatusSkipped,
							})
							continue
						}

						subFuture, settlement := workflow.NewFuture(ctx)
						switchWaits[task.ID] = true

						workflow.Go(ctx, func(gCtx workflow.Context) {
							defer func() {
								if r := recover(); r != nil {
									settlement.Set(nil, fmt.Errorf("panic in loop: %v", r))
								}
							}()

							results := make([]any, len(loopConfig.Items))
							traces := make([][]model.TaskExecutionDetails, len(loopConfig.Items))
							errors := make([]error, len(loopConfig.Items))

							concurrency := loopConfig.BatchSize
							if concurrency <= 0 {
								concurrency = 1
							}
							if concurrency > len(loopConfig.Items) {
								concurrency = len(loopConfig.Items)
							}

							sem := workflow.NewBufferedChannel(gCtx, concurrency)
							for i := 0; i < concurrency; i++ {
								sem.Send(gCtx, struct{}{})
							}

							iterationsFinished := 0
							totalItems := len(loopConfig.Items)

							for i, item := range loopConfig.Items {
								sem.Receive(gCtx, nil)

								idx := i
								val := item

								workflow.Go(gCtx, func(loopCtx workflow.Context) {
									defer func() {
										sem.Send(gCtx, struct{}{})
										iterationsFinished++
									}()

									iterationTplCtx := templateContext.Clone()
									iterationTplCtx.Vars["LoopItem"] = map[string]any{
										loopConfig.ItemVarName: val,
									}

									var hydratedTasks []model.Task
									originalToRenamedID := make(map[string]string)
									for _, t := range loopConfig.Body.Tasks {
										tCopy := t
										tCopy.ID = fmt.Sprintf("%s-%d-%s", task.ID, idx, t.ID)
										hydratedTasks = append(hydratedTasks, tCopy)
										originalToRenamedID[t.ID] = tCopy.ID
									}
									for k := range hydratedTasks {
										if len(hydratedTasks[k].DependsOn) > 0 {
											var newDeps []string
											for _, d := range hydratedTasks[k].DependsOn {
												if newID, ok := originalToRenamedID[d]; ok {
													newDeps = append(newDeps, newID)
												} else {
													newDeps = append(newDeps, d)
												}
											}
											hydratedTasks[k].DependsOn = newDeps
										}
									}

									_, innerTrace, runErr := processTaskLoop(loopCtx, hydratedTasks, iterationTplCtx, logger, pendingApprovalTokens, approvalTokenSignalChan, updateSignalChan, wf, temporalId, taskRegistry, temporalClient, dataConverter, workflowStore, childWorkflowFunc)

									if runErr != nil {
										errors[idx] = runErr
									} else {
										traces[idx] = innerTrace
										if loopConfig.Body.Output != nil {
											childOutputContext := templateContext.Clone()
											childOutputContext.Tasks = make(map[string]map[string]any)
											for origID, renID := range originalToRenamedID {
												if v, ok := iterationTplCtx.Tasks[renID]; ok {
													childOutputContext.Tasks[origID] = v
												}
											}
											childOutputContext.Vars["LoopItem"] = map[string]any{
												loopConfig.ItemVarName: val,
											}
											res, err := childOutputContext.RenderMap(loopConfig.Body.Output)
											if err == nil {
												results[idx] = res
											}
										}
									}
								})
							}

							err = workflow.Await(gCtx, func() bool {
								return iterationsFinished == totalItems
							})

							if err != nil {
								settlement.Set(nil, err)
								return
							}

							for _, e := range errors {
								if e != nil {
									settlement.Set(nil, fmt.Errorf("loop iteration failed: %w", e))
									return
								}
							}

							// Flatten traces for loop iterations (or group them?)
							// For loop, we probably want to group them under the loop task
							var combinedTrace []model.TaskExecutionDetails
							for _, t := range traces {
								combinedTrace = append(combinedTrace, t...)
							}

							settlement.Set(internalTaskResult{Output: results, Trace: combinedTrace}, nil)
						})

						taskFutures[task.ID] = subFuture
						continue
					}
				}

				// Standard Activity Execution
				if wf.DryRun {
					// Inject dry run param
					paramMap[tasks.ParamDryRun] = true
					logger.Info("DryRun: Executing task with dry-run flag", "taskID", task.ID, "taskType", task.Type)
				}

				logger.Info("Executing activity for task", "taskID", task.ID, "taskType", task.Type)
				future := workflow.ExecuteActivity(ctxWithAO, task.Type, paramMap)
				taskFutures[task.ID] = future
			}
		}

		pendingFuturesCount := 0
		for id := range taskFutures {
			if !completedTasks[id] {
				pendingFuturesCount++
			}
		}

		if pendingFuturesCount > 0 {
			for id, future := range taskFutures {
				if !completedTasks[id] {
					selector.AddFuture(future, func(f workflow.Future) {
						defer func() {
							if r := recover(); r != nil {
								logger.Error("Panic recovered in future callback", "panic", r)
							}
						}()

						// Check if this was a sub-execution wait
						if switchWaits[id] {
							var inlineResult internalTaskResult
							inlineErr := f.Get(ctx, &inlineResult)

							taskDef := taskMap[id]
							endTime := workflow.Now(ctx)
							status := model.TaskStatusCompleted
							var errStr string
							if inlineErr != nil {
								status = model.TaskStatusFailed
								errStr = inlineErr.Error()
								workflowError = inlineErr
							}

							// Use the stashed trace so we still surface children that ran
							// before a branch failed. inlineResult.Trace is empty on error
							// because Future.Get does not populate the value pointer then.
							children := inlineTraces[id]
							delete(inlineTraces, id)

							executionTrace = append(executionTrace, model.TaskExecutionDetails{
								ID:       id,
								Type:     taskDef.Type,
								Status:   status,
								EndTime:  &endTime,
								Children: children,
								Output:   inlineResult.Output,
								Error:    errStr,
							})

							if inlineErr == nil {
								if inlineResult.Output != nil {
									templateContext.Tasks[id] = map[string]any{
										"status": string(model.TaskStatusCompleted),
										"output": inlineResult.Output,
									}
								} else {
									templateContext.Tasks[id] = map[string]any{
										"status": string(model.TaskStatusCompleted),
									}
								}
							}
							completedTasks[id] = true
							return
						}

						err := handleTaskCompletion(ctx, wf.ID, id, f, templateContext, taskMap, wf.DryRun)
						if err != nil {
							logger.Error("Task completion error", "taskID", id, "error", err)
							workflowError = err
						}

						completedTasks[id] = true

						if info, ok := reverseMatrixMap[id]; ok {
							if taskData, ok := templateContext.Tasks[id]; ok {
								if output, ok := taskData["output"]; ok {
									if parentData, ok := templateContext.Tasks[info.ParentID]; ok {
										if outputs, ok := parentData["output"].([]any); ok {
											if info.Index >= 0 && info.Index < len(outputs) {
												outputs[info.Index] = output
											}
										}
									}
								}
							}
						}
					})
				}
			}
			selector.Select(ctx)
		} else if len(completedTasks) < len(sortedTasks) {
			return completedTasks, executionTrace, fmt.Errorf("workflow stalled: no tasks to run, but not all tasks are complete")
		}

		if workflowError != nil {
			logger.Error("Workflow error after selector", "error", workflowError)
			return completedTasks, executionTrace, workflowError
		}
	}
	return completedTasks, executionTrace, nil
}

func updateFinalWorkflowStatusAndExecuteHooks(ctx workflow.Context, wf *model.Workflow, inputs map[string]any, logger temporalLog.Logger, workflowError error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Panic recovered in workflow deferred cleanup hooks", "panic", r)
		}
	}()

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    5,
		},
	}
	statusUpdateCtx := workflow.WithActivityOptions(ctx, activityOptions)

	finalStatus := model.WorkflowExecutionStatusCompleted
	var statusMessage string
	if workflowError != nil {
		finalStatus = model.WorkflowExecutionStatusFailed
		statusMessage = workflowError.Error()
	}

	if !wf.DryRun {
		logger.Info("updating final workflow status", "id", wf.ID, "tenant", wf.TenantID, "account", wf.AccountID)
		statusFuture := workflow.ExecuteActivity(statusUpdateCtx, Internal_UpdateLastExecutionStatusActivity, wf, finalStatus, statusMessage)
		statusErr := statusFuture.Get(statusUpdateCtx, nil)
		if statusErr != nil {
			logger.Error("Failed to update final workflow status", "error", statusErr)
		}

		// Update linked event_resolution if one exists for this execution (no-op if none)
		info := workflow.GetInfo(ctx)
		typeRefID := wf.ID + ":" + info.WorkflowExecution.RunID
		resFuture := workflow.ExecuteActivity(statusUpdateCtx, Internal_UpdateEventResolutionStatusActivity, typeRefID, workflowError != nil)
		if resErr := resFuture.Get(statusUpdateCtx, nil); resErr != nil {
			logger.Warn("Failed to update event resolution status", "error", resErr)
		}
	}

	if workflowError != nil {
		logger.Error("Workflow failed", "error", workflowError)
		if wf != nil && wf.Definition.Hooks != nil && len(wf.Definition.Hooks.Failure) > 0 {
			onFailureCtx := createActivityCtxForAction(ctx)
			if err := executeSimpleActions(onFailureCtx, wf.Definition.Hooks.Failure, NewTemplateContext(wf.Definition.Inputs, inputs)); err != nil {
				logger.Error("On.Failure hook failed", "error", err)
			}
		}
	} else {
		logger.Info("Workflow completed successfully")
		if wf != nil && wf.Definition.Hooks != nil && len(wf.Definition.Hooks.Success) > 0 {
			onSuccessCtx := createActivityCtxForAction(ctx)
			if err := executeSimpleActions(onSuccessCtx, wf.Definition.Hooks.Success, NewTemplateContext(wf.Definition.Inputs, inputs)); err != nil {
				logger.Error("On.Success hook failed", "error", err)
			}
		}
	}
	if wf != nil && wf.Definition.Hooks != nil && len(wf.Definition.Hooks.Always) > 0 {
		onFinalCtx := createActivityCtxForAction(ctx)
		if err := executeSimpleActions(onFinalCtx, wf.Definition.Hooks.Always, NewTemplateContext(wf.Definition.Inputs, inputs)); err != nil {
			logger.Error("On.Always hook failed", "error", err)
		}
	}
}

func executeSimpleActions(ctx workflow.Context, actions []model.Action, tplCtx *TemplateContext) error {
	logger := workflow.GetLogger(ctx)
	activityOptions := workflow.GetActivityOptions(ctx)
	if activityOptions.StartToCloseTimeout == 0 && activityOptions.ScheduleToCloseTimeout == 0 {
		activityOptions.StartToCloseTimeout = 30 * time.Second
		if activityOptions.RetryPolicy == nil {
			activityOptions.RetryPolicy = &temporal.RetryPolicy{MaximumAttempts: 1}
		}
		ctx = workflow.WithActivityOptions(ctx, activityOptions)
	}

	defer func() {
		if r := recover(); r != nil {
			logger.Error("Panic recovered in executeSimpleActions", "panic", r)
		}
	}()

	if actions == nil || tplCtx == nil {
		return nil
	}

	selector := workflow.NewSelector(ctx)
	futures := make([]workflow.Future, 0, len(actions))
	numFutures := 0

	for _, action := range actions {
		params, err := tplCtx.RenderMap(action.Params)
		if err != nil {
			logger.Error("Failed to render task params", "actionType", action.Type, "error", err)
			return err
		}
		if params == nil {
			params = make(map[string]any)
		}
		if v, ok := tplCtx.Inputs[tasks.ParamTenantID]; ok {
			params[tasks.ParamTenantID] = v
		}
		if v, ok := tplCtx.Inputs[tasks.ParamAccountID]; ok {
			params[tasks.ParamAccountID] = v
		}
		if v, ok := tplCtx.Inputs[tasks.ParamWorkflowID]; ok {
			params[tasks.ParamWorkflowID] = v
		}

		future := workflow.ExecuteActivity(ctx, action.Type, params)
		futures = append(futures, future)
		selector.AddFuture(future, func(f workflow.Future) {})
		numFutures++
	}

	for i := 0; i < numFutures; i++ {
		selector.Select(ctx)
	}

	var finalHookErr error
	for _, f := range futures {
		var result any
		if err := f.Get(ctx, &result); err != nil {
			logger.Error("Hook activity failed deterministically", "error", err)
			if finalHookErr == nil {
				finalHookErr = err
			}
		}
	}
	return finalHookErr
}
