package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSanitizeErrorForUser_NoLLMConfiguration guards a real regression: before
// this case existed, an account with no default LLM provider (see
// selectAccountLLMIntegration, llm_common.go) surfaced the raw internal error
// — including the account's UUID and internal agent name — verbatim to the
// end user, with no indication of what to actually do about it.
func TestSanitizeErrorForUser_NoLLMConfiguration(t *testing.T) {
	err := errors.New("no LLM configuration found for accountId=a2a30b02-0f67-42e5-a2ab-c658230fd798, agentName=k8s_orchestrator")
	got := sanitizeErrorForUser(err)

	assert.NotContains(t, got, "a2a30b02-0f67-42e5-a2ab-c658230fd798", "must not leak the account UUID")
	assert.NotContains(t, got, "k8s_orchestrator", "must not leak the internal agent name")
	assert.Contains(t, got, "default AI provider")
	assert.Contains(t, got, "Integrations")
}

// TestSanitizeErrorForUser_UnknownErrorPassesThrough documents the existing
// fallback behavior this file relies on: an error matching neither the new
// case nor the sensitive-connection patterns is returned unchanged.
func TestSanitizeErrorForUser_UnknownErrorPassesThrough(t *testing.T) {
	err := errors.New("some genuinely unexpected failure")
	assert.Equal(t, "some genuinely unexpected failure", sanitizeErrorForUser(err))
}

// TestSanitizeErrorForUser_SensitivePatternsStillSanitized pins the
// pre-existing behavior (DB/connection errors) so the new case added above it
// doesn't shadow or reorder these checks.
func TestSanitizeErrorForUser_SensitivePatternsStillSanitized(t *testing.T) {
	err := errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")
	got := sanitizeErrorForUser(err)
	assert.Equal(t, "An internal system error occurred while processing your request. Please try again later.", got)
}

func TestSanitizeErrorForUser_NilError(t *testing.T) {
	assert.Equal(t, "", sanitizeErrorForUser(nil))
}

// TestSanitizeErrorForUser_LazyConfigFailureIsCovered pins the exact string a
// live deployment stored as the assistant's reply after the sanitizer case
// existed but was never reached: config resolves lazily at the first LLM call,
// so the error is raised inside the running planner and returns through
// conversation.go's executeErr path rather than executor.go's planner-construction
// path. The wrapping prefixes must not defeat the match.
func TestSanitizeErrorForUser_LazyConfigFailureIsCovered(t *testing.T) {
	err := errors.New("react4: llm generate: error: agent unable to process request\n" +
		"no LLM configuration found for accountId=6139c931-0385-464c-a1fb-aa1d2dd166e6, agentName=k8s_orchestrator")
	got := sanitizeErrorForUser(err)

	assert.NotContains(t, got, "6139c931-0385-464c-a1fb-aa1d2dd166e6", "must not leak the account UUID")
	assert.NotContains(t, got, "k8s_orchestrator", "must not leak the internal agent name")
	assert.NotContains(t, got, "react4", "must not leak the planner name")
	assert.Contains(t, got, "default AI provider")
}
