package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	llmruntime "nudgebee/llm/llms/runtime"
)

func TestLLMFailureKeepsRawErrorForLogsAndRetries(t *testing.T) {
	cause := errors.New("API returned unexpected status code: 429: rate limit")
	f := &llmruntime.LLMFailure{Kind: llmruntime.LLMFailureRateLimited, Cause: cause}
	wrapped := fmt.Errorf("react4: llm generate: %w", llmruntime.ErrLlmUnableToGenerate(f))

	if f.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want the provider error", f.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("the provider error must stay reachable through errors.Is")
	}
	msg, ok := llmruntime.LLMFailureMessage(wrapped)
	if !ok || msg != f.UserMessage() {
		t.Fatalf("LLMFailureMessage() = %q, %v", msg, ok)
	}
	if got := sanitizeErrorForUser(wrapped); got != f.UserMessage() {
		t.Fatalf("sanitizeErrorForUser() = %q, want the failure's user message", got)
	}
	if got := llmruntime.ClassifyUserFacingLLMError(wrapped); !errors.Is(got, f) && got != wrapped {
		t.Fatalf("classifyUserFacingLLMError() replaced the failure with %v", got)
	}
}

func TestNoLLMConfigurationKeepsItsOwnMessage(t *testing.T) {
	err := llmruntime.ErrLlmUnableToGenerate(errors.New("no LLM configuration found for accountId=a1 agent=k8s"))
	if got := sanitizeErrorForUser(err); !strings.Contains(got, "no default AI provider configured") {
		t.Fatalf("sanitizeErrorForUser() = %q", got)
	}
}
