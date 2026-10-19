package workflow

import (
	"context"
	"time"

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
	// automation, whatever started it. On success it says how the run starts.
	CheckRun(ctx *security.RequestContext, accountID string, wf *model.Workflow, memo map[string]any, trigger model.WorkflowTrigger, inputs map[string]any) (RunDecision, error)
	// DecideApproval is called when a person answers a run's approval gate
	// in the app, before the answer reaches the run. It refuses a decider who
	// may not decide (an approver must be allowed every step the run would
	// take) and records the decision. status is "approve" or "reject".
	DecideApproval(ctx *security.RequestContext, accountID, workflowID, ref, status string) error
	// ApprovalNotDelivered records that a decision DecideApproval accepted did
	// not reach the run, so the decider may answer again.
	ApprovalNotDelivered(ctx *security.RequestContext, ref string, cause error)
	// Binding returns the stored binding of a workflow (nil when unbound).
	// Read before a schedule is registered: scheduled runs start on Temporal
	// without passing CheckRun.
	Binding(ctx context.Context, tenantID, workflowID string) (*model.WorkflowBinding, error)
}

// RunDecision is how an allowed run starts.
type RunDecision struct {
	// Started, when set, is called with the new execution's id.
	Started func(executionID string)
	// Approval, when set, puts an approval in front of the run's own steps:
	// none of them runs until a person approves it through DecideApproval.
	Approval *RunApproval
}

// RunApproval is the approval a run waits on before its own steps.
type RunApproval struct {
	// Ref names the decision at the gate; only an approval carrying it lets
	// the run go on.
	Ref string
	// Timeout is how long the run waits; no answer by then stops the run.
	Timeout time.Duration
	// Message is what the approver is asked.
	Message string
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
