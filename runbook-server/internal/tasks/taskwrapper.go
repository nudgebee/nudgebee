package tasks

import (
	"context" // Add this import
	"encoding/json"
	"fmt"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/types"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
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
	// ParamMaxTier is the run's tier ceiling (the definition's max_tier): a
	// step with a higher tier is refused. Absent or 0 means no ceiling.
	ParamMaxTier = "_max_tier"
	// ParamApprovalGate marks the run's own approval gate (the engine-added
	// model.ApprovalGateTaskID step); set only by the executor.
	ParamApprovalGate = "_approval_gate"
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
	maxTier, readable := tierParam(params[ParamMaxTier])
	approvalGate, _ := params[ParamApprovalGate].(string)

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
	delete(params, ParamMaxTier)
	delete(params, ParamApprovalGate)

	// Checked before anything else about the step: it needs no lookups, and a
	// step built at run time is exactly what the static check could not see.
	// A ceiling that cannot be read refuses the step rather than lifting it.
	if !readable {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("step %s has an unreadable tier ceiling", tw.Task.GetName()), "StepTierExceeded", nil)
	}
	if maxTier > 0 {
		if tier, _ := TierFor(tw.Task.GetName()); tier > maxTier {
			activity.GetLogger(ctx).Warn("step refused: above the run's tier",
				"task", tw.Task.GetName(), "tier", tier, "max_tier", maxTier, "workflow_id", workflowID)
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("step %s is tier %d, above this automation's tier %d", tw.Task.GetName(), tier, maxTier),
				"StepTierExceeded", nil)
		}
	}

	if schema := tw.Task.InputSchema(); schema != nil {
		if err := schema.Process(params); err != nil {
			return nil, scrubError(err, secrets)
		}
	}

	// The run's own approval gate only waits for an answer: it needs no right
	// of the principal's, and who may answer is checked when they answer.
	if approvalGate != "" && tw.Task.GetName() == "core.approval" && activity.GetInfo(ctx).ActivityID == model.ApprovalGateTaskID {
		activity.GetLogger(ctx).Info("step authorization skipped for the run's approval gate", "workflow_id", workflowID)
		return tw.Task.Execute(taskCtx, params)
	}

	stepAccount := StepAccount(params, accountID)
	authErr := tw.authorizer().Authorize(ctx, StepAuthInput{
		TaskName:      tw.Task.GetName(),
		TenantID:      tenantID,
		Principal:     principal,
		RunAccountID:  accountID,
		StepAccountID: stepAccount,
		CheckRunRight: true,
		Params:        params,
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

// tierParam reads the tier ceiling from activity params, where a number
// arrives as float64 after the JSON round trip and as an int when built in the
// same process. Absent means no ceiling; anything else that is not a whole
// number from 0 to TierUnrestricted is unreadable.
func tierParam(v any) (int, bool) {
	var n float64
	switch x := v.(type) {
	case nil:
		return 0, true
	case int:
		n = float64(x)
	case int32:
		n = float64(x)
	case int64:
		n = float64(x)
	case float64:
		n = x
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		n = f
	default:
		return 0, false
	}
	if n != float64(int(n)) || n < 0 || n > TierUnrestricted {
		return 0, false
	}
	return int(n), true
}
