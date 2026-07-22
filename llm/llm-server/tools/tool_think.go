package tools

import (
	"regexp"
	"strings"

	core "nudgebee/llm/tools/core"
)

// ThinkToolName is the registered name for the think tool.
const ThinkToolName = "think"

// narrationStemRe matches the "I'm done" sentence stems the LLM emits when it
// is treating `think` as a status-update channel rather than as a
// decision-recording channel. Pattern set derived from two production samples
// (2026-06-22 pre-tighten baseline, 2026-06-27 post-#32697 recheck) — both
// showed ~70-90% narration share, identical shapes despite two rounds of
// prohibitive description language.
//
// The May 2026 rewrite (abstract prohibition) and the June 2026 rewrite
// (#32697, explicit phrasing list + hypothesis-mode redundancy callout) both
// reduced think *volume* without moving the misuse *share*. Description-only
// fixes do not work against this pattern; the model produces narration
// regardless of what the description says. This regex powers a Call()-level
// reject so the wasted reasoning cycle never reaches the planner.
// Note on `\b` placement (Gemini #33080 review): the trailing word boundary
// must be per-alternative, not on the closing group. A `)\b` after the whole
// alternation requires the char following the match to be a word char — but
// the `consolidated findings:` alternative ends with `:` (non-word), so the
// boundary after `:` is NOT a word boundary when followed by a space
// (`: ready...`). The fix is to attach `\b` only to alternatives that end with
// a word character, and leave `consolidated findings:` bare. Same pattern
// applies to the `vs.` alternative in decisionMarkerRe.
var narrationStemRe = regexp.MustCompile(`(?i)\b(` +
	`i have (enough|sufficient|gathered|confirmed|identified|verified|completed)\b|` +
	`i'?ve (gathered|confirmed|identified|verified)\b|` +
	`i am (now )?ready to (provide|give|finalize|emit|generate|write|format|conclude|answer|respond)\b|` +
	`i'?m (now )?ready to (provide|give|finalize|emit|generate|write|format|conclude|answer|respond)\b|` +
	`ready to (provide|give|finalize|format|emit) (the |a )?(final )?(answer|response|conclusion)\b|` +
	`(the )?investigation is complete\b|` +
	`notebook updated\b|` +
	`finaliz(e|ing) (the )?(investigation|answer|response)\b|` +
	`i (will|am going to|can) (now )?(provide|generate|synthesize|finalize|write|emit|formulate) (the |a )?(final )?(answer|response|conclusion)\b|` +
	`proceed to (the |a )?(final )?(answer|response|conclusion)\b|` +
	`consolidated findings:` +
	`)`)

// decisionMarkerRe matches phrases that indicate the reasoning is *weighing a
// choice* rather than announcing readiness. When narrationStemRe matches but
// decisionMarkerRe ALSO matches, the call is allowed — many legitimate think
// calls open with "I have searched X but found nothing" and pivot to a real
// decision ("will ask the user", "should I try Y instead"). The guard rejects
// only on narration-WITHOUT-decision-markers, biasing toward false-negatives
// (let some narration through) over false-positives (block real reasoning).
var decisionMarkerRe = regexp.MustCompile(`(?i)(` +
	`\?|` +
	`\beither\b|` +
	`\binstead\b|` +
	`\bvs\b\.?|` +
	`\bversus\b|` +
	`\bshould i\b|` +
	`\bconflict(s|ing)?\b|` +
	`\bcontradict(s|ing)?\b|` +
	`\balternative(ly|s)?\b|` +
	`\bbut\b|` +
	`\bhowever\b|` +
	`\bneed (more|to (check|verify|determine|decide|test|investigate|try|fetch|run|ask))\b` +
	`)`)

func init() {
	core.RegisterNBToolFactory(ThinkToolName, func(accountId string) (core.NBTool, error) {
		return &thinkTool{}, nil
	})
}

type thinkTool struct{}

func (t *thinkTool) Name() string             { return ThinkToolName }
func (t *thinkTool) GetType() core.NBToolType { return core.NBToolTypeTool }

func (t *thinkTool) Description() string {
	return "Record reasoning that changes your next action. " +
		"USE for: conflicting evidence to reconcile; stuck after 3+ tool calls; multiple root causes to weigh; tool error/empty result (decide retry, pivot, or honest failure). " +
		"A valid think call names a SPECIFIC choice (this-vs-that, retry-vs-pivot, fetch-X-or-Y-first) AND the reason behind it. If there is no choice being weighed, do not call this tool. " +
		"DO NOT use as a transition signal or status announcement — emit '<final_answer>' directly when you are ready, do not call 'think' to announce it. " +
		"Forbidden phrasings include: 'ready to provide (the) final answer', 'ready to give (the) answer', 'will provide (the) final answer', 'investigation is complete', 'notebook updated', 'i have enough information to answer', 'consolidated findings: ... ready to ...'. These narrate an upcoming action; they are not reasoning that changes it. " +
		"DO NOT restate prior findings. DO NOT replace a tool call. DO NOT use 'think' to record a notebook update — if you are in hypothesis-mode the notebook ('## Hypothesis Tree', '[SUPPORTED]'/'[REFUTED]' markers) is the structured place for that, and 'think' is redundant on top of it. " +
		"NOTE: narration-shape inputs are rejected at the tool boundary with an error explaining the misuse — the description above is the spec; the rejection is the enforcement. " +
		"Max 2 per investigation."
}

func (t *thinkTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{
		Type: core.ToolSchemaTypeObject,
		Properties: map[string]core.ToolSchemaProperty{
			"reasoning": {
				Type:        core.ToolSchemaTypeString,
				Description: "The conflict, stuck point, candidate root causes, or tool error you face — and the next action it leads to. Not for restating findings or announcing the final answer.",
			},
		},
		Required: []string{"reasoning"},
	}
}

func (t *thinkTool) Call(_ core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	reasoning := input.Command
	if input.Arguments != nil {
		if r, ok := input.Arguments["reasoning"].(string); ok && r != "" {
			reasoning = r
		}
	}

	if isNarrationOnly(reasoning) {
		return core.NBToolResponse{
			Data: "think rejected: input looks like narration of an upcoming action " +
				"(e.g. 'I am ready to provide the final answer', 'investigation is complete', " +
				"'notebook updated', 'I have enough information to answer'), not reasoning " +
				"that changes one. If your investigation is complete, emit <final_answer> " +
				"directly — do NOT call think to announce it. If you are weighing alternatives " +
				"(this-vs-that, retry-vs-pivot, fetch-X-or-Y), rewrite the reasoning to name " +
				"the specific choice you are making AND the reason behind it.",
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	return core.NBToolResponse{
		Data:   reasoning,
		Status: core.NBToolResponseStatusSuccess,
	}, nil
}

// isNarrationOnly returns true when the input matches the narration-stem
// pattern AND does NOT contain decision-language markers. Lowercase helper
// so the guard logic is unit-testable without going through Call().
//
// Empty input is intentionally NOT classified as narration — it has its own
// failure mode (the planner will catch missing-input separately).
func isNarrationOnly(reasoning string) bool {
	trimmed := strings.TrimSpace(reasoning)
	if trimmed == "" {
		return false
	}
	if !narrationStemRe.MatchString(trimmed) {
		return false
	}
	if decisionMarkerRe.MatchString(trimmed) {
		return false
	}
	return true
}
