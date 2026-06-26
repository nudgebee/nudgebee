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
	// ListCallers returns every workflow whose definition contains a
	// core.call-workflow task referencing `calleeName`. Used by the UI to
	// surface a "where used" warning before deletion / rename so users don't
	// silently break call chains. Templated workflow_name values (containing
	// `{{ ... }}`) can't be matched statically and are returned only when the
	// literal name appears in the field.
	ListCallers(ctx context.Context, tenantID, accountID, calleeName string) ([]WorkflowCaller, error)
	Update(ctx context.Context, tenantID, accountID, id string, wf Workflow) error
	// UpdateInternal persists a workflow's definition WITHOUT touching the audit
	// columns (updated_by / updated_at). Use for internal touch-ups that are not
	// user edits — e.g. injecting a generated webhook secret during trigger
	// registration — so the "last edited by / at" trail isn't corrupted by the
	// triggering request's identity. Genuine user edits must use Update.
	UpdateInternal(ctx context.Context, tenantID, accountID, id string, wf Workflow) error
	Delete(ctx context.Context, tenantID, accountID, id string) error
	// UpdateWorkflowStatus flips workflows.status directly (legacy path used for
	// workflows without a live_version_id) and bumps updated_at / updated_by in
	// the same write. Empty updatedBy → nil-UUID system user (background paths).
	UpdateWorkflowStatus(ctx context.Context, tenantID, accountID, id, updatedBy string, status WorkflowStatus) error
	GetState(ctx context.Context, workflowID string) ([]WorkflowStateItem, error)
	SetState(ctx context.Context, workflowID string, updates []WorkflowStateUpdate) error
	DeleteExpiredState(ctx context.Context, limit int) (int64, error)
	SetLastExecutionStatus(ctx context.Context, tenantID, accountID, id string, status WorkflowExecutionStatus, executionTime time.Time, statusMessage string) error
	CountWorkflows(ctx context.Context, tenantID, accountID string, status WorkflowStatus, triggerType string) (int64, error)
	GetWorkflowNames(ctx context.Context, tenantID, accountID string, ids []string) (map[string]string, error)
	ListWorkflowVersions(ctx context.Context, workflowID string, limit int) ([]WorkflowVersion, error)
	GetWorkflowVersion(ctx context.Context, workflowID string, versionNumber int) (*WorkflowVersion, error)
	GetWorkflowVersionByID(ctx context.Context, versionID string) (*WorkflowVersion, error)
	GetLiveWorkflowVersion(ctx context.Context, workflowID string) (*WorkflowVersion, error)
	PublishVersion(ctx context.Context, workflowID, createdBy string, source WorkflowVersionSource, name, description *string, restoredFromVersion *int, status WorkflowStatus) (*WorkflowVersion, error)
	// SetLiveVersion flips workflows.live_version_id (and mirrors the version's
	// status onto workflows.status) plus bumps updated_at / updated_by, so a
	// publish / restore registers as a user-initiated update. Empty updatedBy
	// → nil-UUID system user.
	SetLiveVersion(ctx context.Context, tenantID, accountID, workflowID, versionID, updatedBy string) error
	SetDraftVersionID(ctx context.Context, tenantID, accountID, workflowID, versionID string) error
	DeleteWorkflowVersion(ctx context.Context, tenantID, accountID, workflowID, versionID string) error
	UpdateVersionMetadata(ctx context.Context, workflowID string, versionNumber int, name, description *string) (*WorkflowVersion, error)
	// UpdateVersionStatus writes a new status onto a single workflow_versions row
	// and, when the target is the live version, mirrors workflows.status (plus
	// updated_at / updated_by) in the same transaction. updatedBy may be empty —
	// the DAO substitutes the nil-UUID system user so background callers don't
	// need to fabricate one. Returns wasLive so the service layer can decide
	// whether to re-register schedule/webhook triggers.
	UpdateVersionStatus(ctx context.Context, tenantID, accountID, workflowID, versionID, updatedBy string, status WorkflowStatus) (wasLive bool, err error)
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
