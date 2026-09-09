//go:build e2e

package asserts

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// ============================================================
// Tier 1 — tool-call structure
// ============================================================

// NoTools asserts the agent answered directly without reaching for external
// state. Planner bookkeeping (see controlTools — e.g. update_notebook) does not
// count: it touches nothing outside the planner and is invisible under ReAct3.
func NoTools(t *testing.T, resp core.NBAgentResponse) bool {
	t.Helper()
	invs := investigationTools(resp)
	return assert.Empty(t, invs,
		"expected no investigation tool invocations, got %d (tools=%v; control tools ignored, all=%v)",
		len(invs), investigationToolNames(resp), toolNames(resp))
}

// MinToolCalls asserts the agent made at least n tool calls. This is the
// load-bearing assertion that distinguishes "actually investigated" from
// "plausibly-worded refusal" when response keywords overlap with the
// question.
func MinToolCalls(t *testing.T, resp core.NBAgentResponse, n int) bool {
	t.Helper()
	invs := investigationTools(resp)
	return assert.GreaterOrEqual(t, len(invs), n,
		"expected at least %d investigation tool calls, got %d (tools=%v)",
		n, len(invs), investigationToolNames(resp))
}

// MaxToolCalls asserts the agent made at most n tool calls. Use for
// efficiency-sensitive fixtures (e.g. fast-path PromQL queries that
// should resolve in ≤3 calls).
func MaxToolCalls(t *testing.T, resp core.NBAgentResponse, n int) bool {
	t.Helper()
	invs := investigationTools(resp)
	return assert.LessOrEqual(t, len(invs), n,
		"expected at most %d investigation tool calls, got %d (tools=%v)",
		n, len(invs), investigationToolNames(resp))
}

// ExactToolCount asserts the agent made exactly n tool calls.
func ExactToolCount(t *testing.T, resp core.NBAgentResponse, n int) bool {
	t.Helper()
	invs := investigationTools(resp)
	return assert.Equal(t, n, len(invs),
		"expected exactly %d investigation tool calls, got %d (tools=%v)",
		n, len(invs), investigationToolNames(resp))
}

// ToolUsed asserts the named tool appears at least once in the invocation
// list. Match is by exact tool-name equality, not substring.
func ToolUsed(t *testing.T, resp core.NBAgentResponse, name string) bool {
	t.Helper()
	for _, inv := range resp.AgentStepResponse {
		if toolName(inv) == name {
			return true
		}
	}
	t.Errorf("expected tool %q to be invoked; got %v", name, toolNames(resp))
	return false
}

// ToolNotUsed asserts the named tool was NOT invoked. Use for forbidden
// fallbacks like "must use prometheus_execute, not shell_execute".
func ToolNotUsed(t *testing.T, resp core.NBAgentResponse, name string) bool {
	t.Helper()
	for _, inv := range resp.AgentStepResponse {
		if toolName(inv) == name {
			t.Errorf("expected tool %q NOT to be invoked, but it was (tools=%v)",
				name, toolNames(resp))
			return false
		}
	}
	return true
}

// AnyToolMatching asserts at least one tool whose name contains any of the
// given substrings was invoked. Use for family checks ("any prometheus
// tool", "any logs tool") where the exact tool name varies by provider.
func AnyToolMatching(t *testing.T, resp core.NBAgentResponse, families []string) bool {
	t.Helper()
	if len(families) == 0 {
		return true
	}
	names := toolNames(resp)
	for _, n := range names {
		lower := strings.ToLower(n)
		for _, fam := range families {
			if strings.Contains(lower, strings.ToLower(fam)) {
				return true
			}
		}
	}
	t.Errorf("expected at least one tool matching %v in invocation list; got %v",
		families, names)
	return false
}

// AllToolsUsed asserts every tool name in `names` appears at least once.
// Use for fixtures that require coordinated multi-tool investigation
// (e.g. "must use both kubectl AND describe").
func AllToolsUsed(t *testing.T, resp core.NBAgentResponse, names []string) bool {
	t.Helper()
	if len(names) == 0 {
		return true
	}
	seen := make(map[string]bool, len(resp.AgentStepResponse))
	for _, inv := range resp.AgentStepResponse {
		seen[toolName(inv)] = true
	}
	ok := true
	for _, want := range names {
		if !seen[want] {
			t.Errorf("expected tool %q to be invoked at least once; tools=%v",
				want, toolNames(resp))
			ok = false
		}
	}
	return ok
}

// ForbiddenTools asserts none of the named tools were invoked. Plural
// form of ToolNotUsed for convenient migration from YAML
// `forbidden_tools: [...]`.
func ForbiddenTools(t *testing.T, resp core.NBAgentResponse, names []string) bool {
	t.Helper()
	if len(names) == 0 {
		return true
	}
	forbidden := make(map[string]bool, len(names))
	for _, n := range names {
		forbidden[n] = true
	}
	ok := true
	for _, inv := range resp.AgentStepResponse {
		if name := toolName(inv); forbidden[name] {
			t.Errorf("forbidden tool %q was invoked; tools=%v",
				name, toolNames(resp))
			ok = false
		}
	}
	return ok
}
