package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func status(response string) AgentExecutionStatus {
	return guessAgentStatusFromResponse(NBAgentPlannerExecutorResponse{Response: response})
}

func TestGuessAgentStatus_StructuredAnswerQuotingTriggerPhrasesIsSuccess(t *testing.T) {
	cases := map[string]string{
		"quoted error key":    `{"answer":"the source logs \"error\" lines"}`,
		"unable to fetch":     `{"answer":"metrics: unable to fetch label values"}`,
		"unfortunately":       `{"answer":"Unfortunately the cache is cold on boot"}`,
		"bedrock runtime":     `{"answer":"calls bedrock runtime"}`,
		"build verification":  `{"build_verification":{"note":"unable to fetch deps"}}`,
		"empty nested error":  `{"build_verification":{"steps":[{"error":"","status":"success"}]}}`,
		"null error":          `{"answer":"done","error":null}`,
		"empty error":         `{"answer":"done","error":""}`,
		"empty errors list":   `{"answer":"done","errors":[]}`,
		"nested error object": `{"data":{"error":"step failed but the answer is complete"},"answer":"ok"}`,
		"leading whitespace":  "\n  {\"answer\":\"the source logs \\\"error\\\" lines\"}",
		// A code-analysis answer about this repo quotes our own server messages.
		"quotes server message": `{"answer":"the tool returns ` + errLlmUnableToGenerate.Error() + `"}`,
	}
	for name, response := range cases {
		assert.Equal(t, AgentExecutionStatusSuccess, status(response), name)
	}
}

func TestGuessAgentStatus_StillFailsRealFailures(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"empty object":        "{}",
		"json error key":      `{"error":"tool x failed"}`,
		"json errors list":    `{"errors":["boom"]}`,
		"status failed":       `{"status":"failed","message":"unable to fetch metrics"}`,
		"status error":        `{"status":"Error"}`,
		"server message":      errLlmUnableToGenerate.Error(),
		"free text with hint": "Unfortunately I could not do it",
		"json array":          `[{"error":"x"}]`,
		"trailing error":      "something went wrong error:",
	}
	for name, response := range cases {
		assert.Equal(t, AgentExecutionStatusFail, status(response), name)
	}
}

// Custom agents report "FAILED"/"WAITING"; the metric and the stored row both
// come from this mapping.
func TestNormalizeAgentStatus_NormalizesCustomAgentStatuses(t *testing.T) {
	cases := []struct {
		in   AgentExecutionStatus
		want AgentExecutionStatus
	}{
		{AgentExecutionStatus(ConversationStatusFailed), AgentExecutionStatusFail},
		{AgentExecutionStatus(ConversationStatusWaiting), AgentExecutionStatusWaiting},
		{AgentExecutionStatusFail, AgentExecutionStatusFail},
		{AgentExecutionStatusSuccess, AgentExecutionStatusSuccess},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, normalizeAgentStatus(c.in), string(c.in))
	}
}
