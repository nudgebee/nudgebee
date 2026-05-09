package entitlement

import (
	"context"
	"log/slog"
	"time"
)

// WorkflowCounter handles workflow execution and AI step counting

// AITaskTypes defines which task types count as AI workflow steps
var AITaskTypes = map[string]bool{
	"llm.event_investigate": true,
	"llm.nubi":              true,
	"llm.summary":           true,
	"llm.investigate":       true,
	"ai.router":             true,
	"ai.mcp":                true,
	"ai.llm":                true,
	"ai.analyze":            true,
}

// IsAITaskType checks if a task type counts as an AI workflow step
func IsAITaskType(taskType string) bool {
	return AITaskTypes[taskType]
}

// CheckAndRecordWorkflowExecution checks entitlement and records a workflow execution
// Returns: (allowed, status, error)
func (s *Service) CheckAndRecordWorkflowExecution(ctx context.Context, tenantID, workflowID string) (bool, *EntitlementStatus, error) {
	// 1. Check entitlement for workflow executions
	status, err := s.CheckEntitlement(ctx, tenantID, DimensionWorkflowExecutions)
	if err != nil {
		return false, nil, err
	}

	// 2. If not allowed (limit reached, no overage), return
	if !status.Allowed && !status.OverageEnabled {
		return false, status, nil
	}

	// 3. Record the usage
	isBillable := true
	_, err = s.RecordUsage(ctx, RecordUsageRequest{
		TenantID:      tenantID,
		Dimension:     DimensionWorkflowExecutions,
		ReferenceID:   workflowID,
		ReferenceType: strPtr("workflow"),
		IsBillable:    &isBillable,
	})
	if err != nil {
		slog.Error("Failed to record workflow execution usage", "tenantID", tenantID, "workflowID", workflowID, "error", err)
		// Don't fail the request, just log the error
	}

	return true, status, nil
}

// CheckAndRecordAIWorkflowStep checks entitlement and records an AI workflow step
// Returns: (allowed, status, error)
func (s *Service) CheckAndRecordAIWorkflowStep(ctx context.Context, tenantID, workflowID, taskID, taskType string) (bool, *EntitlementStatus, error) {
	// Only count AI task types
	if !IsAITaskType(taskType) {
		return true, &EntitlementStatus{Allowed: true, Message: "Non-AI task (not counted)"}, nil
	}

	// 1. Check entitlement for AI workflow steps
	status, err := s.CheckEntitlement(ctx, tenantID, DimensionAIWorkflowSteps)
	if err != nil {
		return false, nil, err
	}

	// 2. If not allowed (limit reached, no overage), return
	if !status.Allowed && !status.OverageEnabled {
		return false, status, nil
	}

	// 3. Record the usage
	isBillable := true
	sessionID := workflowID // Group by workflow
	_, err = s.RecordUsage(ctx, RecordUsageRequest{
		TenantID:      tenantID,
		Dimension:     DimensionAIWorkflowSteps,
		ReferenceID:   taskID,
		ReferenceType: strPtr("workflow_task"),
		SessionID:     &sessionID,
		IsBillable:    &isBillable,
	})
	if err != nil {
		slog.Error("Failed to record AI workflow step usage", "tenantID", tenantID, "taskID", taskID, "error", err)
		// Don't fail the request, just log the error
	}

	return true, status, nil
}

// CheckWorkflowAccess checks if a tenant has access to workflows (feature flag only)
func (s *Service) CheckWorkflowAccess(ctx context.Context, tenantID string) (bool, error) {
	// Just check the feature flag - no usage counting
	status, err := s.CheckEntitlement(ctx, tenantID, DimensionWorkflowExecutions)
	if err != nil {
		return false, err
	}

	// For access check, we only care if the feature is enabled
	// If graceful degradation is happening, user can still view workflows
	return status.Allowed || status.GracefulDegrade, nil
}

// CheckAIStepAccess checks if a tenant can execute AI steps
func (s *Service) CheckAIStepAccess(ctx context.Context, tenantID string) (bool, *EntitlementStatus, error) {
	status, err := s.CheckEntitlement(ctx, tenantID, DimensionAIWorkflowSteps)
	if err != nil {
		return false, nil, err
	}

	return status.Allowed, status, nil
}

// GetWorkflowUsage returns workflow execution and AI step usage for a tenant
func (s *Service) GetWorkflowUsage(ctx context.Context, tenantID string) (executions, execLimit, aiSteps, aiLimit int, err error) {
	billingPeriod := getFirstDayOfMonth(time.Now())

	executions, err = s.getCurrentUsage(ctx, tenantID, DimensionWorkflowExecutions, billingPeriod)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	execLimit, err = s.getEffectiveLimit(ctx, tenantID, DimensionWorkflowExecutions)
	if err != nil {
		execLimit = -1
	}

	aiSteps, err = s.getCurrentUsage(ctx, tenantID, DimensionAIWorkflowSteps, billingPeriod)
	if err != nil {
		return executions, execLimit, 0, 0, err
	}

	aiLimit, err = s.getEffectiveLimit(ctx, tenantID, DimensionAIWorkflowSteps)
	if err != nil {
		aiLimit = -1
	}

	return executions, execLimit, aiSteps, aiLimit, nil
}
