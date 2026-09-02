package core

import (
	"testing"

	"github.com/tmc/langchaingo/llms"
)

// TestRegisterNBAgentFactoryWithAliases verifies that a factory registered under
// a primary name plus legacy aliases is resolvable under every name — the
// back-compat guarantee relied on by the *_debug → *_orchestrator rename so that
// stored conversation history and @old_name invocations keep resolving.
func TestRegisterNBAgentFactoryWithAliases(t *testing.T) {
	sentinel := &struct{ NBAgent }{}
	factory := func(accountId string) (NBAgent, error) { return sentinel, nil }

	RegisterNBAgentFactoryWithAliases("test_primary_orchestrator", factory, "test_legacy_debug", "test_other_alias")

	for _, name := range []string{"test_primary_orchestrator", "test_legacy_debug", "test_other_alias"} {
		agent, err := getSystemAgent(name, "acct-1")
		if err != nil {
			t.Errorf("getSystemAgent(%q) returned error: %v", name, err)
			continue
		}
		if agent != sentinel {
			t.Errorf("getSystemAgent(%q) resolved to a different factory result", name)
		}
	}

	// Case-insensitivity: lookup lowercases the key, so a mixed-case alias resolves.
	if _, err := getSystemAgent("TEST_LEGACY_DEBUG", "acct-1"); err != nil {
		t.Errorf("expected case-insensitive alias resolution, got error: %v", err)
	}
}

// TestLastNonTrivialStepContent covers the fallback lookup nbAgentTool.Call uses
// when a sub-agent finishes with no synthesized final answer: it must pick the
// most recent tool observation that isn't empty or an empty JSON container, and
// must not treat "[]"/"{}" placeholders as real content — those are indistinguishable
// from "the tool ran and found nothing", not an answer worth surfacing.
func TestLastNonTrivialStepContent(t *testing.T) {
	step := func(content string) ToolInvocation {
		return ToolInvocation{Response: llms.ToolCallResponse{Content: content}}
	}

	t.Run("no steps", func(t *testing.T) {
		content, ok := lastNonTrivialStepContent(nil)
		if ok {
			t.Errorf("expected ok=false for nil steps, got content %q", content)
		}
	})

	t.Run("all steps trivial", func(t *testing.T) {
		content, ok := lastNonTrivialStepContent([]ToolInvocation{step(""), step("[]"), step("{}"), step("null"), step("  []\n")})
		if ok {
			t.Errorf("expected ok=false when every step is empty/empty-JSON, got content %q", content)
		}
	})

	t.Run("picks the most recent non-trivial step, not the first", func(t *testing.T) {
		steps := []ToolInvocation{
			step(`{"matches":["stale discovery data"]}`),
			step("[]"),
			step(`{"matches":["fresh discovery data"]}`),
		}
		content, ok := lastNonTrivialStepContent(steps)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if content != `{"matches":["fresh discovery data"]}` {
			t.Errorf("expected the newest non-trivial step, got %q", content)
		}
	})

	t.Run("skips trailing empty steps to find an earlier real one", func(t *testing.T) {
		steps := []ToolInvocation{
			step("real answer"),
			step(""),
			step("{}"),
		}
		content, ok := lastNonTrivialStepContent(steps)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if content != "real answer" {
			t.Errorf("expected to fall back to the earlier real step, got %q", content)
		}
	})

	t.Run("does not mutate the input slice order", func(t *testing.T) {
		steps := []ToolInvocation{step("first"), step("second"), step("third")}
		_, _ = lastNonTrivialStepContent(steps)
		if steps[0].Response.Content != "first" || steps[2].Response.Content != "third" {
			t.Errorf("lastNonTrivialStepContent must not reorder the caller's slice, got %+v", steps)
		}
	})
}
