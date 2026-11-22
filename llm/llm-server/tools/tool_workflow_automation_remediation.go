package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/watch"
)

// A remediation plan can offer one of the account's AI-invocable automations as a fix option (#42459): a person
// approves it like any step, NudgeBee starts one run as that person, and the wait for the run to end is a watch
// (automation_run) on Nubi's watcher. This file is what the remediation code needs from runbook-server.

func init() {
	watch.RegisterSource(&automationRunSource{})
}

// AutomationOption is an AI-invocable automation as a remediation plan may offer it.
type AutomationOption struct {
	ID string
	// Name is the author's name; Description says when to use it (the AI description).
	Name        string
	Description string
	Inputs      []AutomationInput
	// Version is the live version's id. HasApprovalStep: a run can stop for a person's approval partway.
	Version         string
	HasApprovalStep bool
}

// RequiredInputs are the inputs a run must be given: as the automation's tool schema has them, a required input or
// one with no default.
func (o AutomationOption) RequiredInputs() []string {
	var out []string
	for _, in := range o.Inputs {
		if in.ID != "" && (in.Required || in.Default == nil) {
			out = append(out, in.ID)
		}
	}
	return out
}

// AccountAutomations are the account's AI-invocable automations, from the same cached list its automation tools are
// built from (no user: the tenant's view). For writing plans; a run re-reads them as the approver.
func AccountAutomations(accountId string) []AutomationOption {
	var out []AutomationOption
	for _, t := range listAutomationTools(accountId) {
		if a, ok := t.(*automationTool); ok {
			out = append(out, a.option())
		}
	}
	return out
}

// AutomationsAsUser reads the account's AI-invocable automations now, as userId: what that person could run.
func AutomationsAsUser(ctx context.Context, accountId, tenantId, userId string) ([]AutomationOption, error) {
	raw, err := DoRunbookRequestWithContext(ctx, "GET", fmt.Sprintf("workflows/ai-search?limit=%d", maxAutomationTools+1), nil, accountId, tenantId, userId)
	if err != nil {
		return nil, fmt.Errorf("automations: %w", err)
	}
	var resp automationSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("automations: parse: %w", err)
	}
	out := make([]AutomationOption, 0, len(resp.Workflows))
	for _, a := range buildAutomationTools(accountId, resp.Workflows) {
		out = append(out, a.(*automationTool).option())
	}
	return out, nil
}

// AutomationOfTool is the workflow id and name of the account's automation tool named toolName.
func AutomationOfTool(accountId, toolName string) (id, name string, found bool) {
	for _, t := range listAutomationTools(accountId) {
		if a, ok := t.(*automationTool); ok && a.toolName == toolName {
			return a.id, a.name, true
		}
	}
	return "", "", false
}

func (t *automationTool) option() AutomationOption {
	return AutomationOption{ID: t.id, Name: t.name, Description: t.description, Inputs: t.inputs,
		Version: t.version, HasApprovalStep: t.hasApprovalStep}
}

// TriggerAutomation starts one run of workflowId on accountId as userId (marked as started by the AI, so
// runbook-server's AI-invocation gate applies) and returns its execution id. A run runbook-server refused before
// creating it is an *AutomationNotStartedError.
func TriggerAutomation(ctx context.Context, accountId, tenantId, userId, workflowId string, inputs map[string]any) (string, error) {
	if userId == "" || userId == security.GetSystemUserId() {
		return "", &AutomationNotStartedError{Reason: "there is no identified person to run it as"}
	}
	raw, err := DoRunbookRequestWithContext(ctx, "POST", fmt.Sprintf("workflows/%s/trigger", workflowId), inputs, accountId, tenantId, userId)
	if err != nil {
		if status, body, answered := RunbookErrorStatus(err); answered && status >= http.StatusBadRequest && status < http.StatusInternalServerError {
			return "", &AutomationNotStartedError{Reason: runNotStartedReason(status, body)}
		}
		return "", fmt.Errorf("trigger automation: %w", err)
	}
	var triggered struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.Unmarshal(raw, &triggered); err != nil || strings.TrimSpace(triggered.ExecutionID) == "" {
		// It was started; only its id could not be read back.
		return "", fmt.Errorf("trigger automation: started, but its execution id was not returned: %s", truncateText(string(raw), 200))
	}
	return triggered.ExecutionID, nil
}

// AutomationNotStartedError: runbook-server refused the run before creating it.
type AutomationNotStartedError struct{ Reason string }

func (e *AutomationNotStartedError) Error() string { return "the automation was not run: " + e.Reason }

// AutomationRunURL links a run of an automation.
func AutomationRunURL(accountId, workflowId, executionId string) string {
	return core.BuildAutomationURL(accountId, workflowId, url.Values{"executionId": {executionId}}, "executions")
}

// AutomationRunObservation is what an automation_run watch observes: the run's status and whether it ended. A run
// parked on an approval counts as ended for the watch: it may wait for hours, and the remediation step reports it.
type AutomationRunObservation struct {
	Status          string `json:"status"`
	Ended           bool   `json:"ended"`
	WaitingApproval bool   `json:"waiting_approval,omitempty"`
	Error           string `json:"error,omitempty"`
	// Step is "N of M": the step it stopped at, for a run that failed partway.
	Step string `json:"step,omitempty"`
}

// automationRunSource reads one automation run as the watch's poll context (its creator).
type automationRunSource struct{}

func (automationRunSource) Kind() watch.SourceKind { return watch.SourceKindAutomationRun }

func (automationRunSource) ValidateConfig(rawCfg json.RawMessage) error {
	var cfg watch.AutomationRunSourceConfig
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return fmt.Errorf("automation_run source: invalid config json: %w", err)
	}
	if strings.TrimSpace(cfg.WorkflowID) == "" || strings.TrimSpace(cfg.ExecutionID) == "" {
		return errors.New("automation_run source: workflow_id and execution_id are required")
	}
	return nil
}

// Observe is a read: one GET of the run. It runs as the poll context's user (the person who approved the step), so
// it needs no read-only check of its own.
func (automationRunSource) Observe(ctx *security.RequestContext, w watch.Watch) (watch.Observation, error) {
	var cfg watch.AutomationRunSourceConfig
	if err := json.Unmarshal([]byte(w.SourceConfig), &cfg); err != nil {
		return watch.Observation{}, fmt.Errorf("automation_run source: %w", err)
	}
	sc := ctx.GetSecurityContext()
	details, err := fetchExecutionDetails(ctx.GetContext(), fmt.Sprintf("workflows/%s/runs/%s", cfg.WorkflowID, cfg.ExecutionID),
		w.AccountID.String(), sc.GetTenantId(), sc.GetUserId())
	if err != nil {
		return watch.Observation{}, fmt.Errorf("automation_run source: %w", err)
	}
	out, _ := json.Marshal(automationRunObservation(details))
	return watch.Observation{Data: string(out)}, nil
}

// automationRunObservation reads a run's details.
func automationRunObservation(d executionDetails) AutomationRunObservation {
	o := AutomationRunObservation{Status: d.Status, Error: truncateText(d.Error, 500)}
	switch {
	case terminalExecutionStatuses[d.Status]:
		o.Ended = true
	case hasPendingApproval(d):
		o.Ended, o.WaitingApproval = true, true
	}
	if o.Ended && d.Status != "COMPLETED" {
		for i, t := range d.Tasks {
			if t.Status == "FAILED" {
				o.Step = fmt.Sprintf("%d of %d", i+1, len(d.Tasks))
				break
			}
		}
	}
	return o
}

// AutomationRunEndedPattern is the predicate of an automation_run watch: the observation says the run ended.
const AutomationRunEndedPattern = `"ended":true`

// truncateText cuts s to n bytes.
func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
