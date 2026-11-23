package workflow

import (
	"context"
	"errors"
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type fakeRunGate struct {
	calls    int
	err      error
	decision RunDecision
	// triggerCalls and runKey record CheckTriggerRun.
	triggerCalls int
	runKey       string
	// decideErr is DecideApproval's answer; decided and undelivered record
	// the calls.
	decideErr   error
	decided     []string
	undelivered []string
}

func (g *fakeRunGate) CheckRun(*security.RequestContext, string, *model.Workflow, map[string]any, model.WorkflowTrigger, map[string]any) (RunDecision, error) {
	g.calls++
	return g.decision, g.err
}

func (g *fakeRunGate) CheckTriggerRun(_ *security.RequestContext, _ string, _ *model.Workflow, _ map[string]any, _ map[string]any, runKey string) (RunDecision, error) {
	g.triggerCalls++
	g.runKey = runKey
	return g.decision, g.err
}

func (g *fakeRunGate) DecideApproval(_ *security.RequestContext, _, _, ref, status string) error {
	g.decided = append(g.decided, ref+":"+status)
	return g.decideErr
}

func (g *fakeRunGate) ApprovalNotDelivered(_ *security.RequestContext, ref string, _ error) {
	g.undelivered = append(g.undelivered, ref)
}

func (g *fakeRunGate) Binding(context.Context, string, string) (*model.WorkflowBinding, error) {
	return nil, nil
}

func boundAutomation() *model.Workflow {
	return &model.Workflow{
		ID:      "wf-bound",
		Status:  model.WorkflowStatusActive,
		Binding: &model.WorkflowBinding{Bee: "oom_fix", Name: "raise_limit", Role: model.BindingRoleAutomation},
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks:    []model.Task{{ID: "p", Type: "core.print"}},
		},
	}
}

func reasonOf(err error) string {
	var e common.Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

// Without an extension that registers a gate (the OSS build), a bound
// automation never runs, whatever started it.
func TestBoundAutomationRefusedWithoutARunGate(t *testing.T) {
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry()}
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{"acct"})
	_, err := s.runWorkflow(ctx, "acct", "wf-bound", boundAutomation(), nil, model.WorkflowTriggerManual, map[string]any{})
	require.Error(t, err)
	assert.Equal(t, "bound_automation", reasonOf(err))
}

// With a gate, its decision is the run's.
func TestBoundAutomationAsksTheRunGate(t *testing.T) {
	gate := &fakeRunGate{err: errors.New("refused by the gate")}
	s := &Service{store: &MockWorkflowStore{}, taskRegistry: tasks.NewInitializedTaskRegistry()}
	s.SetRunGate(gate)
	ctx := security.NewRequestContextForTenantAccountAdmin("t1", "u1", []string{"acct"})
	_, err := s.runWorkflow(ctx, "acct", "wf-bound", boundAutomation(), nil, model.WorkflowTriggerManual, map[string]any{})
	require.ErrorContains(t, err, "refused by the gate")
	assert.Equal(t, 1, gate.calls)
}

// The gate is built from the registered factory when the service is.
func TestRegisteredRunGateIsBuiltWithTheService(t *testing.T) {
	orig := runGateFactory
	t.Cleanup(func() { runGateFactory = orig })
	gate := &fakeRunGate{}
	var got RunGateDeps
	RegisterRunGate(func(d RunGateDeps) RunGate { got = d; return gate })
	store := &MockWorkflowStore{}
	s := NewService(nil, store, nil, tasks.NewInitializedTaskRegistry(), nil, nil)
	assert.Same(t, gate, s.runGate)
	assert.Same(t, store, got.Store)
}

func boundTrigger() *model.Workflow {
	return &model.Workflow{
		ID:      "wf-trigger",
		Status:  model.WorkflowStatusActive,
		Binding: &model.WorkflowBinding{Bee: "oom_fix", Role: model.BindingRoleTrigger},
		Definition: model.WorkflowDefinition{
			Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
			Tasks:    []model.Task{{ID: "run_bee", Type: "scripting.run_script"}},
		},
	}
}

// A person starting a bound trigger by hand: the gate decides, sees the
// Temporal id the run gets, and can make the stored owner the principal while
// the person stays the one who started it.
func TestBoundTriggerStartedByHandAsksTheGateAndRunsAsTheOwner(t *testing.T) {
	service, mockTemporal, mockStore, _ := newExecuteTestService()
	mockStore.runAs = map[string]string{"wf-trigger": "bee-user"}
	var started string
	gate := &fakeRunGate{decision: RunDecision{RunAsOwner: true, Started: func(id string) { started = id }}}
	service.SetRunGate(gate)

	var opts client.StartWorkflowOptions
	var ran *model.Workflow
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			opts = args.Get(1).(client.StartWorkflowOptions)
			ran = args.Get(3).(*model.Workflow)
		}).Return(&MockWorkflowRun{}, nil).Once()

	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "person", []string{"test-account"})
	runID, err := service.runWorkflow(ctx, "test-account", "wf-trigger", boundTrigger(), nil, model.WorkflowTriggerManual, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, gate.triggerCalls)
	assert.Equal(t, 0, gate.calls, "a trigger is not an automation")
	assert.Equal(t, opts.ID, gate.runKey, "the gate sees the Temporal id the run starts with")
	assert.Equal(t, "bee-user", ran.PrincipalUserID, "the run acts as the stored owner")
	assert.True(t, ran.RestrictToAccountConfigs, "a tenant admin's start does not give the owner's run tenant-wide configs")
	require.NotNil(t, ran.TriggeredByUser)
	assert.Equal(t, "person", ran.TriggeredByUser.ID, "the person stays the one who started it")
	assert.Equal(t, runID, started)
}

// Without RunAsOwner the requester stays the principal, as for any manual run.
func TestBoundTriggerKeepsTheRequesterWithoutRunAsOwner(t *testing.T) {
	service, mockTemporal, mockStore, _ := newExecuteTestService()
	mockStore.runAs = map[string]string{"wf-trigger": "bee-user"}
	service.SetRunGate(&fakeRunGate{})
	var ran *model.Workflow
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { ran = args.Get(3).(*model.Workflow) }).Return(&MockWorkflowRun{}, nil).Once()

	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "person", []string{"test-account"})
	_, err := service.runWorkflow(ctx, "test-account", "wf-trigger", boundTrigger(), nil, model.WorkflowTriggerManual, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "person", ran.PrincipalUserID)
	assert.False(t, ran.RestrictToAccountConfigs, "a tenant admin's own run keeps tenant-wide configs")
}

// A gate refusal stops the start; only manual starts of a trigger reach it.
func TestBoundTriggerGateRefusalAndOtherStarts(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "person", []string{"test-account"})

	service, _, _, _ := newExecuteTestService()
	gate := &fakeRunGate{err: errors.New("refused by the gate")}
	service.SetRunGate(gate)
	_, err := service.runWorkflow(ctx, "test-account", "wf-trigger", boundTrigger(), nil, model.WorkflowTriggerManual, map[string]any{})
	require.ErrorContains(t, err, "refused by the gate")

	for _, trig := range []model.WorkflowTrigger{model.WorkflowTriggerEvent, model.WorkflowTriggerSchedule} {
		service, mockTemporal, _, _ := newExecuteTestService()
		gate := &fakeRunGate{err: errors.New("must not be asked")}
		service.SetRunGate(gate)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&MockWorkflowRun{}, nil).Once()
		_, err := service.runWorkflow(ctx, "test-account", "wf-trigger", boundTrigger(), nil, trig, map[string]any{})
		require.NoError(t, err, string(trig))
		assert.Equal(t, 0, gate.triggerCalls, string(trig))
	}
}

// A run the gate allowed but Temporal did not start is reported to the gate.
func TestBoundTriggerNotStartedIsReported(t *testing.T) {
	service, mockTemporal, _, _ := newExecuteTestService()
	var cause error
	service.SetRunGate(&fakeRunGate{decision: RunDecision{NotStarted: func(err error) { cause = err }}})
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("temporal unavailable")).Once()

	ctx := security.NewRequestContextForTenantAccountAdmin("test-tenant", "person", []string{"test-account"})
	_, err := service.runWorkflow(ctx, "test-account", "wf-trigger", boundTrigger(), nil, model.WorkflowTriggerManual, map[string]any{})
	require.ErrorContains(t, err, "temporal unavailable")
	require.ErrorContains(t, cause, "temporal unavailable")
}
