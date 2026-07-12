package core

import (
	"slices"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

const (
	// Per-step caps for the two fields we surface. Inputs (the query/command a
	// sub-agent ran) are the highest-signal, smallest piece; output digests add
	// reconciliation power at a small, capped cost.
	subAgentEvidenceInputCap  = 400 // per-step input byte cap
	subAgentEvidenceOutputCap = 300 // per-step output-digest byte cap (head+tail)
	// subAgentEvidenceMinBudget floors the total manifest budget so a
	// mis-set config can't shrink it to nothing.
	subAgentEvidenceMinBudget = 256

	subAgentEvidenceOpen  = "<sub_agent_evidence>\n"
	subAgentEvidenceClose = "\n</sub_agent_evidence>"
)

// buildSubAgentEvidence renders a compact, budget-bounded manifest of the concrete
// tool calls a sub-agent actually ran, for the parent orchestrator to inspect and
// reconcile against the sub-agent's prose conclusion. It is BOUNDED by construction
// so it can never re-inflate the parent context — which is the whole reason the
// sub-agent boundary exists for high-volume agents (logs/metrics/traces):
//
//   - steps are walked NEWEST-FIRST, so the budget is spent on the sub-agent's most
//     recent, most-distilled steps (the grep that produced a count, the EXPLAIN
//     result) rather than early raw-material dumps (a multi-MB fetch_logs, "get all
//     pods");
//   - each step's input and output are individually capped;
//   - the whole block is hard-capped at totalBudget.
//
// The returned block is carried as a SEPARATE field (not concatenated into the
// observation text), so it is exempt from scratchpad compression: the raw
// observation may be summarized as it ages, but this distilled manifest survives
// verbatim. Returns "" when there is nothing worth surfacing.
func buildSubAgentEvidence(steps []ToolInvocation, totalBudget int) string {
	if len(steps) == 0 {
		return ""
	}
	if totalBudget < subAgentEvidenceMinBudget {
		totalBudget = subAgentEvidenceMinBudget
	}
	// Reserve the wrapper so the emitted block never exceeds totalBudget.
	lineBudget := totalBudget - len(subAgentEvidenceOpen) - len(subAgentEvidenceClose)

	lines := make([]string, 0, len(steps))
	used := 0
	for i := len(steps) - 1; i >= 0; i-- { // newest-first
		name := toolInvocationName(steps[i])
		// Truncate the RAW strings to their caps BEFORE collapsing whitespace: a
		// sub-agent step can carry a multi-MB output (a raw log dump — see the '48'
		// scenario test), and running the collapseWhitespace regexp over the whole
		// thing just to keep a few hundred bytes is wasted CPU and allocations. Cut
		// first (a cheap byte slice), then collapse the small remainder — which also
		// tidies the marker's own newlines.
		rawInput := toolInvocationInput(steps[i])
		if len(rawInput) > subAgentEvidenceInputCap {
			rawInput = TruncateMiddle(rawInput, subAgentEvidenceInputCap/2, subAgentEvidenceInputCap/2)
		}
		input := strings.TrimSpace(collapseWhitespace(rawInput))

		rawOutput := steps[i].Response.Content
		if len(rawOutput) > subAgentEvidenceOutputCap {
			rawOutput = TruncateMiddle(rawOutput, subAgentEvidenceOutputCap/2, subAgentEvidenceOutputCap/2)
		}
		output := strings.TrimSpace(collapseWhitespace(rawOutput))

		if name == "" && input == "" && output == "" {
			continue
		}
		line := "- " + name
		if input != "" {
			line += ": " + input
		}
		if output != "" {
			line += " → " + output
		}
		if used+len(line)+1 > lineBudget {
			break
		}
		lines = append(lines, line)
		used += len(line) + 1
	}
	if len(lines) == 0 {
		return ""
	}
	// Reverse back to chronological order for readability.
	slices.Reverse(lines)
	return subAgentEvidenceOpen + strings.Join(lines, "\n") + subAgentEvidenceClose
}

// BuildSubAgentEvidenceForTool builds the bounded evidence manifest from a
// sub-agent's executed steps and logs one greppable line when non-empty,
// returning the manifest (or "" when the feature is off or there is nothing to
// surface). The caller sets the returned string on NBToolResponse.SubAgentEvidence.
//
// Call this with steps in CHRONOLOGICAL order — before any in-place reverse of
// the slice — because buildSubAgentEvidence walks newest-first to spend the
// budget on the most-distilled steps. The generic nbAgentTool wrapper builds the
// manifest inline; every bespoke agent-tool wrapper that constructs its own
// NBToolResponse (logs/metrics/traces/delegate) MUST call this too, or the
// high-volume sub-agents the manifest exists for silently drop their evidence at
// the tool boundary.
func BuildSubAgentEvidenceForTool(ctx *security.RequestContext, name string, steps []ToolInvocation) string {
	if !config.Config.LlmServerSubAgentEvidenceEnabled {
		return ""
	}
	evidence := buildSubAgentEvidence(steps, config.Config.LlmServerSubAgentEvidenceMaxChars)
	if evidence != "" {
		// Instrumentation: one greppable line per sub-agent call that produced a
		// non-empty manifest, keyed by sub_agent for structured filtering.
		ctx.GetLogger().Info("sub-agent evidence attached",
			"sub_agent", name, "evidence_bytes", len(evidence), "sub_steps", len(steps))
	}
	return evidence
}

// toolInvocationName resolves the tool name for a sub-agent step, tolerating the
// several shapes ToolInvocation can carry.
func toolInvocationName(inv ToolInvocation) string {
	if inv.Call.FunctionCall != nil && inv.Call.FunctionCall.Name != "" {
		return inv.Call.FunctionCall.Name
	}
	if inv.Response.Name != "" {
		return inv.Response.Name
	}
	return inv.Call.Type
}

// toolInvocationInput returns the raw arguments the sub-agent passed to the tool
// (the actual query/command — the highest-signal evidence).
func toolInvocationInput(inv ToolInvocation) string {
	if inv.Call.FunctionCall != nil {
		return inv.Call.FunctionCall.Arguments
	}
	return ""
}
