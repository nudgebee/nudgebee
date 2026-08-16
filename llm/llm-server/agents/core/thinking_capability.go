package core

import (
	"regexp"
	"strconv"
	"strings"
)

// Thinking control across providers, in one table.
//
// Every provider now expresses reasoning depth as a qualitative LEVEL, and every
// numeric budget is documented as a target rather than a cap:
//
//   - Gemini 3:   thinking_level; thinking_budget is legacy back-compat. "Gemini 3
//     treats these levels as relative allowances for thinking rather
//     than strict token guarantees."
//   - Anthropic:  budget_tokens is deprecated on 4.6 and REJECTED (400) on 4.7+.
//     output_config.effort replaces it. "Effort is a behavioral signal,
//     not a strict token budget."
//   - OpenAI:     only ever had reasoning_effort.
//
// So the internal currency is the level (ThinkingLevel* in llm_common.go) and each
// model translates it at the wire boundary. A new model generation is a new row here,
// not a redesign — which is the point: the previous budget-shaped design breaks by
// construction when a model drops numeric budgets, as Anthropic 4.7 does.
//
// Matching is deliberately explicit rather than substring-prefixed. The prior
// heuristic (`strings.Contains(n, "flash-lite") -> no thinking`) was correct for
// gemini-2.5-flash-lite and silently wrong once gemini-3.5-flash-lite shipped, which
// does support levels and does think (~148 thinking tokens/call in production).
// An unknown model returns thinkingUnsupported: send nothing, let the provider
// default apply. That is the current behaviour for anything unrecognised, so a model
// we have not classified can never be made worse by this table.

// thinkingWireFormat is how a given model expects thinking control to be serialized.
type thinkingWireFormat int

const (
	// thinkingUnsupported: send no thinking configuration at all. Used for models
	// with no thinking support, and for families we have deliberately stopped
	// controlling (gemini < 3 — too old to be worth a second code path).
	thinkingUnsupported thinkingWireFormat = iota

	// thinkingGeminiLevel: genai ThinkingConfig.ThinkingLevel ("low"/"medium"/...).
	thinkingGeminiLevel

	// thinkingAnthropicBudget: thinking{type:"enabled", budget_tokens:N}, the legacy
	// extended-thinking mode. Only for Claude <= 4.5, where it is the ONLY mode.
	thinkingAnthropicBudget

	// thinkingAnthropicEffort: output_config{effort:"low"|...}. Claude 4.6+, on both
	// the direct API and Bedrock ("Platforms: Claude API, Claude Platform on AWS,
	// Amazon Bedrock, Google Cloud, Microsoft Foundry").
	thinkingAnthropicEffort

	// thinkingOpenAIEffort: reasoning_effort.
	thinkingOpenAIEffort
)

// thinkingCapability is what one model accepts.
type thinkingCapability struct {
	Format thinkingWireFormat
	// Allowed lists the level values this model accepts, weakest first. A requested
	// level outside this set is clamped to the nearest permitted neighbour rather
	// than dropped, so a caller asking for less thinking never accidentally gets more.
	Allowed []string
}

// Level sets, named so the intent is visible at each row.
var (
	levelsFull      = []string{ThinkingLevelMinimal, ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh}
	levelsNoMinimal = []string{ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh}
	// Retained for any model documented as low/high-only. gemini-3-pro-preview was the
	// documented example but now returns 404 ("no longer available") when probed live,
	// so this set is currently unused by any reachable model — kept because the shape
	// recurs and the next restricted model should be a row, not a redesign.
	levelsLowHigh = []string{ThinkingLevelLow, ThinkingLevelHigh}
	// Claude 4.6 supports max; xhigh is 4.7+/Opus-5 only. We never request either —
	// nothing in this service wants more reasoning than "high" — so they are omitted
	// deliberately rather than by oversight.
	levelsAnthropicEffort = []string{ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh}
)

// thinkingCapabilityFor resolves a model name to its thinking contract.
//
// Order matters: the most specific match wins, so gemini-3-pro-preview is classified
// before the generic gemini-3 family.
func thinkingCapabilityFor(model string) thinkingCapability {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return thinkingCapability{Format: thinkingUnsupported}
	}

	switch {
	// ---- Anthropic: the GENERATION decides the wire format ----
	// Parsed, not enumerated. Listing versions is how claude-sonnet-4-7 was missed once
	// already, and an unlisted claude-sonnet-4-9 would fall to a family prefix and be
	// sent budget_tokens — a hard 400 on 4.7+, not a soft failure.
	//
	//   >= 4.6   -> effort        (budget_tokens deprecated on 4.6, rejected on 4.7+)
	//   3.7-4.5  -> legacy budget (no effort parameter exists there)
	//   < 3.7    -> nothing we can express
	case strings.Contains(m, "claude"):
		major, minor, ok := anthropicGeneration(m)
		switch {
		case !ok:
			return thinkingCapability{Format: thinkingUnsupported}
		case major > 4 || (major == 4 && minor >= 6):
			return thinkingCapability{Format: thinkingAnthropicEffort, Allowed: levelsAnthropicEffort}
		case major > 3 || (major == 3 && minor >= 7):
			return thinkingCapability{Format: thinkingAnthropicBudget, Allowed: levelsFull}
		}
		return thinkingCapability{Format: thinkingUnsupported}

	// ---- Gemini 3.x: per-model level sets, from the published support table ----
	// Only the deprecated gemini-3-pro-preview is documented low/high-only. The broader
	// "gemini-3-pro" prefix would deny "medium" to every future Pro model for no
	// evidenced reason; those fall through to the Pro case below and keep medium.
	case strings.Contains(m, "gemini-3-pro-preview"):
		return thinkingCapability{Format: thinkingGeminiLevel, Allowed: levelsLowHigh}
	case strings.Contains(m, "gemini-3.7-flash"), strings.Contains(m, "gemini-3-7-flash"):
		return thinkingCapability{Format: thinkingGeminiLevel, Allowed: levelsNoMinimal}
	// Live-verified: MINIMAL returns 400 "Thinking level MINIMAL is not supported for
	// this model"; MEDIUM is accepted. The clamp turns a would-be 400 into "low".
	case strings.Contains(m, "gemini-3.1-pro"), strings.Contains(m, "gemini-3-1-pro"):
		return thinkingCapability{Format: thinkingGeminiLevel, Allowed: levelsNoMinimal}
	// Any other Gemini 3 Pro, including ones not yet released. Every Pro variant
	// documented so far excludes "minimal", and gemini-3.1-pro rejects it live with a
	// 400. Defaulting an unlisted Pro to levelsFull would hand it "minimal" and take
	// that 400; this errs toward the restrictive set, which the clamp resolves to "low"
	// rather than failing. Restricting is always safe here — it can only ever ask for
	// less thinking than requested, never more.
	case strings.Contains(m, "gemini-3") && strings.Contains(m, "pro"):
		return thinkingCapability{Format: thinkingGeminiLevel, Allowed: levelsNoMinimal}
	case strings.Contains(m, "gemini-3"):
		// Remaining 3.x (3-flash-preview, 3.5-flash, 3.5-flash-lite, 3.6-flash) all
		// document the full set. Note this INCLUDES flash-lite: it supports levels and
		// does think, contradicting the old blanket flash-lite exclusion.
		return thinkingCapability{Format: thinkingGeminiLevel, Allowed: levelsFull}

	// ---- Gemini < 3: deliberately uncontrolled ----
	// 2.5 and earlier are old enough that a second code path is not worth carrying,
	// and our own live observation (400 "Thinking level is not supported for this
	// model") conflicts with the current docs. Send nothing; the model's own default
	// applies. This drops the numeric budget we used to send to 2.5.
	case strings.Contains(m, "gemini"):
		return thinkingCapability{Format: thinkingUnsupported}

	// ---- OpenAI reasoning models ----
	case matchesAny(m, "gpt-5", "o1", "o3", "o4"):
		return thinkingCapability{Format: thinkingOpenAIEffort, Allowed: levelsNoMinimal}
	}

	return thinkingCapability{Format: thinkingUnsupported}
}

// clampLevel maps a requested level onto what the model actually accepts.
//
// Clamping to the nearest permitted neighbour, rather than dropping the level, keeps
// the caller's direction of intent: asking for "minimal" on a model whose floor is
// "low" yields "low", never the model's (higher) default.
func (c thinkingCapability) clampLevel(level string) string {
	if c.Format == thinkingUnsupported || len(c.Allowed) == 0 {
		return ""
	}
	want := strings.ToLower(strings.TrimSpace(level))
	if want == "" || want == ThinkingLevelNone {
		return ""
	}
	for _, a := range c.Allowed {
		if a == want {
			return want
		}
	}
	// Not permitted: fall to the closest allowed level on the same side.
	wantRank := levelRank(want)
	best, bestDist := "", 1<<30
	for _, a := range c.Allowed {
		d := levelRank(a) - wantRank
		if d < 0 {
			d = -d
		}
		// Ties prefer the weaker level: never silently buy more thinking than asked.
		if d < bestDist || (d == bestDist && levelRank(a) < levelRank(best)) {
			best, bestDist = a, d
		}
	}
	return best
}

// levelRank orders levels for clamping. Not exported: ordering is an implementation
// detail of the clamp, not a public concept.
func levelRank(level string) int {
	switch level {
	case ThinkingLevelNone:
		return 0
	case ThinkingLevelMinimal:
		return 1
	case ThinkingLevelLow:
		return 2
	case ThinkingLevelMedium:
		return 3
	case ThinkingLevelHigh:
		return 4
	}
	return 0
}

func matchesAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// clampThinkingLevel resolves a requested level against what the model accepts.
//
// Falls back to the legacy ClampThinkingLevelForModel for models with no capability
// entry, so this can be introduced without altering behaviour for anything outside
// the classified set. The legacy path is the one with the flash-lite defect; the
// table supersedes it only where a model is actually classified.
func clampThinkingLevel(model, level string) string {
	cap := thinkingCapabilityFor(model)
	// Only Gemini takes the table's clamp. The Anthropic and OpenAI rows exist so the
	// mapping is complete and reviewable, but nothing may consume them until their
	// adapters are wired — gating on `!= thinkingUnsupported` would have quietly routed
	// every Claude and GPT model through the new clamp, changing their behaviour while
	// this PR claims not to.
	if cap.Format != thinkingGeminiLevel {
		return ClampThinkingLevelForModel(model, level)
	}
	return cap.clampLevel(level)
}

// anthropicGeneration extracts major/minor from a Claude model id, across bare,
// vendor-prefixed and Bedrock shapes ("claude-sonnet-4-6", "anthropic/claude-...",
// "us.anthropic.claude-...-v1:0").
//
// The minor is bounded to two digits so a date suffix cannot be read as one:
// "claude-sonnet-4-20250514" is generation 4 with no minor, not 4.20250514. A missing
// minor reads as 0, so "claude-sonnet-5" is 5.0.
var (
	anthropicFamilyFirstRE  = regexp.MustCompile(`claude-[a-z]+-(\d+)(?:[-.](\d{1,2})\b)?`)
	anthropicVersionFirstRE = regexp.MustCompile(`claude-(\d+)[-.](\d{1,2})\b`)
)

func anthropicGeneration(m string) (major, minor int, ok bool) {
	// "claude-3-7-sonnet" puts the version first; check it before the family-first form,
	// whose [a-z]+ would otherwise fail to match the digits.
	if g := anthropicVersionFirstRE.FindStringSubmatch(m); g != nil {
		major, _ = strconv.Atoi(g[1])
		minor, _ = strconv.Atoi(g[2])
		return major, minor, true
	}
	if g := anthropicFamilyFirstRE.FindStringSubmatch(m); g != nil {
		major, _ = strconv.Atoi(g[1])
		if g[2] != "" {
			minor, _ = strconv.Atoi(g[2])
		}
		return major, minor, true
	}
	return 0, 0, false
}
