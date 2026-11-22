package workflow

import (
	"errors"
	"fmt"
	"slices"

	"go.temporal.io/sdk/temporal"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/services/security"
)

// An approval gate is a core.approval step the engine puts in front of a run's
// own steps when its run gate asks for one (RunDecision.Approval). It is added
// to the definition the run executes, never to the stored one, and its answer
// counts only when it came through the run gate's DecideApproval: anything else
// stops the run before any of its own steps.

// Error types of a run stopped at its approval gate. Neither runs the
// workflow's failure or always hooks: those are steps too.
const (
	ErrTypeApprovalRejected = "ApprovalRejected"
	ErrTypeApprovalExpired  = "ApprovalExpired"
)

// approvalGateOutputRef is the key, in the gate's output, of the gate reference
// the app's approval path adds once the run gate has accepted the decision.
const approvalGateOutputRef = "approval_gate"

// withApprovalGate returns a copy of def with the approval gate in front of
// every step the run would otherwise start with. def itself is not changed.
func withApprovalGate(def model.WorkflowDefinition, approval *RunApproval) (model.WorkflowDefinition, error) {
	for _, t := range def.Tasks {
		if t.ID == model.ApprovalGateTaskID {
			return def, fmt.Errorf("the automation already has a step with the reserved id %s", model.ApprovalGateTaskID)
		}
	}
	timeout := approval.Timeout
	if timeout <= 0 {
		return def, errors.New("an approval gate needs a timeout")
	}
	gate := model.Task{
		ID:   model.ApprovalGateTaskID,
		Type: "core.approval",
		Params: map[string]any{
			"message":          approval.Message,
			"approval_options": []any{"approve", "reject"},
			"approval_type":    "in_app",
		},
		Timeout: timeout.String(),
		// One attempt: a retry would ask again for an answer already refused.
		FailurePolicy: &model.FailurePolicy{Retry: &model.WorkflowRetryPolicy{MaximumAttempts: 1}},
	}
	tasks := make([]model.Task, 0, len(def.Tasks)+1)
	tasks = append(tasks, gate)
	for _, t := range def.Tasks {
		// Every step the run starts with waits on the gate; the rest wait on
		// those. Steps run only inside a switch or another step's body are
		// copied from here too and lose the gate there (withoutApprovalGate):
		// the step that runs them already waited on it.
		if len(t.DependsOn) == 0 {
			t.DependsOn = []string{model.ApprovalGateTaskID}
		}
		tasks = append(tasks, t)
	}
	def.Tasks = tasks
	return def, nil
}

// withoutApprovalGate drops the approval gate from a dependency list, for a
// step copied into a child run whose parent step already waited on it.
func withoutApprovalGate(dependsOn []string) []string {
	if !slices.Contains(dependsOn, model.ApprovalGateTaskID) {
		return dependsOn
	}
	return slices.DeleteFunc(slices.Clone(dependsOn), func(d string) bool { return d == model.ApprovalGateTaskID })
}

// checkApprovalGateAnswer returns the error that stops a gated run when its
// approval gate (taskID) did not end in an approval made through the run gate.
// It is nil for any other step, and for every step of a run without a gate.
func checkApprovalGateAnswer(wf *model.Workflow, taskID string, result any, err error) error {
	if wf == nil || wf.ApprovalGate == "" || taskID != model.ApprovalGateTaskID {
		return nil
	}
	if err != nil {
		var timeoutErr *temporal.TimeoutError
		if errors.As(err, &timeoutErr) {
			return temporal.NewNonRetryableApplicationError("no one approved this run in time, so none of its steps ran", ErrTypeApprovalExpired, err)
		}
		return temporal.NewNonRetryableApplicationError("the approval failed, so none of this run's steps ran: "+err.Error(), ErrTypeApprovalRejected, err)
	}
	output, _ := result.(map[string]any)
	status, _ := output["status"].(string)
	approver, _ := output["approver"].(string)
	ref, _ := output[approvalGateOutputRef].(string)
	switch {
	case status != "approve":
		return temporal.NewNonRetryableApplicationError("the approval was rejected, so none of this run's steps ran", ErrTypeApprovalRejected, nil)
	case approver == "" || approver == security.GetSystemUserId() || ref != wf.ApprovalGate:
		// Only the app's approval path names a person and carries the gate's
		// reference; an answer by token alone does neither.
		return temporal.NewNonRetryableApplicationError("the approval did not come from a person in the app, so none of this run's steps ran", ErrTypeApprovalRejected, nil)
	}
	return nil
}

// stoppedAtApprovalGate reports whether a run's error is its approval gate
// stopping it.
func stoppedAtApprovalGate(err error) bool {
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		return false
	}
	return appErr.Type() == ErrTypeApprovalRejected || appErr.Type() == ErrTypeApprovalExpired
}
