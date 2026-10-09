package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Fixtures are real error strings from llm_conversation_token_usage.error_message
// on dev. A new classification rule needs a new fixture here.
var llmFailureFixtures = []struct {
	name string
	err  string
	want LLMFailureKind
}{
	{"openai no credits", "error: agent unable to process request\nAPI returned unexpected status code: 429: You have no credits remaining. Add credits to continue using the API at https://platform.openai.com/settings/organization/billing/.", LLMFailureOutOfCredits},
	{"anthropic credit balance", "error: agent unable to process request\nanthropic: API error (400 invalid_request_error): Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits.", LLMFailureOutOfCredits},
	{"anthropic credit balance via gateway", "error: agent unable to process request\nAPI returned unexpected status code: 400: Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits.", LLMFailureOutOfCredits},
	{"openrouter max cost", "error: agent unable to process request\nAPI returned unexpected status code: 402: This request's maximum cost exceeds your available credits. Add credits, or lower max_tokens or prompt size.", LLMFailureOutOfCredits},
	{"openrouter in-flight credits clear on their own", "error: agent unable to process request\nAPI returned unexpected status code: 402: This request would exceed your available credits given your current in-flight requests. Retry after in-flight requests settle, or add credits.", LLMFailureRateLimited},
	{"expired NB token", "model gemini-3.6-flash (googleai) and all 1 fallback model(s) are currently unavailable: error in stream mode: Error 401, Message: Invalid or expired NB token. Generate a new one in NudgeBee → AI Gateway → Connect., Status: UNAUTHENTICATED, Details: []", LLMFailureInvalidKey},
	{"missing vertex credential", "error: agent unable to process request\nAPI returned unexpected status code: 403: No vertex credential is configured for your organization. Ask an administrator to add a vertex key in NudgeBee integrations.", LLMFailureMissingCredential},
	{"env key not set", "react4: llm generate: error: agent unable to process request\nLLM_PROVIDER_API_KEY environment variable is not set for Google AI", LLMFailureMissingCredential},
	{"gemini bad model name", "error: agent unable to process request\nAPI returned unexpected status code: 400: * GenerateContentRequest.model: unexpected model name format ", LLMFailureUnknownModel},
	{"gateway unknown model", "error: agent unable to process request\nAPI returned unexpected status code: 400: unknown or missing model; address it as \"provider/model\" (e.g. \"anthropic/claude-opus-4-8\") or a known model name, or a tier alias (nb-fast/nb-cheap/nb-smart)", LLMFailureUnknownModel},
	{"retired model", "model gemini-3-flash-preview (googleai) and all 1 fallback model(s) are currently unavailable: error in stream mode: Error 404, Message: This model models/gemini-2.0-flash-001 is no longer available. Please update your code to use models/gemini-3.6-flash for the latest features and improvements.", LLMFailureUnknownModel},
	{"vertex resource exhausted", "error: agent unable to process request\nAPI returned unexpected status code: 429: provider API error: [{ \"error\": { \"code\": 429, \"message\": \"Resource exhausted. Please try again later.\", \"status\": \"RESOURCE_EXHAUSTED\" } } ]", LLMFailureRateLimited},
	{"context window", "error: agent unable to process request\nAPI returned unexpected status code: 400: This model's maximum context length is 130712 tokens. However, you requested 32768 output tokens and your prompt contains at least 97945 input tokens, for a total of at least 130713 tokens. Please reduce the length of the input prompt or the number of tokens to generate.", LLMFailureTooLarge},
	{"stream deadline", "error: agent unable to process request\nerror in stream mode: context deadline exceeded", LLMFailureUnavailable},
	{"high demand", "error: agent unable to process request\nerror in stream mode: Error 503, Message: This model is currently experiencing high demand. Spikes in demand are usually temporary. Please try again later., Status: UNAVAILABLE, Details: []", LLMFailureUnavailable},
	{"empty content", "error: agent unable to process request\nllm returned empty content", LLMFailureUnavailable},
	{"garbled reply", "error: agent unable to process request\nAPI returned unexpected status code: 500: Extra data: line 1 column 200 (char 199)", LLMFailureGarbledReply},
	{"unrecognised", "error: agent unable to process request\nunexpected status code: 400, body: {\"error\":\"Bad Request: something new\",\"code\":\"BAD_REQUEST\"}", LLMFailureUnknown},
}

func TestClassifyLLMFailure(t *testing.T) {
	for _, tc := range llmFailureFixtures {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLLMFailure(errors.New(tc.err)); got != tc.want {
				t.Fatalf("classifyLLMFailure() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNeedsSettingsFixOnlyForCertainSignals(t *testing.T) {
	// A daily cap stays on the quota path, which opens the circuit breaker.
	for _, k := range []LLMFailureKind{LLMFailureRateLimited, LLMFailureDailyCap, LLMFailureUnavailable, LLMFailureTooLarge, LLMFailureGarbledReply, LLMFailureUnknown} {
		if k.NeedsSettingsFix() {
			t.Errorf("%q must keep retrying", k)
		}
	}
	for _, k := range []LLMFailureKind{LLMFailureOutOfCredits, LLMFailureInvalidKey, LLMFailureMissingCredential} {
		if !k.NeedsSettingsFix() || !k.failsOnSameKey() {
			t.Errorf("%q must stop and skip same-key fallbacks", k)
		}
	}
	for _, k := range []LLMFailureKind{LLMFailureUnknownModel} {
		if !k.NeedsSettingsFix() || k.failsOnSameKey() {
			t.Errorf("%q must stop but still try other models on the same key", k)
		}
	}
}

func TestAffordableWithFewerTokensIsNotASettingsFix(t *testing.T) {
	err := errors.New("API returned unexpected status code: 402: This request requires more credits, or fewer max_tokens. You requested up to 16384 tokens, but can only afford 13780.")
	if !isAffordableWithFewerTokens(err) {
		t.Fatal("expected the fewer-max_tokens 402 to be recognised")
	}
	// The router sends it to the fallbacks first; if they all fail it is
	// reported as out of credits.
	if got := classifyLLMFailure(err); got != LLMFailureOutOfCredits {
		t.Fatalf("classifyLLMFailure() = %q, want %q", got, LLMFailureOutOfCredits)
	}
}

func TestLLMFailureMessageNamesOnlyCustomerConfigs(t *testing.T) {
	cause := errors.New("API returned unexpected status code: 429: You have no credits remaining.")

	customer := &LLMFailure{Kind: LLMFailureOutOfCredits, CustomerConfig: true, ConfigName: "OpenRouter prod", Provider: "openrouter", Model: "claude-sonnet", Cause: cause}
	msg := customer.UserMessage()
	for _, want := range []string{`"OpenRouter prod"`, "openrouter claude-sonnet", "top up"} {
		if !strings.Contains(msg, want) {
			t.Errorf("customer message %q missing %q", msg, want)
		}
	}

	platform := &LLMFailure{Kind: LLMFailureOutOfCredits, Provider: "openrouter", Model: "claude-sonnet", Cause: cause}
	msg = platform.UserMessage()
	for _, leak := range []string{"openrouter", "claude-sonnet"} {
		if strings.Contains(msg, leak) {
			t.Errorf("platform message %q names %q", msg, leak)
		}
	}
}

func TestCustomProviderIsNotNamed(t *testing.T) {
	f := &LLMFailure{Kind: LLMFailureOutOfCredits, CustomerConfig: true, ConfigName: "or-terra", Provider: "custom", Model: "openai/gpt-5.6-terra", Cause: errors.New("x")}
	if msg := f.UserMessage(); strings.Contains(msg, "custom") || !strings.Contains(msg, `"or-terra" (openai/gpt-5.6-terra)`) {
		t.Fatalf("message %q", msg)
	}
}

func TestUnknownFailureShowsRawErrorOnlyForCustomerConfigs(t *testing.T) {
	cause := errors.New("react4: llm generate: error: agent unable to process request\nunexpected status code: 400, body: something new with api_key=sk-livesecret123456 and Bearer abcdefghijklmnop")

	customer := &LLMFailure{Kind: LLMFailureUnknown, CustomerConfig: true, ConfigName: "my-llm", Cause: cause}
	msg := customer.UserMessage()
	if !strings.Contains(msg, "```") || !strings.Contains(msg, "something new") {
		t.Fatalf("customer message %q should carry the provider error in a code block", msg)
	}
	for _, leak := range []string{"react4", "agent unable to process request", "sk-livesecret", "abcdefghijklmnop"} {
		if strings.Contains(msg, leak) {
			t.Errorf("customer message %q leaks %q", msg, leak)
		}
	}

	platform := &LLMFailure{Kind: LLMFailureUnknown, Cause: cause}
	msg = platform.UserMessage()
	if strings.Contains(msg, "```") || strings.Contains(msg, "something new") {
		t.Fatalf("platform message %q must not show the raw error", msg)
	}
}

func TestLLMFailureKeepsRawErrorForLogsAndRetries(t *testing.T) {
	cause := errors.New("API returned unexpected status code: 429: rate limit")
	f := &LLMFailure{Kind: LLMFailureRateLimited, Cause: cause}
	wrapped := fmt.Errorf("react4: llm generate: %w", ErrLlmUnableToGenerate(f))

	if f.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want the provider error", f.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("the provider error must stay reachable through errors.Is")
	}
	msg, ok := LLMFailureMessage(wrapped)
	if !ok || msg != f.UserMessage() {
		t.Fatalf("LLMFailureMessage() = %q, %v", msg, ok)
	}
	if got := sanitizeErrorForUser(wrapped); got != f.UserMessage() {
		t.Fatalf("sanitizeErrorForUser() = %q, want the failure's user message", got)
	}
	if got := classifyUserFacingLLMError(wrapped); !errors.Is(got, f) && got != wrapped {
		t.Fatalf("classifyUserFacingLLMError() replaced the failure with %v", got)
	}
}

func TestProviderErrorForUserKeepsUTF8Intact(t *testing.T) {
	text := providerErrorForUser(errors.New(strings.Repeat("é", providerErrorMaxLen+10)))
	if !utf8.ValidString(text) {
		t.Fatal("truncation split a multi-byte character")
	}
}

func TestTooLargeMessageLowercasesUnnamedConfig(t *testing.T) {
	f := &LLMFailure{Kind: LLMFailureTooLarge, CustomerConfig: true, Cause: errors.New("x")}
	if msg := f.UserMessage(); !strings.Contains(msg, "too large for your AI model") {
		t.Fatalf("message %q", msg)
	}
}

func TestUnknownFailureWithOnlyInternalTextHasNoEmptyCodeBlock(t *testing.T) {
	f := &LLMFailure{Kind: LLMFailureUnknown, CustomerConfig: true, ConfigName: "my-llm", Cause: errors.New("error: agent unable to process request")}
	if msg := f.UserMessage(); strings.Contains(msg, "```") {
		t.Fatalf("message %q renders an empty code block", msg)
	}
}

func TestRetiredModelMarkerNeedsModelWording(t *testing.T) {
	if got := classifyLLMFailure(errors.New("this resource is no longer available")); got == LLMFailureUnknownModel {
		t.Fatal("a non-model 'no longer available' must not stop retries as an unknown model")
	}
}

func TestNoLLMConfigurationKeepsItsOwnMessage(t *testing.T) {
	err := ErrLlmUnableToGenerate(errors.New("no LLM configuration found for accountId=a1 agent=k8s"))
	if got := sanitizeErrorForUser(err); !strings.Contains(got, "no default AI provider configured") {
		t.Fatalf("sanitizeErrorForUser() = %q", got)
	}
}

func TestIsCustomerLLMConfig(t *testing.T) {
	cases := []struct {
		res  *LLMConfigResolution
		want bool
	}{
		{nil, false},
		{&LLMConfigResolution{Source: "env-global"}, false},
		{&LLMConfigResolution{Source: "db-global"}, true},
		{&LLMConfigResolution{Source: "db-tier"}, true},
		{&LLMConfigResolution{Source: "pinned:db:1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed", PinnedConfigSource: "db:1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed"}, true},
		{&LLMConfigResolution{Source: "pinned:env:global", PinnedConfigSource: "env:global"}, false},
	}
	for _, tc := range cases {
		if got := isCustomerLLMConfig(tc.res); got != tc.want {
			t.Errorf("isCustomerLLMConfig(%+v) = %v, want %v", tc.res, got, tc.want)
		}
	}
}

func TestSameLLMCredential(t *testing.T) {
	primary := &LLMConfigResolution{Provider: "openai", PinnedApiKey: "k1"}
	if !sameLLMCredential(primary, nil) {
		t.Error("a fallback without its own resolution uses the primary's key")
	}
	if !sameLLMCredential(primary, &LLMConfigResolution{Provider: "openai", PinnedApiKey: "k1"}) {
		t.Error("same provider and key is the same credential")
	}
	if sameLLMCredential(primary, &LLMConfigResolution{Provider: "anthropic", PinnedApiKey: "k2"}) {
		t.Error("a different provider and key is a different credential")
	}
}
