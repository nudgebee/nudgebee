package core

import (
	"errors"
	"fmt"
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

// #39704: planner failures reached users verbatim. The cases are the shapes
// stored as FAILED replies on test; none may show planner internals.
func TestSanitizeErrorForUser_PlannerInternalsAreHidden(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"text tool call after the re-prompt": {fmt.Errorf("react4: %w (no tool ran): %w", errReact4TextToolCall, ErrParseFailure), runStoppedEarlyMessage},
		"summary wrote a plan":               {errSummaryTextToolCall, runStoppedEarlyMessage},
		"raw provider 400": {fmt.Errorf("react4: llm generate: %w",
			ErrLlmUnableToGenerate(errors.New("API returned unexpected status code: 400: Extra data: line 1 column 58 (char 57)"))), providerErrorMessage},
		"parse failure mentioning a status code":      {fmt.Errorf("react4: reply was 500 chars: %w", ErrParseFailure), runStoppedEarlyMessage},
		"classified provider error keeps its message": {fmt.Errorf("react4: llm generate: %w", ErrLLMServiceUnavailable), ErrLLMServiceUnavailable.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			got := sanitizeErrorForUser(tc.err)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "react4")
		})
	}
}

// The provider-error classifier matches on substrings, so it must not see
// errors that never came from the provider: those keep their own text.
func TestSanitizeErrorForUser_NonProviderErrorIsNotCalledAnOutage(t *testing.T) {
	err := errors.New("integration connection not configured for account")
	assert.Equal(t, err.Error(), sanitizeErrorForUser(err))
}

// Billing exhaustion must say so: the generic "AI service returned an error" hid an actionable cause.
// Messages are the ones stored as FAILED replies on dev.
func TestSanitizeErrorForUser_OutOfCreditIsNamed(t *testing.T) {
	for name, raw := range map[string]string{
		"anthropic 400":       "API returned unexpected status code: 400: Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits.",
		"openai 429":          "API returned unexpected status code: 429: You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/.",
		"openrouter 402":      "API returned unexpected status code: 402: This request requires more credits, or fewer max_tokens. You requested up to 16384 tokens, but can only afford 14514.",
		"openrouter inflight": "API returned unexpected status code: 402: This request would exceed your available credits given your current in-flight requests.",
		"openai quota":        "API returned unexpected status code: 429: insufficient_quota: You exceeded your current quota, please check your plan and billing details.",
	} {
		t.Run(name, func(t *testing.T) {
			got := sanitizeErrorForUser(fmt.Errorf("react4: llm generate: %w", ErrLlmUnableToGenerate(errors.New(raw))))
			assert.Equal(t, ErrLLMOutOfCredit.Error(), got)
		})
	}
	// A plain rate limit is still a rate limit.
	rl := fmt.Errorf("react4: llm generate: %w", ErrLlmUnableToGenerate(errors.New("API returned unexpected status code: 429: Too Many Requests")))
	assert.Equal(t, ErrLLMRateLimited.Error(), sanitizeErrorForUser(rl))
}
