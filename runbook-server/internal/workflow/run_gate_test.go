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
	"github.com/stretchr/testify/require"
)

type fakeRunGate struct {
	calls int
	err   error
}

func (g *fakeRunGate) CheckRun(*security.RequestContext, string, *model.Workflow, map[string]any, model.WorkflowTrigger, map[string]any) (func(string), error) {
	g.calls++
	return nil, g.err
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
