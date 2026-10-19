package tools

import (
	"context"
	"errors"

	"nudgebee/llm/tools/core"
)

// AutomationInput is an input an automation declares.
type AutomationInput = automationInput

// AutomationSpec describes an automation to run as a tool.
type AutomationSpec struct {
	AccountID   string
	ID          string
	Name        string
	ToolName    string
	Description string
	Summary     string
	Inputs      []AutomationInput
}

// AutomationRunner is what every automation tool shares: the input schema,
// the argument checks, and triggering a run as the calling user and waiting
// for its outcome. Automation tools built outside this package (by an EE
// extension) run through it.
type AutomationRunner struct {
	t *automationTool
}

// NewAutomationRunner is the runner for the automation spec describes.
func NewAutomationRunner(spec AutomationSpec) AutomationRunner {
	return AutomationRunner{t: &automationTool{
		accountId:   spec.AccountID,
		id:          spec.ID,
		name:        spec.Name,
		toolName:    spec.ToolName,
		description: spec.Description,
		humanDesc:   spec.Summary,
		inputs:      spec.Inputs,
		reasonArg:   reasonArgFor(spec.Inputs),
	}}
}

// InputSchema is the automation's inputs as typed parameters, plus the
// required reason.
func (r AutomationRunner) InputSchema() core.ToolSchema { return r.t.InputSchema() }

// ReasonArg is the name of the required reason parameter.
func (r AutomationRunner) ReasonArg() string { return r.t.reasonArg }

// TriggerInputs checks a call's arguments and returns the inputs to trigger
// the automation with, and the stated reason.
func (r AutomationRunner) TriggerInputs(ctx core.NbToolContext, input core.NBToolCallRequest) (map[string]any, string, error) {
	return r.t.triggerInputs(ctx, input)
}

// RunAndWait triggers the automation as the calling user, with any extra
// request headers, and waits for the run's outcome.
func (r AutomationRunner) RunAndWait(ctx core.NbToolContext, inputs map[string]any, headers map[string]string) (core.NBToolResponse, error) {
	return r.t.runAndWait(ctx, inputs, headers)
}

// RunbookErrorStatus returns the status and body runbook-server answered an
// error with, when err (or an error it wraps) is such an answer.
func RunbookErrorStatus(err error) (status int, body string, ok bool) {
	var failed *runbookStatusError
	if !errors.As(err, &failed) {
		return 0, "", false
	}
	return failed.status, failed.body, true
}

// IsAIInvocableAutomationTool reports whether t is one of the account's
// AI-invocable automations (the automation_* tools).
func IsAIInvocableAutomationTool(t core.NBTool) bool {
	_, ok := t.(*automationTool)
	return ok
}

// DoRunbookRequestWithHeaders is DoRunbookRequestWithContext with extra
// request headers, set after the standard ones.
func DoRunbookRequestWithHeaders(reqCtx context.Context, method, path string, body any, accountId, tenantId, userId string, headers map[string]string) ([]byte, error) {
	return doRunbookRequestWithHeaders(reqCtx, method, path, body, accountId, tenantId, userId, headers)
}
