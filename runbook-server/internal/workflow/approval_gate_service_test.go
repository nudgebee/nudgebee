package workflow

import (
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/mocks"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"
)

// A gated run starts with the gate in front of its steps, its reference in
// the memo, and time for the approval on top of its own; the workflow it was
// given keeps its definition.
func TestRunWorkflowStartsAGatedRun(t *testing.T) {
	temporal := mocks.NewClient(t)
	var opts client.StartWorkflowOptions
	var started *model.Workflow
	run := mocks.NewWorkflowRun(t)
	run.On("GetRunID").Return("exec-1")
	temporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			opts = args.Get(1).(client.StartWorkflowOptions)
			started = args.Get(3).(*model.Workflow)
		}).Return(run, nil)

	gate := &fakeRunGate{decision: RunDecision{Approval: &RunApproval{Ref: "run-1/entry-1", Timeout: 4 * time.Hour, Message: "approve?"}}}
	var recorded string
	gate.decision.Started = func(id string) { recorded = id }
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry(), temporalClient: temporal}
	s.SetRunGate(gate)
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "bee-1", []string{"acct"})

	wf := boundAutomation()
	stored := wf.Definition
	id, err := s.runWorkflow(ctx, "acct", "wf-bound", wf, map[string]any{model.MemoWorkflowVersionID: "v1"}, model.WorkflowTriggerManual, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "exec-1", id)
	assert.Equal(t, "exec-1", recorded)

	require.NotNil(t, started)
	assert.Equal(t, "run-1/entry-1", started.ApprovalGate)
	require.Len(t, started.Definition.Tasks, 2)
	assert.Equal(t, model.ApprovalGateTaskID, started.Definition.Tasks[0].ID)
	assert.Equal(t, []string{model.ApprovalGateTaskID}, started.Definition.Tasks[1].DependsOn)
	assert.Equal(t, "run-1/entry-1", opts.Memo[model.MemoApprovalGate])
	assert.Equal(t, 5*time.Hour, opts.WorkflowExecutionTimeout, "the run's own hour after up to four for the approval")
	assert.Len(t, stored.Tasks, 1, "the stored definition is not changed")
	assert.Empty(t, stored.Tasks[0].DependsOn)
}

// A run without a gate never carries one, whatever the workflow or memo it was
// handed said.
func TestRunWorkflowClearsAnUnaskedGate(t *testing.T) {
	temporal := mocks.NewClient(t)
	var opts client.StartWorkflowOptions
	var started *model.Workflow
	run := mocks.NewWorkflowRun(t)
	run.On("GetRunID").Return("exec-1")
	temporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			opts = args.Get(1).(client.StartWorkflowOptions)
			started = args.Get(3).(*model.Workflow)
		}).Return(run, nil)
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry(), temporalClient: temporal}
	s.SetRunGate(&fakeRunGate{})
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "bee-1", []string{"acct"})

	wf := boundAutomation()
	wf.ApprovalGate = "forged/ref"
	_, err := s.runWorkflow(ctx, "acct", "wf-bound", wf, map[string]any{model.MemoApprovalGate: "forged/ref"}, model.WorkflowTriggerManual, map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, started.ApprovalGate)
	assert.NotContains(t, opts.Memo, model.MemoApprovalGate)
	assert.Len(t, started.Definition.Tasks, 1)
}

// uiApprovalService answers the approval of task taskID on a running
// execution whose memo carries gateRef ("" for none), and records what the
// approval completed with.
func uiApprovalService(t *testing.T, gate *fakeRunGate, gateRef string, completeErr error) (*Service, *map[string]any) {
	t.Helper()
	dc := converter.GetDefaultDataConverter()
	temporal := mocks.NewClient(t)
	temporal.On("ListWorkflow", mock.Anything, mock.Anything).Return(&workflowservice.ListWorkflowExecutionsResponse{
		Executions: []*workflowpb.WorkflowExecutionInfo{{Execution: &commonpb.WorkflowExecution{WorkflowId: "temporal-1", RunId: "exec-1"}}},
	}, nil)
	memo := &commonpb.Memo{Fields: map[string]*commonpb.Payload{}}
	if gateRef != "" {
		p, err := dc.ToPayload(gateRef)
		require.NoError(t, err)
		memo.Fields[model.MemoApprovalGate] = p
	}
	temporal.On("DescribeWorkflowExecution", mock.Anything, "temporal-1", "exec-1").Return(&workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, Memo: memo},
	}, nil)
	token := mocks.NewEncodedValue(t)
	token.On("Get", mock.Anything).Run(func(args mock.Arguments) {
		*args.Get(0).(*string) = hex.EncodeToString([]byte("task-token"))
	}).Return(nil)
	temporal.On("QueryWorkflow", mock.Anything, "temporal-1", "exec-1", "getApprovalToken", mock.Anything).Return(token, nil)
	output := &map[string]any{}
	temporal.On("CompleteActivity", mock.Anything, []byte("task-token"), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { *output = args.Get(2).(map[string]any) }).Return(completeErr).Maybe()
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry(), temporalClient: temporal, dataConverter: dc}
	s.SetRunGate(gate)
	return s, output
}

func person() *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin("t1", "person-1", []string{"acct"})
}

func TestUIApprovalOfARunsGateGoesThroughTheRunGate(t *testing.T) {
	gate := &fakeRunGate{}
	s, output := uiApprovalService(t, gate, "run-1/entry-1", nil)
	require.NoError(t, s.CompleteApprovalTaskFromUI(person(), "acct", "wf-1", "exec-1", model.ApprovalGateTaskID, "approve", ""))
	assert.Equal(t, []string{"run-1/entry-1:approve"}, gate.decided)
	assert.Equal(t, "run-1/entry-1", (*output)[approvalGateOutputRef])
	assert.Equal(t, "person-1", (*output)["approver"])
	assert.Empty(t, gate.undelivered)
}

func TestUIApprovalOfARunsGateRefusedByTheRunGate(t *testing.T) {
	gate := &fakeRunGate{decideErr: errors.New("you may not approve this run")}
	s, output := uiApprovalService(t, gate, "run-1/entry-1", nil)
	err := s.CompleteApprovalTaskFromUI(person(), "acct", "wf-1", "exec-1", model.ApprovalGateTaskID, "approve", "")
	require.ErrorContains(t, err, "may not approve")
	assert.Empty(t, *output, "the answer never reached the run")
}

func TestUIApprovalOfARunsGateThatDoesNotReachTheRun(t *testing.T) {
	gate := &fakeRunGate{}
	s, _ := uiApprovalService(t, gate, "run-1/entry-1", errors.New("temporal unavailable"))
	err := s.CompleteApprovalTaskFromUI(person(), "acct", "wf-1", "exec-1", model.ApprovalGateTaskID, "approve", "")
	require.Error(t, err)
	assert.Equal(t, []string{"run-1/entry-1"}, gate.undelivered, "the decision can be made again")
}

func TestUIApprovalOfTheGateIDWithoutAGateIsRefused(t *testing.T) {
	gate := &fakeRunGate{}
	s, output := uiApprovalService(t, gate, "", nil)
	err := s.CompleteApprovalTaskFromUI(person(), "acct", "wf-1", "exec-1", model.ApprovalGateTaskID, "approve", "")
	require.Error(t, err)
	assert.Empty(t, gate.decided)
	assert.Empty(t, *output)
}

// Any other approval completes as it always has: no run gate, no gate ref.
func TestUIApprovalOfAnOrdinaryStepIsUnchanged(t *testing.T) {
	gate := &fakeRunGate{}
	s, output := uiApprovalService(t, gate, "run-1/entry-1", nil)
	require.NoError(t, s.CompleteApprovalTaskFromUI(person(), "acct", "wf-1", "exec-1", "ask", "reject", "no"))
	assert.Empty(t, gate.decided)
	assert.Equal(t, map[string]any{"status": "reject", "approver": "person-1", "comments": "no"}, *output)
}
