package types

import (
	"context"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

// TaskContext provides a rich context for task execution, including access to workflow state, Temporal clients, and logging.
type TaskContext interface {
	GetContext() context.Context
	GetLogger() log.Logger
	GetTenantID() string
	GetAccountID() string
	GetWorkflowID() string
	GetWorkflowRunID() string
	// GetEventID returns the ID of the event that triggered this run, or ""
	// for manual/scheduled/webhook runs.
	GetEventID() string
	GetTaskID() string
	GetUserID() string
	GetWorkflowName() string
	GetUserDisplayName() string
	GetDataConverter() converter.DataConverter
	GetTemporalClient() client.Client
	GetStore() model.WorkflowStore
	GetNewRequestContext() *security.RequestContext
	GetNewRequestContextForAccount(account string) *security.RequestContext
	IsDryRun() bool
}
