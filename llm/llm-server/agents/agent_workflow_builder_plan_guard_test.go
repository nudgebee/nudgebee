package agents

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func planCompletion(content string, stopReason string) *llms.ContentResponse {
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{{Content: content, StopReason: stopReason}},
	}
}

// A plan that hit the provider's output ceiling is unusable: its own prose describes tasks that
// were never written. The reported case reached 99,799 characters — one kubectl fragment repeated
// 636 times, ending mid-token — and the approval card rendered it with an "Approve and Build"
// button, which built a one-task automation described as a four-task one (#35392).
//
// Providers spell the stop reason differently, so all of them must be caught.
func TestSafePlanContent_RejectsTruncatedGeneration(t *testing.T) {
	for _, stopReason := range []string{"length", "max_tokens", "MAX_TOKENS", "Max_Tokens"} {
		_, err := safePlanContent(planCompletion("Here's my plan for building your automation:", stopReason))

		if assert.Error(t, err, "stop_reason %q means generation was cut off", stopReason) {
			assert.ErrorIs(t, err, errPlanIncomplete)
			assert.Contains(t, err.Error(), "output limit", "the reason must be diagnosable from the error")
		}
	}
}

// Length alone still catches a runaway that happened to fit inside the ceiling.
func TestSafePlanContent_RejectsOversizedPlan(t *testing.T) {
	runaway := strings.Repeat("{range .status.containerStatuses[*].lastState.terminated.reason}{.}", 2000)
	assert.Greater(t, len(runaway), maxPlanChars, "fixture must actually exceed the cap")

	_, err := safePlanContent(planCompletion(runaway, "stop"))

	if assert.Error(t, err) {
		assert.ErrorIs(t, err, errPlanIncomplete)
		assert.Contains(t, err.Error(), "exceeds")
	}
}

// The guard must not reject plans that are merely long-ish but complete, or the feature becomes
// unusable for genuinely multi-task automations.
func TestSafePlanContent_AcceptsACompletePlan(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		stopReason string
	}{
		{name: "normal plan", content: "**1. Automation Name:** pod-health-monitor", stopReason: "stop"},
		{name: "anthropic end_turn", content: "**1. Automation Name:** pod-health-monitor", stopReason: "end_turn"},
		{name: "empty stop reason", content: "**1. Automation Name:** pod-health-monitor", stopReason: ""},
		{name: "large but within the cap", content: strings.Repeat("a", maxPlanChars-1), stopReason: "stop"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := safePlanContent(planCompletion(tc.content, tc.stopReason))
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tc.content), plan)
		})
	}
}

// "stop_reason mentions length" must not be confused with an empty response, which is a different
// failure with a different message — and must still be rejected.
func TestSafePlanContent_RejectsEmptyResponseDistinctly(t *testing.T) {
	_, err := safePlanContent(&llms.ContentResponse{})

	if assert.Error(t, err) {
		assert.False(t, errors.Is(err, errPlanIncomplete),
			"no choices is a transport failure, not a model that overran — retrying the same way will not help")
		assert.Contains(t, err.Error(), "no choices")
	}
}

// get_task_schema returned "not found" as an ordinary string, so a miss was recorded as a
// SUCCESSFUL tool call. One reported build asked for `notifications.slack`, was told it did not
// exist with status=success, and reached three different conclusions about the same capability
// across two runs before finding `notifications.im` (#35392).
func TestToolGetTaskSchema_UnknownTypeIsReportedAsAnError(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	registry := `{"tasks":[{"name":"k8s.cli","description":"Run kubectl"}]}`

	got := agent.toolGetTaskSchema(map[string]interface{}{"task_type": "notifications.slack"}, registry)

	assert.True(t, strings.HasPrefix(got, "Error:"),
		"a miss must read as a failure, not as a successful lookup that happened to return prose")
	assert.Contains(t, got, "notifications.slack", "name the type that was not found")
	assert.NotContains(t, got, "list_task_types", "must not point at a tool that does not exist")
}

// The success path must keep returning the schema unchanged.
func TestToolGetTaskSchema_KnownTypeStillReturnsSchema(t *testing.T) {
	agent := newWorkflowBuilderAgent("test-account")
	registry := `{"tasks":[{"name":"k8s.cli","description":"Run kubectl"}]}`

	got := agent.toolGetTaskSchema(map[string]interface{}{"task_type": "k8s.cli"}, registry)

	assert.False(t, strings.HasPrefix(got, "Error:"))
	assert.Contains(t, got, "k8s.cli")
}
