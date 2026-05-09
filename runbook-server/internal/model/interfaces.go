package model

import (
	"context"
	"time"
)

// WorkflowStore defines the interface for storing and retrieving workflow definitions and state.
type WorkflowStore interface {
	Save(ctx context.Context, tenantID, accountID string, wf Workflow) (string, error)
	List(ctx context.Context, tenantID, accountID string, request ListWorkflowRequest) ([]Workflow, int, error)
	Find(ctx context.Context, tenantID, accountID, id string) (*Workflow, error)
	FindByName(ctx context.Context, tenantID, accountID, name string) (*Workflow, error)
	FindByIntegrationName(ctx context.Context, tenantID, accountID, integrationName string) (*Workflow, error)
	Update(ctx context.Context, tenantID, accountID, id string, wf Workflow) error
	Delete(ctx context.Context, tenantID, accountID, id string) error
	UpdateWorkflowStatus(ctx context.Context, tenantID, accountID, id string, status WorkflowStatus) error
	GetState(ctx context.Context, workflowID string) ([]WorkflowStateItem, error)
	SetState(ctx context.Context, workflowID string, updates []WorkflowStateUpdate) error
	DeleteExpiredState(ctx context.Context, limit int) (int64, error)
	SetLastExecutionStatus(ctx context.Context, tenantID, accountID, id string, status WorkflowExecutionStatus, executionTime time.Time, statusMessage string) error
	CountWorkflows(ctx context.Context, tenantID, accountID string, status WorkflowStatus, triggerType string) (int64, error)
}

// WorkflowTemplateStore defines the interface for storing and retrieving global workflow templates.
type WorkflowTemplateStore interface {
	ListGlobal(ctx context.Context, request ListWorkflowTemplateRequest) ([]WorkflowTemplate, int, error)
	FindGlobal(ctx context.Context, id string) (*WorkflowTemplate, error)
}

// WorkflowStateUpdate is a helper struct for updating workflow state.
type WorkflowStateUpdate struct {
	Key         string
	Value       any
	ExecutionID string
	TaskID      string
	ExpiresAt   *time.Time
}
