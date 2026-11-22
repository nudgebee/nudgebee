package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

const testGateRef = "run-1/entry-1"

// approvalThenStep is a definition with an approval, a step after it, and
// failure and always hooks: the shape a normal workflow uses an approval in.
func approvalThenStep() model.WorkflowDefinition {
	return model.WorkflowDefinition{
		Tasks: []model.Task{
			{ID: "ask", Type: "core.approval", Params: map[string]any{"message": "go?", "approval_options": []any{"approve", "reject"}}},
			{ID: "after", Type: "core.print", DependsOn: []string{"ask"}, Params: map[string]any{"message": "after-ran"}},
		},
		Hooks: &model.Hooks{
			Failure: []model.Action{{Type: "test.hook", Params: map[string]any{"message": "failure"}}},
			Always:  []model.Action{{Type: "test.hook", Params: map[string]any{"message": "always"}}},
		},
		Output: map[string]any{
			"ask":   "{{ Tasks['ask'].status }}/{{ Tasks['ask'].output.status | default('-') }}",
			"after": "{{ Tasks['after'].status | default('-') }}/{{ Tasks['after'].output.data | default('-') }}",
		},
	}
}

// gatedRun is the definition of a gated run of one step, as runWorkflow
// builds it.
func gatedRun(t *testing.T, def model.WorkflowDefinition) *model.Workflow {
	t.Helper()
	gated, err := withApprovalGate(def, &RunApproval{Ref: testGateRef, Timeout: time.Hour, Message: "approve?"})
	require.NoError(t, err)
	return &model.Workflow{ID: "wf-gated", TenantID: "test-tenant", AccountID: "test-account", Definition: gated, ApprovalGate: testGateRef}
}

func oneStep() model.WorkflowDefinition {
	return model.WorkflowDefinition{
		Tasks: []model.Task{{ID: "act", Type: "core.print", Params: map[string]any{"message": "acted"}}},
		Hooks: &model.Hooks{
			Failure: []model.Action{{Type: "test.hook", Params: map[string]any{"message": "failure"}}},
			Always:  []model.Action{{Type: "test.hook", Params: map[string]any{"message": "always"}}},
		},
		Output: map[string]any{"act": "{{ Tasks['act'].status | default('-') }}"},
	}
}

// gateEnv registers the steps; approval answers with output (or err), and
// records the params it got. It returns the steps and hooks that ran.
func (s *ExecutorTestSuite) gateEnv(output map[string]any, err error) (ran *[]string, approvalParams *map[string]any) {
	ran = &[]string{}
	approvalParams = &map[string]any{}
	// Every activity is registered before any is mocked.
	s.registerFanInActivities()
	s.env.RegisterActivityWithOptions(func(context.Context, map[string]any) (any, error) { return nil, nil }, activity.RegisterOptions{Name: "test.hook"})
	s.env.RegisterActivityWithOptions(func(context.Context, map[string]any) (any, error) { return nil, nil }, activity.RegisterOptions{Name: "core.approval"})
	s.env.OnActivity("core.print", mock.Anything, mock.Anything).Return(func(_ context.Context, p map[string]any) (any, error) {
		msg, _ := p["message"].(string)
		*ran = append(*ran, "step:"+msg)
		return map[string]string{"data": msg}, nil
	}).Maybe()
	s.env.OnActivity("test.hook", mock.Anything, mock.Anything).Return(func(_ context.Context, p map[string]any) (any, error) {
		msg, _ := p["message"].(string)
		*ran = append(*ran, "hook:"+msg)
		return nil, nil
	}).Maybe()
	s.env.OnActivity("core.approval", mock.Anything, mock.Anything).Return(func(_ context.Context, p map[string]any) (any, error) {
		*approvalParams = p
		if err != nil {
			return nil, err
		}
		return output, nil
	})
	return ran, approvalParams
}

func (s *ExecutorTestSuite) run(wf *model.Workflow) (string, error) {
	executor := s.newFanInExecutor()
	s.env.RegisterWorkflow(executor.ExecuteWorkflowInternal)
	s.env.ExecuteWorkflow(executor.ExecuteWorkflowInternal, wf, nil)
	s.True(s.env.IsWorkflowCompleted())
	if err := s.env.GetWorkflowError(); err != nil {
		return "", err
	}
	var result string
	s.NoError(s.env.GetWorkflowResult(&result))
	return result, nil
}

// A normal workflow's approval is unchanged: whatever the answer, the step
// after it runs and the run completes, and the approval gets no gate mark.
// The expected results are what main produced before approval gates existed.
func (s *ExecutorTestSuite) TestNormalApprovalUnchangedOnApprove() {
	ran, params := s.gateEnv(map[string]any{"status": "approve", "approver": ""}, nil)
	result, err := s.run(&model.Workflow{ID: "wf-normal", TenantID: "test-tenant", AccountID: "test-account", Definition: approvalThenStep()})
	s.Require().NoError(err)
	s.JSONEq(`{"ask":"COMPLETED/approve","after":"COMPLETED/after-ran"}`, result)
	s.Equal([]string{"step:after-ran", "hook:always"}, *ran)
	s.NotContains(*params, "_approval_gate")
}

func (s *ExecutorTestSuite) TestNormalApprovalUnchangedOnReject() {
	ran, params := s.gateEnv(map[string]any{"status": "reject", "approver": ""}, nil)
	result, err := s.run(&model.Workflow{ID: "wf-normal", TenantID: "test-tenant", AccountID: "test-account", Definition: approvalThenStep()})
	s.Require().NoError(err)
	s.JSONEq(`{"ask":"COMPLETED/reject","after":"COMPLETED/after-ran"}`, result)
	s.Equal([]string{"step:after-ran", "hook:always"}, *ran)
	s.NotContains(*params, "_approval_gate")
}

// A step named like the gate in a run without one is a normal approval: the
// run carries no gate, so its answer is not checked as one.
func (s *ExecutorTestSuite) TestUngatedRunIgnoresTheReservedID() {
	def := approvalThenStep()
	def.Tasks[0].ID = model.ApprovalGateTaskID
	def.Tasks[1].DependsOn = []string{model.ApprovalGateTaskID}
	def.Output = map[string]any{"after": "{{ Tasks['after'].status | default('-') }}"}
	ran, params := s.gateEnv(map[string]any{"status": "reject", "approver": ""}, nil)
	result, err := s.run(&model.Workflow{ID: "wf-normal", TenantID: "test-tenant", AccountID: "test-account", Definition: def})
	s.Require().NoError(err)
	s.JSONEq(`{"after":"COMPLETED"}`, result)
	s.Equal([]string{"step:after-ran", "hook:always"}, *ran)
	s.NotContains(*params, "_approval_gate")
}

// An approval made in the app (a person, carrying the gate's reference) lets
// the run's steps go on; the gate is marked as the run's own.
func (s *ExecutorTestSuite) TestGatedRunGoesOnWhenApprovedInTheApp() {
	ran, params := s.gateEnv(map[string]any{"status": "approve", "approver": "person-1", approvalGateOutputRef: testGateRef}, nil)
	result, err := s.run(gatedRun(s.T(), oneStep()))
	s.Require().NoError(err)
	s.JSONEq(`{"act":"COMPLETED"}`, result)
	s.Equal([]string{"step:acted", "hook:always"}, *ran, "the step ran, then the always hook; no failure hook")
	s.Equal(testGateRef, (*params)["_approval_gate"])
	s.Equal("in_app", (*params)["approval_type"])
}

// Anything but an approval made in the app stops the run before its steps and
// skips its hooks.
func (s *ExecutorTestSuite) TestGatedRunStopsOnAnythingButAnAppApproval() {
	cases := map[string]map[string]any{
		"rejected in the app":         {"status": "reject", "approver": "person-1", approvalGateOutputRef: testGateRef},
		"approved by a token alone":   {"status": "approve", "approver": ""},
		"token naming the gate's ref": {"status": "approve", "approver": "", approvalGateOutputRef: testGateRef},
		"approved as the system user": {"status": "approve", "approver": security.GetSystemUserId(), approvalGateOutputRef: testGateRef},
		"approved without the ref":    {"status": "approve", "approver": "person-1"},
		"approved with another ref":   {"status": "approve", "approver": "person-1", approvalGateOutputRef: "run-2/entry-9"},
	}
	for name, output := range cases {
		s.Run(name, func() {
			s.SetupTest()
			ran, _ := s.gateEnv(output, nil)
			_, err := s.run(gatedRun(s.T(), oneStep()))
			s.Require().Error(err)
			var appErr *temporal.ApplicationError
			s.Require().True(errors.As(err, &appErr), err)
			s.Equal(ErrTypeApprovalRejected, appErr.Type())
			s.Empty(*ran, "no step and no hook ran")
		})
	}
}

// The gate's text is shown as it was written, never rendered: a run asked to
// act on input that looks like a template still reaches its gate, and a
// rejection still runs no hook.
func (s *ExecutorTestSuite) TestGatedRunDoesNotRenderItsGate() {
	ran, params := s.gateEnv(map[string]any{"status": "reject", "approver": "person-1", approvalGateOutputRef: testGateRef}, nil)
	wf := gatedRun(s.T(), oneStep())
	wf.Definition.Tasks[0].Params["message"] = `asks to run x. Inputs: note="{{ oops" secret="{{ Secrets.api_key }}" {% if %}`
	_, err := s.run(wf)
	s.Require().Error(err)
	var appErr *temporal.ApplicationError
	s.Require().True(errors.As(err, &appErr), err)
	s.Equal(ErrTypeApprovalRejected, appErr.Type(), "the run reached its gate")
	s.Equal(`asks to run x. Inputs: note="{{ oops" secret="{{ Secrets.api_key }}" {% if %}`, (*params)["message"])
	s.Empty(*ran, "no step and no hook ran")
}

// No answer in time stops the run as expired, with no step and no hook.
func (s *ExecutorTestSuite) TestGatedRunExpires() {
	ran, _ := s.gateEnv(nil, temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil))
	_, err := s.run(gatedRun(s.T(), oneStep()))
	s.Require().Error(err)
	var appErr *temporal.ApplicationError
	s.Require().True(errors.As(err, &appErr), err)
	s.Equal(ErrTypeApprovalExpired, appErr.Type())
	s.Empty(*ran)
}

// Steps a switch routes to run inside its child run, which does not have the
// gate: the switch itself waited on it.
func (s *ExecutorTestSuite) TestGatedRunWithASwitch() {
	s.gateEnv(map[string]any{"status": "approve", "approver": "person-1", approvalGateOutputRef: testGateRef}, nil)
	wf := gatedRun(s.T(), switchFanInWorkflow("b").Definition)
	result, err := s.run(wf)
	s.Require().NoError(err)
	s.JSONEq(`{"converge":"joined:SKIPPED/-|COMPLETED/ran-leaf_b|SKIPPED/-|SKIPPED/-"}`, result)
}

func TestWithApprovalGate(t *testing.T) {
	def := model.WorkflowDefinition{Tasks: []model.Task{
		{ID: "a", Type: "core.print"},
		{ID: "b", Type: "core.print", DependsOn: []string{"a"}},
		{ID: "c", Type: "core.print"},
	}}
	gated, err := withApprovalGate(def, &RunApproval{Ref: "r/e", Timeout: 2 * time.Hour, Message: "m"})
	require.NoError(t, err)

	require.Len(t, gated.Tasks, 4)
	gate := gated.Tasks[0]
	assert.Equal(t, model.ApprovalGateTaskID, gate.ID)
	assert.Equal(t, "core.approval", gate.Type)
	assert.Equal(t, "2h0m0s", gate.Timeout)
	assert.Equal(t, int32(1), gate.FailurePolicy.Retry.MaximumAttempts)
	assert.Equal(t, []string{model.ApprovalGateTaskID}, gated.Tasks[1].DependsOn, "a step the run starts with waits on the gate")
	assert.Equal(t, []string{"a"}, gated.Tasks[2].DependsOn, "a later step waits on its own dependencies")
	assert.Equal(t, []string{model.ApprovalGateTaskID}, gated.Tasks[3].DependsOn)

	assert.Len(t, def.Tasks, 3, "the definition it was given is unchanged")
	assert.Empty(t, def.Tasks[0].DependsOn)
	assert.Empty(t, def.Tasks[2].DependsOn)

	def.Tasks = append(def.Tasks, model.Task{ID: model.ApprovalGateTaskID, Type: "core.print"})
	_, err = withApprovalGate(def, &RunApproval{Ref: "r/e", Timeout: time.Hour})
	assert.ErrorContains(t, err, "reserved id")
	_, err = withApprovalGate(model.WorkflowDefinition{}, &RunApproval{Ref: "r/e"})
	assert.ErrorContains(t, err, "timeout")
}

func TestWithoutApprovalGate(t *testing.T) {
	deps := []string{"x", model.ApprovalGateTaskID}
	assert.Equal(t, []string{"x"}, withoutApprovalGate(deps))
	assert.Equal(t, []string{"x", model.ApprovalGateTaskID}, deps, "the list it was given is unchanged")
	assert.Equal(t, []string{"y"}, withoutApprovalGate([]string{"y"}))
}

func TestReservedTaskIDsAreRefusedOnSave(t *testing.T) {
	wf := model.Workflow{Name: "w", Definition: model.WorkflowDefinition{
		Triggers: []model.Trigger{{Type: model.WorkflowTriggerManual}},
		Tasks:    []model.Task{{ID: model.ApprovalGateTaskID, Type: "core.approval"}},
	}}
	err := model.ValidateWorkflow(wf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved_task_id")
}
