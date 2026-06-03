package planners

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"

	"nudgebee/code-analysis-agent/common"
)

// defaultReflectionEvery is the cadence at which the planner runs a reflection
// pass. K=5 means: after every 5 completed tool calls, ask the cheap model to
// consolidate observations into the ledger and judge readiness to submit.
// Tunable per-planner via SetReflectionCadence; this default balances cost
// against early-termination payoff.
const defaultReflectionEvery = 5

// minStepsBeforeReflection prevents the planner from reflecting before the
// agent has produced ANY observations. Reflecting at step 0 just echoes the
// goal and wastes a call.
const minStepsBeforeReflection = 3

// reflectionTimeout caps the cheap-model call. Reflection is best-effort —
// if it times out, the loop continues with the existing ledger. We use the
// no-retry path on the LLM client (see GenerateContentNoRetry) so the timeout
// applies to a single attempt; a longer budget would otherwise let the retry
// chain (2s+4s+8s+16s+...) chew through this window without ever succeeding.
const reflectionTimeout = 45 * time.Second

// reflect calls the LLM with the goal, prior ledger, and recent tool history,
// asking it to return an updated ledger (in the ledgerJSONSchema shape) plus
// a ready_to_submit flag. Failures are non-fatal: on any error the prior
// ledger is preserved and the loop continues with the existing reactive
// guardrails. This is intentional — reflection is a steering signal, not a
// safety-critical check.
func (p *ReActPlanner) reflect(ctx context.Context, goal *Goal, prior *Ledger, recentSteps []Step) (*Ledger, error) {
	if goal == nil {
		return prior, fmt.Errorf("reflect: goal is nil")
	}

	prompt := buildReflectionPrompt(goal, prior, recentSteps)

	rctx, cancel := context.WithTimeout(ctx, reflectionTimeout)
	defer cancel()

	messages := []llms.MessageContent{
		{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextPart("You are the reflection step of a code-investigation agent. Your job is to consolidate raw tool observations into a structured ledger and decide whether the agent has enough evidence to submit a final answer. Be ruthless about declaring ready_to_submit=true once the termination criterion is met — wandering further wastes iterations.")},
		},
		{
			Role:  llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{llms.TextPart(prompt)},
		},
	}

	response, err := p.llmClient.GenerateContentNoRetry(rctx, messages)
	if err != nil {
		return prior, fmt.Errorf("reflect: LLM call failed: %w", err)
	}
	if len(response.Choices) == 0 {
		return prior, fmt.Errorf("reflect: LLM returned no choices")
	}

	raw := response.Choices[0].Content
	jsonStr := p.extractJSONFromContent(p.logger, raw)
	if jsonStr == "" {
		jsonStr = p.repairTruncatedJSON(raw)
	}
	if jsonStr == "" {
		return prior, fmt.Errorf("reflect: no parseable JSON in LLM output (len=%d)", len(raw))
	}

	next, err := ParseLedgerJSON(jsonStr)
	if err != nil {
		return prior, fmt.Errorf("reflect: parse ledger: %w", err)
	}

	if p.logger != nil {
		p.logger.Log(common.EventPlanningProgress, "Reflection updated ledger", map[string]any{
			"findings":           len(next.Findings),
			"citations":          len(next.Citations),
			"open_sub_questions": len(next.OpenSubQuestions),
			"ready_to_submit":    next.ReadyToSubmit,
			"confidence":         next.Confidence,
		})
	}
	return next, nil
}

// buildReflectionPrompt assembles the human-message payload for a reflection
// call. Kept as a free function so unit tests can snapshot the prompt without
// instantiating a planner + LLM client.
func buildReflectionPrompt(goal *Goal, prior *Ledger, recentSteps []Step) string {
	var b strings.Builder
	b.WriteString("# Goal recap\n\n")
	b.WriteString("Original query: ")
	b.WriteString(goal.Query)
	b.WriteString("\nMode: ")
	if goal.Mode == "" {
		b.WriteString("(specialist)")
	} else {
		b.WriteString(goal.Mode)
	}
	b.WriteString("\nTermination criterion: ")
	b.WriteString(goal.TerminationCriterion)
	b.WriteString("\n\n")

	b.WriteString("# Contract for ready submission\n\n")
	b.WriteString(goal.Contract)
	b.WriteString("\n\n")

	b.WriteString("# Prior ledger\n\n")
	if prior == nil || prior.IsEmpty() {
		b.WriteString("(empty — this is the first reflection)\n\n")
	} else {
		b.WriteString(prior.ToPromptBlock())
		b.WriteString("\n")
	}

	b.WriteString("# Recent investigation steps\n\n")
	if len(recentSteps) == 0 {
		b.WriteString("(no new steps since last reflection)\n\n")
	} else {
		for _, s := range recentSteps {
			fmt.Fprintf(&b, "Step %d  action=%s\n", s.Number, s.Action)
			if s.Thought != "" {
				fmt.Fprintf(&b, "  thought: %s\n", truncateLine(s.Thought, 240))
			}
			if s.Observation != "" {
				fmt.Fprintf(&b, "  observation: %s\n", truncateLine(s.Observation, 1200))
			}
			if s.Status == "failed" && s.Error != "" {
				fmt.Fprintf(&b, "  ERROR: %s\n", truncateLine(s.Error, 240))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("# Your task\n\n")
	b.WriteString(strings.TrimSpace(`
Update the ledger by integrating the recent steps into the prior ledger. Specifically:

1. Promote new facts from observations into "findings", citing the step IDs that established them.
2. Extract structured citations from any file_view / file content observations that materially support a claim. Each citation needs file_path, line_start, snippet, and ideally a one-line note.
3. Update "open_sub_questions": remove any that are now answered (a citation exists for them); add any new sub-questions you realize are still needed.
4. If — and only if — the termination criterion is now met, write a 1–3 sentence plain-prose "answer" and set "ready_to_submit": true. Otherwise set "ready_to_submit": false.
5. Set "confidence" based on how directly the citations support the answer.

CRITICAL: Do not invent citations. If a fact is in observations but you cannot point at a specific file/line/snippet, leave it as a finding without a citation entry and keep the related sub-question open.

CRITICAL: Do not pad the ledger with irrelevant findings. Cull anything that doesn't materially help answer the original query.

Respond with EXACTLY this JSON shape (no markdown fences, no explanation outside the JSON):
`))
	b.WriteString("\n")
	b.WriteString(ledgerJSONSchema)
	return b.String()
}

// truncateLine collapses interior newlines to spaces and trims, then clips
// to maxRunes via the shared truncateRunes helper. The helper walks the
// string with `range` rather than allocating a []rune slice up front, which
// matters because reflection observations can be ~1.2KB.
func truncateLine(s string, maxRunes int) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	return truncateRunes(s, maxRunes)
}
