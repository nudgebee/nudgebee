package core

import (
	"fmt"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// A reply that runs an automation carries a record of what ran, written from
// the turn's tool calls rather than by the model.
//
// Run claims in replies were free text. A refused trigger, a failed hand-off or
// a search that found nothing all ended in replies reporting a run — once with
// an execution id, a COMPLETED status and a table of step results that no tool
// had returned. The user could not tell those from real runs. The record names each run the turn tried to start and says, from
// the tool's own result, whether it started: the execution id and status the
// run returned, or why it did not run.
//
// It is built once per turn, for the whole turn: every nesting level and the
// turn resumed after an approval write their tool calls under the turn's
// message id, so a run started by a sub-agent is on the record of the reply the
// user reads. It goes on after the agent's last rewrite of the answer, so no
// model sees it to reword it.

// runReceiptHeading opens the record. It is also how a reply already carrying
// one is recognised, so applying the record twice is harmless.
const runReceiptHeading = "**Automation runs for this request** (from the run records)"

// runReceiptSources are the conversations a person reads as a chat reply. Event
// investigations, automation steps and the like consume the reply as data, and
// a heading on top would be noise in them, or worse, parsed.
var runReceiptSources = map[ConversationSource]bool{
	ConversationSourceUserInvestigation:    true,
	ConversationSourceUserInvestigationCLI: true,
	ConversationSourceInstantNotification:  true,
}

// listRunToolCalls and automationRunName are variables so the record can be
// tested without a database or an automation catalog.
var (
	listRunToolCalls = func(messageId string) ([]ToolCallTrace, error) {
		return GetConversationDao().ListRunToolCallsByMessage(messageId, tools.RunToolNames(), tools.RunToolPrefix())
	}
	automationRunName = tools.AutomationRunName
)

// applyRunReceipt puts the turn's run record at the top of response[0], in
// place, and returns the record. It returns "" — and leaves the response alone
// — when the turn tried to start no run, the conversation is not a chat, or the
// response already has a record.
func applyRunReceipt(ctx *security.RequestContext, accountId, messageId string, source ConversationSource, response []string) string {
	if len(response) == 0 || messageId == "" || !runReceiptSources[source] {
		return ""
	}
	if strings.Contains(response[0], runReceiptHeading) {
		return ""
	}
	calls, err := listRunToolCalls(messageId)
	if err != nil {
		// The answer still goes out; it is only missing its record.
		ctx.GetLogger().Warn("run receipt: unable to read the turn's run tool calls", "error", err, "message_id", messageId)
		return ""
	}
	receipt := renderRunReceipt(accountId, calls)
	if receipt == "" {
		return ""
	}
	ctx.GetLogger().Info("run receipt: added the turn's automation run record", "message_id", messageId, "run_tool_calls", len(calls))
	response[0] = joinRunReceipt(receipt, response[0])
	return receipt
}

// joinRunReceipt puts receipt above answer.
func joinRunReceipt(receipt, answer string) string {
	if strings.TrimSpace(answer) == "" {
		return receipt
	}
	return receipt + "\n\n" + answer
}

// withoutRunReceipt is answer without the receipt joinRunReceipt put on it.
func withoutRunReceipt(receipt, answer string) string {
	if receipt == "" {
		return answer
	}
	if rest, ok := strings.CutPrefix(answer, receipt); ok {
		return strings.TrimLeft(rest, "\n")
	}
	return answer
}

// runToolResult is the part of a started run's result the record shows. It
// mirrors the outcome the run tools return (tools.executionOutcome).
type runToolResult struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	URL         string `json:"url"`
	Note        string `json:"note"`
}

// renderRunReceipt is the record for a turn's run tool calls, or "" when there
// are none.
func renderRunReceipt(accountId string, calls []ToolCallTrace) string {
	type entry struct {
		name string
		line string
		ran  bool
		// plainFailure is an error that is not a refusal: a bad argument or a
		// failed request. Shown only when the same automation did not run later
		// in the turn, since the model usually corrects the call and retries.
		plainFailure bool
	}
	var entries []entry
	for _, call := range calls {
		// The query matches on the name prefix; agents that share it are not runs.
		if !tools.IsRunTool(call.ToolName) {
			continue
		}
		name := automationRunName(accountId, call.ToolName, call.Parameters)
		// Rows store the status lowercased.
		switch toolcore.NBToolResponseStatus(strings.ToUpper(strings.TrimSpace(call.Status))) {
		case toolcore.NBToolResponseStatusSuccess:
			var result runToolResult
			if err := common.UnmarshalJson([]byte(call.Response), &result); err != nil || strings.TrimSpace(result.ExecutionID) == "" {
				entries = append(entries, entry{name: name, line: fmt.Sprintf(
					"%s: the request was accepted but no execution ID came back, so whether it ran is not known. Check the automation's Executions tab.", name)})
				continue
			}
			line := fmt.Sprintf("%s ran — execution `%s`, status %s", name, result.ExecutionID, runStatusOrUnknown(result.Status))
			if note := strings.TrimSpace(result.Note); note != "" {
				line += " (" + note + ")"
			}
			line += "."
			if result.URL != "" {
				line += fmt.Sprintf(" [Open the automation](%s)", result.URL)
			}
			entries = append(entries, entry{name: name, line: line, ran: true})
		case toolcore.NBToolResponseStatusError:
			if statement, ok := tools.RunNotStartedStatement(call.Response); ok {
				entries = append(entries, entry{name: name, line: statement})
				continue
			}
			// Not a refusal, so whether a run started is not known: a request
			// can fail after the server created the run.
			entries = append(entries, entry{name: name, plainFailure: true, line: fmt.Sprintf(
				"%s: the run request failed (%s), and no run was confirmed.", name, runFailureSummary(call.Response))})
		case toolcore.NBToolResponseStatusWaiting:
			// The row still holds the approval question, never an outcome: an
			// approval nobody gave leaves it this way.
			entries = append(entries, entry{name: name, line: fmt.Sprintf(
				"%s was not run: it was waiting for approval, which was not given.", name)})
		default:
			entries = append(entries, entry{name: name, line: fmt.Sprintf(
				"%s: no result was recorded for this run request.", name)})
		}
	}

	ranLater := map[string]bool{}
	var lines []string
	seen := map[string]bool{}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.ran {
			ranLater[e.name] = true
		}
		if e.plainFailure && ranLater[e.name] {
			continue
		}
		if seen[e.line] {
			continue
		}
		seen[e.line] = true
		lines = append(lines, "- "+e.line)
	}
	if len(lines) == 0 {
		return ""
	}
	// Collected newest first so a failure can see a later run; shown in order.
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return runReceiptHeading + "\n" + strings.Join(lines, "\n")
}

func runStatusOrUnknown(status string) string {
	if status = strings.TrimSpace(status); status != "" {
		return status
	}
	return "unknown"
}

// runFailureSummary is a failed run call's error, short and on one line.
func runFailureSummary(response string) string {
	text := strings.TrimSpace(response)
	text = strings.TrimPrefix(text, "action execution failed: ")
	if first, _, found := strings.Cut(text, "\n"); found {
		text = first
	}
	text = strings.TrimSuffix(common.TruncateHead(strings.TrimSpace(text), 200), ".")
	if text == "" {
		return "no error was recorded"
	}
	return text
}
