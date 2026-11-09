package core

import (
	"errors"
	"strings"
	"testing"
)

func TestLiveProblemNamesOnlyCustomerConfigs(t *testing.T) {
	rc := &retryContext{
		resolution:      &LLMConfigResolution{Source: "env-global"},
		currentProvider: "googleai",
		currentModel:    "gemini-3.7-flash",
		lastErr:         errors.New("error in stream mode: Error 503, Message: This model is currently experiencing high demand."),
	}
	got := rc.liveProblem()
	if got != "Couldn't reach the platform AI model." {
		t.Fatalf("liveProblem() = %q", got)
	}
	for _, leak := range []string{"googleai", "gemini"} {
		if strings.Contains(got, leak) {
			t.Fatalf("platform status %q names %q", got, leak)
		}
	}
}

func TestLiveProblemUsesCustomerConfigName(t *testing.T) {
	rc := &retryContext{
		resolution:           &LLMConfigResolution{Source: "db-global"},
		currentProvider:      "openrouter",
		currentModel:         "claude-sonnet-4-6",
		liveConfigName:       "OpenRouter prod",
		liveConfigNameLoaded: true,
		lastErr:              errors.New("API returned unexpected status code: 429: rate limit exceeded"),
	}
	if got := rc.liveProblem(); got != `"OpenRouter prod" (openrouter claude-sonnet-4-6) is rate limited.` {
		t.Fatalf("liveProblem() = %q", got)
	}
}

func TestLiveStatusKeyIsPerAgent(t *testing.T) {
	if got := (&retryContext{agentId: "a1", agentName: "k8s"}).liveStatusKey(); got != "a1" {
		t.Fatalf("liveStatusKey() = %q", got)
	}
	if got := (&retryContext{agentName: "k8s"}).liveStatusKey(); got != "k8s" {
		t.Fatalf("liveStatusKey() = %q", got)
	}
}

func TestClearLiveStatusIsNoOpWhenNothingWasSet(t *testing.T) {
	// No DAO is configured in unit tests; clearing must not touch it.
	(&retryContext{messageId: "m1", agentId: "a1"}).clearLiveStatus()
}

func TestLiveProblemWhenCircuitSkippedTheModel(t *testing.T) {
	rc := &retryContext{resolution: &LLMConfigResolution{Source: "env-global"}}
	if got := rc.liveProblem(); got != "The platform AI model is temporarily unavailable." {
		t.Fatalf("liveProblem() = %q", got)
	}
}
