package core

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// ScratchpadContext carries optional context for LLM-backed observation summarization.
// When nil or zero-valued, ConstructScratchPad falls back to byte truncation.
type ScratchpadContext struct {
	Ctx     *security.RequestContext
	Request NBAgentRequest
	// Tracker deduplicates compression visibility DB writes across repeated
	// ConstructScratchPad calls. Nil is safe — visibility is simply skipped.
	Tracker *CompressionTracker
}

// getMaxObservationChars returns the maximum number of bytes for a single observation in the scratchpad.
func getMaxObservationChars() int {
	limit := config.Config.LlmConfigAutoSelectionMaxObservationLen
	if limit <= 0 {
		return 65536 // Default 64KB
	}
	// Clamp to a safe minimum to ensure subtraction (e.g. -2048) doesn't result in negative lengths
	if limit < 4096 {
		return 4096
	}
	return limit
}

// recentStepsFullContext is how many recent tool steps retain full observations.
// Older steps get semantic compression (thought + tool input + truncated preview).
// Kept at 10 — sub-agents with fewer steps rely on the per-step budget compression
// path in buildScratchpad (triggered by LlmServerAgentMaxScratchpadChars) instead.
const recentStepsFullContext = 10

// compressedObservationPreview is the max bytes kept for an older step's observation
// after semantic compression (byte truncation fallback).
// Raised from 100 to 500 so truncated observations retain enough context to be useful.
const compressedObservationPreview = 500

// compressObservation replaces a full observation with a short preview for older steps.
// If truncation + prefix would be larger than the original, the observation is returned as-is.
func compressObservation(obs string) string {
	if len(obs) <= compressedObservationPreview {
		return obs
	}
	preview := TruncateHead(obs, compressedObservationPreview)
	result := fmt.Sprintf("[output truncated — %d chars] %s", len(obs), preview)
	if len(result) >= len(obs) {
		return obs
	}
	return result
}

// TruncateHead truncates s to at most maxBytes from the start, ensuring the cut
// does not split a multi-byte UTF-8 character.
func TruncateHead(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes to find a valid rune boundary
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// TruncateMiddle truncates s by keeping headBytes from the start and tailBytes
// from the end, injecting a truncation marker in between.
func TruncateMiddle(s string, headBytes, tailBytes int) string {
	if len(s) <= headBytes+tailBytes {
		return s
	}

	head := TruncateHead(s, headBytes)
	tail := truncateTail(s, tailBytes)

	return fmt.Sprintf("%s\n\n[... output truncated — %d chars removed ...]\n\n%s", head, len(s)-len(head)-len(tail), tail)
}

// truncateTail returns the last maxBytes of s, ensuring the cut does not split
// a multi-byte UTF-8 character.
func truncateTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	// Walk forward from start to find a valid rune boundary
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// ConstructScratchPad generates a formatted XML scratchpad from intermediate tool steps.
// It handles plan summaries, tool results, and applies budget-based compression.
// When a ScratchpadContext is provided and the summarization feature flag is enabled,
// older observations are summarized by an LLM instead of blindly truncated.
func ConstructScratchPad(intermediateSteps []NBAgentPlannerToolActionStep, sctx ...ScratchpadContext) string {
	var scratchpadCtx *ScratchpadContext
	if len(sctx) > 0 {
		scratchpadCtx = &sctx[0]
	}
	plannerSummaryToolIndex := map[int]int{}
	for i := range intermediateSteps {
		plan := intermediateSteps[i]
		if plan.Action.Tool == plannerDummyTool {
			plannerSummaryToolIndex[i] = -1
			nextPlannerFound := false
			for j := i + 1; j < len(intermediateSteps); j++ {
				plannerStep := intermediateSteps[j]
				if strings.EqualFold(plannerStep.Action.Tool, plannerDummyTool) {
					if strings.EqualFold(intermediateSteps[j-1].Action.Tool, ToolLlm) {
						plannerSummaryToolIndex[i] = j - 1
					}
					nextPlannerFound = true
					break
				}
			}
			if !nextPlannerFound && strings.EqualFold(intermediateSteps[len(intermediateSteps)-1].Action.Tool, ToolLlm) {
				plannerSummaryToolIndex[i] = len(intermediateSteps) - 1
			}
		}
	}

	type StepComponent struct {
		Header       string
		Observation  string
		Footer       string
		IsToolStep   bool
		IsCompressed bool
		StepIndex    int // index into intermediateSteps for this component
	}
	components := make([]StepComponent, 0, len(intermediateSteps))

	summaryToolIndex := -1
	previousNodeType := ""

	for i, plan := range intermediateSteps {
		if i <= summaryToolIndex {
			continue
		}

		comp := StepComponent{}

		if plannerSummaryToolIndex[i] > 0 {
			var sb strings.Builder
			sb.WriteString("  <plan_summary>\n")
			fmt.Fprintf(&sb, "    <plan><![CDATA[%s]]></plan>\n", plan.Action.Log)
			comp.Header = sb.String()

			obs := intermediateSteps[plannerSummaryToolIndex[i]].Observation
			if len(obs) > getMaxObservationChars() {
				obs = TruncateMiddle(obs, 2048, getMaxObservationChars()-2048)
			}
			comp.Observation = obs
			comp.Footer = "  </plan_summary>\n"

			components = append(components, comp)
			summaryToolIndex = plannerSummaryToolIndex[i]
			previousNodeType = "plan_summary"
			continue
		}

		if plan.Action.Tool == plannerDummyTool {
			if previousNodeType == "plan" {
				comp.Header = "  </plan>\n"
			}
			comp.Header += "  <plan>\n"
			comp.Header += fmt.Sprintf("    <thought><![CDATA[%s]]></thought>\n", plan.Action.Log)
			components = append(components, comp)
			previousNodeType = "plan"
			continue
		}

		// Tool Step
		comp.StepIndex = i
		comp.IsToolStep = true
		hhsb := "  <step>\n"
		hhsb += fmt.Sprintf("    <id>%s</id>\n", plan.Action.ToolID)
		if !strings.EqualFold(plan.Action.Tool, ToolLlm) {
			hhsb += fmt.Sprintf("    <thought><![CDATA[%s]]></thought>\n", plan.Action.Log)
		}
		hhsb += fmt.Sprintf("    <tool>%s</tool>\n", plan.Action.Tool)
		hhsb += fmt.Sprintf("    <query><![CDATA[%s]]></query>\n", plan.Action.ToolInput)
		comp.Header = hhsb
		obs := plan.Observation
		if obs == "" {
			obs = "No data found. The tool returned an empty response."
		}

		// PRUNING: Minimize fixed failures
		if plan.Status == ToolStatusFailure {
			isFixed := false
			for j := i + 1; j < len(intermediateSteps); j++ {
				next := intermediateSteps[j]
				if next.Status == ToolStatusSuccess && (next.Action.Tool == plan.Action.Tool || strings.HasPrefix(next.Action.ToolID, plan.Action.ToolID+"_")) {
					isFixed = true
					break
				}
			}

			// Exceptions for findings: preserve these even if fixed as they are useful for RCA
			lowerObs := strings.ToLower(obs)
			isFinding := false
			for _, sub := range []string{"permission denied", "access denied", "not found", "does not exist"} {
				if strings.Contains(lowerObs, sub) {
					isFinding = true
					break
				}
			}

			if isFixed && !isFinding && len(obs) > 200 {
				obs = obs[:200] + "\n[failure log minimized — retried successfully]"
			}
		}

		if len(obs) > getMaxObservationChars() {
			obs = TruncateMiddle(obs, 2048, getMaxObservationChars()-2048)
		}
		comp.Observation = obs

		var fsb strings.Builder
		if len(plan.References) > 0 {
			fsb.WriteString("    <references>\n")
			for _, ref := range plan.References {
				fmt.Fprintf(&fsb, "      <reference text=%q url=%q type=%q description=%q />\n", ref.Text, ref.Url, ref.Type, ref.Description)
			}
			fsb.WriteString("    </references>\n")
		}
		fsb.WriteString("  </step>\n")
		comp.Footer = fsb.String()

		components = append(components, comp)
	}

	if previousNodeType == "plan" {
		if len(components) > 0 {
			components[len(components)-1].Footer += "  </plan>\n"
		}
	}

	maxChars := config.Config.LlmServerAgentMaxScratchpadChars
	if maxChars <= 0 {
		maxChars = 200000
	}

	// Calculate total
	total := len("<observation>\n</observation>\n\n")
	for _, c := range components {
		total += len(c.Header) + len(c.Observation) + len(c.Footer) + len("    <response><![CDATA[]]></response>\n")
	}

	// Compress from oldest if over budget.
	// When a ScratchpadContext is provided and summarization is enabled, we use an LLM
	// to generate a concise summary instead of blindly truncating to 100 bytes.
	if total > maxChars {
		for i := 0; i < len(components); i++ {
			if !components[i].IsToolStep || len(components[i].Observation) < 500 {
				continue
			}
			oldLen := len(components[i].Observation)
			obs := components[i].Observation

			si := components[i].StepIndex
			if scratchpadCtx != nil && scratchpadCtx.Ctx != nil {
				components[i].Observation = SummarizeObservation(scratchpadCtx.Ctx, &intermediateSteps[si], scratchpadCtx.Request, obs)
			} else {
				components[i].Observation = compressObservation(obs)
			}
			components[i].IsCompressed = true
			// Mark the original step so compression visibility can detect it.
			if intermediateSteps[si].CompressedObservation == "" {
				intermediateSteps[si].CompressedObservation = components[i].Observation
			}
			total -= (oldLen - len(components[i].Observation))
			if total <= maxChars {
				break
			}
		}
	}

	// Persist compression visibility so the UI shows a summary of what was compressed.
	if scratchpadCtx != nil {
		SaveCompressionVisibility(scratchpadCtx.Ctx, scratchpadCtx.Request, intermediateSteps, scratchpadCtx.Tracker)
	}

	var res strings.Builder
	res.WriteString("<observation>\n")
	for _, c := range components {
		res.WriteString(c.Header)
		if c.Observation != "" {
			fmt.Fprintf(&res, "    <response><![CDATA[%s]]></response>\n", c.Observation)
		}
		res.WriteString(c.Footer)
	}
	res.WriteString("</observation>\n\n")

	// Append data quality summary so the solver/LLM knows about tool failures and can
	// decide whether the gathered data is sufficient to answer the user's question.
	// We track unique tools by their LAST outcome to avoid counting retries as separate failures.
	toolLastStatus := make(map[string]ToolStatus)
	for _, step := range intermediateSteps {
		if strings.EqualFold(step.Action.Tool, plannerDummyTool) || step.Action.Tool == "" || strings.EqualFold(step.Action.Tool, ToolLlm) {
			continue
		}
		key := step.Action.Tool
		if step.Action.ToolID != "" {
			key = step.Action.ToolID
			// Normalize retry suffixes (E1_1, E1_2 → E1) so retries collapse to the original tool
			if idx := strings.LastIndex(key, "_"); idx > 0 {
				suffix := key[idx+1:]
				isRetry := suffix != ""
				for _, r := range suffix {
					if r < '0' || r > '9' {
						isRetry = false
						break
					}
				}
				if isRetry {
					key = key[:idx]
				}
			}
		}
		toolLastStatus[key] = step.Status
	}

	failedCount := 0
	emptyCount := 0
	successCount := 0
	for _, status := range toolLastStatus {
		switch status {
		case ToolStatusFailure:
			failedCount++
		case ToolStatusEmptyResult:
			emptyCount++
		default:
			successCount++
		}
	}

	if failedCount > 0 || emptyCount > 0 {
		totalUniqueTools := failedCount + emptyCount + successCount
		fmt.Fprintf(&res, "<data_quality failed=\"%d\" empty=\"%d\" success=\"%d\" total=\"%d\">\n", failedCount, emptyCount, successCount, totalUniqueTools)
		res.WriteString("Some tool calls FAILED or returned NO DATA. Review the observations above to assess whether the gathered data is sufficient to answer the user's question.\n")
		res.WriteString("- If the data IS sufficient despite some failures, provide a confident final answer based on what was gathered.\n")
		res.WriteString("- If critical data is MISSING and you cannot provide a reliable answer, you MUST:\n")
		res.WriteString("  1. Clearly state what could NOT be investigated and why.\n")
		res.WriteString("  2. Include a '### Recommended Next Steps' section with specific CLI commands or manual steps the user can run to gather the missing information.\n")
		res.WriteString("  3. Do NOT fabricate findings for the missing data.\n")
		res.WriteString("</data_quality>\n\n")
	}

	final := res.String()
	if len(final) > maxChars {
		final = "<observation>\n  <note>Budget exceeded. Earliest steps truncated.</note>\n" + truncateTail(final, maxChars)
	}
	return final
}
