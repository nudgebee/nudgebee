package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"nudgebee/llm/common"
)

const (
	// headerTriggerSource / triggerSourceAI declare that a request originated from
	// the AI assistant. runbook-server reads these to apply the AI-invocation
	// gate; the value must stay in lockstep with api.HeaderTriggerSource /
	// api.TriggerSourceAI on that side.
	headerTriggerSource = "X-Trigger-Source"
	triggerSourceAI     = "ai"

	// executionWaitTimeout bounds how long a tool call blocks waiting for a run
	// to finish. Matches the dry-run ceiling: most automations that are worth
	// invoking from chat finish in seconds, and anything longer is better
	// reported as "still running" than left holding the conversation open.
	executionWaitTimeout = 3 * time.Minute

	// approvalTaskType is the task type that parks a run waiting for a human.
	approvalTaskType = "core.approval"
)

// executionPollInterval is how often a run is re-checked. Deliberately coarse —
// the workflow engine will not finish sooner because we asked more often, and
// every poll is an HTTP round trip. A variable rather than a constant only so
// tests need not spend real seconds waiting.
var executionPollInterval = 3 * time.Second

// executionOutcome is what the LLM is told about a run it started.
type executionOutcome struct {
	WorkflowID  string `json:"workflow_id"`
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	// Result / Error carry the run's own output, present only once terminal.
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	// Note explains a non-terminal return so the model does not report a run as
	// finished when it merely stopped being watched.
	Note string `json:"note,omitempty"`
}

// terminalExecutionStatuses are the states from which a run will not progress.
// Mirrors model.WorkflowExecutionStatus on the runbook-server side.
var terminalExecutionStatuses = map[string]bool{
	"COMPLETED":        true,
	"FAILED":           true,
	"CANCELED":         true,
	"TERMINATED":       true,
	"TIMED_OUT":        true,
	"CONTINUED_AS_NEW": true,
}

// executionDetails is the subset of runbook-server's WorkflowExecutionDetails
// this helper needs. Intentionally partial: the full payload carries the whole
// definition snapshot and every task's rendered params, none of which belong in
// a chat response.
type executionDetails struct {
	Status         string `json:"status"`
	WorkflowResult any    `json:"workflow_result"`
	Error          string `json:"error"`
	Tasks          []struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"tasks"`
}

// waitForExecution polls a run to a terminal state and returns its outcome.
//
// It exists because returning an execution id to the LLM is not an answer: the
// model either reports success it has not observed, or burns turns polling by
// hand. Waiting means the user hears what actually happened.
//
// Two cases return early rather than blocking, both reporting the live status
// so the model can say something truthful:
//
//   - Timeout. The run is still going; the caller can check later with
//     workflow_execution_get.
//   - Parked on an approval. A core.approval task sits SCHEDULED until a human
//     answers out of band, which may be hours. Waiting for that inside a chat
//     turn would pin the conversation open for no benefit.
func waitForExecution(reqCtx context.Context, workflowID, executionID, accountID, tenantID, userID string) executionOutcome {
	outcome := executionOutcome{
		WorkflowID:  workflowID,
		ExecutionID: executionID,
		Status:      "RUNNING",
	}

	waitCtx, cancel := context.WithTimeout(reqCtx, executionWaitTimeout)
	defer cancel()

	path := fmt.Sprintf("workflows/%s/runs/%s", workflowID, executionID)

	// Consecutive read failures before giving up. A single blip must not end the
	// watch: over a 3-minute wait at this poll interval there are ~60 chances to
	// catch one, and a transient 401 or a rolling pod was enough in testing to
	// make a healthy run get reported to the user as failed. Counted
	// consecutively, so a run that is genuinely unreachable still ends quickly.
	const maxConsecutiveReadFailures = 3
	consecutiveFailures := 0

	for {
		details, err := fetchExecutionDetails(waitCtx, path, accountID, tenantID, userID)
		if err != nil {
			// Distinguish "we stopped watching" from "we could not watch". A read
			// that fails because the wait deadline expired mid-request carries a
			// context error, and reporting that as an unreadable status would tell
			// the user their automation is in an unknown state when in fact it is
			// simply still going — the one thing this whole helper exists to avoid.
			// Checked before the retry counter, which cannot help here: the context
			// is done, so every further attempt fails the same way.
			if waitCtx.Err() != nil {
				outcome.Note = "still running; check back with workflow_execution_get"
				return outcome
			}
			consecutiveFailures++
			if consecutiveFailures < maxConsecutiveReadFailures {
				select {
				case <-waitCtx.Done():
					outcome.Note = "still running; check back with workflow_execution_get"
					return outcome
				case <-time.After(executionPollInterval):
					continue
				}
			}
			// The run was started successfully — only the observation failed. Say
			// so rather than implying the automation did not run.
			outcome.Note = fmt.Sprintf("started, but its status could not be read: %v", err)
			return outcome
		}
		consecutiveFailures = 0

		if details.Status != "" {
			outcome.Status = details.Status
		}

		if terminalExecutionStatuses[details.Status] {
			outcome.Result = details.WorkflowResult
			outcome.Error = details.Error
			return outcome
		}

		if hasPendingApproval(details) {
			outcome.Note = "paused waiting for an approval; it will continue once someone approves it"
			return outcome
		}

		select {
		case <-waitCtx.Done():
			outcome.Note = "still running; check back with workflow_execution_get"
			return outcome
		case <-time.After(executionPollInterval):
		}
	}
}

func fetchExecutionDetails(ctx context.Context, path, accountID, tenantID, userID string) (executionDetails, error) {
	raw, err := DoRunbookRequestWithContext(ctx, "GET", path, nil, accountID, tenantID, userID)
	if err != nil {
		return executionDetails{}, fmt.Errorf("failed to fetch execution details: %w", err)
	}
	var details executionDetails
	if err := common.UnmarshalJson(raw, &details); err != nil {
		return executionDetails{}, fmt.Errorf("failed to parse execution details: %w", err)
	}
	return details, nil
}

// hasPendingApproval reports whether the run is parked on a human approval.
// runbook-server reconciles these against Temporal's live pending activities, so
// a SCHEDULED core.approval task is a genuine wait rather than a stale record.
func hasPendingApproval(details executionDetails) bool {
	for _, task := range details.Tasks {
		if task.Type == approvalTaskType && task.Status == "SCHEDULED" {
			return true
		}
	}
	return false
}

// marshalOutcome renders an outcome for the tool response.
func marshalOutcome(outcome executionOutcome) string {
	encoded, err := json.Marshal(outcome)
	if err != nil {
		// Never drop the ids — they are how a user finds the run in the UI.
		return fmt.Sprintf(`{"workflow_id":%q,"execution_id":%q,"status":%q}`,
			outcome.WorkflowID, outcome.ExecutionID, outcome.Status)
	}
	return string(encoded)
}
