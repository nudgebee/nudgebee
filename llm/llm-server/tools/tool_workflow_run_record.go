package tools

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/tools/core"
)

// What a chat reply may say about an automation run is decided here, by the
// tools that start runs, rather than by the model's own wording.
//
// The model was told a trigger failed only through the raw server error —
// `runbook server returned error (status 403): {"error":"... (not_active)"}` —
// which says nothing about whether a run exists. Replies then reported runs
// that never started, with an execution id, a status and per-step results.
// A refused trigger now comes back as a plain statement that no run started
// and why, and the same statement is what the run record above the reply
// shows.

// runNotStartedPrefix opens every refused-trigger observation. The run record
// (agents/core/run_receipt.go) keys on it, so the wording after it is free to
// change but the prefix is not.
const runNotStartedPrefix = "NO RUN STARTED: "

// runNotStartedTail follows the reason in the observation. It is guidance for
// the model only; the run record shows the reason without it.
const runNotStartedTail = " No execution was created, so there is no execution ID, status or result to report. " +
	"Do not retry it or change the automation to get around this; tell the user it did not run and why."

// aiGateCode finds the code runbook-server's AI-invocation gate refuses a run
// with. The gate sends it inside the error message —
// "workflow is not available to the AI assistant (not_active)" — and no
// separate field carries it, so the message is the contract.
var aiGateCode = regexp.MustCompile(`\((no_user|not_enabled|not_opted_in|not_active|no_manual_trigger)\)`)

// runToolNames are the built-in tools that start an automation run. Each
// AI-invocable automation is also a tool of its own, named with
// automationToolPrefix.
var runToolNames = []string{ToolWorkflowTrigger, ToolWorkflowExecutionRetrigger}

// RunToolNames returns the built-in tools that start an automation run.
func RunToolNames() []string { return append([]string(nil), runToolNames...) }

// RunToolPrefix is the name prefix of the per-automation tools, each of which
// starts a run of its automation.
func RunToolPrefix() string { return automationToolPrefix }

// IsRunTool reports whether the tool named name starts an automation run.
// Built-in tools and agents that merely share the prefix (automation_builder)
// do not: a per-automation tool is never registered, it is derived per account.
func IsRunTool(name string) bool {
	if strings.HasPrefix(name, automationToolPrefix) {
		return !core.IsSystemToolName(name)
	}
	for _, n := range runToolNames {
		if name == n {
			return true
		}
	}
	return false
}

// runNotStartedResponse is the observation for a trigger runbook-server
// refused with a 4xx: the server decided not to run it, so no execution
// exists, and this says so in words. ok is false for anything else — a
// network failure, a bad argument, or a 5xx, which a proxy can return after
// the run was created — so those keep their own error and nobody is told a
// run did not happen when that is not known.
func runNotStartedResponse(automation string, err error) (core.NBToolResponse, bool) {
	status, body, answered := RunbookErrorStatus(err)
	if !answered || status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		return core.NBToolResponse{}, false
	}
	return core.NBToolResponse{
		Data:   runNotStartedPrefix + automation + " was not run: " + runNotStartedReason(status, body) + "." + runNotStartedTail,
		Type:   core.NBToolResponseTypeText,
		Status: core.NBToolResponseStatusError,
	}, true
}

// runNotStartedReason words why runbook-server did not start a run.
func runNotStartedReason(status int, body string) string {
	if status == http.StatusForbidden {
		if m := aiGateCode.FindStringSubmatch(body); m != nil {
			switch m[1] {
			case "not_active":
				return "it is not ACTIVE (it is paused or disabled), and the assistant may only run ACTIVE automations"
			case "not_opted_in":
				return "it is not enabled for the AI assistant; its author has not allowed the assistant to run it"
			case "not_enabled":
				return "running automations from the AI assistant is not enabled for this account"
			case "no_manual_trigger":
				return "its live version has no manual trigger, so it cannot be run on request"
			case "no_user":
				return "the request has no identified user to run it as"
			}
		}
	}
	if status == http.StatusNotFound {
		return "the automation was not found"
	}
	message := refusalBodyMessage(body)
	if message == "" {
		return fmt.Sprintf("the automation server refused the request (status %d)", status)
	}
	return fmt.Sprintf("the automation server refused the request (status %d): %s", status, message)
}

// refusalBodyMessage is the human part of a runbook-server error body: its
// error or message field when the body is JSON, else the body, cut short.
func refusalBodyMessage(body string) string {
	var parsed map[string]any
	if common.UnmarshalJson([]byte(body), &parsed) == nil {
		for _, key := range []string{"error", "message"} {
			if s, ok := parsed[key].(string); ok && strings.TrimSpace(s) != "" {
				body = s
				break
			}
		}
	}
	return strings.TrimSuffix(common.TruncateHead(strings.TrimSpace(body), 300), ".")
}

// RunNotStartedStatement returns the "<automation> was not run: <reason>."
// sentence from a refused-trigger observation, without the guidance that
// follows it. ok is false for any other observation.
func RunNotStartedStatement(observation string) (string, bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(observation), runNotStartedPrefix)
	if !found {
		return "", false
	}
	statement, _, _ := strings.Cut(rest, runNotStartedTail)
	return strings.TrimSpace(statement), true
}

// quotedAutomation names an automation for a run statement: its name in
// quotes, or its id when the name is unknown.
func quotedAutomation(name, id string) string {
	if name = strings.TrimSpace(name); name != "" {
		return fmt.Sprintf("%q", name)
	}
	return "the automation with ID " + id
}

// refusedAutomationName names the automation a refused workflow_trigger or
// workflow_execution_retrigger call was for. A refused automation is often one
// the assistant may not run — paused, not opted in — and so absent from the
// cached list AutomationRunName reads; it is then read from runbook-server, so
// the user is told which automation did not run rather than an id.
func refusedAutomationName(ctx core.NbToolContext, toolName, id string) string {
	args := fmt.Sprintf(`{"id":%q}`, id)
	if name := AutomationRunName(ctx.AccountId, toolName, args); name != quotedAutomation("", id) {
		return name
	}
	if name, err := fetchWorkflowName(ctx, id); err == nil {
		return quotedAutomation(name, id)
	}
	return quotedAutomation("", id)
}

// AutomationRunName names the automation a run tool call was for, for the run
// record: the per-automation tool's automation, or the one whose id a
// workflow_trigger / workflow_execution_retrigger call passed. Falls back to
// the id, then to the tool name.
func AutomationRunName(accountId, toolName, arguments string) string {
	if strings.HasPrefix(toolName, automationToolPrefix) {
		for _, tool := range listAutomationTools(accountId) {
			if automation, ok := tool.(*automationTool); ok && automation.toolName == toolName {
				return quotedAutomation(automation.name, automation.id)
			}
		}
		return fmt.Sprintf("%q", toolName)
	}
	var args struct {
		ID string `json:"id"`
	}
	if common.UnmarshalJson([]byte(arguments), &args) != nil || strings.TrimSpace(args.ID) == "" {
		return fmt.Sprintf("%q", toolName)
	}
	if automation, ok := automationForConfirmation(accountId, args.ID); ok {
		return quotedAutomation(automation.name, args.ID)
	}
	return quotedAutomation("", args.ID)
}
