//go:build e2e

package asserts

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
)

// ============================================================
// Tier 3 — tool input / output structure
// ============================================================

// ToolInputContains asserts at least one invocation of the named tool
// passed arguments containing the given substring (case-insensitive).
// Use to verify the agent targeted the right resource: e.g.
// `ToolInputContains(t, resp, "kubectl", "-n app-25")`.
func ToolInputContains(t *testing.T, resp core.NBAgentResponse, name, want string) bool {
	t.Helper()
	wantLower := strings.ToLower(want)
	sawTool := false
	for _, inv := range resp.AgentStepResponse {
		if toolName(inv) != name {
			continue
		}
		sawTool = true
		if strings.Contains(strings.ToLower(toolArgs(inv)), wantLower) {
			return true
		}
	}
	if !sawTool {
		t.Errorf("expected at least one call to tool %q (got %v) so ToolInputContains could check args",
			name, toolNames(resp))
	} else {
		t.Errorf("expected at least one %q invocation with args containing %q; none matched",
			name, want)
	}
	return false
}

// AnyToolInputContains asserts at least one tool call (any name) has
// arguments containing the given substring. Looser than ToolInputContains;
// use when the agent could legitimately target the resource via several
// tool families.
func AnyToolInputContains(t *testing.T, resp core.NBAgentResponse, want string) bool {
	t.Helper()
	wantLower := strings.ToLower(want)
	for _, inv := range resp.AgentStepResponse {
		if strings.Contains(strings.ToLower(toolArgs(inv)), wantLower) {
			return true
		}
	}
	t.Errorf("expected at least one tool call with args containing %q; tools=%v",
		want, toolNames(resp))
	return false
}

// ToolOutputsNonEmpty asserts every tool invocation returned a non-empty
// response. A tool call whose output is empty usually means a silent
// failure the agent failed to notice — common bug signature.
func ToolOutputsNonEmpty(t *testing.T, resp core.NBAgentResponse) bool {
	t.Helper()
	ok := true
	for i, inv := range resp.AgentStepResponse {
		if strings.TrimSpace(toolOutput(inv)) == "" {
			t.Errorf("tool call #%d (%s) returned empty output", i, toolName(inv))
			ok = false
		}
	}
	return ok
}

// ToolOutputsNoErrors asserts no tool invocation's output starts with an
// error-looking marker. Heuristic only (real tool outputs may legitimately
// contain the word "error"), but catches blanket-failure patterns like
// "Error: connection refused", "error executing command:", etc.
func ToolOutputsNoErrors(t *testing.T, resp core.NBAgentResponse) bool {
	t.Helper()
	prefixes := []string{
		"error:",
		"error executing",
		"failed to ",
		"unable to ",
		"connection refused",
	}
	ok := true
	for i, inv := range resp.AgentStepResponse {
		out := strings.ToLower(strings.TrimSpace(toolOutput(inv)))
		// Only check the first ~200 bytes — substring inside a long log
		// dump is fine, but if the response *starts* with an error marker
		// that's a real failure.
		head := out
		if len(head) > 200 {
			head = head[:200]
		}
		for _, p := range prefixes {
			if strings.HasPrefix(head, p) {
				t.Errorf("tool call #%d (%s) returned error-like output: %s",
					i, toolName(inv), truncate(toolOutput(inv), 200))
				ok = false
				break
			}
		}
	}
	return ok
}

// ToolOutputContains asserts at least one tool returned output containing
// the given substring (case-insensitive). Useful to verify the agent
// actually saw the expected data: e.g. checking that a kubectl get pods
// call returned a row mentioning the expected pod name before evaluating
// whether the agent's answer used that information.
func ToolOutputContains(t *testing.T, resp core.NBAgentResponse, want string) bool {
	t.Helper()
	wantLower := strings.ToLower(want)
	for _, inv := range resp.AgentStepResponse {
		if strings.Contains(strings.ToLower(toolOutput(inv)), wantLower) {
			return true
		}
	}
	t.Errorf("expected at least one tool output to contain %q; tools=%v",
		want, toolNames(resp))
	return false
}
