package tools

import (
	"regexp"
	"strings"
	"unicode/utf8"

	core "nudgebee/llm/tools/core"
)

// ThinkToolName is the registered name for the think tool.
const ThinkToolName = "think"

// Compile-time proof that thinkTool implements the escape-hatch interface.
// Without this, a typo in the receiver-method name would silently opt out.
var _ core.MissingFieldsResponder = (*thinkTool)(nil)

// thinkEmptyInputRejectionMsg is returned when the model calls think with
// no reasoning at all — either as an empty schema-required field or as an
// empty request.Command. Reused across Call() (direct-invocation defense)
// and OnMissingRequiredFields (planner-level path).
const thinkEmptyInputRejectionMsg = "think requires a non-empty 'reasoning' field naming the specific choice you are weighing " +
	"and the reason behind it (e.g. 'kubectl exec timed out. Retry with a longer timeout vs. " +
	"pivot to fetch_logs — pivoting because the pod itself may be unreachable'). " +
	"If you have no choice to weigh, do NOT call think — emit <final_answer> directly."

// thinkNarrationCoreRejectionMsg is the common narration reject text used
// by both Call() (when narration lands on Arguments["reasoning"]) and
// OnMissingRequiredFields (when narration lands on request.Command).
// The responder path appends thinkWrongFieldNudge; the Call() path uses
// this alone.
const thinkNarrationCoreRejectionMsg = "think rejected: input looks like narration of an upcoming action " +
	"(e.g. 'I am ready to provide the final answer', 'investigation is complete', " +
	"'notebook updated', 'I have enough information to answer'), not reasoning " +
	"that changes one. If your investigation is complete, emit <final_answer> " +
	"directly — do NOT call think to announce it. If you are weighing alternatives " +
	"(this-vs-that, retry-vs-pivot, fetch-X-or-Y), rewrite the reasoning to name " +
	"the specific choice you are making AND the reason behind it."

// thinkWrongFieldNudge is appended by OnMissingRequiredFields when
// narration text landed on request.Command instead of the required
// Arguments["reasoning"] slot — the 21/22 shape from the 7d 2026-07-08
// error window.
const thinkWrongFieldNudge = " Also — the reasoning belongs in the 'reasoning' field, not the top-level command string."

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
	`i have all (the )?(necessary|required|needed) (info|information|data|evidence|context)\b|` +
	// 2026-07-08 leak class (see project_tool_reliability_2026_07_02_batch memory):
	// swapped word order — the qualifier ("needed"/"required"/"necessary") lands
	// AFTER the noun. Original pattern only caught qualifier-BEFORE-noun order.
	// Example shape: "I have all the evidence needed", "I have the data needed
	// to provide the final answer". Uses the same noun set as the pre-swap
	// alternative above so behavior stays symmetric.
	`i have (all )?(the )?(info|information|data|evidence|context) (needed|required|necessary)\b|` +
	`i'?ve (gathered|confirmed|identified|verified)\b|` +
	`i am (now )?ready to (provide|give|finalize|emit|generate|write|format|conclude|answer|respond)\b|` +
	`i'?m (now )?ready to (provide|give|finalize|emit|generate|write|format|conclude|answer|respond)\b|` +
	`ready to (provide|give|finalize|format|emit) (the |a )?(final )?(answer|response|conclusion)\b|` +
	`(the )?investigation is complete\b|` +
	`notebook updated\b|` +
	`finaliz(e|ing) (the )?(investigation|answer|response)\b|` +
	// Trailing-space / word-boundary bug guard: all optional trailing groups
	// carry their space INSIDE (leading), not as a trailing character.
	// Original shape `(the |a |this |it )?(final )?(answer|...|into)?\b` could
	// terminate on a space (e.g. `format this ` when nothing follows), and Go's
	// RE2 does not fire `\b` between a space and a non-word char like `.`.
	// Consequence: bare phrases like `I will format this.` slipped past the
	// guard. New shape ends on a word char whenever an optional group
	// matches, so `\b` always fires. When ALL optional groups miss we anchor
	// on `format\b` directly. Regression cases in tool_think_test.go under
	// the "trailing space bug" name locks this.
	`i (will|am going to|can) (now )?(provide|generate|synthesize|finalize|write|emit|formulate|format)( (the|a|this|it))?( final)?( (answer|response|conclusion|report|summary|output|into))?\b|` +
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

// OnMissingRequiredFields intercepts the planner-level "missing required
// fields — reasoning" line so the model gets teachable feedback that
// specifically names the misuse. Two shapes seen 7d 2026-07-08:
//
//  1. Model sent narration text on Command (not Arguments["reasoning"]).
//     21/22 of the think error hits fell here. We run the narration guard
//     on whatever landed on Command; if it matches, we return the same
//     "input looks like narration" message the in-Call guard would have,
//     pointing at <final_answer>. If it's a real reasoning shape that
//     just landed on the wrong field, we nudge the model at the field
//     name.
//  2. Model sent no content at all. Return the empty-input rejection
//     that also points at <final_answer> for the no-choice case.
func (t *thinkTool) OnMissingRequiredFields(request core.NBToolCallRequest, _ []string) *core.NBToolResponse {
	candidate := strings.TrimSpace(request.Command)
	if candidate == "" {
		return &core.NBToolResponse{
			Data:   thinkEmptyInputRejectionMsg,
			Status: core.NBToolResponseStatusError,
		}
	}
	if isNarrationOnly(candidate) {
		return &core.NBToolResponse{
			Data:   thinkNarrationCoreRejectionMsg + thinkWrongFieldNudge,
			Status: core.NBToolResponseStatusError,
		}
	}
	return &core.NBToolResponse{
		Data: "think received text on the top-level command but the 'reasoning' field was empty. " +
			"Move the reasoning into the 'reasoning' field so the guardrails and downstream " +
			"consumers see it. Received text (first 200 chars): " + truncateForError(candidate, 200),
		Status: core.NBToolResponseStatusError,
	}
}

// truncateForError caps s at maxBytes, walking back to the nearest UTF-8
// rune boundary so the appended ellipsis doesn't glue onto a half-rune.
// Mirrors agents/core/scratchpad.go:TruncateHead — duplicated here to
// avoid a tools→agents/core import cycle.
func truncateForError(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes] + "…"
}

func (t *thinkTool) Call(_ core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	reasoning := input.Command
	if input.Arguments != nil {
		if r, ok := input.Arguments["reasoning"].(string); ok && r != "" {
			reasoning = r
		}
	}

	if strings.TrimSpace(reasoning) == "" {
		return core.NBToolResponse{
			Data:   thinkEmptyInputRejectionMsg,
			Status: core.NBToolResponseStatusError,
		}, nil
	}

	if isNarrationOnly(reasoning) {
		return core.NBToolResponse{
			Data:   thinkNarrationCoreRejectionMsg,
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
