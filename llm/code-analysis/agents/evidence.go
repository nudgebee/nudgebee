package agents

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/llm"

	"github.com/tmc/langchaingo/llms"
)

const (
	// evidenceInlineThreshold: evidence at/below this size is injected verbatim
	// (small payloads cost little and lose nothing).
	evidenceInlineThreshold = 8 * 1024
	// evidenceSummaryInputCap bounds the summarizer's own input so even
	// multi-hundred-MB evidence is one bounded LLM call (head+tail of the payload).
	evidenceSummaryInputCap = 48 * 1024
	// evidenceExcerptBytes: verbatim head+tail kept alongside the digest, so the
	// exact first error and final state survive even if the digest omits them.
	evidenceExcerptBytes = 6 * 1024
)

// buildEvidenceBlock renders the incident-evidence section for a specialist's enhanced
// query. Small evidence is injected verbatim (unchanged behaviour). Large evidence is
// replaced with a compact LLM "Event Understanding" digest plus a bounded verbatim
// head+tail excerpt — so the agent understands the incident without the full payload
// being re-sent on every ReAct iteration, which is the dominant token cost on
// event-triage runs (a large payload lives in the cacheable prefix and is re-sent
// N times). Falls back to a bounded excerpt when summarization is unavailable or
// fails, so it is never worse than a blunt cap.
func buildEvidenceBlock(ctx context.Context, llmClient *llm.Client, logger *common.Logger, header, logs string) string {
	if strings.TrimSpace(logs) == "" {
		return ""
	}
	if len(logs) <= evidenceInlineThreshold {
		return fmt.Sprintf("=== %s ===\n%s\n\n", header, logs)
	}

	excerpt := evidenceHeadTail(logs, evidenceExcerptBytes)
	digest := summarizeEvidence(ctx, llmClient, evidenceHeadTail(logs, evidenceSummaryInputCap))
	if digest == "" {
		if logger != nil {
			logger.Log(common.EventPlanningProgress, "Evidence summarization unavailable; using bounded excerpt",
				map[string]any{"evidence_bytes": len(logs)})
		}
		return fmt.Sprintf("=== %s (truncated — %d bytes total; key head/tail shown) ===\n%s\n\n", header, len(logs), excerpt)
	}
	if logger != nil {
		logger.Log(common.EventPlanningProgress, "Summarized large event evidence into a digest",
			map[string]any{"evidence_bytes": len(logs), "digest_bytes": len(digest)})
	}
	return fmt.Sprintf("=== EVENT UNDERSTANDING (digest of %d bytes of evidence) ===\n%s\n\n=== KEY LOG EXCERPT (verbatim head+tail) ===\n%s\n\n",
		len(logs), digest, excerpt)
}

// summarizeEvidence asks the LLM for a compact, signal-preserving digest of incident
// evidence. Best-effort: returns "" on any failure so the caller falls back to an
// excerpt.
func summarizeEvidence(ctx context.Context, llmClient *llm.Client, evidence string) string {
	if llmClient == nil || strings.TrimSpace(evidence) == "" {
		return ""
	}
	const instr = "You are triaging a production incident to guide a code-level root-cause analysis. " +
		"From the evidence below, produce a terse, structured digest that PRESERVES the signals an engineer needs: " +
		"1) the root error / exception signature (verbatim, including any file:line or stack frames); " +
		"2) the affected service / component / workload; " +
		"3) key symptoms with timestamps; " +
		"4) the most likely code areas/files to investigate; " +
		"5) anything pointing to the actual fix. " +
		"Drop repetition and noise. Output only the digest.\n\n=== EVIDENCE ===\n"
	msgs := []llms.MessageContent{{
		Role:  llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{llms.TextPart(instr + evidence)},
	}}
	resp, err := llmClient.GenerateContentNoRetry(ctx, msgs, llms.WithTemperature(0))
	if err != nil || resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Content)
}

// evidenceHeadTail keeps the first ~3/4 and last ~1/4 of s within maxBytes, with a
// marker for the dropped middle, without splitting a UTF-8 rune. Errors usually start
// at the top (first failure) and end at the bottom (final state), so both ends carry
// signal.
func evidenceHeadTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	head := maxBytes * 3 / 4
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	tailStart := len(s) - (maxBytes - head)
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	return fmt.Sprintf("%s\n\n[... %d bytes omitted ...]\n\n%s", s[:head], tailStart-head, s[tailStart:])
}
