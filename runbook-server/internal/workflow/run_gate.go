package workflow

import (
	"context"

	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks"
	"nudgebee/runbook/services/security"
)

// RunGate decides runs of automations bound to an agent (workflows.bee). An
// extension registers one from its init(); without one, a bound automation
// never runs and gets no schedule.
type RunGate interface {
	// CheckRun is called on the shared run tail for every run of a bound
	// automation, whatever started it. On success it returns a function to
	// call with the new execution's id.
	CheckRun(ctx *security.RequestContext, accountID string, wf *model.Workflow, memo map[string]any, trigger model.WorkflowTrigger, inputs map[string]any) (func(executionID string), error)
	// Binding returns the stored binding of a workflow (nil when unbound).
	// Read before a schedule is registered: scheduled runs start on Temporal
	// without passing CheckRun.
	Binding(ctx context.Context, tenantID, workflowID string) (*model.WorkflowBinding, error)
}

// RunGateDeps is what a RunGate gets when the service is built.
type RunGateDeps struct {
	Store        model.WorkflowStore
	TaskRegistry *tasks.TaskRegistry
}

var runGateFactory func(RunGateDeps) RunGate

// RegisterRunGate sets the RunGate every Service built afterwards uses. Call
// it from an init().
func RegisterRunGate(factory func(RunGateDeps) RunGate) {
	runGateFactory = factory
}

// SetRunGate replaces the service's gate (tests).
func (s *Service) SetRunGate(g RunGate) {
	s.runGate = g
}

// errBoundAutomationUnavailable refuses a bound automation when no gate is
// registered: only its agent may start it, and nothing here can tell.
var errBoundAutomationUnavailable = func() error {
	e := common.ErrorUnauthorized("this automation is bound to an agent and only that agent can run it")
	e.Reason = "bound_automation"
	return e
}
