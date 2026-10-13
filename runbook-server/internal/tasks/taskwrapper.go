package tasks

import (
	"context" // Add this import
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"

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
	// Authorizer checks each step against the run's principal. Nil means
	// DefaultStepAuthorizer; tests set a stub.
	Authorizer *StepAuthorizer
	// Secrets loads the run account's secret values, which are masked in the
	// step's logs and in the error it returns. Nil masks nothing.
	Secrets SecretLoader
}

func (tw *TaskWrapper) authorizer() StepAuthorizer {
	if tw.Authorizer != nil {
		return *tw.Authorizer
	}
	return DefaultStepAuthorizer
}

const (
	// Internal param keys for workflow metadata
	ParamTenantID   = "_tenant_id"
	ParamAccountID  = "_account_id"
	ParamWorkflowID = "_workflow_id"
	ParamEventID    = "_event_id"
	ParamUserID     = "_user_id"
	// ParamRunAsUserID is the run's principal: whose rights each step is
	// checked against. _user_id stays the display identity (last editor).
	ParamRunAsUserID     = "_run_as_user_id"
	ParamVars            = "_vars" // New: Current state of global vars map
	ParamDryRun          = "_dry_run"
	ParamWorkflowName    = "_workflow_name"
	ParamUserDisplayName = "_user_display_name"
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
	// The run's principal. Runs started before principals existed carry only
	// _user_id (the workflow's last editor), which is who they acted as then.
	principal, _ := params[ParamRunAsUserID].(string)
	if principal == "" {
		principal = userID
	}

	// Build TaskContext (includes logger, metadata, etc.). Its user is the
	// principal, so outbound calls carry the identity the step was checked as.
	// Its logger masks the account's secret values, as does every error below.
	secrets := newStepSecrets(ctx, tw.Secrets, tenantID, accountID, activity.GetLogger(ctx))
	logger := newScrubbingLogger(activity.GetLogger(ctx), secrets)
	taskCtx := types.NewTemporalTaskContextFromActivity(ctx, tenantID, accountID, workflowID, eventID, principal, workflowName, userDisplayName, tw.TemporalClient, tw.Converter, tw.Store, logger, isDryRun)

	// Remove internal metadata keys from params before passing to real task
	delete(params, ParamTenantID)
	delete(params, ParamAccountID)
	delete(params, ParamWorkflowID)
	delete(params, ParamEventID)
	delete(params, ParamUserID)
	delete(params, ParamRunAsUserID)
	delete(params, ParamVars) // Defensive: strip legacy ParamVars from in-flight (pre-fix) activity inputs; no longer attached by the executor
	delete(params, ParamDryRun)
	delete(params, ParamWorkflowName)
	delete(params, ParamUserDisplayName)

	if schema := tw.Task.InputSchema(); schema != nil {
		if err := schema.Process(params); err != nil {
			return nil, scrubError(err, secrets)
		}
	}

	stepAccount := StepAccount(params, accountID)
	authErr := tw.authorizer().Authorize(ctx, StepAuthInput{
		TaskName:      tw.Task.GetName(),
		TenantID:      tenantID,
		Principal:     principal,
		RunAccountID:  accountID,
		StepAccountID: stepAccount,
		CheckRunRight: true,
	})
	activity.GetLogger(ctx).Info("step authorization",
		"task", tw.Task.GetName(), "principal", principal, "account_id", stepAccount,
		"workflow_id", workflowID, "allowed", authErr == nil)
	if authErr != nil {
		return nil, authErr
	}

	result, err := tw.Task.Execute(taskCtx, params)
	return result, scrubError(err, secrets)
}
