package types

import (
	"context"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	temporalLog "go.temporal.io/sdk/log"
)

// TemporalTaskContext implements types.TaskContext for Temporal workflows.
type TemporalTaskContext struct {
	context         context.Context
	dataConverter   converter.DataConverter
	logger          temporalLog.Logger
	temporalClient  client.Client
	store           model.WorkflowStore
	tenantID        string
	accountID       string
	workflowID      string
	workflowRunID   string
	taskID          string
	userID          string
	workflowName    string
	userDisplayName string
	dryRun          bool
}

func (t *TemporalTaskContext) GetContext() context.Context {
	return t.context
}

func (t *TemporalTaskContext) GetLogger() temporalLog.Logger {
	return t.logger
}

func (t *TemporalTaskContext) GetTenantID() string {
	return t.tenantID
}

func (t *TemporalTaskContext) GetAccountID() string {
	return t.accountID
}

func (t *TemporalTaskContext) GetWorkflowID() string {
	return t.workflowID
}

func (t *TemporalTaskContext) GetWorkflowRunID() string {
	return t.workflowRunID
}

func (t *TemporalTaskContext) GetTaskID() string {
	return t.taskID
}

func (t *TemporalTaskContext) GetUserID() string {
	return t.userID
}

func (t *TemporalTaskContext) GetWorkflowName() string {
	return t.workflowName
}

func (t *TemporalTaskContext) GetUserDisplayName() string {
	return t.userDisplayName
}

func (t *TemporalTaskContext) GetDataConverter() converter.DataConverter {
	return t.dataConverter
}

func (t *TemporalTaskContext) GetTemporalClient() client.Client {
	return t.temporalClient
}

func (t *TemporalTaskContext) GetStore() model.WorkflowStore {
	return t.store
}

func (t *TemporalTaskContext) GetNewRequestContext() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(t.GetTenantID(), t.GetUserID(), []string{t.GetAccountID()})
}

func (t *TemporalTaskContext) GetNewRequestContextForAccount(accountId string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(t.GetTenantID(), t.GetUserID(), []string{accountId})
}

func (t *TemporalTaskContext) IsDryRun() bool {
	return t.dryRun
}

func NewTemporalTaskContextFromActivity(ctx context.Context, tenantID, accountID, workflowID, userID, workflowName, userDisplayName string, temporalClient client.Client, temporalDataConverter converter.DataConverter, store model.WorkflowStore, isDryRun bool) TaskContext {
	activityInfo := activity.GetInfo(ctx)

	tc := TemporalTaskContext{
		context:         ctx,
		dataConverter:   temporalDataConverter,
		logger:          activity.GetLogger(ctx),
		temporalClient:  temporalClient,
		store:           store,
		tenantID:        tenantID,
		accountID:       accountID,
		workflowID:      workflowID,
		workflowRunID:   activityInfo.WorkflowExecution.RunID,
		taskID:          activityInfo.ActivityID,
		userID:          userID,
		workflowName:    workflowName,
		userDisplayName: userDisplayName,
		dryRun:          isDryRun,
	}

	return &tc
}

func NewTemporalTaskContext(ctx context.Context, tenantID, accountID, workflowID, userID, workflowName, userDisplayName string, temporalClient client.Client, temporalDataConverter converter.DataConverter, store model.WorkflowStore, workflowRunId string, workflowActivityId string, logger temporalLog.Logger, isDryRun bool) TaskContext {
	tc := TemporalTaskContext{
		context:         ctx,
		dataConverter:   temporalDataConverter,
		logger:          logger,
		temporalClient:  temporalClient,
		store:           store,
		tenantID:        tenantID,
		accountID:       accountID,
		workflowID:      workflowID,
		workflowRunID:   workflowRunId,
		taskID:          workflowActivityId,
		userID:          userID,
		workflowName:    workflowName,
		userDisplayName: userDisplayName,
		dryRun:          isDryRun,
	}

	return &tc
}
