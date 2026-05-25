package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	configSvc "nudgebee/runbook/services/config"
	"time"
)

// WorkflowActivities holds dependencies for workflow-related activities.
type WorkflowActivities struct {
	Store         model.WorkflowStore
	ConfigService configSvc.ConfigService
}

// Internal_UpdateLastExecutionStatus updates the last execution status of a workflow.
func (a *WorkflowActivities) Internal_UpdateLastExecutionStatus(ctx context.Context, wf model.Workflow, status model.WorkflowExecutionStatus, statusMessage string) error {
	return a.Store.SetLastExecutionStatus(ctx, wf.TenantID, wf.AccountID, wf.ID, status, time.Now().UTC(), statusMessage)
}

// FetchWorkflowDefinitionActivity fetches a workflow definition by its name.
func (a *WorkflowActivities) FetchWorkflowDefinitionActivity(ctx context.Context, tenantID, accountID, workflowName string) (*model.Workflow, error) {
	wf, err := a.Store.FindByName(ctx, tenantID, accountID, workflowName)
	if err != nil {
		return nil, err
	}
	return wf, nil
}

// FetchWorkflowStateActivity fetches the persistent state for a workflow.
func (a *WorkflowActivities) FetchWorkflowStateActivity(ctx context.Context, workflowID string) (map[string]any, error) {
	items, err := a.Store.GetState(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	state := make(map[string]any)
	for _, item := range items {
		state[item.Key] = item.Value
	}
	return state, nil
}

// UpdateWorkflowStateActivity updates the persistent state for a workflow.
func (a *WorkflowActivities) UpdateWorkflowStateActivity(ctx context.Context, workflowID string, stateUpdates map[string]model.StateUpdateDTO, executionID, taskID string) error {
	const MaxStateValueSize = 10 * 1024 // 10KB

	updates := make([]model.WorkflowStateUpdate, 0, len(stateUpdates))
	for k, dto := range stateUpdates {
		valueBytes, err := json.Marshal(dto.Value)
		if err != nil {
			return fmt.Errorf("failed to marshal state value for key %s: %w", k, err)
		}

		if len(valueBytes) > MaxStateValueSize {
			return fmt.Errorf("state value for key %s exceeds limit of %d bytes", k, MaxStateValueSize)
		}

		var expiresAt *time.Time
		if dto.TTL != "" {
			duration, err := time.ParseDuration(dto.TTL)
			if err != nil {
				return fmt.Errorf("invalid TTL duration for key %s: %w", k, err)
			}
			t := time.Now().Add(duration)
			expiresAt = &t
		}

		updates = append(updates, model.WorkflowStateUpdate{
			Key:         k,
			Value:       dto.Value,
			ExecutionID: executionID,
			TaskID:      taskID,
			ExpiresAt:   expiresAt,
		})
	}

	return a.Store.SetState(ctx, workflowID, updates)
}

// FetchConfigsResponse holds configs and secrets.
type FetchConfigsResponse struct {
	Configs map[string]any
	Secrets map[string]any
}

// FetchWorkflowConfigsActivity fetches the effective configs and secrets a
// workflow execution should see for a given account: tenant-level rows merged
// with account-level rows, where account-level overrides tenant-level on key
// collision. The returned struct contains plain configs and decrypted secrets.
func (a *WorkflowActivities) FetchWorkflowConfigsActivity(ctx context.Context, tenantID, accountID string) (*FetchConfigsResponse, error) {
	acc := accountID
	configs, err := a.ConfigService.ListConfigsDecrypted(ctx, tenantID, &acc, nil)
	if err != nil {
		return nil, err
	}

	configMap := make(map[string]any)
	secretMap := make(map[string]any)

	for _, cfg := range configs {
		if cfg.Type == model.ConfigTypeSecret {
			secretMap[cfg.Key] = cfg.Value
		} else {
			configMap[cfg.Key] = cfg.Value
		}
	}

	return &FetchConfigsResponse{Configs: configMap, Secrets: secretMap}, nil
}

// Internal_UpdateEventResolutionStatus updates the event_resolution status when a workflow execution completes.
// typeReferenceID is the full "workflowID:runID" stored at trigger time.
// This is a no-op if no event_resolution exists for the given reference.
func (a *WorkflowActivities) Internal_UpdateEventResolutionStatus(ctx context.Context, typeReferenceID string, workflowFailed bool) error {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return fmt.Errorf("failed to get DB for event resolution update: %w", err)
	}

	resolutionStatus := "Success"
	if workflowFailed {
		resolutionStatus = "Failed"
	}

	_, err = db.Db.ExecContext(ctx,
		`UPDATE event_resolution SET status = $1, updated_at = now()
		 WHERE type = 'WorkflowExecution' AND type_reference_id = $2 AND status = 'InProgress'`,
		resolutionStatus, typeReferenceID)
	if err != nil {
		return fmt.Errorf("failed to update event resolution status: %w", err)
	}
	return nil
}
