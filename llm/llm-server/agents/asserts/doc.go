//go:build e2e

// Package asserts is a focused set of testing-helper assertion functions for
// e2e tests against the LLM agent stack.
//
// It exposes two flavors of checks:
//
//   - **Deterministic** (Tier 1-3): inspect tool-call structure, response
//     content, and tool input/output. Cheap, free of LLM calls. Examples:
//     [MinToolCalls], [ToolUsed], [ResponseContainsAny], [ResponseNotContains],
//     [ToolInputContains].
//
//   - **Semantic** (Tier 4): use a small LLM as judge to evaluate whether a
//     free-form agent response satisfies a list of expected claims. Costs
//     ~$0.001 per call. Examples: [LLMClaims], [JudgeClaims].
//
// Naming convention:
//
//   - No `Assert` prefix on function names — the package qualifier already
//     reads as `asserts.MinToolCalls(...)`. Mirrors testify.
//   - `Tool*` operates on the tool-call list.
//   - `Response*` operates on the agent's response text.
//   - `LLM*` invokes an LLM call (and so costs money).
//
// Build tag: every file in this package is //go:build e2e because asserts
// import `testing` and the llm-server core packages — we don't want them
// linked into production binaries.
package asserts

import (
	"nudgebee/llm/agents/core"

	"github.com/tmc/langchaingo/llms"
)

// toolName extracts the human-readable name of a tool from a single
// ToolInvocation entry. Returns "" if the call isn't a function call.
func toolName(inv core.ToolInvocation) string {
	if inv.Call.FunctionCall != nil {
		return inv.Call.FunctionCall.Name
	}
	return ""
}

// toolArgs returns the JSON-encoded arguments string the agent passed to a
// tool. Returns "" if not a function call.
func toolArgs(inv core.ToolInvocation) string {
	if inv.Call.FunctionCall != nil {
		return inv.Call.FunctionCall.Arguments
	}
	return ""
}

// toolOutput returns the textual content the tool returned for a single
// invocation. Returns "" if no response payload was recorded.
func toolOutput(inv core.ToolInvocation) string {
	return inv.Response.Content
}

// toolNames returns just the function names of every tool invocation in
// order. Useful for set/sequence checks.
func toolNames(resp core.NBAgentResponse) []string {
	out := make([]string, 0, len(resp.AgentStepResponse))
	for _, inv := range resp.AgentStepResponse {
		out = append(out, toolName(inv))
	}
	return out
}

// joinResponse concatenates the agent's response chunks into a single
// string. The Response field is []string; most assertions operate on the
// joined body.
func joinResponse(resp core.NBAgentResponse) string {
	if len(resp.Response) == 0 {
		return ""
	}
	out := resp.Response[0]
	for i := 1; i < len(resp.Response); i++ {
		out += "\n" + resp.Response[i]
	}
	return out
}

// Silence unused-import warnings when the file is built but no symbol from
// llms is referenced directly (the helpers above use llms transitively via
// core.ToolInvocation).
var _ = llms.ToolCall{}
