package workflow

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/mocks"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"
)

var cardToken = hex.EncodeToString([]byte("task-token")) + ":acct:temporal-1:exec-1:ask"

// tokenApprovalService answers a card's approval on a run of the given tenant
// and account, in the given state, waiting on cardToken, and records what the
// approval completed with. A nil describeErr means the run exists.
func tokenApprovalService(t *testing.T, tenantID, accountID string, status enumspb.WorkflowExecutionStatus, describeErr error) (*Service, *mocks.Client, *map[string]any) {
	t.Helper()
	dc := converter.GetDefaultDataConverter()
	attrs := map[string]*commonpb.Payload{}
	for k, v := range map[string]string{model.SearchAttrTenantID: tenantID, model.SearchAttrAccountID: accountID} {
		p, err := dc.ToPayload(v)
		require.NoError(t, err)
		attrs[k] = p
	}
	temporal := mocks.NewClient(t)
	var resp *workflowservice.DescribeWorkflowExecutionResponse
	if describeErr == nil {
		resp = &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Status:           status,
			SearchAttributes: &commonpb.SearchAttributes{IndexedFields: attrs},
		}}
	}
	temporal.On("DescribeWorkflowExecution", mock.Anything, "temporal-1", "exec-1").Return(resp, describeErr).Maybe()
	temporal.On("ListWorkflow", mock.Anything, mock.Anything).Return(&workflowservice.ListWorkflowExecutionsResponse{}, nil).Maybe()
	pending := mocks.NewEncodedValue(t)
	pending.On("Get", mock.Anything).Run(func(args mock.Arguments) { *args.Get(0).(*string) = cardToken }).Return(nil).Maybe()
	temporal.On("QueryWorkflow", mock.Anything, "temporal-1", "exec-1", "getApprovalToken", "ask").Return(pending, nil).Maybe()
	output := &map[string]any{}
	temporal.On("CompleteActivity", mock.Anything, []byte("task-token"), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { *output = args.Get(2).(map[string]any) }).Return(nil).Maybe()
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry(), temporalClient: temporal, dataConverter: dc}
	return s, temporal, output
}

func codeOf(t *testing.T, err error) int {
	t.Helper()
	var commonErr common.Error
	require.True(t, errors.As(err, &commonErr), "want a common.Error, got %v", err)
	return commonErr.Code
}

func TestTokenApprovalRecordsTheCaller(t *testing.T) {
	s, _, output := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	require.NoError(t, s.CompleteApprovalTaskByToken(person(), cardToken, "approve"))
	assert.Equal(t, "approve", (*output)["status"])
	assert.Equal(t, "person-1", (*output)["approver"])
}

func TestTokenApprovalRefusesAnotherTenantsRun(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t2", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	assert.Equal(t, 404, codeOf(t, s.CompleteApprovalTaskByToken(person(), cardToken, "approve")))
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalRefusesARunOfAnotherAccount(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "other-acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	assert.Equal(t, 404, codeOf(t, s.CompleteApprovalTaskByToken(person(), cardToken, "approve")))
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalRefusesACallerWithoutAccess(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	outsider := security.NewRequestContextForTenantAccountAdmin("t1", "person-2", []string{"another-acct"})
	assert.Equal(t, 403, codeOf(t, s.CompleteApprovalTaskByToken(outsider, cardToken, "approve")))
	temporal.AssertNotCalled(t, "DescribeWorkflowExecution", mock.Anything, mock.Anything, mock.Anything)
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalRefusesTheRunGate(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	gateToken := hex.EncodeToString([]byte("task-token")) + ":acct:temporal-1:exec-1:" + model.ApprovalGateTaskID
	assert.Equal(t, 403, codeOf(t, s.CompleteApprovalTaskByToken(person(), gateToken, "approve")))
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalRefusesAMalformedToken(t *testing.T) {
	s, _, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	for _, token := range []string{"abcd", "abcd:acct:temporal-1:exec-1", "abcd::temporal-1:exec-1:ask"} {
		assert.Equal(t, 400, codeOf(t, s.CompleteApprovalTaskByToken(person(), token, "approve")), token)
	}
}

func TestTokenApprovalOfAFinishedRun(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, nil)
	assert.Equal(t, 409, codeOf(t, s.CompleteApprovalTaskByToken(person(), cardToken, "approve")))
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalOfAMissingRun(t *testing.T) {
	s, _, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, serviceerror.NewNotFound("gone"))
	assert.Equal(t, 404, codeOf(t, s.CompleteApprovalTaskByToken(person(), cardToken, "approve")))
}

// A task token from another run, put in front of a run the caller may approve,
// completes nothing.
func TestTokenApprovalRefusesATaskTokenFromAnotherRun(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	spliced := hex.EncodeToString([]byte("someone-elses-task")) + ":acct:temporal-1:exec-1:ask"
	assert.Equal(t, 404, codeOf(t, s.CompleteApprovalTaskByToken(person(), spliced, "approve")))
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalOfAStepNotWaitingIsNotFound(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	notWaiting := hex.EncodeToString([]byte("task-token")) + ":acct:temporal-1:exec-1:other"
	temporal.On("QueryWorkflow", mock.Anything, "temporal-1", "exec-1", "getApprovalToken", "other").
		Return(nil, serviceerror.NewQueryFailed("no pending approval found for task ID: other"))
	assert.Equal(t, 404, codeOf(t, s.CompleteApprovalTaskByToken(person(), notWaiting, "approve")))
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestTokenApprovalWithoutACallerIsRefused(t *testing.T) {
	s, temporal, _ := tokenApprovalService(t, "t1", "acct", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil)
	for _, ctx := range []*security.RequestContext{nil, {}} {
		assert.Equal(t, 403, codeOf(t, s.CompleteApprovalTaskByToken(ctx, cardToken, "approve")))
	}
	temporal.AssertNotCalled(t, "DescribeWorkflowExecution", mock.Anything, mock.Anything, mock.Anything)
	temporal.AssertNotCalled(t, "CompleteActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
