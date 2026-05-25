package model

import (
	"context"
	"time"
)

// WorkflowStore defines the interface for storing and retrieving workflow definitions and state.
//
// Versioning model:
//   - CreateWorkflowWithInitialVersion inserts the workflow, snapshots v1, and
//     sets it live — atomically, in one transaction.
//   - Update mutates workflows.definition (the DRAFT). It does not write a
//     workflow_versions row; that only happens via PublishVersion.
//   - PublishVersion snapshots the current draft and prunes back to
//     MaxWorkflowVersionsPerWorkflow (excluding the live version).
//   - SetLiveVersion is a pointer flip on workflows.live_version_id; it MUST NOT
//     touch the draft or workflows.status. This is what protects unpublished
//     edits during a rollback.
type WorkflowStore interface {
	CreateWorkflowWithInitialVersion(ctx context.Context, tenantID, accountID string, wf Workflow) (workflowID string, version *WorkflowVersion, err error)
	List(ctx context.Context, tenantID, accountID string, request ListWorkflowRequest) ([]Workflow, int, error)
	Find(ctx context.Context, tenantID, accountID, id string) (*Workflow, error)
	FindByName(ctx context.Context, tenantID, accountID, name string) (*Workflow, error)
	FindByIntegrationName(ctx context.Context, tenantID, accountID, integrationName string) (*Workflow, error)
	// ListByIntegrationName returns every Active workflow whose definition.triggers
	// contains a webhook trigger with params.integration_name == integrationName,
	// scoped to the tenant only (no account filter). Used for tenant-wide webhook
	// fan-out where one integration can route to subscribers across accounts.
	ListByIntegrationName(ctx context.Context, tenantID, integrationName string) ([]Workflow, error)
	Update(ctx context.Context, tenantID, accountID, id string, wf Workflow) error
	Delete(ctx context.Context, tenantID, accountID, id string) error
	UpdateWorkflowStatus(ctx context.Context, tenantID, accountID, id string, status WorkflowStatus) error
	GetState(ctx context.Context, workflowID string) ([]WorkflowStateItem, error)
	SetState(ctx context.Context, workflowID string, updates []WorkflowStateUpdate) error
	DeleteExpiredState(ctx context.Context, limit int) (int64, error)
	SetLastExecutionStatus(ctx context.Context, tenantID, accountID, id string, status WorkflowExecutionStatus, executionTime time.Time, statusMessage string) error
	CountWorkflows(ctx context.Context, tenantID, accountID string, status WorkflowStatus, triggerType string) (int64, error)
	ListWorkflowVersions(ctx context.Context, workflowID string, limit int) ([]WorkflowVersion, error)
	GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*WorkflowVersion, error)
	GetWorkflowVersionByID(ctx context.Context, versionID string) (*WorkflowVersion, error)
	GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*WorkflowVersion, error)
	PublishVersion(ctx context.Context, workflowID, createdBy string, source WorkflowVersionSource, name, description *string, restoredFromVersion *int) (*WorkflowVersion, error)
	SetLiveVersion(ctx context.Context, tenantID, accountID, workflowID, versionID string) error
	UpdateVersionMetadata(ctx context.Context, workflowID string, versionNumber int, name, description *string) (*WorkflowVersion, error)
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
