package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"nudgebee/runbook/internal/model"

	commonpb "go.temporal.io/api/common/v1"
)

// maxFailedStepErrorRunes caps the step error carried on a run's Memo and in
// the automation's status message; the full error stays on the run detail.
const maxFailedStepErrorRunes = 300

var (
	failedStepsMessagePattern = regexp.MustCompile(`^(\d+) steps? failed\b`)
	// Temporal's wrappers around the task's own message, e.g.
	// "activity error (type: x, ...): User not found (type: E, retryable: false)".
	temporalErrorPrefix = regexp.MustCompile(`^(?:activity|child workflow execution) error \([^)]*\): `)
	temporalErrorSuffix = regexp.MustCompile(` \(type: [^,()]*, retryable: (?:true|false)\)$`)
)

// collectFailedSteps counts the steps a completed run recorded as FAILED (only
// failure_policy "continue" leaves one behind) and picks the first in
// definition order, so the choice is deterministic across workflow replays.
// The error is already secret-scrubbed by handleTaskCompletion.
func collectFailedSteps(definition []model.Task, results map[string]map[string]any) model.FailedSteps {
	var failed []string
	for id, data := range results {
		if data != nil && fmt.Sprint(data["status"]) == string(model.TaskStatusFailed) {
			failed = append(failed, id)
		}
	}
	if len(failed) == 0 {
		return model.FailedSteps{}
	}
	order := map[string]int{}
	var walk func(tasks []model.Task)
	walk = func(tasks []model.Task) {
		for _, t := range tasks {
			if _, seen := order[t.ID]; !seen {
				order[t.ID] = len(order)
			}
			walk(t.Tasks)
		}
	}
	walk(definition)
	sort.Slice(failed, func(i, j int) bool {
		oi, iKnown := order[failed[i]]
		oj, jKnown := order[failed[j]]
		if iKnown != jKnown {
			return iKnown
		}
		if iKnown && oi != oj {
			return oi < oj
		}
		return failed[i] < failed[j]
	})
	first := failed[0]
	errText := ""
	if e, ok := results[first]["error"]; ok && e != nil {
		errText = truncateRunes(stepErrorMessage(fmt.Sprint(e)), maxFailedStepErrorRunes)
	}
	return model.FailedSteps{FailedStepCount: len(failed), FirstFailedStep: first, FirstFailedStepError: errText}
}

// stepErrorMessage strips Temporal's wrappers so a list shows the task's message.
func stepErrorMessage(errText string) string {
	return temporalErrorSuffix.ReplaceAllString(temporalErrorPrefix.ReplaceAllString(errText, ""), "")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func failedStepsMemo(f model.FailedSteps) map[string]any {
	return map[string]any{
		model.MemoFailedStepCount:      f.FailedStepCount,
		model.MemoFirstFailedStep:      f.FirstFailedStep,
		model.MemoFirstFailedStepError: f.FirstFailedStepError,
	}
}

// failedStepsStatusMessage is the last_execution_status_message of a completed
// run with failed steps; failedStepCountFromStatusMessage reads it back.
func failedStepsStatusMessage(f model.FailedSteps) string {
	if f.FailedStepCount == 0 {
		return ""
	}
	noun := "steps"
	if f.FailedStepCount == 1 {
		noun = "step"
	}
	msg := fmt.Sprintf("%d %s failed; first: %s", f.FailedStepCount, noun, f.FirstFailedStep)
	if f.FirstFailedStepError != "" {
		msg += ": " + f.FirstFailedStepError
	}
	return msg
}

func failedStepCountFromStatusMessage(status model.WorkflowExecutionStatus, message *string) int {
	if status != model.WorkflowExecutionStatusCompleted || message == nil {
		return 0
	}
	m := failedStepsMessagePattern.FindStringSubmatch(*message)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// failedStepsFromMemo decodes the failed-steps keys of a run's Memo; absent
// keys (a clean run, or one that predates them) yield the zero value.
func (s *Service) failedStepsFromMemo(memo *commonpb.Memo) model.FailedSteps {
	var f model.FailedSteps
	if memo == nil {
		return f
	}
	fields := memo.GetFields()
	if p, ok := fields[model.MemoFailedStepCount]; ok {
		var raw any
		if err := s.dataConverter.FromPayload(p, &raw); err == nil {
			if n, ok := toInt(raw); ok {
				f.FailedStepCount = n
			}
		}
	}
	if f.FailedStepCount == 0 {
		return model.FailedSteps{}
	}
	if p, ok := fields[model.MemoFirstFailedStep]; ok {
		_ = s.dataConverter.FromPayload(p, &f.FirstFailedStep)
	}
	if p, ok := fields[model.MemoFirstFailedStepError]; ok {
		_ = s.dataConverter.FromPayload(p, &f.FirstFailedStepError)
	}
	return f
}
